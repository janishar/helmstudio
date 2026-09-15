package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunValidateExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, 2},
		{"valid manifests", []string{"../../studios/h3-studio.yaml", "../../studios/iris-studio.yaml"}, 0},
		{"one invalid manifest", []string{"../../internal/manifest/testdata/depends_on_unknown.yaml"}, 1},
		{"unreadable file", []string{"../../internal/manifest/testdata/does-not-exist.yaml"}, 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := runValidate(tt.args, &stdout, &stderr)
			if got != tt.want {
				t.Fatalf("exit code = %d, want %d (stdout=%q stderr=%q)", got, tt.want, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunValidateJSONHasOneEntryPerFile(t *testing.T) {
	args := []string{
		"--json",
		"../../studios/h3-studio.yaml", // valid
		"../../internal/manifest/testdata/depends_on_unknown.yaml", // invalid
		"../../internal/manifest/testdata/does-not-exist.yaml",     // unreadable
	}
	var stdout, stderr bytes.Buffer
	got := runValidate(args, &stdout, &stderr)
	if got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}

	var results []struct {
		File   string `json:"file"`
		Errors []struct {
			Rule string `json:"rule"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput: %s", err, stdout.String())
	}
	if len(results) != len(args)-1 { // minus the --json flag
		t.Fatalf("expected %d result entries (one per input file, including the unreadable one), got %d: %+v",
			len(args)-1, len(results), results)
	}
	if results[0].Errors == nil {
		t.Fatalf("expected a clean manifest to serialise errors as [] not null")
	}
	if len(results[0].Errors) != 0 {
		t.Fatalf("expected the first (valid) manifest to have no errors, got %+v", results[0].Errors)
	}
	if len(results[2].Errors) == 0 || results[2].Errors[0].Rule != "read" {
		t.Fatalf("expected the unreadable file's entry to carry a 'read' error, got %+v", results[2])
	}
}

func TestRunValidateUsageOnNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runValidate(nil, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("expected usage text on stderr, got %q", stderr.String())
	}
}
