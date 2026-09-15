package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/supervisor"
)

// fakeUV is a uv on PATH that records every call and never reaches the
// network. `uv venv ... <dir>` writes a pyvenv.cfg naming only the minor, as
// uv 0.12.1 does, and a bin/python that reports <minor>.7 — or the version in
// the file named by "reports" — and fails when "fail" exists. `uv --version`
// and `uv pip show` answer; anything else succeeds.
type fakeUV struct {
	bin     string // the directory holding uv
	calls   string // one line per call: argv, then selected environment
	reports string // when present, the version venv writes instead
	fail    string // when present, venv exits 1
}

func newFakeUV(t *testing.T) *fakeUV {
	t.Helper()
	dir := t.TempDir()
	u := &fakeUV{bin: filepath.Join(dir, "bin"), calls: filepath.Join(dir, "calls"), reports: filepath.Join(dir, "reports"), fail: filepath.Join(dir, "fail")}
	os.MkdirAll(u.bin, 0o755)
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s | cwd=%%s VIRTUAL_ENV=%%s UV_PROJECT_ENVIRONMENT=%%s UV_PYTHON_PREFERENCE=%%s UV_PYTHON_INSTALL_DIR=%%s UV_CACHE_DIR=%%s UV_NO_CONFIG=%%s\n' "$*" "$PWD" "$VIRTUAL_ENV" "$UV_PROJECT_ENVIRONMENT" "$UV_PYTHON_PREFERENCE" "$UV_PYTHON_INSTALL_DIR" "$UV_CACHE_DIR" "$UV_NO_CONFIG" >> '%[1]s'
case "$1" in
--version) echo "uv 0.0.0 (fake)" ;;
venv)
  if [ -f '%[3]s' ]; then echo "error: no interpreter for Python $4 found" >&2; exit 1; fi
  for last; do :; done
  version="$4.7"
  if [ -f '%[2]s' ]; then version=$(cat '%[2]s'); fi
  minor=$(echo "$version" | cut -d. -f1,2)
  mkdir -p "$last/bin" && printf 'home = /fake\nimplementation = CPython\nversion_info = %%s\n' "$minor" > "$last/pyvenv.cfg"
  printf '#!/bin/sh\necho %%s\n' "$version" > "$last/bin/python" && chmod +x "$last/bin/python"
  echo "Creating virtual environment at: $last" ;;
pip) if [ "$2" = show ]; then printf 'Name: %%s\nVersion: 2.7.1\n' "$5"; fi ;;
esac
`, u.calls, u.reports, u.fail)
	if err := os.WriteFile(filepath.Join(u.bin, "uv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return u
}

// config puts the fake uv on the PATH install sees and looks tools up there.
func (u *fakeUV) config(c *Config) {
	path := u.bin + string(os.PathListSeparator) + os.Getenv("PATH")
	c.Environ = func() []string {
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "PATH=") {
				env = append(env, kv)
			}
		}
		return append(env, "PATH="+path)
	}
	c.LookPath = func(name string) (string, error) {
		for _, dir := range filepath.SplitList(path) {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
				return filepath.Join(dir, name), nil
			}
		}
		return "", errors.New("not found")
	}
}

// venvCalls returns the recorded `uv venv` calls.
func (u *fakeUV) venvCalls(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(u.calls)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(line, "venv ") {
			out = append(out, line)
		}
	}
	return out
}

func envLine(t *testing.T, file string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, kv := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// pythonBuild is a build whose step records the environment it ran in.
func pythonBuild(envFile string) string {
	return fmt.Sprintf(`
  - { name: sync, run: "uv sync --locked; env > '%s'" }
