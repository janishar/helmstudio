package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// proc is one member of a group run. Fields below mu are guarded by the
// supervisor's mu.
type proc struct {
	g       *group
	pl      *planned // nil for a re-adopted process whose manifest entry no longer resolves
	rowID   string
	name    string
	role    string
	command []string
	port    int

	// guarded by Supervisor.mu
	state      string
	health     string
	exitReason string
	exitCode   *int
	ident      platform.ProcessIdentity
	startedAt  time.Time
	// spawned is true once a process has existed for this row, in this
	// daemon or the one before it.
	spawned bool
	// exited is closed when the current incarnation is gone. It is replaced
	// on every spawn.
	exited      chan struct{}
	exitHandled bool
	// noSweep marks a process found gone at re-adoption: nothing is
	// signalled on its behalf (docs/design/02-data-model.md §7).
	noSweep bool
	// noRestart marks a member whose spawn was claimed but never recorded
	// when the last daemon died: a copy nobody recorded may still be running.
	noRestart bool
	restarts  []time.Time

	log    *logStream
	logRel string
}

// group is one run of a studio's process group.
type group struct {
	sup      *Supervisor
	studio   Studio
	studioID string
	runID    string
	procs    []*proc
	ports    []int
	// monitorOnly is set for a re-adopted group whose manifest no longer
	// describes it: survivors are watched and can be stopped, and nothing is
	// started, restarted or probed.
	monitorOnly bool

	wake       chan struct{}
	stopCh     chan struct{}
	stopOnce   sync.Once
	stopReason string // guarded by Supervisor.mu
	failed     bool   // touched only by the run goroutine
	done       chan struct{}
}

