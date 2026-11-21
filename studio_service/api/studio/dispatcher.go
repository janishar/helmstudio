package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
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

	// The hand-over (contracts/README.md "Two envs on one host: the hand-over"): an env holding victims is asked to
	// unload them at most every AskEvery, each ask waits UnloadWait for its reply, and a run that still does not fit
	// GateTimeout after it first waited fails.
	AskEvery    = 10 * time.Second
	UnloadWait  = 60 * time.Second
	GateTimeout = 120 * time.Second

	// The fairness bound of resident-first order: a user's head that needs a swap goes once passed over SwapSkips
	// times, or SwapWait after it was first passed over (check.py SWAP_SKIPS, SWAP_WAIT_S).
	SwapSkips = 4
	SwapWait  = 180 * time.Second
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
	// Unload sends a core NATS request (runner.unload.<host>.<env>) and returns the reply; nil: never asked (a run
	// that does not fit then waits for the hosts watch, and fails after GateTimeout).
	Unload func(ctx context.Context, subject string, body []byte) ([]byte, error)
	waited map[string]time.Time // run id → when it first waited for room (in memory: a restart starts over)
	mu     sync.Mutex           // guards asks and skips: replies arrive on their own goroutines, readers on theirs
	asks   map[string]*ask      // "<host>.<env>" asked to unload → the state of its asks
	skips  map[string]skip      // queued run id → how often resident-first order passed it over
	asking sync.WaitGroup       // the asks in flight (tests wait for them)
	admin  []*adminAsk          // the admin's unloads not answered yet (API 1.16, dispatcher_admin.go)
}

// ask is the state of the unload requests to one env: whether one is in flight, when the last went, and gen, bumped by
// every release to it: a reply to a request sent before a release says nothing about what that run loads. ok: an ok
// reply of the current generation, whose resident stands for the env's key until the key is rewritten after at (when
// it arrived) or a run is released to it.
type ask struct {
	inFlight, ok bool
	sent, at     time.Time
	gen          int
	resident     []registry.Resident
}

