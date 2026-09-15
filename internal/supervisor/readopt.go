package supervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/janishar/helmstudio/internal/platform"
)

// ReadoptReport says what a startup sweep found, for the daemon's log.
type ReadoptReport struct {
	Adopted []string // "studio/process pid N"
	Gone    []string // recorded as running, no longer there or not the same process
	// Survivors names process groups whose recorded leader is gone but which
	// still have members — children of a crashed studio, possibly holding
	// memory. They are reported, never signalled: nothing proves they are
	// the group helmstudio started.
	Survivors []string
	// Unmanaged names adopted studios whose manifest is not loaded.
	Unmanaged []string
}

// Readopt runs once at daemon start, before any launch. Every process the last
// daemon left running is checked against its recorded pid, process start time
// and process group. A process matching all three is adopted: its group is
// supervised again, logs resume, and it can be stopped. One that does not
// match — gone, or a different process that inherited the pid — is recorded
// as crashed and never signalled (docs/design/02-data-model.md §7).
func (s *Supervisor) Readopt(ctx context.Context) (ReadoptReport, error) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()

	rows, err := s.liveRows(ctx)
	if err != nil {
		return ReadoptReport{}, err
	}
	type run struct {
		studioID, runID string
		rows            []row
	}
	var runs []*run
	byID := map[string]*run{}
	for _, r := range rows {
		key := r.StudioID + "/" + r.GroupRunID
		if byID[key] == nil {
			byID[key] = &run{studioID: r.StudioID, runID: r.GroupRunID}
			runs = append(runs, byID[key])
		}
		byID[key].rows = append(byID[key].rows, r)
	}

	var report ReadoptReport
	for _, rn := range runs {
		alive := map[string]platform.ProcessIdentity{}
		for _, r := range rn.rows {
			if r.State == stateQueued {
				continue
			}
			if r.PID == 0 {
				// Claimed for a spawn that was never recorded: a process may
				// be running from it that nobody can identify.
				report.Gone = append(report.Gone, fmt.Sprintf("%s/%s (started, pid never recorded)", r.StudioID, r.SpecName))
				continue
			}
			recorded := platform.ProcessIdentity{PID: r.PID, StartTime: r.StartTime, PGID: r.PGID}
			cur, err := platform.IdentifyProcess(r.PID)
			switch {
			case err == nil && r.StartTime != 0 && cur.Matches(recorded):
				alive[r.ID] = recorded
				report.Adopted = append(report.Adopted, fmt.Sprintf("%s/%s pid %d", r.StudioID, r.SpecName, r.PID))
			case err == nil:
				// The pid now belongs to another process. The kernel does not
				// reuse a pid while a group with that id exists, so the old
				// group had already ended: there are no survivors to report.
				report.Gone = append(report.Gone, fmt.Sprintf("%s/%s pid %d (now another process)", r.StudioID, r.SpecName, r.PID))
			case errors.Is(err, platform.ErrNoProcess):
				report.Gone = append(report.Gone, fmt.Sprintf("%s/%s pid %d", r.StudioID, r.SpecName, r.PID))
				if r.PGID > 1 {
					if members, gerr := platform.GroupExists(r.PGID); gerr == nil && members {
						report.Survivors = append(report.Survivors, fmt.Sprintf("%s/%s process group %d", r.StudioID, r.SpecName, r.PGID))
					}
				}
			default:
				s.logf("supervisor: re-adoption: could not check %s/%s pid %d, recording it as gone without signalling it: %v", r.StudioID, r.SpecName, r.PID, err)
				report.Gone = append(report.Gone, fmt.Sprintf("%s/%s pid %d", r.StudioID, r.SpecName, r.PID))
			}
		}

		if len(alive) == 0 {
			for _, r := range rn.rows {
				p := &proc{rowID: r.ID}
				switch {
				case r.State == stateQueued:
					s.persistState(p, stateExited, "", nil, s.now())
				default:
					s.persistState(p, stateExited, reasonCrashed, nil, s.now())
				}
			}
			continue
		}
		if !s.adoptRun(ctx, rn.studioID, rn.runID, rn.rows, alive) {
			report.Unmanaged = append(report.Unmanaged, rn.studioID)
		}
	}
	return report, nil
}

