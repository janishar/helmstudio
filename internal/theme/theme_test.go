package theme

import (
	"bufio"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
)

func TestSettingsDefaultToSystemAndPublishChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, platformtest.Dirs(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub(System)
	s := &Settings{Store: st, Hub: hub}
	if v, err := s.Load(ctx); err != nil || v != System {
		t.Fatalf("Load on an empty store = %q, %v; want system", v, err)
	}
	var heard []string
	hub.OnChange(func(v string) { heard = append(heard, v) })
	for _, v := range []string{Light, Dark} {
		if err := s.Set(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set(ctx, "sepia"); err == nil {
		t.Error("Set accepted sepia")
	}
	if v, _ := s.Load(ctx); v != Dark || hub.Current() != Dark {
		t.Errorf("after setting dark: stored %q, hub %q", v, hub.Current())
	}
	if strings.Join(heard, ",") != "light,dark" {
		t.Errorf("listeners heard %q, want light,dark", heard)
	}
}

// The stream sends the current theme first, then each change, and nothing else.
func TestThemeStreamSendsCurrentThenChanges(t *testing.T) {
	hub := NewHub(Dark)
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeEvents))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if l := sc.Text(); l != "" {
				lines <- l
			}
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("no event within 5s")
			return ""
		}
	}
	want := func(theme string) {
		t.Helper()
		if l := next(); l != "event: theme" {
			t.Fatalf("line %q, want event: theme", l)
		}
		if l := next(); l != `data: {"theme":"`+theme+`"}` {
			t.Fatalf("line %q, want data for %s", l, theme)
		}
	}
	want(Dark)
	for deadline := time.Now().Add(5 * time.Second); hub.Subscribers() == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	hub.Publish(Light)
	want(Light)
	hub.Publish(System)
	want(System)
}

// 03 §2c's ramp table is Ramp, and every entry keeps its stated distance from
// the accent and the launch hues, and its contrast on the panel.
func TestRampMatchesTheDesignAndKeepsItsDistances(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "03-design-system.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(doc)
	sec := s[strings.Index(s, "## 2c ·"):]
	sec = sec[:strings.Index(sec[3:], "\n## ")+3]
	row := regexp.MustCompile("(?m)^\\| (\\d) \\| `(#[0-9a-f]{6})` \\| `(#[0-9a-f]{6})` \\|$")
	var fromDoc []Pair
	for _, m := range row.FindAllStringSubmatch(sec, -1) {
		fromDoc = append(fromDoc, Pair{m[2], m[3]})
	}
	if len(fromDoc) != len(Ramp) {
		t.Fatalf("03 §2c has %d ramp rows, Ramp has %d", len(fromDoc), len(Ramp))
	}
	for i := range Ramp {
		if fromDoc[i] != Ramp[i] {
			t.Errorf("ramp %d: 03 §2c says %v, Ramp has %v", i, fromDoc[i], Ramp[i])
		}
	}
	// The accent and the four launch studios' hues (03 §2), and each theme's panel.
	fixed := map[string][]string{
		"dark":  {"#ffc700", "#5b8def", "#e0a33c", "#d8558f", "#8b7cf0"},
		"light": {"#f5b800", "#2f5fc4", "#a06a10", "#b12f68", "#5c4bc4"},
	}
	panel := map[string]string{"dark": "#161512", "light": "#ffffff"}
	for i, p := range Ramp {
		for theme, hex := range map[string]string{"dark": p.Dark, "light": p.Light} {
			h, _ := Hue(hex)
			for _, other := range fixed[theme] {
				oh, _ := Hue(other)
				d := math.Abs(h - oh)
				if d > 180 {
					d = 360 - d
				}
				if d < 30 {
					t.Errorf("ramp %d %s %s is %.0f° from %s; 03 §2c requires 30°", i, theme, hex, d, other)
				}
			}
			if r, _ := Contrast(hex, panel[theme]); r < 3 {
				t.Errorf("ramp %d %s %s is %.2f:1 on the panel; 03 §2c requires 3:1", i, theme, hex, r)
			}
		}
	}
}

func TestAccentIsTheHueOrAStableRampEntry(t *testing.T) {
	m := &manifest.Manifest{ID: "h3-studio", Hue: &manifest.Hue{Dark: "#E0A33C", Light: "#a06a10"}}
	if got := Accent(m); got != (Pair{"#e0a33c", "#a06a10"}) {
		t.Errorf("Accent with a hue = %v", got)
	}
	a := Accent(&manifest.Manifest{ID: "wan-studio"})
	b := Accent(&manifest.Manifest{ID: "wan-studio"})
	if a != b || a == (Pair{}) {
		t.Errorf("ramp entries for the same id differ or are empty: %v %v", a, b)
	}
	seen := map[Pair]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "wan-studio", "sdxl-studio"} {
		seen[RampFor(id)] = true
	}
	if len(seen) < 3 {
		t.Errorf("ten ids used only %d ramp entries; the hash is not spreading", len(seen))
	}
}

func TestOnPicksTheMoreLegibleText(t *testing.T) {
	for hex, want := range map[string]string{"#e0a33c": "#1a1400", "#5c4bc4": "#ffffff", "#ffc700": "#1a1400", "#1b767e": "#ffffff"} {
		if got := On(hex); got != want {
			t.Errorf("On(%s) = %s, want %s", hex, got, want)
		}
	}
}

func TestSDKMajor(t *testing.T) {
	for _, tc := range []struct {
		sdk *manifest.SDK
		ok  bool
	}{
		{nil, true},
		{&manifest.SDK{Runtime: "^1", CSS: "~1.2"}, true},
		{&manifest.SDK{UI: "1.0.3"}, true},
		{&manifest.SDK{Runtime: "^1", UI: "^2"}, false},
		{&manifest.SDK{CSS: "0.9"}, false},
	} {
		major, err := SDKMajor(&manifest.Manifest{ID: "x", SDK: tc.sdk})
		if tc.ok != (err == nil) || (tc.ok && major != 1) {
			t.Errorf("SDKMajor(%+v) = %d, %v", tc.sdk, major, err)
		}
		if err != nil && !strings.Contains(err.Error(), "serves only major 1") {
			t.Errorf("refusal %q does not say what is served", err)
		}
	}
}
