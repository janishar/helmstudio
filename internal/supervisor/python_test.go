package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
)

// makeVenv writes the part of an environment a launch reads: pyvenv.cfg as
// uv 0.12.1 writes it (version_info names only the minor), and bin/python.
func makeVenv(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "python"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "home = /opt/python/bin\nimplementation = CPython\nuv = 0.12.1\nversion_info = " + version + "\ninclude-system-site-packages = false\n"
	if err := os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envValue(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// Q5, Q6: a studio that declares python: has its environment active in every
// process and exec health probe — <venv>/bin first on PATH, VIRTUAL_ENV,
// UV_PROJECT_ENVIRONMENT, and the uv settings for an environment helmstudio
// owns — and {venv} names it. A studio without python: gets none of it.
func TestAPythonStudioRunsInsideItsEnvironment(t *testing.T) {
	e := newEnv(t, Config{})
	out := filepath.Join(t.TempDir(), "env.txt")
	e.manifest("snake", `python: { version: "3.11" }`, `
  - name: studio
    cmd: env > `+out+`; echo venv={venv} >> `+out+`; exec sleep 60
    health: { exec: '[ -n "$VIRTUAL_ENV" ] && [ "$UV_PROJECT_ENVIRONMENT" = "$VIRTUAL_ENV" ]', timeout_s: 10, interval_s: 1 }
`)
	venv := VenvDir(e.dirs, "snake")
	makeVenv(t, venv, "3.11")

	if _, err := e.sup.Launch(context.Background(), "snake", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("snake", stateRunning, 10*time.Second) // the exec probe saw the environment
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := map[string]string{
		"VIRTUAL_ENV":            venv,
		"UV_PROJECT_ENVIRONMENT": venv,
		"UV_PYTHON_PREFERENCE":   "only-managed",
		"UV_PYTHON_INSTALL_DIR":  filepath.Join(e.dirs.Data(), "python"),
		"UV_CACHE_DIR":           filepath.Join(e.dirs.Cache(), "uv"),
		"UV_NO_CONFIG":           "1",
		"venv":                   venv,
	}
	for k, v := range want {
		if got, _ := envValue(env, k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if path, _ := envValue(env, "PATH"); !strings.HasPrefix(path, filepath.Join(venv, "bin")+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q; want the environment's bin first", path)
	}
	if !strings.HasPrefix(venv, filepath.Join(e.dirs.Data(), "studios", "snake")) || strings.HasPrefix(venv, e.root) {
		t.Errorf("environment %s; want it under the studio's data directory and outside the checkout %s", venv, e.root)
	}

	plain := e.manifest("plain", "", `
  - name: studio
    cmd: exec sleep 60
`)
	lp, err := e.sup.installed(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolvePlan(e.dirs, plain, lp, nil, e.sup.alloc)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"VIRTUAL_ENV", "UV_PROJECT_ENVIRONMENT", "UV_PYTHON_PREFERENCE", "UV_CACHE_DIR", "UV_NO_CONFIG"} {
		if v, ok := envValue(plan[0].env, k); ok {
			t.Errorf("a studio without python: got %s=%s", k, v)
		}
	}
	e.manifest("no-venv", "", `
  - name: studio
    cmd: exec {venv}/bin/python
`)
	var se *Error
	if _, err := e.sup.Launch(context.Background(), "no-venv", LaunchOptions{}); !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, "declares no python block") {
		t.Fatalf("{venv} without python: %v; want not launchable, naming the missing block", err)
	}
}

// A Python studio whose environment is gone, or was made for another Python,
// is refused at launch with what to do, not started to fail at import time.
func TestAMissingOrMismatchedEnvironmentRefusesTheLaunch(t *testing.T) {
	e := newEnv(t, Config{})
	e.manifest("snake", `python: { version: "3.11" }`, `
  - name: studio
    cmd: exec sleep 60
`)
	var se *Error
	_, err := e.sup.Launch(context.Background(), "snake", LaunchOptions{})
	if !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, "no Python environment") || !strings.Contains(se.Message, "install snake again") {
		t.Fatalf("launch with no environment: %v", err)
	}
	makeVenv(t, VenvDir(e.dirs, "snake"), "3.10")
	_, err = e.sup.Launch(context.Background(), "snake", LaunchOptions{})
	if !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, "Python 3.10;") || !strings.Contains(se.Message, "3.11") {
		t.Fatalf("launch with a 3.10 environment for a 3.11 studio: %v", err)
	}
	// Right version, but its interpreter link points at a deleted Python.
	os.RemoveAll(VenvDir(e.dirs, "snake"))
	makeVenv(t, VenvDir(e.dirs, "snake"), "3.11")
	python := filepath.Join(VenvDir(e.dirs, "snake"), "bin", "python")
	os.Remove(python)
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone", "python3.11"), python); err != nil {
		t.Fatal(err)
	}
	_, err = e.sup.Launch(context.Background(), "snake", LaunchOptions{})
	if !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, "no usable interpreter") {
		t.Fatalf("launch with a broken interpreter link: %v; want not launchable", err)
	}
	if rows := e.rows("snake"); len(rows) != 0 {
		t.Fatalf("a refused launch wrote rows: %v", rows)
	}
}

