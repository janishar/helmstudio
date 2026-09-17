package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	helmtheme "github.com/janishar/helmstudio/internal/theme"
	helmcss "github.com/janishar/helmstudio/packages/helm-css"
)

// Which text the components draw on which ground, read from what Chrome paints
// (docs/design/03-design-system.md §2a, §2b).
//
// helm-css's contract test holds every pair §2b declares to its ratio in both
// themes. That says nothing about the pairs a component actually draws:
// text-primary and ground-inset each pass every pair they are declared in, and
// together they are dark on dark in the light theme, where ground-inset stays
// dark. Nor can a component's style string say it alone, because a ground is as
// often an ancestor's as an element's own — the player's empty state sits on
// its stage, the gallery's kind label on its thumbnail. So this reads the
// fixture pages the goldens are made from, in both themes: every element that
// shows text, the colour it is drawn in and the first opaque ground behind it,
// named back to the tokens that have those values.

// drawnFixtures are the component pages as the goldens load them, and ui.html
// again with thumbnails, which move the gallery's kind label onto one.
var drawnFixtures = []string{"ui.html", "ui.html?thumbs=1", "timeline.html?export=running"}

// insetGround is dark in both themes (§2a) while the text tokens turn over with
// the theme, so only the foregrounds §2b declares on it may sit there: any
// other holds in one theme at most.
const insetGround = "--helm-ground-inset"

// drawn is one element showing text, and the colours it was drawn with.
type drawn struct {
	Host   string `json:"host"`
	What   string `json:"what"`
	Text   string `json:"text"`
	Color  string `json:"color"`
	Ground string `json:"ground"`
}

// drawnText lists every element inside a component's shadow root that shows
// text — its own text nodes, or a form control's value or placeholder — with
// the first ground behind it that is not transparent, crossing shadow roots
// on the way up. An element with no box, such as a <style>, an unopened
// <option> or the 1px screen-reader text, shows nothing and is left out.
const drawnText = `(() => {
  const ground = (node) => {
    for (let n = node; n; n = n.parentElement || n.parentNode?.host) {
      const bg = getComputedStyle(n).backgroundColor;
      if (bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") return bg;
    }
    return getComputedStyle(document.documentElement).backgroundColor;
  };
  const control = (el) => el.matches("input:not([type=range], [type=checkbox], [type=radio]), select, textarea");
  const shows = (el) => control(el) || [...el.childNodes].some((c) => c.nodeType === Node.TEXT_NODE && c.textContent.trim() !== "");
  const name = (el) => el.localName + [...el.classList].map((c) => "." + c).join("");
  const out = [];
  const walk = (root, host) => {
    for (const el of root.querySelectorAll("*")) {
      if (el.shadowRoot) walk(el.shadowRoot, el.localName);
      if (!host || !shows(el)) continue;
      const box = el.getBoundingClientRect();
      if (box.width < 2 || box.height < 2 || !el.checkVisibility({ visibilityProperty: true })) continue;
      const text = el.localName === "select" ? el.selectedOptions[0]?.textContent : control(el) ? el.value : el.textContent;
      out.push({ host, what: name(el), text: (text || "").trim().slice(0, 40), color: getComputedStyle(el).color, ground: ground(el) });
      if (el.placeholder && !el.value) {
        out.push({ host, what: name(el) + "::placeholder", text: el.placeholder, color: getComputedStyle(el, "::placeholder").color, ground: ground(el) });
      }
    }
  };
  walk(document, "");
  return out;
})()`

