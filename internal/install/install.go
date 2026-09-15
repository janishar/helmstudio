// Package install takes a studio from listed to ready and back: clone at a
// pinned ref, run build[] with resume from the first step that has not
// succeeded, fetch the declared weights, and uninstall (docs/design/01-prd.md
// R7–R13, R67; docs/design/02-data-model.md §4, §7).
//
// Every phase is idempotent. Install on a ready studio whose manifest has not
// changed does nothing and makes no network call; install after a failure
// resumes where the failure left off; nothing a phase completed is redone
// unless the manifest changed.
//
// Uninstall has no path into the models directory. It stops the studio,
// removes the checkout helmstudio cloned — never a local_path checkout, and
// never the studio's data — and its Python environment, and deletes the
// installation row, which drops its weight bindings. Weights stay for an
// explicit reclaim.
package install

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
)

// Install states (docs/design/01-prd.md §5).
const (
	StateListed          = "listed"
	StateCloning         = "cloning"
	StateCloned          = "cloned"
	StateBuilding        = "building"
	StateBuilt           = "built"
	StateFetchingWeights = "fetching_weights"
	// StateAuthRequired: a gated weight waits for a Hugging Face token; the
	// install has not failed (R18; 01 §5 amended in the M3 review).
	StateAuthRequired    = "auth_required"
	StateReady           = "ready"
	StateUpdateAvailable = "update_available"
	StateFailedClone     = "failed_clone"
	StateFailedBuild     = "failed_build"
	StateFailedWeights   = "failed_weights"
	StateRemoving        = "removing"
)

// ErrorKind classifies a refusal for the API.
type ErrorKind string

const (
	KindNotFound     ErrorKind = "not_found"
	KindNotInstalled ErrorKind = "not_installed"
	KindBlocked      ErrorKind = "blocked"
	KindConflict     ErrorKind = "conflict"
)

// Error is a refusal a user can act on. Details, when set, carries what a UI
// needs to choose a sentence, such as the code and tool of a missing tool.
type Error struct {
	Kind    ErrorKind
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Message }

func refuse(kind ErrorKind, format string, args ...any) error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Config is everything an Installer needs.
type Config struct {
	Store      *store.Store
	Dirs       *platform.Dirs
	Supervisor *supervisor.Supervisor
	Weights    *weights.Service
	// Logf receives diagnostics. Default: discarded.
	Logf func(format string, args ...any)

	// The host, replaceable in tests. Defaults come from internal/platform.
	HostOS       func() string
	HostArch     func() string
	HostBackends func() []string
	HostMemory   func() (uint64, error)
	FreeDisk     func(path string) (uint64, error)
	// LookPath finds a tool on PATH. Default exec.LookPath.
	LookPath func(name string) (string, error)
	// Environ is the daemon's environment, filtered before anything runs.
	// Default os.Environ.
	Environ func() []string
	// GitTimeout bounds one git command. Default 30 minutes.
	GitTimeout time.Duration
	// EnvTimeout bounds creating a Python environment, which may download an
	// interpreter. Default 30 minutes.
	EnvTimeout time.Duration
	// BuildLogsKept is how many install jobs' build logs a studio keeps.
	// Default 5 (R30's retention, applied to build logs).
	BuildLogsKept int
	// StepGrace is how long a build step that outlived a killed daemon has
	// between SIGTERM and SIGKILL when the next daemon's sweep stops it.
	// Default 30s, the supervisor's grace.
	StepGrace time.Duration
	// StepKillWait bounds the wait for its group to empty after SIGKILL.
	// Default 5s.
	StepKillWait time.Duration

	now func() time.Time
}

// Installer runs install, uninstall and weight jobs.
type Installer struct {
	cfg Config

	mu       sync.Mutex
	active   map[string]*active // by job id
	byStudio map[string]*active // the one job a studio may have at a time
	kinds    map[string]string  // job id → kind, for active jobs
	wg       sync.WaitGroup
	closed   bool
}