func newGroup(s *Supervisor, st Studio, studioID, runID string) *group {
	return &group{
		sup: s, studio: st, studioID: studioID, runID: runID,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func isClosed(ch chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (g *group) finished() bool { return isClosed(g.done) }

func (g *group) poke() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

func (g *group) requestStop(reason string) {
	g.stopOnce.Do(func() {
		g.sup.mu.Lock()
		g.stopReason = reason
		g.sup.mu.Unlock()
		close(g.stopCh)
	})
}

func (g *group) stopRequested() bool { return isClosed(g.stopCh) }

// stopContext is cancelled when the group is asked to stop, so a probe in
// flight does not delay a teardown.
func (g *group) stopContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-g.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// run brings the group up in dependency order, supervises it, and tears it
// down. It is the only goroutine that changes process state, apart from a
// health monitor changing health_state.
func (g *group) run() {
	defer close(g.done)
	g.bringUp()
	g.teardown()
	if h := g.sup.cfg.Platform; h != nil {
		h.GroupEnded(g.studioID, g.runID)
	}
}

func (g *group) bringUp() {
	s := g.sup
	for _, p := range g.procs {
		if g.stopRequested() {
			return
		}
		s.mu.Lock()
		spawned, exited, state, handled := p.spawned, p.exited, p.state, p.exitHandled
		s.mu.Unlock()
		switch {
		case !spawned:
			if g.monitorOnly || p.pl == nil {
				continue
			}
			if err := g.spawn(p); err != nil {
				s.logf("supervisor: %s: %v", g.studioID, err)
				if !g.memberFailed(p, reasonCrashed) {
					return
				}
				continue
			}
			if !g.gate(p) {
				return
			}
		case isClosed(exited) && !handled:
			// Found gone at re-adoption.
			s.mu.Lock()
			p.exitHandled = true
			s.mu.Unlock()
			if !g.memberExited(p) {
				return
			}
		case g.monitorOnly || p.pl == nil:
			// Watched only: no probe to gate on.
		case state == stateRunning:
			g.startMonitor(p)
		case state == stateStarting:
			if !g.gate(p) {
				return
			}
		}
	}
	g.supervise()
}

// supervise waits for a stop request or a member exiting.
func (g *group) supervise() {
	for {
		select {
		case <-g.stopCh:
			return
		case <-g.wake:
			if !g.checkExits(nil) {
				return
			}
		}
	}
}

// checkExits handles every member that has exited since it was last looked
// at, except the one being gated. It reports whether the group carries on.
func (g *group) checkExits(gating *proc) bool {
	s := g.sup
	anyAlive := false
	for _, q := range g.procs {
		if q == gating {
			anyAlive = true
			continue
		}
		s.mu.Lock()
		gone := q.spawned && isClosed(q.exited) && !q.exitHandled
		if gone {
			q.exitHandled = true
		}
		alive := q.spawned && !isClosed(q.exited)
		s.mu.Unlock()
		if gone {
			if !g.memberExited(q) {
				return false
			}
			s.mu.Lock()
			alive = q.spawned && !isClosed(q.exited)
			s.mu.Unlock()
		}
		anyAlive = anyAlive || alive
	}
	return anyAlive
}

// spawn starts one member in its own process group with its output going
// straight to its log file, and records its identity before anything else.
func (g *group) spawn(p *proc) error {
	s := g.sup
	newLog := p.log == nil
	if newLog {
		p.logRel = filepath.Join("studios", g.studioID, g.runID+"-"+p.name+".log")
		abs := filepath.Join(s.dirs.Logs(), p.logRel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return fmt.Errorf("creating the log directory for %s: %w", p.name, err)
		}
		ls := newLogStream(abs, s.cfg.Log)
		rowID := p.rowID
		ls.onTruncate = func() { s.markLogTruncated(rowID) }
		// SubscribeLogs reads p.log under s.mu, so it is written under it too
		// (found by M4's helm dev test under the race detector).
		s.mu.Lock()
		p.log = ls
		s.mu.Unlock()
		go ls.run(s.cfg.LogTick)
	}
	out, err := p.log.openForProcess()
	if err != nil {
		return err
	}
	defer out.Close()
	fmt.Fprintf(out, "[helmstudio %s: starting %s in %s: %s]\n", s.now().Format(time.RFC3339), p.name, p.pl.cwd, describe(p.pl.argv))
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()

	if err := s.persistClaim(p); err != nil {
		return fmt.Errorf("process %q was not started: %w", p.name, err)
	}
	cmd := exec.Command(p.pl.argv[0], p.pl.argv[1:]...)
	cmd.Dir, cmd.Env = p.pl.cwd, p.pl.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, out, out
	if err := platform.StartInGroup(cmd); err != nil {
		return fmt.Errorf("starting process %q (%s): %w", p.name, describe(p.pl.argv), err)
	}
	pid := cmd.Process.Pid
	ident, err := platform.IdentifyProcess(pid)
	if err != nil {
		// Already gone. The start time cannot be read any more, so this
		// identity can never be matched after a restart, which is right:
		// there is nothing to re-adopt.
		ident = platform.ProcessIdentity{PID: pid, PGID: pid}
	}
	s.mu.Lock()
	p.ident, p.startedAt = ident, s.now()
	s.mu.Unlock()
	if err := s.persistSpawn(p, p.logRel, newLog); err != nil {
		// Nothing records this process, so a daemon restart could never find
		// it. It is killed now, while this daemon still knows its pid.
		_ = platform.KillGroup(pid)
		_ = cmd.Wait()
		waitUntil(time.Now().Add(s.cfg.KillWait), func() bool { return groupGone(ident) })
		return fmt.Errorf("process %q was stopped again because it could not be recorded: %w", p.name, err)
	}

	exited := make(chan struct{})
	s.mu.Lock()
	p.state, p.health, p.exitReason, p.exitCode = stateStarting, healthUnknown, "", nil
	p.spawned, p.exited, p.exitHandled, p.noSweep = true, exited, false, false
	s.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		code := platform.ExitCode(cmd.ProcessState)
		s.mu.Lock()
		p.exitCode = &code
		close(exited)
		s.mu.Unlock()
		g.poke()
	}()
	return nil
}

// watchAdopted polls a re-adopted process, which is not this daemon's child
// and so cannot be waited on, until its identity no longer matches.
func (g *group) watchAdopted(p *proc, ident platform.ProcessIdentity, exited chan struct{}) {
	t := time.NewTicker(g.sup.cfg.ExitPoll)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-g.done:
			return
		}
		cur, err := platform.IdentifyProcess(ident.PID)
		if err == nil && cur.Matches(ident) {
			continue
		}
		if err != nil && !errors.Is(err, platform.ErrNoProcess) {
			continue // could not tell; ask again
		}
		g.sup.mu.Lock()
		close(exited)
		g.sup.mu.Unlock()
		g.poke()
		return
	}
}

