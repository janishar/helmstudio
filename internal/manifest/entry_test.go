package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest that validates, to build variations from. Kept minimal on
// purpose: every field below is one the rules under test actually read.
const goodManifest = `
id: wan-studio
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
processes:
  - name: studio
    role: main
    cmd: "./dist/wan --port {port}"
    port: { prefer: 8790 }
    health: { tcp: true, timeout_s: 60 }
`

func TestDetectKindReadsShapeNotFilename(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want Kind
	}{
		{"a manifest", goodManifest, KindManifest},
		{"a bare pointer", "id: a-studio\nrepo: https://example.com/a\nref: v1\n", KindPointer},
		{"a pointer with an inline manifest", "id: a-studio\nrepo: https://example.com/a\nref: v1\nmanifest:\n  name: a\n", KindPointer},
		{"not YAML at all", "\tthis: [is: not", KindUnknown},
		{"a bare string", "hello", KindUnknown},
		// A half-written manifest is a manifest, so the person gets the
		// errors for the document they were writing.
		{"a manifest missing everything but its name", "name: half a studio\n", KindManifest},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectKind([]byte(c.text)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The rule JSON Schema cannot express: two copies of one fact must agree.
func TestAnInlineManifestMustAgreeWithItsPointer(t *testing.T) {
	// The inline manifest, with its three shared fields parameterised.
	inline := func(id, repo, ref string) []byte {
		body := goodManifest
		body = strings.Replace(body, "id: wan-studio", "id: "+id, 1)
		body = strings.Replace(body, "repo: https://github.com/someone/wan-studio", "repo: "+repo, 1)
		body = strings.Replace(body, "ref: v0.3.1", "ref: "+ref, 1)
		return []byte("id: wan-studio\nrepo: https://github.com/someone/wan-studio\nref: v0.3.1\nmanifest:\n" +
			indent(strings.TrimPrefix(body, "\n"), "  "))
	}

	agreeing := inline("wan-studio", "https://github.com/someone/wan-studio", "v0.3.1")
	res, err := ValidateEntryBytes("agreeing.yaml", agreeing)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("an entry whose inline manifest agrees with it should validate: %v", res.Errors)
	}

	for _, c := range []struct {
		name, field string
		text        []byte
	}{
		{"a different id", "id", inline("other-studio", "https://github.com/someone/wan-studio", "v0.3.1")},
		{"a different repo", "repo", inline("wan-studio", "https://github.com/someone-else/wan-studio", "v0.3.1")},
		{"a different ref", "ref", inline("wan-studio", "https://github.com/someone/wan-studio", "v9.9.9")},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := ValidateEntryBytes("disagreeing.yaml", c.text)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK() {
				t.Fatalf("an inline %s differing from the pointer's own should be refused", c.field)
			}
			var found bool
			for _, e := range res.Errors {
				if e.Rule == "entry-agreement" && strings.Contains(e.Pointer, c.field) {
					found = true
				}
			}
			if !found {
				t.Errorf("want an entry-agreement error naming %s, got %v", c.field, res.Errors)
			}
		})
	}
}

// An inline manifest is held to every rule a standalone one is — it stands in
// for the file in the studio's own repository, so it cannot be a weaker thing.
func TestAnInlineManifestIsHeldToTheSemanticRules(t *testing.T) {
	// Two main processes: rule 4, which no schema keyword can express.
	broken := "id: a-studio\nrepo: https://example.com/a\nref: v1\nmanifest:\n" +
		indent(strings.TrimPrefix(goodManifest, "\n"), "  ") +
		"    - name: second\n      role: main\n      cmd: \"./b\"\n      health: { tcp: true, timeout_s: 60 }\n"
	broken = strings.Replace(broken, "  id: wan-studio", "  id: a-studio", 1)
	broken = strings.Replace(broken, "  repo: https://github.com/someone/wan-studio", "  repo: https://example.com/a", 1)
	broken = strings.Replace(broken, "  ref: v0.3.1", "  ref: v1", 1)

	res, err := ValidateEntryBytes("two-mains.yaml", []byte(broken))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("an inline manifest with two main processes should be refused")
	}
	for _, e := range res.Errors {
		if !strings.HasPrefix(e.Pointer, "/manifest") {
			t.Errorf("an error about the inline manifest should point inside it, got %q", e.Pointer)
		}
	}
}

