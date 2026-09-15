package studioapi_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/theme"
)

func envOf(t *testing.T, file string) map[string]string {
	t.Helper()
	vars := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(waitFile(t, file)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		vars[k] = v
	}
	return vars
}

// A launched process gets the theme as it is at spawn, its hue per theme (or
// its ramp entry), and where the SDK files for its major are served
// (docs/decisions.md M6 Q7, Q8, Q14).
func TestLaunchInjectsThemeAccentAndSDKBase(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: d, API: "http://127.0.0.1:8700/api/v1", Logf: t.Logf})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: 2 * time.Second, PortMin: 44000, PortMax: 44999, Platform: plat.Launches})
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		sup.Shutdown(c)
	})
	if err := plat.Theme.Set(ctx, theme.Light); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dump := func(name string) string {
		return `"env | grep ^HELM_ > ` + filepath.Join(root, name) + `.tmp && mv ` + filepath.Join(root, name) + `.tmp ` + filepath.Join(root, name) + ` && sleep 60"`
	}
	hued := launchable(t, st, manifestYAML("hued-studio", t.TempDir(), "", dump("hued"))+"hue: { dark: \"#E0A33C\", light: \"#a06a10\" }\nsdk: { runtime: \"^1\", css: \"~1.0\" }\n")
	plain := launchable(t, st, manifestYAML("plain-studio", t.TempDir(), "", dump("plain")))
	sup.SetStudios([]supervisor.Studio{hued, plain})

	for _, id := range []string{"hued-studio", "plain-studio"} {
		if _, err := sup.Launch(ctx, id, supervisor.LaunchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	h := envOf(t, filepath.Join(root, "hued"))
	if h["HELM_THEME"] != "light" || h["HELM_ACCENT_DARK"] != "#e0a33c" || h["HELM_ACCENT_LIGHT"] != "#a06a10" || h["HELM_SDK_BASE"] != "http://127.0.0.1:8700/sdk/v1" {
		t.Errorf("a studio with a hue got %v", h)
	}
	p := envOf(t, filepath.Join(root, "plain"))
	want := theme.RampFor("plain-studio")
	if p["HELM_ACCENT_DARK"] != want.Dark || p["HELM_ACCENT_LIGHT"] != want.Light || p["HELM_THEME"] != "light" {
		t.Errorf("a studio without a hue got %v, want ramp entry %v", p, want)
	}
}

// A studio pinning a major nobody serves is refused before anything is
// stopped for it: the running heavy studio keeps running.
func TestAnUnservedSDKMajorRefusesBeforePreemption(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: d, API: "http://127.0.0.1:8700/api/v1", Logf: t.Logf})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: 2 * time.Second, PortMin: 45000, PortMax: 45999, Platform: plat.Launches,
		HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		sup.Shutdown(c)
	})
	heavy := func(id, extra string) supervisor.Studio {
		y := manifestYAML(id, t.TempDir(), "", `"sleep 60"`)
		y = strings.Replace(y, "    cmd:", "    heavy: true\n    cmd:", 1)
		return launchable(t, st, y+extra)
	}
	running := heavy("running-studio", "")
	future := heavy("future-studio", "sdk: { ui: \"^2\" }\n")
	sup.SetStudios([]supervisor.Studio{running, future})
	if _, err := sup.Launch(ctx, "running-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err = sup.Launch(ctx, "future-studio", supervisor.LaunchOptions{})
	var se *supervisor.Error
	if !errors.As(err, &se) || se.Kind != supervisor.KindNotLaunchable || !strings.Contains(se.Message, `sdk.ui "^2"`) {
		t.Fatalf("launch = %v, want not_launchable naming sdk.ui", err)
	}
	gs, err := sup.Status(ctx, "running-studio")
	if err != nil || (gs.State != "running" && gs.State != "starting") {
		t.Errorf("the running studio is %q (%v) after the refusal", gs.State, err)
	}
}
