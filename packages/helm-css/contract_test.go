package helmcss

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The design document the tokens are a contract with.
var designDoc = filepath.Join("..", "..", "docs", "design", "03-design-system.md")

// section returns the text of "## <heading>" up to the next "## ".
func section(t *testing.T, heading string) string {
	t.Helper()
	doc, err := os.ReadFile(designDoc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(doc)
	start := strings.Index(s, "\n## "+heading+"\n")
	if start < 0 {
		t.Fatalf("%s has no section %q", designDoc, heading)
	}
	rest := s[start+1:]
	if end := strings.Index(rest[3:], "\n## "); end >= 0 {
		rest = rest[:end+3]
	}
	return rest
}

var backticked = regexp.MustCompile("`([^`]*)`")

func cells(row string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func firstCode(cell string) string {
	m := backticked.FindStringSubmatch(cell)
	if m == nil {
		return ""
	}
	return m[1]
}

// designTokens reads 03 §2a: the themed table (token, dark, light, role) and
// the theme-independent one (token, value, role), expanding the spacing row.
func designTokens(t *testing.T) (dark, light, common map[string]string) {
	dark, light, common = map[string]string{}, map[string]string{}, map[string]string{}
	for _, row := range strings.Split(section(t, "2a · Tokens"), "\n") {
		if !strings.HasPrefix(row, "| `--helm-") {
			continue
		}
		c := cells(row)
		name := firstCode(c[0])
		switch {
		case strings.Contains(c[0], "…"):
			// "--helm-space-1 … --helm-space-9" with nine values
			names := backticked.FindAllStringSubmatch(c[0], -1)
			values := backticked.FindAllStringSubmatch(c[1], -1)
			prefix := strings.TrimRight(names[0][1], "0123456789")
			from, _ := strconv.Atoi(strings.TrimPrefix(names[0][1], prefix))
			to, _ := strconv.Atoi(strings.TrimPrefix(names[1][1], prefix))
			if to-from+1 != len(values) {
				t.Fatalf("03 §2a: %s names %d tokens and %d values", c[0], to-from+1, len(values))
			}
			for i, v := range values {
				common[prefix+strconv.Itoa(from+i)] = v[1]
			}
		case len(c) == 4:
			dark[name], light[name] = firstCode(c[1]), firstCode(c[2])
		case len(c) == 3:
			common[name] = firstCode(c[1])
		default:
			t.Fatalf("03 §2a: row %q has %d cells", row, len(c))
		}
	}
	return dark, light, common
}

func readTokens(t *testing.T) (fromCSS, fromJSON Tokens) {
	t.Helper()
	src, err := os.ReadFile("helm-tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	fromCSS, err = ParseTokens(string(src))
	if err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile("tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(js, &fromJSON); err != nil {
		t.Fatal(err)
	}
	return fromCSS, fromJSON
}

func diffMaps(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if g, ok := got[k]; !ok {
			t.Errorf("%s: %s is in 03 §2a and missing here", what, k)
		} else if g != want[k] {
			t.Errorf("%s: %s = %q, 03 §2a says %q", what, k, g, want[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: %s is not in 03 §2a; a token exists only once the design names it", what, k)
		}
	}
}

// Token names are API within a major (03 §5): the design table, the CSS and
// tokens.json name the same tokens with the same values, and nothing else.
func TestTokensMatchTheDesignTable(t *testing.T) {
	dark, light, common := designTokens(t)
	if len(dark) < 20 || len(common) < 20 {
		t.Fatalf("read %d themed and %d other tokens from 03 §2a; the table did not parse", len(dark), len(common))
	}
	fromCSS, fromJSON := readTokens(t)
	for name, tok := range map[string]Tokens{"helm-tokens.css": fromCSS, "tokens.json": fromJSON} {
		diffMaps(t, name+" dark", tok.Themes["dark"], dark)
		diffMaps(t, name+" light", tok.Themes["light"], light)
		diffMaps(t, name+" theme-independent", tok.Tokens, common)
	}
}

// 03 §2a: under prefers-reduced-motion every duration except the progress fill is 0ms.
func TestReducedMotionZeroesEveryDurationButProgress(t *testing.T) {
	fromCSS, _ := readTokens(t)
	for name := range fromCSS.Tokens {
		if !strings.HasPrefix(name, "--helm-duration-") {
			continue
		}
		v, replaced := fromCSS.ReducedMotion[name]
		if name == "--helm-duration-progress" {
			if replaced {
				t.Errorf("reduced motion replaces %s; the progress fill is information and keeps its duration", name)
			}
			continue
		}
		if !replaced || v != "0ms" {
			t.Errorf("reduced motion leaves %s at %q, want 0ms", name, fromCSS.Tokens[name])
		}
	}
}

// helm.css, helm.min.css and tokens.json are built from the layers; a hand
// edit, or a layer changed without `make css`, fails here.
func TestDerivedFilesAreBuiltFromTheLayers(t *testing.T) {
	out, err := Build(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Derived {
		disk, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(disk, out[name]) {
			t.Errorf("%s differs from what the layers build; run make css", name)
		}
	}
}

func TestMinifyKeepsStringsAndDropsComments(t *testing.T) {
	got := Minify("/* c */\n.a  ,  .b {\n  font-family: \"IBM  Plex\";\n  margin: 0 auto;\n}\n")
	want := `.a,.b{font-family: "IBM  Plex";margin: 0 auto}`
	if got != want {
		t.Errorf("Minify = %q, want %q", got, want)
	}
}

// ---- contrast (03 §2b) ------------------------------------------------------

type pair struct {
	fg, bg string
	min    float64
}

func designPairs(t *testing.T) []pair {
	var out []pair
	for _, row := range strings.Split(section(t, "2b · Contrast pairs"), "\n") {
		if !strings.HasPrefix(row, "| `") {
			continue
		}
		c := cells(row)
		min, err := strconv.ParseFloat(c[2], 64)
		if err != nil {
			t.Fatalf("03 §2b: row %q: threshold %q", row, c[2])
		}
		for _, fg := range backticked.FindAllStringSubmatch(c[0], -1) {
			for _, bg := range backticked.FindAllStringSubmatch(c[1], -1) {
				out = append(out, pair{"--helm-" + fg[1], "--helm-" + bg[1], min})
			}
		}
	}
	return out
}

// Contrast is WCAG 2's ratio of relative luminances.
func Contrast(a, b string) (float64, error) {
	la, err := luminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := luminance(b)
	if err != nil {
		return 0, err
	}
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05), nil
}

func luminance(hex string) (float64, error) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 0, fmt.Errorf("%q is not a #rrggbb colour", hex)
	}
	var ch [3]float64
	for i := range ch {
		v, err := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("%q is not a #rrggbb colour", hex)
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			ch[i] = c / 12.92
		} else {
			ch[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2], nil
}

func checkPairs(tok Tokens, pairs []pair) []string {
	var failures []string
	for _, theme := range []string{"dark", "light"} {
		vals := tok.Themes[theme]
		for _, p := range pairs {
			r, err := Contrast(vals[p.fg], vals[p.bg])
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %s on %s: %v", theme, p.fg, p.bg, err))
				continue
			}
			if r < p.min {
				failures = append(failures, fmt.Sprintf("%s: %s (%s) on %s (%s) is %.2f:1, below %.1f:1", theme, p.fg, vals[p.fg], p.bg, vals[p.bg], r, p.min))
			}
		}
	}
	return failures
}

func TestEveryDeclaredPairMeetsItsContrastInBothThemes(t *testing.T) {
	pairs := designPairs(t)
	if len(pairs) < 25 {
		t.Fatalf("read %d pairs from 03 §2b; the table did not parse", len(pairs))
	}
	tok, _ := readTokens(t)
	for _, f := range checkPairs(tok, pairs) {
		t.Error(f)
	}
}

// The check must be able to fail: a darkened text-secondary in either theme is caught.
func TestTheContrastCheckCatchesADarkenedToken(t *testing.T) {
	tok, _ := readTokens(t)
	pairs := designPairs(t)
	for theme, darker := range map[string]string{"dark": "#3b372e", "light": "#c9c2b0"} {
		bad := Tokens{Themes: map[string]map[string]string{"dark": maps.Clone(tok.Themes["dark"]), "light": maps.Clone(tok.Themes["light"])}}
		bad.Themes[theme]["--helm-text-secondary"] = darker
		if len(checkPairs(bad, pairs)) == 0 {
			t.Errorf("text-secondary set to %s in %s passed the contrast check", darker, theme)
		}
	}
}

func TestContrastMatchesKnownRatios(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{{"#000000", "#ffffff", 21}, {"#777777", "#ffffff", 4.48}, {"#f5b800", "#ffffff", 1.79}} {
		got, err := Contrast(c.a, c.b)
		if err != nil || math.Abs(got-c.want) > 0.05 {
			t.Errorf("Contrast(%s, %s) = %.2f (%v), want %.2f", c.a, c.b, got, err, c.want)
		}
	}
}
