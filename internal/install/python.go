package install

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// A studio that declares python: gets its own uv environment before its first
// build step (docs/design/01-prd.md R12; docs/decisions.md M5 Q3–Q7). uv is
// found on PATH; vendoring it is M9's. The environment lives at
// supervisor.VenvDir, outside the checkout, is kept while its Python matches
// the manifest, and is removed by uninstall.

// uvHint is how to get uv when it is missing (Q3): no curl | sh.
const uvHint = "install it with `brew install uv`, or see https://docs.astral.sh/uv/getting-started/installation/, then retry"

// frameworkDist maps runtime.framework to the Python distribution whose
// installed version runtime_env records (Q7). Frameworks with no single
// distribution are not probed.
var frameworkDist = map[string]string{
	"pytorch": "torch",
	"mlx":     "mlx",
	"jax":     "jax",
	"onnx":    "onnxruntime",
}

// pythonEnv is the environment build steps of a python: studio run with:
// the restricted environment, uv's settings for an environment helmstudio
// owns, and the environment active (Q5, Q6).
func (in *Installer) pythonEnv(m *manifest.Manifest, env []string) []string {
	if m.Python == nil {
		return env
	}
	return supervisor.Activate(append(slices.Clone(env), supervisor.UVEnv(in.cfg.Dirs)...), supervisor.VenvDir(in.cfg.Dirs, m.ID))
}

// ensureVenv makes the studio's environment exist for its declared Python,
// keeping one that already matches, and reports whether it made one. Its
// output goes to a build log owned by the install job, since it is not one of
// the manifest's steps.
func (in *Installer) ensureVenv(ctx context.Context, jobID string, m *manifest.Manifest, uv string, env []string) (bool, *Failure) {
	venv := supervisor.VenvDir(in.cfg.Dirs, m.ID)
	version := m.Python.Version
	fail := func(logID, format string, args ...any) *Failure {
		return &Failure{Phase: "build", Code: "env_failed", LogFileID: logID,
			Message: fmt.Sprintf("creating %s's Python %s environment at %s: ", m.Name, version, venv) + fmt.Sprintf(format, args...)}
	}

	why := supervisor.CheckVenv(venv, version)
	if why == nil {
		return false, nil
	}
	logRel := filepath.Join("studios", m.ID, fmt.Sprintf("build-%s-env.log", jobID))
	logAbs := filepath.Join(in.cfg.Dirs.Logs(), logRel)
	if err := os.MkdirAll(filepath.Dir(logAbs), 0o755); err != nil {
		return false, fail("", "creating its log directory: %v", err)
	}
	logFile, err := os.OpenFile(logAbs, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, fail("", "opening its log: %v", err)
	}
	defer logFile.Close()
	logID := in.newID()
	if err := in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES (?, ?, 'build', 'job', ?, ?, ?)`,
			logID, logRel, jobID, m.ID, in.nowMs())
		return err
	}); err != nil {
		return false, fail("", "recording its log: %v", err)
	}
	fmt.Fprintf(logFile, "# %v\n", why)
	// An environment for another Python, or a partial one, is replaced
	// rather than patched.
	if err := in.removeStudioEntry(m.ID, "venv"); err != nil {
		return false, fail(logID, "removing the old environment: %v", err)
	}
	args := []string{"venv", "--no-project", "--python", version, venv}
	fmt.Fprintf(logFile, "$ uv %s\n", strings.Join(args, " "))
	cctx, cancel := context.WithTimeout(ctx, in.cfg.EnvTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, uv, args...)
	cmd.Dir = filepath.Dir(venv) // outside the checkout: no project to discover
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logFile, logFile
	cmd.Cancel = func() error { return platform.KillGroup(cmd.Process.Pid) }
	cmd.WaitDelay = time.Second
	if err := os.MkdirAll(cmd.Dir, 0o700); err != nil {
		return false, fail(logID, "%v", err)
	}
	runErr := platform.StartInGroup(cmd)
	if runErr == nil {
		runErr = cmd.Wait()
	}
	switch {
	case ctx.Err() != nil:
		f := fail(logID, "stopped")
		f.Code = "cancelled"
		return false, f
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return false, fail(logID, "uv did not finish within %s. Its last output:\n%s", in.cfg.EnvTimeout, readTail(logAbs, 20))
	case runErr != nil:
		return false, fail(logID, "uv venv: %v. Its last output:\n%s", runErr, readTail(logAbs, 20))
	}
	if err := supervisor.CheckVenv(venv, version); err != nil {
		return false, fail(logID, "uv reported success, but %v", err)
	}
	return true, nil
}

// venvAfterBuild checks the environment again once every step has run, and
// resolves what runtime_env records about it: its path, the interpreter's own
// full version (pyvenv.cfg may name only the minor), uv's version, and the
// declared framework's installed version (Q7). An environment a step replaced
// or broke fails the build, and every step runs again on retry, since they
// installed into something else.
func (in *Installer) venvAfterBuild(ctx context.Context, m *manifest.Manifest, uv string, env []string) (map[string]any, *Failure) {
	venv := supervisor.VenvDir(in.cfg.Dirs, m.ID)
	if why := supervisor.CheckVenv(venv, m.Python.Version); why != nil {
		f := &Failure{Phase: "build", Code: "env_failed", Message: fmt.Sprintf(
			"after the build steps ran, %v: a build step replaced or broke the Python %s environment helmstudio made. "+
				"uv sync remakes it when the project's requires-python or .python-version disagrees with the manifest's python.version; "+
				"make them agree, then retry (every step runs again)", why, m.Python.Version)}
		if err := in.forgetSteps(ctx, m.ID); err != nil {
			in.cfg.Logf("install: %s: %v", m.ID, err)
		}
		return nil, f
	}
	info := map[string]any{"venv": venv}
	python := filepath.Join(venv, "bin", "python")
	out, err := in.output(ctx, env, python, "-c", "import platform; print(platform.python_version())")
	if full := strings.TrimSpace(out); err == nil && supervisor.VersionMatches(full, m.Python.Version) {
		info["python"] = full
	} else {
		in.cfg.Logf("install: %s: reading the environment's Python version: %v %q", m.ID, err, full)
	}
	if out, err := in.output(ctx, env, uv, "--version"); err == nil {
		info["uv"] = strings.TrimSpace(out)
	}
	in.frameworkVersion(ctx, m, uv, env, info)
	return info, nil
}

// forgetSteps drops a studio's step records, so the next build runs every step.
func (in *Installer) forgetSteps(ctx context.Context, studioID string) error {
	return in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM step_runs WHERE studio_id = ?`, studioID)
		if err != nil {
			return fmt.Errorf("forgetting the build steps that ran against an older Python environment: %w", err)
		}
		return nil
	})
}