// New returns an Installer.
func New(cfg Config) *Installer {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.HostOS == nil {
		cfg.HostOS = platform.HostOS
	}
	if cfg.HostArch == nil {
		cfg.HostArch = platform.HostArch
	}
	if cfg.HostBackends == nil {
		cfg.HostBackends = platform.HostBackends
	}
	if cfg.HostMemory == nil {
		cfg.HostMemory = platform.HostMemoryBytes
	}
	if cfg.FreeDisk == nil {
		cfg.FreeDisk = platform.FreeDiskBytes
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Environ == nil {
		cfg.Environ = os.Environ
	}
	if cfg.GitTimeout == 0 {
		cfg.GitTimeout = 30 * time.Minute
	}
	if cfg.EnvTimeout == 0 {
		cfg.EnvTimeout = 30 * time.Minute
	}
	if cfg.BuildLogsKept == 0 {
		cfg.BuildLogsKept = 5
	}
	if cfg.StepGrace == 0 {
		cfg.StepGrace = 30 * time.Second
	}
	if cfg.StepKillWait == 0 {
		cfg.StepKillWait = 5 * time.Second
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Installer{cfg: cfg, active: map[string]*active{}, byStudio: map[string]*active{}, kinds: map[string]string{}}
}

func (in *Installer) nowMs() int64  { return in.cfg.now().UnixMilli() }
func (in *Installer) newID() string { return store.NewID(in.cfg.now()) }

// managedRoot is where install clones a studio (R7). It is the only checkout
// uninstall ever removes.
func (in *Installer) managedRoot(studioID string) string {
	return filepath.Join(in.cfg.Dirs.Data(), "studios", studioID, "src")
}

// PlannedRoot is where a studio's checkout will be once installed: its
// local_path, or the directory install clones into.
func (in *Installer) PlannedRoot(m *manifest.Manifest) string {
	if m.LocalPath != "" {
		return filepath.Clean(m.LocalPath)
	}
	return in.managedRoot(m.ID)
}

type installation struct {
	studioID, digest, root, commit, state string
	lastFailure                           sql.NullString
	runtimeEnv                            sql.NullString
	size                                  sql.NullInt64
}

func (in *Installer) row(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, studioID string) (installation, bool, error) {
	var r installation
	err := q.QueryRowContext(ctx, `SELECT studio_id, manifest_digest, root_path, COALESCE(commit_sha,''), install_state, last_failure, runtime_env, size_bytes
		FROM installations WHERE studio_id = ?`, studioID).Scan(&r.studioID, &r.digest, &r.root, &r.commit, &r.state, &r.lastFailure, &r.runtimeEnv, &r.size)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// Info is a studio's install state as the launcher shows it.
type Info struct {
	State string `json:"install_state"`
	Root  string `json:"root_path,omitempty"`
	// RootPresent is whether the recorded checkout exists.
	RootPresent bool   `json:"root_present"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	// RebuildNeeded means the manifest changed since this checkout was
	// built from it (02 §4, manifest_digest).
	RebuildNeeded bool            `json:"rebuild_needed"`
	RuntimeEnv    json.RawMessage `json:"runtime_env,omitempty"`
	LastFailure   *Failure        `json:"last_failure,omitempty"`
	SizeBytes     int64           `json:"size_bytes,omitempty"`
	// JobID is the install, uninstall or download job running now.
	JobID string `json:"job_id,omitempty"`
}

// Info reads a studio's install state; a studio with no installation is
// listed.
func (in *Installer) Info(ctx context.Context, studioID string, m *manifest.Manifest) (Info, error) {
	r, ok, err := in.row(ctx, in.cfg.Store.Reader(), studioID)
	if err != nil {
		return Info{}, err
	}
	info := Info{State: StateListed}
	in.mu.Lock()
	if a := in.byStudio[studioID]; a != nil {
		info.JobID = a.id
	}
	in.mu.Unlock()
	if !ok {
		return info, nil
	}
	info.State, info.Root, info.CommitSHA, info.SizeBytes = r.state, r.root, r.commit, r.size.Int64
	if fi, err := os.Stat(r.root); err == nil && fi.IsDir() {
		info.RootPresent = true
	}
	info.RebuildNeeded = m != nil && m.Digest != "" && m.Digest != r.digest
	if r.runtimeEnv.Valid {
		info.RuntimeEnv = json.RawMessage(r.runtimeEnv.String)
	}
	if r.lastFailure.Valid {
		info.LastFailure = new(Failure)
		_ = json.Unmarshal([]byte(r.lastFailure.String), info.LastFailure)
	}
	return info, nil
}

// checkHost applies R5 and R5a: os, arch and backends block; memory and disk
// shortfalls warn with the numbers. A manifest that needs a Python
// environment is refused before anything is cloned when uv is not on PATH
// (docs/decisions.md M5 Q3).
func (in *Installer) checkHost(m *manifest.Manifest) ([]string, error) {
	if hostOS := in.cfg.HostOS(); !slices.Contains(m.Requires.OS, hostOS) {
		return nil, refuse(KindBlocked, "%s runs on %s; this machine is %s", m.Name, strings.Join(m.Requires.OS, ", "), hostOS)
	}
	if arch := in.cfg.HostArch(); !slices.Contains(m.Requires.Arch, arch) {
		return nil, refuse(KindBlocked, "%s needs a %s processor; this machine is %s", m.Name, strings.Join(m.Requires.Arch, " or "), arch)
	}
	host := in.cfg.HostBackends()
	supported := false
	for _, b := range m.Runtime.Backends {
		supported = supported || slices.Contains(host, b)
	}
	if !supported {
		return nil, refuse(KindBlocked, "%s runs %s on %s; this machine provides %s", m.Name, m.Runtime.Framework, strings.Join(m.Runtime.Backends, ", "), strings.Join(host, ", "))
	}
	if m.Python != nil {
		if _, err := in.cfg.LookPath("uv"); err != nil {
			return nil, &Error{Kind: KindBlocked, Details: map[string]any{"code": "tool_missing", "tool": "uv"},
				Message: fmt.Sprintf("%s needs a Python %s environment, which helmstudio makes with uv; uv is not on PATH: %s", m.Name, m.Python.Version, uvHint)}
		}
	}
	if m.LocalPath != "" && !filepath.IsAbs(m.LocalPath) {
		return nil, refuse(KindBlocked, "%s: local_path %q is not an absolute path", m.ID, m.LocalPath)
	}
	// git would read a leading "-" as an option: --upload-pack= runs any
	// command during the clone, outside the build steps a user approves.
	if strings.HasPrefix(m.Ref, "-") || strings.HasPrefix(m.Repo, "-") {
		return nil, refuse(KindBlocked, "%s: ref %q and repo %q may not start with \"-\"", m.ID, m.Ref, m.Repo)
	}
	var warnings []string
	if m.Requires.RAMGB > 0 {
		if mem, err := in.cfg.HostMemory(); err == nil && mem < uint64(m.Requires.RAMGB)<<30 {
			warnings = append(warnings, fmt.Sprintf("%s asks for %d GB of memory; this machine has %d GB", m.Name, m.Requires.RAMGB, mem>>30))
		}
	}
	if m.Requires.DiskGB > 0 {
		if free, err := in.cfg.FreeDisk(in.cfg.Dirs.Data()); err == nil && free < uint64(m.Requires.DiskGB)<<30 {
			warnings = append(warnings, fmt.Sprintf("%s asks for %d GB of disk; %d GB is free on the volume holding %s", m.Name, m.Requires.DiskGB, free>>30, in.cfg.Dirs.Data()))
		}
	}
	return warnings, nil
}

// reserve claims a studio for one job. It returns the job already running
// for the studio when there is one.
func (in *Installer) reserve(studioID string) (*active, *active, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return nil, nil, refuse(KindConflict, "helmstudio is shutting down")
	}
	if a := in.byStudio[studioID]; a != nil {
		return nil, a, nil
	}
	a := &active{studioID: studioID, done: make(chan struct{})}
	in.byStudio[studioID] = a
	return a, nil, nil
}

func (in *Installer) release(a *active) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.byStudio[a.studioID] == a {
		delete(in.byStudio, a.studioID)
	}
	delete(in.active, a.id)
	delete(in.kinds, a.id)
}

func (in *Installer) start(a *active, kind string, run func(ctx context.Context)) {
	ctx, cancel := context.WithCancelCause(context.Background())
	a.cancel = cancel
	in.mu.Lock()
	in.active[a.id] = a
	in.kinds[a.id] = kind
	in.wg.Add(1)
	in.mu.Unlock()
	go func() {
		defer in.wg.Done()
		defer close(a.done)
		defer in.release(a)
		defer cancel(nil)
		run(ctx)
	}()
}

// Install installs a studio, or resumes an install that failed or was
// interrupted, as a Job. Running it on a studio that is already installed is
// not an error: it returns a job that finds nothing to do. When the studio
// already has an install job running, that job is returned.
func (in *Installer) Install(ctx context.Context, studioID string) (Job, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return Job{}, refuse(KindNotFound, "no studio %q is known", studioID)
	}
	m := st.Manifest
	warnings, err := in.checkHost(m)
	if err != nil {
		return Job{}, err
	}
	a, running, err := in.reserve(studioID)
	if err != nil {
		return Job{}, err
	}
	if running != nil {
		in.mu.Lock()
		kind := in.kinds[running.id]
		in.mu.Unlock()
		if kind == "install" {
			return in.Job(ctx, running.id)
		}
		return Job{}, refuse(KindConflict, "%s has a %s job running; wait for it or cancel it first", m.Name, kind)
	}
	a.warnings = warnings

	root := in.managedRoot(studioID)
	if m.LocalPath != "" {
		root = filepath.Clean(m.LocalPath)
	}
	err = in.cfg.Supervisor.HoldLaunches(func() error {
		if in.cfg.Supervisor.Running(studioID) {
			return refuse(KindConflict, "%s is running; stop it before installing", m.Name)
		}
		return in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			r, exists, err := in.row(ctx, tx, studioID)
			if err != nil {
				return err
			}
			if exists && r.state == StateRemoving {
				return refuse(KindConflict, "%s is being uninstalled; finish the uninstall first", m.Name)
			}
			// Every install starts at the clone phase; for a local_path
			// studio it records the checkout without cloning.
			fresh := StateCloning
			switch {
			case !exists:
				if _, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?)`, studioID, m.Digest, root, fresh, in.nowMs(), in.nowMs()); err != nil {
					return fmt.Errorf("recording the installation (is %s already another studio's checkout?): %w", root, err)
				}
			case r.digest != m.Digest || r.root != root:
				// The manifest changed since this checkout was built: start
				// again from the clone phase, and forget which steps
				// succeeded, since they ran against something else.
				if _, err := tx.ExecContext(ctx, `DELETE FROM step_runs WHERE studio_id = ?`, studioID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE installations SET manifest_digest = ?, root_path = ?, install_state = ?, updated_at = ? WHERE studio_id = ?`,
					m.Digest, root, fresh, in.nowMs(), studioID); err != nil {
					return err
				}
			}
			id, err := in.insertJob(ctx, tx, "install", studioID, "installation", studioID)
			a.id = id
			return err
		})
	})
	if err != nil {
		in.release(a)
		return Job{}, err
	}
	in.start(a, "install", func(jctx context.Context) {
		failure := in.pipeline(jctx, a.id, st)
		in.endJob(jctx, a.id, failure)
		in.retainBuildLogs(context.WithoutCancel(jctx), studioID)
	})
	return in.Job(ctx, a.id)
}

func (in *Installer) endJob(ctx context.Context, id string, failure *Failure) {
	switch {
	case failure == nil:
		in.finishJob(ctx, id, JobSucceeded, nil)
	case ctx.Err() != nil:
		state, _ := jobEnd(ctx)
		in.finishJob(ctx, id, state, failure)
	default:
		in.finishJob(ctx, id, JobFailed, failure)
	}
}

func (in *Installer) setState(ctx context.Context, studioID, state string, extra string, args ...any) error {
	return in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
		q := `UPDATE installations SET install_state = ?, updated_at = ?` + extra + ` WHERE studio_id = ?`
		_, err := tx.ExecContext(ctx, q, append(append([]any{state, in.nowMs()}, args...), studioID)...)
		return err
	})
}

// fail records a failed phase on the installation and returns the failure.
func (in *Installer) fail(ctx context.Context, studioID, state string, f *Failure) *Failure {
	if ctx.Err() != nil && f.Code != "auth_required" {
		_, code := jobEnd(ctx)
		f.Code = code
		f.Message = fmt.Sprintf("%s during the %s phase; work already done is kept, and retry resumes it", map[string]string{
			"cancelled": "Cancelled", "interrupted": "helmstudio stopped"}[code], f.Phase)
	}
	b, _ := json.Marshal(f)
	if err := in.setState(ctx, studioID, state, `, last_failure = ?`, string(b)); err != nil {
		in.cfg.Logf("install: recording %s's failure: %v", studioID, err)
	}
	return f
}

// pipeline runs the phases an installation still needs, in order.
func (in *Installer) pipeline(ctx context.Context, jobID string, st supervisor.Studio) *Failure {
	m := st.Manifest
	r, _, err := in.row(ctx, in.cfg.Store.Reader(), m.ID)
	if err != nil {
		return &Failure{Phase: "clone", Code: "clone_failed", Message: err.Error()}
	}
	env := supervisor.RestrictedEnv(in.cfg.Dirs, m.ID, in.cfg.Environ())
	state := r.state

	if state == StateCloning || state == StateFailedClone {
		if err := in.setState(ctx, m.ID, StateCloning, ``); err != nil {
			return &Failure{Phase: "clone", Code: "clone_failed", Message: err.Error()}
		}
		commit, f := in.checkout(ctx, m, r.root, env)
		if f != nil {
			return in.fail(ctx, m.ID, StateFailedClone, f)
		}
		err := in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if commit != r.commit {
				if _, err := tx.ExecContext(ctx, `DELETE FROM step_runs WHERE studio_id = ?`, m.ID); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `UPDATE installations SET install_state = 'cloned', commit_sha = NULLIF(?, ''), last_failure = NULL, updated_at = ? WHERE studio_id = ?`,
				commit, in.nowMs(), m.ID)
			return err
		})
		if err != nil {
			return in.fail(ctx, m.ID, StateFailedClone, &Failure{Phase: "clone", Code: "clone_failed", Message: err.Error()})
		}
		state = StateCloned
	}

	// An installed Python studio whose environment no longer checks — its
	// interpreter was deleted, or someone changed it — builds again, which
	// remakes the environment and reruns every step. This is what a launch's
	// "install it again" refers to (M5 review #10).
	if m.Python != nil && state != StateCloned && state != StateBuilding && state != StateFailedBuild {
		if why := supervisor.CheckVenv(supervisor.VenvDir(in.cfg.Dirs, m.ID), m.Python.Version); why != nil {
			in.cfg.Logf("install: %s: %v; building again", m.ID, why)
			state = StateCloned
		}
	}

	if state == StateCloned || state == StateBuilding || state == StateFailedBuild {
		if f := in.build(ctx, jobID, m, r.root, env); f != nil {
			return in.fail(ctx, m.ID, StateFailedBuild, f)
		}
		state = StateBuilt
	}

	switch state {
	case StateBuilt, StateFetchingWeights, StateAuthRequired, StateFailedWeights, StateReady, StateUpdateAvailable:
		if f := in.fetchWeights(ctx, m, state); f != nil {
			if f.Code == "linked_missing" {
				return f // install state untouched (R19b)
			}
			if f.Code == "auth_required" {
				// R18: a gated repository asks for a token; the install
				// has not failed, and the state says so across restarts.
				return in.fail(ctx, m.ID, StateAuthRequired, f)
			}
			return in.fail(ctx, m.ID, StateFailedWeights, f)
		}
		return nil
	}
	return &Failure{Phase: "install", Code: "interrupted", Message: fmt.Sprintf("%s is %s, which install cannot continue from", m.ID, state)}
}

// checkout clones the studio at its ref, or records a local_path checkout as
// it is.
func (in *Installer) checkout(ctx context.Context, m *manifest.Manifest, root string, env []string) (string, *Failure) {
	if m.LocalPath != "" {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			return "", &Failure{Phase: "clone", Code: "clone_failed", Message: fmt.Sprintf("local_path %s is not a directory", root)}
		}
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			if git, err := in.cfg.LookPath("git"); err == nil {
				if out, err := in.git(ctx, git, root, env, "rev-parse", "HEAD"); err == nil {
					return strings.TrimSpace(out), nil
				}
			}
		}
		return "", nil
	}
	git, err := in.cfg.LookPath("git")
	if err != nil {
		return "", &Failure{Phase: "clone", Code: "tool_missing", Message: toolMessage("git")}
	}
	fail := func(format string, args ...any) (string, *Failure) {
		return "", &Failure{Phase: "clone", Code: "clone_failed", Message: fmt.Sprintf(format, args...)}
	}

	fi, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fail("creating %s: %v", root, err)
		}
		if _, err := in.git(ctx, git, root, env, "init", "-q"); err != nil {
			return fail("%v", err)
		}
	case err != nil:
		return fail("%s: %v", root, err)
	case !fi.IsDir():
		return fail("%s is not a directory helmstudio made; move it aside and retry", root)
	default:
		if _, err := os.Lstat(filepath.Join(root, ".git")); err != nil {
			entries, _ := os.ReadDir(root)
			if len(entries) > 0 {
				return fail("%s exists and is not a git checkout; move it aside and retry", root)
			}
			if _, err := in.git(ctx, git, root, env, "init", "-q"); err != nil {
				return fail("%v", err)
			}
		}
	}
	if _, err := in.git(ctx, git, root, env, "remote", "set-url", "--end-of-options", "origin", m.Repo); err != nil {
		if _, err := in.git(ctx, git, root, env, "remote", "add", "--end-of-options", "origin", m.Repo); err != nil {
			return fail("%v", err)
		}
	}
	ref := m.Ref
	if ref == "" {
		ref = "HEAD"
	}
	targets := []string{"FETCH_HEAD"}
	// --end-of-options: whatever the manifest says is a ref, never an option.
	if _, shallowErr := in.git(ctx, git, root, env, "fetch", "--depth", "1", "--end-of-options", "origin", ref); shallowErr != nil {
		if _, err := in.git(ctx, git, root, env, "fetch", "--tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return fail("fetching %s from %s: %v", ref, m.Repo, err)
		}
		targets = []string{ref, "origin/" + ref}
	}
	var checkoutErr error
	for _, t := range targets {
		if _, checkoutErr = in.git(ctx, git, root, env, "checkout", "-q", "--force", "--detach", "--end-of-options", t); checkoutErr == nil {
			break
		}
	}
	if checkoutErr != nil {
		return fail("checking out %s: %v", ref, checkoutErr)
	}
	if m.Submodules {
		if _, err := in.git(ctx, git, root, env, "submodule", "update", "--init", "--recursive", "--force"); err != nil {
			return fail("updating submodules: %v", err)
		}
	}
	out, err := in.git(ctx, git, root, env, "rev-parse", "HEAD")
	if err != nil {
		return fail("%v", err)
	}
	return strings.TrimSpace(out), nil
}

// git runs one git command in dir with the restricted environment and no
// terminal prompt, so a private repository fails instead of waiting for a
// password nobody can type.
func (in *Installer) git(ctx context.Context, git, dir string, env []string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, in.cfg.GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, git, args...)
	cmd.Dir = dir
	cmd.Env = append(slices.Clone(env), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := platform.RunInGroup(cmd); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := fmt.Sprintf("git %s: %v\n%s", strings.Join(args, " "), err, lastLines(out.String(), 20))
		if lock := staleLock(out.String()); lock != "" {
			// A git command killed mid-way (a cancelled install) leaves its
			// lock behind, and every retry then stops here. helmstudio does
			// not delete it: after a daemon crash that git may still be
			// running.
			msg += fmt.Sprintf("\n\ngit found %s, left by a git command that was stopped mid-way. If no git process is running in %s, delete that file, then retry; or uninstall and install again.", lock, dir)
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}

// buildLogName is the only file name build-log retention removes: a step's
// log, or the log of creating a Python environment.
var buildLogName = regexp.MustCompile(`^build-[0-9A-HJKMNP-TV-Z]{26}-([0-9]{2,}|env)\.log$`)

var lockPath = regexp.MustCompile(`Unable to create '([^']+\.lock)': File exists`)

// staleLock returns the lock file git's output says it could not create.
func staleLock(output string) string {
	if m := lockPath.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return ""
}

// toolMessage names a missing tool and the command that installs it (R11).
// The Xcode command line tools provide the compilers and git on macOS; for
// anything else the manifest names no installer, so none is guessed.
func toolMessage(tool string) string {
	switch tool {
	case "git", "make", "cc", "clang", "c++", "clang++", "gcc", "ld":
		return fmt.Sprintf("this studio needs %q, which is not on PATH; install it with `xcode-select --install`, then retry", tool)
	case "uv":
		return fmt.Sprintf("this studio needs %q, which is not on PATH; %s", tool, uvHint)
	}
	return fmt.Sprintf("this studio needs %q, which is not on PATH; install %s, then retry", tool, tool)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// build runs build[] from the first step that has not succeeded (R8, R10).
func (in *Installer) build(ctx context.Context, jobID string, m *manifest.Manifest, root string, env []string) *Failure {
	if err := in.setState(ctx, m.ID, StateBuilding, ``); err != nil {
		return &Failure{Phase: "build", Code: "step_failed", Message: err.Error()}
	}
	// R11: every declared tool before the first step; uv for any studio that
	// declares python:, whether or not it lists uv (Q3).
	required := slices.Clone(m.Requires.Tools)
	if m.Python != nil && !slices.Contains(required, "uv") {
		required = append(required, "uv")
	}
	tools := map[string]string{}
	for _, t := range required {
		p, err := in.cfg.LookPath(t)
		if err != nil {
			return &Failure{Phase: "build", Code: "tool_missing", Message: toolMessage(t)}
		}
		tools[t] = p
	}

	runtimeEnv := map[string]any{"tools": tools}
	if m.Python != nil {
		// Q7: before step 0, and not a step: the manifest's steps keep their
		// own numbering.
		created, f := in.ensureVenv(ctx, jobID, m, tools["uv"], append(slices.Clone(env), supervisor.UVEnv(in.cfg.Dirs)...))
		if f != nil {
			return f
		}
		// A new environment holds nothing the steps installed before, so
		// none of them counts as done (M5 review #1).
		if created {
			if err := in.forgetSteps(ctx, m.ID); err != nil {
				return &Failure{Phase: "build", Code: "env_failed", Message: err.Error()}
			}
		}
		env = in.pythonEnv(m, env)
	}

	done := map[int]bool{}
	rows, err := in.cfg.Store.Reader().QueryContext(ctx, `SELECT step_index FROM step_runs WHERE studio_id = ? AND state IN ('succeeded','skipped')`, m.ID)
	if err != nil {
		return &Failure{Phase: "build", Code: "step_failed", Message: err.Error()}
	}
	for rows.Next() {
		var i int
		if rows.Scan(&i) == nil {
			done[i] = true
		}
	}
	rows.Close()
	start := 0
	for start < len(m.Build) && done[start] {
		start++
	}

	ids := make([]string, len(m.Build))
	if start < len(m.Build) {
		err := in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			for i := start; i < len(m.Build); i++ {
				ids[i] = in.newID()
				if _, err := tx.ExecContext(ctx, `INSERT INTO step_runs (id, studio_id, job_id, step_index, step_name, command, state) VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
					ids[i], m.ID, jobID, i, m.Build[i].EffectiveName(), m.Build[i].Run); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return &Failure{Phase: "build", Code: "step_failed", Message: err.Error()}
		}
	}
	for i := start; i < len(m.Build); i++ {
		in.setProgress(ctx, jobID, int64(i), int64(len(m.Build)))
		if f := in.runStep(ctx, ids[i], jobID, m, root, i, env); f != nil {
			return f
		}
	}
	in.setProgress(ctx, jobID, int64(len(m.Build)), int64(len(m.Build)))

	if m.Python != nil {
		// A step can replace the environment — uv sync remakes it for the
		// project's own requires-python or .python-version, and exits 0 — so
		// it is checked again before it is recorded (M5 review #1).
		info, f := in.venvAfterBuild(ctx, m, tools["uv"], env)
		if f != nil {
			return f
		}
		for k, v := range info {
			runtimeEnv[k] = v
		}
	}
	if goPath, ok := tools["go"]; ok {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(cctx, goPath, "version")
		cmd.Env = env
		var out bytes.Buffer
		cmd.Stdout = &out
		if platform.RunInGroup(cmd) == nil {
			runtimeEnv["go"] = strings.TrimSpace(out.String())
		}
		cancel()
	}
	envJSON, _ := json.Marshal(runtimeEnv)
	if err := in.setState(ctx, m.ID, StateBuilt, `, runtime_env = ?, size_bytes = ?, last_failure = NULL`, string(envJSON), treeBytes(root)); err != nil {
		return &Failure{Phase: "build", Code: "step_failed", Message: err.Error()}
	}
	return nil
}

// treeBytes totals the regular files under root without following symlinks.
func treeBytes(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// runStep runs one build step in its own process group, with its output in a
// log file, and records the outcome. A failure carries the step's last lines,
// because "build failed" is nothing a user can act on.
func (in *Installer) runStep(ctx context.Context, stepID, jobID string, m *manifest.Manifest, root string, i int, env []string) *Failure {
	step := m.Build[i]
	index := i
	failure := func(code, format string, args ...any) *Failure {
		return &Failure{Phase: "build", StepIndex: &index, Code: code, Message: fmt.Sprintf("step %d of %d, %q: ", i+1, len(m.Build), step.EffectiveName()) + fmt.Sprintf(format, args...)}
	}
	record := func(state string, code *int) {
		err := in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE step_runs SET state = ?, exit_code = ?, finished_at = ? WHERE id = ?`, state, code, in.nowMs(), stepID)
			return err
		})
		if err != nil {
			in.cfg.Logf("install: recording step %d of %s as %s: %v", i, m.ID, state, err)
		}
	}

	cwd := root
	if step.Cwd != "" {
		cwd = filepath.Join(root, filepath.FromSlash(step.Cwd))
	}
	realRoot, rerr := filepath.EvalSymlinks(root)
	realCwd, cerr := filepath.EvalSymlinks(cwd)
	if rerr != nil || cerr != nil {
		record("failed", nil)
		return failure("step_failed", "its working directory %s does not exist", cwd)
	}
	if rel, err := filepath.Rel(realRoot, realCwd); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		record("failed", nil)
		return failure("step_failed", "its working directory %s resolves outside the checkout %s", cwd, realRoot)
	}
	argv, err := platform.ShellArgv(step.EffectiveShell(), step.Run)
	if err != nil {
		record("failed", nil)
		return failure("step_failed", "%v", err)
	}

	logRel := filepath.Join("studios", m.ID, fmt.Sprintf("build-%s-%02d.log", jobID, i))
	logAbs := filepath.Join(in.cfg.Dirs.Logs(), logRel)
	if err := os.MkdirAll(filepath.Dir(logAbs), 0o755); err != nil {
		record("failed", nil)
		return failure("step_failed", "creating its log directory: %v", err)
	}
	logFile, err := os.OpenFile(logAbs, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		record("failed", nil)
		return failure("step_failed", "opening its log: %v", err)
	}
	fmt.Fprintf(logFile, "$ %s\n# in %s\n", step.Run, cwd)
	logID := in.newID()
	err = in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES (?, ?, 'build', 'step_run', ?, ?, ?)`,
			logID, logRel, stepID, m.ID, in.nowMs()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE step_runs SET state = 'running', started_at = ?, log_file_id = ? WHERE id = ?`, in.nowMs(), logID, stepID)
		return err
	})
	if err != nil {
		logFile.Close()
		record("failed", nil)
		return failure("step_failed", "recording it: %v", err)
	}

	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.EffectiveTimeoutS())*time.Second)
	defer cancel()
	cmd := exec.CommandContext(stepCtx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = cwd, env, logFile, logFile
	// Cancelling or timing out kills the whole group, not only the shell.
	cmd.Cancel = func() error { return platform.KillGroup(cmd.Process.Pid) }
	cmd.WaitDelay = time.Second
	var runErr error
	if runErr = platform.StartInGroup(cmd); runErr == nil {
		// The step leads its own group and outlives a daemon killed with
		// SIGKILL. Recording its identity is what lets the next daemon's
		// sweep find and stop it (M3 review); a step whose identity cannot
		// be recorded is not left running.
		if err := in.recordStepIdentity(ctx, stepID, cmd.Process.Pid); err != nil {
			_ = platform.KillGroup(cmd.Process.Pid)
			_ = cmd.Wait()
			logFile.Close()
			record("failed", nil)
			return failure("step_failed", "recording its process so a restarted helmstudio could stop it: %v; the step was killed", err)
		}
		runErr = cmd.Wait()
	}
	logFile.Close()
	var code *int
	if cmd.ProcessState != nil {
		c := platform.ExitCode(cmd.ProcessState)
		code = &c
	}

	tail := readTail(logAbs, 20)
	switch {
	case ctx.Err() != nil:
		state, _ := jobEnd(ctx)
		if state == JobInterrupted {
			record("interrupted", code)
		} else {
			record("cancelled", code)
		}
		f := failure("cancelled", "stopped")
		f.LogFileID = logID
		return f
	case runErr == nil:
		record("succeeded", code)
		return nil
	case errors.Is(stepCtx.Err(), context.DeadlineExceeded):
		record("failed", code)
		f := failure("step_timeout", "did not finish within %d s and was killed. Its last output:\n%s", step.EffectiveTimeoutS(), tail)
		f.ExitCode, f.LogFileID = code, logID
		return f
	case step.Optional && code != nil:
		record("skipped", code)
		in.cfg.Logf("install: %s step %d (%s) is optional and exited %d; skipped", m.ID, i, step.EffectiveName(), *code)
		return nil
	default:
		record("failed", code)
		what := fmt.Sprintf("could not run: %v", runErr)
		if code != nil {
			what = fmt.Sprintf("exited %d", *code)
		}
		f := failure("step_failed", "%s in %s. Its last output:\n%s", what, cwd, tail)
		f.ExitCode, f.LogFileID = code, logID
		return f
	}
}

