package conformance

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// The same-origin proxy exists three times — Go, Python, Node — and a studio
// page's safety rests on each (docs/decisions.md M6 Q10). Every case here runs
// against all three.

type proxyImpl struct {
	name  string
	start func(t *testing.T, env map[string]string) string // base URL
}

var proxyImpls = []proxyImpl{
	{"go", func(t *testing.T, env map[string]string) string {
		srv := httptest.NewServer(helm.Proxy(helm.ProxyConfig{API: env["HELM_API"], Token: env["HELM_TOKEN"], SDKBase: env["HELM_SDK_BASE"],
			AccentDark: env["HELM_ACCENT_DARK"], AccentLight: env["HELM_ACCENT_LIGHT"]}))
		t.Cleanup(srv.Close)
		return srv.URL
	}},
	{"python", func(t *testing.T, env map[string]string) string {
		return startProxyProcess(t, "python3", filepath.Join("..", "..", "packages", "helm-runtime-sdk", "python", "tests", "proxy_server.py"), env)
	}},
	{"node", func(t *testing.T, env map[string]string) string {
		return startProxyProcess(t, "node", filepath.Join("..", "..", "packages", "helm-runtime-sdk", "node", "test", "proxy_server.mjs"), env)
	}},
}

func startProxyProcess(t *testing.T, tool, script string, env map[string]string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		if os.Getenv("HELM_ALLOW_MISSING_CLIENTS") == "" {
			t.Fatalf("%s is not installed, so its proxy cannot be checked; install it, or set HELM_ALLOW_MISSING_CLIENTS=1 to skip on purpose", tool)
		}
		t.Skipf("%s is not installed; skipped because HELM_ALLOW_MISSING_CLIENTS is set", tool)
	}
	cmd := exec.Command(path, script)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		if sc.Scan() {
			line <- sc.Text()
		}
		io.Copy(io.Discard, out)
	}()
	select {
	case l := <-line:
		port, ok := strings.CutPrefix(l, "listening ")
		if !ok {
			t.Fatalf("%s proxy said %q", tool, l)
		}
		return "http://127.0.0.1:" + port
	case <-time.After(15 * time.Second):
		t.Fatalf("%s proxy did not start", tool)
		return ""
	}
}

// recorder is an upstream that remembers what reached it.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	method, path, query string
	header              http.Header
}

func (rec *recorder) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone()})
		rec.mu.Unlock()
		w.Header().Set("Set-Cookie", "daemon=1")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (rec *recorder) take() []recorded {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := rec.reqs
	rec.reqs = nil
	return out
}

type contractOp struct{ method, path, tag string }

// contractOps reads every operation's path, method and tag from
// api/openapi.yaml, whose layout is fixed: a path at two spaces, a method at
// four, its tags at six.
func contractOps(t *testing.T) []contractOp {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	pathLine := regexp.MustCompile(`^  (/\S*):\s*$`)
	verbLine := regexp.MustCompile(`^    (get|put|post|patch|delete|head):\s*$`)
	tagLine := regexp.MustCompile(`^      tags: \[(\S+)\]\s*$`)
	var ops []contractOp
	inPaths := false
	path, verb := "", ""
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case l == "paths:":
			inPaths = true
		case inPaths && l == "components:":
			inPaths = false
		case !inPaths:
		case pathLine.MatchString(l):
			path = pathLine.FindStringSubmatch(l)[1]
		case verbLine.MatchString(l):
			verb = strings.ToUpper(verbLine.FindStringSubmatch(l)[1])
		case tagLine.MatchString(l) && verb != "":
			ops = append(ops, contractOp{verb, regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "x1"), tagLine.FindStringSubmatch(l)[1]})
			verb = ""
		}
	}
	return ops
}