`, envFile)
}

// R12, Q4–Q7: a python: studio gets its own environment, made by uv for the
// declared Python outside its checkout before step 0; its steps run with it
// active and with uv's settings for an environment helmstudio owns;
// runtime_env records it; a second install keeps it; the studio launches.
func TestPythonStudioGetsItsOwnEnvironment(t *testing.T) {
	u := newFakeUV(t)
	f := newFixture(t, u.config)
	envFile := filepath.Join(f.work, "step.env")
	m := f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", pythonBuild(envFile))
	m.Runtime.Framework = "pytorch"

	j := f.install("snake-studio")
	info := f.info("snake-studio")
	if j.State != JobSucceeded || info.State != StateReady {
		t.Fatalf("install: job %+v, info %+v", j, info)
	}
	venv := filepath.Join(f.dirs.Data(), "studios", "snake-studio", "venv")
	if got, err := supervisor.VenvVersion(venv); err != nil || got != "3.11" {
		t.Fatalf("environment at %s: %q %v; want Python 3.11", venv, got, err)
	}
	calls := u.venvCalls(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "venv --no-project --python 3.11 "+venv+" |") {
		t.Fatalf("uv venv calls = %q; want one, for 3.11 at %s", calls, venv)
	}
	for _, want := range []string{"UV_PYTHON_PREFERENCE=only-managed", "UV_NO_CONFIG=1",
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(f.dirs.Data(), "python"), "UV_CACHE_DIR=" + filepath.Join(f.dirs.Cache(), "uv")} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("uv venv ran without %s: %s", want, calls[0])
		}
	}
	if root := info.Root; strings.Contains(calls[0], "cwd="+root) || strings.HasPrefix(venv, root) {
		t.Errorf("the environment was made inside the checkout %s: %s", root, calls[0])
	}

	step := envLine(t, envFile)
	if step["VIRTUAL_ENV"] != venv || step["UV_PROJECT_ENVIRONMENT"] != venv || step["UV_NO_CONFIG"] != "1" {
		t.Errorf("the build step's environment: VIRTUAL_ENV=%q UV_PROJECT_ENVIRONMENT=%q UV_NO_CONFIG=%q; want %s active", step["VIRTUAL_ENV"], step["UV_PROJECT_ENVIRONMENT"], step["UV_NO_CONFIG"], venv)
	}
	if !strings.HasPrefix(step["PATH"], filepath.Join(venv, "bin")+string(os.PathListSeparator)) {
		t.Errorf("the build step's PATH = %q; want the environment's bin first", step["PATH"])
	}
	if len(j.Steps) != 1 || j.Steps[0].Index != 0 {
		t.Errorf("steps = %+v; want the manifest's one step at index 0, creating the environment not counted", j.Steps)
	}

	var rt map[string]any
	if err := json.Unmarshal(info.RuntimeEnv, &rt); err != nil {
		t.Fatal(err)
	}
	tools, _ := rt["tools"].(map[string]any)
	if rt["venv"] != venv || rt["python"] != "3.11.7" || rt["uv"] != "uv 0.0.0 (fake)" || rt["torch"] != "2.7.1" || tools["uv"] == nil {
		t.Errorf("runtime_env = %s; want venv, python, uv, torch and uv among the tools", info.RuntimeEnv)
	}
	if n := f.count(`SELECT count(*) FROM log_files WHERE owner_kind = 'job' AND owner_id = ? AND kind = 'build'`, j.ID); n != 1 {
		t.Errorf("%d job-owned build logs; want the environment's log", n)
	}

	f.install("snake-studio")
	if calls := u.venvCalls(t); len(calls) != 1 {
		t.Fatalf("a second install called uv venv again: %q", calls)
	}
	if _, err := f.sup.Launch(context.Background(), "snake-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatalf("launching the installed Python studio: %v", err)
	}
}

// A changed python.version recreates the environment for the new Python;
// two studios never share one; uninstall removes it and keeps the data.
func TestEnvironmentFollowsTheManifestAndGoesWithUninstall(t *testing.T) {
	u := newFakeUV(t)
	f := newFixture(t, u.config)
	build := pythonBuild(filepath.Join(f.work, "step.env"))
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", build)
	f.studio("adder-studio", f.repoLine()+"\npython: { version: \"3.11\" }", build)
	if j := f.install("snake-studio"); j.State != JobSucceeded {
		t.Fatalf("install snake: %+v", j)
	}
	if j := f.install("adder-studio"); j.State != JobSucceeded {
		t.Fatalf("install adder: %+v", j)
	}
	snake := filepath.Join(f.dirs.Data(), "studios", "snake-studio", "venv")
	adder := filepath.Join(f.dirs.Data(), "studios", "adder-studio", "venv")
	calls := u.venvCalls(t)
	if len(calls) != 2 || !strings.Contains(calls[0], snake+" |") || !strings.Contains(calls[1], adder+" |") {
		t.Fatalf("uv venv calls = %q; want one environment per studio", calls)
	}

	// A manifest change that keeps the Python rebuilds the studio and keeps
	// its environment.
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", build+"  - { name: again, run: \"true\" }\n")
	if j := f.install("snake-studio"); j.State != JobSucceeded || len(j.Steps) != 2 {
		t.Fatalf("install after adding a step: %+v; want both steps run again", j)
	}
	if calls := u.venvCalls(t); len(calls) != 2 {
		t.Fatalf("a rebuild for the same Python made the environment again: %q", calls)
	}

	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.10\" }", build)
	if j := f.install("snake-studio"); j.State != JobSucceeded {
		t.Fatalf("install after changing python.version: %+v", j)
	}
	if got, _ := supervisor.VenvVersion(snake); got != "3.10" {
		t.Fatalf("after changing python.version to 3.10 the environment is %q", got)
	}
	if got, _ := supervisor.VenvVersion(adder); got != "3.11" {
		t.Fatalf("recreating one studio's environment changed another's: %q", got)
	}

	data := filepath.Join(f.dirs.Data(), "studios", "snake-studio", "data")
	os.MkdirAll(data, 0o700)
	j, err := f.in.Uninstall(context.Background(), "snake-studio")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(j); got.State != JobSucceeded {
		t.Fatalf("uninstall: %+v", got)
	}
	if _, err := os.Lstat(snake); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left the environment: %v", err)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatalf("uninstall removed the studio's data: %v", err)
	}
	if _, err := os.Stat(adder); err != nil {
		t.Fatalf("uninstalling one studio removed another's environment: %v", err)
	}
}

// Q3: with no uv, a python: studio is refused before anything is cloned or
// recorded, naming uv and how to get it.
func TestNoUVBlocksBeforeTheClone(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.LookPath = func(name string) (string, error) {
			if name == "uv" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
	})
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", pythonBuild(filepath.Join(f.work, "step.env")))
	_, err := f.in.Install(context.Background(), "snake-studio")
	var ie *Error
	if !errors.As(err, &ie) || ie.Kind != KindBlocked || ie.Details["code"] != "tool_missing" || ie.Details["tool"] != "uv" ||
		!strings.Contains(ie.Message, "uv") || !strings.Contains(ie.Message, "brew install uv") {
		t.Fatalf("install without uv: %v (%+v); want blocked, tool_missing naming uv and how to install it", err, ie)
	}
	if n := f.count(`SELECT count(*) FROM installations`); n != 0 {
		t.Fatalf("a blocked install recorded %d installations", n)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Data(), "studios", "snake-studio", "src")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something was cloned: %v", err)
	}
}

// A uv that fails, or that makes an environment for another Python, fails
// the build with env_failed, no step index, uv's output and its log; no step
// runs; a retry once uv works resumes and makes the environment.
func TestAFailedEnvironmentFailsTheBuildBeforeStepZero(t *testing.T) {
	u := newFakeUV(t)
	f := newFixture(t, u.config)
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", f.defaultBuild())
	os.WriteFile(u.fail, nil, 0o644)

	j := f.install("snake-studio")
	info := f.info("snake-studio")
	lf := info.LastFailure
	if j.State != JobFailed || info.State != StateFailedBuild || lf == nil || lf.Code != "env_failed" || lf.StepIndex != nil || lf.LogFileID == "" {
		t.Fatalf("job %+v, info %+v; want failed_build with env_failed, no step index, a log", j, info)
	}
	if !strings.Contains(lf.Message, "no interpreter for Python 3.11") {
		t.Errorf("the failure does not carry uv's output: %q", lf.Message)
	}
	if f.steps() != "" {
		t.Fatalf("steps ran without an environment: %s", f.steps())
	}

	os.Remove(u.fail)
	os.WriteFile(u.reports, []byte("3.12.1"), 0o644)
	j = f.install("snake-studio")
	if lf := f.info("snake-studio").LastFailure; j.State != JobFailed || lf == nil || lf.Code != "env_failed" || !strings.Contains(lf.Message, "Python 3.12;") {
		t.Fatalf("uv made a 3.12 environment for a 3.11 studio: job %+v, failure %+v", j, lf)
	}

	os.Remove(u.reports)
	if j := f.install("snake-studio"); j.State != JobSucceeded || f.steps() != "one,two,three" {
		t.Fatalf("retry with a working uv: %+v, steps %s", j, f.steps())
	}
}

// The same install against the real uv, which downloads a managed CPython
// and so needs the network: run only with HELM_REAL_UV=1, never in the gate.
// It proves the flags and variables helmstudio passes are ones uv accepts.
func TestRealUVMakesTheEnvironment(t *testing.T) {
	if os.Getenv("HELM_REAL_UV") != "1" {
		t.Skip("set HELM_REAL_UV=1 to create a real uv environment (downloads a managed Python)")
	}
	f := newFixture(t, nil)
	envFile := filepath.Join(f.work, "step.env")
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", fmt.Sprintf(`
  - { name: python, run: "python -c 'import sys; print(sys.prefix)' > '%s'" }
