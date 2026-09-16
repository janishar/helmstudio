package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests drive a real git against local repositories, so nothing here needs a
// network and nothing depends on a forge being up (docs/decisions.md M7
// defaults).
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet", "-b", "main")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
	}
	run("commit", "--quiet", "-m", "first")
	return dir
}

const repoManifest = `id: wan-studio
name: wan studio
kinds: [video]
repo: https://github.com/someone/wan-studio
ref: main
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

func TestReadingARepositoryGetsItsManifestAndCommit(t *testing.T) {
	repo := gitRepo(t, map[string]string{ManifestName: repoManifest})
	r := NewReader(t.TempDir())

	res, err := r.ReadRepo(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found {
		t.Fatal("the repository ships a manifest and it was not found")
	}
	if res.Text != repoManifest {
		t.Errorf("the manifest came back changed:\n%s", res.Text)
	}
	if len(res.Commit) != 40 {
		t.Errorf("the ref should be resolved to a commit, got %q", res.Commit)
	}
}

// Most repositories worth wrapping ship no manifest. That is the common case,
// not a failure — and the commit still comes back, because the editor
// pre-fills repo and ref from it.
func TestARepositoryWithNoManifestIsNotAnError(t *testing.T) {
	repo := gitRepo(t, map[string]string{"README.md": "# something else\n"})
	res, err := NewReader(t.TempDir()).ReadRepo(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("a repository without a manifest should read cleanly: %v", err)
	}
	if res.Found {
		t.Error("found should be false")
	}
	if len(res.Commit) != 40 {
		t.Errorf("the commit is still wanted, got %q", res.Commit)
	}
}

// A submodule is code that gets cloned and may be built, from a URL the
// top-level repository chose, so the approval screen has to show it.
func TestSubmodulesAreRead(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		ManifestName: repoManifest,
		".gitmodules": `[submodule "vendor/engine"]
	path = vendor/engine
	url = https://github.com/someone/engine
[submodule "vendor/tools"]
	path = vendor/tools
	url = git@github.com:someone/tools.git
`,
	})
	res, err := NewReader(t.TempDir()).ReadRepo(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Submodules) != 2 {
		t.Fatalf("got %d submodules, want 2: %+v", len(res.Submodules), res.Submodules)
	}
	if res.Submodules[0].Path != "vendor/engine" || res.Submodules[0].URL != "https://github.com/someone/engine" {
		t.Errorf("first submodule is %+v", res.Submodules[0])
	}
	if res.Submodules[1].URL != "git@github.com:someone/tools.git" {
		t.Errorf("second submodule's url is %q", res.Submodules[1].URL)
	}
}

// Nothing is checked out. A repository is read, not materialised — someone
// deciding whether to trust a studio should not have to download it first.
func TestNothingIsCheckedOut(t *testing.T) {
	repo := gitRepo(t, map[string]string{ManifestName: repoManifest, "big.bin": strings.Repeat("x", 4096)})
	r := NewReader(t.TempDir())
	if _, err := r.ReadRepo(context.Background(), repo, "main"); err != nil {
		t.Fatal(err)
	}
	// The temporary directory is removed on return, so what is asserted here
	// is that the read succeeded without a working tree — a checkout would
	// have needed one and this used --bare.
}

// The cache is keyed by commit, not by ref: <id>@<ref> goes stale the moment a
// branch moves, and then a fetch would read a file that describes something
// else (Q5).
func TestTheCacheIsKeyedByCommit(t *testing.T) {
	repo := gitRepo(t, map[string]string{ManifestName: repoManifest})
	cache := t.TempDir()
	r := NewReader(cache)

	res, file, err := r.ReadAndCache(context.Background(), "wan-studio", repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if file == "" {
		t.Fatal("a valid manifest should have been cached")
	}
	if !strings.Contains(filepath.Base(file), "@"+res.Commit) {
		t.Errorf("cached as %q, want the commit in the name", filepath.Base(file))
	}
	// And the cache store finds it, which is what makes a fetched manifest
	// resolve on the next startup without touching the network.
	ids, err := Cache(cache).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "wan-studio" {
		t.Errorf("the cache store lists %v", ids)
	}
}

// A manifest claiming a different id than the entry pointing at it is not
// cached: it would be filed under a name it does not claim, and would then
// resolve for a studio it is not.
func TestAManifestWithTheWrongIdIsNotCached(t *testing.T) {
	repo := gitRepo(t, map[string]string{ManifestName: strings.Replace(repoManifest, "id: wan-studio", "id: other-studio", 1)})
	cache := t.TempDir()
	_, file, err := NewReader(cache).ReadAndCache(context.Background(), "wan-studio", repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if file != "" {
		t.Errorf("cached a manifest that claims another id: %s", file)
	}
}

// From folder reads exactly one file at a typed absolute path, and nothing else.
func TestFromFolderReadsOnlyTheManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(repoManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReader(t.TempDir())

	res, err := r.ReadFolder(dir)
	if err != nil || !res.Found || res.Text != repoManifest {
		t.Fatalf("got %+v, %v", res, err)
	}

	empty, err := r.ReadFolder(t.TempDir())
	if err != nil {
		t.Fatalf("a folder without a manifest should read cleanly: %v", err)
	}
	if empty.Found {
		t.Error("found should be false")
	}

	if _, err := r.ReadFolder("relative/path"); err == nil {
		t.Error("a relative path should be refused; this is a path a person types, not one a page discovers")
	}
}