// recordStepIdentity writes a running step's pid, start time and process
// group. A process that has already exited has no start time to read; its
// row keeps pid and pgid only, and the sweep never signals without all three.
func (in *Installer) recordStepIdentity(ctx context.Context, stepID string, pid int) error {
	var start any
	if id, err := platform.IdentifyProcess(pid); err == nil {
		start = id.StartTime
	}
	return in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE step_runs SET pid = ?, pid_start_time = ?, pgid = ? WHERE id = ?`, pid, start, pid, stepID)
		return err
	})
}

// stopSurvivor stops a build step a killed daemon left running, only when
// it is provably that step: the group id is the recorded leader's pid, and
// either the leader still matches its recorded pid, start time and group, or
// no process has that pid (while any member of a group lives, the kernel
// cannot reuse its id). A pid now held by another process is never signalled
// — M2's identity guard, applied to build steps.
func (in *Installer) stopSurvivor(ctx context.Context, stepID string, pid, pgid int, start sql.NullInt64) {
	if pid <= 1 || pgid != pid || !start.Valid {
		in.cfg.Logf("install: build step %s left no verifiable identity (pid %d, pgid %d); it was not signalled", stepID, pid, pgid)
		return
	}
	cur, err := platform.IdentifyProcess(pid)
	switch {
	case err == nil:
		if !cur.Matches(platform.ProcessIdentity{PID: pid, StartTime: start.Int64, PGID: pgid}) {
			in.cfg.Logf("install: build step %s's pid %d now belongs to another process; it was not signalled", stepID, pid)
			return
		}
	case errors.Is(err, platform.ErrNoProcess):
		if alive, gerr := platform.GroupExists(pgid); gerr != nil || !alive {
			return
		}
	default:
		in.cfg.Logf("install: build step %s: reading pid %d: %v; it was not signalled", stepID, pid, err)
		return
	}
	in.cfg.Logf("install: stopping build step %s (process group %d), left running by a helmstudio that was killed", stepID, pgid)
	gone := func(within time.Duration) bool {
		deadline := time.Now().Add(within)
		for {
			if alive, err := platform.GroupExists(pgid); err != nil || !alive {
				return true
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return false
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := platform.TerminateGroup(pgid); err != nil {
		in.cfg.Logf("install: %v", err)
	}
	if gone(in.cfg.StepGrace) {
		return
	}
	if err := platform.KillGroup(pgid); err != nil {
		in.cfg.Logf("install: %v", err)
	}
	if !gone(in.cfg.StepKillWait) {
		in.cfg.Logf("install: build step %s's process group %d survived SIGKILL", stepID, pgid)
	}
}

func readTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 64<<10 {
		f.Seek(-64<<10, 0)
		_, _ = bufio.NewReader(f).ReadString('\n') // drop a partial first line
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// fetchWeights binds and downloads every weight that is not optional and not
// already satisfied, each as its own download Job, then records the studio
// ready. Bindings for weights the manifest no longer declares are dropped.
func (in *Installer) fetchWeights(ctx context.Context, m *manifest.Manifest, state string) *Failure {
	var names []string
	for _, w := range m.Weights {
		names = append(names, w.Name)
	}
	if err := in.cfg.Weights.Unbind(ctx, m.ID, names); err != nil {
		return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
	}
	var todo []manifest.Weight
	for _, w := range m.Weights {
		if w.Optional {
			continue
		}
		values, _, err := in.cfg.Weights.Launch(ctx, m.ID, []manifest.Weight{w})
		if err != nil {
			return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
		}
		if _, ok := values["models."+w.Name]; !ok {
			todo = append(todo, w)
		}
	}
	if len(todo) == 0 && (state == StateReady || state == StateUpdateAvailable) {
		return nil
	}
	// R19b: a linked directory that is not there refuses the job and leaves
	// the install state as it was; it is not a failed install.
	for _, w := range todo {
		path, missing, err := in.cfg.Weights.MissingLink(ctx, w)
		if err != nil {
			return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
		}
		if missing {
			return &Failure{Phase: "weights", Code: "linked_missing", Message: fmt.Sprintf(
				"weight %q is linked to %s, which is not there; reconnect it and install again. The install state is unchanged", w.Name, path)}
		}
	}
	if err := in.setState(ctx, m.ID, StateFetchingWeights, ``); err != nil {
		return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
	}
	for _, w := range todo {
		if f := in.fetchOne(ctx, m.ID, w); f != nil {
			return f
		}
	}
	if err := in.setState(ctx, m.ID, StateReady, `, last_failure = NULL`); err != nil {
		return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
	}
	return nil
}

// fetchOne runs one weight's download as a Job.
func (in *Installer) fetchOne(ctx context.Context, studioID string, w manifest.Weight) *Failure {
	var jobID string
	err := in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		jobID, err = in.insertJob(ctx, tx, "download", studioID, "model_artifact", "")
		return err
	})
	if err != nil {
		return &Failure{Phase: "weights", Code: "weights_failed", Message: err.Error()}
	}
	err = in.cfg.Weights.Fetch(ctx, studioID, w, func(artifactID string) {
		_ = in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE jobs SET subject_id = ? WHERE id = ?`, artifactID, jobID)
			return err
		})
	})
	if err == nil {
		in.finishJob(ctx, jobID, JobSucceeded, nil)
		return nil
	}
	f := &Failure{Phase: "weights", Code: "weights_failed", Message: fmt.Sprintf("weight %q: %v", w.Name, err)}
	switch {
	case errors.Is(err, weights.ErrAuthRequired):
		f.Code = "auth_required"
		f.Message = fmt.Sprintf("weight %q: %s is gated or private; add a Hugging Face token, then retry", w.Name, w.Repo)
	case errors.Is(err, weights.ErrDiskSpace):
		f.Code = "disk_space"
	}
	in.endJob(ctx, jobID, f)
	return f
}