`, envFile))
	j := f.install("snake-studio")
	info := f.info("snake-studio")
	if j.State != JobSucceeded || info.State != StateReady {
		t.Fatalf("install with the real uv: job %+v, info %+v", j, info)
	}
	venv := filepath.Join(f.dirs.Data(), "studios", "snake-studio", "venv")
	b, _ := os.ReadFile(envFile)
	if strings.TrimSpace(string(b)) != venv {
		t.Fatalf("python in the build step ran from %q, want the environment %s", b, venv)
	}
	if got, _ := supervisor.VenvVersion(venv); !supervisor.VersionMatches(got, "3.11") {
		t.Fatalf("environment Python %q", got)
	}
	var rt map[string]any
	if err := json.Unmarshal(info.RuntimeEnv, &rt); err != nil {
		t.Fatal(err)
	}
	// uv 0.12.1 writes only the minor into pyvenv.cfg; runtime_env carries the
	// interpreter's own full version (review #2).
	if full, _ := rt["python"].(string); len(strings.Split(full, ".")) != 3 || !supervisor.VersionMatches(full, "3.11") {
		t.Fatalf("runtime_env.python = %v; want a full 3.11.x version", rt["python"])
	}
	if entries, err := os.ReadDir(filepath.Join(f.dirs.Data(), "python")); err != nil || len(entries) == 0 {
		t.Fatalf("no managed interpreter under the data root: %v", err)
	}
	t.Logf("runtime_env: %s", info.RuntimeEnv)
}

// Review #1: a build step can replace the environment — uv sync remakes it
// for the project's own requires-python and exits 0. The build then fails
// env_failed, saying a step replaced it, records no runtime_env, and a retry
// runs every step again against a remade environment.
func TestABuildStepThatReplacesTheEnvironmentFailsTheBuild(t *testing.T) {
	u := newFakeUV(t)
	f := newFixture(t, u.config)
	flag := filepath.Join(f.work, "replace")
	os.WriteFile(flag, nil, 0o644)
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", fmt.Sprintf(`
  - { name: one, run: "echo one >> '%[1]s'" }
  - { name: sync, run: "echo sync >> '%[1]s'; if [ -f '%[2]s' ]; then printf 'version_info = 3.14\n' > \"$VIRTUAL_ENV/pyvenv.cfg\"; fi" }
