// Package visual is helm-css's visual regression, in both themes, and the end
// to end check that the launcher's theme reaches a running studio's page
// (docs/design/04-packages.md §10; docs/decisions.md M6 Q9, Q16).
//
// It needs a Chrome. A missing browser fails the tests unless
// HELM_ALLOW_MISSING_BROWSER is set. Goldens are made with `make golden`,
// never by hand, and are tied to the Chrome major and the operating system
// recorded beside them.
package visual

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/chrome"
	helmcss "github.com/janishar/helmstudio/packages/helm-css"
	helmruntimenode "github.com/janishar/helmstudio/packages/helm-runtime-sdk/node"
	helmui "github.com/janishar/helmstudio/packages/helm-ui-sdk"
)

// EnvUpdate rewrites the goldens instead of comparing (make golden).
const EnvUpdate = "HELM_UPDATE_GOLDEN"

const goldenDir = "golden"

// A pixel differs when any channel moves by more than channelTolerance; a
// screenshot differs when more than maxDifferingFraction of its pixels do.
// Both are zero: with the flags internal/chrome launches with, repeated runs on
// one machine and one Chrome major rendered identically, so any change —
// down to one token moved by 1/255 in one channel — fails.
const (
	channelTolerance     = 0
	maxDifferingFraction = 0
)

var (
	browserOnce sync.Once
	browser     *chrome.Browser
	browserBin  string
	browserErr  error
)

// openBrowser starts one Chrome for the package, or fails (or skips, when
// allowed) the test.
func openBrowser(t *testing.T) *chrome.Browser {
	t.Helper()
	browserOnce.Do(func() {
		browserBin, browserErr = chrome.Find()
		if browserErr == nil {
			browser, browserErr = chrome.Launch(browserBin)
		}
	})
	if browserErr != nil {
		if os.Getenv(chrome.EnvAllowMissing) != "" {
			t.Skipf("%v; skipped because %s is set", browserErr, chrome.EnvAllowMissing)
		}
		// A skip is silent in go test's output, and the gate would stay green
		// without ever looking (the M4 second review #10 precedent).
		t.Fatalf("%v; visual regression needs a browser, or set %s=1 to skip on purpose", browserErr, chrome.EnvAllowMissing)
	}
	return browser
}

func TestMain(m *testing.M) {
	code := m.Run()
	if browser != nil {
		browser.Close()
	}
	os.Exit(code)
}

// fixtureServer serves the fixture pages, and everything the daemon serves
// them from: helm-css and the components under /sdk/v1/, and the launcher's
// own modules under /web/. The fixtures import the real files, so a golden is
// of the code that ships rather than of a copy that has drifted from it.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/fixtures/", http.StripPrefix("/fixtures/", http.FileServerFS(os.DirFS("fixtures"))))
	mux.Handle("/web/", http.StripPrefix("/web/", http.FileServerFS(os.DirFS(filepath.Join("..", "..", "web")))))
	mux.HandleFunc("/sdk/v1/{file...}", serveSDK)
	// helm-timeline's fixture sequence, laid out by the daemon's own rules: see
	// timeline_fixture_test.go.
	mountSequenceFixture(t, mux)
	// The editor's fixture talks to a real daemon: see daemon_test.go.
	srv := httptest.NewUnstartedServer(mux)
	mountDaemon(t, mux, srv)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// serveSDK mirrors internal/api's own /sdk/v1/ routing: helm-css at the root,
