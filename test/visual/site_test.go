package visual

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/chrome"
)

// The site's landing page and its quickstart, in both themes at the three
// widths the layout collapses at (docs/agents/milestones/10-docs-and-site.md,
// task 9). The site is its own module, so it is built by running its command,
// under a base path the test serves it from — which also proves every address
// on the two pages is written under the base.
//
// A page follows the system theme until a visitor picks one, so each theme is
// the emulated prefers-color-scheme with no data-theme set.
func TestTheSiteMatchesItsGoldens(t *testing.T) {
	b := openBrowser(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	build := exec.Command("go", "run", "./cmd/site", "-root", root, "-site", ".", "-out", out, "-base", "/site/", "-cname", "")
	build.Dir = filepath.Join(root, "site")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the site: %v\n%s", err, b)
	}
	mux := http.NewServeMux()
	mux.Handle("/site/", http.StripPrefix("/site/", http.FileServer(http.Dir(out))))
	mux.Handle("/fixtures/", http.StripPrefix("/fixtures/", http.FileServerFS(os.DirFS("fixtures"))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

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

	pages := []struct{ name, path string }{{"landing", "/site/"}, {"quickstart", "/site/docs/quickstart/"}}
	outDir := ""
	for _, pg := range pages {
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{1280, 1000, 380} {
				name := fmt.Sprintf("site-%s-%s-%d", pg.name, theme, w)
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					page, err := b.NewPage(ctx, w, 800, theme)
					if err != nil {
						t.Fatal(err)
					}
					defer page.Close()
					if err := page.Navigate(ctx, srv.URL+pg.path); err != nil {
						t.Fatal(err)
					}
					if err := page.WaitFor(ctx, `document.readyState === "complete" && document.fonts.status === "loaded"`); err != nil {
						t.Fatal(err)
					}
					var state struct {
						Plex      bool   `json:"plex"`
						DataTheme string `json:"dataTheme"`
						Missing   int    `json:"missing"`
					}
					if err := page.Eval(ctx, `({
						plex: document.fonts.check('15px "IBM Plex Sans"') && document.fonts.check('12px "IBM Plex Mono"'),
						dataTheme: document.documentElement.getAttribute("data-theme") || "",
						missing: [...document.styleSheets].filter(s => { try { return s.cssRules.length === 0 } catch (e) { return true } }).length,
					})`, &state); err != nil {
						t.Fatal(err)
					}
					if !state.Plex {
						t.Fatal("IBM Plex did not load from the site's css/fonts")
					}
					if state.DataTheme != "" || state.Missing != 0 {
						t.Fatalf("data-theme=%q and %d stylesheets did not load; the golden would not be of the site as served", state.DataTheme, state.Missing)
					}
					got, err := page.Screenshot(ctx, w)
					if err != nil {
						t.Fatal(err)
					}
					golden := filepath.Join(goldenDir, name+".png")
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
							outDir, _ = os.MkdirTemp("", "helm-visual-site-")
						}
						_ = os.WriteFile(filepath.Join(outDir, name+".actual.png"), got, 0o644)
						if diffImg != nil {
							_ = os.WriteFile(filepath.Join(outDir, name+".diff.png"), diffImg, 0o644)
						}
						t.Errorf("%s differs from its golden: %d of %d pixels (%v); actual and diff in %s. A change to the landing page or the quickstart changes these: run make golden and review the images",
							name, differ, total, err, outDir)
					}
				})
			}
		}
	}
}
