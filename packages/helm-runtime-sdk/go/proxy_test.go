package helm

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Clone(r.Context()))
}

func (s *seen) last() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		return nil
	}
	return s.reqs[len(s.reqs)-1]
}

func (s *seen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func upstream(t *testing.T) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		if strings.HasSuffix(r.URL.Path, "/theme/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			rc := http.NewResponseController(w)
			io.WriteString(w, "event: theme\ndata: {\"theme\":\"dark\"}\n\n")
			rc.Flush()
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=1")
		w.Header().Set("Etag", `"e1"`)
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func TestProxyForwardsStudioAPIWithTheToken(t *testing.T) {
	up, got := upstream(t)
	p := httptest.NewServer(Proxy(ProxyConfig{API: up.URL + "/api/v1", Token: "hs_live_secret", SDKBase: up.URL + "/sdk/v1"}))
	defer p.Close()

	req, _ := http.NewRequest("PATCH", p.URL+"/helm/api/v1/kv/ui/panel?x=1", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("If-Match", `"e0"`)
	req.Header.Set("Authorization", "Bearer page-token")
	req.Header.Set("Cookie", "page=1")
	req.Header.Set("Origin", "http://127.0.0.1:8710")
	req.Header.Set("Referer", "http://127.0.0.1:8710/")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != `{"path":"/api/v1/kv/ui/panel"}` {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
	r := got.last()
	if r.Method != "PATCH" || r.URL.RawQuery != "x=1" || r.Header.Get("Authorization") != "Bearer hs_live_secret" ||
		r.Header.Get("If-Match") != `"e0"` || r.Header.Get("Content-Type") != "application/merge-patch+json" {
		t.Errorf("upstream saw %s %s %v", r.Method, r.URL, r.Header)
	}
	for _, h := range []string{"Cookie", "Origin", "Referer"} {
		if r.Header.Get(h) != "" {
			t.Errorf("the page's %s reached the daemon: %q", h, r.Header.Get(h))
		}
	}
	if res.Header.Get("Set-Cookie") != "" || res.Header.Get("X-Upstream") != "" || res.Header.Get("Etag") != `"e1"` {
		t.Errorf("response headers to the page: %v", res.Header)
	}
}

func TestProxyRefusesEverythingElseWithoutReachingTheDaemon(t *testing.T) {
	up, got := upstream(t)
	p := httptest.NewServer(Proxy(ProxyConfig{API: up.URL + "/api/v1", Token: "hs_live_secret", SDKBase: up.URL + "/sdk/v1"}))
	defer p.Close()
	for _, c := range []struct{ method, path string }{
		{"POST", "/helm/api/v1/studios/h3-studio:stop"},
		{"POST", "/helm/api/v1/assets:reclaim"},
		{"GET", "/helm/api/v1/launcher/jobs"},
		{"GET", "/helm/api/v1/models"},
		{"GET", "/helm/api/v1/"},
		{"GET", "/helm/api/v1/kv/../studios"},
		{"GET", "/helm/api/v1/kv/%2e%2e/studios"},
		{"GET", "/helm/api/v1/kv%2F..%2Fstudios"},
		{"GET", "/helm/api/v2/me"},
		{"POST", "/helm/sdk/v1/helm.css"},
		{"GET", "/helm/other"},
		{"GET", "/helm/accent.css"}, // no hue configured
	} {
		req, _ := http.NewRequest(c.method, p.URL+c.path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "hs_live_") {
			t.Errorf("%s %s = %d %s, want 404 without the token", c.method, c.path, res.StatusCode, body)
		}
	}
	if n := got.count(); n != 0 {
		t.Errorf("the daemon received %d requests; refused paths must not reach it", n)
	}
}

func TestProxySDKFilesGoWithoutAToken(t *testing.T) {
	up, got := upstream(t)
	p := httptest.NewServer(Proxy(ProxyConfig{API: up.URL + "/api/v1", Token: "hs_live_secret", SDKBase: up.URL + "/sdk/v1"}))
	defer p.Close()
	res, err := http.Get(p.URL + "/helm/sdk/v1/fonts/IBMPlexSans-Regular-Latin1.woff2")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	r := got.last()
	if res.StatusCode != 200 || r.URL.Path != "/sdk/v1/fonts/IBMPlexSans-Regular-Latin1.woff2" || r.Header.Get("Authorization") != "" {
		t.Errorf("status %d, upstream saw %s with Authorization %q", res.StatusCode, r.URL.Path, r.Header.Get("Authorization"))
	}
}

func TestProxyStreamsEventsAsTheyArrive(t *testing.T) {
	up, _ := upstream(t)
	p := httptest.NewServer(Proxy(ProxyConfig{API: up.URL + "/api/v1"}))
	defer p.Close()
	res, err := http.Get(p.URL + "/helm/api/v1/theme/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				line <- sc.Text()
				return
			}
		}
	}()
	select {
	case l := <-line:
		if l != `data: {"theme":"dark"}` {
			t.Errorf("event data %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event did not come through while the stream stayed open")
	}
}

func TestAccentCSS(t *testing.T) {
	css, ok := AccentCSS("#E0A33C", "#5c4bc4")
	want := ":root { --helm-studio-accent: #e0a33c; --helm-on-studio-accent: #1a1400; }\n" +
		"@media (prefers-color-scheme: light) { :root:not([data-theme]) { --helm-studio-accent: #5c4bc4; --helm-on-studio-accent: #ffffff; } }\n" +
		":root[data-theme=\"light\"] { --helm-studio-accent: #5c4bc4; --helm-on-studio-accent: #ffffff; }\n"
	if !ok || css != want {
		t.Errorf("AccentCSS =\n%s\nwant\n%s", css, want)
	}
	if _, ok := AccentCSS("#e0a33c", "red"); ok {
		t.Error("AccentCSS accepted a light value that is not #rrggbb")
	}
}
