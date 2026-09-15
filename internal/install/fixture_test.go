package install

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

type fixture struct {
	t    *testing.T
	dirs *platform.Dirs
	st   *store.Store
	hub  *hubtest.Hub
	w    *weights.Service
	sup  *supervisor.Supervisor
	in   *Installer
	cfg  Config

	work    string // outside helmstudio: the repo, counters, flags
	repo    string
	commit  string
	counter string
}

func newFixture(t *testing.T, tweak func(*Config)) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; install tests clone a local repository")
	}
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dirs: d, st: st, hub: hubtest.New(t), work: t.TempDir()}
	f.counter = filepath.Join(f.work, "steps.log")
	hf := &weights.HF{
		Endpoint: f.hub.URL(),
		Token:    func(context.Context) (string, error) { return "", nil },
		Client:   &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Backoff:  []time.Duration{time.Millisecond},
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
	f.w = weights.New(weights.Config{Store: st, Dirs: d, HF: hf, FreeDisk: func(string) (uint64, error) { return 1 << 50, nil }, Logf: t.Logf})
	f.sup = supervisor.New(supervisor.Config{Dirs: d, Store: st, Weights: f.w, Grace: 2 * time.Second, PortMin: 41000, PortMax: 41999, Logf: t.Logf,
		HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	f.cfg = Config{Store: st, Dirs: d, Supervisor: f.sup, Weights: f.w, Logf: t.Logf,
		HostOS: func() string { return "darwin" }, HostArch: func() string { return "arm64" },
		HostBackends: func() []string { return []string{"metal", "mps", "cpu"} },
		HostMemory:   func() (uint64, error) { return 64 << 30, nil },
		FreeDisk:     func(string) (uint64, error) { return 1 << 50, nil },
	}
	if tweak != nil {
		tweak(&f.cfg)
	}
	f.in = New(f.cfg)
	f.hub.Add("org/toy", "model.bin", hubtest.Content(4096, 1), true)
	f.hub.Add("org/toy", "config.json", []byte(`{}`), false)
	f.makeRepo()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f.in.Shutdown(ctx)
		f.sup.Shutdown(ctx)
		st.Close()
	})
	return f
}

// gitTest runs git for the test's own setup, isolated from the user's
// configuration (a global commit.gpgsign would otherwise fail the commit).
func (f *fixture) gitTest(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=helmstudio test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) makeRepo() {
	f.repo = filepath.Join(f.work, "toy-studio.git")
	os.MkdirAll(filepath.Join(f.repo, "engine"), 0o755)
	os.WriteFile(filepath.Join(f.repo, "README"), []byte("toy\n"), 0o644)
	os.WriteFile(filepath.Join(f.repo, "engine", "Makefile"), []byte("all:\n\t@echo engine\n"), 0o644)
	f.gitTest(f.repo, "init", "-q")
	f.gitTest(f.repo, "add", ".")
	f.gitTest(f.repo, "commit", "-q", "-m", "toy")
	f.commit = f.gitTest(f.repo, "rev-parse", "HEAD")
}

// defaultBuild appends each step's name to the counter file, so a test can
// see exactly which steps ran, and fails step two while flag exists.
func (f *fixture) defaultBuild() string {
	flag := filepath.Join(f.work, "fail-two")
	return fmt.Sprintf(`
  - { name: one, cwd: engine, run: "echo one >> '%[1]s'" }
  - { name: two, run: "if [ -f '%[2]s' ]; then echo boom from step two; exit 3; fi; echo two >> '%[1]s'" }
  - { name: three, run: "echo three >> '%[1]s'; touch built" }
`, f.counter, flag)
}

func (f *fixture) failStepTwo(on bool) {
	flag := filepath.Join(f.work, "fail-two")
	if on {
		os.WriteFile(flag, nil, 0o644)
	} else {
		os.Remove(flag)
	}
}

// studio writes a manifest and registers it with the supervisor.
func (f *fixture) studio(id, extra, build string) *manifest.Manifest {
	f.t.Helper()
	body := fmt.Sprintf(`id: %s
name: %s
kinds: [video]
%s
requires: { os: [darwin, linux], arch: [arm64, amd64], tools: [git] }
runtime: { framework: other, backends: [cpu] }
build:%s
weights:
  - { name: base, repo: org/toy, dest: Toy, files: ["*.bin"] }
processes:
  - name: studio
    cmd: "echo model {models.base}; exec sleep 60"
`, id, strings.ReplaceAll(id, "-", " "), extra, build)
	file := filepath.Join(f.t.TempDir(), id+".yaml")
	os.WriteFile(file, []byte(body), 0o600)
	m, res, err := manifest.Load(file)
	if err != nil || !res.OK() {
		f.t.Fatalf("manifest: %v %v\n%s", err, res.Errors, body)
	}
	var studios []supervisor.Studio
	for _, s := range f.sup.Studios() {
		if s.Manifest.ID != id {
			studios = append(studios, s)
		}
	}
	f.sup.SetStudios(append(studios, supervisor.Studio{Manifest: m, File: file}))
	return m
}

func (f *fixture) repoLine() string {
	return fmt.Sprintf("repo: file://%s\nref: %s", f.repo, f.commit)
}

// wait blocks until a job is no longer running and returns it.
func (f *fixture) wait(j Job) Job {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := f.in.Job(context.Background(), j.ID)
		if err != nil {
			f.t.Fatal(err)
		}
		f.in.mu.Lock()
		running := f.in.active[j.ID] != nil
		f.in.mu.Unlock()
		if got.State != JobRunning && !running {
			return got
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("job %s (%s) still %s after 30s: %+v", j.ID, j.Kind, got.State, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) install(id string) Job {
	f.t.Helper()
	j, err := f.in.Install(context.Background(), id)
	if err != nil {
		f.t.Fatalf("install %s: %v", id, err)
	}
	return f.wait(j)
}

func (f *fixture) info(id string) Info {
	f.t.Helper()
	st, _ := f.sup.Studio(id)
	var m *manifest.Manifest
	if st.Manifest != nil {
		m = st.Manifest
	}
	info, err := f.in.Info(context.Background(), id, m)
	if err != nil {
		f.t.Fatal(err)
	}
	return info
}

func (f *fixture) steps() string {
	b, _ := os.ReadFile(f.counter)
	return strings.Join(strings.Fields(string(b)), ",")
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.st.Reader().QueryRow(query, args...).Scan(&n); err != nil && err != sql.ErrNoRows {
		f.t.Fatal(err)
	}
	return n
}