// NewDispatcher returns the dispatcher.
func NewDispatcher(pool *pgxpool.Pool, registry *registry.Registry, purge func(ctx context.Context, subject string) error,
	backlog func(ctx context.Context) (int, error)) *Dispatcher {
	return &Dispatcher{pool: pool, registry: registry, purge: purge, backlog: backlog, lost: map[string]time.Time{},
		waited: map[string]time.Time{}, asks: map[string]*ask{}, skips: map[string]skip{}}
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
// waiting run with no eligible host fails; then each free host takes the next run in the fair order, released when
// the host's memory admits it (the hand-over), else the host waits for it. Nothing happens until the registry has
// loaded the KV's current values.
func (d *Dispatcher) Pass(ctx context.Context, now time.Time) error {
	if !d.registry.Ready() { // an empty view would read every host as gone: nothing to decide yet
		return nil
	}
	lost, waited := map[string]time.Time{}, map[string]time.Time{}
	var snap snapshot
	decided := map[string][]string{} // run id released or failed → the heads its pick passed over
	err := common.System(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('studio.dispatcher'))`); err != nil {
			return err
		}
		var err error
		if snap, err = look(ctx, tx, d.registry, now); err != nil {
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
		running := map[string]bool{}
		for _, run := range snap.released {
			running[*run.Host+"."+run.Env] = true
		}
		for host := range d.adminPass(running, busy, now) { // an admin unload not answered holds its host
			busy[host] = true
		}
		// Several hosts (contracts/README.md): line up every queued run, walk the free hosts by name, each picking
		// among the runs lined up on it; after a release, a fail or a wait line up again and walk again, until a walk
		// decides nothing. A host whose pick waits is busy for the rest of this pass.
		skips, clock := d.skipState(), now
		for _, at := range snap.last {
			if at.After(clock) { // a simulated turn later than every real one
				clock = at
			}
		}
		for decidedOne := true; decidedOne; {
			decidedOne = false
			d.lineUp(d.registry, snap, busy, now)
			for _, host := range snap.hosts {
				if busy[host] {
					continue
				}
				k, passed := pick(host, snap.queue, snap.last, skips, now)
				if k < 0 {
					continue
				}
				q := snap.queue[k]
				next := placed{run: q.run, host: host, env: q.slots[host], passed: passed}
				got := d.handover(ctx, snap, next, now)
				reason := got.err
				if got.decision != "release" && got.decision != "fail" {
					first, seen := d.waited[next.run.ID]
					if !seen {
						first = now
					}
					if now.Sub(first) < GateTimeout {
						waited[next.run.ID] = first // stays queued: the host waits for it, busy for this pass
						busy[host] = true
						snap.queue = slices.Delete(snap.queue, k, k+1)
						decidedOne = true // line up again: its other runs may go to a free host
						break
					}
					reason = fmt.Sprintf("env %s on host %s did not unload its models in %d s", got.holder, next.host, int(GateTimeout.Seconds()))
				}
				if got.decision == "release" {
					if err := release(ctx, tx, next, got.budget); err != nil {
						return err
					}
					d.loads(next.host + "." + next.env)
					busy[host] = true
				} else if err := failTurn(ctx, tx, next.run, reason); err != nil {
					return err
				}
				decided[next.run.ID] = next.passed
				passOver(skips, next.run.ID, passed, now)
				clock = clock.Add(time.Nanosecond) // its owner's turn, for the next host's pick
				snap.last[next.run.OwnerID] = clock
				snap.queue = slices.Delete(snap.queue, k, k+1)
				decidedOne = true
				break
			}
		}
		return nil
	})
	if err == nil {
		d.lost, d.waited = lost, waited // runs held again start over
		d.passOver(decided, snap, now)
	}
	return err
}

// handover decides next's admission on its host (admit) from the host's present keys, an env's standing ok reply in
// place of its key's resident, next's plan and what the host's other queued runs need, and asks each env holding
// victims to unload them. A stale key counts (fails closed: a runner cut off from NATS may still have its weights
// mapped) and is never asked; a deleted key holds nothing.
func (d *Dispatcher) handover(ctx context.Context, snap snapshot, next placed, now time.Time) admission {
	d.mu.Lock()
	defer d.mu.Unlock()
	got := d.decide(snap, next, now)
	for _, env := range slices.Sorted(maps.Keys(got.unload)) {
		d.ask(ctx, next, env, got.unload[env], now)
	}
	return got
}

// pinnedVictims are the pinned entries handover would ask to unload for next now (Run.unloads_pinned); a nil
// dispatcher decides from the hosts values alone (no unload replies standing).
func (d *Dispatcher) pinnedVictims(reg *registry.Registry, snap snapshot, next placed, now time.Time) []dto.PinnedUnload {
	if d == nil {
		d = &Dispatcher{registry: reg, asks: map[string]*ask{}}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []dto.PinnedUnload
	got := d.decide(snap, next, now)
	for _, env := range slices.Sorted(maps.Keys(got.unload)) {
		for _, v := range got.unload[env] {
			if v.res.Pinned {
				out = append(out, dto.PinnedUnload{Env: v.env, Component: v.res.Component, Model: v.res.Model,
					Variant: v.res.Variant, Precision: v.res.Precision, GB: v.res.GB})
			}
		}
	}
	return out
}

// decide is next's admission on its host, without asking anything (d.mu held).
func (d *Dispatcher) decide(snap snapshot, next placed, now time.Time) admission {
	if next.env == "server" { // a local server's call loads nothing: its memory is reserved already (API 1.15)
		return admission{decision: "release"}
	}
	var entries []entry
	running := ""
	budget, ram := budgets(snap.values, next.host)
	for _, value := range snap.values {
		if value.Host != next.host {
			continue
		}
		if value.RunID != nil && (running == "" || value.Env < running) {
			running = value.Env // its run may still be using up to its budget_gb: the next write decides
		}
		resident, standing := value.Resident, false
		if state := d.asks[value.Host+"."+value.Env]; state != nil && state.ok {
			if value.UpdatedAt.After(state.at) {
				state.ok = false // the key was rewritten since: it speaks for itself
			} else {
				resident, standing = state.resident, true
			}
		}
		entries = append(entries, keyEntries(value.Env, resident, value.Live(now) && value.RunID == nil,
			measured(value, standing))...)
	}
	if budget < 0 {
		return admission{decision: "release"} // no key reports memory (the fake host): no admission
	}
	budget -= snap.reserved[next.host] // enabled local servers' declared memory is outside the app's control
	referenced := map[string]bool{}
	for _, q := range snap.queue {
		if _, ok := q.slots[next.host]; ok && q.run.ID != next.run.ID {
			for _, key := range planOf(d.registry, next.host, q.run).needs {
				referenced[q.run.Env+" "+key] = true
			}
		}
	}
	p := planOf(d.registry, next.host, next.run)
	return admit(next.host, entries, budget, next.env, p, referenced, running, ram)
}

// budgets are host's B and R in hundredths: the smallest memory.budget_gb and host_ram.budget_gb among its present
// keys (-1: no key reports one; the fake host reports neither, so no admission).
func budgets(values []registry.Host, host string) (int, int) {
	budget, ram := -1, -1
	for _, value := range values {
		if value.Host != host {
			continue
		}
		if value.Memory != nil && (budget < 0 || max(0, hundredths(value.Memory.BudgetGB)) < budget) {
			budget = max(0, hundredths(value.Memory.BudgetGB)) // a negative budget is none left, never "no key"
		}
		if value.HostRAM != nil && (ram < 0 || max(0, hundredths(value.HostRAM.BudgetGB)) < ram) {
			ram = max(0, hundredths(value.HostRAM.BudgetGB))
		}
	}
	return budget, ram
}

// ask sends env of next's host one unload request naming victims, unless one is in flight or the last went less than
// AskEvery ago (d.mu held).
func (d *Dispatcher) ask(ctx context.Context, next placed, env string, victims []entry, now time.Time) {
	source := next.host + "." + env
	state := d.asks[source]
	if state == nil {
		state = &ask{}
		d.asks[source] = state
	}
	if d.Unload == nil || state.inFlight || now.Sub(state.sent) < AskEvery {
		return
	}
	request := message.Unload{ForEnv: next.env, RunID: next.run.ID}
	for _, v := range victims {
		request.Components = append(request.Components, v.component())
	}
	body, _ := json.Marshal(request)
	state.inFlight, state.sent = true, now
	log.Printf("dispatcher: asking %s to unload %d components for run %s (env %s)", source, len(victims), next.run.ID, next.env)
	d.asking.Add(1)
	go d.reply(ctx, source, body, state.gen)
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

// reply sends one unload request to source and records the answer: an ok of generation gen (no release to source
// since it was sent) stands with its resident; an older ok, busy, internal, a malformed reply or none within
// UnloadWait change nothing until the next ask.
func (d *Dispatcher) reply(ctx context.Context, source string, body []byte, gen int) {
	defer d.asking.Done()
	ctx, cancel := context.WithTimeout(ctx, UnloadWait)
	defer cancel()
	raw, err := d.Unload(ctx, "runner.unload."+source, body)
	var reply message.UnloadReply
	if err == nil {
		err = json.Unmarshal(raw, &reply)
	}
	ok := err == nil && reply.UnloadedGB != nil && reply.Resident != nil && reply.Code == ""
	if !ok {
		log.Printf("dispatcher: %s did not unload: %v %s %s", source, err, reply.Code, reply.Error)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.asks[source]
	state.inFlight = false
	if ok && state.gen == gen {
		state.ok, state.at, state.resident = true, time.Now(), *reply.Resident
	}
}

// skipState is a copy of the skip counts (nil dispatcher: none).
func (d *Dispatcher) skipState() map[string]skip {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return maps.Clone(d.skips)
}

// passOver applies a committed pass's decided picks to the skip counts and drops every run no longer waiting: a run
// held (its batch paused) is still waiting and keeps its count.
func (d *Dispatcher) passOver(decided map[string][]string, snap snapshot, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, passed := range decided {
		passOver(d.skips, id, passed, now)
	}
	maps.DeleteFunc(d.skips, func(id string, _ skip) bool {
		return !snap.held[id] && !slices.ContainsFunc(snap.queue, func(q queued) bool { return q.run.ID == id })
	})
}

// ------------------------------------------------------------------ host memory admission

// entry is one resident item of a present hosts key: its env, identity (entryKey), the item, its gb in hundredths and
// whether its key is fresh.
type entry struct {
	env   string
	key   string
	res   registry.Resident
	gb    int
	fresh bool
}

// hundredths is gb in integer hundredths (the hand-over's arithmetic), rounding half to even as check.py does.
func hundredths(gb float64) int { return int(math.RoundToEven(gb * 100)) }

// entryKey identifies a resident entry or a planned component: component, model, variant, precision and its baked
// LoRAs (lora_id and strength, in order; none = the plain component).
func entryKey(component, model, variant, precision string, loras []registry.ResidentLora) string {
	key := component + " " + model + " " + variant + " " + precision
	for _, l := range loras {
		key += fmt.Sprintf(" %s@%v", deref(l.LoraID), deref(l.Strength))
	}
	return key
}

// newEntry is r of env's key.
func newEntry(env string, r registry.Resident, fresh bool) entry {
	return entry{env: env, key: entryKey(r.Component, r.Model, r.Variant, r.Precision, r.Loras), res: r, gb: hundredths(r.GB), fresh: fresh}
}

// keyEntries are the entries of one present key of env holding resident: fresh only when the key is live and names no
// run (a key still naming a run is older than that run's end: its pins and sizes wait for the next write); measured
// (hundredths, -1: none) beyond what the entries declare is one more counted entry that is never a victim (device
// caches, activations: the runner frees them when it unloads).
func keyEntries(env string, resident []registry.Resident, fresh bool, measured int) []entry {
	var out []entry
	declared := 0
	for _, r := range resident {
		if !registry.OnDevice(r.Device) {
			continue // in host RAM (API 1.18): not an entry of the memory budget, never a victim
		}
		e := newEntry(env, r, fresh)
		out, declared = append(out, e), declared+e.gb
	}
	if measured > declared {
		out = append(out, entry{env: env, key: "(measured)", res: registry.Resident{Component: "(measured)"}, gb: measured - declared})
	}
	return out
}

// measured is the key's memory.used_gb in hundredths when it counts beyond its entries: a key not stood for by an ok
// reply (whose resident is newer than the key's used_gb); else -1. The run's own env counts too (admit: it may make
// room in other envs, never make the run wait): a plan's peak is above its components' declared sizes, not above
// what its runner measures holding them.
func measured(value registry.Host, standing bool) int {
	if value.Memory == nil || standing {
		return -1
	}
	return hundredths(value.Memory.UsedGB)
}

// held is what a key's runner holds in hundredths: its device entries' gb, or its measured used_gb when larger.
func held(value registry.Host) int {
	declared := 0
	for _, r := range value.Resident {
		if registry.OnDevice(r.Device) {
			declared += hundredths(r.GB)
		}
	}
	if value.Memory != nil {
		return max(declared, hundredths(value.Memory.UsedGB))
	}
	return declared
}

// component names the entry in an unload request.
func (e entry) component() message.Component {
	out := message.Component{Component: e.res.Component, Model: e.res.Model, Variant: e.res.Variant, Precision: e.res.Precision}
	for _, l := range e.res.Loras {
		out.Loras = append(out.Loras, message.ComponentLora{LoraID: deref(l.LoraID), Strength: deref(l.Strength)})
	}
	return out
}

// plan is what a run loads on a host: its needs (entry keys of its env, device components only) and its planned peak
// in hundredths (-1: no plan, which admit plans at the whole budget); ramPeak is its plan's ram_peak_gb in hundredths
// (-1: none, not checked).
type plan struct {
	needs   []string
	peak    int
	ramPeak int
}

// planOf reads run's plan in <host>.<env>.models: its <family>/model node's variant's memory item for the node's
// precision, the family's lora component carrying the run's baked LoRAs; the peak adds the run's LoRA files.
func planOf(reg *registry.Registry, host string, run model.Run) plan {
	out := plan{peak: -1, ramPeak: -1}
	var graph map[string]registry.Node
	_ = json.Unmarshal(run.Nodes, &graph)
	models := runModels(graph)
	if reg == nil || len(models) == 0 { // a workflow is one family: its first model node
		return out
	}
	found, ok := reg.Model(host+"."+run.Env, models[0].family)
	if !ok {
		return out
	}
	memory, ok := found.Plan(models[0].variant, found.RunsAt(models[0].variant, models[0].precision), run.WorkflowID)
	if !ok {
		return out
	}
	larger := len(models) > 1 || memory.PlannedAt != nil && beyond(graph, *memory.PlannedAt)
	var media registry.PlannedAt // API 1.19: a media source larger than the plan's planned_at
	if at := memory.PlannedAt; at != nil && json.Unmarshal(run.MediaAt, &media) == nil &&
		(media.MP > at.MP+0.001 || media.Frames > at.Frames) {
		larger = true
	}
	if at := memory.PlannedAt; at != nil && at.Images != nil && run.Thumbs != nil && *run.Thumbs > *at.Images {
		larger = true // more thumbnails than the ask plan holds for
	}
	used := runUsed(run.Loras)
	bake := baked(used, graph)
	if memory.RAMPeakGB != nil {
		out.ramPeak = hundredths(*memory.RAMPeakGB)
	}
	for _, c := range memory.Components {
		if !registry.OnDevice(c.Device) {
			continue // kept in host RAM: inside ram_peak_gb, not a device need
		}
		var loras []registry.ResidentLora
		if found.Lora != nil && c.Component == found.Lora.Component {
			loras = bake
		}
		out.needs = append(out.needs, entryKey(c.Component, found.ID, c.Variant, c.Precision, loras))
	}
	var bytes int64
	seen := map[string]bool{}
	for _, u := range used {
		if !seen[u.ID] {
			seen[u.ID], bytes = true, bytes+u.Bytes
		}
	}
	if !larger { // larger than its plan: planned at the whole budget, its own entries still reused
		out.peak = hundredths(memory.PeakGB + float64(bytes)/(1<<30))
	}
	return out
}

// beyond reports whether a graph is larger than at: some node's width × height / 10^6 or numeric frames above it
// (canvas auto with references is about 1 MP, so its width × height overstates it: fail safe).
func beyond(graph map[string]registry.Node, at registry.PlannedAt) bool {
	for _, node := range graph {
		w, wok := node.Params["width"].(float64)
		h, hok := node.Params["height"].(float64)
		frames, fok := node.Params["frames"].(float64) // ponytail: "auto" is planned at its longest clip, at the default fps
		if wok && hok && w*h/1e6 > at.MP+0.001 || fok && frames > float64(at.Frames) {
			return true
		}
	}
	return false
}

// admission is one decision for a run picked for a host: release (budget: the GB its env may hold, nil when no key of
// the host reports memory), unload (env → its victims in order; the run waits), wait (nothing to ask), or fail (err).
// holder is the first env by name holding a victim or an offline entry (the timeout's message).
type admission struct {
	decision string
	unload   map[string][]entry
	budget   *float64
	err      string
	holder   string
}

// admit is the host memory admission (contracts/README.md "Two envs on one host: the hand-over", check.py admit): a
// run of env with planned peak (-1: the budget less other envs' measured excess) and needs (entry keys) on a host of budget (hundredths) holding
// entries; referenced holds "<env> <key>" of what the host's other queued runs need; running is the first env by name
// whose key still names a run ("": none): that run may still use up to the budget_gb it was released with, more than
// its key declares, so the run waits (nothing asked) until that key's next write. ram is the host's host-RAM budget
// (-1: none): a run whose ram_peak_gb exceeds it fails at once, after the device check; no victims are taken for it.
func admit(host string, entries []entry, budget int, env string, run plan, referenced map[string]bool,
	running string, ram int) admission {
	p, needs := run.peak, run.needs
	if p < 0 {
		p = budget
	}
	var counted []entry
	c, own := 0, 0
	for _, e := range entries {
		if e.env == env && slices.Contains(needs, e.key) {
			continue // reused: inside the peak
		}
		counted = append(counted, e)
		c += e.gb
		if e.env == env && e.key == "(measured)" {
			own += e.gb // the run's own runner measured beyond its entries
		} else if run.peak < 0 && e.key == "(measured)" {
			p -= e.gb // P = B takes what it can get: another env's measured excess (never freed) lowers it, never holds it
		}
	}
	if why := tooBig(host, p, budget, run.ramPeak, ram); why != "" { // a pin never fails a run: pins are its last victims
		return admission{decision: "fail", err: why}
	}
	if running != "" {
		return admission{decision: "wait", holder: running}
	}
	released := func() admission {
		others := 0
		for _, e := range entries {
			if e.env != env {
				others += e.gb
			}
		}
		left := float64(budget-others) / 100
		return admission{decision: "release", budget: &left}
	}
	if c+p <= budget {
		return released()
	}
	tier := func(e entry) int { // an unreferenced baked set first
		if len(e.res.Loras) > 0 && !referenced[e.env+" "+e.key] {
			return 0
		}
		return 1
	}
	var victims []entry
	holders := []string{}
	for _, e := range counted {
		if e.fresh && e.res.PinnedWhy != "in_use" {
			victims = append(victims, e)
		} else if !e.fresh && !slices.Contains(holders, e.env) && (e.env != env || e.key != "(measured)") {
			holders = append(holders, e.env)
		}
	}
	slices.SortStableFunc(victims, func(a, b entry) int { // pinned ones last: taken only when nothing else fits
		return cmp.Or(cmp.Compare(btoi(a.res.Pinned), btoi(b.res.Pinned)), cmp.Compare(tier(a), tier(b)), cmp.Compare(btoi(a.res.LastUsed != nil), btoi(b.res.LastUsed != nil)),
			deref(a.res.LastUsed).Compare(deref(b.res.LastUsed)), cmp.Compare(b.gb, a.gb), strings.Compare(a.env, b.env),
			strings.Compare(a.res.Component, b.res.Component), strings.Compare(a.res.Model, b.res.Model),
			strings.Compare(a.res.Variant, b.res.Variant), strings.Compare(a.res.Precision, b.res.Precision))
	})
	unload := map[string][]entry{}
	for _, v := range victims {
		if c+p <= budget {
			break
		}
		unload[v.env] = append(unload[v.env], v)
		c -= v.gb
		if !slices.Contains(holders, v.env) {
			holders = append(holders, v.env)
		}
	}
	if len(unload) == 0 && c-own+p <= budget {
		return released() // only the run's own measured excess is in the way: its runner drops its caches itself
	}
	slices.Sort(holders)
	got := admission{decision: "wait"}
	if len(holders) > 0 {
		got.holder = holders[0]
	}
	if len(unload) > 0 {
		got.decision, got.unload = "unload", unload
	}
	return got
}

// tooBig is why a run of planned peak p (and ram_peak, -1: none) can never run on host (budget and ram in hundredths,
// ram -1: none), or "".
func tooBig(host string, p, budget, ramPeak, ram int) string {
	switch {
	case p > budget:
		return fmt.Sprintf("needs %.1f GB at its peak; host %s has %.1f GB", float64(p)/100, host, float64(budget)/100)
	case ram >= 0 && ramPeak > ram:
		return fmt.Sprintf("needs %.1f GB of host RAM at its peak; host %s has %.1f GB", float64(ramPeak)/100, host, float64(ram)/100)
	}
	return ""
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
// queued again as attempt 2 with its host as lost_host (the next pass lines it up on another eligible host, on the
// same one only when it is the only one, or fails it when none is eligible), attempt 2 fails.
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
	// the run moves first, under its row lock (the projector's FOR UPDATE waits for it, then drops attempt 1's events):
	// a run the projector ended since the look keeps its outputs and is not retried
	retried, err := one[model.Run](ctx, tx, `UPDATE runs SET status = 'queued', attempt = attempt + 1, lost_host = host, host = NULL,
		released_at = NULL, progress = NULL, started_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1 AND status IN ('queued', 'running') RETURNING `+model.RunCols, run.ID)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
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
	if err := appendEvent(ctx, tx, retried, "status", now, statusData("queued", nil, nil, retried.Attempt)); err != nil {
		return err
	}
	return runMoved(ctx, tx, retried)
}

// release hands a waiting run to its host: host and released_at set, the command in the outbox with its Image values
// resolved (resolveImages) and budget_gb from the admission. A run cancelled since the look (no longer waiting) is skipped; one whose reference was
// deleted since it was created fails (input image <take_id> is gone; a video or audio take: input take <take_id> is
// gone) and is never sent.
func release(ctx context.Context, tx pgx.Tx, next placed, budget *float64) error {
	nodes, gone, err := resolveImages(ctx, tx, next.run.OwnerID, next.run.Nodes)
	if err != nil {
		return fmt.Errorf("run %s: %w", next.run.ID, err)
	}
	if gone != "" {
		what := "image" // a video or audio take (Media inputs) is named media-neutrally; a swept row reads as an image
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM takes WHERE id = $1`, gone).Scan(&kind); err == nil && kind != "image" {
			what = "take"
		}
		reason := "input " + what + " " + gone + " is gone"
		_, err := finish(ctx, tx, next.run, "failed", &reason)
		return err
	}
	resolveLoras(nodes, runUsed(next.run.Loras), next.run.OwnerID)
	sendVoices(nodes, next.run.Mentions) // API 1.30: the voices as resolved when the run was made
	run, err := one[model.Run](ctx, tx, `UPDATE runs SET host = $2, released_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'queued' AND released_at IS NULL RETURNING `+model.RunCols, next.run.ID, next.host)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if err := turn(ctx, tx, run.OwnerID); err != nil {
		return err
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: run.ID, OwnerID: run.OwnerID, Attempt: run.Attempt,
		Data: message.Execute{RunID: run.ID, SessionID: run.SessionID, Attempt: run.Attempt,
			Workflow:     message.GraphWorkflow{ID: run.WorkflowID, Nodes: nodes},
			OutputPrefix: OutputPrefix(run), BudgetGB: budget, Host: next.host, Env: next.env}})
}