// Config.Python replaces where the environment comes from (helm dev), and a
// manifest's own env still has the last word.
func TestPythonConfigAndManifestEnvOverride(t *testing.T) {
	own := t.TempDir()
	e := newEnv(t, Config{Python: func(m *manifest.Manifest) (string, []string, error) { return own, nil, nil }})
	st := e.manifest("snake", `python: { version: "3.11" }`, `
  - name: studio
    cmd: exec {venv}/bin/python
    env: { UV_NO_CONFIG: "0" }
`)
	lp, err := e.sup.installed(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolvePlan(e.dirs, st, lp, nil, e.sup.alloc)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(plan[0].env, "VIRTUAL_ENV"); v != own {
		t.Errorf("VIRTUAL_ENV = %q, want the configured %q", v, own)
	}
	if _, ok := envValue(plan[0].env, "UV_CACHE_DIR"); ok {
		t.Error("an environment helmstudio does not own was given helmstudio's uv cache")
	}
	if v, _ := envValue(plan[0].env, "UV_NO_CONFIG"); v != "0" {
		t.Errorf("UV_NO_CONFIG = %q; the manifest's env should win", v)
	}
	if !strings.Contains(strings.Join(plan[0].argv, " "), filepath.Join(own, "bin", "python")) {
		t.Errorf("argv %q does not name the configured environment", plan[0].argv)
	}
}

func TestActivateAndVenvVersion(t *testing.T) {
	env := Activate([]string{"HOME=/h", "PATH=/usr/bin:/bin", "VIRTUAL_ENV=/old", "PYTHONHOME=/py"}, "/v")
	if v, _ := envValue(env, "PATH"); v != "/v/bin:/usr/bin:/bin" {
		t.Errorf("PATH = %q", v)
	}
	for _, kv := range env {
		if kv == "VIRTUAL_ENV=/old" || strings.HasPrefix(kv, "PYTHONHOME=") {
			t.Errorf("a stale %s survived activation", kv)
		}
	}
	if v, _ := envValue(env, "HOME"); v != "/h" {
		t.Errorf("HOME = %q; activation must not drop other variables", v)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte("home = /x\nversion = 3.10.12\n"), 0o644)
	if v, err := VenvVersion(dir); err != nil || v != "3.10.12" {
		t.Errorf("python -m venv's cfg: %q %v", v, err)
	}
	for full, ok := range map[string]bool{"3.11": true, "3.11.0": true, "3.11.13": true, "3.1.1": false, "3.110.1": false, "3.12.0": false} {
		if VersionMatches(full, "3.11") != ok {
			t.Errorf("VersionMatches(%q, 3.11) = %v", full, !ok)
		}
	}
}
