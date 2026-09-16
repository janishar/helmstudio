// Package packaging checks that the packages a studio author installs from a
// registry agree with each other and with what the repository releases
// (docs/releasing.md, docs/design/04-packages.md §8 and §9).
//
// A release is a tag on a commit that passed the gate, so everything a
// registry will be handed is checked here, where it can still be fixed: the
// versions each package declares, where each says it comes from, what npm
// would pack, and that a workflow publishes every package.
package packaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	helmcss "github.com/janishar/helmstudio/packages/helm-css"
	helmui "github.com/janishar/helmstudio/packages/helm-ui-sdk"
)

const (
	repositoryURL = "git+https://github.com/janishar/helmstudio.git"
	pythonDir     = "packages/helm-runtime-sdk/python"
	runtimeDir    = "packages/helm-runtime-sdk/node"
	uiDir         = "packages/helm-ui-sdk"
	cssDir        = "packages/helm-css"
)

// npmPackages are the three packages published to npm, by directory.
var npmPackages = map[string]string{
	runtimeDir: "@helmstudio/runtime",
	uiDir:      "@helmstudio/ui",
	cssDir:     "@helmstudio/css",
}

// inRepoModules are the Go modules the repository publishes by tag.
var inRepoModules = []string{
	"github.com/janishar/helmstudio",
	"github.com/janishar/helmstudio/packages/helm-runtime-sdk/go",
	"github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded",
}

type packageJSON struct {
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	License    string            `json:"license"`
	Style      string            `json:"style"`
	Exports    map[string]string `json:"exports"`
	Peers      map[string]string `json:"peerDependencies"`
	Repository struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Directory string `json:"directory"`
	} `json:"repository"`
	PublishConfig struct {
		Access string `json:"access"`
	} `json:"publishConfig"`
}

func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func readPackage(t *testing.T, dir string) packageJSON {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p packageJSON
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%s/package.json: %v", dir, err)
	}
	return p
}

func pyprojectVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), pythonDir, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "([^"]+)"$`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s/pyproject.toml declares no version", pythonDir)
	}
	return string(m[1])
}

// pep440 is a semver version in PEP 440's spelling: 1.0.0-rc.1 is 1.0.0rc1.
func pep440(v string) (string, error) {
	release, pre, found := strings.Cut(v, "-")
	if !found {
		return release, nil
	}
	label, n, ok := strings.Cut(pre, ".")
	short := map[string]string{"alpha": "a", "beta": "b", "rc": "rc"}[label]
	if _, err := strconv.Atoi(n); !ok || short == "" || err != nil {
		return "", fmt.Errorf("%s has no PEP 440 spelling; use -alpha.N, -beta.N or -rc.N", v)
	}
	return release + short + n, nil
}

