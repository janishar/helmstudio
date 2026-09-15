package install

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
)

// The test binary doubles as a daemon that can be killed with SIGKILL:
// `<test binary> install-daemon <home> <manifest>` installs the studio and
// waits to be killed.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "install-daemon" {
		os.Exit(installDaemon(os.Args[2], os.Args[3]))
	}
	os.Exit(m.Run())
}

func installDaemon(home, manifestPath string) int {
	ctx := context.Background()
	dirs, err := platform.Under(home)
	if err == nil {
		err = dirs.Ensure()
	}
	if err != nil {
		fmt.Println(err)
		return 1
	}
	st, err := store.Open(ctx, dirs)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	m, res, err := manifest.Load(manifestPath)
	if err != nil || !res.OK() {
		fmt.Println("manifest:", err, res.Errors)
		return 1
	}
	w := weights.New(weights.Config{Store: st, Dirs: dirs})
	sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Weights: w})
	sup.SetStudios([]supervisor.Studio{{Manifest: m, File: manifestPath}})
	in := New(Config{Store: st, Dirs: dirs, Supervisor: sup, Weights: w})
	j, err := in.Install(ctx, m.ID)
	if err != nil {
		fmt.Println("install:", err)
		return 1
	}
	fmt.Println("job", j.ID)
	select {}
}

// Review M3 #1, the human's decision: a build step that outlives a daemon
// killed with SIGKILL is stopped by the next daemon's sweep — its recorded
// pid, start time and group verified — before it is recorded interrupted, so
// a Retry never builds beside it.
func TestSweepStopsABuildStepThatOutlivedAKilledDaemon(t *testing.T) {
	f := newFixture(t, nil)
	home := t.TempDir()
	childPID := filepath.Join(f.work, "child.pid")
	done := filepath.Join(f.work, "slow-may-finish")
	manifestPath := filepath.Join(t.TempDir(), "survivor.yaml")
	os.WriteFile(manifestPath, []byte(fmt.Sprintf(`id: survivor-studio
name: survivor studio
kinds: [video]
repo: file://%s
ref: %s
requires: { os: [darwin, linux], arch: [arm64, amd64], tools: [git] }
runtime: { framework: other, backends: [cpu] }
build:
  - { name: quick, run: "echo quick >> '%s'" }
  - { name: slow, run: "if [ -f '%s' ]; then echo slow >> '%s'; exit 0; fi; sleep 60 & echo $! > '%s'; wait" }
processes:
  - { name: studio, cmd: "exec sleep 60" }
`, f.repo, f.commit, f.counter, done, f.counter, childPID)), 0o600)

	daemon := exec.Command(os.Args[0], "install-daemon", home, manifestPath)
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output strings.Builder
	daemon.Stdout, daemon.Stderr = &output, &output
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(daemon.Process.Pid, syscall.SIGKILL) })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(childPID); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the helper daemon never reached the slow step; its output:\n%s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	pidText, _ := os.ReadFile(childPID)
	child, _ := strconv.Atoi(strings.TrimSpace(string(pidText)))
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })

	// kill -9 the daemon. The step's shell and its sleep survive it.
	syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
	daemon.Wait()
	if syscall.Kill(child, 0) != nil {
		t.Fatal("the step's child died with the daemon; this test needs it to survive")
	}

	// The next daemon.
	dirs, _ := platform.Under(home)
	st, err := store.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var pid, pgid int
	var start sql.NullInt64
	if err := st.Reader().QueryRow(`SELECT pid, pgid, pid_start_time FROM step_runs WHERE step_name = 'slow' AND state = 'running'`).Scan(&pid, &pgid, &start); err != nil {
		t.Fatalf("the running step's identity was not recorded: %v", err)
	}
	if pid <= 1 || pgid != pid || !start.Valid {
		t.Fatalf("recorded identity pid=%d pgid=%d start=%v", pid, pgid, start)
	}
	w := weights.New(weights.Config{Store: st, Dirs: dirs})
	sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Weights: w})
	m, _, _ := manifest.Load(manifestPath)
	sup.SetStudios([]supervisor.Studio{{Manifest: m, File: manifestPath}})
	next := New(Config{Store: st, Dirs: dirs, Supervisor: sup, Weights: w, StepGrace: 2 * time.Second, Logf: t.Logf})
	if err := next.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(child, 0) == nil {
		t.Fatalf("the surviving step's child %d is still running after the sweep", child)
	}
	if alive, _ := platform.GroupExists(pgid); alive {
		t.Fatalf("the surviving step's process group %d is still there", pgid)
	}
	var state, installState string
	st.Reader().QueryRow(`SELECT state FROM step_runs WHERE step_name = 'slow'`).Scan(&state)
	st.Reader().QueryRow(`SELECT install_state FROM installations WHERE studio_id = 'survivor-studio'`).Scan(&installState)
	if state != "interrupted" || installState != StateFailedBuild {
		t.Fatalf("after the sweep: step %s, install %s; want interrupted and failed_build", state, installState)
	}

	// Retry resumes at the interrupted step, with nothing left beside it.
	os.WriteFile(done, nil, 0o644)
	j, err := next.Install(context.Background(), "survivor-studio")
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		got, _ := next.Job(context.Background(), j.ID)
		if got.FinishedAt != nil {
			if got.State != JobSucceeded {
				t.Fatalf("retry = %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, _ := os.ReadFile(f.counter); strings.Join(strings.Fields(string(b)), ",") != "quick,slow" {
		t.Fatalf("steps ran: %q; want quick once, then slow", b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	next.Shutdown(ctx)
}

// The identity guard: a recorded step whose pid now belongs to a different
// process is never signalled.
func TestSweepNeverSignalsARecycledPid(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.install("toy-studio")
	stranger := exec.Command("sleep", "60")
	stranger.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stranger.Process.Kill(); stranger.Wait() })
	pid := stranger.Process.Pid
	if err := f.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		// The pid and group match the stranger; the start time is another
		// process's, as after pid reuse.
		_, err := tx.ExecContext(ctx, `UPDATE step_runs SET state = 'running', pid = ?, pgid = ?, pid_start_time = 1 WHERE step_index = 0`, pid, pid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg
	cfg.StepGrace, cfg.StepKillWait = 200*time.Millisecond, 200*time.Millisecond
	if err := New(cfg).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	// IdentifyProcess reads a signalled, unreaped child as gone, where
	// kill(pid, 0) would still succeed on the zombie.
	if _, err := platform.IdentifyProcess(pid); err != nil {
		t.Fatalf("the sweep signalled pid %d, which belongs to another process: %v", pid, err)
	}
}

// The installer hands link the studio's other weights, so a link standing
// for h3's shared repository must hold the required one.
func TestLinkWeightChecksTheStudiosOtherWeights(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("toy-studio")
	st.Manifest.Weights = []manifest.Weight{
		{Name: "fl2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"FL2VA/**"}},
		{Name: "ref2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"Ref2VA/**"}, Optional: true},
	}
	onlyRef := filepath.Join(t.TempDir(), "only-ref2va")
	os.MkdirAll(filepath.Join(onlyRef, "Ref2VA"), 0o755)
	os.WriteFile(filepath.Join(onlyRef, "Ref2VA", "dit.safetensors"), []byte("y"), 0o644)
	if _, err := f.in.LinkWeight(context.Background(), "toy-studio", "ref2va", onlyRef); err == nil || !strings.Contains(err.Error(), `"fl2va"`) {
		t.Fatalf("linking ref2va without the required fl2va: err = %v", err)
	}
}
