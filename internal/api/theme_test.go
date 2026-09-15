package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// themeServer is the daemon's HTTP surface built as cmd/helmstudio builds it,
// with one studio holding a token.
func themeServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: d, API: "http://" + ts.Listener.Addr().String() + studioapi.Base, Logf: t.Logf})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Platform: plat.Launches})
	sup.SetStudios([]supervisor.Studio{{Manifest: &manifest.Manifest{ID: "listener", Name: "listener", Capabilities: []string{"kv"}}}})
	svc, h := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
	srv, err := New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf, WithStudioAPI(h, svc))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv
	ts.Start()
	tok, err := plat.Tokens.Mint(ctx, "listener", store.NewID(time.Now()), []string{"kv"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		svc.Events().Shutdown("test over")
		ts.CloseClientConnections()
		ts.Close()
		cancel()
		st.Close()
	})
	return ts, tok
}

func request(t *testing.T, method, url, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

// sseData reads `data:` lines of the named event from a stream.
func sseData(t *testing.T, res *http.Response, event string) <-chan string {
	out := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(res.Body)
		name := ""
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "event: "):
				name = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: ") && name == event:
				out <- strings.TrimPrefix(l, "data: ")
			case l == "":
				name = ""
			}
		}
	}()
	return out
}

func within(t *testing.T, c <-chan string) string {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived within 5s")
		return ""
	}
}

func TestThemeSettingIsReadSetAndPublished(t *testing.T) {
	ts, tok := themeServer(t)
	base := ts.URL + Base

	if res, body := request(t, "GET", base+"/launcher/settings/theme", "", nil); res.StatusCode != 200 || strings.TrimSpace(body) != `{"theme":"system"}` {
		t.Fatalf("initial theme: %d %s", res.StatusCode, body)
	}

	// A tokenless theme stream and a studio's /events both hear the change.
	streamRes, err := http.Get(base + "/theme/events")
	if err != nil {
		t.Fatal(err)
	}
	defer streamRes.Body.Close()
	stream := sseData(t, streamRes, "theme")
	if v := within(t, stream); v != `{"theme":"system"}` {
		t.Fatalf("stream opened with %s", v)
	}
	evReq, _ := http.NewRequest("GET", base+"/events", nil)
	evReq.Header.Set("Authorization", "Bearer "+tok)
	evRes, err := http.DefaultClient.Do(evReq)
	if err != nil || evRes.StatusCode != 200 {
		t.Fatalf("events: %v %v", evRes, err)
	}
	defer evRes.Body.Close()
	events := sseData(t, evRes, "theme")
	time.Sleep(100 * time.Millisecond) // let the /events subscription register

	if res, body := request(t, "PUT", base+"/launcher/settings/theme", `{"theme":"light"}`, nil); res.StatusCode != 200 || strings.TrimSpace(body) != `{"theme":"light"}` {
		t.Fatalf("PUT light: %d %s", res.StatusCode, body)
	}
	if v := within(t, stream); v != `{"theme":"light"}` {
		t.Errorf("theme stream got %s", v)
	}
	if v := within(t, events); v != `{"theme":"light"}` {
		t.Errorf("/events got %s", v)
	}
	if res, body := request(t, "GET", base+"/theme", "", nil); res.StatusCode != 200 || strings.TrimSpace(body) != `{"theme":"light"}` {
		t.Errorf("GET /theme: %d %s", res.StatusCode, body)
	}

	for _, c := range []struct {
		body string
		want int
		code string
	}{{`{"theme":"sepia"}`, 422, "invalid_theme"}, {`{"theme":"dark","x":1}`, 400, "bad_request"}, {`nope`, 400, "bad_request"}} {
		if res, body := request(t, "PUT", base+"/launcher/settings/theme", c.body, nil); res.StatusCode != c.want || !strings.Contains(body, c.code) {
			t.Errorf("PUT %s: %d %s, want %d %s", c.body, res.StatusCode, body, c.want, c.code)
		}
	}
}

// Only the SDK files and the theme GETs are exempt from the Origin check, and
// a foreign Host is still refused on them.
func TestOnlyTheThemeAndSDKFilesAcceptAStudioPagesOrigin(t *testing.T) {
	ts, _ := themeServer(t)
	studioPage := map[string]string{"Origin": "http://127.0.0.1:8710"}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/sdk/v1/helm.css", 200},
		{"HEAD", "/sdk/v1/helm-tokens.css", 200},
		{"GET", Base + "/theme", 200},
		{"PUT", Base + "/launcher/settings/theme", 403},
		{"GET", Base + "/launcher/settings/theme", 403},
		{"POST", "/sdk/v1/helm.css", 403},
		{"GET", Base + "/studios", 403},
		{"GET", Base + "/theme/other", 403},
	} {
		res, body := request(t, c.method, ts.URL+c.path, `{"theme":"dark"}`, studioPage)
		if res.StatusCode != c.want {
			t.Errorf("%s %s from a studio's page = %d, want %d: %s", c.method, c.path, res.StatusCode, c.want, body)
		}
		exempt := c.want == 200
		if got := res.Header.Get("Access-Control-Allow-Origin"); (got == "*") != exempt {
			t.Errorf("%s %s: Access-Control-Allow-Origin %q", c.method, c.path, got)
		}
	}
	req, _ := http.NewRequest("GET", ts.URL+"/sdk/v1/helm.css", nil)
	req.Host = "evil.example:8700"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("a rebound name reached /sdk/: %d", res.StatusCode)
	}
}

func TestSDKFilesAreServed(t *testing.T) {
	ts, _ := themeServer(t)
	for _, c := range []struct {
		path, ctype, contains string
	}{
		{"/sdk/v1/helm.css", "text/css; charset=utf-8", "--helm-ground-page"},
		{"/sdk/v1/helm.min.css", "text/css; charset=utf-8", "--helm-accent-base"},
		{"/sdk/v1/tokens.json", "application/json", `"--helm-text-primary"`},
		{"/sdk/v1/fonts/IBMPlexSans-Regular-Latin1.woff2", "font/woff2", "wOF2"},
		{"/sdk/v1/fonts/LICENSE.txt", "text/plain; charset=utf-8", "SIL OPEN FONT LICENSE"},
		{"/sdk/v1/helm-runtime.js", "text/javascript; charset=utf-8", `export * from "./runtime/browser.js"`},
		{"/sdk/v1/runtime/browser.js", "text/javascript; charset=utf-8", "export function connect"},
		{"/sdk/v1/runtime/theme.js", "text/javascript; charset=utf-8", "export function themeBridge"},
	} {
		res, body := request(t, "GET", ts.URL+c.path, "", nil)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != c.ctype || !strings.Contains(body, c.contains) {
			t.Errorf("%s: %d %q, body has %q: %v", c.path, res.StatusCode, res.Header.Get("Content-Type"), c.contains, strings.Contains(body, c.contains))
		}
	}
	for _, p := range []string{"/sdk/v1/runtime/proxy.js", "/sdk/v1/runtime/index.js", "/sdk/v1/helmcss.go", "/sdk/v1/fonts/../helmcss.go", "/sdk/v1/nope.css", "/sdk/v2/helm.css"} {
		if res, _ := request(t, "GET", ts.URL+p, "", nil); res.StatusCode != 404 {
			t.Errorf("%s = %d, want 404", p, res.StatusCode)
		}
	}
}