// The runtime SDK's Go, Python and JavaScript clients are generated from one
// contract and released together, at one version, and the three Go modules
// are tagged at it (docs/decisions.md, M4 second review #1).
func TestTheRuntimeSDKIsOneVersionInEveryLanguage(t *testing.T) {
	version := readPackage(t, runtimeDir).Version
	want, err := pep440(version)
	if err != nil {
		t.Fatal(err)
	}
	if got := pyprojectVersion(t); got != want {
		t.Errorf("%s/package.json is %s, so pyproject.toml should be %s, and it is %s", runtimeDir, version, want, got)
	}

	var mods []string
	err = filepath.WalkDir(root(t), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".claude" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Name() == "go.mod" {
			mods = append(mods, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	require := regexp.MustCompile(`(?m)^(?:require\s+|\t)(github\.com/janishar/helmstudio(?:/[^\s]+)?)\s+(v\S+)`)
	for _, file := range mods {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root(t), file)
		for _, m := range require.FindAllSubmatch(b, -1) {
			module, got := string(m[1]), string(m[2])
			for _, published := range inRepoModules {
				if module == published && got != "v"+version {
					t.Errorf("%s requires %s %s; the runtime SDK is %s, and the three Go modules are tagged together at it", rel, module, got, version)
				}
			}
		}
	}
}

// helm-ui-sdk and helm-css carry their version in Go and in JavaScript as well
// as in package.json, and the three must not drift.
func TestTheVersionConstantsMatchTheirPackages(t *testing.T) {
	ui := readPackage(t, uiDir).Version
	if helmui.Version != ui {
		t.Errorf("helmui.Version is %s and %s/package.json is %s", helmui.Version, uiDir, ui)
	}
	index, err := os.ReadFile(filepath.Join(root(t), uiDir, "src", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	var declared string
	if m := regexp.MustCompile(`export const VERSION = "([^"]+)";`).FindSubmatch(index); m != nil {
		declared = string(m[1])
	}
	if declared != ui {
		t.Errorf("src/index.js's VERSION is %q and %s/package.json is %s", declared, uiDir, ui)
	}
	if css := readPackage(t, cssDir).Version; helmcss.Version != css {
		t.Errorf("helmcss.Version is %s and %s/package.json is %s", helmcss.Version, cssDir, css)
	}
}

// npm attaches provenance only when repository.url is this repository, a
// scoped package is private unless published public, and a registry page
// shows the README and the licence each package carries.
func TestEveryPackageSaysWhereItComesFrom(t *testing.T) {
	licence, err := os.ReadFile(filepath.Join(root(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	for dir, name := range npmPackages {
		p := readPackage(t, dir)
		switch {
		case p.Name != name:
			t.Errorf("%s is named %q, not %q", dir, p.Name, name)
		case p.Repository.Type != "git" || p.Repository.URL != repositoryURL || p.Repository.Directory != dir:
			t.Errorf("%s's repository is %+v; want git, %s, %s", dir, p.Repository, repositoryURL, dir)
		case p.PublishConfig.Access != "public":
			t.Errorf("%s would publish as a private package: publishConfig.access is %q", dir, p.PublishConfig.Access)
		case p.License != "MIT":
			t.Errorf("%s's licence is %q", dir, p.License)
		}
	}
	for _, dir := range []string{pythonDir, runtimeDir, uiDir, cssDir} {
		if b, err := os.ReadFile(filepath.Join(root(t), dir, "LICENSE")); err != nil || !bytes.Equal(b, licence) {
			t.Errorf("%s/LICENSE is not the repository's licence (%v)", dir, err)
		}
		if _, err := os.Stat(filepath.Join(root(t), dir, "README.md")); err != nil {
			t.Errorf("%s has no README for its registry page: %v", dir, err)
		}
	}
	pyproject, _ := os.ReadFile(filepath.Join(root(t), pythonDir, "pyproject.toml"))
	if !bytes.Contains(pyproject, []byte(`readme = "README.md"`)) {
		t.Errorf("%s/pyproject.toml does not name its README", pythonDir)
	}
}

// @helmstudio/ui's optional peers must accept the versions of css and runtime
// released beside it.
func TestTheComponentsAcceptTheirSiblings(t *testing.T) {
	ui := readPackage(t, uiDir)
	for peer, dir := range map[string]string{"@helmstudio/css": cssDir, "@helmstudio/runtime": runtimeDir} {
		rng, ok := ui.Peers[peer]
		if !ok {
			t.Errorf("%s has no peer %s", uiDir, peer)
			continue
		}
		sibling := readPackage(t, dir).Version
		if ok, err := caretAccepts(rng, sibling); err != nil || !ok {
			t.Errorf("%s's peer range %s for %s does not accept %s (%v)", uiDir, rng, peer, sibling, err)
		}
	}
}

// What npm would put in each package: every file its exports and style name,
// its README and licence, and none of the Go files and tests that share its
// directory. `npm pack --dry-run` needs no network, and its cache goes to a
// temporary directory.
func TestNpmPacksWhatEachPackageNames(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		if os.Getenv("HELM_ALLOW_MISSING_CLIENTS") == "" {
			t.Fatal("npm is not installed, so what each package publishes cannot be checked; install it, or set HELM_ALLOW_MISSING_CLIENTS=1 to skip on purpose")
		}
		t.Skip("npm is not installed; skipped because HELM_ALLOW_MISSING_CLIENTS is set")
	}
	cache := t.TempDir()
	for dir := range npmPackages {
		t.Run(dir, func(t *testing.T) {
			cmd := exec.Command("npm", "pack", "--dry-run", "--json")
			cmd.Dir = filepath.Join(root(t), dir)
			cmd.Env = append(os.Environ(), "npm_config_cache="+cache, "npm_config_update_notifier=false", "npm_config_fund=false")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("npm pack --dry-run: %v\n%s", err, stderr.String())
			}
			var packs []struct {
				Files []struct {
					Path string `json:"path"`
				} `json:"files"`
			}
			if err := json.Unmarshal(out, &packs); err != nil || len(packs) != 1 {
				t.Fatalf("npm pack --dry-run --json printed %s (%v)", out, err)
			}
			packed := map[string]bool{}
			for _, f := range packs[0].Files {
				packed[f.Path] = true
				if strings.HasSuffix(f.Path, ".go") || strings.Contains(f.Path, "_test") || strings.HasPrefix(f.Path, "cmd/") {
					t.Errorf("%s would publish %s", dir, f.Path)
				}
			}
			p := readPackage(t, dir)
			want := []string{"package.json", "README.md", "LICENSE"}
			if p.Style != "" {
				want = append(want, p.Style)
			}
			for _, target := range p.Exports {
				want = append(want, strings.TrimPrefix(target, "./"))
			}
			for _, w := range want {
				if prefix, wild := strings.CutSuffix(w, "*"); wild {
					var any bool
					for path := range packed {
						any = any || strings.HasPrefix(path, prefix)
					}
					if !any {
						t.Errorf("%s names %s, and packs nothing under %s", dir, w, prefix)
					}
				} else if !packed[w] {
					t.Errorf("%s names %s, and would not publish it", dir, w)
				}
			}
		})
	}
}

// Every package has a workflow that publishes it, and the release instructions
// name the tag that starts it.
func TestAWorkflowPublishesEveryPackage(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(root(t), p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	npm, python, gotags, releasing := read(".github/workflows/publish-npm.yml"), read(".github/workflows/publish-python.yml"), read(".github/workflows/verify-go-tags.yml"), read("docs/releasing.md")
	for dir := range npmPackages {
		if !strings.Contains(npm, `"`+dir+`/v*"`) {
			t.Errorf("publish-npm.yml has no tag for %s", dir)
		}
		if !strings.Contains(releasing, dir+"/v") {
			t.Errorf("docs/releasing.md names no tag for %s", dir)
		}
	}
	if !strings.Contains(python, `"`+pythonDir+`/v*"`) || !strings.Contains(releasing, pythonDir+"/v") {
		t.Errorf("the Python package's tag is missing from publish-python.yml or docs/releasing.md")
	}
	for _, tag := range []string{`"v*"`, `"packages/helm-runtime-sdk/go/v*"`, `"packages/helm-runtime-sdk/go/embedded/v*"`} {
		if !strings.Contains(gotags, tag) {
			t.Errorf("verify-go-tags.yml has no tag %s", tag)
		}
	}
}

// ------------------------------------------------------------------ semver

type semver struct {
	release [3]int
	pre     []string
}

func parseSemver(s string) (semver, error) {
	var v semver
	release, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(release, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("%q: %v", s, err)
		}
		v.release[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

// compare orders two versions as semver 2.0 does: a pre-release sorts before
// its release, and numeric identifiers before alphanumeric ones.
func compare(a, b semver) int {
	for i := range a.release {
		if a.release[i] != b.release[i] {
			return a.release[i] - b.release[i]
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		an, aErr := strconv.Atoi(a.pre[i])
		bn, bErr := strconv.Atoi(b.pre[i])
		switch {
		case aErr == nil && bErr == nil && an != bn:
			return an - bn
		case aErr == nil && bErr != nil:
			return -1
		case aErr != nil && bErr == nil:
			return 1
		case aErr != nil && bErr != nil && a.pre[i] != b.pre[i]:
			return strings.Compare(a.pre[i], b.pre[i])
		}
	}
	return len(a.pre) - len(b.pre)
}

// caretAccepts is npm's ^ for a major of 1 or more, as far as releasing needs
// it: the same major, no lower than the range's base, and a pre-release only
// of the base's own MAJOR.MINOR.PATCH.
func caretAccepts(rng, version string) (bool, error) {
	base, ok := strings.CutPrefix(rng, "^")
	if !ok {
		return false, fmt.Errorf("%q is not a ^ range", rng)
	}
	b, err := parseSemver(base)
	if err != nil {
		return false, err
	}
	v, err := parseSemver(version)
	if err != nil {
		return false, err
	}
	if b.release[0] == 0 {
		return false, fmt.Errorf("%q: a ^0 range is not used here", rng)
	}
	if v.release[0] != b.release[0] || compare(v, b) < 0 {
		return false, nil
	}
	return len(v.pre) == 0 || v.release == b.release, nil
}

func TestCaretAccepts(t *testing.T) {
	for _, c := range []struct {
		rng, version string
		want         bool
	}{
		{"^1.0.0-rc.1", "1.0.0-rc.1", true},
		{"^1.0.0-rc.1", "1.0.0-rc.2", true},
		{"^1.0.0-rc.1", "1.0.0", true},
		{"^1.0.0-rc.1", "1.4.2", true},
		{"^1.0.0-rc.2", "1.0.0-rc.1", false},
		{"^1.0.0-rc.1", "1.1.0-rc.1", false},
		{"^1.0.0-rc.1", "2.0.0", false},
		{"^1.2.0", "1.1.9", false},
		{"^1.0.0-rc.10", "1.0.0-rc.9", false},
	} {
		if got, err := caretAccepts(c.rng, c.version); err != nil || got != c.want {
			t.Errorf("caretAccepts(%q, %q) = %v, %v; want %v", c.rng, c.version, got, err, c.want)
		}
	}
	for v, want := range map[string]string{"1.0.0-rc.1": "1.0.0rc1", "1.2.3": "1.2.3", "2.0.0-beta.4": "2.0.0b4"} {
		if got, err := pep440(v); err != nil || got != want {
			t.Errorf("pep440(%q) = %q, %v; want %q", v, got, err, want)
		}
	}
	if _, err := pep440("1.0.0-dev.0"); err == nil {
		t.Error("pep440 spelled -dev.0, which is not a pre-release PyPI and npm read the same way")
	}
}
