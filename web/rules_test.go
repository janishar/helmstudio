package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What the launcher may not do (03 §13a, M7 Q17; M7b's definition of done).
//
// **The page never parses YAML.** It sends text to the daemon and gets back
// errors with lines and pointers, the criteria, and the document as JSON. A
// JavaScript YAML parser would be a second implementation of the contract, and
// its disagreements with `yaml.v3` — anchors, merge keys, `on` and `yes`,
// duplicate keys — would show one manifest on screen and validate another.
//
// What these rules can prove: that no parser arrives as a dependency, and that
// nothing here calls one. What they cannot prove is that nobody ever writes
// twenty lines of `split(":")` and calls it reading a manifest. That is what
// review is for, and it is worth saying plainly rather than implying the
// grep is a proof.

// sources reads every .js file the launcher ships.
func sources(t *testing.T) map[string]string {
	t.Helper()
	names, err := filepath.Glob("*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 5 {
		t.Fatalf("found %d launcher modules; the layout has changed", len(names))
	}
	out := map[string]string{}
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(b)
	}
	return out
}

// code strips whole-line comments, so a rule cannot be tripped by prose that
// discusses the thing it forbids — every comment above is about YAML parsers.
// Only whole lines, so nothing can hide behind a trailing comment.
func code(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

var importFrom = regexp.MustCompile(`(?m)^\s*(?:import|export)[^;]*?\bfrom\s+"([^"]+)"`)

// A YAML parser would arrive the way any dependency does: as an import. The
// launcher has no bundler and no package manager, so every import is either
// one of its own files or something the daemon already serves.
func TestTheLauncherImportsNothingItDoesNotAlreadyShip(t *testing.T) {
	for name, src := range sources(t) {
		for _, m := range importFrom.FindAllStringSubmatch(code(src), -1) {
			spec := m[1]
			switch {
			case strings.HasPrefix(spec, "./") && strings.HasSuffix(spec, ".js"):
				if _, err := os.Stat(strings.TrimPrefix(spec, "./")); err != nil {
					t.Errorf("%s imports %q, which is not here", name, spec)
				}
			case strings.HasPrefix(spec, "/sdk/v1/"):
				// helm-css and the runtime, which the daemon serves itself.
			default:
				t.Errorf("%s imports %q. The launcher ships no dependencies: a module from anywhere else is a second implementation of something the daemon already does", name, spec)
			}
		}
	}
}

var yamlCall = regexp.MustCompile(`\b(?:YAML|yaml|jsyaml|jsYaml)\s*\.\s*(?:parse|load|safeLoad|loadAll|dump)\b|\bparseYAML\b|\bparseYaml\b`)

// And nothing calls one, however it got here.
func TestNothingInTheLauncherParsesYAML(t *testing.T) {
	for name, src := range sources(t) {
		if m := yamlCall.FindString(code(src)); m != "" {
			t.Errorf("%s calls %s. Every verdict on a manifest comes from the daemon: it is the only thing here that has read the document", name, m)
		}
	}
}

// The editor's text is a document the daemon owns. The page holds it, shows it
// and sends it back; the moment it starts reading fields out of it there are
// two answers to "what does this manifest say".
func TestTheEditorReadsFieldsOnlyFromTheDecodedDocument(t *testing.T) {
	src, ok := sources(t)["editor.js"]
	if !ok {
		t.Fatal("web/editor.js is gone")
	}
	bad := regexp.MustCompile(`valueAt\(\s*(?:st\.)?text\b|st\.text\s*\.\s*(?:split|match|replace)\b`)
	if m := bad.FindString(code(src)); m != "" {
		t.Errorf("editor.js reads the manifest text directly (%s). The decoded document comes back with every validation; that is the one to read", m)
	}
}
