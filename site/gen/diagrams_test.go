package gen

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSVG = `<svg viewBox="0 0 360 120" xmlns="http://www.w3.org/2000/svg" role="img">
  <title>How two things fit together</title>
  <desc>A box marked one, an arrow, and a box marked two.</desc>
  <rect class="d-box" x="8" y="8" width="100" height="40"/>
  <path class="d-edge" d="M108 28 H252"/>
  <rect class="d-box d-unbuilt" x="252" y="8" width="100" height="40"/>
</svg>`

// renderDiagram renders a page against one diagram file of the test's own.
func renderDiagram(t *testing.T, svg, src string) (Rendered, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	return newRenderer("/", t.TempDir(), dir).render([]byte(src))
}

func TestADiagramIsInlinedWithItsCaption(t *testing.T) {
	r, err := renderDiagram(t, goodSVG, "# Page\n\n@diagram d caption: Two things, and the arrow between them.\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Diagrams) != 1 || r.Diagrams[0] != "d" {
		t.Errorf("diagrams = %v; want the name, so the build can check every one is used", r.Diagrams)
	}
	for _, want := range []string{
		`<figure class="diagram">`,
		`<svg viewBox="0 0 360 120"`,
		`<title>How two things fit together</title>`,
		`class="d-box d-unbuilt"`,
		`<figcaption>Two things, and the arrow between them.</figcaption>`,
	} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("the page has no %s:\n%s", want, r.HTML)
		}
	}
	// It must be a block of its own, not swallowed into a paragraph.
	if strings.Contains(r.HTML, "<p><figure") {
		t.Errorf("the figure was folded into a paragraph:\n%s", r.HTML)
	}
}

// A diagram is markup inlined into a page, which is what this package
// otherwise refuses. Everything that makes that unsafe is refused by name.
func TestADiagramCarriesNothingButShapes(t *testing.T) {
	for _, c := range []struct{ name, svg, want string }{
		{"script", strings.Replace(goodSVG, "<rect", "<script>alert(1)</script><rect", 1), "a script"},
		{"handler", strings.Replace(goodSVG, `<rect class="d-box"`, `<rect onload="x()" class="d-box"`, 1), "an event handler"},
		{"foreignObject", strings.Replace(goodSVG, "<rect", "<foreignObject><b>hi</b></foreignObject><rect", 1), "embedded HTML"},
		{"stylesheet", strings.Replace(goodSVG, "<rect", "<style>.d-box{fill:red}</style><rect", 1), "a stylesheet"},
		{"link", strings.Replace(goodSVG, "<rect", `<a href="https://example.com"><rect`, 1), "a link"},
		{"inline style", strings.Replace(goodSVG, `<rect class="d-box"`, `<rect style="fill:#fff"`, 1), "an inline style"},
		{"hex colour", strings.Replace(goodSVG, `class="d-box"`, `class="d-box" data-x="#ff0000"`, 1), "a hex colour"},
		{"colour function", strings.Replace(goodSVG, `class="d-box"`, `class="d-box" data-x="rgb(1,2,3)"`, 1), "a colour function"},
		{"presentation colour", strings.Replace(goodSVG, `class="d-box"`, `class="d-box" fill="rebeccapurple"`, 1), "a presentation colour"},
		{"no title", strings.Replace(goodSVG, "<title>How two things fit together</title>", "", 1), "no <title>"},
		{"no desc", strings.Replace(goodSVG, "<desc>A box marked one, an arrow, and a box marked two.</desc>", "", 1), "no <desc>"},
		{"no viewBox", strings.Replace(goodSVG, `viewBox="0 0 360 120"`, `width="360"`, 1), "no viewBox"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := renderDiagram(t, c.svg, "# Page\n\n@diagram d caption: A caption.\n")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v; want it refused, saying %q", err, c.want)
			}
		})
	}
}

// fill="none" is a shape, not a colour, and an outlined box needs it.
func TestADiagramMayDeclareNoFill(t *testing.T) {
	svg := strings.Replace(goodSVG, `class="d-box"`, `class="d-box" fill="none"`, 1)
	if _, err := renderDiagram(t, svg, "# Page\n\n@diagram d caption: A caption.\n"); err != nil {
		t.Errorf(`fill="none": %v; want it allowed`, err)
	}
}

func TestADiagramMustExistAndBeCaptioned(t *testing.T) {
	if _, err := renderDiagram(t, goodSVG, "# Page\n\n@diagram missing caption: A caption.\n"); err == nil {
		t.Error("a diagram that is not a file was inlined")
	}
	// Without a caption it is not the directive, so it is an unknown one.
	if _, err := renderDiagram(t, goodSVG, "# Page\n\n@diagram d\n"); err == nil || !strings.Contains(err.Error(), "not a directive") {
		t.Errorf("an uncaptioned diagram: %v; want it refused", err)
	}
}

// A diagram nobody shows is a drawing nobody checked against the software.
func TestEveryDiagramIsOnAPage(t *testing.T) {
	res, _ := buildSite(t, "/")
	dir := filepath.Join(siteDir, "diagrams")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(p), ".svg")
		if filepath.Ext(p) != ".svg" {
			t.Errorf("diagrams/%s is not an .svg", filepath.Base(p))
			return nil
		}
		if len(res.Diagrams[name]) == 0 {
			t.Errorf("diagrams/%s.svg is on no page", name)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