// gate waits for a starting member to become ready: its health probe passes,
// or, for a oneshot, it exits zero. It reports whether the group carries on.
func (g *group) gate(p *proc) bool {
	s := g.sup
	if p.role == "oneshot" {
		return g.waitOneshot(p)
	}
	h := p.pl.spec.Health
	if h == nil {
		g.setRunning(p)
		return true
	}
	s.mu.Lock()
	started, exited := p.startedAt, p.exited
	s.mu.Unlock()
	deadline := started.Add(time.Duration(h.EffectiveTimeoutS()) * time.Second)
	interval := time.Duration(h.EffectiveIntervalS()) * time.Second
	ctx, cancel := g.stopContext()
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-g.stopCh:
			return false
		case <-exited:
			return g.memberFailed(p, reasonCrashed) // exited before it was ready
		case <-g.wake:
			if !g.checkExits(p) {
				return false
			}
		case <-timer.C:
			if probe(ctx, h, p.port, p.pl.healthArgv, p.pl.cwd, p.pl.env) {
				g.setRunning(p)
				return true
			}
			if !s.now().Before(deadline) {
				if isClosed(exited) {
					return g.memberFailed(p, reasonCrashed)
				}
				return g.memberFailed(p, reasonHealthTimeout)
			}
			timer.Reset(interval)
		}
	}
}

func (g *group) waitOneshot(p *proc) bool {
	s := g.sup
	s.mu.Lock()
	exited := p.exited
	s.mu.Unlock()
	for {
		select {
		case <-g.stopCh:
			return false
		case <-g.wake:
			if !g.checkExits(p) {
				return false
			}
		case <-exited:
			s.mu.Lock()
			p.exitHandled = true
			code := p.exitCode
			s.mu.Unlock()
			// A re-adopted oneshot's exit code is unknowable; it is taken as
			// success rather than failing a group that may be healthy.
			if code == nil || *code == 0 {
				g.setState(p, stateExited, reasonClean)
				return true
			}
			g.failMember(p, reasonCrashed)
			return false
		}
	}
}

// setState records a state change, then makes it visible. Writing first means
// Status never shows a state that a daemon killed a moment later would lose:
// whatever a user saw is what the next daemon re-adopts.
func (g *group) setState(p *proc, state, reason string) {
	s := g.sup
	s.mu.Lock()
	code := p.exitCode
	s.mu.Unlock()
	var exitedAt time.Time
	if state == stateExited || state == stateFailed {
		exitedAt = s.now()
	}
	s.persistState(p, state, reason, code, exitedAt)
	s.mu.Lock()
	p.state, p.exitReason = state, reason
	s.mu.Unlock()
}

func (g *group) setRunning(p *proc) {
	g.setState(p, stateRunning, "")
	if p.pl != nil && p.pl.spec.Health != nil {
		g.setHealth(p, healthPassing)
		g.startMonitor(p)
	}
	g.weightsUsed()
}

// weightsUsed records that the studio's weights were used: 02 §7's
// `starting` → `running` row, and Models & disk's "Last used". A failure to
// record it is logged and changes nothing about the launch.
func (g *group) weightsUsed() {
	s := g.sup
	if s.weights == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.weights.Used(ctx, g.studioID); err != nil {
		s.logf("supervisor: recording that %s's weights were used: %v", g.studioID, err)
	}
}

func (g *group) setHealth(p *proc, health string) {
	s := g.sup
	s.mu.Lock()
	changed := p.health != health
	s.mu.Unlock()
	if changed {
		s.persistHealth(p, health)
		s.mu.Lock()
		p.health = health
		s.mu.Unlock()
	}
}

// failMember marks a member failed. The group then tears down.
func (g *group) failMember(p *proc, reason string) {
	g.failed = true
	g.setState(p, stateFailed, reason)
}