// frameworkVersion records the installed version of the framework the
// manifest declares, read from the environment after the build (Q7).
func (in *Installer) frameworkVersion(ctx context.Context, m *manifest.Manifest, uv string, env []string, info map[string]any) {
	dist, ok := frameworkDist[m.Runtime.Framework]
	if !ok {
		return
	}
	python := filepath.Join(supervisor.VenvDir(in.cfg.Dirs, m.ID), "bin", "python")
	out, err := in.output(ctx, env, uv, "pip", "show", "--python", python, dist)
	if err != nil {
		in.cfg.Logf("install: %s: reading the installed %s version: %v", m.ID, dist, err)
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "Version:"); ok {
			info[dist] = strings.TrimSpace(v)
		}
	}
}

// output runs a short command in its own process group and returns stdout.
func (in *Installer) output(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	err := platform.RunInGroup(cmd)
	return out.String(), err
}

// removeStudioEntry removes <data>/studios/<id>/<name> — a directory
// helmstudio owns — through os.Root on the studio's directory, so a symlink
// on the way is removed as a link and never followed.
func (in *Installer) removeStudioEntry(studioID, name string) error {
	parent := filepath.Join(in.cfg.Dirs.Data(), "studios", studioID)
	fi, err := os.Lstat(parent)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		// A symlink here would make the removal below land in its target.
		return fmt.Errorf("%s is not a directory helmstudio made (it is a %s); not removing anything under it", parent, fi.Mode().Type())
	}
	r, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("opening %s: %w", parent, err)
	}
	defer r.Close()
	if err := r.RemoveAll(name); err != nil {
		return fmt.Errorf("removing %s: %w", filepath.Join(parent, name), err)
	}
	return nil
}
