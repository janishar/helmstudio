package supervisor

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/weights"
)

// Config is everything a Supervisor needs. Zero durations and limits take the
// defaults documented on each field.
type Config struct {
	Dirs  *platform.Dirs
	Store *store.Store
	// Weights resolves {models.<name>} from the studio's bindings. Default:
	// a weights service over Store and Dirs that downloads nothing.
	Weights *weights.Service

	// PortMin and PortMax bound assigned ports. Default 8701–8799 (R22).
	PortMin, PortMax int
	// Grace is how long a process group has between SIGTERM and SIGKILL.
	// Default 30s: h3 studio's own SIGTERM path can take over 20s.
	Grace time.Duration
	// KillWait is how long to wait for a group to disappear after SIGKILL
	// before reporting it. Default 5s.
	KillWait time.Duration
	// RestartBackoff is the wait before each automatic restart of a sidecar
	// or worker; the last entry repeats. Default 1s, 5s, 25s.
	RestartBackoff []time.Duration
	// MaxRestarts within RestartWindow, after which the group fails.
	// Default three in ten minutes (R27).
	MaxRestarts   int
	RestartWindow time.Duration
	// ExitPoll is how often a re-adopted process — not this daemon's child,
	// so it cannot be waited on — is checked for exit. Default 500ms.
	ExitPoll time.Duration
	// LogTick is how often log files are tailed. Default 200ms.
	LogTick time.Duration
	// PortClearWait bounds the wait for a preempted group's ports. Default 30s.
	PortClearWait time.Duration
	// Log bounds per-process log memory and disk. Default DefaultLogLimits.
	Log LogLimits
	// RunsKept is how many group runs' logs a studio keeps. Default 5 (R30).
	RunsKept int
	// HostMemory reports the machine's memory for the heavy-studio
	// arithmetic. Default platform.HostMemoryBytes.
	HostMemory func() (uint64, error)
	// Logf receives the daemon's own diagnostics. Default: discarded.
	Logf func(format string, args ...any)
	// Platform connects launches to the studio API: the environment every
	// member of a group is spawned with, and word that a group has ended.
	// Nil runs studios with no platform environment (docs/decisions.md M2).
	Platform PlatformHooks
	// StudioData is {data} for a studio. Default <data>/studios/<id>/data;
	// helm dev keeps it at ./.helm/data (docs/decisions.md M4 Q26).
	StudioData func(m *manifest.Manifest) string

	// For tests.
	now        func() time.Time
	portFree   func(int) bool
	portHolder func(int) string
	failWrite  func(what string) error
}

// PlatformHooks is what a launch needs from the studio API
// (docs/design/07-platform-services.md §3).
type PlatformHooks interface {
	// LaunchEnv returns the variables every member of this launch gets:
	// HELM_API, HELM_TOKEN, HELM_STAGE_DIR, HELM_STUDIO_ID. It is called at
	// launch, and again for a re-adopted group whose members may be restarted.
	LaunchEnv(ctx context.Context, st Studio, groupRunID string) ([]string, error)
	// GroupEnded is called once a group has stopped: its tokens are revoked
	// and its stage directory removed.
	GroupEnded(studioID, groupRunID string)
}

// Supervisor owns every process group the daemon runs.
type Supervisor struct {
	cfg     Config
	dirs    *platform.Dirs
	store   *store.Store
	weights *weights.Service
	alloc   *allocator

	launchMu sync.Mutex // one launch or re-adoption at a time

	mu      sync.Mutex
	studios map[string]Studio
	groups  map[string]*group // the latest group per studio started by this daemon
}