// memberFailed records a member that could not start or stay up, and reports
// whether the group carries on. A worker's failure does not end the group —
// its main keeps running (docs/decisions.md, "M2 supervision", review round 1)
// — unless another member depends on it, in which case it is needed the way a
// sidecar is. A worker still alive (a health timeout) is stopped first, and its
// exit is marked handled so it is not taken for a new crash and restarted.
func (g *group) memberFailed(p *proc, reason string) bool {
	if p.role != "worker" || g.dependedOn(p.name) {
		g.failMember(p, reason)
		return false
	}
	s := g.sup
	s.mu.Lock()
	spawned, exited, ident := p.spawned, p.exited, p.ident
	s.mu.Unlock()
	if spawned && !isClosed(exited) {
		g.terminate(ident, exited)
	}
	s.mu.Lock()
	p.exitHandled = true
	s.mu.Unlock()
	s.logf("supervisor: %s: worker %s failed (%s); the rest of the group carries on", g.studioID, p.name, reason)
	g.setState(p, stateFailed, reason)
	return true
}

// dependedOn reports whether any member of the group lists name in depends_on.
func (g *group) dependedOn(name string) bool {
	for _, q := range g.procs {
		if q.pl == nil {
			continue
		}
		for _, d := range q.pl.spec.DependsOn {
			if d == name {
				return true
			}
		}
	}
	return false
}

// startMonitor keeps probing a running member's health and records what it
// finds. It never stops, restarts or signals anything: a slow probe during an
// eight-minute generation must not be able to end the generation (R27).
func (g *group) startMonitor(p *proc) {
	if g.monitorOnly || p.pl == nil || p.pl.spec.Health == nil {
		return
	}
	s := g.sup
	s.mu.Lock()
	exited := p.exited
	s.mu.Unlock()
	h := p.pl.spec.Health
	go func() {
		ctx, cancel := g.stopContext()
		defer cancel()
		t := time.NewTicker(time.Duration(h.EffectiveIntervalS()) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-exited:
				return
			case <-g.stopCh:
				return
			case <-t.C:
				if probe(ctx, h, p.port, p.pl.healthArgv, p.pl.cwd, p.pl.env) {
					g.setHealth(p, healthPassing)
				} else if !isClosed(exited) {
					g.setHealth(p, healthFailing)
				}
			}
		}
	}()
}

// memberExited decides what a member exiting while the group runs means. It
// reports whether the group carries on.
//
// Restart is reachable only from here, only for a sidecar or worker, and only
// when its manifest says restart: on-failure. A main process is never
// restarted, whatever its manifest says (R27).
//
// A worker that fails without a restart left is recorded failed and the group
// carries on (see memberFailed). A sidecar in the same position fails the
// group, because its dependents need it.
func (g *group) memberExited(q *proc) bool {
	s := g.sup
	s.mu.Lock()
	code, noSweep := q.exitCode, q.noSweep
	s.mu.Unlock()
	if !noSweep {
		g.sweep(q)
	}
	// A member found claimed but unrecorded at re-adoption is never
	// restarted: a copy nobody recorded may still be running.
	restartable := (q.role == "sidecar" || q.role == "worker") && !q.noRestart &&
		q.pl != nil && !g.monitorOnly && q.pl.spec.EffectiveRestart() == "on-failure"
	failure := code == nil || *code != 0

	switch {
	case q.role == "worker" && !failure:
		g.setState(q, stateExited, reasonClean)
		return true
	case restartable && failure:
		wait, ok := g.allowRestart(q)
		if !ok {
			s.logf("supervisor: %s: %s exited %d times in %s; not restarting it again", g.studioID, q.name, s.cfg.MaxRestarts+1, s.cfg.RestartWindow)
			return g.memberFailed(q, reasonCrashed)
		}
		g.setState(q, stateStarting, reasonCrashed)
		select {
		case <-g.stopCh:
			g.setState(q, stateExited, reasonCrashed)
			return false
		case <-time.After(wait):
		}
		if err := g.spawn(q); err != nil {
			s.logf("supervisor: %s: restarting %s: %v", g.studioID, q.name, err)
			return g.memberFailed(q, reasonCrashed)
		}
		return g.gate(q)
	case q.role == "worker":
		return g.memberFailed(q, reasonCrashed)
	default:
		reason := reasonCrashed
		if !failure {
			reason = reasonClean
		}
		g.failMember(q, reason)
		return false
	}
}

