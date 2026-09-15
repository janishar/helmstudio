package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// detachedPID waits for a serve helper to report the child it detached.
func detachedPID(t *testing.T, e *env, id string) int {
	t.Helper()
	var pids []int
	waitFor(t, 10*time.Second, "the detached child to be reported", func() bool {
		pids = reportedPIDs(t, e.logPath(id, "studio"), "detached pid ")
		return len(pids) == 1 && alive(pids[0])
	})
	// Outside every group the test's teardown signals: if the code under test
	// fails to stop it, the test must not leave it running.
	detached, _ := platform.IdentifyProcess(pids[0])
	t.Cleanup(func() {
		if stillRunning(detached) {
			_ = platform.KillProcess(detached.PID)
		}
	})
	return pids[0]
}

// A studio that starts its work in its own session — ltx studio's renders —
// takes that work out of the process group R26 signals. Stop still reaches
// it: SIGTERM first, then SIGKILL after grace when it ignores SIGTERM
// (docs/decisions.md M5 Q11).
func TestStopReachesAChildThatLeftTheGroup(t *testing.T) {
	e := newEnv(t, Config{Grace: time.Second})
	termFile := filepath.Join(t.TempDir(), "terms")
	e.manifest("detaching", "", `  - name: studio
    cmd: `+serve+` --name studio --port {port} --detach --detached-ignores-term --term-file `+termFile+`
    port: { prefer: 30100 }
    health: { tcp: true, timeout_s: 10, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "detaching", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("detaching", stateRunning, 10*time.Second)
	pid := detachedPID(t, e, "detaching")
	studioPGID := e.rows("detaching")["studio"].pgid
	if id, err := platform.IdentifyProcess(pid); err != nil || id.PGID == studioPGID {
		t.Fatalf("detached child %+v %v; want it outside the studio's group %d", id, err, studioPGID)
	}

	if err := e.sup.Stop("detaching"); err != nil {
		t.Fatal(err)
	}
	e.waitDone("detaching")
	if alive(pid) {
		t.Fatalf("detached child %d survived Stop", pid)
	}
	b, _ := os.ReadFile(termFile)
	if !strings.Contains(string(b), "studio-detached ") {
		t.Fatalf("the detached child was never sent SIGTERM before SIGKILL; term file:\n%s", b)
	}
}

// The walk finds every descendant outside the group, at any depth, and
// nothing else: not the group's own members or their in-group children, not
// an unrelated process, not a cycle.
func TestDetachedDescendantsWalk(t *testing.T) {
	p := func(pid, ppid, pgid int) platform.ProcessEntry {
		return platform.ProcessEntry{ProcessIdentity: platform.ProcessIdentity{PID: pid, StartTime: int64(pid), PGID: pgid}, PPID: ppid}
	}
	table := []platform.ProcessEntry{
		p(1, 0, 1),
		p(100, 1, 100), // the leader
		p(101, 100, 100),
		p(200, 100, 200), // detached by the leader
		p(201, 200, 200), // its child, in the detached group
		p(300, 101, 300), // detached by an in-group child
		p(301, 300, 301), // detached again, one level down
		p(400, 1, 400),   // unrelated
		p(401, 400, 100), // an unrelated parent, though it claims the group
		p(500, 500, 500), // its own parent
	}
	got := map[int]bool{}
	for _, d := range detachedDescendants(table, 100) {
		got[d.PID] = true
	}
	want := map[int]bool{200: true, 201: true, 300: true, 301: true}
	for pid := range want {
		if !got[pid] {
			t.Errorf("descendant %d outside the group was not found", pid)
		}
	}
	for pid := range got {
		if !want[pid] {
			t.Errorf("process %d is not a detached descendant, but was listed", pid)
		}
	}
}

// A detached process whose pid has been taken by a different process, and a
// group whose leader's pid has, are never signalled or walked.
func TestARecycledDetachedPIDIsNeverSignalled(t *testing.T) {
	e := newEnv(t, Config{})
	g := newGroup(e.sup, Studio{}, "recycled", "run")

	// A leader of its own group with a child in its own session: the shape a
	// studio leaves, started by someone else.
	logFile := filepath.Join(t.TempDir(), "out")
	out, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	leader := exec.Command(os.Args[0], "helper", "serve", "--name", "stranger", "--detach")
	leader.Stdout = out
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	waitFor(t, 10*time.Second, "the stranger's detached child", func() bool {
		pids := reportedPIDs(t, logFile, "detached pid ")
		if len(pids) == 1 && alive(pids[0]) {
			child = pids[0]
			return true
		}
		return false
	})
	defer func() {
		_ = platform.KillProcess(child)
		_ = leader.Process.Kill()
		_ = leader.Wait()
	}()
	leaderID, err := platform.IdentifyProcess(leader.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	childID, err := platform.IdentifyProcess(child)
	if err != nil {
		t.Fatal(err)
	}

	// Verified, the walk finds the child: the test can tell the difference.
	if got := g.snapshotDetached(leaderID, nil); len(got) != 1 || !got[0].SameProcess(childID) {
		t.Fatalf("snapshot of a verified group = %+v, want the detached child %+v", got, childID)
	}
	// Recorded with another start time — the same pid, a different process —
	// the group is not walked.
	recycled := leaderID
	recycled.StartTime--
	if got := g.snapshotDetached(recycled, nil); len(got) != 0 {
		t.Fatalf("snapshot of a group whose leader pid was recycled = %+v, want nothing", got)
	}
	// And a detached process recorded with another start time is not signalled.
	stale := childID
	stale.StartTime--
	g.signalDetached([]platform.ProcessIdentity{stale}, true)
	time.Sleep(100 * time.Millisecond)
	if !alive(child) {
		t.Fatal("a process that merely inherited a recorded pid was killed")
	}
}
