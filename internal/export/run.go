package export

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// DefaultGrace is how long a stopped render is given to finish writing before
// it is killed. ffmpeg closes its output on SIGTERM, so this is generous.
const DefaultGrace = 10 * time.Second

// Run is one ffmpeg invocation the daemon supervises: its own process group,
// its identity recorded before it can leave anything behind, its progress read
// from its own reporting, and a stop that reaches it whether it is listening
// or not.
type Run struct {
	Tool *Tool
	Args []string
	// Log receives everything ffmpeg says, which becomes the export's log.
	Log io.Writer
	// Started is called once the process has an identity and before it has
	// written anything worth finding. Returning an error kills the group at
	// once: a render nobody recorded is a render the next daemon cannot stop
	// (M3 review #1, applied to exports).
	Started func(platform.ProcessIdentity) error
	// Progress is called with the frames written so far.
	Progress func(frames int64)
	Grace    time.Duration
}

// ErrCancelled means the run was stopped on purpose.
var ErrCancelled = errors.New("the export was stopped")

// Do runs ffmpeg to completion, or stops it when ctx ends.
func (r Run) Do(ctx context.Context) error {
	if err := CheckArgs(r.Args); err != nil {
		return err
	}
	grace := r.Grace
	if grace <= 0 {
		grace = DefaultGrace
	}
	cmd := exec.Command(r.Tool.FFmpeg, r.Args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = r.Log
	if err := platform.StartInGroup(cmd); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}
	pid := cmd.Process.Pid
	identity, idErr := platform.IdentifyProcess(pid)
	if idErr != nil {
		identity = platform.ProcessIdentity{PID: pid, PGID: pid}
	}
	if r.Started != nil {
		if err := r.Started(identity); err != nil {
			stopGroup(pid, grace)
			_ = cmd.Wait()
			return fmt.Errorf("recording the export's process: %w", err)
		}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			stopGroup(pid, grace)
		case <-done:
		}
	}()

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || key != "frame" || r.Progress == nil {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			r.Progress(n)
		}
	}
	waitErr := cmd.Wait()
	close(done)
	<-stopped

	switch {
	case ctx.Err() != nil:
		return ErrCancelled
	case waitErr != nil:
		return fmt.Errorf("ffmpeg exited %d", ExitCodeOf(waitErr))
	}
	return nil
}

// stopGroup asks the render's whole process group to stop, then insists.
func stopGroup(pgid int, grace time.Duration) {
	if err := platform.TerminateGroup(pgid); err != nil {
		return
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if alive, err := platform.GroupExists(pgid); err != nil || !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = platform.KillGroup(pgid)
}

// StopSurvivor stops a render a killed daemon left running, and only when it
// is provably that render: the group is its recorded leader's pid, and either
// the leader still matches its recorded identity or no process holds that pid
// at all — while any member of a group lives, the kernel cannot reuse its id.
// A pid that now belongs to something else is never signalled (M2's rule).
func StopSurvivor(id platform.ProcessIdentity, grace time.Duration, logf func(string, ...any)) bool {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if id.PID <= 1 || id.PGID != id.PID || id.StartTime == 0 {
		logf("export: a render left no verifiable identity (pid %d, pgid %d); it was not signalled", id.PID, id.PGID)
		return false
	}
	cur, err := platform.IdentifyProcess(id.PID)
	switch {
	case err == nil:
		if !cur.Matches(id) {
			logf("export: pid %d now belongs to another process; it was not signalled", id.PID)
			return false
		}
	case errors.Is(err, platform.ErrNoProcess):
		if alive, gerr := platform.GroupExists(id.PGID); gerr != nil || !alive {
			return false
		}
	default:
		logf("export: reading pid %d: %v; it was not signalled", id.PID, err)
		return false
	}
	logf("export: stopping the render in process group %d, left running by a helmstudio that was killed", id.PGID)
	stopGroup(id.PGID, grace)
	return true
}