// A page reaches the studio API and the theme through its proxy, with the
// studio's token added and its own credentials dropped, and reaches no
// launcher operation at all.
func TestProxiesForwardOnlyTheStudioAPIAndTheTheme(t *testing.T) {
	ops := contractOps(t)
	count := map[string]int{}
	for _, op := range ops {
		count[op.tag]++
	}
	if count["studio-api"] < 30 || count["launcher"] < 15 || count["public"] != 2 {
		t.Fatalf("read %v operations from api/openapi.yaml; the layout did not parse", count)
	}
	rec := &recorder{}
	up := rec.server(t)
	env := map[string]string{"HELM_API": up.URL + "/api/v1", "HELM_TOKEN": "hs_live_proxytest", "HELM_SDK_BASE": up.URL + "/sdk/v1"}
	for _, impl := range proxyImpls {
		t.Run(impl.name, func(t *testing.T) {
			base := impl.start(t, env)
			rec.take()
			for _, op := range ops {
				req, _ := http.NewRequest(op.method, base+"/helm/api/v1"+op.path+"?limit=1", strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer page-supplied")
				req.Header.Set("Cookie", "page=1")
				req.Header.Set("Origin", "http://127.0.0.1:8710")
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("%s %s: %v", op.method, op.path, err)
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				got := rec.take()
				if strings.Contains(string(body), "hs_live_") || res.Header.Get("Set-Cookie") != "" {
					t.Errorf("%s %s: the page saw %q, Set-Cookie %q", op.method, op.path, body, res.Header.Get("Set-Cookie"))
				}
				if op.tag == "launcher" {
					if res.StatusCode != http.StatusNotFound || len(got) != 0 {
						t.Errorf("launcher %s %s through the proxy: %d, reached the daemon %d times", op.method, op.path, res.StatusCode, len(got))
					}
					continue
				}
				if len(got) != 1 {
					t.Errorf("%s %s %s: reached the daemon %d times, want once (status %d %s)", op.tag, op.method, op.path, len(got), res.StatusCode, body)
					continue
				}
				g := got[0]
				if g.method != op.method || g.path != "/api/v1"+op.path || g.query != "limit=1" {
					t.Errorf("%s %s arrived as %s %s?%s", op.method, op.path, g.method, g.path, g.query)
				}
				if a := g.header.Get("Authorization"); a != "Bearer hs_live_proxytest" {
					t.Errorf("%s %s: Authorization %q, want the studio's token", op.method, op.path, a)
				}
				for _, h := range []string{"Cookie", "Origin", "Referer"} {
					if g.header.Get(h) != "" {
						t.Errorf("%s %s: the page's %s reached the daemon", op.method, op.path, h)
					}
				}
			}
			for _, p := range []string{"/helm/api/v1/kv/../studios", "/helm/api/v1/kv/%2e%2e/studios", "/helm/api/v1/kv%2f..%2fstudios", "/helm/other", "/helm/sdk/v2/helm.css"} {
				res, err := http.Get(base + p)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if got := rec.take(); res.StatusCode != 404 || len(got) != 0 {
					t.Errorf("%s: %d, reached the daemon %d times", p, res.StatusCode, len(got))
				}
			}
			res, err := http.Get(base + "/helm/sdk/v1/helm.css")
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if got := rec.take(); len(got) != 1 || got[0].path != "/sdk/v1/helm.css" || got[0].header.Get("Authorization") != "" {
				t.Errorf("SDK file: %+v; want one request without a token", got)
			}
		})
	}
}

// All three serve the same accent.css.
func TestProxiesServeTheSameAccentCSS(t *testing.T) {
	want, _ := helm.AccentCSS("#e0a33c", "#5c4bc4")
	for _, impl := range proxyImpls {
		t.Run(impl.name, func(t *testing.T) {
			base := impl.start(t, map[string]string{"HELM_ACCENT_DARK": "#E0A33C", "HELM_ACCENT_LIGHT": "#5c4bc4"})
			res, err := http.Get(base + "/helm/accent.css")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") || string(body) != want {
				t.Errorf("accent.css: %d %q\n%s\nwant\n%s", res.StatusCode, res.Header.Get("Content-Type"), body, want)
			}
			if res, err := http.Get(base + "/helm/api/v1/me"); err != nil || res.StatusCode != 404 {
				t.Errorf("with no HELM_API, /helm/api/v1/me = %v %v, want 404", res, err)
			}
		})
	}
}

// Against the real daemon: a studio call works with the studio's own token,
// and the theme stream comes through and follows a change.
func TestProxiesAgainstTheDaemon(t *testing.T) {
	e := openDaemon(t)
	for _, impl := range proxyImpls {
		t.Run(impl.name, func(t *testing.T) {
			base := impl.start(t, map[string]string{"HELM_API": e.URL + "/api/v1", "HELM_TOKEN": e.Tokens[S], "HELM_SDK_BASE": e.URL + "/sdk/v1"})
			res, err := http.Get(base + "/helm/api/v1/me")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 200 || !strings.Contains(string(body), `"studio_id":"`+S+`"`) || strings.Contains(string(body), "hs_live_") {
				t.Fatalf("/me through the proxy: %d %s", res.StatusCode, body)
			}
			css, err := http.Get(base + "/helm/sdk/v1/helm-tokens.css")
			if err != nil {
				t.Fatal(err)
			}
			cssBody, _ := io.ReadAll(css.Body)
			css.Body.Close()
			if css.StatusCode != 200 || !strings.Contains(string(cssBody), "--helm-ground-page") {
				t.Errorf("helm-tokens.css through the proxy: %d", css.StatusCode)
			}

			stream, err := http.Get(base + "/helm/api/v1/theme/events")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			data := make(chan string, 8)
			go func() {
				sc := bufio.NewScanner(stream.Body)
				for sc.Scan() {
					if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
						data <- d
					}
				}
			}()
			next := func() string {
				select {
				case d := <-data:
					return d
				case <-time.After(10 * time.Second):
					t.Fatal("no theme event through the proxy within 10s")
					return ""
				}
			}
			first := next()
			want := `{"theme":"dark"}`
			if first == want {
				want = `{"theme":"light"}`
			}
			theme := strings.TrimSuffix(strings.TrimPrefix(want, `{"theme":"`), `"}`)
			req, _ := http.NewRequest("PUT", e.URL+"/api/v1/launcher/settings/theme", strings.NewReader(`{"theme":"`+theme+`"}`))
			put, err := http.DefaultClient.Do(req)
			if err != nil || put.StatusCode != 200 {
				t.Fatalf("setting the theme: %v %v", put, err)
			}
			put.Body.Close()
			if got := next(); got != want {
				t.Errorf("after setting %s the stream said %s", theme, got)
			}
		})
	}
}
