package gen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// What every test in the package shares: where the repository is, and a helm
// built from it once.
var (
	repoRoot string
	siteDir  string
	helmBin  string
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	var err error
	if repoRoot, err = filepath.Abs(filepath.Join("..", "..")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	siteDir = filepath.Join(repoRoot, "site")
	tmp, err := os.MkdirTemp("", "helm-site-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	helmBin = filepath.Join(tmp, "helm")
	build := exec.Command("go", "build", "-o", helmBin, "./cmd/helm")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building helm for the site's tests: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// buildSite writes the site under base into a temporary directory.
func buildSite(t *testing.T, base string) (*Result, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	res, err := Build(Options{Root: repoRoot, Site: siteDir, Out: out, Base: base, CNAME: "helmstudio.in", Helm: helmBin})
	if err != nil {
		t.Fatalf("building the site under %s: %v", base, err)
	}
	return res, out
}
