package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Second review #1: the manifest schema is embedded, so a binary built with
// -trimpath and run far from the source tree validates exactly as a checkout
// does. The embedded provider and helm dev load manifests the same way.
func TestTrimpathBinaryValidatesWithoutTheSourceTree(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "helm")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -trimpath: %v\n%s", err, out)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "studios", "h3-studio.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "h3-studio.yaml")
	os.WriteFile(manifest, src, 0o644)
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("id: Not A Slug\n"), 0o644)

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HELM_SCHEMA_PATH=")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("validate", manifest); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("a trimpath binary could not validate a good manifest: %v\n%s", err, out)
	}
	if out, err := run("validate", bad); err == nil || strings.Contains(out, "schema not found") {
		t.Fatalf("a trimpath binary on a bad manifest: %v\n%s", err, out)
	}
}