func TestComponentTextMeetsItsContrastInBothThemes(t *testing.T) {
	b := openBrowser(t)
	srv := fixtureServer(t)
	pairs := declaredPairs(t)
	for _, theme := range []string{"dark", "light"} {
		colours := tokenColours(t, theme)
		for _, fixture := range drawnFixtures {
			t.Run(theme+"/"+fixture, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				// The system preference opposes data-theme, as the goldens do.
				page, err := b.NewPage(ctx, 900, 1400, map[string]string{"dark": "light", "light": "dark"}[theme])
				if err != nil {
					t.Fatal(err)
				}
				defer page.Close()
				sep := map[bool]string{true: "&", false: "?"}[strings.Contains(fixture, "?")]
				if err := page.Navigate(ctx, srv.URL+"/fixtures/"+fixture+sep+"theme="+theme); err != nil {
					t.Fatal(err)
				}
				if err := page.WaitFor(ctx, `document.documentElement.dataset.ready === "1"`); err != nil {
					t.Fatal(err)
				}
				var seen []drawn
				if err := page.Eval(ctx, drawnText, &seen); err != nil {
					t.Fatal(err)
				}
				if len(seen) < 20 {
					t.Fatalf("read %d elements showing text; the probe did not reach the components", len(seen))
				}
				reported := map[string]bool{}
				for _, d := range seen {
					key := d.Host + " " + d.What + " " + d.Color + " " + d.Ground
					if reported[key] {
						continue
					}
					for _, f := range judge(theme, colours, pairs, d) {
						reported[key] = true
						t.Error(f)
					}
				}
			})
		}
	}
}

// The check must be able to fail, and on this: helm-gallery's controls drawn
// in text-primary on ground-inset, which is 1.02:1 in the light theme and
// passes on contrast alone in the dark.
func TestTheDrawnTextCheckCatchesTextOnTheInsetGround(t *testing.T) {
	pairs := declaredPairs(t)
	for theme, c := range map[string]struct{ text, inset string }{
		"dark":  {"rgb(241, 237, 228)", "rgb(9, 8, 4)"},
		"light": {"rgb(26, 24, 19)", "rgb(28, 26, 21)"},
	} {
		d := drawn{Host: "helm-gallery", What: "select", Text: "All kinds", Color: c.text, Ground: c.inset}
		if len(judge(theme, tokenColours(t, theme), pairs, d)) == 0 {
			t.Errorf("%s: text-primary on ground-inset passed the check", theme)
		}
	}
	// And a declared pair passes, so the failure above is the pair's.
	logText := drawn{Host: "helm-terminal", What: "div.row", Text: "make mps", Color: "rgb(207, 200, 186)", Ground: "rgb(28, 26, 21)"}
	if f := judge("light", tokenColours(t, "light"), pairs, logText); len(f) != 0 {
		t.Errorf("log-text on ground-inset failed the check: %v", f)
	}
}

// judge holds one drawn element to §2b in theme. colours maps a value to the
// tokens that have it there. A pair §2b declares must meet its ratio; one it
// does not must meet the lowest ratio §2b holds the foreground to anywhere, or
// 4.5:1; and on ground-inset only the foregrounds §2b declares there are
// accepted at all.
func judge(theme string, colours map[string][]string, pairs map[[2]string]float64, d drawn) []string {
	where := fmt.Sprintf("%s: <%s> %s %q", theme, d.Host, d.What, d.Text)
	fg, okFg := hexOf(d.Color)
	bg, okBg := hexOf(d.Ground)
	if !okFg || !okBg {
		return []string{fmt.Sprintf("%s is drawn in %s on %s; a component draws opaque tokens", where, d.Color, d.Ground)}
	}
	fgs, bgs := colours[fg], colours[bg]
	if len(fgs) == 0 || len(bgs) == 0 {
		return []string{fmt.Sprintf("%s is drawn in %s on %s, and a component draws only in tokens: %s names %v and %v", where, fg, bg, theme, fgs, bgs)}
	}
	var failures []string
	if slices.Contains(bgs, insetGround) && !slices.ContainsFunc(fgs, func(f string) bool {
		_, ok := pairs[[2]string{f, insetGround}]
		return ok
	}) {
		failures = append(failures, fmt.Sprintf("%s is %s on %s, which §2b declares only for the log tokens: it stays dark in the light theme (03 §2a)",
			where, strings.Join(fgs, " or "), insetGround))
	}
	need, declared := 4.5, false
	for _, f := range fgs {
		for _, g := range bgs {
			if r, ok := pairs[[2]string{f, g}]; ok && (!declared || r < need) {
				need, declared = r, true
			}
		}
	}
	if !declared {
		for p, r := range pairs {
			if slices.Contains(fgs, p[0]) && r < need {
				need = r
			}
		}
	}
	ratio, err := helmtheme.Contrast(fg, bg)
	if err != nil {
		return append(failures, fmt.Sprintf("%s: %v", where, err))
	}
	if ratio < need {
		failures = append(failures, fmt.Sprintf("%s is %s (%s) on %s (%s) at %.2f:1, below %.1f:1",
			where, strings.Join(fgs, " or "), fg, strings.Join(bgs, " or "), bg, ratio, need))
	}
	return failures
}

