package manifest

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every manifest actually shipped in studios/ must validate clean. They are
// read from the real location rather than copied into testdata, so a change
// to one can never drift from what `make gate` checks. This does not pin the
// count at 4: `make gate`'s own validate target already runs the same glob,
// so a milestone that adds a fifth studio (M6) doesn't need to touch this
// test to keep it honest — it only needs a studio that's actually valid.
func TestRealManifestsValid(t *testing.T) {
	files, err := filepath.Glob("../../studios/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no studio manifests found in ../../studios")
	}
	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			res, err := Validate(f)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !res.OK() {
				t.Fatalf("expected valid, got errors: %v", res.Errors)
			}
		})
	}
}

// exactlyOne asserts the fixture produced precisely one error, with the
// given rule and a message containing want — not merely that *an* error
// with that rule exists somewhere in the list. Presence-only assertions are
// exactly what let the cycle-detector bug (a real, fixed bug: an early
// return left DFS state dirty across independent subtrees and printed
// "a -> b -> c -> d -> c" for two disjoint two-node cycles) pass a test that
// only checked "is there a no-cycle error mentioning arrows."
func exactlyOne(t *testing.T, errs []Error, rule, want string) {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Rule != rule {
		t.Fatalf("expected rule %q, got %q: %+v", rule, errs[0].Rule, errs[0])
	}
	if !strings.Contains(errs[0].Message, want) {
		t.Fatalf("expected message containing %q, got %+v", want, errs[0])
	}
}

func TestSemanticRules(t *testing.T) {
	tests := []struct {
		file string
		rule string
		want string // substring the single resulting error's Message must contain
	}{
		{"depends_on_unknown.yaml", "depends_on-known", `depends_on "engine"`},
		{"cycle_self.yaml", "no-cycle", "studio -> studio"},
		{"cycle_three.yaml", "no-cycle", "a -> b -> c -> a"},
		{"main_zero.yaml", "exactly-one-main", "no process has role: main"},
		{"main_two.yaml", "exactly-one-main", "2 processes have role: main"},
		{"heavy_two.yaml", "at-most-one-heavy", "2 processes are heavy"},
		{"models_unknown.yaml", "models-substitution", `{models.missing}`},
		{"models_selected_without_selectable.yaml", "models-substitution", "no weight is marked selectable"},
		{"models_selectable_unused.yaml", "models-substitution", `weight "w1" is selectable`},
		{"ports_unknown.yaml", "ports-substitution", `{ports.missing}`},
		{"cwd_escape.yaml", "cwd-within-root", "escapes the studio root"},
		{"build_cwd_escape.yaml", "cwd-within-root", "escapes the studio root"},
		{"import_cwd_escape.yaml", "cwd-within-root", "escapes the studio root"},
		{"cwd_absolute.yaml", "cwd-within-root", "is absolute"},
		{"duplicate_process_name.yaml", "unique-names", `process name "studio" is also used at /processes/0`},
		{"duplicate_weight_name.yaml", "unique-names", `weight name "w1" is also used at /weights/0`},
		{"unknown_placeholder.yaml", "known-placeholder", `{prot}`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.file, func(t *testing.T) {
			res, err := Validate(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if res.OK() {
				t.Fatalf("expected an error, got none")
			}
			exactlyOne(t, res.Errors, tt.rule, tt.want)
		})
	}
}

// Two disjoint cycles (a<->b, c->d->c) must each be reported once, correctly
// — not merged, not misattributed to nodes outside them. This is the
// regression test for the DFS state-leak bug: the buggy detector reported a
// second cycle as "a -> b -> c -> d -> c", pulling in a and b (not part of
// the c/d cycle at all) because their DFS frames were never popped.
func TestCycleDetectorHandlesDisjointCycles(t *testing.T) {
	res, err := Validate(filepath.Join("testdata", "cycle_two_disjoint.yaml"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("expected 2 cycle errors, got %d: %+v", len(res.Errors), res.Errors)
	}
	var got []string
	for _, e := range res.Errors {
		if e.Rule != "no-cycle" {
			t.Fatalf("expected rule no-cycle, got %+v", e)
		}
		got = append(got, e.Message)
	}
	wantAB := "dependency cycle: a -> b -> a"
	wantCD := "dependency cycle: c -> d -> c"
	if !(strings.Contains(got[0], wantAB) || strings.Contains(got[1], wantAB)) {
		t.Errorf("missing %q among %v", wantAB, got)
	}
	if !(strings.Contains(got[0], wantCD) || strings.Contains(got[1], wantCD)) {
		t.Errorf("missing %q among %v", wantCD, got)
	}
	// Neither report may pull in a node from the other cycle.
	for _, g := range got {
		if strings.Contains(g, "a -> b -> c") || strings.Contains(g, "b -> c -> d") {
			t.Errorf("cycle report crosses into the other cycle: %q", g)
		}
	}
}

// A cwd component that merely contains the substring ".." — a directory
// genuinely named "..cache" — must not be rejected. Only ruleCwdWithinRoot's
// actual path resolution proves this; a naive strings.Contains(cwd, "..")
// check would fail it.
func TestCwdDotDotInNameIsNotAnEscape(t *testing.T) {
	res, err := Validate(filepath.Join("testdata", "cwd_dotdot_name_ok.yaml"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.OK() {
		t.Fatalf("expected valid, got errors: %v", res.Errors)
	}
}

// A file with more than one "---"-separated YAML document must be rejected
// rather than silently validating only the first and dropping the second —
// yaml.Unmarshal's default behaviour, which this package deliberately does
// not inherit.
func TestMultiDocumentRejected(t *testing.T) {
	res, err := Validate(filepath.Join("testdata", "multi_document.yaml"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK() {
		t.Fatalf("expected an error, got none")
	}
	if res.Errors[0].Rule != "parse" || !strings.Contains(res.Errors[0].Message, "more than one YAML document") {
		t.Fatalf("expected a parse error about multiple documents, got %+v", res.Errors[0])
	}
}

func TestSchemaFailures(t *testing.T) {
	tests := []struct {
		file string
		rule string
		want string // substring expected in the single resulting error's Message
	}{
		{"schema_no_processes.yaml", "schema", "processes"},
		{"schema_both_port_modes.yaml", "schema", "port.prefer and port.fixed"},
		{"schema_two_health_probes.yaml", "schema", "more than one health probe"},
		{"schema_unknown_backend.yaml", "schema", "must be one of"},
		{"schema_no_source.yaml", "schema", "neither 'repo' nor 'local_path'"},
		{"schema_unknown_field.yaml", "schema", "turbo_mode"},
		{"schema_invalid_uri.yaml", "schema", "not valid"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.file, func(t *testing.T) {
			res, err := Validate(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if res.OK() {
				t.Fatalf("expected a schema error, got none")
			}
			for _, e := range res.Errors {
				if e.Rule != "schema" {
					t.Fatalf("expected a schema-rule error, got rule %q: %s", e.Rule, e.Message)
				}
			}
			found := false
			for _, e := range res.Errors {
				if strings.Contains(e.Message, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an error mentioning %q, got %+v", tt.want, res.Errors)
			}
		})
	}
}

// Every schema-level error carries a line number pointing somewhere inside
// the fixture file, not just a JSON pointer — PRD R6 asks for line numbers,
// and a pointer alone doesn't answer "where do I look."
func TestSchemaErrorsHaveLineNumbers(t *testing.T) {
	res, err := Validate(filepath.Join("testdata", "schema_both_port_modes.yaml"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Line == 0 {
		t.Fatalf("expected one error with a nonzero line number, got %+v", res.Errors)
	}
}