// allowRestart applies R27's budget: at most MaxRestarts in RestartWindow.
func (g *group) allowRestart(q *proc) (time.Duration, bool) {
	s := g.sup
	now := s.now()
	kept := q.restarts[:0]
	for _, t := range q.restarts {
		if now.Sub(t) < s.cfg.RestartWindow {
			kept = append(kept, t)
		}
	}
	q.restarts = kept
	if len(kept) >= s.cfg.MaxRestarts {
		return 0, false
	}
	wait := s.cfg.RestartBackoff[min(len(kept), len(s.cfg.RestartBackoff)-1)]
	q.restarts = append(q.restarts, now)
	return wait, true
}

// errRecycled means the recorded pid now belongs to a different process.
var errRecycled = errors.New("the recorded pid now belongs to a different process")

// signalGroup sends SIGTERM or SIGKILL to a member's process group, but only
// when that is provably still the group helmstudio started: the leader is the
// same process (pid, start time and group all match), or the leader is gone
// and no other process has taken its pid — while any member of the old group
// survives, the kernel cannot hand that group id to anyone else.
func signalGroup(id platform.ProcessIdentity, kill bool) error {
	if err := verifyGroup(id); err != nil {
		return err
	}
	if kill {
		return platform.KillGroup(id.PGID)
	}
	return platform.TerminateGroup(id.PGID)
}

// verifyGroup is signalGroup's check without the signal.
func verifyGroup(id platform.ProcessIdentity) error {
	if id.PGID != id.PID {
		return fmt.Errorf("process %d: recorded group %d is not its own: %w", id.PID, id.PGID, platform.ErrUnsafeGroup)
	}
	cur, err := platform.IdentifyProcess(id.PID)
	switch {
	case err == nil && (id.StartTime == 0 || !cur.Matches(id)):
		return errRecycled
	case err != nil && !errors.Is(err, platform.ErrNoProcess):
		return err
	}
	return nil
}

func groupGone(id platform.ProcessIdentity) bool {
	exists, err := platform.GroupExists(id.PGID)
	return err == nil && !exists
}

// detachedDescendants lists the processes descended from any member of
// process group pgid that are no longer in that group — a render a studio
// started in its own session (docs/decisions.md M5 Q11).
// Only a process still parented, through any chain, by a member of the group
// is found: one already re-parented to init or launchd is not.
func detachedDescendants(table []platform.ProcessEntry, pgid int) []platform.ProcessIdentity {
	children := map[int][]platform.ProcessEntry{}
	var queue []int
	for _, e := range table {
		if e.PID == e.PPID {
			continue
		}
		children[e.PPID] = append(children[e.PPID], e)
		if e.PGID == pgid {
			queue = append(queue, e.PID)
		}
	}
	seen := map[int]bool{}
	var out []platform.ProcessIdentity
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if seen[c.PID] {
				continue
			}
			seen[c.PID] = true
			queue = append(queue, c.PID)
			if c.PGID != pgid {
				out = append(out, c.ProcessIdentity)
			}
		}
	}
	return out
}

// snapshotDetached adds to known the detached descendants of a member's
// group, read only while that group is still provably the one helmstudio
// started, so an unrelated process that inherited a pid is never taken for a
// descendant.
func (g *group) snapshotDetached(ident platform.ProcessIdentity, known []platform.ProcessIdentity) []platform.ProcessIdentity {
	if ident.PGID == 0 || verifyGroup(ident) != nil || groupGone(ident) {
		return known
	}
	table, err := platform.ProcessTable()
	if err != nil {
		g.sup.logf("supervisor: %s: reading the process table to find children that left group %d: %v", g.studioID, ident.PGID, err)
		return known
	}
	// The check above and the read are two moments. The table itself must
	// show the same leader, or no process with its pid, before its entries
	// with that group are trusted (M5 review #9).
	for _, e := range table {
		if e.PID == ident.PGID && e.StartTime != ident.StartTime {
			return known
		}
	}
	for _, d := range detachedDescendants(table, ident.PGID) {
		if !slices.ContainsFunc(known, d.SameProcess) {
			g.sup.logf("supervisor: %s: process %d left group %d but descends from it; it is stopped with the group", g.studioID, d.PID, ident.PGID)
			known = append(known, d)
		}
	}
	return known
}

