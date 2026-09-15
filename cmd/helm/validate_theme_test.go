package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func themeTree(t *testing.T, style string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"static/style.css":                   style,
		"static/vendor/helm/helm-tokens.css": ":root { --helm-ground-page: #0d0c0a; }",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// -theme is advisory and exits 0; -strict fails on any finding (M6 Q17).
func TestValidateThemeIsAdvisoryUnlessStrict(t *testing.T) {
	dirty := themeTree(t, ".take { color: #ffb454; font-family: Menlo; }")
	clean := themeTree(t, ".take { color: var(--helm-studio-accent); font: var(--helm-type-mono); }")
	for _, tc := range []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"advisory", []string{"-theme", dirty}, 0, "theme advisory: 1 colour literals, 1 font families"},
		{"strict", []string{"-theme", dirty, "-strict"}, 1, "theme invalid"},
		{"clean strict", []string{"-theme", clean, "-strict"}, 0, "theme ok"},
		{"strict without theme", []string{"-strict", "x.yaml"}, 2, ""},
		{"missing dir", []string{"-theme", filepath.Join(clean, "nope")}, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runValidate(tc.args, &stdout, &stderr); got != tc.code {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, tc.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.out) {
				t.Errorf("stdout %q does not contain %q", stdout.String(), tc.out)
			}
			if tc.out != "" && !strings.Contains(stdout.String(), "colours set from JavaScript are not checked") {
				t.Errorf("stdout does not say JavaScript is not checked: %q", stdout.String())
			}
		})
	}
}

func TestValidateThemeJSONNamesEachFinding(t *testing.T) {
	dir := themeTree(t, ".take {\n  color: #ffb454;\n}")
	var stdout, stderr bytes.Buffer
	if code := runValidate([]string{"-json", "-theme", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Manifests []any `json:"manifests"`
		Theme     struct {
			Files    int `json:"files"`
			Findings []struct {
				File string `json:"file"`
				Line int    `json:"line"`
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"findings"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, stdout.String())
	}
	f := got.Theme.Findings
	if got.Theme.Files != 1 || len(f) != 1 || f[0].File != "static/style.css" || f[0].Line != 2 || f[0].Kind != "colour" || f[0].Text != "#ffb454" {
		t.Errorf("theme report = %+v", got.Theme)
	}
}