// New returns a supervisor with no studios. Call SetStudios, then Readopt,
// before launching anything.
func New(cfg Config) *Supervisor {
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	if cfg.PortMin == 0 && cfg.PortMax == 0 {
		cfg.PortMin, cfg.PortMax = DefaultPortMin, DefaultPortMax
	}
	def(&cfg.Grace, 30*time.Second)
	def(&cfg.KillWait, 5*time.Second)
	def(&cfg.RestartWindow, 10*time.Minute)
	def(&cfg.ExitPoll, 500*time.Millisecond)
	def(&cfg.LogTick, 200*time.Millisecond)
	def(&cfg.PortClearWait, 30*time.Second)
	if len(cfg.RestartBackoff) == 0 {
		cfg.RestartBackoff = []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.Log == (LogLimits{}) {
		cfg.Log = DefaultLogLimits
	}
	if cfg.RunsKept == 0 {
		cfg.RunsKept = 5
	}
	if cfg.HostMemory == nil {
		cfg.HostMemory = platform.HostMemoryBytes
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.portFree == nil {
		cfg.portFree = portFree
	}
	if cfg.portHolder == nil {
		cfg.portHolder = platform.PortHolder
	}
	if cfg.Weights == nil {
		cfg.Weights = weights.New(weights.Config{Store: cfg.Store, Dirs: cfg.Dirs, Logf: cfg.Logf})
	}
	return &Supervisor{
		cfg:     cfg,
		dirs:    cfg.Dirs,
		store:   cfg.Store,
		weights: cfg.Weights,
		alloc: &allocator{
			min: cfg.PortMin, max: cfg.PortMax,
			leases: make(map[int]string),
			free:   cfg.portFree, holder: cfg.portHolder,
		},
		studios: make(map[string]Studio),
		groups:  make(map[string]*group),
	}
}

func (s *Supervisor) now() time.Time { return s.cfg.now() }

func (s *Supervisor) logf(format string, args ...any) { s.cfg.Logf(format, args...) }

// SetStudios replaces the set of launchable studios. Running groups are not
// affected.
func (s *Supervisor) SetStudios(studios []Studio) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.studios = make(map[string]Studio, len(studios))
	for _, st := range studios {
		s.studios[st.Manifest.ID] = st
	}
}

// Studios lists the known studios, sorted by id.
func (s *Supervisor) Studios() []Studio {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Studio, 0, len(s.studios))
	for _, st := range s.studios {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

// Unmanaged lists the studios this daemon supervises without a loaded
// manifest — re-adopted from the last run when its manifest was not found or
// no longer validates. They are still running, count as heavy, and can be
// stopped, so the launcher must show them.
func (s *Supervisor) Unmanaged() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, g := range s.groups {
		if _, known := s.studios[id]; !known && !g.finished() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Studio returns one known studio.
func (s *Supervisor) Studio(id string) (Studio, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.studios[id]
	return st, ok
}

// LaunchOptions modify a launch.
type LaunchOptions struct {
	// Preempt confirms stopping a running heavy group so this one can start
	// (R25). Without it, a second heavy launch is refused with the arithmetic.
	Preempt bool
}

// Launch starts a studio's process group. It returns once the group is
// planned and its rows are written; spawning and health gating continue in
// the background and are visible through Status.
func (s *Supervisor) Launch(ctx context.Context, studioID string, opts LaunchOptions) (GroupStatus, error) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()

	s.mu.Lock()
	st, ok := s.studios[studioID]
	if !ok {
		s.mu.Unlock()
		return GroupStatus{}, &Error{Kind: KindNotFound, Message: fmt.Sprintf("no studio %q is known", studioID)}
	}
	if g := s.groups[studioID]; g != nil && !g.finished() {
		s.mu.Unlock()
		return GroupStatus{}, &Error{Kind: KindAlreadyRunning, Message: fmt.Sprintf("%s is already running; stop it before launching it again", st.Manifest.Name)}
	}
	var other *group
	if heavyMember(st.Manifest) {
		other = s.liveHeavyLocked(studioID)
	}
	s.mu.Unlock()

	if other != nil {
		// Find out whether this studio can launch at all before stopping
		// anything for it. Ports are left out of the check — the running
		// group may hold the very port this one needs — so everything else
		// is resolved against an allocator that finds every port free.
		dry := &allocator{min: s.cfg.PortMin, max: s.cfg.PortMax, leases: map[int]string{},
			free: func(int) bool { return true }, holder: func(int) string { return "" }}
		lp, err := s.installed(ctx, st)
		if err != nil {
			return GroupStatus{}, err
		}
		if _, err := resolvePlan(s.dirs, st, lp, nil, dry); err != nil {
			return GroupStatus{}, err
		}
		arith := s.arithmetic(other, st)
		if !opts.Preempt {
			return GroupStatus{}, &Error{Kind: KindHeavyConflict, Message: arith.sentence(), Heavy: &arith}
		}
		s.logf("supervisor: stopping %s so %s can launch: %s", other.studioID, studioID, arith.sentence())
		other.requestStop(reasonPreempted)
		// The running studio is already being stopped. The launch finishes
		// even if the caller goes away, or the user is left with neither.
		ctx = context.WithoutCancel(ctx)
		select {
		case <-other.done:
		case <-ctx.Done():
			return GroupStatus{}, ctx.Err()
		}
		if err := s.waitPortsClear(ctx, other); err != nil {
			return GroupStatus{}, err
		}
	}

	lp, err := s.installed(ctx, st)
	if err != nil {
		return GroupStatus{}, err
	}
	plan, err := resolvePlan(s.dirs, st, lp, nil, s.alloc)
	if err != nil {
		return GroupStatus{}, err
	}
	if err := os.MkdirAll(s.studioData(st.Manifest), 0o700); err != nil {
		s.releasePlanPorts(plan)
		return GroupStatus{}, fmt.Errorf("%s: creating its data directory: %w", studioID, err)
	}

	g := newGroup(s, st, studioID, newID(s.now()))
	if err := s.addPlatformEnv(ctx, st, g.runID, plan); err != nil {
		s.releasePlanPorts(plan)
		return GroupStatus{}, err
	}
	for _, pl := range plan {
		p := &proc{g: g, pl: pl, rowID: newID(s.now()), name: pl.spec.Name, role: pl.role,
			command: pl.argv, port: pl.port, state: stateQueued, health: healthUnknown}
		g.procs = append(g.procs, p)
		if pl.port != 0 {
			g.ports = append(g.ports, pl.port)
		}
	}
	s.applyRetention(ctx, studioID, s.cfg.RunsKept-1)
	if err := s.insertQueued(g.procs); err != nil {
		s.releasePlanPorts(plan)
		return GroupStatus{}, fmt.Errorf("%s was not launched: %w", studioID, err)
	}
	s.mu.Lock()
	s.groups[studioID] = g
	s.mu.Unlock()
	go g.run()
	return s.Status(ctx, studioID)
}

// addPlatformEnv appends the launch's platform environment to every member.
func (s *Supervisor) addPlatformEnv(ctx context.Context, st Studio, runID string, plan []*planned) error {
	if s.cfg.Platform == nil || len(plan) == 0 {
		return nil
	}
	env, err := s.cfg.Platform.LaunchEnv(ctx, st, runID)
	if err != nil {
		return fmt.Errorf("%s was not launched: preparing its platform environment: %w", st.Manifest.ID, err)
	}
	for _, pl := range plan {
		pl.env = append(pl.env, env...)
	}
	return nil
}

func (s *Supervisor) releasePlanPorts(plan []*planned) {
	for _, pl := range plan {
		if pl.port != 0 {
			s.alloc.release(pl.port)
		}
	}
}

// liveHeavyLocked returns a running group that holds a heavy member. A
// re-adopted group whose manifest is not loaded cannot say whether it is
// heavy, so it is counted as heavy: refusing a launch the user can confirm
// past is recoverable, and two models overflowing unified memory is not.
func (s *Supervisor) liveHeavyLocked(except string) *group {
	for id, g := range s.groups {
		if id == except || g.finished() {
			continue
		}
		if g.studio.Manifest == nil || heavyMember(g.studio.Manifest) {
			return g
		}
	}
	return nil
}

func (s *Supervisor) arithmetic(running *group, wanted Studio) HeavyArithmetic {
	a := HeavyArithmetic{
		RunningStudio: running.studioID, RunningName: running.studioID + " (its manifest is not loaded)",
		WantedStudio: wanted.Manifest.ID, WantedName: wanted.Manifest.Name, WantedPeakGB: wanted.Manifest.PeakRAMGB,
	}
	if m := running.studio.Manifest; m != nil {
		a.RunningName, a.RunningPeakGB = m.Name, m.PeakRAMGB
	}
	if b, err := s.cfg.HostMemory(); err == nil {
		a.HostGB = int(math.Round(float64(b) / (1 << 30)))
	}
	return a
}

// sentence states the arithmetic in words a person can act on.
func (a HeavyArithmetic) sentence() string {
	gb := func(name string, n int, verb string) string {
		if n == 0 {
			return fmt.Sprintf("%s %s an undeclared amount of memory (its manifest has no peak_ram_gb)", name, verb)
		}
		return fmt.Sprintf("%s %s up to %d GB", name, verb, n)
	}
	var b strings.Builder
	b.WriteString(gb(a.RunningName+" is running and", a.RunningPeakGB, "may hold"))
	b.WriteString("; ")
	b.WriteString(gb(a.WantedName, a.WantedPeakGB, "needs"))
	switch {
	case a.RunningPeakGB > 0 && a.WantedPeakGB > 0 && a.HostGB > 0:
		fmt.Fprintf(&b, " — together %d GB of this machine's %d GB", a.RunningPeakGB+a.WantedPeakGB, a.HostGB)
	case a.HostGB > 0:
		fmt.Fprintf(&b, " — this machine has %d GB", a.HostGB)
	}
	fmt.Fprintf(&b, ". Only one heavy studio runs at a time: stop %s first, or confirm to have it stopped for you.", a.RunningName)
	return b.String()
}

func (s *Supervisor) waitPortsClear(ctx context.Context, g *group) error {
	deadline := time.Now().Add(s.cfg.PortClearWait)
	for _, port := range g.ports {
		for !s.cfg.portFree(port) {
			if time.Now().After(deadline) {
				who := s.cfg.portHolder(port)
				if who == "" {
					who = "a process lsof could not name"
				}
				return &Error{Kind: KindPortConflict, Message: fmt.Sprintf(
					"%s was stopped, but its port %d is still held by %s", g.studio.Manifest.Name, port, who)}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return nil
}

// Stop tears a studio's group down in reverse dependency order. It returns at
// once; Status shows stopping, then exited.
func (s *Supervisor) Stop(studioID string) error {
	s.mu.Lock()
	g := s.groups[studioID]
	s.mu.Unlock()
	if g == nil || g.finished() {
		return &Error{Kind: KindNotRunning, Message: fmt.Sprintf("%s is not running", studioID)}
	}
	g.requestStop(reasonKilledByUser)
	return nil
}

// Wait blocks until a studio's current group has finished tearing down.
func (s *Supervisor) Wait(ctx context.Context, studioID string) error {
	s.mu.Lock()
	g := s.groups[studioID]
	s.mu.Unlock()
	if g == nil {
		return nil
	}
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown stops every group, as a clean daemon exit does (R28), and waits for
// them until ctx ends.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	s.mu.Lock()
	var live []*group
	for _, g := range s.groups {
		if !g.finished() {
			live = append(live, g)
		}
	}
	s.mu.Unlock()
	for _, g := range live {
		g.requestStop(reasonClean)
	}
	for _, g := range live {
		select {
		case <-g.done:
		case <-ctx.Done():
			return fmt.Errorf("stopping studios: %w", ctx.Err())
		}
	}
	return nil
}

// ProcessStatus is one process as the launcher shows it.
type ProcessStatus struct {
	Name           string     `json:"spec_name"`
	Role           string     `json:"role"`
	State          string     `json:"state"`
	HealthState    string     `json:"health_state"`
	ExitReason     string     `json:"exit_reason,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	PID            int        `json:"pid,omitempty"`
	PGID           int        `json:"pgid,omitempty"`
	Port           int        `json:"port,omitempty"`
	Command        string     `json:"command"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	HealthTimeoutS int        `json:"health_timeout_s,omitempty"`
	UI             string     `json:"ui,omitempty"`
}

// Failure is what the launcher shows when a group failed: which member, why,
// and its last output (R27: the last 200 lines).
type Failure struct {
	Process    string   `json:"process"`
	ExitReason string   `json:"exit_reason"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	LastLines  []string `json:"last_lines"`
}

// GroupStatus is a studio's current or most recent group run.
type GroupStatus struct {
	StudioID   string          `json:"studio_id"`
	GroupRunID string          `json:"group_run_id,omitempty"`
	State      string          `json:"state,omitempty"`
	Processes  []ProcessStatus `json:"processes"`
	Failure    *Failure        `json:"failure,omitempty"`
}

// lastLinesShown is how much output a failure surfaces (R27).
const lastLinesShown = 200

// Status reports a studio's group: the live one, else the last one this
// daemon ran, else the last one recorded.
func (s *Supervisor) Status(ctx context.Context, studioID string) (GroupStatus, error) {
	s.mu.Lock()
	g := s.groups[studioID]
	st, known := s.studios[studioID]
	if g != nil {
		gs := GroupStatus{StudioID: studioID, GroupRunID: g.runID, Processes: []ProcessStatus{}}
		var failed *proc
		for _, p := range g.procs {
			ps := ProcessStatus{Name: p.name, Role: p.role, State: p.state, HealthState: p.health,
				ExitReason: p.exitReason, ExitCode: p.exitCode, Port: p.port, Command: describe(p.command)}
			if p.spawned {
				ps.PID, ps.PGID = p.ident.PID, p.ident.PGID
				t := p.startedAt
				ps.StartedAt = &t
			}
			if p.pl != nil {
				if h := p.pl.spec.Health; h != nil {
					ps.HealthTimeoutS = h.EffectiveTimeoutS()
				}
				ps.UI = p.pl.spec.UI
			}
			if p.state == stateFailed && (failed == nil || failed.role == "worker") {
				failed = p
			}
			gs.Processes = append(gs.Processes, ps)
		}
		gs.State = groupState(gs.Processes)
		s.mu.Unlock()
		if failed != nil {
			gs.Failure = &Failure{Process: failed.name, ExitReason: failed.exitReason, ExitCode: failed.exitCode}
			if failed.log != nil {
				gs.Failure.LastLines = failed.log.lastLines(lastLinesShown)
			}
		}
		return gs, nil
	}
	s.mu.Unlock()
	if !known {
		return GroupStatus{}, &Error{Kind: KindNotFound, Message: fmt.Sprintf("no studio %q is known", studioID)}
	}

	rows, err := s.lastRunRows(ctx, studioID)
	if err != nil {
		return GroupStatus{}, err
	}
	gs := GroupStatus{StudioID: studioID, Processes: []ProcessStatus{}}
	for _, r := range rows {
		gs.GroupRunID = r.GroupRunID
		ps := ProcessStatus{Name: r.SpecName, Role: r.Role, State: r.State, HealthState: r.Health,
			ExitReason: r.ExitReason, ExitCode: r.ExitCode, PID: r.PID, PGID: r.PGID, Port: r.Port, Command: describe(r.Command)}
		if !r.StartedAt.IsZero() {
			t := r.StartedAt
			ps.StartedAt = &t
		}
		for _, sp := range st.Manifest.EffectiveProcesses() {
			if sp.Name == r.SpecName {
				ps.UI = sp.UI
			}
		}
		gs.Processes = append(gs.Processes, ps)
		if r.State == stateFailed && (gs.Failure == nil || r.Role != "worker") {
			gs.Failure = &Failure{Process: r.SpecName, ExitReason: r.ExitReason, ExitCode: r.ExitCode,
				LastLines: readLastLines(filepath.Join(s.dirs.Logs(), r.LogPath), lastLinesShown)}
		}
	}
	if len(rows) > 0 {
		gs.State = groupState(gs.Processes)
	}
	return gs, nil
}

// groupState folds member states into one, the way the top bar shows a group.
// A failed worker does not fail the group: its main keeps running.
func groupState(ps []ProcessStatus) string {
	has := func(states ...string) bool {
		for _, p := range ps {
			for _, st := range states {
				if p.State == st && !(st == stateFailed && p.Role == "worker") {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has(stateFailed):
		return stateFailed
	case has(stateStopping):
		return stateStopping
	case has(stateQueued, stateStarting):
		return stateStarting
	case has(stateRunning):
		return stateRunning
	}
	for _, p := range ps {
		if p.State == stateFailed {
			return stateFailed // only workers, and nothing else is left
		}
	}
	return stateExited
}

// LogFile is one run log.
type LogFile struct {
	ID         string    `json:"id"`
	GroupRunID string    `json:"group_run_id"`
	Process    string    `json:"process"`
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	Truncated  bool      `json:"truncated"`
	CapBytes   int64     `json:"cap_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// LogFiles lists a studio's retained run logs, newest first.
func (s *Supervisor) LogFiles(ctx context.Context, studioID string) ([]LogFile, error) {
	rows, err := s.store.Reader().QueryContext(ctx, `SELECT l.id, p.group_run_id, p.spec_name, l.path, l.truncated, l.created_at
		FROM log_files l JOIN processes p ON p.id = l.owner_id
		WHERE l.studio_id = ? AND l.owner_kind = 'process' ORDER BY l.created_at DESC, l.rowid DESC`, studioID)
	if err != nil {
		return nil, fmt.Errorf("listing the logs of %s: %w", studioID, err)
	}
	defer rows.Close()
	out := []LogFile{}
	for rows.Next() {
		var f LogFile
		var created int64
		if err := rows.Scan(&f.ID, &f.GroupRunID, &f.Process, &f.Path, &f.Truncated, &created); err != nil {
			return nil, err
		}
		f.CreatedAt = time.UnixMilli(created)
		f.CapBytes = s.cfg.Log.FileCap
		f.Path = filepath.Join(s.dirs.Logs(), f.Path)
		if fi, err := os.Lstat(f.Path); err == nil {
			f.Bytes = fi.Size()
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// LogSubscription is a live view of one process's output.
type LogSubscription struct {
	// History is what was already buffered, oldest first.
	History []LogLine
	// Lines delivers what follows; nil when the process is not live, in which
	// case History is read from its log file and is all there is.
	Lines <-chan LogLine
	// Dropped returns, and resets, how many lines this viewer lost for being
	// slower than the process.
	Dropped func() int64
	// Close ends the subscription.
	Close func()
}

// SubscribeLogs follows one process of a studio's current group, replaying
// buffered lines after afterSeq. For a studio whose group is not in memory it
// returns the tail of the most recent log file instead.
func (s *Supervisor) SubscribeLogs(ctx context.Context, studioID, process string, afterSeq uint64) (LogSubscription, error) {
	s.mu.Lock()
	g := s.groups[studioID]
	var stream *logStream
	if g != nil {
		for _, p := range g.procs {
			if p.name == process {
				stream = p.log
			}
		}
	}
	_, known := s.studios[studioID]
	s.mu.Unlock()
	if stream != nil {
		history, sub := stream.subscribe(afterSeq)
		return LogSubscription{
			History: history,
			Lines:   sub.ch,
			Dropped: func() int64 { return sub.dropped.Swap(0) },
			Close:   func() { stream.unsubscribe(sub) },
		}, nil
	}
	if !known && g == nil {
		return LogSubscription{}, &Error{Kind: KindNotFound, Message: fmt.Sprintf("no studio %q is known", studioID)}
	}
	rows, err := s.lastRunRows(ctx, studioID)
	if err != nil {
		return LogSubscription{}, err
	}
	for _, r := range rows {
		if r.SpecName == process && r.LogPath != "" {
			var history []LogLine
			for i, line := range readLastLines(filepath.Join(s.dirs.Logs(), r.LogPath), s.cfg.Log.RingLines) {
				history = append(history, LogLine{Seq: uint64(i + 1), Text: line})
			}
			return LogSubscription{History: history, Dropped: func() int64 { return 0 }, Close: func() {}}, nil
		}
	}
	return LogSubscription{}, &Error{Kind: KindNotFound, Message: fmt.Sprintf("%s has no log for a process named %q", studioID, process)}
}

// readLastLines returns up to n trailing lines of a file, reading at most the
// last 4 MiB of it.
func readLastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := max(fi.Size()-4<<20, 0)
	if _, err := f.Seek(start, 0); err != nil {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine*4)
	first := start > 0
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

// applyRetention deletes the log files of every group run of a studio beyond
// the newest keep, and their rows (R30). It only removes regular files inside
// the logs root; a path that has become a symlink or escapes the root is left
// alone and reported.
func (s *Supervisor) applyRetention(ctx context.Context, studioID string, keep int) {
	rows, err := s.store.Reader().QueryContext(ctx, `SELECT l.id, l.path, p.group_run_id
		FROM log_files l JOIN processes p ON p.id = l.owner_id
		WHERE l.studio_id = ? AND l.owner_kind = 'process' AND p.state NOT IN ('queued','starting','running','stopping')
		ORDER BY l.created_at DESC, l.rowid DESC`, studioID)
	if err != nil {
		s.logf("supervisor: log retention for %s: %v", studioID, err)
		return
	}
	type entry struct{ id, path, run string }
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.path, &e.run); err == nil {
			all = append(all, e)
		}
	}
	rows.Close()

	runs := map[string]bool{}
	var doomed []entry
	for _, e := range all {
		if !runs[e.run] && len(runs) >= keep {
			doomed = append(doomed, e)
			continue
		}
		runs[e.run] = true
	}
	if len(doomed) == 0 {
		return
	}
	// Removal goes through os.Root on the logs root, so a symlinked
	// directory on the way (<logs>/studios/<id>) is refused rather than
	// followed (M3 review), and only a regular file is removed.
	logsRoot := filepath.Clean(s.dirs.Logs())
	logs, err := os.OpenRoot(logsRoot)
	if err != nil {
		s.logf("supervisor: log retention for %s: %v", studioID, err)
		return
	}
	defer logs.Close()
	var removed []string
	for _, e := range doomed {
		abs := filepath.Join(logsRoot, e.path)
		if !filepath.IsLocal(e.path) || !strings.HasSuffix(e.path, ".log") {
			s.logf("supervisor: not deleting log %q: it is not a run log inside the logs root", e.path)
			continue
		}
		fi, err := logs.Lstat(e.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			s.logf("supervisor: not deleting log %s: %v", abs, err)
			continue
		case !fi.Mode().IsRegular():
			s.logf("supervisor: not deleting log %s: it is not a regular file", abs)
			continue
		default:
			if err := logs.Remove(e.path); err != nil {
				s.logf("supervisor: deleting old log %s: %v", abs, err)
				continue
			}
		}
		removed = append(removed, e.id)
	}
	_ = s.write("log retention", func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM log_files WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Running reports whether a studio has a group this daemon has not finished
// tearing down.
func (s *Supervisor) Running(studioID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[studioID]
	return g != nil && !g.finished()
}

// HoldLaunches runs fn while no launch can begin or be mid-way, so a caller
// can check Running and change a studio's install state without a launch
// slipping in between. fn must not call Launch or Shutdown.
func (s *Supervisor) HoldLaunches(fn func() error) error {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	return fn()
}
