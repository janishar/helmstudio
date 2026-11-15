package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// The fair dispatcher (plan 17.5, contracts/README.md "The dispatcher"). A run waits in the runs table (queued,
// released_at null) until a pass releases it to one free host: host set, released_at stamped and cmd.run.execute
// written to the outbox, in one transaction. A host is free while no released run on it has ended.

const (
	// Lease is how long after its release a run may hold its host without the host's key naming it.
	Lease = 30 * time.Second
	// Confirm is how long a run must keep looking lost before it loses its lease: one look at the hosts KV is not
	// enough (a runner writes idle right after a run, and the view can lag behind the projector).
	Confirm = 10 * time.Second
	// Tick is how often the dispatcher passes. ponytail: polling; NOTIFY from create, cancel and the projector if
	// half a second of queue latency ever matters.
	Tick = 500 * time.Millisecond

	// The GPU gate (contracts/README.md "Two envs on one host"): an env holding weights is asked to unload at most every
	// AskEvery, each ask waits UnloadWait for its reply, and a run the gate keeps shut for GateTimeout fails.
	AskEvery    = 10 * time.Second
	UnloadWait  = 60 * time.Second
	GateTimeout = 120 * time.Second
)

// Dispatcher releases queued runs and watches the leases of released ones. purge drops the messages of a CMD subject
// and backlog says how many events the projector has yet to apply; nil means no NATS (tests). lost holds when each
// released run that looks lost was first seen so.
type Dispatcher struct {
	pool     *pgxpool.Pool
	registry *registry.Registry
	purge    func(ctx context.Context, subject string) error
	backlog  func(ctx context.Context) (int, error)
	lost     map[string]time.Time
	// Unload sends a core NATS request (runner.unload.<host>.<env>) and returns the reply; nil: never asked (a gate
	// shut by another env's weights then waits for the hosts watch, and fails after GateTimeout).
	Unload func(ctx context.Context, subject string, body []byte) ([]byte, error)
	gated  map[string]time.Time // run id → when the gate first kept it (in memory: a restart starts over)
	mu     sync.Mutex           // guards asks: the replies arrive on their own goroutines
	asks   map[string]*ask      // "<host>.<env>" asked to unload → the state of its asks
	asking sync.WaitGroup       // the asks in flight (tests wait for them)
}

// ask is the state of the unload requests to one env: whether one is in flight, when the last went, whether it
// answered ok since the last run released to it (it loads nothing again before its next command), and gen, bumped by
// every release to it: a reply to a request sent before a release says nothing about what that run loads.
type ask struct {
	inFlight, ok bool
	sent         time.Time
	gen          int
}

// NewDispatcher returns the dispatcher.
func NewDispatcher(pool *pgxpool.Pool, registry *registry.Registry, purge func(ctx context.Context, subject string) error,
	backlog func(ctx context.Context) (int, error)) *Dispatcher {
	return &Dispatcher{pool: pool, registry: registry, purge: purge, backlog: backlog, lost: map[string]time.Time{},
		gated: map[string]time.Time{}, asks: map[string]*ask{}}
}

// Settle waits for the unload requests in flight (tests).
func (d *Dispatcher) Settle() { d.asking.Wait() }