var rgbColour = regexp.MustCompile(`^rgb\((\d+), (\d+), (\d+)\)$`)

// hexOf turns Chrome's computed rgb() into #rrggbb. A colour with alpha, or in
// any other notation, is not one a component should compute.
func hexOf(computed string) (string, bool) {
	m := rgbColour.FindStringSubmatch(computed)
	if m == nil {
		return "", false
	}
	hex := "#"
	for _, channel := range m[1:] {
		n, err := strconv.Atoi(channel)
		if err != nil || n > 255 {
			return "", false
		}
		hex += fmt.Sprintf("%02x", n)
	}
	return hex, true
}

var hexColour = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// tokenColours maps each colour helm-css's tokens take in theme to the tokens
// that take it, following aliases such as --helm-studio-accent.
func tokenColours(t *testing.T, theme string) map[string][]string {
	t.Helper()
	b, err := fs.ReadFile(helmcss.Files, "tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var toks helmcss.Tokens
	if err := json.Unmarshal(b, &toks); err != nil {
		t.Fatal(err)
	}
	values := toks.Themes[theme]
	if len(values) == 0 {
		t.Fatalf("tokens.json has no %s theme", theme)
	}
	alias := regexp.MustCompile(`^var\((--helm-[a-z0-9-]+)\)$`)
	out := map[string][]string{}
	for name, v := range values {
		for range values {
			m := alias.FindStringSubmatch(v)
			if m == nil {
				break
			}
			v = values[m[1]]
		}
		if v = strings.ToLower(v); hexColour.MatchString(v) {
			out[v] = append(out[v], name)
		}
	}
	for _, names := range out {
		slices.Sort(names)
	}
	return out
}

// declaredPairs reads 03 §2b: {foreground, ground} and the ratio it must meet.
func declaredPairs(t *testing.T) map[[2]string]float64 {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "03-design-system.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(doc)
	start := strings.Index(s, "\n## 2b · Contrast pairs\n")
	if start < 0 {
		t.Fatal("03-design-system.md has no section 2b · Contrast pairs")
	}
	s = s[start+1:]
	if end := strings.Index(s[3:], "\n## "); end >= 0 {
		s = s[:end+3]
	}
	code := regexp.MustCompile("`([^`]*)`")
	out := map[[2]string]float64{}
	for _, row := range strings.Split(s, "\n") {
		if !strings.HasPrefix(row, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
		if len(cells) != 3 {
			t.Fatalf("03 §2b: row %q has %d cells", row, len(cells))
		}
		ratio, err := strconv.ParseFloat(strings.TrimSpace(cells[2]), 64)
		if err != nil {
			t.Fatalf("03 §2b: row %q: %v", row, err)
		}
		for _, fg := range code.FindAllStringSubmatch(cells[0], -1) {
			for _, bg := range code.FindAllStringSubmatch(cells[1], -1) {
				out[[2]string{"--helm-" + fg[1], "--helm-" + bg[1]}] = ratio
			}
		}
	}
	if len(out) < 25 {
		t.Fatalf("read %d pairs from 03 §2b; the table did not parse", len(out))
	}
	return out
}
