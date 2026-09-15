package themelint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func texts(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Kind+":"+f.Text)
	}
	return out
}

// Every literal kind docs/decisions.md M6 Q17 names is found, and nothing that
// only looks like one.
func TestColourLiteralsAreFoundInValuesOnly(t *testing.T) {
	src := `
#app, .red, a[href="#fff"] { color: var(--helm-text-primary); }
.a {
  color: #fff;
  background: #0b0e14cc;
  border-color: rgba(255,180,84,.12);
  outline-color: hsl(40 100% 50%);
  --mine: oklch(0.7 0.1 80);
  fill: color(display-p3 1 0 0);
  box-shadow: 0 0 0 1px RED;
  caret-color: hwb(0 0% 0%);
  text-decoration-color: lab(50 40 59);
  column-rule-color: lch(52 72 50);
  border-top-color: hsla(0,0%,0%,.1);
  scrollbar-color: white transparent;
}
.ok {
  color: transparent;
  background: currentColor;
  border: 1px solid var(--helm-border-hairline);
  background-image: url("data:image/svg+xml,%23fff") , url(#red);
  content: "#fff red";
  animation: tan 1s;
  grid-area: red;
  color: color-mix(in srgb, var(--helm-studio-accent) 12%, transparent);
  width: calc(100% - #{0}px);
  --helm-grid: 1fr;
}
`
	got := texts(CSS("s.css", src))
	want := []string{
		"colour:#fff", "colour:#0b0e14cc", "colour:rgba(255,180,84,.12)", "colour:hsl(40 100% 50%)",
		"colour:oklch(0.7 0.1 80)", "colour:color(display-p3 1 0 0)", "colour:RED", "colour:hwb(0 0% 0%)",
		"colour:lab(50 40 59)", "colour:lch(52 72 50)", "colour:hsla(0,0%,0%,.1)", "colour:white",
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestFontFamiliesMustBeTheHelmTokens(t *testing.T) {
	src := `
.a { font-family: var(--helm-font-sans); }
.b { font: var(--helm-type-body); }
.c { font: 600 14.5px/20px var(--helm-font-mono); }
.d { font: inherit; font-family: inherit; }
.e { font-family: -apple-system, "Helvetica Neue", sans-serif; }
.f { font: 400 13px/1.5 var(--sans); }
.g { font-family: var(--mono); }
.h { font: menu; }
@font-face { font-family: "IBM Plex Sans"; src: url(x.woff2); }
`
	got := texts(CSS("s.css", src))
	want := []string{`font:-apple-system, "Helvetica Neue", sans-serif`, "font:var(--sans)", "font:var(--mono)", "font:menu"}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestHTMLStyleElementsAndAttributesWithPositions(t *testing.T) {
	src := "<!doctype html>\n<style>\n  .x { color: #123456; }\n</style>\n<div data-style=\"color: red\" style=\"color: blue\"></div>\n"
	got := HTML("index.html", src)
	if len(got) != 2 {
		t.Fatalf("findings = %v", got)
	}
	if got[0].Text != "#123456" || got[0].Line != 3 || got[0].Col != 15 {
		t.Errorf("style element finding = %+v, want #123456 at 3:15", got[0])
	}
	if got[1].Text != "blue" || got[1].Line != 5 || got[1].Col != 44 {
		t.Errorf("style attribute finding = %+v, want blue at 5:44", got[1])
	}
}

func TestDirSkipsVendoredHelmCSSAndOtherFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("static/vendor/helm/helm-tokens.css", ":root { --helm-ground-page: #0d0c0a; }")
	write("vendor/helm/helm.css", ".x { color: #000; }")
	write("node_modules/x/a.css", ".x { color: #000; }")
	write(".git/a.css", ".x { color: #000; }")
	write("static/app.js", `el.style.color = "#fff"`)
	write("static/style.css", ".x {\n  color: #abc;\n}")
	rep, err := Dir(root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 || len(rep.Findings) != 1 || rep.Findings[0].File != "static/style.css" || rep.Findings[0].Line != 2 {
		t.Errorf("report = %+v, want one finding in static/style.css line 2", rep)
	}
	if !strings.Contains(rep.Findings[0].String(), "static/style.css:2:10:") {
		t.Errorf("finding reads %q", rep.Findings[0])
	}
}
