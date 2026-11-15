package studio

import (
	"cmp"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/registry"
)

// The shared dispatcher fixtures (contracts/fixtures/dispatch, check.py admit and simulate) through admit and pick.

// dispatchFixtures returns the names and bodies of the fixtures with prefix.
func dispatchFixtures(t *testing.T, prefix string) map[string][]byte {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join("..", "..", "..", "contracts", "fixtures", "dispatch", prefix+".*.json"))
	if len(paths) == 0 {
		t.Fatal("no fixtures", prefix)
	}
	out := map[string][]byte{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = data
	}
	return out
}

// componentKey is a fixture's component (or a planned one) as an entry key.
func componentKey(c message.Component) string {
	var loras []registry.ResidentLora
	for _, l := range c.Loras {
		loras = append(loras, registry.ResidentLora{LoraID: &l.LoraID, Strength: &l.Strength})
	}
	return entryKey(c.Component, c.Model, c.Variant, c.Precision, loras)
}

// jsonOf is v as generic JSON (for comparing with a fixture's expect).
func jsonOf(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

// Every admit.* fixture: one admission decision for the host's keys, the run and the other queued runs.
func TestAdmitFixtures(t *testing.T) {
	for name, data := range dispatchFixtures(t, "admit") {
		var fx struct {
			Host string `json:"host"`
			Keys map[string]struct {
				State    string              `json:"state"`
				RunID    *string             `json:"run_id"`
				BudgetGB *float64            `json:"budget_gb"`
				UsedGB   *float64            `json:"used_gb"`
				Resident []registry.Resident `json:"resident"`
			} `json:"keys"`
			Run struct {
				Env    string              `json:"env"`
				PeakGB *float64            `json:"peak_gb"`
				Needs  []message.Component `json:"needs"`
			} `json:"run"`
			Queued []struct {
				Env   string              `json:"env"`
				Needs []message.Component `json:"needs"`
			} `json:"queued"`
			Expect json.RawMessage `json:"expect"`
		}
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatal(name, err)
		}
		var entries []entry
		budget, running := -1, ""
		for _, env := range slices.Sorted(maps.Keys(fx.Keys)) {
			key := fx.Keys[env]
			if key.State == "gone" {
				continue
			}
			if key.RunID != nil && running == "" {
				running = env
			}
			if key.BudgetGB != nil && (budget < 0 || hundredths(*key.BudgetGB) < budget) {
				budget = hundredths(*key.BudgetGB)
			}
			measured := -1
			if key.UsedGB != nil {
				measured = hundredths(*key.UsedGB)
			}
			entries = append(entries, keyEntries(env, key.Resident, key.State == "fresh" && key.RunID == nil, measured)...)
		}
		peak := -1
		if fx.Run.PeakGB != nil {
			peak = hundredths(*fx.Run.PeakGB)
		}
		var needs []string
		for _, n := range fx.Run.Needs {
			needs = append(needs, componentKey(n))
		}
		referenced := map[string]bool{}
		for _, q := range fx.Queued {
			for _, n := range q.Needs {
				referenced[q.Env+" "+componentKey(n)] = true
			}
		}
		got := admission{decision: "release"}
		if budget >= 0 {
			got = admit(fx.Host, entries, budget, fx.Run.Env, peak, needs, referenced, running)
		}
		unload := map[string][]message.Component{}
		for env, victims := range got.unload {
			for _, v := range victims {
				unload[env] = append(unload[env], v.component())
			}
		}
		var errText *string
		if got.err != "" {
			errText = &got.err
		}
		out := jsonOf(t, map[string]any{"decision": got.decision, "unload": unload, "budget_gb": got.budget, "error": errText})
		var want any
		_ = json.Unmarshal(fx.Expect, &want)
		if !reflect.DeepEqual(out, want) {
			t.Errorf("%s:\n got %v\nwant %v", name, out, want)
		}
	}
}

// simPlan is an order fixture's plan.
type simPlan struct {
	Env           string                   `json:"env"`
	Model         string                   `json:"model"`
	PeakGB        float64                  `json:"peak_gb"`
	LoraComponent string                   `json:"lora_component"`
	Components    []registry.PlanComponent `json:"components"`
}

