// Package visual is helm-css's visual regression, in both themes, and the end
// to end check that the launcher's theme reaches a running studio's page
// (docs/design/04-packages.md §10; docs/decisions.md M6 Q9, Q16).
//
// It needs a Chrome. A missing browser fails the tests unless
// HELM_ALLOW_MISSING_BROWSER is set. Goldens are made with `make golden`,
// never by hand, and are tied to the Chrome major recorded beside them.
package visual

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

// fixtureServer serves the fixture pages and helm-css as the daemon serves it.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/fixtures/", http.StripPrefix("/fixtures/", http.FileServerFS(os.DirFS("fixtures"))))
	mux.Handle("/sdk/v1/", http.StripPrefix("/sdk/v1/", http.FileServerFS(helmcss.Files)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type shot struct {
	name    string
	fixture string
	theme   string // data-theme, or "" for the system preference
	system  string // emulated prefers-color-scheme
	width   int
}

func shots() []shot {
	var out []shot
	for _, theme := range []string{"dark", "light"} {
		// The system preference opposes data-theme, so a golden only matches
		// when data-theme wins.
		opposite := map[string]string{"dark": "light", "light": "dark"}[theme]
		out = append(out,
			shot{"tokens-" + theme, "tokens.html", theme, opposite, 1100},
			shot{"components-" + theme, "components.html", theme, opposite, 1100},
		)
		for _, w := range []int{1280, 1000, 380} {
			out = append(out, shot{fmt.Sprintf("layout-%s-%d", theme, w), "layout.html", theme, opposite, w})
		}
	}
	// No data-theme: the prefers-color-scheme block decides.
	out = append(out, shot{"tokens-system-light", "tokens.html", "", "light", 1100})
	return out
}

func TestHelmCSSMatchesItsGoldensInBothThemes(t *testing.T) {
	b := openBrowser(t)
	major, version, err := chrome.Major(browserBin)
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv(EnvUpdate) != ""
	versionFile := filepath.Join(goldenDir, "CHROME_MAJOR")
	if update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(versionFile, []byte(strconv.Itoa(major)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		recorded, err := os.ReadFile(versionFile)
		if err != nil {
			t.Fatalf("no goldens (%v); run make golden", err)
		}
		if strings.TrimSpace(string(recorded)) != strconv.Itoa(major) {
			t.Fatalf("the goldens were made with Chrome %s and this is %s (%s); fonts and antialiasing change between majors, so regenerate deliberately with make golden and review the images",
				strings.TrimSpace(string(recorded)), strconv.Itoa(major), strings.TrimSpace(version))
		}
	}
	srv := fixtureServer(t)
	outDir := ""
	for _, s := range shots() {
		t.Run(s.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			page, err := b.NewPage(ctx, s.width, 800, s.system)
			if err != nil {
				t.Fatal(err)
			}
			url := srv.URL + "/fixtures/" + s.fixture
			if s.theme != "" {
				url += "?theme=" + s.theme
			}
			if err := page.Navigate(ctx, url); err != nil {
				t.Fatal(err)
			}
			if err := page.WaitFor(ctx, `document.documentElement.dataset.ready === "1" && document.fonts.status === "loaded"`); err != nil {
				t.Fatal(err)
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