// the browser runtime under runtime/, the components under ui/, and the two
// shims that re-export them.
func serveSDK(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	var body []byte
	var err error
	switch {
	case name == "helm-ui.js":
		body = []byte(`export * from "./ui/index.js";` + "\n")
	case name == "helm-runtime.js":
		body = []byte(`export * from "./runtime/browser.js";` + "\n")
	case strings.HasPrefix(name, "ui/"):
		body, err = fs.ReadFile(helmui.Files, helmui.Served[strings.TrimPrefix(name, "ui/")])
	case strings.HasPrefix(name, "runtime/"):
		body, err = fs.ReadFile(helmruntimenode.Browser, helmruntimenode.BrowserFiles[strings.TrimPrefix(name, "runtime/")])
	default:
		body, err = fs.ReadFile(helmcss.Files, name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if strings.HasSuffix(name, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(body)
}

type shot struct {
	name    string
	fixture string // with its query, if it takes one
	theme   string // data-theme, or "" for the system preference
	system  string // emulated prefers-color-scheme
	width   int
	height  int // 0 for the default
}

// The launcher's screens (docs/decisions.md M6 Q2). Each is drawn from the
// canned data in fixtures/fake.js, at the three widths 03's layout collapses
// at, in both themes.
var screens = []string{"catalogue", "install", "processes", "gallery", "models", "settings", "switch", "add", "editor", "import", "approve"}

func shots() []shot {
	var out []shot
	for _, theme := range []string{"dark", "light"} {
		// The system preference opposes data-theme, so a golden only matches
		// when data-theme wins.
		opposite := map[string]string{"dark": "light", "light": "dark"}[theme]
		out = append(out,
			shot{"tokens-" + theme, "tokens.html", theme, opposite, 1100, 0},
			shot{"components-" + theme, "components.html", theme, opposite, 1100, 0},
		)
		out = append(out, shot{"ui-" + theme, "ui.html", theme, opposite, 900, 1400})
		for _, w := range []int{1280, 1000, 380} {
			out = append(out, shot{fmt.Sprintf("layout-%s-%d", theme, w), "layout.html", theme, opposite, w, 0})
			// helm-timeline, posed: the playhead inside the dissolve, a clip
			// selected so the inspector shows, and an export running.
			out = append(out, shot{fmt.Sprintf("timeline-%s-%d", theme, w), "timeline.html?export=running", theme, opposite, w, 0})
			for _, s := range screens {
				out = append(out, shot{
					name:    fmt.Sprintf("screen-%s-%s-%d", s, theme, w),
					fixture: "launcher.html?screen=" + s,
					theme:   theme, system: opposite, width: w,
				})
			}
		}
	}
	// No data-theme: the prefers-color-scheme block decides.
	out = append(out, shot{"tokens-system-light", "tokens.html", "", "light", 1100, 0})
	return out
}

// A pin is one fact about the environment the goldens were made in, recorded
// beside them. Text rasterisation changes with the browser major and with the
// operating system — macOS 27 moved every golden while Chrome stayed at 152 —
// so a screenshot is comparable only when both still match.
type pin struct {
	file    string // under the golden directory
	what    string // how a failure names it
	current string // what this machine has
	detail  string // the full version, for the message
}

// checkPins writes each pin under `make golden`, and otherwise fails when one
// no longer matches what is recorded.
func checkPins(dir string, update bool, pins ...pin) error {
	for _, p := range pins {
		file := filepath.Join(dir, p.file)
		if update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(file, []byte(p.current+"\n"), 0o644); err != nil {
				return err
			}
			continue
		}
		recorded, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("no goldens (%v); run make golden", err)
		}
		if was := strings.TrimSpace(string(recorded)); was != p.current {
			detail := p.detail
			if detail != "" {
				detail = " (" + detail + ")"
			}
			return fmt.Errorf("the goldens were made with %s %s and this is %s%s; fonts and antialiasing change between them, so regenerate deliberately with make golden and review the images",
				p.what, was, p.current, detail)
		}
	}
	return nil
}

// renderOS is the operating system the browser reports rendering on, as
// "macOS 27", with its full version for the message. It is read from the
// browser through User-Agent Client Hints rather than from Go, so that no file
// outside internal/platform makes an operating-system decision, and so that
// what is pinned is what the renderer itself sees. The hints need a secure
// context, which the fixture server on 127.0.0.1 is.
func renderOS(ctx context.Context, b *chrome.Browser, origin string) (name, version string, err error) {
	page, err := b.NewPage(ctx, 400, 300, "light")
	if err != nil {
		return "", "", err
	}
	defer page.Close()
	if err := page.Navigate(ctx, origin+"/fixtures/tokens.html"); err != nil {
		return "", "", err
	}
	var ua struct {
		Platform        string `json:"platform"`
		PlatformVersion string `json:"platformVersion"`
	}
	const expr = `navigator.userAgentData
		? navigator.userAgentData.getHighEntropyValues(["platformVersion"])
			.then(v => ({platform: v.platform, platformVersion: v.platformVersion}))
		: {platform: "", platformVersion: ""}`
	if err := page.Eval(ctx, expr, &ua); err != nil {
		return "", "", fmt.Errorf("asking the browser which operating system it renders on: %w", err)
	}
	if ua.Platform == "" {
		return "", "", fmt.Errorf("the browser reported no platform, so the goldens cannot be pinned to one; User-Agent Client Hints need a secure context")
	}
	name = ua.Platform
	if major, _, _ := strings.Cut(ua.PlatformVersion, "."); major != "" {
		name += " " + major
	}
	return name, ua.PlatformVersion, nil
}

func TestHelmCSSMatchesItsGoldensInBothThemes(t *testing.T) {
	b := openBrowser(t)
	srv := fixtureServer(t)
	major, version, err := chrome.Major(browserBin)
	if err != nil {
		t.Fatal(err)
	}
	pinCtx, cancelPin := context.WithTimeout(context.Background(), 30*time.Second)
	osName, osVersion, err := renderOS(pinCtx, b, srv.URL)
	cancelPin()
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv(EnvUpdate) != ""
	if err := checkPins(goldenDir, update,
		pin{"CHROME_MAJOR", "Chrome", strconv.Itoa(major), strings.TrimSpace(version)},
		pin{"OS_MAJOR", "the operating system", osName, osVersion},
	); err != nil {
		t.Fatal(err)
	}
	outDir := ""
	for _, s := range shots() {
		t.Run(s.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			height := s.height
			if height == 0 {
				height = 800
			}
			page, err := b.NewPage(ctx, s.width, height, s.system)
			if err != nil {
				t.Fatal(err)
			}
			url := srv.URL + "/fixtures/" + s.fixture
			if s.theme != "" {
				url += (map[bool]string{true: "&", false: "?"})[strings.Contains(s.fixture, "?")] + "theme=" + s.theme
			}
			if err := page.Navigate(ctx, url); err != nil {
				t.Fatal(err)
			}
			if err := page.WaitFor(ctx, `document.documentElement.dataset.ready && document.fonts.status === "loaded"`); err != nil {
				t.Fatal(err)
			}
			// A fixture that gave up waiting for itself says so, rather than
			// letting a half-drawn page be pinned as if it were correct.
			var ready string
			if err := page.Eval(ctx, `document.documentElement.dataset.ready`, &ready); err != nil {
				t.Fatal(err)
			}
			if ready != "1" {
				t.Fatalf("%s never settled (data-ready=%q); its fixture kept changing", s.name, ready)
			}
			var plex bool
			if err := page.Eval(ctx, `document.fonts.check('13px "IBM Plex Sans"') && document.fonts.check('12px "IBM Plex Mono"')`, &plex); err != nil || !plex {
				t.Fatalf("IBM Plex did not load from /sdk/v1/fonts (%v)", err)
			}
			got, err := page.Screenshot(ctx, s.width)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join(goldenDir, s.name+".png")
			if update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v; run make golden", err)
			}
			differ, total, diffImg, err := compare(want, got)
			if err != nil || float64(differ) > maxDifferingFraction*float64(total) {
				if outDir == "" {
					outDir, _ = os.MkdirTemp("", "helm-visual-")
				}
				_ = os.WriteFile(filepath.Join(outDir, s.name+".actual.png"), got, 0o644)
				if diffImg != nil {
					_ = os.WriteFile(filepath.Join(outDir, s.name+".diff.png"), diffImg, 0o644)
				}
				t.Errorf("%s differs from its golden: %d of %d pixels (%v); actual and diff in %s. If the change is intended, run make golden and review the images",
					s.name, differ, total, err, outDir)
			}
		})
	}
}

