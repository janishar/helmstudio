package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderPage(t *testing.T, base, src string) (Rendered, error) {
	t.Helper()
	samples := t.TempDir()
	if err := os.MkdirAll(filepath.Join(samples, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(samples, "a", "run.sh"), []byte("echo <hello>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return newRenderer(base, samples).render([]byte(src))
}

func TestCodeIsNeverWrittenInline(t *testing.T) {
	for _, fence := range []string{"```", "~~~", "  ```sh"} {
		_, err := renderPage(t, "/", "# Page\n\n"+fence+"\necho hi\n```\n")
		if err == nil || !strings.Contains(err.Error(), "@sample") {
			t.Errorf("an inline %q block: %v; want it refused, pointing at @sample", fence, err)
		}
	}
}

func TestAnUnknownDirectiveIsRefused(t *testing.T) {
	if _, err := renderPage(t, "/", "# Page\n\n@smaple a/run.sh\n"); err == nil || !strings.Contains(err.Error(), "not a directive") {
		t.Errorf("a misspelt directive: %v; want it refused", err)
	}
	if _, err := renderPage(t, "/", "# Page\n\n@sample a/missing.sh\n"); err == nil {
		t.Error("a sample that is not a file was included")
	}
}

func TestASampleSaysWhereItIsAndWhetherItRuns(t *testing.T) {
	r, err := renderPage(t, "/", "# Page\n\n@sample a/run.sh not-run: it needs the network\n\n@sample a/run.sh\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Samples) != 2 || r.Samples[0].NotRun != "it needs the network" || r.Samples[1].NotRun != "" {
		t.Errorf("samples = %+v", r.Samples)
	}
	for _, want := range []string{
		`<span class="helm-mono">a/run.sh</span>`,
		`<span class="not-run">Not run by the gate: it needs the network</span>`,
		`echo &lt;hello&gt;`,
	} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("the page has no %s:\n%s", want, r.HTML)
		}
	}
	if strings.Count(r.HTML, "Not run by the gate") != 1 {
		t.Errorf("the mark was not on exactly the one sample:\n%s", r.HTML)
	}
}

func TestInternalLinksAreWrittenUnderTheBase(t *testing.T) {
	r, err := renderPage(t, "/helmstudio/", "# Page\n\n[q](/docs/quickstart/#6-run-it) [out](https://example.com/) [rel](other/)\n\n<script>alert(1)</script>\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/helmstudio/docs/quickstart/#6-run-it"`, `href="https://example.com/"`, `href="other/"`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("no %s in:\n%s", want, r.HTML)
		}
	}
	if strings.Contains(r.HTML, "<script>") {
		t.Errorf("raw HTML was rendered:\n%s", r.HTML)
	}
	if len(r.Internal) != 1 || r.Internal[0] != "/docs/quickstart/#6-run-it" {
		t.Errorf("internal links = %v", r.Internal)
	}
	if r.Title != "Page" {
		t.Errorf("title = %q", r.Title)
	}
}

// @capabilities and @criteria are the software's own tables.
func TestTheTablesAreTheSoftwares(t *testing.T) {
	samples := filepath.Join(siteDir, "samples")
	r, err := newRenderer("/", samples).render([]byte("# Page\n\n@capabilities\n\n@criteria hello-studio/helmstudio.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<code>gallery.read_all</code>",
		"Can read everything you have ever made, in every studio.",
		"This manifest: 5 of 7 checkable pass",
		"Not checked: needs the smoke harness, which builds and runs the studio.",
		"<strong>Fails:</strong> test.profile is not declared",
	} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("no %q in:\n%s", want, r.HTML)
		}
	}
	if len(r.Samples) != 1 || r.Samples[0].Path != "hello-studio/helmstudio.yaml" {
		t.Errorf("the scored manifest is not counted as an included sample: %+v", r.Samples)
	}
}
