package visual

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// studioPage is a studio's own page at level 1: helm.css and its accent through
// the proxy, and the theme bridge. It logs every data-theme it sees, with the
// id of the page load, so a reload would show as a second id.
const studioPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>fixture studio</title>
<link rel="stylesheet" href="/helm/sdk/v1/helm.css">
<link rel="stylesheet" href="/helm/accent.css">
<script>
  window.__load = String(Math.random());
  sessionStorage.setItem("loads", String(Number(sessionStorage.getItem("loads") || "0") + 1));
  window.__themes = [];
  new MutationObserver(() => {
    window.__themes.push(document.documentElement.getAttribute("data-theme") || "system");
  }).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
</script>
<script type="module">
  import { themeBridge, connect } from "/helm/sdk/v1/helm-runtime.js";
  window.__bridge = themeBridge();
  window.__helm = connect();
</script>
</head>
<body><main class="helm-main"><button class="helm-btn helm-btn-studio">Render</button></main></body>
</html>`

type studioServer struct {
	url string
}

// startStudio serves studioPage and mounts the Go runtime SDK's proxy, the way
// a Go studio would, configured from the environment the daemon injected.
func startStudio(t *testing.T, env []string) studioServer {
	t.Helper()
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	mux := http.NewServeMux()
	mux.Handle(helm.ProxyPrefix, helm.Proxy(helm.ProxyConfig{API: get(helm.EnvAPI), Token: get(helm.EnvToken), SDKBase: get(helm.EnvSDKBase),
		AccentDark: get(helm.EnvAccentDark), AccentLight: get(helm.EnvAccentLight)}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, studioPage)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		// The page's event stream would hold Close open.
		srv.CloseClientConnections()
		srv.Close()
	})
	return studioServer{url: srv.URL + "/"}
}

func setTheme(t *testing.T, daemon, theme string) {
	t.Helper()
	req, _ := http.NewRequest("PUT", daemon+"/api/v1/launcher/settings/theme", strings.NewReader(`{"theme":"`+theme+`"}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("setting the theme to %s: %d", theme, res.StatusCode)
	}
}

// The toggle reaches a studio that is already running, without a reload —
// for a studio with capabilities and for one with none (docs/decisions.md M6
// Q9, and the milestone's review focus).
func TestTheLauncherThemeReachesARunningStudioPage(t *testing.T) {
	b := openBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The daemon, assembled as cmd/helmstudio assembles it.
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewUnstartedServer(nil)
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: d, API: "http://" + ts.Listener.Addr().String() + studioapi.Base, Logf: t.Logf})
	studios := []*manifest.Manifest{
		{ID: "capable-studio", Name: "capable", Capabilities: []string{"kv"}, Hue: &manifest.Hue{Dark: "#e0a33c", Light: "#5c4bc4"}},
		{ID: "plain-studio", Name: "plain"},
	}
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Platform: plat.Launches})
	var loaded []supervisor.Studio
	for _, m := range studios {
		loaded = append(loaded, supervisor.Studio{Manifest: m})
	}
	sup.SetStudios(loaded)
	svc, h := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
	srv, err := api.New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf, api.WithStudioAPI(h, svc))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv
	ts.Start()
	defer func() { svc.Events().Shutdown("test over"); ts.CloseClientConnections(); ts.Close() }()
	setTheme(t, ts.URL, "dark")

	for _, m := range studios {
		t.Run(m.ID, func(t *testing.T) {
			// The environment the daemon injects at spawn, from the real hook.
			env, err := plat.Launches.LaunchEnv(ctx, supervisor.Studio{Manifest: m}, store.NewID(time.Now()))
			if err != nil {
				t.Fatal(err)
			}
			studio := startStudio(t, env)
			page, err := b.NewPage(ctx, 600, 400, "light")
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			if err := page.Navigate(ctx, studio.url); err != nil {
				t.Fatal(err)
			}
			// The bridge has heard the stream once it claims to follow it.
			if err := page.WaitFor(ctx, `document.documentElement.getAttribute("data-helm-theme-follows") === "launcher" && document.documentElement.getAttribute("data-theme") === "dark"`); err != nil {
				t.Fatal(err)
			}
			background := func() string {
				var bg string
				if err := page.Eval(ctx, `getComputedStyle(document.body).backgroundColor`, &bg); err != nil {
					t.Fatal(err)
				}
				return bg
			}
			if bg := background(); bg != "rgb(13, 12, 10)" {
				t.Errorf("dark ground = %s", bg)
			}

			setTheme(t, ts.URL, "light")
			if err := page.WaitFor(ctx, `document.documentElement.getAttribute("data-theme") === "light"`); err != nil {
				t.Fatal(err)
			}
			if bg := background(); bg != "rgb(250, 248, 243)" {
				t.Errorf("light ground = %s", bg)
			}
			// system removes data-theme; the page's own preference (light, emulated) applies.
			setTheme(t, ts.URL, "system")
			if err := page.WaitFor(ctx, `!document.documentElement.hasAttribute("data-theme")`); err != nil {
				t.Fatal(err)
			}
			setTheme(t, ts.URL, "dark")
			if err := page.WaitFor(ctx, `document.documentElement.getAttribute("data-theme") === "dark"`); err != nil {
				t.Fatal(err)
			}

			var state struct {
				Loads      string   `json:"loads"`
				Navigation int      `json:"navigation"`
				Themes     []string `json:"themes"`
				Accent     string   `json:"accent"`
				HTML       string   `json:"html"`
			}
			if err := page.Eval(ctx, `({loads: sessionStorage.getItem("loads"), navigation: performance.getEntriesByType("navigation").length,
				themes: window.__themes, accent: getComputedStyle(document.documentElement).getPropertyValue("--helm-studio-accent").trim(),
				html: document.documentElement.outerHTML})`, &state); err != nil {
				t.Fatal(err)
			}
			if state.Loads != "1" || state.Navigation != 1 {
				t.Errorf("the page loaded %s times (%d navigations); the theme must change without a reload", state.Loads, state.Navigation)
			}
			if got := strings.Join(state.Themes, ","); got != "dark,light,system,dark" {
				t.Errorf("the page saw themes %s, want dark,light,system,dark", got)
			}
			if want := map[string]string{"capable-studio": "#e0a33c"}[m.ID]; want != "" && state.Accent != want {
				t.Errorf("--helm-studio-accent in dark = %q, want %s from the manifest's hue", state.Accent, want)
			}
			if strings.Contains(state.HTML, "hs_live_") {
				t.Error("the page's DOM contains a studio token")
			}

			// A studio with capabilities reaches its own API from the page, and
			// nothing the page can read carries the token.
			if len(m.Capabilities) > 0 {
				var me string
				if err := page.Eval(ctx, `window.__helm.me.get().then((m) => JSON.stringify(m))`, &me); err != nil {
					t.Fatal(err)
				}
				var decoded struct {
					StudioID string `json:"studio_id"`
				}
				if json.Unmarshal([]byte(me), &decoded) != nil || decoded.StudioID != m.ID || strings.Contains(me, "hs_live_") {
					t.Errorf("/me from the page = %s", me)
				}
			} else {
				var refused string
				if err := page.Eval(ctx, `window.__helm.me.get().then(() => "ok", (e) => e.kind)`, &refused); err != nil {
					t.Fatal(err)
				}
				if refused != "Unauthenticated" {
					t.Errorf("a studio with no capabilities reached /me from its page: %s", refused)
				}
			}
		})
	}
}