// turn stamps owner's last turn in round-robin (dispatch_owners) at the transaction's clock.
func turn(ctx context.Context, tx pgx.Tx, owner string) error {
	_, err := tx.Exec(ctx, `INSERT INTO dispatch_owners (owner_id, last_released_at) VALUES ($1, clock_timestamp())
		ON CONFLICT (owner_id) DO UPDATE SET last_released_at = EXCLUDED.last_released_at`, owner)
	return err
}

// failTurn fails a run the order picked (admission or its time limit): a turn, so its owner's next run waits for the
// other users' heads as after a release.
func failTurn(ctx context.Context, tx pgx.Tx, run model.Run, reason string) error {
	if _, err := finish(ctx, tx, run, "failed", &reason); err != nil {
		return err
	}
	return turn(ctx, tx, run.OwnerID)
}

// OutputPrefix is where the run's current attempt writes its outputs: each attempt its own keys, so a lost attempt
// still running never writes over the retry's.
func OutputPrefix(run model.Run) string {
	return common.OwnerPrefix(run.OwnerID) + "runs/" + run.ID + "/" + strconv.Itoa(run.Attempt) + "/"
}

// ------------------------------------------------------------------ the queue as one look sees it

// queued is a waiting run with where it may go: eligible host name → its env, the hosts with its model resident, and
// the host it is lined up on (lineUp).
type queued struct {
	run      model.Run
	slots    map[string]string
	resident map[string]bool
	host     string
}