// Run passes every Tick until ctx ends.
func (d *Dispatcher) Run(ctx context.Context) {
	tick := time.NewTicker(Tick)
	defer tick.Stop()
	for {
		if err := d.Pass(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("dispatcher: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Pass does one round as System, under a transaction-scoped advisory lock so two replicas never pass at once: a
// released run whose lease has been lost for Confirm is retried once (purged, attempt 2, queued again) or ended; a
// waiting run with no eligible host fails; then each free host takes the next run in the fair order. Nothing happens
// until the registry has loaded the KV's current values.
func (d *Dispatcher) Pass(ctx context.Context, now time.Time) error {
	if !d.registry.Ready() { // an empty view would read every host as gone: nothing to decide yet
		return nil
	}
	lost, gated := map[string]time.Time{}, map[string]time.Time{}
	err := common.System(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('studio.dispatcher'))`); err != nil {
			return err
		}
		snap, err := look(ctx, tx, d.registry, now)
		if err != nil {
			return err
		}
		busy := map[string]bool{}
		lagging := -1 // the projector's backlog, read once a run first looks lost: 1 while it has events to apply
		for _, run := range snap.released {
			host := *run.Host
			if holds(snap.values, run, now) {
				busy[host] = true
				continue
			}
			if lagging < 0 {
				lagging = 0
				if d.backlog != nil {
					if pending, err := d.backlog(ctx); err != nil || pending > 0 {
						lagging = 1
					}
				}
			}
			first, seen := d.lost[run.ID]
			if !seen {
				first = now
			}
			busy[host] = true // until it is let go below
			if lagging == 1 {
				continue // the terminal status may be on its way: Confirm starts over once the projector caught up
			}
			lost[run.ID] = first
			if now.Sub(first) < Confirm {
				continue
			}
			subject := "cmd.run." + host + "." + run.Env
			if d.purge != nil {
				if err := d.purge(ctx, subject); err != nil {
					log.Printf("dispatcher: purge %s: %v", subject, err) // stays held; tried again next pass
					continue
				}
			}
			if err := lose(ctx, tx, run, now); err != nil {
				return err
			}
			delete(lost, run.ID)
			busy[host] = false
		}
		for _, stuck := range snap.stuck {
			if _, err := finish(ctx, tx, stuck.run, "failed", &stuck.reason); err != nil {
				return err
			}
		}
		var free []string
		for _, host := range snap.hosts {
			if !busy[host] {
				free = append(free, host)
			}
		}
		for _, next := range lineup(free, snap.queue, snap.last, nil, 1) {
			holder := d.gate(ctx, snap.values, next, now)
			if holder == "" {
				if err := release(ctx, tx, next); err != nil {
					return err
				}
				d.loads(next.host + "." + next.env)
				continue
			}
			first, seen := d.gated[next.run.ID]
			if !seen {
				first = now
			}
			if now.Sub(first) < GateTimeout {
				gated[next.run.ID] = first // stays queued: the host waits for it
				continue
			}
			reason := fmt.Sprintf("env %s on host %s did not unload its models in %d s", holder, next.host, int(GateTimeout.Seconds()))
			if _, err := finish(ctx, tx, next.run, "failed", &reason); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		d.lost, d.gated = lost, gated // runs held again start over
	}
	return err
}

// gate returns the first other env (by name) of next's host that still holds weights, asking it to unload, or "" when
// the GPU is free for next: every other env's key is gone, lists nothing resident, or answered an unload ok since its
// last run (the hosts watch may lag behind the reply). A stale key still listing resident weights holds them (fails
// closed: a runner cut off from NATS may still have them mapped) until it is deleted or rewritten. One GPU per host;
// the dispatcher's one run per host means the other env is idle.
func (d *Dispatcher) gate(ctx context.Context, values []registry.Host, next placed, now time.Time) string {
	holders := []string{}
	for _, value := range values {
		if value.Host == next.host && value.Env != next.env && len(value.Resident) > 0 {
			holders = append(holders, value.Env)
		}
	}
	slices.Sort(holders)
	d.mu.Lock()
	defer d.mu.Unlock()
	shut := ""
	for _, env := range holders {
		source := next.host + "." + env
		state := d.asks[source]
		if state == nil {
			state = &ask{}
			d.asks[source] = state
		}
		if state.ok {
			continue
		}
		shut = cmp.Or(shut, env)
		if d.Unload == nil || state.inFlight || now.Sub(state.sent) < AskEvery {
			continue
		}
		state.inFlight, state.sent = true, now
		body, _ := json.Marshal(message.Unload{ForEnv: next.env, RunID: next.run.ID})
		log.Printf("dispatcher: asking %s to unload for run %s (env %s)", source, next.run.ID, next.env)
		d.asking.Add(1)
		go d.ask(ctx, source, body, state.gen)
	}
	return shut
}

// loads says a run was released to source: an unload ok it gave before, or gives to a request sent before, no longer
// holds.
func (d *Dispatcher) loads(source string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if state := d.asks[source]; state != nil {
		state.ok = false
		state.gen++
	}
}

// ask sends one unload request to source and records the answer: ok opens its gate until a run is released to it
// (an ok to a request sent before such a release, gen, is ignored); busy, internal, a malformed reply or none within
// UnloadWait leave it shut until the next ask.
func (d *Dispatcher) ask(ctx context.Context, source string, body []byte, gen int) {
	defer d.asking.Done()
	ctx, cancel := context.WithTimeout(ctx, UnloadWait)
	defer cancel()
	raw, err := d.Unload(ctx, "runner.unload."+source, body)
	var reply message.UnloadReply
	if err == nil {
		err = json.Unmarshal(raw, &reply)
	}
	ok := err == nil && reply.UnloadedGB != nil && reply.Code == ""
	if !ok {
		log.Printf("dispatcher: %s did not unload: %v %s %s", source, err, reply.Code, reply.Error)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.asks[source]
	state.inFlight, state.ok = false, ok && state.gen == gen
}

// holds reports whether a released run still holds its host: the host's key is fresh and, from Lease after the
// release, names this run and attempt.
func holds(values []registry.Host, run model.Run, now time.Time) bool {
	i := slices.IndexFunc(values, func(value registry.Host) bool { return value.Host == *run.Host && value.Env == run.Env })
	if i < 0 || !values[i].Live(now) {
		return false
	}
	if now.Sub(*run.ReleasedAt) < Lease {
		return true
	}
	value := values[i]
	attempt := 1
	if value.Attempt != nil {
		attempt = *value.Attempt
	}
	return value.RunID != nil && *value.RunID == run.ID && attempt == run.Attempt
}

// lose ends a lost lease, the run's command already purged: a run asked to cancel ends cancelled, attempt 1 is
// queued again as attempt 2 (the next pass places it, or fails it when no host is eligible), attempt 2 fails.
func lose(ctx context.Context, tx pgx.Tx, run model.Run, now time.Time) error {
	switch {
	case run.Cancelling:
		_, err := finish(ctx, tx, run, "cancelled", nil)
		return err
	case run.Attempt >= 2:
		reason := "runner on host " + *run.Host + " stopped responding; gave up after 2 attempts"
		_, err := finish(ctx, tx, run, "failed", &reason)
		return err
	}
	// attempt 1's outputs go: their takes are deleted (the sweep removes the objects) and let go of (run, index), which
	// attempt 2's outputs take under their own keys
	if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, run.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE takes SET deleted_at = coalesce(deleted_at, clock_timestamp()), run_id = NULL,
		output_idx = NULL WHERE run_id = $1`, run.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM run_outputs WHERE run_id = $1`, run.ID); err != nil {
		return err
	}
	retried, err := one[model.Run](ctx, tx, `UPDATE runs SET status = 'queued', attempt = attempt + 1, host = NULL,
		released_at = NULL, progress = NULL, started_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1 AND status IN ('queued', 'running') RETURNING `+model.RunCols, run.ID)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, retried, "status", now, statusData("queued", nil, nil, retried.Attempt)); err != nil {
		return err
	}
	return runMoved(ctx, tx, retried)
}

// release hands a waiting run to its host: host and released_at set, the command in the outbox with its Image values
// resolved (resolveImages). A run cancelled since the look (no longer waiting) is skipped; one whose reference was
// deleted since it was created fails (input image <take_id> is gone) and is never sent.
func release(ctx context.Context, tx pgx.Tx, next placed) error {
	nodes, gone, err := resolveImages(ctx, tx, next.run.OwnerID, next.run.Nodes)
	if err != nil {
		return fmt.Errorf("run %s: %w", next.run.ID, err)
	}
	if gone != "" {
		reason := "input image " + gone + " is gone"
		_, err := finish(ctx, tx, next.run, "failed", &reason)
		return err
	}
	resolveLoras(nodes, runUsed(next.run.Loras), next.run.OwnerID)
	run, err := one[model.Run](ctx, tx, `UPDATE runs SET host = $2, released_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'queued' AND released_at IS NULL RETURNING `+model.RunCols, next.run.ID, next.host)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dispatch_owners (owner_id, last_released_at) VALUES ($1, $2)
		ON CONFLICT (owner_id) DO UPDATE SET last_released_at = EXCLUDED.last_released_at`, run.OwnerID, run.ReleasedAt); err != nil {
		return err
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: run.ID, OwnerID: run.OwnerID, Attempt: run.Attempt,
		Data: message.Execute{RunID: run.ID, SessionID: run.SessionID, Attempt: run.Attempt,
			Workflow:     message.GraphWorkflow{ID: run.WorkflowID, Nodes: nodes},
			OutputPrefix: OutputPrefix(run), Host: next.host, Env: next.env}})
}

// OutputPrefix is where the run's current attempt writes its outputs: each attempt its own keys, so a lost attempt
// still running never writes over the retry's.
func OutputPrefix(run model.Run) string {
	return common.OwnerPrefix(run.OwnerID) + "runs/" + run.ID + "/" + strconv.Itoa(run.Attempt) + "/"
}

// ------------------------------------------------------------------ the queue as one look sees it

// queued is a waiting run with where it may go: eligible host name → its env, and the hosts with its model resident.
type queued struct {
	run      model.Run
	slots    map[string]string
	resident map[string]bool
}

// stuck is a waiting run with nowhere to go, and why.
type stuck struct {
	run    model.Run
	reason string
}

// placed is a waiting run lined up on a host (and its env) at a position.
type placed struct {
	run      model.Run
	host     string
	env      string
	position int
}

// snapshot is the queue as one look sees it: the released runs not ended, the waiting runs (oldest first) with
// somewhere or nowhere to go, the hosts values, the live host names idle longest first, and when each waiting owner
// last had a run released.
type snapshot struct {
	released []model.Run
	queue    []queued
	stuck    []stuck
	values   []registry.Host
	hosts    []string
	last     map[string]time.Time
}

// look reads the open runs and the history the order needs, in tx (as System: it sees every user's queue).
func look(ctx context.Context, tx pgx.Tx, reg *registry.Registry, now time.Time) (snapshot, error) {
	snap := snapshot{values: reg.Hosts(), last: map[string]time.Time{}}
	open, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE status IN ('queued', 'running')
		ORDER BY created_at, id`)
	if err != nil {
		return snap, err
	}
	// a queued run of a batch asked to pause, or reported paused since its last Resume, is held: no host, no position
	// (still failed with nowhere to go)
	rows, _ := tx.Query(ctx, `SELECT r.id::text FROM runs r JOIN batches b ON b.id = r.batch_id
		WHERE r.status = 'queued' AND r.released_at IS NULL
		AND (b.control = 'pause' OR (b.status = 'paused' AND b.pipeline_seq > b.resumed_seq))`)
	heldIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return snap, err
	}
	held := make(map[string]bool, len(heldIDs))
	for _, id := range heldIDs {
		held[id] = true
	}
	deleted, err := deletedLoras(ctx, tx, open)
	if err != nil {
		return snap, err
	}
	var owners []string
	for _, run := range open {
		if run.ReleasedAt != nil && run.Host != nil {
			snap.released = append(snap.released, run)
			continue
		}
		slots, resident, reason := eligible(reg, snap.values, run, now)
		if name, gone := deleted[run.ID]; gone {
			reason = "LoRA " + name + " was deleted"
		}
		if reason != "" {
			snap.stuck = append(snap.stuck, stuck{run, reason})
			continue
		}
		if held[run.ID] {
			continue
		}
		snap.queue = append(snap.queue, queued{run, slots, resident})
		owners = append(owners, run.OwnerID)
	}
	idle := map[string]time.Time{}
	for _, value := range snap.values {
		if value.Live(now) && !slices.Contains(snap.hosts, value.Host) {
			snap.hosts = append(snap.hosts, value.Host)
		}
	}
	if len(snap.queue) == 0 {
		return snap, nil
	}
	type latest struct {
		Key string
		At  time.Time
	}
	lasts, err := collect[latest](ctx, tx, `SELECT owner_id::text, last_released_at FROM dispatch_owners
		WHERE owner_id = ANY($1::uuid[])`, owners)
	if err != nil {
		return snap, err
	}
	for _, row := range lasts {
		snap.last[row.Key] = row.At
	}
	// ponytail: an aggregate over every run of these hosts; a per-host row if runs pile up.
	ends, err := collect[latest](ctx, tx, `SELECT host, max(finished_at) FROM runs
		WHERE host = ANY($1) AND finished_at IS NOT NULL GROUP BY host`, snap.hosts)
	if err != nil {
		return snap, err
	}
	for _, row := range ends {
		idle[row.Key] = row.At
	}
	slices.SortStableFunc(snap.hosts, func(a, b string) int { // idle longest first (never used first), then by name
		return cmp.Or(idle[a].Compare(idle[b]), strings.Compare(a, b))
	})
	return snap, nil
}

// deletedLoras names, per waiting run, the first own LoRA it was made with that is no longer the owner's ready LoRA
// (deleted since): the run fails at this pass. One query per owner with such runs (as System, explicit owner check).
func deletedLoras(ctx context.Context, tx pgx.Tx, open []model.Run) (map[string]string, error) {
	out, byOwner := map[string]string{}, map[string][]string{}
	for _, run := range open {
		if run.ReleasedAt == nil {
			for _, u := range runUsed(run.Loras) {
				if u.Source == "mine" && !slices.Contains(byOwner[run.OwnerID], u.ID) {
					byOwner[run.OwnerID] = append(byOwner[run.OwnerID], u.ID)
				}
			}
		}
	}
	ready := map[string]bool{} // "<owner> <id>"
	for owner, ids := range byOwner {
		found, err := lora.Ready(ctx, tx, owner, ids)
		if err != nil {
			return nil, err
		}
		for id, ok := range found {
			ready[owner+" "+id] = ok
		}
	}
	for _, run := range open {
		for _, u := range runUsed(run.Loras) {
			if _, named := out[run.ID]; !named && run.ReleasedAt == nil && u.Source == "mine" && !ready[run.OwnerID+" "+u.ID] {
				out[run.ID] = u.Name
			}
		}
	}
	return out, nil
}

// eligible returns where run may go now: each live host with the run's env whose runner publishes its workflow (a user
// workflow's run: every kind of its graph), lists every shared LoRA the run was made with and, for every
// <family>/model node whose family its models key lists, has that variant present (host name → env), and which of
// those hold every such model resident (with the run's baked LoRAs). With none, reason says why.
func eligible(reg *registry.Registry, values []registry.Host, run model.Run, now time.Time) (map[string]string, map[string]bool, string) {
	var graph map[string]registry.Node
	_ = json.Unmarshal(run.Nodes, &graph)
	models := runModels(graph)
	used := runUsed(run.Loras)
	bake := baked(used, graph)
	slots, resident, missing := map[string]string{}, map[string]bool{}, ""
	var lacking []string // the sources that would take the run but for a shared LoRA
hosts:
	for _, value := range values {
		source := value.Host + "." + value.Env
		if value.Env != run.Env || !value.Live(now) {
			continue
		}
		if run.WorkflowVersion == nil && !reg.Publishes(source, run.WorkflowID) {
			continue
		}
		if run.WorkflowVersion != nil && !reg.PublishesKinds(source, kinds(graph)) {
			continue // a user workflow: the runner must publish every kind of its snapshot
		}
		if slices.ContainsFunc(used, func(u lora.Used) bool { return u.Source == "shared" && !reg.HasLora(source, u.ID) }) {
			lacking = append(lacking, source)
			continue
		}
		all := len(models) > 0
		for _, m := range models {
			model, listed := reg.Model(source, m.family)
			if !listed {
				all = false
				continue
			}
			if !model.Has(m.variant) {
				missing = cmp.Or(missing, fmt.Sprintf("model %s variant %s", m.family, m.variant))
				continue hosts
			}
			all = all && slices.ContainsFunc(value.Resident, func(r registry.Resident) bool {
				return r.Model == model.ID && r.Variant == m.variant && r.Precision == m.precision && sameBaked(r.Loras, bake)
			})
		}
		resident[value.Host] = all
		slots[value.Host] = value.Env
	}
	switch {
	case len(slots) > 0:
		return slots, resident, ""
	case missing != "":
		return nil, nil, fmt.Sprintf("%s is not present on any live host for env %s", missing, run.Env)
	case len(lacking) > 0: // name the first shared LoRA none of them has (else the first one some lack)
		at := slices.IndexFunc(used, func(u lora.Used) bool {
			return u.Source == "shared" && !slices.ContainsFunc(lacking, func(source string) bool { return reg.HasLora(source, u.ID) })
		})
		if at < 0 {
			at = slices.IndexFunc(used, func(u lora.Used) bool { return u.Source == "shared" && !reg.HasLora(lacking[0], u.ID) })
		}
		return nil, nil, "no live host for env " + run.Env + " has LoRA " + used[at].Name
	}
	return nil, nil, "no live host for env " + run.Env
}

// runModel is what a <family>/model node of a run loads.
type runModel struct{ family, variant, precision string }

// runModels lists the graph's <family>/model nodes by node id.
func runModels(graph map[string]registry.Node) []runModel {
	var out []runModel
	for _, id := range slices.Sorted(maps.Keys(graph)) {
		if family, found := strings.CutSuffix(graph[id].Kind, "/model"); found {
			variant, _ := graph[id].Params["variant"].(string)
			precision, _ := graph[id].Params["precision"].(string)
			out = append(out, runModel{family, variant, precision})
		}
	}
	return out
}

// lineup orders waiting runs (oldest first) onto hosts the way the dispatcher releases them. Hosts take turns in
// the given order (idle longest first), each taking at most one run per round; rounds 0 runs until every run is
// placed. A host takes the next run of pick, but a run whose model is resident on another host still free this
// round goes there and the first host picks again. last is each owner's last release; ahead[h] counts runs already
// before the line on h (released, not started).
func lineup(hosts []string, waiting []queued, last map[string]time.Time, ahead map[string]int, rounds int) []placed {
	last, position, left := maps.Clone(last), maps.Clone(ahead), slices.Clone(waiting)
	if last == nil {
		last = map[string]time.Time{}
	}
	if position == nil {
		position = map[string]int{}
	}
	clock := time.Time{} // a simulated release time later than every real one
	for _, at := range last {
		if at.After(clock) {
			clock = at
		}
	}
	var out []placed
	for round := 0; rounds == 0 || round < rounds; round++ {
		used, before := map[string]bool{}, len(out)
		for i := 0; i < len(hosts); i++ {
			host := hosts[i]
			k := -1
			if !used[host] {
				k = pick(host, left, last)
			}
			if k < 0 {
				continue
			}
			next := left[k]
			target := host
			if !next.resident[host] {
				if j := slices.IndexFunc(hosts, func(o string) bool { return !used[o] && next.resident[o] }); j >= 0 {
					target = hosts[j]
				}
			}
			used[target] = true
			position[target]++
			out = append(out, placed{run: next.run, host: target, env: next.slots[target], position: position[target]})
			clock = clock.Add(time.Nanosecond)
			last[next.run.OwnerID] = clock
			left = slices.Delete(left, k, k+1)
			if target != host {
				i-- // this host has not taken a run yet
			}
		}
		if len(out) == before {
			break
		}
	}
	return out
}

// pick returns the index in left (oldest first) of the run host takes next, or -1: interactive before batch, then the
// owner whose last release is the oldest (never released first; a tie goes to the owner with the older oldest run),
// then that owner's oldest run whose model is resident on host, else their oldest.
func pick(host string, left []queued, last map[string]time.Time) int {
	best := -1
	for i, candidate := range left {
		if _, ok := candidate.slots[host]; !ok {
			continue
		}
		if best < 0 || goesFirst(candidate, left[best], host, last) {
			best = i
		}
	}
	return best
}

// goesFirst reports whether a goes before b on host, b being older or of an owner whose oldest run is older.
func goesFirst(a, b queued, host string, last map[string]time.Time) bool {
	if a.run.Lane != b.run.Lane {
		return a.run.Lane == "interactive"
	}
	if a.run.OwnerID != b.run.OwnerID {
		return last[a.run.OwnerID].Before(last[b.run.OwnerID])
	}
	return a.resident[host] && !b.resident[host]
}

// ------------------------------------------------------------------ the queue as users see it

// lineupNow lines up every waiting run on the live hosts, all rounds, as the dispatcher would release them (by run
// id), and says which hosts have a released run not ended. It reads every user's queue as System: callers show only
// their own runs and counts of others'. ponytail: rebuilt on every read, O(n^2) in the waiting runs (per user n is
// bounded by MaxOpenRuns interactive runs plus MaxOpenBatches × the pipelines engine's WINDOW batch rows); one line-up
// per pass shared with readers if users grow large.
func (s *Service) lineupNow(ctx context.Context) (map[string]placed, map[string]bool, error) {
	at, busy := map[string]placed{}, map[string]bool{}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		snap, err := look(ctx, tx, s.registry, time.Now())
		if err != nil {
			return err
		}
		ahead := map[string]int{}
		for _, run := range snap.released {
			busy[*run.Host] = true
			if run.Status == "queued" { // sent, not started: first in its host's line
				ahead[*run.Host]++
				at[run.ID] = placed{run: run, host: *run.Host, env: run.Env, position: ahead[*run.Host]}
			}
		}
		for _, next := range lineup(snap.hosts, snap.queue, snap.last, ahead, 0) {
			at[next.run.ID] = next
		}
		return nil
	})
	return at, busy, err
}

// placeQueued sets host and position on each queued run of runs from the line-up; a queued run with nowhere to go
// gets neither (the next pass fails it).
func (s *Service) placeQueued(ctx context.Context, runs ...*dto.Run) error {
	if !slices.ContainsFunc(runs, func(run *dto.Run) bool { return run.Status == "queued" }) {
		return nil
	}
	at, _, err := s.lineupNow(ctx)
	if err != nil {
		return err
	}
	fill(runs, at)
	return nil
}

// fill sets host and position on the queued runs from at.
func fill(runs []*dto.Run, at map[string]placed) {
	for _, run := range runs {
		if run.Status != "queued" {
			continue
		}
		run.Host, run.Position = nil, nil
		if next, ok := at[run.ID]; ok {
			run.Host, run.Position = &next.host, &next.position
		}
	}
}

// Queue returns the caller's queued and running runs across sessions (running first, then queued by host and
// position) and, per host name in the KV hosts, its state and how many runs of other users are lined up for it.
func (s *Service) Queue(ctx context.Context, caller common.Caller) (dto.Queue, error) {
	out := dto.Queue{Runs: []dto.Run{}, Hosts: []dto.QueueHost{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		runs, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE status IN ('queued', 'running')
			ORDER BY created_at, id`)
		for _, run := range runs {
			if err != nil {
				break
			}
			var mapped dto.Run
			mapped, err = s.withOutputs(ctx, tx, run)
			out.Runs = append(out.Runs, mapped)
		}
		return err
	})
	if err != nil {
		return out, err
	}
	at, busy, err := s.lineupNow(ctx)
	if err != nil {
		return out, err
	}
	pointers := make([]*dto.Run, len(out.Runs))
	for i := range out.Runs {
		pointers[i] = &out.Runs[i]
	}
	fill(pointers, at)
	slices.SortStableFunc(out.Runs, func(a, b dto.Run) int {
		return cmp.Or(cmp.Compare(b.Status, a.Status), // running before queued
			cmp.Compare(deref(a.Host), deref(b.Host)), cmp.Compare(deref(a.Position), deref(b.Position)))
	})
	now := time.Now()
	for _, value := range s.registry.Hosts() {
		i := slices.IndexFunc(out.Hosts, func(host dto.QueueHost) bool { return host.Host == value.Host })
		if i < 0 {
			out.Hosts = append(out.Hosts, dto.QueueHost{Host: value.Host, State: "offline"})
			i = len(out.Hosts) - 1
		}
		if value.Live(now) && out.Hosts[i].State == "offline" {
			out.Hosts[i].State = "idle"
		}
	}
	for i, host := range out.Hosts {
		if busy[host.Host] && host.State != "offline" {
			out.Hosts[i].State = "busy"
		}
		for _, next := range at {
			if next.host == host.Host && next.run.OwnerID != caller.ID {
				out.Hosts[i].OthersQueued++
			}
		}
	}
	return out, nil
}

// deref is *p, or the zero value for nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
