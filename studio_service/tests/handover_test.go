// Integration tests of the hand-over between host mac's torch and mlx runners (contracts/README.md "Two envs on one
// host: the hand-over"): host memory admission over both envs, unload requests naming the victims, budget_gb on
// release, waiting and its time limit, a stale key, resident-first order across users, and GET /hosts.
package tests

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// unloads is a fake runner.unload: it records each request and answers reply.
type unloads struct {
	mu    sync.Mutex
	asked []string
	reply string
}

func (u *unloads) request(_ context.Context, subject string, body []byte) ([]byte, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.asked = append(u.asked, subject+" "+string(body))
	return []byte(u.reply), nil
}

// budgets holds the budget_gb of each command gatePass drained, by run id.
var budgets = map[string]any{}

// gatePass runs one pass of d at at, waits for its unload requests, drains the outbox and returns "<subject> <run>"
// of each command sent.
func (e *env) gatePass(d *studio.Dispatcher, at time.Time) []string {
	e.t.Helper()
	if err := d.Pass(context.Background(), at); err != nil {
		e.t.Fatal(err)
	}
	d.Settle()
	var out []string
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, subject string, envelope *gen.Envelope, _ string) error {
		out = append(out, subject+" "+*envelope.JobID)
		budgets[*envelope.JobID] = data(*envelope)["budget_gb"]
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// done projects running then succeeded for a released run.
func (e *env) done(owner, sessionID, runID string) {
	e.emit(owner, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	e.emit(owner, sessionID, runID, "status", 2, map[string]any{"status": "succeeded"})
}

// torchVictims is the unload request an LTX run makes of torch holding qwenResident: the larger two go (LRU: no
// last_used, then larger first) until 1.3 + 45.87 fits 48.
func torchVictims(runID string) string {
	return `runner.unload.mac.torch {"for_env":"mlx","run_id":"` + runID + `","components":[` +
		`{"component":"text_encoder","model":"qwen-image-2.1","variant":"base","precision":"int8"},` +
		`{"component":"transformer","model":"qwen-image-2.1","variant":"turbo","precision":"int8"}]}`
}

// torch busy: an mlx run waits; then torch idle with its weights: it is asked to unload what does not fit, and the run
// goes once it answered ok (before the hosts watch shows it) with budget_gb = 48 − what torch keeps.
func TestHandoverWaitsForTheOtherEnv(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	fake := &unloads{reply: `{"unloaded_gb": 15.9, "freed": [], "resident": [{"component": "vae", "model": "qwen-image-2.1",
		"variant": "base", "precision": "bf16", "gb": 1.3, "pinned": false}]}`}
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	d.Unload = fake.request
	now := time.Now()
	torch, torchSession := e.start(userA, "qwen-image", turbo)
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.torch " + torch}) || budgets[torch] != float64(48) {
		t.Fatal(got, budgets[torch])
	}
	mlx, _ := e.start(userB, "ltx-text-to-video", nil)
	if got := e.gatePass(d, now); len(got) != 0 || len(fake.asked) != 0 {
		t.Fatal("torch is running: the host is busy", got, fake.asked)
	}
	e.done(userA, torchSession, torch)
	e.mac("torch", qwenResident) // keep_warm
	if got := e.gatePass(d, now); len(got) != 0 {
		t.Fatal("torch holds its weights:", got)
	}
	if !slices.Equal(fake.asked, []string{torchVictims(mlx)}) {
		t.Fatal(fake.asked)
	}
	if run := e.call(userB, "GET", "/v1/studio/runs/"+mlx, nil, 200); run["status"] != "queued" || run["host"] != "mac" ||
		run["position"] != float64(1) {
		t.Fatal("waits first in line", run)
	}
	if got := e.gatePass(d, now.Add(time.Second)); !slices.Equal(got, []string{"cmd.run.mac.mlx " + mlx}) || len(fake.asked) != 1 ||
		budgets[mlx] != 46.7 {
		t.Fatal("released on the ok", got, fake.asked, budgets[mlx])
	}
}

// busy answers: asked again every AskEvery, never more often; a run that still does not fit GateTimeout after it first
// waited fails. A stale key is never asked: the run waits for the hosts watch.
func TestHandoverAsksAgainThenGivesUp(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	e.mac("torch", qwenResident)
	fake := &unloads{reply: `{"error": "a run is in progress", "code": "busy"}`}
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	d.Unload = fake.request
	now := time.Now()
	mlx, _ := e.start(userA, "ltx-text-to-video", nil)
	for _, at := range []time.Duration{0, time.Second, studio.AskEvery - time.Second, studio.AskEvery, studio.AskEvery + time.Second} {
		if got := e.gatePass(d, now.Add(at)); len(got) != 0 {
			t.Fatal(got)
		}
	}
	if len(fake.asked) != 2 {
		t.Fatal("asked at 0 and AskEvery:", fake.asked)
	}
	e.mac("torch", qwenResident, now.Add(studio.GateTimeout)) // the runners kept refreshing their keys
	e.mac("mlx", nil, now.Add(studio.GateTimeout))
	e.gatePass(d, now.Add(studio.GateTimeout))
	if run := e.call(userA, "GET", "/v1/studio/runs/"+mlx, nil, 200); run["status"] != "failed" ||
		run["error"] != "env torch on host mac did not unload its models in 120 s" {
		t.Fatal(run)
	}

	e.mac("torch", qwenResident, now.Add(-time.Minute)) // stale: counts, never asked
	e.mac("mlx", nil)
	asked := len(fake.asked)
	second, _ := e.start(userA, "ltx-text-to-video", nil)
	if got := e.gatePass(d, now); len(got) != 0 || len(fake.asked) != asked {
		t.Fatal(got, fake.asked)
	}
	e.mac("torch", nil) // its first write after a restart
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.mlx " + second}) {
		t.Fatal(got)
	}
}

