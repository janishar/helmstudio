package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pythonDevStudio(t *testing.T, envFile string) string {
	t.Helper()
	studio := t.TempDir()
	os.WriteFile(filepath.Join(studio, "helmstudio.yaml"), []byte(`id: snake-dev
name: snake dev
kinds: [audio]
repo: https://example.com/snake-dev.git
license: MIT
python: { version: "3.11" }
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: pytorch, backends: [cpu] }
processes:
  - name: web
    role: main
    cmd: "env > `+envFile+`.tmp; echo venv={venv} >> `+envFile+`.tmp; mv `+envFile+`.tmp `+envFile+`; sleep 60"
`), 0o644)
	return studio
}

// fakeVenv is an environment as uv 0.12.1 leaves it: pyvenv.cfg naming the
// minor, and a bin/python that resolves.
func fakeVenv(t *testing.T, version string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "author venv")
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "python"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte("home = /x\nversion_info = "+version+"\n"), 0o644)
	return dir
}

// Q15: helm dev runs a python: studio in the author's own environment,
// activated as the daemon activates its own, and never creates or changes
// one: without one, or with one for another Python, it refuses and says how to
// make one.
func TestHelmDevUsesTheAuthorsEnvironment(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.txt")
	studio := pythonDevStudio(t, envFile)
	manifest := filepath.Join(studio, "helmstudio.yaml")
	var stdout, stderr syncBuffer

	broken := fakeVenv(t, "3.11")
	os.Remove(filepath.Join(broken, "bin", "python"))
	for _, c := range []struct{ venv, want string }{
		{"", "uv venv --python 3.11"},
		{fakeVenv(t, "3.12"), "Python 3.12;"},
		{filepath.Join(t.TempDir(), "nothing"), "no Python environment"},
		{broken, "no usable interpreter"},
	} {
		err := runDev(devOptions{manifest: manifest, addr: "127.0.0.1:0", venv: c.venv, stdout: &stdout, stderr: &stderr})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "-venv") {
			t.Fatalf("helm dev with venv %q: %v; want a refusal mentioning %q and -venv", c.venv, err, c.want)
		}
	}
	for _, p := range []string{".venv", filepath.Join(".helm", "venv"), filepath.Join(".helm", "studios")} {
		if _, err := os.Stat(filepath.Join(studio, p)); err == nil {
			t.Errorf("helm dev created %s", p)
		}
	}

	venv := fakeVenv(t, "3.11")
	stop := make(chan struct{})
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDev(devOptions{manifest: manifest, addr: "127.0.0.1:0", venv: venv, stdout: &stdout, stderr: &stderr, stop: stop, ready: ready})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("helm dev ended before it was ready: %v\n%s", err, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatal("helm dev never became ready")
	}
	var env string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(envFile); err == nil {
			env = string(b)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the studio never wrote its environment\n%s", stderr.String())
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("helm dev: %v", err)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(env), "\n") {
		k, v, _ := strings.Cut(line, "=")
		vars[k] = v
	}
	if vars["VIRTUAL_ENV"] != venv || vars["UV_PROJECT_ENVIRONMENT"] != venv || vars["venv"] != venv {
		t.Errorf("VIRTUAL_ENV=%q UV_PROJECT_ENVIRONMENT=%q {venv}=%q; want the author's %s", vars["VIRTUAL_ENV"], vars["UV_PROJECT_ENVIRONMENT"], vars["venv"], venv)
	}
	if !strings.HasPrefix(vars["PATH"], filepath.Join(venv, "bin")+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q; want the author's environment first", vars["PATH"])
	}
	for _, k := range []string{"UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR", "UV_NO_CONFIG", "UV_PYTHON_PREFERENCE"} {
		if v, ok := vars[k]; ok {
			t.Errorf("helm dev set %s=%s on the author's own environment", k, v)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(venv, "pyvenv.cfg")); !strings.Contains(string(b), "version_info = 3.11\n") {
		t.Errorf("helm dev changed the author's environment: %s", b)
	}
}