// FetchWeight downloads one declared weight — an optional one, or one whose
// download failed — as a download Job, for an installed studio.
func (in *Installer) FetchWeight(ctx context.Context, studioID, name string) (Job, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return Job{}, refuse(KindNotFound, "no studio %q is known", studioID)
	}
	w, ok := weightNamed(st.Manifest, name)
	if !ok {
		return Job{}, refuse(KindNotFound, "%s declares no weight %q", studioID, name)
	}
	r, exists, err := in.row(ctx, in.cfg.Store.Reader(), studioID)
	if err != nil {
		return Job{}, err
	}
	if !exists || !slices.Contains([]string{StateReady, StateUpdateAvailable, StateFetchingWeights, StateAuthRequired, StateFailedWeights}, r.state) {
		return Job{}, refuse(KindNotInstalled, "%s is not installed as far as its weights; install it first", studioID)
	}
	a, running, err := in.reserve(studioID)
	if err != nil {
		return Job{}, err
	}
	if running != nil {
		return Job{}, refuse(KindConflict, "%s already has a job running; wait for it or cancel it first", studioID)
	}
	err = in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		a.id, err = in.insertJob(ctx, tx, "download", studioID, "model_artifact", "")
		return err
	})
	if err != nil {
		in.release(a)
		return Job{}, err
	}
	jobID := a.id
	in.start(a, "download", func(jctx context.Context) {
		err := in.cfg.Weights.Fetch(jctx, studioID, w, func(artifactID string) {
			_ = in.cfg.Store.Update(context.WithoutCancel(jctx), func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE jobs SET subject_id = ? WHERE id = ?`, artifactID, jobID)
				return err
			})
		})
		var f *Failure
		if err != nil {
			f = &Failure{Phase: "weights", Code: "weights_failed", Message: fmt.Sprintf("weight %q: %v", name, err)}
			if errors.Is(err, weights.ErrAuthRequired) {
				f.Code = "auth_required"
			}
		} else if ferr := in.finishIfWeightsReady(jctx, st.Manifest); ferr != nil {
			f = &Failure{Phase: "weights", Code: "weights_failed", Message: ferr.Error()}
		}
		in.endJob(jctx, jobID, f)
	})
	return in.Job(ctx, a.id)
}

// finishIfWeightsReady completes an install stopped at the weights phase —
// waiting for a token, or failed — once every required weight resolves, so a
// successful fetch of the weight it was waiting on makes the studio ready.
func (in *Installer) finishIfWeightsReady(ctx context.Context, m *manifest.Manifest) error {
	r, ok, err := in.row(ctx, in.cfg.Store.Reader(), m.ID)
	if err != nil || !ok || (r.state != StateFetchingWeights && r.state != StateAuthRequired && r.state != StateFailedWeights) {
		return err
	}
	for _, w := range m.Weights {
		if w.Optional {
			continue
		}
		values, _, err := in.cfg.Weights.Launch(ctx, m.ID, []manifest.Weight{w})
		if err != nil {
			return err
		}
		if _, ok := values["models."+w.Name]; !ok {
			return nil
		}
	}
	return in.setState(ctx, m.ID, StateReady, `, last_failure = NULL`)
}

// LinkWeight points one declared weight at a directory the user already has
// (R14a). It works before install, too: the artifact is recorded, and install
// binds to it instead of downloading.
func (in *Installer) LinkWeight(ctx context.Context, studioID, name, path string) (weights.Artifact, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return weights.Artifact{}, refuse(KindNotFound, "no studio %q is known", studioID)
	}
	w, ok := weightNamed(st.Manifest, name)
	if !ok {
		return weights.Artifact{}, refuse(KindNotFound, "%s declares no weight %q", studioID, name)
	}
	in.mu.Lock()
	busy := in.byStudio[studioID] != nil
	in.mu.Unlock()
	if busy {
		return weights.Artifact{}, refuse(KindConflict, "%s has a job running; wait for it or cancel it before linking", studioID)
	}
	return in.cfg.Weights.Link(ctx, studioID, w, path, st.Manifest.Weights...)
}

func weightNamed(m *manifest.Manifest, name string) (manifest.Weight, bool) {
	for _, w := range m.Weights {
		if w.Name == name {
			return w, true
		}
	}
	return manifest.Weight{}, false
}

// Uninstall removes a studio as a Job (R67): it stops the studio, removes the
// checkout helmstudio cloned, and deletes the installation, which drops the
// studio's weight bindings. It never removes a local_path checkout, the
// studio's data directory, or anything under the models directory — weights
// stay for reclaim. An install job running for the studio is cancelled first.
func (in *Installer) Uninstall(ctx context.Context, studioID string) (Job, error) {
	r, exists, err := in.row(ctx, in.cfg.Store.Reader(), studioID)
	if err != nil {
		return Job{}, err
	}
	if !exists {
		return Job{}, refuse(KindNotInstalled, "%s is not installed", studioID)
	}
	var a *active
	for {
		var running *active
		a, running, err = in.reserve(studioID)
		if err != nil {
			return Job{}, err
		}
		if running == nil {
			break
		}
		in.mu.Lock()
		kind := in.kinds[running.id]
		in.mu.Unlock()
		if kind == "uninstall" {
			return in.Job(ctx, running.id)
		}
		if err := in.Cancel(ctx, running.id); err != nil && !errors.Is(err, ErrNotRunning) {
			return Job{}, err
		}
	}
	err = in.cfg.Supervisor.HoldLaunches(func() error {
		return in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE installations SET install_state = 'removing', updated_at = ? WHERE studio_id = ?`, in.nowMs(), studioID); err != nil {
				return err
			}
			var err error
			a.id, err = in.insertJob(ctx, tx, "uninstall", studioID, "installation", studioID)
			return err
		})
	})
	if err != nil {
		in.release(a)
		return Job{}, err
	}
	in.start(a, "uninstall", func(jctx context.Context) {
		f := in.uninstall(jctx, studioID, r.root)
		if f != nil {
			b, _ := json.Marshal(f)
			_ = in.setState(jctx, studioID, StateRemoving, `, last_failure = ?`, string(b))
		}
		in.endJob(jctx, a.id, f)
	})
	return in.Job(ctx, a.id)
}

