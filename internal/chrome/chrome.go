// Package chrome drives an installed Chrome for tests: open a page at a size,
// evaluate JavaScript, wait for a condition, take a PNG screenshot. It speaks
// the DevTools protocol over --remote-debugging-pipe with the standard library
// only, so the gate needs a browser and no module (docs/decisions.md M6 Q16).
//
// It is test tooling. It never navigates anywhere a test did not pass it, and
// every browser runs with its own temporary profile.
package chrome

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// EnvChrome names the browser binary; EnvAllowMissing lets a test skip when
// none is found instead of failing.
const (
	EnvChrome       = "HELM_CHROME"
	EnvAllowMissing = "HELM_ALLOW_MISSING_BROWSER"
)

// ErrNotFound means no browser binary was found.
var ErrNotFound = errors.New("chrome: no browser found; install Google Chrome or set " + EnvChrome)

// candidates are where Chrome usually lives when HELM_CHROME is not set.
var candidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
}

// Find returns the browser binary to use.
func Find() (string, error) {
	if p := os.Getenv(EnvChrome); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("chrome: %s=%s: %w", EnvChrome, p, err)
		}
		return p, nil
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Major is the browser's major version, from --version.
func Major(binary string) (int, string, error) {
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return 0, "", fmt.Errorf("chrome: %s --version: %w", binary, err)
	}
	m := regexp.MustCompile(`(\d+)\.\d+\.\d+`).FindStringSubmatch(string(out))
	if m == nil {
		return 0, string(out), fmt.Errorf("chrome: no version in %q", out)
	}
	n, _ := strconv.Atoi(m[1])
	return n, string(out), nil
}

// Browser is one running headless Chrome.
type Browser struct {
	cmd     *exec.Cmd
	profile string
	w       *os.File
	mu      sync.Mutex
	id      int
	waiting map[int]chan message
	done    chan struct{}
}

type message struct {
	ID        int                       `json:"id"`
	Method    string                    `json:"method"`
	Result    json.RawMessage           `json:"result"`
	Error     *struct{ Message string } `json:"error"`
	SessionID string                    `json:"sessionId"`
}

// Launch starts a headless browser with rendering flags chosen for stable
// screenshots: sRGB, no LCD text, no GPU, no hinting.
func Launch(binary string) (*Browser, error) {
	profile, err := os.MkdirTemp("", "helm-chrome-")
	if err != nil {
		return nil, err
	}
	toChrome, ourEnd, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromChrome, chromeEnd, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "--headless=new", "--remote-debugging-pipe", "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "--hide-scrollbars", "--mute-audio",
		"--disable-extensions", "--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--force-color-profile=srgb", "--font-render-hinting=none", "--disable-lcd-text", "--disable-font-subpixel-positioning",
		"about:blank")
	cmd.ExtraFiles = []*os.File{toChrome, chromeEnd} // fd 3: commands in, fd 4: messages out
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, fmt.Errorf("chrome: starting %s: %w", binary, err)
	}
	toChrome.Close()
	chromeEnd.Close()
	b := &Browser{cmd: cmd, profile: profile, w: ourEnd, waiting: map[int]chan message{}, done: make(chan struct{})}
	go b.read(fromChrome)
	return b, nil
}

func (b *Browser) read(r *os.File) {
	defer close(b.done)
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		raw, err := br.ReadBytes(0)
		if err != nil {
			b.mu.Lock()
			for id, c := range b.waiting {
				close(c)
				delete(b.waiting, id)
			}
			b.mu.Unlock()
			return
		}
		var m message
		if json.Unmarshal(raw[:len(raw)-1], &m) != nil || m.ID == 0 {
			continue
		}
		b.mu.Lock()
		c := b.waiting[m.ID]
		delete(b.waiting, m.ID)
		b.mu.Unlock()
		if c != nil {
			c <- m
		}
	}
}