// signalDetached signals each detached descendant still provably the same
// process (pid and start time); one whose pid now names another process is
// left alone.
func (g *group) signalDetached(procs []platform.ProcessIdentity, kill bool) {
	for _, d := range procs {
		if !stillRunning(d) {
			continue
		}
		send := platform.TerminateProcess
		if kill {
			send = platform.KillProcess
		}
		if err := send(d.PID); err != nil {
			g.sup.logf("supervisor: %s: signalling detached process %d: %v", g.studioID, d.PID, err)
		}
	}
}

// stillRunning reports whether a recorded process still exists as itself.
func stillRunning(d platform.ProcessIdentity) bool {
	cur, err := platform.IdentifyProcess(d.PID)
	return err == nil && cur.SameProcess(d)
}

// terminate stops a member's whole process group and every descendant that
// left it: SIGTERM, up to Grace for the leader to exit, the group to empty and
// the descendants to exit, then SIGKILL. The process table is read again
// before SIGKILL, for anything detached during the grace period.
func (g *group) terminate(ident platform.ProcessIdentity, exited chan struct{}) {
	s := g.sup
	detached := g.snapshotDetached(ident, nil)
	if err := signalGroup(ident, false); err != nil {
		if !errors.Is(err, errRecycled) {
			s.logf("supervisor: %s: SIGTERM to group %d: %v", g.studioID, ident.PGID, err)
		}
		return
	}
	g.signalDetached(detached, false)
	gone := func() bool {
		return (exited == nil || isClosed(exited)) && groupGone(ident) && !slices.ContainsFunc(detached, stillRunning)
	}
	if !waitUntil(time.Now().Add(s.cfg.Grace), gone) {
		detached = g.snapshotDetached(ident, detached)
		if err := signalGroup(ident, true); err != nil && !errors.Is(err, errRecycled) {
			s.logf("supervisor: %s: SIGKILL to group %d: %v", g.studioID, ident.PGID, err)
		}
		g.signalDetached(detached, true)
		if !waitUntil(time.Now().Add(s.cfg.KillWait), gone) {
			s.logf("supervisor: %s: process group %d or a process that left it survived SIGKILL for %s", g.studioID, ident.PGID, s.cfg.KillWait)
		}
	}
}

func waitUntil(deadline time.Time, cond func() bool) bool {
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// sweep ends whatever a member's process group still holds after its leader
// exited — children it forked that did not die with it.
func (g *group) sweep(p *proc) {
	s := g.sup
	s.mu.Lock()
	ident := p.ident
	s.mu.Unlock()
	if ident.PGID == 0 || groupGone(ident) {
		return
	}
	g.terminate(ident, nil)
}

// teardown stops every member in reverse dependency order.
func (g *group) teardown() {
	s := g.sup
	s.mu.Lock()
	reason := g.stopReason
	s.mu.Unlock()
	if g.failed {
		reason = "" // the failed member carries the reason
	}
	for i := len(g.procs) - 1; i >= 0; i-- {
		g.stopMember(g.procs[i], reason)
	}
	for _, port := range g.ports {
		s.alloc.release(port)
	}
	for _, p := range g.procs {
		if p.log != nil {
			p.log.close()
		}
	}
}

func (g *group) stopMember(p *proc, reason string) {
	s := g.sup
	s.mu.Lock()
	spawned, exited, state, ident, noSweep := p.spawned, p.exited, p.state, p.ident, p.noSweep
	s.mu.Unlock()
	if !spawned {
		if state == stateQueued {
			g.setState(p, stateExited, reason)
		}
		return
	}
	if !isClosed(exited) {
		if state != stateFailed {
			g.setState(p, stateStopping, "")
		}
		g.terminate(ident, exited)
	} else if !noSweep {
		g.sweep(p)
	}
	if state != stateFailed && state != stateExited {
		g.setState(p, stateExited, reason)
	}
}
