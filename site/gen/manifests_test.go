package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An annotated manifest fails the build when the studio file changed since
// its notes were written, when a field has no note, and when a note names a
// field the file does not have.
func TestAnnotationsFailWhenOutOfStep(t *testing.T) {
	studio, err := os.ReadFile(filepath.Join(repoRoot, "studios", "iris-studio.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	notes, err := os.ReadFile(filepath.Join(siteDir, "annotations", "iris-studio.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	setup := func(t *testing.T, studioText, notesText string) Options {
		root, site := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "studios"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(site, "annotations"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "studios", "iris-studio.yaml"), []byte(studioText), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(site, "annotations", "iris-studio.yaml"), []byte(notesText), 0o644); err != nil {
			t.Fatal(err)
		}
		return Options{Root: root, Site: site}
	}

	if _, err := annotatedManifest(setup(t, string(studio), string(notes)), "iris-studio"); err != nil {
		t.Fatalf("the committed notes: %v", err)
	}

	changed := string(studio) + "# a comment added upstream\n"
	if _, err := annotatedManifest(setup(t, changed, string(notes)), "iris-studio"); err == nil || !strings.Contains(err.Error(), "changed since its notes were written") {
		t.Errorf("a changed studio file: %v", err)
	}

	withoutNote := strings.Replace(string(notes), "  /manifest/kinds:", "  /manifest/kinds-was-here:", 1)
	_, err = annotatedManifest(setup(t, string(studio), withoutNote), "iris-studio")
	if err == nil || !strings.Contains(err.Error(), "fields with no note: /manifest/kinds") {
		t.Errorf("a field with no note: %v", err)
	}

	unknown := string(notes) + "  /manifest/processes/0/nope: \"a field the file does not have\"\n"
	if _, err := annotatedManifest(setup(t, string(studio), unknown), "iris-studio"); err == nil || !strings.Contains(err.Error(), "a note for /manifest/processes/0/nope") {
		t.Errorf("a note for a field that is not there: %v", err)
	}
}