// Resident first across users: B's qwen run, resident, goes before A's older LTX run that needs a swap (no unload);
// then A's run goes, torch asked to make room; each env gives memory up in turn.
func TestHandoverResidentFirstAcrossEnvs(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	e.mac("torch", qwenResident)
	fake := &unloads{reply: `{"unloaded_gb": 15.9, "freed": [], "resident": []}`}
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	d.Unload = fake.request
	now := time.Now()
	mlx, mlxSession := e.start(userA, "ltx-text-to-video", nil) // older
	torch, torchSession := e.start(userB, "qwen-image", turbo)
	if run := e.call(userB, "GET", "/v1/studio/runs/"+torch, nil, 200); run["position"] != float64(1) {
		t.Fatal("resident first in line:", run)
	}
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.torch " + torch}) || len(fake.asked) != 0 {
		t.Fatal(got, fake.asked)
	}
	e.done(userB, torchSession, torch)
	if got := e.gatePass(d, now); len(got) != 0 || !slices.Equal(fake.asked, []string{torchVictims(mlx)}) {
		t.Fatal(got, fake.asked)
	}
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.mlx " + mlx}) {
		t.Fatal(got)
	}
	e.done(userA, mlxSession, mlx)
	e.mac("torch", nil)
	e.mac("mlx", ltxResident)
	next, _ := e.start(userB, "qwen-image", turbo)
	if got := e.gatePass(d, now); len(got) != 0 || len(fake.asked) != 2 ||
		!strings.HasPrefix(fake.asked[1], `runner.unload.mac.mlx {"for_env":"torch","run_id":"`+next+`","components":[{"component":"transformer"`) {
		t.Fatal(got, fake.asked)
	}
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.torch " + next}) {
		t.Fatal(got)
	}
}

// Batch rows of two envs on one host: interactive first, then the env whose model is resident (no unload), then the
// other env after the hand-over.
func TestHandoverBatchRowsOfTwoEnvs(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	fake := &unloads{reply: `{"unloaded_gb": 19.18, "freed": [], "resident": []}`}
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	d.Unload = fake.request
	now := time.Now()
	torchRow, _ := e.start(userA, "qwen-image", turbo)
	mlxRow, mlxRowSession := e.start(userA, "ltx-text-to-video", nil)
	e.exec(`UPDATE runs SET lane = 'batch' WHERE id = ANY($1::uuid[])`, []string{torchRow, mlxRow})
	interactive, session := e.start(userA, "ltx-text-to-video", nil)
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.mlx " + interactive}) {
		t.Fatal(got)
	}
	e.done(userA, session, interactive)
	e.mac("mlx", ltxResident)
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.mlx " + mlxRow}) || len(fake.asked) != 0 {
		t.Fatal("the resident env's row first, no unload", got, fake.asked)
	}
	e.done(userA, mlxRowSession, mlxRow)
	if got := e.gatePass(d, now); len(got) != 0 || len(fake.asked) != 1 || !strings.HasPrefix(fake.asked[0], "runner.unload.mac.mlx ") {
		t.Fatal(got, fake.asked)
	}
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.mac.torch " + torchRow}) {
		t.Fatal(got)
	}
}