// adoptRun rebuilds a group from its rows and resumes it. It reports whether
// the studio's manifest is loaded.
func (s *Supervisor) adoptRun(ctx context.Context, studioID, runID string, rows []row, alive map[string]platform.ProcessIdentity) bool {
	s.mu.Lock()
	st, known := s.studios[studioID]
	s.mu.Unlock()

	g := newGroup(s, st, studioID, runID)
	byName := map[string]row{}
	assigned := map[string]int{}
	for _, r := range rows {
		byName[r.SpecName] = r
		if _, ok := alive[r.ID]; ok && r.Port != 0 {
			assigned[r.SpecName] = r.Port
		}
	}

	// Resolve the manifest as it is now. Survivors keep the ports they hold;
	// members that never started get fresh ones.
	var plan []*planned
	if known {
		for name, port := range assigned {
			s.alloc.lease(port, fmt.Sprintf("%s (process %q)", studioID, name))
		}
		lp, err := s.installed(ctx, st)
		if err == nil {
			plan, err = resolvePlan(s.dirs, st, lp, assigned, s.alloc)
		}
		if err != nil {
			s.logf("supervisor: re-adoption: %s no longer resolves (%v); watching its survivors without restarting or probing them", studioID, err)
			plan = nil
		}
	}
	if plan != nil {
		if err := s.addPlatformEnv(ctx, st, runID, plan); err != nil {
			s.logf("supervisor: re-adoption: %s: %v; watching its survivors without restarting or probing them", studioID, err)
			s.releasePlanPorts(plan)
			plan = nil
		}
	}
	if plan != nil {
		for _, pl := range plan {
			if _, ok := byName[pl.spec.Name]; !ok {
				s.logf("supervisor: re-adoption: %s's manifest now has a process %q the running group does not; watching its survivors only", studioID, pl.spec.Name)
				s.releasePlanPorts(plan)
				plan = nil
				break
			}
		}
	}

	if plan == nil {
		g.monitorOnly = true
		for _, r := range rows {
			g.procs = append(g.procs, s.procFromRow(g, nil, r, alive))
		}
	} else {
		for _, pl := range plan {
			g.procs = append(g.procs, s.procFromRow(g, pl, byName[pl.spec.Name], alive))
			delete(byName, pl.spec.Name)
		}
		for _, r := range byName { // rows the manifest no longer names
			g.procs = append(g.procs, s.procFromRow(g, nil, r, alive))
		}
	}
	for _, p := range g.procs {
		if p.port != 0 {
			g.ports = append(g.ports, p.port)
			s.alloc.lease(p.port, fmt.Sprintf("%s (process %q)", studioID, p.name))
		}
	}

	s.mu.Lock()
	s.groups[studioID] = g
	s.mu.Unlock()
	for _, p := range g.procs {
		if ident, ok := alive[p.rowID]; ok {
			go g.watchAdopted(p, ident, p.exited)
		}
	}
	for _, r := range rows {
		if _, ok := alive[r.ID]; ok && r.State == stateStopping {
			g.requestStop(reasonKilledByUser) // the last daemon was asked to stop it
			break
		}
	}
	go g.run()
	return known
}

func (s *Supervisor) procFromRow(g *group, pl *planned, r row, alive map[string]platform.ProcessIdentity) *proc {
	p := &proc{g: g, pl: pl, rowID: r.ID, name: r.SpecName, role: r.Role, command: r.Command, port: r.Port,
		state: r.State, health: r.Health, startedAt: r.StartedAt, logRel: r.LogPath}
	if p.health == "" {
		p.health = healthUnknown
	}
	if pl != nil && p.port == 0 {
		p.port = pl.port
	}
	if pl != nil && r.State == stateQueued {
		p.command = pl.argv
	}
	if r.LogPath != "" {
		p.log = newLogStream(filepath.Join(s.dirs.Logs(), r.LogPath), s.cfg.Log)
		rowID := r.ID
		p.log.onTruncate = func() { s.markLogTruncated(rowID) }
		p.log.seedFromFile(64 << 10)
		go p.log.run(s.cfg.LogTick)
	}
	switch ident, ok := alive[r.ID]; {
	case ok:
		p.spawned, p.ident, p.exited = true, ident, make(chan struct{})
		if r.State == stateStopping {
			p.state = stateRunning
		}
	case r.State == stateQueued:
		p.state = stateQueued
	default:
		// Recorded as spawned, now gone or a different process: crashed,
		// and nothing is signalled on its behalf.
		closed := make(chan struct{})
		close(closed)
		p.spawned, p.exited, p.noSweep = true, closed, true
		p.noRestart = r.PID == 0 // claimed, never recorded
		p.ident = platform.ProcessIdentity{PID: r.PID, StartTime: r.StartTime, PGID: r.PGID}
		p.state, p.exitReason = stateExited, reasonCrashed
		s.persistState(p, stateExited, reasonCrashed, nil, s.now())
	}
	if g.monitorOnly && p.state == stateQueued {
		p.state = stateExited
		s.persistState(p, stateExited, "", nil, s.now())
	}
	return p
}