`, f.counter, flag))

	j := f.install("snake-studio")
	info := f.info("snake-studio")
	lf := info.LastFailure
	if j.State != JobFailed || info.State != StateFailedBuild || lf == nil || lf.Code != "env_failed" || lf.StepIndex != nil ||
		!strings.Contains(lf.Message, "Python 3.14") || !strings.Contains(lf.Message, "a build step replaced") {
		t.Fatalf("job %+v, info %+v; want failed_build env_failed naming the replacement", j, info)
	}
	if info.RuntimeEnv != nil {
		t.Errorf("runtime_env recorded for a replaced environment: %s", info.RuntimeEnv)
	}
	if n := f.count(`SELECT count(*) FROM step_runs WHERE studio_id = 'snake-studio'`); n != 0 {
		t.Errorf("%d step records kept; every step installed into the replaced environment", n)
	}

	os.Remove(flag)
	if j := f.install("snake-studio"); j.State != JobSucceeded || f.steps() != "one,sync,one,sync" {
		t.Fatalf("retry once the step no longer replaces it: %+v, steps %s; want both steps again", j, f.steps())
	}
	if calls := u.venvCalls(t); len(calls) != 2 {
		t.Fatalf("uv venv calls = %q; want the environment remade on retry", calls)
	}
}

// Review #10 and #1: an environment remade after steps already succeeded holds
// nothing they installed, so they all run again; and a ready studio whose
// interpreter was deleted builds again on install, as a launch's refusal
// says to do.
func TestARemadeEnvironmentRunsEveryStepAgain(t *testing.T) {
	u := newFakeUV(t)
	f := newFixture(t, u.config)
	f.studio("snake-studio", f.repoLine()+"\npython: { version: \"3.11\" }", f.defaultBuild())
	venv := filepath.Join(f.dirs.Data(), "studios", "snake-studio", "venv")
	breakInterpreter := func() {
		python := filepath.Join(venv, "bin", "python")
		os.Remove(python)
		if err := os.Symlink(filepath.Join(f.work, "deleted", "python3.11"), python); err != nil {
			t.Fatal(err)
		}
	}

	f.failStepTwo(true)
	if j := f.install("snake-studio"); j.State != JobFailed || f.steps() != "one" {
		t.Fatalf("first install: %+v, steps %s; want step two to fail", j, f.steps())
	}
	breakInterpreter()
	f.failStepTwo(false)
	if j := f.install("snake-studio"); j.State != JobSucceeded || f.steps() != "one,one,two,three" {
		t.Fatalf("retry after the environment broke: %+v, steps %s; want step one again", j, f.steps())
	}

	breakInterpreter()
	if _, err := f.sup.Launch(context.Background(), "snake-studio", supervisor.LaunchOptions{}); err == nil || !strings.Contains(err.Error(), "install snake-studio again") {
		t.Fatalf("launch with a broken interpreter: %v; want a refusal saying to install again", err)
	}
	if j := f.install("snake-studio"); j.State != JobSucceeded || f.steps() != "one,one,two,three,one,two,three" {
		t.Fatalf("install again on a ready studio with a broken environment: %+v, steps %s; want a full rebuild", j, f.steps())
	}
	if calls := u.venvCalls(t); len(calls) != 3 {
		t.Fatalf("uv venv calls = %d; want the environment made three times", len(calls))
	}
	if _, err := f.sup.Launch(context.Background(), "snake-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatalf("launch after installing again: %v", err)
	}
}