// A pin never fails or holds a run (plan 03-runtime 6.2): the queued run says first which pinned entries it would
// unload (unloads_pinned), then the pinned transformer is asked last, after every unpinned entry.
func TestHandoverPinnedLastResort(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	pinned := slices.Clone(qwenResident)
	pinned[1] = map[string]any{"component": "transformer", "model": "qwen-image-2.1", "variant": "turbo", "precision": "int8",
		"gb": 7.4, "pinned": true, "pinned_why": "pin"}
	e.mac("torch", pinned)
	mlx, _ := e.start(userA, "ltx-text-to-video", nil)
	run := e.call(userA, "GET", "/v1/studio/runs/"+mlx, nil, 200)
	want := []any{map[string]any{"env": "torch", "component": "transformer", "model": "qwen-image-2.1", "variant": "turbo",
		"precision": "int8", "gb": 7.4}}
	if b, _ := json.Marshal(run["unloads_pinned"]); run["status"] != "queued" || string(b) != mustJSON(want) {
		t.Fatal("says so first", run)
	}
	fake := &unloads{reply: `{"error": "a run is in progress", "code": "busy"}`}
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	d.Unload = fake.request
	if got := e.gatePass(d, time.Now()); len(got) != 0 || len(fake.asked) != 1 || fake.asked[0] != `runner.unload.mac.torch {"for_env":"mlx","run_id":"`+mlx+`","components":[`+
		`{"component":"text_encoder","model":"qwen-image-2.1","variant":"base","precision":"int8"},`+
		`{"component":"vae","model":"qwen-image-2.1","variant":"base","precision":"bf16"},`+
		`{"component":"transformer","model":"qwen-image-2.1","variant":"turbo","precision":"int8"}]}` {
		t.Fatal(got, fake.asked)
	}
	if run := e.call(userA, "GET", "/v1/studio/runs/"+mlx, nil, 200); run["status"] != "queued" {
		t.Fatal("waits for the unload, never fails", run)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// GET /hosts: every env of a host carries host_memory (the smallest budget, what all its envs hold) and each resident
// component its last_used.
func TestHostsMemory(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	used := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	withUse := slices.Clone(ltxResident)
	withUse[0] = map[string]any{"component": "vae_encoder", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16",
		"gb": 0.59, "pinned": false, "last_used": used}
	e.mac("torch", qwenResident)
	e.mac("mlx", withUse)
	hosts := e.call(userA, "GET", "/v1/studio/hosts", nil, 200)["hosts"].([]any)
	seen := 0
	for _, h := range hosts {
		host := h.(map[string]any)
		if host["host"] != "mac" {
			continue
		}
		seen++
		if m := host["host_memory"].(map[string]any); m["budget_gb"] != float64(48) || m["used_gb"] != 39.2 {
			t.Fatal(host)
		}
		if host["env"] == "mlx" && host["resident"].([]any)[0].(map[string]any)["last_used"] != "2026-11-15T10:00:00Z" {
			t.Fatal(host)
		}
	}
	if seen != 2 {
		t.Fatal(hosts)
	}
	for _, h := range hosts {
		if host := h.(map[string]any); host["host"] == "fake" && host["host_memory"] != nil {
			t.Fatal("no key of fake reports memory:", host)
		}
	}
	raw, _ := json.Marshal(hosts)
	if strings.Contains(string(raw), "run_id") {
		t.Fatal(string(raw))
	}
}

// A run that fails on the time limit is its owner's turn in round-robin: with an offline torch key in the way of every
// LTX run, A's three runs and B's one fail A, B, A, A (B waits behind one of A's timeouts, not all three).
func TestHandoverATimeoutIsATurn(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	owner := map[string]string{}
	var runs []string
	for _, user := range []string{userA, userA, userA, userB} {
		id, _ := e.start(user, "ltx-text-to-video", nil)
		owner[id], runs = user, append(runs, id)
	}
	var failed []string
	now := time.Now()
	for at := now; len(failed) < len(runs) && at.Before(now.Add(10*studio.GateTimeout)); at = at.Add(studio.GateTimeout / 4) {
		e.mac("torch", qwenResident, at.Add(-time.Minute)) // offline: counted, never asked
		e.mac("mlx", nil, at)
		e.gatePass(d, at)
		for _, id := range runs {
			if !slices.Contains(failed, id) && e.call(owner[id], "GET", "/v1/studio/runs/"+id, nil, 200)["status"] == "failed" {
				failed = append(failed, id)
			}
		}
	}
	if want := []string{runs[0], runs[3], runs[1], runs[2]}; !slices.Equal(failed, want) {
		t.Fatal(failed, want)
	}
}
