package manifest

import (
	"strings"
	"testing"
)

const commented = `# A studio someone wrote by hand.
id: wan-studio          # the slug, immutable once published
name: wan studio
kinds: [video]
repo: https://github.com/someone/wan-studio
ref: v0.3.1
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [metal]

# The one process. Everything else is derived from it.
processes:
  - name: studio
    role: main
    cmd: "./dist/wan --port {port}"
    port: { prefer: 8790 }
    health: { tcp: true, timeout_s: 60 }
`

// The whole reason this is a node-tree edit rather than a decode and re-encode:
// a person who opens the editor to change one port and gets their comments
// deleted will not open it twice.
func TestAnEditKeepsTheCommentsAroundIt(t *testing.T) {
	out, err := Edit([]byte(commented), "/name", "wan studio 2")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, comment := range []string{
		"# A studio someone wrote by hand.",
		"# the slug, immutable once published",
		"# The one process. Everything else is derived from it.",
	} {
		if !strings.Contains(got, comment) {
			t.Errorf("the edit lost a comment: %q\n%s", comment, got)
		}
	}
	if !strings.Contains(got, "wan studio 2") {
		t.Errorf("the edit did not take:\n%s", got)
	}
	// And the edited document is still one the validator accepts.
	if res, err := ValidateBytes("edited.yaml", out); err != nil || !res.OK() {
		t.Errorf("the edited document no longer validates: %v %v", res.Errors, err)
	}
}

func TestEditingReachesIntoListsAndMappings(t *testing.T) {
	out, err := Edit([]byte(commented), "/processes/0/cmd", "./dist/wan --port {port} --verbose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "--verbose") {
		t.Fatalf("the nested edit did not take:\n%s", out)
	}
	m, res, err := LoadBytes("edited.yaml", out)
	if err != nil || !res.OK() {
		t.Fatalf("the edited document does not load: %v %v", res.Errors, err)
	}
	if got := m.EffectiveProcesses()[0].Cmd; !strings.HasSuffix(got, "--verbose") {
		t.Errorf("cmd is %q", got)
	}
}

// Key order is the author's. Re-encoding from a struct would reorder every
// field to whatever order the type happens to declare.
func TestAnEditKeepsKeyOrder(t *testing.T) {
	out, err := Edit([]byte(commented), "/ref", "v0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"id:", "name:", "kinds:", "repo:", "ref:", "requires:", "runtime:", "processes:"}
	at := -1
	for _, key := range order {
		i := strings.Index(string(out), "\n"+key)
		if i < 0 {
			i = strings.Index(string(out), key)
		}
		if i < at {
			t.Fatalf("%s moved; key order is the author's:\n%s", key, out)
		}
		at = i
	}
}

func TestAddingAndRemovingFields(t *testing.T) {
	added, err := Edit([]byte(commented), "/description", "A studio someone else wrote.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(added), "A studio someone else wrote.") {
		t.Fatalf("the field was not added:\n%s", added)
	}

	removed, err := Edit(added, "/description", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed), "someone else wrote") {
		t.Fatalf("the field was not removed:\n%s", removed)
	}
	// Removing what is not there is what was asked for, not an error.
	if _, err := Edit(removed, "/description", nil); err != nil {
		t.Errorf("removing an absent field should be a no-op, got %v", err)
	}
}

// Guessing at the shape of what is missing is how an editor writes something
// nobody asked for.
func TestEditingInsideSomethingAbsentIsRefused(t *testing.T) {
	if _, err := Edit([]byte(commented), "/python/version", "3.12"); err == nil {
		t.Error("editing inside an absent block should be refused")
	}
	if _, err := Edit([]byte(commented), "/processes/7/cmd", "x"); err == nil {
		t.Error("an index outside the list should be refused")
	}
	if _, err := Edit([]byte(commented), "/processes/first/cmd", "x"); err == nil {
		t.Error("a name where a list wants an index should be refused")
	}
	if _, err := Edit([]byte(commented), "name", "x"); err == nil {
		t.Error("a pointer without a leading slash should be refused")
	}
}

// An anchor is shared by definition, so editing through one would change every
// place it is used — which is never what the person editing one field meant.
func TestEditingThroughAnAnchorIsRefused(t *testing.T) {
	anchored := "common: &c\n  timeout_s: 60\nfirst: *c\nsecond: *c\n"
	_, err := Edit([]byte(anchored), "/first/timeout_s", 90)
	if err == nil {
		t.Fatal("editing through an alias should be refused")
	}
	if !strings.Contains(err.Error(), "anchor") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

// A flow-style value stays flow-style: these manifests write their weights one
// per line, and reformatting them all is not an edit anybody asked for.
func TestFlowStyleSurvives(t *testing.T) {
	out, err := Edit([]byte(commented), "/processes/0/port", map[string]any{"prefer": 8791})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "{prefer: 8791}") {
		t.Errorf("a flow mapping should stay on one line:\n%s", out)
	}
}

// The first field typed into a new manifest has to land somewhere.
func TestEditingAnEmptyDocumentStartsOne(t *testing.T) {
	out, err := Edit(nil, "/id", "new-studio")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "id: new-studio") {
		t.Errorf("got %q", out)
	}
}
