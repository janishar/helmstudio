package supervisor

import (
	"bufio"
	"context"
	"errors"
	"net"
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
)

const serve = `exec "$SUPERVISOR_TEST_HELPER" helper serve`

// R20, R26, and the DoD's "teardown leaves no orphan — assert by pgid".
// Every member leads its own process group; start follows depends_on; stop
// sends SIGTERM in reverse order, to groups, and leaves every group empty,
// including a grandchild the studio forked without telling anyone.
func TestLaunchOrdersByDependencyAndTeardownEmptiesEveryGroup(t *testing.T) {
	e := newEnv(t, Config{})
	termFile := filepath.Join(t.TempDir(), "terms")
	e.manifest("two-proc", "", `
  - name: studio
    role: main
    depends_on: [engine]
    cmd: `+serve+` --name studio --port {port} --grandchild --term-file `+termFile+`
    port: { prefer: 30100 }
    health: { path: /healthz, timeout_s: 20, interval_s: 1 }
  - name: engine
    role: sidecar
    cmd: `+serve+` --name engine --port {port} --grandchild --term-file `+termFile+`
    port: {}
    health: { tcp: true, timeout_s: 20, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "two-proc", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("two-proc", stateRunning, 20*time.Second)

	rows := e.rows("two-proc")
	engine, studio := rows["engine"], rows["studio"]
	for _, r := range []dbRow{engine, studio} {
		if r.pid == 0 || r.pgid != r.pid || r.pgid == syscall.Getpgrp() || !r.start.Valid {
			t.Fatalf("%s: pid %d pgid %d start %v; want its own process group and a recorded start time", r.name, r.pid, r.pgid, r.start)
		}
	}
	if studio.startedAt.Int64 < engine.startedAt.Int64 {
		t.Fatalf("studio started at %d before its dependency engine at %d", studio.startedAt.Int64, engine.startedAt.Int64)
	}
	gcs := append(grandchildPIDs(t, e.logPath("two-proc", "engine")), grandchildPIDs(t, e.logPath("two-proc", "studio"))...)
	if len(gcs) != 2 {
		t.Fatalf("grandchildren = %v, want one per process", gcs)
	}
	for _, pid := range gcs {
		id, err := platform.IdentifyProcess(pid)
		if err != nil || (id.PGID != engine.pgid && id.PGID != studio.pgid) {
			t.Fatalf("grandchild %d: %+v %v; want it inside a studio process group", pid, id, err)
		}
	}

	if err := e.sup.Stop("two-proc"); err != nil {
		t.Fatal(err)
	}
	e.waitDone("two-proc")

	for _, r := range []dbRow{engine, studio} {
		if groupExists(t, r.pgid) {
			t.Errorf("process group %d (%s) still has members after teardown", r.pgid, r.name)
		}
	}
	for _, pid := range gcs {
		if alive(pid) {
			t.Errorf("grandchild %d survived teardown", pid)
		}
	}
	after := e.rows("two-proc")
	for name, r := range after {
		if r.state != stateExited || r.reason != reasonKilledByUser {
			t.Errorf("%s: state %s reason %q, want exited/killed_by_user", name, r.state, r.reason)
		}
	}

	b, err := os.ReadFile(termFile)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	grandchildTerms := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		who := strings.Fields(line)[0]
		if strings.HasSuffix(who, "-grandchild") {
			grandchildTerms++
			continue
		}
		order = append(order, who)
	}
	if strings.Join(order, ",") != "studio,engine" {
		t.Fatalf("SIGTERM order = %v, want studio then engine (reverse dependency order)", order)
	}
	// SIGTERM went to the group, not only the leader: the grandchildren got
	// it too, rather than being left for the SIGKILL fallback.
	if grandchildTerms != 2 {
		t.Fatalf("%d grandchildren received SIGTERM, want 2: it was sent to the process, not its group\n%s", grandchildTerms, b)
	}
}

// R27 and the DoD: a failing health check on a running main process does not
// restart it — not even when its manifest asks for restart: on-failure.
func TestFailingHealthNeverRestartsMain(t *testing.T) {
	e := newEnv(t, Config{RestartBackoff: []time.Duration{10 * time.Millisecond}})
	e.manifest("slow-probe", "", `
  - name: studio
    role: main
    restart: on-failure
    cmd: `+serve+` --name studio --port {port} --health-file {data}/health
    port: {}
    health: { path: /healthz, timeout_s: 20, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "slow-probe", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	gs := e.waitState("slow-probe", stateRunning, 20*time.Second)
	pid := gs.Processes[0].PID

	st, _ := e.sup.Studio("slow-probe")
	if err := os.WriteFile(filepath.Join(studioData(e.dirs, st.Manifest), "health"), []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "health_state failing", func() bool {
		gs, _ := e.sup.Status(context.Background(), "slow-probe")
		return gs.Processes[0].HealthState == healthFailing
	})
	// Several more failed probes, well past any timeout_s a restart policy
	// could be keyed on relative to the last success.
	time.Sleep(3500 * time.Millisecond)

	gs, _ = e.sup.Status(context.Background(), "slow-probe")
	p := gs.Processes[0]
	if p.PID != pid || p.State != stateRunning || p.HealthState != healthFailing {
		t.Fatalf("after failing probes: pid %d (was %d) state %s health %s; want the same process, running, health failing", p.PID, pid, p.State, p.HealthState)
	}
	if !alive(pid) {
		t.Fatalf("pid %d is gone", pid)
	}
	if r := e.rows("slow-probe")["studio"]; r.pid != pid || r.state != stateRunning || r.health != healthFailing {
		t.Fatalf("row: %+v; want pid %d running, health failing", r, pid)
	}
	b, _ := os.ReadFile(e.logPath("slow-probe", "studio"))
	if n := strings.Count(string(b), "[helmstudio "); n != 1 {
		t.Fatalf("the log shows %d starts, want 1", n)
	}
}

// R27: a main process that exits unexpectedly fails the group, keeps its exit
// status, and is not restarted even with restart: on-failure.
func TestCrashedMainFailsGroupAndIsNotRestarted(t *testing.T) {
	e := newEnv(t, Config{RestartBackoff: []time.Duration{10 * time.Millisecond}})
	e.manifest("crashy", "", `
  - name: studio
    role: main
    restart: on-failure
    cmd: `+serve+` --name studio
`)
	if _, err := e.sup.Launch(context.Background(), "crashy", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	gs := e.waitState("crashy", stateRunning, 10*time.Second)
	pid := gs.Processes[0].PID
	waitFor(t, 5*time.Second, "output", func() bool {
		b, _ := os.ReadFile(e.logPath("crashy", "studio"))
		return strings.Contains(string(b), "tick 2")
	})
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	e.waitDone("crashy")
	time.Sleep(200 * time.Millisecond) // many backoffs' worth

	gs, _ = e.sup.Status(context.Background(), "crashy")
	p := gs.Processes[0]
	if gs.State != stateFailed || p.State != stateFailed || p.ExitReason != reasonCrashed || p.ExitCode == nil || *p.ExitCode != 137 || p.PID != pid {
		t.Fatalf("status %+v, process %+v; want failed/crashed exit 137 with the original pid", gs, p)
	}
	if gs.Failure == nil || len(gs.Failure.LastLines) == 0 || !strings.Contains(strings.Join(gs.Failure.LastLines, "\n"), "tick") {
		t.Fatalf("failure = %+v; want the last lines of output surfaced", gs.Failure)
	}
	b, _ := os.ReadFile(e.logPath("crashy", "studio"))
	if n := strings.Count(string(b), "[helmstudio "); n != 1 {
		t.Fatalf("the log shows %d starts, want 1: main was restarted", n)
	}
}

// R27: a sidecar with restart: on-failure is restarted with backoff, at most
// MaxRestarts times in the window, then the group fails.
func TestSidecarRestartBudget(t *testing.T) {
	e := newEnv(t, Config{RestartBackoff: []time.Duration{10 * time.Millisecond}, MaxRestarts: 3})
	e.manifest("flaky", "", `
  - name: studio
    role: main
    depends_on: [engine]
    cmd: `+serve+` --name studio
  - name: engine
    role: sidecar
    restart: on-failure
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 150 --code 3'
`)
	if _, err := e.sup.Launch(context.Background(), "flaky", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("flaky")
	b, _ := os.ReadFile(e.logPath("flaky", "engine"))
	if n := strings.Count(string(b), "[helmstudio "); n != 4 {
		t.Fatalf("engine started %d times, want 4 (one start and three restarts)", n)
	}
	rows := e.rows("flaky")
	if r := rows["engine"]; r.state != stateFailed || r.reason != reasonCrashed || r.code.Int64 != 3 {
		t.Fatalf("engine row %+v; want failed/crashed with exit 3", r)
	}
	if r := rows["studio"]; r.state != stateExited || groupExists(t, r.pgid) {
		t.Fatalf("studio row %+v; want exited with its group gone", r)
	}
}

// R23, 02 §7: a process whose health never passes within its budget fails
// with health_timeout, and the group is torn down.
func TestHealthTimeoutFailsTheGroup(t *testing.T) {
	e := newEnv(t, Config{})
	e.manifest("never-ready", "", `
  - name: studio
    role: main
    cmd: `+serve+` --name studio
    port: {}
    health: { tcp: true, timeout_s: 1, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "never-ready", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("never-ready")
	r := e.rows("never-ready")["studio"]
	if r.state != stateFailed || r.reason != reasonHealthTimeout {
		t.Fatalf("row %+v; want failed/health_timeout", r)
	}
	if groupExists(t, r.pgid) {
		t.Fatalf("group %d survived a health timeout", r.pgid)
	}
}

// R22 and the DoD: a fixed port already in use is an error naming its holder.
func TestFixedPortConflictNamesTheHolder(t *testing.T) {
	e := newEnv(t, Config{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	e.manifest("fixed", "", `
  - name: studio
    role: main
    cmd: `+serve+` --port {port}
    port: { fixed: `+strconv.Itoa(port)+` }
`)
	_, err = e.sup.Launch(context.Background(), "fixed", LaunchOptions{})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindPortConflict {
		t.Fatalf("err = %v; want a port conflict", err)
	}
	if !strings.Contains(se.Message, strconv.Itoa(port)) || !strings.Contains(se.Message, "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("message %q does not name port %d and its holder, pid %d", se.Message, port, os.Getpid())
	}
	if rows := e.rows("fixed"); len(rows) != 0 {
		t.Fatalf("a refused launch wrote rows: %v", rows)
	}
}

// R25 and the DoD: a second heavy studio is refused with the memory
// arithmetic; confirming preempts the first.
func TestSecondHeavyStudioIsRefusedWithArithmetic(t *testing.T) {
	e := newEnv(t, Config{HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	proc := `
  - name: studio
    role: main
    heavy: true
    cmd: ` + serve + ` --port {port}
    port: {}
    health: { tcp: true, timeout_s: 20, interval_s: 1 }
`
	e.manifest("first-heavy", "peak_ram_gb: 21", proc)
	e.manifest("second-heavy", "peak_ram_gb: 30", proc)
	if _, err := e.sup.Launch(context.Background(), "first-heavy", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	first := e.waitState("first-heavy", stateRunning, 20*time.Second)

	_, err := e.sup.Launch(context.Background(), "second-heavy", LaunchOptions{})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindHeavyConflict || se.Heavy == nil {
		t.Fatalf("err = %v; want a heavy conflict", err)
	}
	for _, want := range []string{"first heavy", "21 GB", "30 GB", "51 GB", "64 GB"} {
		if !strings.Contains(se.Message, want) {
			t.Errorf("message %q lacks %q", se.Message, want)
		}
	}
	if gs, _ := e.sup.Status(context.Background(), "first-heavy"); gs.State != stateRunning || gs.Processes[0].PID != first.Processes[0].PID {
		t.Fatalf("a refused launch disturbed the running studio: %+v", gs)
	}

	// A heavy studio that could not launch anyway is refused for that reason,
	// even with preempt, and the running studio is left alone.
	e.manifest("broken-heavy", "", `
  - name: studio
    role: main
    heavy: true
    cmd: run {venv}/bin/python
`)
	_, err = e.sup.Launch(context.Background(), "broken-heavy", LaunchOptions{Preempt: true})
	if !errors.As(err, &se) || se.Kind != KindNotLaunchable {
		t.Fatalf("preempting launch of an unlaunchable studio: err = %v; want not launchable", err)
	}
	if gs, _ := e.sup.Status(context.Background(), "first-heavy"); gs.State != stateRunning || gs.Processes[0].PID != first.Processes[0].PID {
		t.Fatalf("preempt stopped the running studio for a launch that could not happen: %+v", gs)
	}

	if _, err := e.sup.Launch(context.Background(), "second-heavy", LaunchOptions{Preempt: true}); err != nil {
		t.Fatal(err)
	}
	e.waitState("second-heavy", stateRunning, 20*time.Second)
	r := e.rows("first-heavy")["studio"]
	if r.state != stateExited || r.reason != reasonPreempted || groupExists(t, r.pgid) {
		t.Fatalf("first studio row %+v; want exited/preempted with its group gone", r)
	}
}

// orphaned is a studio left running by a daemon that was killed with SIGKILL.
type orphaned struct {
	dirs            *platform.Dirs
	store           *store.Store
	manifest        string
	pid, pgid, port int
	logPath         string
}

// orphanStudio runs a real daemon subprocess, launches a heavy fake studio
// through it, and kills the daemon with SIGKILL once the studio is running.
// The studio's process group is killed when the test ends, whatever happens.
func orphanStudio(t *testing.T) orphaned {
	t.Helper()
	home := t.TempDir()
	dirs, err := platform.Under(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.Ensure(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "checkout")
	os.MkdirAll(root, 0o755)
	mf := filepath.Join(t.TempDir(), "survivor.yaml")
	os.WriteFile(mf, []byte(withHelper(`id: survivor
name: survivor
kinds: [video]
local_path: `+root+`
peak_ram_gb: 21
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
processes:
  - name: studio
    role: main
    heavy: true
    cmd: `+serve+` --name studio --port {port} --grandchild
    port: {}
    health: { path: /healthz, timeout_s: 20, interval_s: 1 }
`)), 0o600)

	daemon := exec.Command(os.Args[0], "helper", "daemon", "--home", home, "--manifest", mf)
	out, _ := daemon.StdoutPipe()
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(out)
	ready := false
	for sc.Scan() {
		if sc.Text() == "running" {
			ready = true
			break
		}
		t.Log("daemon:", sc.Text())
	}
	if !ready {
		daemon.Process.Kill()
		daemon.Wait()
		t.Fatal("the first daemon never reported the studio running")
	}
	if err := daemon.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	daemon.Wait()

	st, err := store.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	o := orphaned{dirs: dirs, store: st, manifest: mf}
	var logRel string
	if err := st.Reader().QueryRow(`SELECT p.pid, p.pgid, p.port, l.path FROM processes p
		JOIN log_files l ON l.owner_id = p.id WHERE p.spec_name = 'studio'`).Scan(&o.pid, &o.pgid, &o.port, &logRel); err != nil {
		t.Fatal(err)
	}
	o.logPath = filepath.Join(dirs.Logs(), logRel)
	t.Cleanup(func() {
		if o.pgid > 1 {
			platform.KillGroup(o.pgid)
		}
		st.Close()
	})
	return o
}

// R28 and the machine-bound demo, with a fake studio: kill -9 the daemon,
// start another, and the survivor is re-adopted — same pid, still running,
// its logs still flowing — and can then be stopped with nothing left behind.
func TestReadoptAfterDaemonSIGKILL(t *testing.T) {
	o := orphanStudio(t)
	dirs, st, mf, pid, pgid, port, logPath := o.dirs, o.store, o.manifest, o.pid, o.pgid, o.port, o.logPath
	if !alive(pid) {
		t.Fatalf("studio pid %d died with the daemon", pid)
	}
	sizeAtKill := fileSize(t, logPath)
	waitFor(t, 5*time.Second, "the orphaned studio to keep logging", func() bool { return fileSize(t, logPath) > sizeAtKill })

	m, res, err := manifest.Load(mf)
	if err != nil || !res.OK() {
		t.Fatal(err, res.Errors)
	}
	sup := New(Config{Dirs: dirs, Store: st, Grace: 2 * time.Second, Logf: t.Logf})
	sup.SetStudios([]Studio{{Manifest: m, File: mf}})
	rep, err := sup.Readopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Adopted) != 1 || len(rep.Gone) != 0 {
		t.Fatalf("readopt report %+v; want the one studio adopted", rep)
	}
	gs, err := sup.Status(context.Background(), "survivor")
	if err != nil {
		t.Fatal(err)
	}
	if gs.State != stateRunning || gs.Processes[0].PID != pid || gs.Processes[0].Port != port {
		t.Fatalf("status %+v; want running with pid %d on port %d", gs, pid, port)
	}

	sub, err := sup.SubscribeLogs(context.Background(), "survivor", "studio", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.History) == 0 {
		t.Fatal("no log history after re-adoption")
	}
	select {
	case line := <-sub.Lines:
		if !strings.HasPrefix(line.Text, "tick") {
			t.Fatalf("live line %+v", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live log line from the re-adopted process")
	}
	sub.Close()

	// A re-adopted heavy group still counts for the one-heavy rule.
	if sup.liveHeavyLocked("") == nil {
		t.Fatal("the re-adopted heavy group is not seen as running")
	}

	gcs := grandchildPIDs(t, logPath)
	if err := sup.Stop("survivor"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sup.Wait(ctx, "survivor"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := platform.GroupExists(pgid); ok {
		t.Fatalf("process group %d survived stopping the re-adopted studio", pgid)
	}
	for _, gc := range gcs {
		if alive(gc) {
			t.Fatalf("grandchild %d survived", gc)
		}
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
