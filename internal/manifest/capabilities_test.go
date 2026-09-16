package manifest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The sentences are the security surface, so they are diffed against the
// design the way the design tokens are (docs/decisions.md M7 Q12).
//
// Without this, the two copies drift and the drift is invisible: both stay
// plausible English about the same capability, and the one a person actually
// reads is whichever the screen happens to use.
func TestCapabilitySentencesMatchTheDesign(t *testing.T) {
	design := readDesignTable(t)

	got := map[string]Capability{}
	for _, c := range KnownCapabilities() {
		got[c.Name] = c
	}

	for name, want := range design {
		if name == "__none__" {
			continue
		}
		c, ok := got[name]
		if !ok {
			t.Errorf("03 §18 names the capability %q and internal/manifest has no sentence for it", name)
			continue
		}
		if c.Sentence != want.Sentence {
			t.Errorf("%s:\n  design: %q\n  code:   %q", name, want.Sentence, c.Sentence)
		}
		if c.Warning != want.Warning {
			t.Errorf("%s: design marks warning=%v, code has %v", name, want.Warning, c.Warning)
		}
	}
	for name := range got {
		if _, ok := design[name]; !ok {
			t.Errorf("internal/manifest has a sentence for %q that 03 §18 does not name", name)
		}
	}

	if want := design["__none__"].Sentence; want != "" && want != NoCapabilities {
		t.Errorf("the none row:\n  design: %q\n  code:   %q", want, NoCapabilities)
	}
}

var designRow = regexp.MustCompile(`^\|\s*(.+?)\s*\|\s*(.+?)\s*\|$`)

// readDesignTable pulls the table out of 03 §18's "Capabilities, as sentences"
// section. A capability in bold is a warning, which is how the design marks
// the two that reach beyond a studio's own work.
func readDesignTable(t *testing.T) map[string]Capability {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "design", "03-design-system.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the design: %v", err)
	}
	text := string(b)
	start := strings.Index(text, "### Capabilities, as sentences")
	if start < 0 {
		t.Fatal("03 §18 has no \"Capabilities, as sentences\" section; the table that governs these sentences is gone")
	}
	section := text[start:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end]
	}

	out := map[string]Capability{}
	for _, line := range strings.Split(section, "\n") {
		m := designRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, sentence := m[1], m[2]
		if name == "Capability" || strings.HasPrefix(name, "---") {
			continue
		}
		warning := strings.HasPrefix(name, "**") && strings.HasSuffix(name, "**")
		name = strings.Trim(name, "*")
		name = strings.Trim(name, "`")
		if name == "*none*" || name == "none" {
			out["__none__"] = Capability{Sentence: sentence}
			continue
		}
		out[name] = Capability{Name: name, Sentence: sentence, Warning: warning}
	}
	if len(out) < 5 {
		t.Fatalf("parsed only %d rows from 03 §18; the table's shape changed", len(out))
	}
	return out
}

// Order is the table's, not the manifest's, so a manifest cannot bury the
// alarming capability at the bottom by declaring it last.
func TestSentencesComeInTheTablesOrder(t *testing.T) {
	got := CapabilitySentences([]string{"gallery.read_all", "kv"})
	if len(got) != 2 || got[0].Name != "kv" || got[1].Name != "gallery.read_all" {
		t.Fatalf("got %v, want kv then gallery.read_all", got)
	}
	if !got[1].Warning {
		t.Error("gallery.read_all should be marked a warning")
	}
}

// An unrecognised capability is shown, named as itself. Dropping it would hide
// exactly the thing worth looking at: a permission this build does not know.
func TestAnUnknownCapabilityIsShownNotDropped(t *testing.T) {
	got := CapabilitySentences([]string{"kv", "filesystem.write_all"})
	if len(got) != 2 {
		t.Fatalf("got %d sentences, want 2: %v", len(got), got)
	}
	last := got[len(got)-1]
	if last.Name != "filesystem.write_all" || !last.Warning {
		t.Errorf("an unknown capability should be shown as a warning, got %+v", last)
	}
}

// A studio that declares none gets a sentence of its own rather than a blank
// section, because "no capabilities" is information.
func TestNoCapabilitiesHasItsOwnSentence(t *testing.T) {
	if got := CapabilitySentences(nil); len(got) != 0 {
		t.Errorf("declaring nothing should produce no rows, got %v", got)
	}
	if NoCapabilities == "" {
		t.Error("a studio with no capabilities needs a sentence")
	}
}