// simRun is a run of an order fixture's simulation; row is (batch, row, step) for a batch run.
type simRun struct {
	ID      string  `json:"id"`
	Owner   string  `json:"owner"`
	Lane    string  `json:"lane"`
	Plan    string  `json:"plan"`
	At      float64 `json:"at"`
	Seconds float64 `json:"seconds"`
	seq     int
	batch   string
	row     int
	step    int
}

type simUnload struct {
	Before     string   `json:"before"`
	Env        string   `json:"env"`
	Components []string `json:"components"`
}

type simBatch struct {
	ID      string    `json:"id"`
	Owner   string    `json:"owner"`
	Rows    int       `json:"rows"`
	Steps   []string  `json:"steps"`
	Seconds []float64 `json:"seconds"`
	At      float64   `json:"at"`
	started int
	ended   int
}

// Every order.* fixture: one host releasing runs one at a time in pick's order with admit, every unload answered at
// once; batches are two-step pipelines with row 1 first and a window of 16 rows (the pipelines engine), as check.py's
// simulate.
func TestOrderFixtures(t *testing.T) {
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return base.Add(time.Duration(s * float64(time.Second))) }
	for name, data := range dispatchFixtures(t, "order") {
		var fx struct {
			BudgetGB float64            `json:"budget_gb"`
			Plans    map[string]simPlan `json:"plans"`
			Resident []string           `json:"resident"`
			Runs     []simRun           `json:"runs"`
			Batches  []*simBatch        `json:"batches"`
			Expect   struct {
				Releases []string          `json:"releases"`
				Unloads  []simUnload       `json:"unloads"`
				Loads    map[string]int    `json:"loads"`
				Fails    map[string]string `json:"fails"`
			} `json:"expect"`
		}
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatal(name, err)
		}
		key := func(plan string, c registry.PlanComponent) string {
			return entryKey(c.Component, fx.Plans[plan].Model, c.Variant, c.Precision, nil)
		}
		resident := map[string]entry{} // "<env> <key>" → the entry
		hold := func(plan string, used func(j int) time.Time) {
			p := fx.Plans[plan]
			for j, c := range p.Components {
				if !c.UnloadsAfter {
					last := used(j)
					resident[p.Env+" "+key(plan, c)] = entry{env: p.Env, key: key(plan, c), gb: hundredths(c.GB), fresh: true,
						res: registry.Resident{Component: c.Component, Model: p.Model, Variant: c.Variant, Precision: c.Precision,
							GB: c.GB, LastUsed: &last}}
				}
			}
		}
		for i, plan := range fx.Resident {
			hold(plan, func(j int) time.Time { return base.Add(time.Duration((i-len(fx.Resident))*1000+j) * time.Millisecond) })
		}
		var runs []simRun
		seq := 0
		add := func(r simRun) {
			seq++
			r.seq = seq
			if r.Seconds == 0 {
				r.Seconds = 10
			}
			runs = append(runs, r)
		}
		for _, r := range fx.Runs {
			add(r)
		}
		batches := map[string]*simBatch{}
		for _, b := range fx.Batches {
			b.started = 1
			batches[b.ID] = b
			add(simRun{ID: b.ID + ".1.1", Owner: b.Owner, Lane: "batch", Plan: b.Steps[0], At: b.At, Seconds: b.Seconds[0],
				batch: b.ID, row: 1, step: 1})
		}
		isResident := func(r simRun) bool {
			p := fx.Plans[r.Plan]
			return slices.ContainsFunc(p.Components, func(c registry.PlanComponent) bool {
				_, ok := resident[p.Env+" "+key(r.Plan, c)]
				return c.Component == p.LoraComponent && ok
			})
		}
		t0, last, skips := 0.0, map[string]time.Time{}, map[string]skip{}
		var releases []string
		unloads := []simUnload{}
		loads, fails := map[string]int{}, map[string]string{}
		for len(runs) > 0 {
			var ready []simRun
			for _, r := range runs {
				if r.At <= t0 {
					ready = append(ready, r)
				}
			}
			if len(ready) == 0 {
				t0 = slices.MinFunc(runs, func(a, b simRun) int { return cmp.Compare(a.At, b.At) }).At
				continue
			}
			slices.SortStableFunc(ready, func(a, b simRun) int { return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.seq, b.seq)) })
			left := make([]queued, len(ready))
			for i, r := range ready {
				left[i] = queued{run: model.Run{ID: r.ID, OwnerID: r.Owner, Lane: r.Lane},
					slots: map[string]string{"mac": fx.Plans[r.Plan].Env}, resident: map[string]bool{"mac": isResident(r)}}
			}
			now := at(t0)
			k, passed := pick("mac", left, last, skips, now)
			passOver(skips, ready[k].ID, passed, now)
			chosen := ready[k]
			p := fx.Plans[chosen.Plan]
			var entries []entry
			for _, e := range resident {
				entries = append(entries, e)
			}
			referenced := map[string]bool{}
			for _, r := range ready {
				if r.ID != chosen.ID {
					for _, c := range fx.Plans[r.Plan].Components {
						referenced[fx.Plans[r.Plan].Env+" "+key(r.Plan, c)] = true
					}
				}
			}
			var needs []string
			for _, c := range p.Components {
				needs = append(needs, key(chosen.Plan, c))
			}
			got := admit("mac", entries, hundredths(fx.BudgetGB), p.Env, hundredths(p.PeakGB), needs, referenced, "")
			runs = slices.DeleteFunc(runs, func(r simRun) bool { return r.ID == chosen.ID })
			end := t0
			if got.decision == "fail" {
				fails[chosen.ID] = got.err
			} else {
				if got.decision == "wait" {
					t.Fatal(name, "nothing in a simulation is stale or pinned")
				}
				for _, env := range slices.Sorted(maps.Keys(got.unload)) {
					u := simUnload{Before: chosen.ID, Env: env}
					for _, v := range got.unload[env] {
						u.Components = append(u.Components, v.res.Component)
						delete(resident, env+" "+v.key)
					}
					unloads = append(unloads, u)
				}
				releases = append(releases, chosen.ID)
				if slices.ContainsFunc(p.Components, func(c registry.PlanComponent) bool {
					_, ok := resident[p.Env+" "+key(chosen.Plan, c)]
					return !c.UnloadsAfter && !ok
				}) {
					loads[chosen.Plan]++
				}
				end = t0 + chosen.Seconds
				hold(chosen.Plan, func(j int) time.Time { return base.Add(time.Duration(end*1000+float64(j)) * time.Millisecond) })
				last[chosen.Owner] = now
			}
			if chosen.batch != "" {
				b := batches[chosen.batch]
				if chosen.step < len(b.Steps) && got.decision != "fail" {
					add(simRun{ID: b.ID + "." + strconv.Itoa(chosen.row) + "." + strconv.Itoa(chosen.step+1), Owner: b.Owner, Lane: "batch",
						Plan: b.Steps[chosen.step], At: end, Seconds: b.Seconds[chosen.step], batch: b.ID, row: chosen.row, step: chosen.step + 1})
				} else {
					b.ended++
					for b.started < b.Rows && b.started-b.ended < 16 { // row 1 has ended
						b.started++
						add(simRun{ID: b.ID + "." + strconv.Itoa(b.started) + ".1", Owner: b.Owner, Lane: "batch", Plan: b.Steps[0], At: end,
							Seconds: b.Seconds[0], batch: b.ID, row: b.started, step: 1})
					}
				}
			}
			t0 = end
		}
		if !slices.Equal(releases, fx.Expect.Releases) {
			t.Errorf("%s releases:\n got %v\nwant %v", name, releases, fx.Expect.Releases)
		}
		if !reflect.DeepEqual(jsonOf(t, unloads), jsonOf(t, fx.Expect.Unloads)) {
			t.Errorf("%s unloads:\n got %v\nwant %v", name, unloads, fx.Expect.Unloads)
		}
		if !reflect.DeepEqual(loads, fx.Expect.Loads) || (len(fails) > 0 || fx.Expect.Fails != nil) && !reflect.DeepEqual(fails, fx.Expect.Fails) {
			t.Errorf("%s loads %v fails %v, want %v %v", name, loads, fails, fx.Expect.Loads, fx.Expect.Fails)
		}
	}
}
