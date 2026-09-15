package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
)

// 05 §10: a studio runs "with a restricted environment, no inherited secrets".
// The daemon's tokens and cloud keys do not reach a studio process or its
// exec health probe; PATH and HOME do, and so does the manifest's own env.
func TestStudiosDoNotInheritTheDaemonEnvironment(t *testing.T) {
	t.Setenv("HF_TOKEN", "hf_secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_secret")
	t.Setenv("LC_ALL", "C")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	e := newEnv(t, Config{})
	dump := filepath.Join(t.TempDir(), "probe-env")
	e.manifest("envcheck", "", `
  - name: studio
    role: main
    cmd: 'env; exec sleep 60'
    env: { STUDIO_FLAG: "on" }
    health: { exec: 'env > `+dump+`', timeout_s: 10, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "envcheck", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("envcheck", stateRunning, 15*time.Second)
	waitFor(t, 5*time.Second, "the process's env in its log", func() bool {
		b, _ := os.ReadFile(e.logPath("envcheck", "studio"))
		return strings.Contains(string(b), "STUDIO_FLAG=on")
	})
	procEnv, _ := os.ReadFile(e.logPath("envcheck", "studio"))
	probeEnv, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	noToken := filepath.Join(e.dirs.Data(), "studios", "envcheck", "no-hf-token")
	for name, env := range map[string]string{"process": string(procEnv), "health probe": string(probeEnv)} {
		lines := "\n" + env
		for _, secret := range []string{"\nHF_TOKEN=", "\nAWS_SECRET_ACCESS_KEY=", "hf_secret", "aws_secret", "\nSSH_AUTH_SOCK=", "\n" + helperEnv + "="} {
			if strings.Contains(lines, secret) {
				t.Errorf("%s environment contains %q", name, strings.TrimPrefix(secret, "\n"))
			}
		}
		// HOME passes, so the token `hf auth login` wrote under it must be
		// steered away from, and implicit tokens switched off.
		for _, want := range []string{"\nPATH=", "\nHOME=", "\nLC_ALL=C\n", "\nSTUDIO_FLAG=on\n",
			"\nHTTPS_PROXY=http://proxy.example:3128\n", "\nHF_TOKEN_PATH=" + noToken + "\n", "\nHF_HUB_DISABLE_IMPLICIT_TOKEN=1\n"} {
			if !strings.Contains(lines, want) {
				t.Errorf("%s environment lacks %q", name, strings.Trim(want, "\n"))
			}
		}
	}
	if _, err := os.Stat(noToken); !os.IsNotExist(err) {
		t.Errorf("HF_TOKEN_PATH %s exists; it must never hold a token: %v", noToken, err)
	}
}

// A process that cannot be recorded is not left running: a restarted daemon
// could never find it. The launch fails and the process group is gone.
func TestSpawnThatCannotBeRecordedIsKilled(t *testing.T) {
	e := newEnv(t, Config{})
	e.sup.cfg.failWrite = func(what string) error {
		if what == "spawned process" {
			return errors.New("disk full")
		}
		return nil
	}
	marker := "unrecorded-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	e.manifest("unrecorded", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name `+marker+`
`)
	if _, err := e.sup.Launch(context.Background(), "unrecorded", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("unrecorded")
	gs, _ := e.sup.Status(context.Background(), "unrecorded")
	if gs.State != stateFailed {
		t.Fatalf("status %+v; want failed", gs)
	}
	// No process carrying this launch's unique argument may be left.
	waitFor(t, 5*time.Second, "no process running "+marker, func() bool {
		return exec.Command("pgrep", "-f", marker).Run() != nil
	})
	var pid any
	e.store.Reader().QueryRow(`SELECT pid FROM processes WHERE studio_id = 'unrecorded'`).Scan(&pid)
	if pid != nil {
		t.Fatalf("row records pid %v although recording it failed", pid)
	}
}

// A launch whose rows cannot be written starts nothing and leaves no queued
// row for a later re-adoption to start.
func TestLaunchThatCannotBeRecordedStartsNothing(t *testing.T) {
	e := newEnv(t, Config{})
	e.sup.cfg.failWrite = func(what string) error {
		if what == "queued processes" {
			return errors.New("disk full")
		}
		return nil
	}
	e.manifest("unwritten", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
	if _, err := e.sup.Launch(context.Background(), "unwritten", LaunchOptions{}); err == nil {
		t.Fatal("launch succeeded without its rows")
	}
	if rows := e.rows("unwritten"); len(rows) != 0 {
		t.Fatalf("rows written: %v", rows)
	}
	if gs, _ := e.sup.Status(context.Background(), "unwritten"); gs.State != "" {
		t.Fatalf("status %+v; want never launched", gs)
	}
}

// A row claimed for a spawn but never given a pid may stand for a process
// nobody recorded. Re-adoption must not start a second copy beside it.
func TestReadoptDoesNotRestartAnUnrecordedSpawn(t *testing.T) {
	e := newEnv(t, Config{})
	e.manifest("claimed", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
	insertRow(t, e, "claimed", "studio", stateStarting, platform.ProcessIdentity{})
	rep, err := e.sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Adopted) != 0 || len(rep.Gone) != 1 {
		t.Fatalf("report %+v", rep)
	}
	time.Sleep(300 * time.Millisecond)
	var n int
	e.store.Reader().QueryRow(`SELECT count(*) FROM processes WHERE studio_id = 'claimed' AND pid IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatalf("re-adoption started the studio again (%d rows with a pid)", n)
	}
	if r := e.rows("claimed")["studio"]; r.state != stateExited || r.reason != reasonCrashed {
		t.Fatalf("row %+v; want exited/crashed", r)
	}

	// The same holds for a claimed member of a run that has a survivor: the
	// group rebuilt around the survivor must treat the claimed member as
	// gone, not as never started.
	g := newGroup(e.sup, Studio{}, "claimed", "run")
	p := e.sup.procFromRow(g, &planned{}, row{ID: newID(time.Now()), SpecName: "engine", Role: "sidecar", State: stateStarting}, nil)
	if !p.spawned || p.state == stateQueued || !isClosed(p.exited) || !p.noSweep {
		t.Fatalf("claimed member rebuilt as %+v; want gone (spawned, exited, never signalled), not queued", p)
	}
}

// When a recorded leader is gone but its group still has members, re-adoption
// reports them and signals nothing.
func TestReadoptReportsSurvivorsOfAGoneLeader(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	c := exec.Command("/bin/sh", "-c", "sleep 60 & echo $! > "+pidFile)
	if err := platform.StartInGroup(c); err != nil {
		t.Fatal(err)
	}
	leader, err := platform.IdentifyProcess(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	c.Wait() // the shell exits; its backgrounded sleep stays in the group
	t.Cleanup(func() { platform.KillGroup(leader.PGID) })
	b, _ := os.ReadFile(pidFile)
	child, _ := strconv.Atoi(strings.TrimSpace(string(b)))

	e := newEnv(t, Config{})
	e.manifest("leftovers", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
	insertRow(t, e, "leftovers", "studio", stateRunning, leader)
	rep, err := e.sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Survivors) != 1 || !strings.Contains(rep.Survivors[0], strconv.Itoa(leader.PGID)) {
		t.Fatalf("report %+v; want process group %d named as a survivor", rep, leader.PGID)
	}
	time.Sleep(200 * time.Millisecond)
	if !alive(child) {
		t.Fatalf("the surviving member %d was signalled", child)
	}
}

// A studio re-adopted without its manifest is still listed, still counts as
// heavy, and can still be stopped.
func TestReadoptWithoutManifestIsVisibleHeavyAndStoppable(t *testing.T) {
	o := orphanStudio(t)
	sup := New(Config{Dirs: o.dirs, Store: o.store, Grace: 2 * time.Second, Logf: t.Logf,
		HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	rep, err := sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Adopted) != 1 || len(rep.Unmanaged) != 1 || rep.Unmanaged[0] != "survivor" {
		t.Fatalf("report %+v; want the studio adopted and named unmanaged", rep)
	}
	if got := sup.Unmanaged(); len(got) != 1 || got[0] != "survivor" {
		t.Fatalf("Unmanaged() = %v", got)
	}
	gs, err := sup.Status(context.Background(), "survivor")
	if err != nil || gs.State != stateRunning || gs.Processes[0].PID != o.pid {
		t.Fatalf("status %+v %v", gs, err)
	}

	other := filepath.Join(t.TempDir(), "other.yaml")
	os.WriteFile(other, []byte(`id: other-heavy
name: other heavy
kinds: [video]
local_path: `+t.TempDir()+`
peak_ram_gb: 30
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
processes:
  - name: studio
    heavy: true
    cmd: exec sleep 60
`), 0o600)
	m, res, err := manifest.Load(other)
	if err != nil || !res.OK() {
		t.Fatal(err, res.Errors)
	}
	if err := recordInstall(context.Background(), o.store, o.dirs, m); err != nil {
		t.Fatal(err)
	}
	sup.SetStudios([]Studio{{Manifest: m, File: other}})
	_, err = sup.Launch(context.Background(), "other-heavy", LaunchOptions{})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindHeavyConflict || !strings.Contains(se.Message, "survivor") {
		t.Fatalf("second heavy launch beside an unmanaged studio: err = %v; want a heavy conflict naming it", err)
	}

	if err := sup.Stop("survivor"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sup.Wait(ctx, "survivor"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := platform.GroupExists(o.pgid); ok {
		t.Fatalf("process group %d survived stopping the unmanaged studio", o.pgid)
	}
	if len(sup.Unmanaged()) != 0 {
		t.Fatalf("a stopped unmanaged studio is still listed: %v", sup.Unmanaged())
	}
}

// A crashed worker is recorded failed; main keeps running (decided in M2
// review round 1).
func TestWorkerCrashLeavesMainRunning(t *testing.T) {
	e := newEnv(t, Config{})
	e.manifest("with-worker", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name studio
  - name: batch
    role: worker
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 200 --code 5'
`)
	if _, err := e.sup.Launch(context.Background(), "with-worker", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "the worker to fail", func() bool {
		return e.rows("with-worker")["batch"].state == stateFailed
	})
	time.Sleep(500 * time.Millisecond)
	rows := e.rows("with-worker")
	if r := rows["batch"]; r.reason != reasonCrashed || r.code.Int64 != 5 {
		t.Fatalf("worker row %+v; want failed/crashed exit 5", r)
	}
	if r := rows["studio"]; r.state != stateRunning || !alive(r.pid) {
		t.Fatalf("main row %+v; want it still running after the worker crashed", r)
	}
	if gs, _ := e.sup.Status(context.Background(), "with-worker"); gs.State != stateRunning {
		t.Fatalf("group state %q; a failed worker must not read as a failed group", gs.State)
	}
}

// Once a preemption has begun, the launch completes even if the caller goes
// away — otherwise the user is left with neither studio.
func TestPreemptingLaunchSurvivesTheCallerLeaving(t *testing.T) {
	e := newEnv(t, Config{Grace: time.Second})
	e.manifest("slow-to-stop", "peak_ram_gb: 10", `
  - name: studio
    role: main
    heavy: true
    cmd: `+serve+` --name studio --ignore-term
`)
	e.manifest("replacement", "peak_ram_gb: 10", `
  - name: studio
    role: main
    heavy: true
    cmd: `+serve+` --name replacement
`)
	if _, err := e.sup.Launch(context.Background(), "slow-to-stop", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("slow-to-stop", stateRunning, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := e.sup.Launch(ctx, "replacement", LaunchOptions{Preempt: true}); err != nil {
		t.Fatalf("preempting launch returned %v after its caller left", err)
	}
	e.waitState("replacement", stateRunning, 10*time.Second)
}

// A working directory that is a symlink out of the checkout is refused, and
// so is a relative local_path.
func TestWorkingDirectoryMustResolveInsideTheCheckout(t *testing.T) {
	e := newEnv(t, Config{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.root, "escape")); err != nil {
		t.Fatal(err)
	}
	e.manifest("escaper", "", `
  - name: studio
    role: main
    cwd: escape
    cmd: exec sleep 60
`)
	_, err := e.sup.Launch(context.Background(), "escaper", LaunchOptions{})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, "outside the checkout") {
		t.Fatalf("err = %v; want a refusal naming the escape", err)
	}

	st, _ := e.sup.Studio("escaper")
	rel := *st.Manifest
	rel.LocalPath = "relative/checkout"
	if _, err := resolvePlan(e.dirs, Studio{Manifest: &rel}, launchPaths{root: e.root}, nil, e.sup.alloc); !errors.As(err, &se) || !strings.Contains(se.Message, "not an absolute path") {
		t.Fatalf("relative local_path: err = %v; want a refusal", err)
	}
}

// Review round 2: a worker whose restart never becomes ready — its health
// probe times out — is recorded failed, and main keeps running.
func TestRestartedWorkerThatNeverReadiesLeavesMainRunning(t *testing.T) {
	e := newEnv(t, Config{RestartBackoff: []time.Duration{10 * time.Millisecond}})
	flag := filepath.Join(t.TempDir(), "ready")
	os.WriteFile(flag, nil, 0o600)
	e.manifest("worker-restart", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name studio
  - name: batch
    role: worker
    restart: on-failure
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 1500 --code 3'
    health: { exec: 'test -f `+flag+`', timeout_s: 1, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "worker-restart", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("worker-restart", stateRunning, 15*time.Second)
	waitFor(t, 10*time.Second, "the worker to be running", func() bool {
		return e.rows("worker-restart")["batch"].state == stateRunning
	})
	os.Remove(flag) // its restart can never pass health again
	waitFor(t, 15*time.Second, "the restarted worker to fail", func() bool {
		return e.rows("worker-restart")["batch"].state == stateFailed
	})
	time.Sleep(time.Second)
	rows := e.rows("worker-restart")
	if r := rows["studio"]; r.state != stateRunning || !alive(r.pid) {
		t.Fatalf("main row %+v; want it still running after the worker's restart failed", r)
	}
	b, _ := os.ReadFile(e.logPath("worker-restart", "batch"))
	if n := strings.Count(string(b), "[helmstudio "); n != 2 {
		t.Fatalf("worker started %d times, want 2 (the start and one restart)", n)
	}
	if r := rows["batch"]; r.pid != 0 && groupExists(t, r.pgid) {
		t.Fatalf("the failed worker's group %d is still alive", r.pgid)
	}
	if gs, _ := e.sup.Status(context.Background(), "worker-restart"); gs.State != stateRunning {
		t.Fatalf("group state %q, want running", gs.State)
	}
}

// A worker another member depends on is needed the way a sidecar is: its
// failure fails the group.
func TestWorkerThatIsDependedOnFailsTheGroup(t *testing.T) {
	e := newEnv(t, Config{})
	e.manifest("needed-worker", "", `
  - name: studio
    role: main
    depends_on: [prep]
    cmd: `+serve+` --name studio
  - name: prep
    role: worker
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 100 --code 4'
    health: { exec: 'false', timeout_s: 5, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "needed-worker", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("needed-worker")
	rows := e.rows("needed-worker")
	if rows["prep"].state != stateFailed || rows["studio"].pid != 0 {
		t.Fatalf("rows %+v; want the worker failed and main never started", rows)
	}
}

// Review round 2: a claimed-but-unrecorded sidecar with restart: on-failure is
// not restarted at re-adoption, even when another member of its run survives.
func TestReadoptDoesNotRestartAClaimedSidecar(t *testing.T) {
	survivor := exec.Command(os.Args[0], "helper", "sleep")
	if err := platform.StartInGroup(survivor); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { survivor.Wait(); close(waited) }()
	t.Cleanup(func() { platform.KillGroup(survivor.Process.Pid); <-waited })
	var real platform.ProcessIdentity
	waitFor(t, 5*time.Second, "identity", func() bool {
		var err error
		real, err = platform.IdentifyProcess(survivor.Process.Pid)
		return err == nil
	})

	e := newEnv(t, Config{RestartBackoff: []time.Duration{10 * time.Millisecond}})
	e.manifest("cprobe", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name studio
  - name: engine
    role: sidecar
    restart: on-failure
    cmd: `+serve+` --name engine
`)
	run := newID(time.Now())
	insertRunRow(t, e, "cprobe", run, "studio", "main", stateRunning, real)
	insertRunRow(t, e, "cprobe", run, "engine", "sidecar", stateStarting, platform.ProcessIdentity{})

	rep, err := e.sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Adopted) != 1 {
		t.Fatalf("report %+v; want main adopted", rep)
	}
	time.Sleep(1500 * time.Millisecond)
	var withPID int
	e.store.Reader().QueryRow(`SELECT count(*) FROM processes WHERE studio_id = 'cprobe' AND spec_name = 'engine' AND pid IS NOT NULL`).Scan(&withPID)
	if withPID != 0 {
		t.Fatal("the claimed sidecar was started again at re-adoption")
	}
	if logs, _ := filepath.Glob(filepath.Join(e.dirs.Logs(), "studios", "cprobe", "*engine.log")); len(logs) != 0 {
		t.Fatalf("the claimed sidecar got a new log: %v", logs)
	}
	if r := e.rows("cprobe")["engine"]; r.state != stateFailed {
		t.Fatalf("engine row %+v; want failed (a gone sidecar that may not be restarted fails the group)", r)
	}
}

// Review round 2: a recorded pid that now belongs to another process means
// the old group had ended, so no survivors are reported for it.
func TestReadoptReportsNoSurvivorsForARecycledPid(t *testing.T) {
	other := exec.Command(os.Args[0], "helper", "sleep")
	if err := platform.StartInGroup(other); err != nil { // its own group, id == pid
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { other.Wait(); close(waited) }()
	t.Cleanup(func() { platform.KillGroup(other.Process.Pid); <-waited })
	var real platform.ProcessIdentity
	waitFor(t, 5*time.Second, "identity", func() bool {
		var err error
		real, err = platform.IdentifyProcess(other.Process.Pid)
		return err == nil
	})

	e := newEnv(t, Config{})
	e.manifest("recycled", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
	// Same pid and pgid as the live process, older start time: the pid was reused.
	insertRow(t, e, "recycled", "studio", stateRunning, platform.ProcessIdentity{PID: real.PID, StartTime: real.StartTime - 1, PGID: real.PGID})
	rep, err := e.sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Gone) != 1 || len(rep.Survivors) != 0 {
		t.Fatalf("report %+v; want the row gone and no survivors (the group belongs to the new process)", rep)
	}
	if !alive(real.PID) {
		t.Fatal("the process that took the pid was signalled")
	}
}

// A state a user can see has already been recorded, so a daemon killed right
// after showing "running" re-adopts a running process, not a starting one.
// Writes are slowed so the gap between showing and recording would be wide.
func TestStateIsRecordedBeforeItIsShown(t *testing.T) {
	e := newEnv(t, Config{})
	e.sup.cfg.failWrite = func(what string) error {
		if what == "process state" {
			time.Sleep(300 * time.Millisecond)
		}
		return nil
	}
	e.manifest("ordered", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name studio
`)
	if _, err := e.sup.Launch(context.Background(), "ordered", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("ordered", stateRunning, 15*time.Second)
	if r := e.rows("ordered")["studio"]; r.state != stateRunning {
		t.Fatalf("Status shows running while the row says %q", r.state)
	}
}