func (in *Installer) uninstall(ctx context.Context, studioID, root string) *Failure {
	fail := func(format string, args ...any) *Failure {
		return &Failure{Phase: "uninstall", Code: "uninstall_failed", Message: fmt.Sprintf(format, args...) + "; uninstall again to retry"}
	}
	sup := in.cfg.Supervisor
	if sup.Running(studioID) {
		if err := sup.Stop(studioID); err != nil {
			var se *supervisor.Error
			if !errors.As(err, &se) || se.Kind != supervisor.KindNotRunning {
				return fail("stopping %s: %v", studioID, err)
			}
		}
		if err := sup.Wait(ctx, studioID); err != nil {
			return fail("waiting for %s to stop: %v", studioID, err)
		}
	}
	if err := in.removeCheckout(studioID, root); err != nil {
		return fail("%v", err)
	}
	// The Python environment is helmstudio's whatever the checkout is, and is
	// removed even when the manifest no longer declares python: (Q4).
	if err := in.removeStudioEntry(studioID, "venv"); err != nil {
		return fail("removing the Python environment: %v", err)
	}
	err := in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM installations WHERE studio_id = ?`, studioID)
		return err
	})
	if err != nil {
		return fail("removing the installation record: %v", err)
	}
	return nil
}

// removeCheckout removes <data>/studios/<id>/src when that is the recorded
// checkout, and nothing otherwise. The removal goes through os.Root on the
// studio's directory, so a symlink inside the checkout — or the checkout
// itself having become one — is removed as a link and never followed.
func (in *Installer) removeCheckout(studioID, root string) error {
	managed := in.managedRoot(studioID)
	if filepath.Clean(root) != managed {
		in.cfg.Logf("install: %s's checkout %s is its local_path; it belongs to the user and is not removed", studioID, root)
		return nil
	}
	if err := in.removeStudioEntry(studioID, filepath.Base(managed)); err != nil {
		return fmt.Errorf("removing the checkout: %w", err)
	}
	return nil
}

// Sweep records what a previous daemon left mid-flight (02 §7): a build step
// still running is stopped when its recorded identity verifies, then becomes
// interrupted, never silently failed; a phase in progress becomes its failed
// state with an interrupted failure; running jobs become interrupted; a
// download becomes interrupted. Nothing is resumed until a user retries. It
// runs before the API serves, so a Retry can never start beside a survivor.
func (in *Installer) Sweep(ctx context.Context) error {
	rows, err := in.cfg.Store.Reader().QueryContext(ctx, `SELECT id, pid, pgid, pid_start_time FROM step_runs WHERE state = 'running' AND pid IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("reading build steps left running: %w", err)
	}
	type survivor struct {
		id        string
		pid, pgid int
		start     sql.NullInt64
	}
	var survivors []survivor
	for rows.Next() {
		var sv survivor
		var pgid sql.NullInt64
		if err := rows.Scan(&sv.id, &sv.pid, &pgid, &sv.start); err != nil {
			rows.Close()
			return err
		}
		sv.pgid = int(pgid.Int64)
		survivors = append(survivors, sv)
	}
	rows.Close()
	for _, sv := range survivors {
		in.stopSurvivor(ctx, sv.id, sv.pid, sv.pgid, sv.start)
	}

	interrupted := func(phase string) string {
		b, _ := json.Marshal(Failure{Phase: phase, Code: "interrupted", Message: fmt.Sprintf("helmstudio stopped during the %s phase; work already done is kept, and retry resumes it", phase)})
		return string(b)
	}
	err = in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := in.nowMs()
		if _, err := tx.ExecContext(ctx, `UPDATE step_runs SET state = 'interrupted', finished_at = ? WHERE state = 'running'`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'interrupted', finished_at = ? WHERE state IN ('queued','running')`, now); err != nil {
			return err
		}
		for from, to := range map[string][2]string{
			StateCloning:         {StateFailedClone, "clone"},
			StateBuilding:        {StateFailedBuild, "build"},
			StateFetchingWeights: {StateFailedWeights, "weights"},
		} {
			if _, err := tx.ExecContext(ctx, `UPDATE installations SET install_state = ?, updated_at = ?, last_failure = ?
				WHERE install_state = ?`, to[0], now, interrupted(to[1]), from); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recording interrupted installs: %w", err)
	}
	return in.cfg.Weights.SweepInterrupted(ctx)
}

// Shutdown stops every running job, recording each interrupted, and waits.
func (in *Installer) Shutdown(ctx context.Context) error {
	in.mu.Lock()
	in.closed = true
	for _, a := range in.active {
		a.cancel(errShutdown)
	}
	in.mu.Unlock()
	done := make(chan struct{})
	go func() { in.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stopping install jobs: %w", ctx.Err())
	}
}

// retainBuildLogs keeps the build logs of a studio's newest install jobs and
// removes the rest: only regular files inside the logs root, found with
// Lstat, so a log path that has become a symlink is left alone.
func (in *Installer) retainBuildLogs(ctx context.Context, studioID string) {
	rows, err := in.cfg.Store.Reader().QueryContext(ctx, `SELECT id, path, created_at FROM log_files WHERE studio_id = ? AND kind = 'build' ORDER BY created_at DESC, id DESC`, studioID)
	if err != nil {
		in.cfg.Logf("install: build log retention for %s: %v", studioID, err)
		return
	}
	type entry struct{ id, path, job string }
	var all []entry
	for rows.Next() {
		var e entry
		var created int64
		if rows.Scan(&e.id, &e.path, &created) == nil {
			base := filepath.Base(e.path) // build-<job>-<nn>.log
			e.job = strings.TrimSuffix(strings.TrimPrefix(base, "build-"), filepath.Ext(base))
			if k := strings.LastIndex(e.job, "-"); k > 0 {
				e.job = e.job[:k]
			}
			all = append(all, e)
		}
	}
	rows.Close()
	jobs := map[string]bool{}
	var doomed []entry
	for _, e := range all {
		if !jobs[e.job] && len(jobs) >= in.cfg.BuildLogsKept {
			doomed = append(doomed, e)
			continue
		}
		jobs[e.job] = true
	}
	if len(doomed) == 0 {
		return
	}
	// Removal goes through os.Root on the logs root, so a symlinked
	// directory on the way (<logs>/studios/<id>) is refused rather than
	// followed, and only a file with the name install generates is removed.
	logs, err := os.OpenRoot(in.cfg.Dirs.Logs())
	if err != nil {
		in.cfg.Logf("install: build log retention for %s: %v", studioID, err)
		return
	}
	defer logs.Close()
	var removed []string
	for _, e := range doomed {
		if !filepath.IsLocal(e.path) || !buildLogName.MatchString(filepath.Base(e.path)) {
			in.cfg.Logf("install: not deleting log %q: it is not a build log inside the logs root", e.path)
			continue
		}
		fi, err := logs.Lstat(e.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			in.cfg.Logf("install: not deleting log %q: %v", e.path, err)
			continue
		case !fi.Mode().IsRegular():
			in.cfg.Logf("install: not deleting log %q: it is not a regular file", e.path)
			continue
		default:
			if err := logs.Remove(e.path); err != nil {
				continue
			}
		}
		removed = append(removed, e.id)
	}
	sort.Strings(removed)
	_ = in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM log_files WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}
