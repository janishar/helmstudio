package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// A process that the kernel still has under the recorded pid is adopted only
// when start time and process group match too. Each case changes exactly one
// recorded field, so a re-adoption that skips any one of the three checks
// adopts — and later signals — a process that is not its own.
func TestReadoptRequiresPidStartTimeAndPgid(t *testing.T) {
	stranger := exec.Command(os.Args[0], "helper", "sleep") // not in a group of ours
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stranger.Process.Kill(); stranger.Wait() })
	var real platform.ProcessIdentity
	waitFor(t, 5*time.Second, "the stranger to be identifiable", func() bool {
		var err error
		real, err = platform.IdentifyProcess(stranger.Process.Pid)
		return err == nil
	})

	cases := map[string]platform.ProcessIdentity{
		"start time differs": {PID: real.PID, StartTime: real.StartTime + 1, PGID: real.PGID},
		"pgid differs":       {PID: real.PID, StartTime: real.StartTime, PGID: real.PGID + 1},
		"start time unknown": {PID: real.PID, StartTime: 0, PGID: real.PGID},
		"pid gone":           {PID: deadPID(t), StartTime: real.StartTime, PGID: real.PGID},
	}
	for name, recorded := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, Config{})
			e.manifest("ghost", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
			insertRow(t, e, "ghost", "studio", stateRunning, recorded)

			rep, err := e.sup.Readopt(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Adopted) != 0 || len(rep.Gone) != 1 {
				t.Fatalf("report %+v; want the row recorded as gone, nothing adopted", rep)
			}
			r := e.rows("ghost")["studio"]
			if r.state != stateExited || r.reason != reasonCrashed {
				t.Fatalf("row %+v; want exited/crashed", r)
			}
			if name != "pid gone" {
				time.Sleep(100 * time.Millisecond)
				if cur, err := platform.IdentifyProcess(real.PID); err != nil || !cur.Matches(real) {
					t.Fatalf("the unrelated process %d was disturbed: %+v %v", real.PID, cur, err)
				}
			}
			if gs, _ := e.sup.Status(context.Background(), "ghost"); gs.State == stateRunning {
				t.Fatalf("status %+v; a mismatched row must not read as running", gs)
			}
		})
	}

	// Control: the identical row with the true identity is adopted, so the
	// cases above fail for the field they change and not for some other reason.
	t.Run("all three match", func(t *testing.T) {
		e := newEnv(t, Config{})
		e.manifest("ghost", "", `
  - name: studio
    role: main
    cmd: `+serve+`
`)
		insertRow(t, e, "ghost", "studio", stateRunning, real)
		rep, err := e.sup.Readopt(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Adopted) != 1 {
			t.Fatalf("report %+v; want the matching process adopted", rep)
		}
		if gs, _ := e.sup.Status(context.Background(), "ghost"); gs.State != stateRunning || gs.Processes[0].PID != real.PID {
			t.Fatalf("status %+v", gs)
		}
		// Hand it back without killing it: the stranger is the test's.
		e.sup.mu.Lock()
		e.sup.groups["ghost"].procs[0].ident.StartTime++ // make a stop refuse to signal it
		e.sup.mu.Unlock()
	})
}

func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("/bin/sh", "-c", "exit 0")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

func insertRow(t *testing.T, e *env, studio, name, state string, id platform.ProcessIdentity) {
	t.Helper()
	insertRunRow(t, e, studio, newID(time.Now()), name, "main", state, id)
}

func insertRunRow(t *testing.T, e *env, studio, run, name, role, state string, id platform.ProcessIdentity) {
	t.Helper()
	cmd, _ := json.Marshal([]string{"/bin/sh", "-c", "true"})
	now := time.Now()
	var pid, start, pgid any
	if id.PID != 0 {
		pid, pgid = id.PID, id.PGID
	}
	if id.StartTime != 0 {
		start = id.StartTime
	}
	err := e.store.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO processes (id, studio_id, group_run_id, spec_name, role, pid, pid_start_time, pgid, command, state, health_state, started_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unknown', ?)`,
			newID(now), studio, run, name, role, pid, start, pgid, string(cmd), state, now.UnixMilli())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every signal after spawn is guarded by identity: a group whose leader's pid
// now belongs to a process with a different start time is not signalled,
// even though its group id is valid and its members are alive.
func TestSignalGroupRefusesARecycledLeader(t *testing.T) {
	c := exec.Command(os.Args[0], "helper", "sleep")
	if err := platform.StartInGroup(c); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { c.Wait(); close(waited) }() // reap it, or its zombie keeps the group alive
	t.Cleanup(func() { platform.KillGroup(c.Process.Pid); <-waited })
	var real platform.ProcessIdentity
	waitFor(t, 5*time.Second, "identity", func() bool {
		var err error
		real, err = platform.IdentifyProcess(c.Process.Pid)
		return err == nil
	})

	for name, id := range map[string]platform.ProcessIdentity{
		"start time differs": {PID: real.PID, StartTime: real.StartTime + 1, PGID: real.PGID},
		"start time unknown": {PID: real.PID, PGID: real.PGID},
	} {
		if err := signalGroup(id, true); err == nil {
			t.Fatalf("%s: signalGroup succeeded", name)
		}
		time.Sleep(50 * time.Millisecond)
		if cur, err := platform.IdentifyProcess(real.PID); err != nil || !cur.Matches(real) {
			t.Fatalf("%s: the process was killed: %v", name, err)
		}
	}
	if err := signalGroup(real, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "the matching group to die", func() bool {
		ok, _ := platform.GroupExists(real.PGID)
		return !ok
	})
}
