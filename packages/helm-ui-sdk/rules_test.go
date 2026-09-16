package helmui_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/themelint"
	helmcss "github.com/janishar/helmstudio/packages/helm-css"
	helmui "github.com/janishar/helmstudio/packages/helm-ui-sdk"
)

// The rules that stop the three packages rotting (docs/design/04-packages.md
// §11). Each is a test rather than a convention, because every one of them is
// the kind of thing that is true on the day it is written and quietly false
// four commits later.

// tags are the elements this package registers.
var tags = []string{"helm-terminal", "helm-gallery", "helm-player", "helm-timeline"}

func sources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for served, src := range helmui.Served {
		b, err := fs.ReadFile(helmui.Files, src)
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		out[served] = string(b)
	}
	if len(out) == 0 {
		t.Fatal("no component sources are embedded")
	}
	return out
}

// Rule 2: only the runtime SDK knows the wire. No URL, header or response
// shape appears anywhere in helm-ui-sdk — this is the milestone's own grep,
// run where it cannot be forgotten.
func TestNoComponentKnowsTheWire(t *testing.T) {
	forbidden := regexp.MustCompile(`fetch\(|XMLHttpRequest|EventSource|/api/v1|Authorization|Bearer|hs_live_`)
	for name, src := range sources(t) {
		for i, line := range strings.Split(code(src), "\n") {
			if m := forbidden.FindString(line); m != "" {
				t.Errorf("%s:%d names %q; only helm-runtime-sdk may know the wire (04 §11 rule 2)\n\t%s", name, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

// Rule 3: components receive a client, never build one. Nothing in the package
// may import another package at all: a component's whole dependency is the
// object it is handed.
func TestNoComponentConstructsAClient(t *testing.T) {
	constructs := regexp.MustCompile(`\b(fromEnv|connect|new Client|new Transport|new LauncherClient)\b`)
	imports := regexp.MustCompile(`(?m)^\s*(?:import|export)[^;]*\bfrom\s+"([^"]+)"`)
	for name, src := range sources(t) {
		if m := constructs.FindString(code(src)); m != "" {
			t.Errorf("%s calls %s; a component receives a client and never constructs one (04 §11 rule 3)", name, m)
		}
		for _, m := range imports.FindAllStringSubmatch(code(src), -1) {
			if !strings.HasPrefix(m[1], "./") {
				t.Errorf("%s imports %q; helm-ui-sdk imports nothing outside itself", name, m[1])
			}
		}
	}
}

// Rule 1, one way: the runtime SDK must not import a component.
func TestTheRuntimeSDKDoesNotImportAComponent(t *testing.T) {
	root := filepath.Join("..", "helm-runtime-sdk")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".js", ".py", ".go":
		default:
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range []string{"helm-ui-sdk", "@helmstudio/ui", "helm-ui.js"} {
			if strings.Contains(string(b), needle) {
				t.Errorf("%s names %q; the runtime SDK must not know a component exists (04 §11 rule 1)", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Rule 1, the other way: helm-css must not assume a component's markup. Its
// own .helm-terminal class is a surface a studio puts on its own <pre>, which
// is not the same thing as styling <helm-terminal>.
func TestHelmCSSDoesNotStyleAComponent(t *testing.T) {
	elementSelector := map[string]*regexp.Regexp{}
	for _, tag := range tags {
		elementSelector[tag] = regexp.MustCompile(`(^|[\s,>+~(])` + tag + `\b`)
	}
	for _, layer := range helmcss.Layers {
		b, err := fs.ReadFile(helmcss.Files, layer)
		if err != nil {
			t.Fatalf("reading %s: %v", layer, err)
		}
		for tag, re := range elementSelector {
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s styles <%s> at byte %d; helm-css must not assume a component's markup exists (04 §11 rule 1)", layer, tag, loc[0])
			}
		}
	}
}

// Tokens are the API (03 §5). A component that reaches for a token helm-css
// does not define renders unthemed, and nothing else would catch it: the
// custom property simply resolves to nothing.
func TestEveryTokenAComponentUsesExists(t *testing.T) {
	b, err := fs.ReadFile(helmcss.Files, "tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var toks helmcss.Tokens
	if err := json.Unmarshal(b, &toks); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, n := range toks.Names() {
		known[n] = true
	}
	use := regexp.MustCompile(`var\((--helm-[a-z0-9-]+)`)
	for name, src := range sources(t) {
		for _, m := range use.FindAllStringSubmatch(src, -1) {
			if !known[m[1]] {
				t.Errorf("%s uses %s, which helm-css does not define", name, m[1])
			}
		}
	}
}

// A component wears the theme; it does not carry a palette. The same lint
// `helm validate -theme` holds a studio to (M6 Q17) is applied to the styles
// the components ship.
func TestComponentStylesHoldNoColourLiteral(t *testing.T) {
	for name, src := range sources(t) {
		for _, f := range themelint.CSS(name, styleBlocks(src)) {
			t.Errorf("%s: %s: %s", name, f.Kind, f.Text)
		}
	}
}

// stylesLiteral is the one template literal a component ships its CSS in.
var stylesLiteral = regexp.MustCompile("(?s)const styles = `(.*?)`")

// styleBlocks pulls that CSS out, so the lint reads declarations rather than
// JavaScript. Matching the binding by name rather than splitting on backticks
// keeps a template literal elsewhere in the file — a filename, a label — from
// being read as a stylesheet.
func styleBlocks(src string) string {
	var b strings.Builder
	for _, m := range stylesLiteral.FindAllStringSubmatch(src, -1) {
		b.WriteString(m[1])
		b.WriteString("\n")
	}
	return b.String()
}

// code removes whole-line comments. Every rule here is about what the package
// executes: a doc comment showing a studio author the three lines to put on
// their own page is documentation, not a dependency. Only lines that are
// entirely comment are dropped, so nothing can be hidden behind one.
func code(src string) string {
	var b strings.Builder
	block := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case block:
			if strings.Contains(trimmed, "*/") {
				block = false
			}
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "/*"):
			block = !strings.Contains(trimmed, "*/")
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "//"), strings.HasPrefix(trimmed, "*"):
			b.WriteString("\n")
		default:
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Every component registers exactly one element, under the name 04 §5 gives
// it. A renamed tag is a breaking change in every studio's markup at once.
func TestEveryComponentIsRegisteredOnce(t *testing.T) {
	src := sources(t)
	var parts []string
	for _, s := range src {
		parts = append(parts, s)
	}
	all := strings.Join(parts, "\n")
	define := regexp.MustCompile(`define\("([a-z-]+)"`)
	seen := map[string]int{}
	for _, m := range define.FindAllStringSubmatch(all, -1) {
		seen[m[1]]++
	}
	for _, tag := range tags {
		if seen[tag] != 1 {
			t.Errorf("<%s> is registered %d times; want exactly one", tag, seen[tag])
		}
	}
	for tag := range seen {
		if !contains(tags, tag) {
			t.Errorf("<%s> is registered but is not one of the components 04 §5 names", tag)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
