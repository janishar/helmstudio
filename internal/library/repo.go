package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
)

// Reading a repository without cloning it (docs/decisions.md M7 Q7, Q8, Q14).
//
// Two decisions shape this, and both are about restraint.
//
// **Git only, never a forge's HTTP API.** GitHub's API would be one call and
// would work for GitHub. This works for every transport git understands — a
// GitLab instance behind a VPN, a bare repository on a colleague's machine, a
// directory on this Mac — and needs no token, no rate limit and no account.
// The cost is a temporary directory and three git commands.
//
// **Only what is needed is fetched.** A depth-1 fetch with no file contents
// (`--filter=blob:none --no-checkout`), then exactly two blobs read out of the
// tree. Reading a manifest must not drag down a repository whose weights are
// committed in it, and someone deciding whether to trust a studio should not
// have to download it first.
//
// This is a user action, never startup: fetching happens when someone clicks
// Fetch, opens the approval preview, or adds a repository (Q7).

// Result is what a repository holds at a ref.
type Result struct {
	Found  bool   `json:"found"`
	Commit string `json:"commit,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Text   string `json:"text,omitempty"`
	// Submodules come from .gitmodules at the resolved commit. A submodule is
	// code that gets cloned and may be built, from a URL the top-level
	// repository chose, so the approval screen shows them beside `repo`.
	Submodules []Submodule `json:"submodules,omitempty"`
}

// Submodule is one entry of .gitmodules.
type Submodule struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}

// ManifestName is the file a studio's repository carries.
const ManifestName = "helmstudio.yaml"

// Reader reads repositories. Git and Timeout are seams so tests can drive a
// real git against local bare repositories without a network.
type Reader struct {
	Git     string
	Timeout time.Duration
	// CacheDir is <cache>/manifests. A manifest read here is written to
	// <id>@<commit>.yaml, keyed by the commit rather than the ref, because
	// <id>@<ref> goes stale the moment a branch moves (Q5).
	CacheDir string
}

// NewReader returns a reader with the defaults the daemon uses.
func NewReader(cacheDir string) *Reader {
	return &Reader{Git: "git", Timeout: 60 * time.Second, CacheDir: cacheDir}
}

// ReadRepo resolves ref to a commit and reads the manifest and .gitmodules at
// it, without checking anything out.
//
// A repository that ships no manifest is not an error: it is the common case,
// and it is what the editor pre-fills `repo` and `ref` from. Found is false and
// the commit is still returned.
func (r *Reader) ReadRepo(ctx context.Context, repo, ref string) (Result, error) {
	if repo == "" {
		return Result{}, fmt.Errorf("no repository to read")
	}
	if ref == "" {
		ref = "HEAD"
	}
	dir, err := os.MkdirTemp("", "helm-read-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	// An empty repository with the remote added: nothing is fetched until the
	// fetch below, and nothing is ever checked out.
	if _, err := r.git(ctx, dir, "init", "--quiet", "--bare"); err != nil {
		return Result{}, err
	}
	if _, err := r.git(ctx, dir, "remote", "add", "origin", repo); err != nil {
		return Result{}, err
	}
	// --filter=blob:none asks the server for the tree and no file contents;
	// the two blobs actually read are fetched on demand below. A server that
	// does not support filtering still answers, having sent more than was
	// wanted, which is a bandwidth cost and not a correctness one.
	if _, err := r.git(ctx, dir, "fetch", "--quiet", "--depth", "1", "--filter=blob:none", "origin", ref); err != nil {
		return Result{}, fmt.Errorf("reading %s at %s: %w", repo, ref, err)
	}
	commit, err := r.git(ctx, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return Result{}, err
	}
	out := Result{Repo: repo, Ref: ref, Commit: strings.TrimSpace(commit)}

	if text, ok, err := r.show(ctx, dir, out.Commit, ManifestName); err != nil {
		return out, err
	} else if ok {
		out.Found, out.Text = true, text
	}
	if text, ok, err := r.show(ctx, dir, out.Commit, ".gitmodules"); err != nil {
		return out, err
	} else if ok {
		out.Submodules = parseGitmodules(text)
	}
	return out, nil
}

// ReadFolder is "From folder": a typed absolute path, from which only
// <path>/helmstudio.yaml is read.
//
// The daemon opens no path a request names, with this one exception — and the
// exception is narrow on purpose. There is no directory-listing endpoint, and
// a browser folder picker never hands a page an absolute path, so this is a
// thing a person types rather than a thing a page discovers (Q14).
func (r *Reader) ReadFolder(path string) (Result, error) {
	if !filepath.IsAbs(path) {
		return Result{}, fmt.Errorf("the folder must be an absolute path, and %q is not", path)
	}
	clean := filepath.Clean(path)
	file := filepath.Join(clean, ManifestName)
	b, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Result{Found: false}, nil
	case err != nil:
		return Result{}, fmt.Errorf("reading %s: %w", file, err)
	}
	return Result{Found: true, Text: string(b)}, nil
}

// show reads one path out of a commit. A missing file is not an error: most
// repositories have neither of the two files this asks for.
func (r *Reader) show(ctx context.Context, dir, commit, path string) (string, bool, error) {
	out, err := r.git(ctx, dir, "show", commit+":"+path)
	if err != nil {
		// git says "path does not exist" or "does not exist in" for a missing
		// file, and something else for a real failure. Distinguishing them by
		// message is unpleasant but the alternative — cat-file -e first — is
		// a second round trip to say the same thing.
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

// Cache writes a manifest read from a repository to <cache>/manifests, keyed
// by the commit it was read at.
func (r *Reader) Cache(id, commit, text string) (string, error) {
	if r.CacheDir == "" || id == "" || commit == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(r.CacheDir, fmt.Sprintf("%s@%s.yaml", id, commit))
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		return "", err
	}
	return file, nil
}

// ReadAndCache reads a repository and, when it holds a valid manifest whose id
// matches, caches it. A manifest declaring a different id than the entry
// pointing at it is not cached: it would be filed under a name it does not
// claim, and would then resolve for a studio it is not.
func (r *Reader) ReadAndCache(ctx context.Context, id, repo, ref string) (Result, string, error) {
	res, err := r.ReadRepo(ctx, repo, ref)
	if err != nil || !res.Found {
		return res, "", err
	}
	m, vres, err := manifest.LoadBytes(repo, []byte(res.Text))
	if err != nil || !vres.OK() || m.ID != id {
		return res, "", err
	}
	file, err := r.Cache(id, res.Commit, res.Text)
	return res, file, err
}

func (r *Reader) git(ctx context.Context, dir string, args ...string) (string, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gitBin := r.Git
	if gitBin == "" {
		gitBin = "git"
	}
	cmd := exec.CommandContext(cctx, gitBin, args...)
	cmd.Dir = dir
	// No terminal prompt, so a private repository fails instead of waiting for
	// a password nobody can type; and a restricted environment, because
	// reading a manifest must not inherit whatever the daemon was started with.
	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := platform.RunInGroup(cmd); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), timeout)
		}
		return "", fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// parseGitmodules reads the path and url of each submodule. It is deliberately
// small: .gitmodules is git config syntax, and only two keys matter here.
func parseGitmodules(text string) []Submodule {
	var out []Submodule
	var cur *Submodule
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[submodule"):
			if cur != nil && cur.Path != "" {
				out = append(out, *cur)
			}
			cur = &Submodule{}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "path"):
			cur.Path = configValue(line)
		case strings.HasPrefix(line, "url"):
			cur.URL = configValue(line)
		}
	}
	if cur != nil && cur.Path != "" {
		out = append(out, *cur)
	}
	return out
}

func configValue(line string) string {
	_, v, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}