// Q14: nothing escapes. Each of these passed validation before M7 and failed
// somewhere later — at download, or not at all.
func TestNothingEscapesItsRoot(t *testing.T) {
	withWeight := func(dest string) []byte {
		return []byte(goodManifest + "weights:\n  - name: w\n    repo: org/model\n    dest: " + dest + "\n")
	}
	for _, c := range []struct {
		name, dest string
		refused    bool
	}{
		{"a dest above the models root", "../x", true},
		{"a dest far above it", "a/../../..", true},
		{"a dest that is the models root itself", ".", true},
		{"an absolute dest", "/tmp/models", true},
		{"a dest that only looks like an escape", "..cache/model", false},
		{"an ordinary dest", "wan-2.1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := ValidateBytes("w.yaml", withWeight(c.dest))
			if err != nil {
				t.Fatal(err)
			}
			refused := hasRule(res, "dest-within-models")
			if refused != c.refused {
				t.Errorf("dest %q: refused=%v, want %v (%v)", c.dest, refused, c.refused, res.Errors)
			}
		})
	}

	for _, c := range []struct {
		name, smoke string
		refused     bool
	}{
		{"a smoke test above the studio root", "../evil.sh", true},
		{"an absolute smoke test", "/usr/bin/true", true},
		{"an ordinary smoke test", "scripts/smoke.sh", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := ValidateBytes("s.yaml", []byte(goodManifest+"test:\n  smoke: "+c.smoke+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if refused := hasRule(res, "smoke-within-root"); refused != c.refused {
				t.Errorf("smoke %q: refused=%v, want %v (%v)", c.smoke, refused, c.refused, res.Errors)
			}
		})
	}
}

// One validator (Q17). Every file both paths can see must get the same verdict
// and the same errors, because the day they differ is the day `helm validate`
// passes something the daemon refuses to load — or worse, the other way round.
func TestTheFileAndBytesPathsAgreeEverywhere(t *testing.T) {
	var files []string
	for _, dir := range []string{"testdata", filepath.Join("..", "..", "studios")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || (filepath.Ext(e.Name()) != ".yaml" && filepath.Ext(e.Name()) != ".yml") {
				continue
			}
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) < 5 {
		t.Fatalf("expected a corpus to compare, found %d files", len(files))
	}

	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			kind := DetectKind(data)

			fromFile, ferr := Validate(f)
			fromBytes, berr := ValidateBytes(f, data)
			if kind == KindPointer {
				// A pointer is not a manifest; both paths must say so the
				// same way, which is what the envelope path is compared on.
				fromFile, ferr = ValidateEntryBytes(f, data)
				fromBytes, berr = ValidateEntryBytes(f, data)
			}
			if (ferr == nil) != (berr == nil) {
				t.Fatalf("one path errored and the other did not: %v vs %v", ferr, berr)
			}
			if fromFile.OK() != fromBytes.OK() {
				t.Fatalf("verdicts differ: file OK=%v, bytes OK=%v", fromFile.OK(), fromBytes.OK())
			}
			if len(fromFile.Errors) != len(fromBytes.Errors) {
				t.Fatalf("error counts differ: %d vs %d", len(fromFile.Errors), len(fromBytes.Errors))
			}
			for i := range fromFile.Errors {
				if fromFile.Errors[i] != fromBytes.Errors[i] {
					t.Errorf("error %d differs:\n file:  %+v\n bytes: %+v", i, fromFile.Errors[i], fromBytes.Errors[i])
				}
			}
		})
	}
}

func hasRule(res Result, rule string) bool {
	for _, e := range res.Errors {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func indent(s, with string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = with + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// loadInlineManifest reads a registry entry and returns the manifest it
// carries, for tests written against studios/*.yaml before those files became
// pointers (M7 Q4).
func loadInlineManifest(t *testing.T, file string) (*Manifest, Result, error) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, Result{}, err
	}
	e, res, err := LoadEntryBytes(file, data)
	if err != nil || !res.OK() {
		return nil, res, err
	}
	if e.Manifest == nil {
		t.Fatalf("%s carries no inline manifest", file)
	}
	return e.Manifest, res, nil
}