func (b *Browser) call(ctx context.Context, session, method string, params any, out any) error {
	b.mu.Lock()
	b.id++
	id := b.id
	c := make(chan message, 1)
	b.waiting[id] = c
	req := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		req["sessionId"] = session
	}
	raw, _ := json.Marshal(req)
	_, err := b.w.Write(append(raw, 0))
	b.mu.Unlock()
	if err != nil {
		return fmt.Errorf("chrome: %s: %w", method, err)
	}
	select {
	case m, ok := <-c:
		if !ok {
			return fmt.Errorf("chrome: %s: the browser exited", method)
		}
		if m.Error != nil {
			return fmt.Errorf("chrome: %s: %s", method, m.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
		return fmt.Errorf("chrome: %s: %w", method, ctx.Err())
	}
}

// Close ends the browser and removes its profile.
func (b *Browser) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.call(ctx, "", "Browser.close", nil, nil)
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
		_ = b.cmd.Process.Kill()
	}
	_ = b.cmd.Wait()
	b.w.Close()
	os.RemoveAll(b.profile)
}

// Page is one tab.
type Page struct {
	b        *Browser
	session  string
	targetID string
}

// Close closes the tab, ending anything it still has open, such as an event
// stream a server would otherwise wait on.
func (p *Page) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.b.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": p.targetID}, nil)
}

// NewPage opens a tab with a fixed viewport and colour-scheme preference
// ("light", "dark", or "" for none).
func (b *Browser) NewPage(ctx context.Context, width, height int, colorScheme string) (*Page, error) {
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := b.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return nil, err
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &att); err != nil {
		return nil, err
	}
	p := &Page{b: b, session: att.SessionID, targetID: target.TargetID}
	if err := p.Resize(ctx, width, height); err != nil {
		return nil, err
	}
	if colorScheme != "" {
		if err := b.call(ctx, p.session, "Emulation.setEmulatedMedia", map[string]any{
			"features": []map[string]string{{"name": "prefers-color-scheme", "value": colorScheme}, {"name": "prefers-reduced-motion", "value": "reduce"}},
		}, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Resize sets the viewport at scale 1.
func (p *Page) Resize(ctx context.Context, width, height int) error {
	return p.b.call(ctx, p.session, "Emulation.setDeviceMetricsOverride",
		map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false}, nil)
}

// Navigate loads url and waits for its load event and its fonts.
func (p *Page) Navigate(ctx context.Context, url string) error {
	if err := p.b.call(ctx, p.session, "Page.navigate", map[string]any{"url": url}, nil); err != nil {
		return err
	}
	want, _ := json.Marshal(url)
	return p.WaitFor(ctx, `location.href === `+string(want)+` && document.readyState === "complete" && document.fonts.status === "loaded"`)
}

// Eval evaluates expression, awaiting a promise, and decodes its value into out.
func (p *Page) Eval(ctx context.Context, expression string, out any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := p.b.call(ctx, p.session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil {
			msg = res.ExceptionDetails.Exception.Description
		}
		return fmt.Errorf("chrome: evaluating %q: %s", expression, msg)
	}
	if out != nil && len(res.Result.Value) > 0 {
		return json.Unmarshal(res.Result.Value, out)
	}
	return nil
}

// WaitFor polls a boolean expression until it is true or ctx ends.
func (p *Page) WaitFor(ctx context.Context, expression string) error {
	for {
		var ok bool
		err := p.Eval(ctx, "Boolean("+expression+")", &ok)
		if err == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("chrome: waiting for %s: %w (last error: %v)", expression, ctx.Err(), err)
			}
			return fmt.Errorf("chrome: waiting for %s: %w", expression, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Screenshot captures the whole page as PNG, growing the viewport to the
// content's height first.
func (p *Page) Screenshot(ctx context.Context, width int) ([]byte, error) {
	var height float64
	if err := p.Eval(ctx, `Math.ceil(document.documentElement.scrollHeight)`, &height); err != nil {
		return nil, err
	}
	if err := p.Resize(ctx, width, int(height)); err != nil {
		return nil, err
	}
	// Let layout settle at the new size.
	if err := p.Eval(ctx, `new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => r(true))))`, nil); err != nil {
		return nil, err
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := p.b.call(ctx, p.session, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true}, &shot); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(shot.Data)
}