// compare counts the pixels that differ beyond the channel tolerance, and
// draws them red over a dimmed copy of the golden.
func compare(wantPNG, gotPNG []byte) (differ, total int, diff []byte, err error) {
	want, err := png.Decode(bytes.NewReader(wantPNG))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("decoding the golden: %w", err)
	}
	got, err := png.Decode(bytes.NewReader(gotPNG))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("decoding the screenshot: %w", err)
	}
	wb, gb := want.Bounds(), got.Bounds()
	if wb.Dx() != gb.Dx() || wb.Dy() != gb.Dy() {
		return wb.Dx() * wb.Dy(), wb.Dx() * wb.Dy(), nil, fmt.Errorf("size %dx%d, golden %dx%d", gb.Dx(), gb.Dy(), wb.Dx(), wb.Dy())
	}
	out := image.NewRGBA(wb)
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			wr, wg, wbl, _ := want.At(wb.Min.X+x, wb.Min.Y+y).RGBA()
			gr, gg, gbl, _ := got.At(gb.Min.X+x, gb.Min.Y+y).RGBA()
			if far(wr, gr) || far(wg, gg) || far(wbl, gbl) {
				differ++
				out.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				out.Set(x, y, color.RGBA{uint8(wr >> 10), uint8(wg >> 10), uint8(wbl >> 10), 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, out)
	return differ, wb.Dx() * wb.Dy(), buf.Bytes(), nil
}

func far(a, b uint32) bool {
	d := int(a>>8) - int(b>>8)
	return d > channelTolerance || d < -channelTolerance
}

// The pins must be able to fail, or an environment change reads as a pixel
// regression instead of what it is. A value that no longer matches names what
// was recorded, what this machine has, and what to run.
func TestPinsFailWhenTheEnvironmentMoved(t *testing.T) {
	dir := t.TempDir()
	made := []pin{
		{"CHROME_MAJOR", "Chrome", "152", "152.0.7977.83"},
		{"OS_MAJOR", "the operating system", "macOS 27", "27.0.0"},
	}
	if err := checkPins(dir, true, made...); err != nil {
		t.Fatalf("writing the pins: %v", err)
	}
	if err := checkPins(dir, false, made...); err != nil {
		t.Fatalf("the pins just written do not match: %v", err)
	}
	for _, moved := range []pin{
		{"CHROME_MAJOR", "Chrome", "153", "153.0.1.2"},
		{"OS_MAJOR", "the operating system", "macOS 28", "28.0.0"},
	} {
		changed := append([]pin(nil), made...)
		var was string
		for i := range changed {
			if changed[i].file == moved.file {
				was, changed[i] = changed[i].current, moved
			}
		}
		err := checkPins(dir, false, changed...)
		if err == nil {
			t.Fatalf("%s moved from %q to %q and the pins still passed", moved.file, was, moved.current)
		}
		for _, want := range []string{was, moved.current, "make golden"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the failure does not mention %q: %v", moved.file, want, err)
			}
		}
	}
	// A golden directory with nothing recorded is not silently accepted.
	if err := checkPins(t.TempDir(), false, made...); err == nil {
		t.Error("a golden directory with no pins passed")
	}
}