// stuck is a waiting run with nowhere to go, and why.
type stuck struct {
	run    model.Run
	reason string
}

// placed is a waiting run lined up on a host (and its env) at a position, and the heads its pick passed over.
type placed struct {
	run      model.Run
	host     string
	env      string
	position int
	passed   []string
	pins     []dto.PinnedUnload // read only: the pinned entries admitting it now would unload
}

// snapshot is the queue as one look sees it: the released runs not ended, the waiting runs (oldest first) with
// somewhere or nowhere to go, the hosts values, the live host names by name and when each last ended a run (none:
// never), and when each waiting owner last had a run released.
type snapshot struct {
	released []model.Run
	queue    []queued
	stuck    []stuck
	values   []registry.Host
	hosts    []string
	idle     map[string]time.Time
	last     map[string]time.Time
	held     map[string]bool // waiting runs of a paused batch: no position, skip counts kept
	reserved map[string]int  // host → hundredths of a GB its enabled local servers reserve (API 1.15)
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
	snap.held = held
	if snap.reserved, err = reservedOn(ctx, tx); err != nil {
		return snap, err
	}
	deleted, err := deletedLoras(ctx, tx, open)
	if err != nil {
		return snap, err
	}
	// a released delete (or lora_delete) unlinks files a run there would load: its host takes no such run until it
	// ends (the rescan before its terminal status then says what is left)
	deleting, err := collect[struct{ Host, Kind, Name string }](ctx, tx, `SELECT host, kind, coalesce(model, lora_id, '')
		FROM model_jobs WHERE kind IN ('delete', 'lora_delete') AND state IN ('queued', 'running') AND released_at IS NOT NULL`)
	if err != nil {
		return snap, err
	}
	var owners []string
	for _, run := range open {
		if run.ReleasedAt != nil && run.Host != nil {
			snap.released = append(snap.released, run)
			continue
		}
		slots, resident, reason := eligibleIn(reg, snap.values, snap.reserved, run, now)
		if name, gone := deleted[run.ID]; gone {
			reason = "LoRA " + name + " was deleted"
		}
		if reason != "" {
			snap.stuck = append(snap.stuck, stuck{run, reason})
			continue
		}
		for _, d := range deleting {
			if env, ok := slots[d.Host]; ok && deletes(reg, d.Host+"."+env, d.Kind, d.Name, run) {
				delete(slots, d.Host)
			}
		}
		if held[run.ID] || len(slots) == 0 {
			continue // (no slot left: it waits for the delete to end, without a position)
		}
		snap.queue = append(snap.queue, queued{run: run, slots: slots, resident: resident})
		owners = append(owners, run.OwnerID)
	}
	snap.idle = map[string]time.Time{}
	for _, value := range snap.values { // by key: by host name
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
		snap.idle[row.Key] = row.At
	}
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

// eligible returns where run may go now (contracts/README.md "Several hosts", Eligibility), each step narrowing the
// hosts by name: (1) a live key of the run's env whose runner publishes its workflow (a user workflow's run: every
// kind of its graph); (2) every <family>/model node's variant present at its precision in that runner's models key;
// (3) every shared LoRA the run was made with; (4) room: its admission there would not fail it at once. It returns
// host name → env, which of them hold every model node resident (its family's lora component, with the run's baked
// LoRAs), and with none the text of the step that left none. A retried run drops its lost host while another remains,
// and with none fails as that host's loss. Room counts no local server's reservation (eligibleIn does).
func eligible(reg *registry.Registry, values []registry.Host, run model.Run, now time.Time) (map[string]string, map[string]bool, string) {
	return eligibleIn(reg, values, nil, run, now)
}

// eligibleIn is eligible with room checked against each host's budget less what its enabled local servers reserve
// (hundredths, as decide subtracts it): a host the reservation leaves too small is not eligible, so the run is never
// lined up there and failed by its admission while another host fits.
func eligibleIn(reg *registry.Registry, values []registry.Host, reserved map[string]int, run model.Run, now time.Time) (map[string]string, map[string]bool, string) {
	var graph map[string]registry.Node
	_ = json.Unmarshal(run.Nodes, &graph)
	models := runModels(graph)
	used := runUsed(run.Loras)
	bake := baked(used, graph)
	env := run.Env
	gone := func(reason string) (map[string]string, map[string]bool, string) {
		if run.LostHost != nil {
			reason = "runner on host " + *run.LostHost + " stopped responding; no other live host for env " + env
		}
		return nil, nil, reason
	}
	var hosts []string // values are by key, so by host name
	for _, value := range values {
		source := value.Host + "." + value.Env
		switch {
		case value.Env != env || !value.Live(now) || (run.PinHost != nil && value.Host != *run.PinHost):
			continue // (a local server's call goes only to its provider's host)
		case run.WorkflowVersion == nil && !reg.Publishes(source, run.WorkflowID):
			continue
		case run.WorkflowVersion != nil && !reg.PublishesKinds(source, kinds(graph)):
			continue // a user workflow: the runner must publish every kind of its snapshot
		}
		hosts = append(hosts, value.Host)
	}
	if len(hosts) == 0 {
		return gone("no live host for env " + env)
	}
	missing := "" // the first model node the first host lacks
	hosts = slices.DeleteFunc(hosts, func(host string) bool {
		for _, m := range models {
			found, listed := reg.Model(host+"."+env, m.family)
			if !listed || !found.Has(m.variant) || !found.HasPrecision(m.variant, found.RunsAt(m.variant, m.precision)) {
				if missing == "" {
					id := m.family
					if listed {
						id = found.ID
					}
					missing = fmt.Sprintf("model %s variant %s at %s", id, m.variant, m.precision)
				}
				return true
			}
		}
		return false
	})
	if len(hosts) == 0 {
		return gone("no live host for env " + env + " has " + missing)
	}
	// API 1.19: a <family>/distill node fuses the family's distilled LoRA onto another variant: the host's models
	// key must list it as pre-fused for variant distilled
	for _, id := range slices.Sorted(maps.Keys(graph)) {
		family, distill := strings.CutSuffix(graph[id].Kind, "/distill")
		if !distill {
			continue
		}
		hosts = slices.DeleteFunc(hosts, func(host string) bool {
			found, _ := reg.Model(host+"."+env, family)
			return found.Lora == nil || !slices.ContainsFunc(found.Lora.Prefused, func(p registry.Prefused) bool { return p.Variant == "distilled" })
		})
		if len(hosts) == 0 {
			return gone("no live host for env " + env + " has the distilled LoRA")
		}
	}
	has := func(host string, u lora.Used) bool { return u.Source != "shared" || reg.HasLora(host+"."+env, u.ID) }
	withAll := slices.DeleteFunc(slices.Clone(hosts), func(host string) bool {
		return slices.ContainsFunc(used, func(u lora.Used) bool { return !has(host, u) })
	})
	if len(withAll) == 0 {
		first := used[slices.IndexFunc(used, func(u lora.Used) bool { return !has(hosts[0], u) })]
		if !slices.ContainsFunc(hosts, func(host string) bool { return has(host, first) }) {
			return gone("no live host for env " + env + " has LoRA " + first.Name)
		}
		return gone("no live host for env " + env + " has all of its LoRAs (" + first.Name + " is not on " + hosts[0] + ")")
	}
	hosts, largest, why := withAll, -2, ""
	hosts = slices.DeleteFunc(hosts, func(host string) bool {
		budget, ram := budgets(values, host)
		if budget < 0 {
			return false // no admission there
		}
		budget -= reserved[host]
		p := planOf(reg, host, run)
		peak := p.peak
		if peak < 0 {
			peak = budget
		}
		reason := tooBig(host, peak, budget, p.ramPeak, ram)
		if reason != "" && budget > largest { // the host with the largest budget (then name) says why
			largest, why = budget, reason
		}
		return reason != ""
	})
	if len(hosts) == 0 {
		return gone(why)
	}
	if run.LostHost != nil && len(hosts) > 1 {
		hosts = slices.DeleteFunc(hosts, func(host string) bool { return host == *run.LostHost }) // attempt 2 goes elsewhere
	}
	slots, resident := map[string]string{}, map[string]bool{}
	for _, host := range hosts {
		slots[host] = env
		value := values[slices.IndexFunc(values, func(v registry.Host) bool { return v.Host == host && v.Env == env })]
		all := len(models) > 0
		for _, m := range models {
			found, _ := reg.Model(host+"."+env, m.family)
			all = all && slices.ContainsFunc(value.Resident, func(r registry.Resident) bool {
				return r.Model == found.ID && r.Variant == m.variant && r.Precision == found.RunsAt(m.variant, m.precision) && sameBaked(r.Loras, bake) &&
					(found.Lora == nil || r.Component == found.Lora.Component) && registry.OnDevice(r.Device)
			})
		}
		resident[host] = all
	}
	return slots, resident, ""
}

// deletes is whether a delete of model (kind delete) or of a shared LoRA (lora_delete) removes files run would load
// on source.
func deletes(reg *registry.Registry, source, kind, name string, run model.Run) bool {
	if kind == "lora_delete" {
		return slices.ContainsFunc(runUsed(run.Loras), func(u lora.Used) bool { return u.Source == "shared" && u.ID == name })
	}
	var graph map[string]registry.Node
	_ = json.Unmarshal(run.Nodes, &graph)
	return slices.ContainsFunc(runModels(graph), func(m runModel) bool {
		found, listed := reg.Model(source, m.family)
		return listed && found.ID == name
	})
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

// lineUp lines up every waiting run of snap on one of its eligible hosts (q.host; contracts/README.md "Several hosts",
// Line-up): the first by free (not busy), resident, fits now (its admission would release it at once), idle longest
// (oldest last ended run; never ran first), then name.
func (d *Dispatcher) lineUp(reg *registry.Registry, snap snapshot, busy map[string]bool, now time.Time) {
	for i := range snap.queue {
		q := &snap.queue[i]
		hosts := slices.Sorted(maps.Keys(q.slots))
		fits := map[string]bool{}
		if len(hosts) > 1 {
			for _, host := range hosts {
				fits[host] = d.fitsNow(reg, snap, placed{run: q.run, host: host, env: q.slots[host]}, now)
			}
		}
		q.host = slices.MinFunc(hosts, func(a, b string) int {
			return cmp.Or(cmp.Compare(btoi(busy[a]), btoi(busy[b])), cmp.Compare(btoi(!q.resident[a]), btoi(!q.resident[b])),
				cmp.Compare(btoi(!fits[a]), btoi(!fits[b])), snap.idle[a].Compare(snap.idle[b]), strings.Compare(a, b))
		})
	}
}

// fitsNow reports whether next's admission on its host would release it now, nothing unloaded; a nil dispatcher
// decides from the hosts values alone.
func (d *Dispatcher) fitsNow(reg *registry.Registry, snap snapshot, next placed, now time.Time) bool {
	if d == nil {
		d = &Dispatcher{registry: reg, asks: map[string]*ask{}}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	snap.queue = nil // what other runs need orders victims only: not whether it fits
	return d.decide(snap, next, now).decision == "release"
}

// lineup orders the waiting runs lined up on each host (q.host) the way the dispatcher would release them there, every
// one of them (Run.position): ahead[h] counts runs already before the line on h (released, not started); last is
// each owner's last release; skips are the current skip counts, time frozen at now. ponytail: each host's line is
// simulated on its own and residency stays as it is now for the whole line (positions only: each pass picks again
// from the newest state).
func lineup(waiting []queued, last map[string]time.Time, ahead map[string]int, skips map[string]skip, now time.Time) []placed {
	var out []placed
	hosts := map[string]bool{}
	for _, q := range waiting {
		hosts[q.host] = true
	}
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		last, skips, left, position := maps.Clone(last), maps.Clone(skips), slices.Clone(waiting), ahead[host]
		if last == nil {
			last = map[string]time.Time{}
		}
		if skips == nil {
			skips = map[string]skip{}
		}
		clock := time.Time{} // a simulated release time later than every real one
		for _, at := range last {
			if at.After(clock) {
				clock = at
			}
		}
		for {
			k, passed := pick(host, left, last, skips, now)
			if k < 0 {
				break
			}
			next := left[k]
			position++
			out = append(out, placed{run: next.run, host: host, env: next.slots[host], position: position, passed: passed})
			passOver(skips, next.run.ID, passed, now)
			clock = clock.Add(time.Nanosecond)
			last[next.run.OwnerID] = clock
			left = slices.Delete(left, k, k+1)
		}
	}
	return out
}

// skip is how often a queued run, its user's head, was passed over for another user's resident head, and when first.
type skip struct {
	n     int
	first time.Time
}

// overdue reports whether a head that needs a swap goes now: passed over SwapSkips times, or SwapWait since the first.
func overdue(s skip, now time.Time) bool {
	return s.n >= SwapSkips || (!s.first.IsZero() && now.Sub(s.first) >= SwapWait)
}

// passOver records a decided pick in skips: the picked run drops its count, each head passed over counts one more.
func passOver(skips map[string]skip, picked string, passed []string, now time.Time) {
	delete(skips, picked)
	for _, id := range passed {
		s := skips[id]
		s.n++
		if s.first.IsZero() {
			s.first = now
		}
		skips[id] = s
	}
}

// pick returns the index in left (oldest first) of the run lined up on host that host takes next, or -1, and the ids
// of the heads it passed over (contracts/README.md "the hand-over", Order): interactive before batch; users in round-robin order (the oldest
// last release first, never released first; a tie: the older oldest run); a user's head is their oldest run resident
// on host, else their oldest; the first user whose head is resident or overdue goes, else the first user's head.
func pick(host string, left []queued, last map[string]time.Time, skips map[string]skip, now time.Time) (int, []string) {
	lane := ""
	for _, q := range left {
		if q.host == host && lane != "interactive" {
			lane = q.run.Lane
		}
	}
	var owners []string
	heads := map[string]int{}
	for i, q := range left {
		if q.host != host || q.run.Lane != lane {
			continue
		}
		owner := q.run.OwnerID
		if h, seen := heads[owner]; !seen {
			owners, heads[owner] = append(owners, owner), i
		} else if q.resident[host] && !left[h].resident[host] {
			heads[owner] = i
		}
	}
	if len(owners) == 0 {
		return -1, nil
	}
	slices.SortStableFunc(owners, func(a, b string) int { return last[a].Compare(last[b]) })
	for n, owner := range owners {
		head := left[heads[owner]]
		if head.resident[host] || overdue(skips[head.run.ID], now) {
			var passed []string
			for _, o := range owners[:n] {
				passed = append(passed, left[heads[o]].run.ID)
			}
			return heads[owner], passed
		}
	}
	return heads[owners[0]], nil
}

// ------------------------------------------------------------------ the queue as users see it

// lineupNow lines up every waiting run on the live hosts as the dispatcher would release them (by run id), and says
// which hosts have a released run not ended. It reads every user's queue as System: callers show only
// their own runs and counts of others'. ponytail: rebuilt on every read, O(n^2) in the waiting runs (per user n is
// bounded by MaxOpenRuns interactive runs plus MaxOpenBatches × the pipelines engine's WINDOW batch rows); one line-up
// per pass shared with readers if users grow large.
func (s *Service) lineupNow(ctx context.Context) (map[string]placed, map[string]bool, error) {
	at, busy := map[string]placed{}, map[string]bool{}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		now := time.Now()
		snap, err := look(ctx, tx, s.registry, now)
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
		s.dispatcher.lineUp(s.registry, snap, busy, now)
		for _, next := range lineup(snap.queue, snap.last, ahead, s.dispatcher.skipState(), now) {
			next.pins = s.dispatcher.pinnedVictims(s.registry, snap, next, now)
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
		run.Host, run.Position, run.UnloadsPinned = nil, nil, nil
		if next, ok := at[run.ID]; ok {
			run.Host, run.Position, run.UnloadsPinned = &next.host, &next.position, next.pins
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