// The comparison must be able to fail: one token changed in the served CSS is
// caught on the tokens fixture in both themes.
func TestTheComparisonCatchesAOneTokenChange(t *testing.T) {
	b := openBrowser(t)
	css, err := helmcss.Files.ReadFile("helm.css")
	if err != nil {
		t.Fatal(err)
	}
	// border-strong moved by 1/255 in its blue channel, in one theme only.
	for theme, change := range map[string][2]string{
		"dark":  {"--helm-border-strong: #3b372e;", "--helm-border-strong: #3b372f;"},
		"light": {"--helm-border-strong: #c9c2b0;", "--helm-border-strong: #c9c2b1;"},
	} {
		t.Run(theme, func(t *testing.T) {
			from := change[0]
			mutated := bytes.Replace(css, []byte(from), []byte(change[1]), -1)
			if bytes.Equal(mutated, css) {
				t.Fatalf("helm.css has no %q to change", from)
			}
			mux := http.NewServeMux()
			mux.Handle("/fixtures/", http.StripPrefix("/fixtures/", http.FileServerFS(os.DirFS("fixtures"))))
			mux.HandleFunc("/sdk/v1/helm.css", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/css")
				w.Write(mutated)
			})
			mux.Handle("/sdk/v1/", http.StripPrefix("/sdk/v1/", http.FileServerFS(helmcss.Files)))
			srv := httptest.NewServer(mux)
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			page, err := b.NewPage(ctx, 1100, 800, map[string]string{"dark": "light", "light": "dark"}[theme])
			if err != nil {
				t.Fatal(err)
			}
			if err := page.Navigate(ctx, srv.URL+"/fixtures/tokens.html?theme="+theme); err != nil {
				t.Fatal(err)
			}
			if err := page.WaitFor(ctx, `document.documentElement.dataset.ready === "1"`); err != nil {
				t.Fatal(err)
			}
			got, err := page.Screenshot(ctx, 1100)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(goldenDir, "tokens-"+theme+".png"))
			if err != nil {
				t.Fatalf("%v; run make golden", err)
			}
			differ, total, _, err := compare(want, got)
			if err == nil && float64(differ) <= maxDifferingFraction*float64(total) {
				t.Errorf("%s changed to %s moved %d of %d pixels, within tolerance; the check would not catch a token change", from, change[1], differ, total)
			}
		})
	}
}
