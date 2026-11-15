package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/registry"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// waiting builds a queued run id of owner, created at minute n, eligible on hosts (name → env), resident on those
// named with a leading "*".
func waiting(id, owner string, n int, hosts ...string) queued {
	q := queued{run: model.Run{ID: id, OwnerID: owner, Lane: "interactive", CreatedAt: t0.Add(time.Duration(n) * time.Minute)},
		slots: map[string]string{}, resident: map[string]bool{}}
	for _, host := range hosts {
		name, resident := strings.CutPrefix(host, "*")
		q.slots[name] = "env"
		q.resident[name] = resident
	}
	return q
}

// order renders a line-up as "id@host#position" strings.
func order(lined []placed) []string {
	var out []string
	for _, p := range lined {
		out = append(out, fmt.Sprintf("%s@%s#%d", p.run.ID, p.host, p.position))
	}
	return out
}

// wantLineup fails unless got equals want.
func wantLineup(t *testing.T, got []placed, want ...string) {
	t.Helper()
	if !slices.Equal(order(got), want) {
		t.Fatalf("got %v, want %v", order(got), want)
	}
}

// Round-robin between users on one host: each user's oldest run in turn, never-released users first in the order of
// their oldest run, then the user released least recently; positions count the whole line.
func TestLineupRoundRobin(t *testing.T) {
	queue := []queued{waiting("a1", "A", 1, "h"), waiting("a2", "A", 2, "h"), waiting("b1", "B", 3, "h"),
		waiting("a3", "A", 4, "h"), waiting("c1", "C", 5, "h"), waiting("b2", "B", 6, "h")}
	wantLineup(t, lineup([]string{"h"}, queue, nil, nil, 0, nil, t0), "a1@h#1", "b1@h#2", "c1@h#3", "a2@h#4", "b2@h#5", "a3@h#6")

	last := map[string]time.Time{"A": t0.Add(time.Hour), "B": t0} // C never released: first; then B, served longer ago
	wantLineup(t, lineup([]string{"h"}, queue, last, nil, 0, nil, t0), "c1@h#1", "b1@h#2", "a1@h#3", "b2@h#4", "a2@h#5", "a3@h#6")

	// One dispatch round releases one run per free host; ahead shifts the positions behind a sent run.
	wantLineup(t, lineup([]string{"h"}, queue, last, nil, 1, nil, t0), "c1@h#1")
	wantLineup(t, lineup([]string{"h"}, queue[:2], nil, map[string]int{"h": 1}, 0, nil, t0), "a1@h#2", "a2@h#3")
}

// Interactive runs go before batch runs whatever their owner's turn or age.
func TestLineupLane(t *testing.T) {
	batch := waiting("b1", "B", 1, "h")
	batch.run.Lane = "batch"
	wantLineup(t, lineup([]string{"h"}, []queued{batch, waiting("a1", "A", 2, "h")}, map[string]time.Time{"A": t0}, nil, 0, nil, t0),
		"a1@h#1", "b1@h#2")
}

// Within one user, a run whose model is resident on the host goes before an older one; another user's turn is kept.
func TestLineupPrefersResident(t *testing.T) {
	queue := []queued{waiting("a1", "A", 1, "h"), waiting("b1", "B", 2, "h"), waiting("a2", "A", 3, "*h")}
	wantLineup(t, lineup([]string{"h"}, queue, nil, nil, 0, nil, t0), "a2@h#1", "b1@h#2", "a1@h#3")
}

// Placement: hosts take turns idle longest first, a run goes only where it is eligible, and a run resident on another
// free host goes there while the first host picks again.
func TestLineupPlacement(t *testing.T) {
	wantLineup(t, lineup([]string{"h1", "h2"}, []queued{waiting("a1", "A", 1, "h2"), waiting("b1", "B", 2, "h1", "h2")}, nil, nil, 1, nil, t0),
		"b1@h1#1", "a1@h2#1")
	wantLineup(t, lineup([]string{"h1", "h2"}, []queued{waiting("a1", "A", 1, "h1", "*h2"), waiting("b1", "B", 2, "h1", "h2")}, nil, nil, 1, nil, t0),
		"a1@h2#1", "b1@h1#1")
	wantLineup(t, lineup([]string{"h1", "h2"}, []queued{waiting("a1", "A", 1, "h1", "h2"), waiting("a2", "A", 2, "h1", "h2"),
		waiting("a3", "A", 3, "h1", "h2")}, nil, nil, 0, nil, t0), "a1@h1#1", "a2@h2#1", "a3@h1#2")
	if got := lineup(nil, []queued{waiting("a1", "A", 1, "h")}, nil, nil, 0, nil, t0); len(got) != 0 {
		t.Fatal("no host, no line", order(got))
	}
}

// hostValue is a hosts KV value of host.env refreshed at.
func hostValue(host, env string, at time.Time, extra string) []byte {
	return []byte(fmt.Sprintf(`{"host": %q, "env": %q, "state": "idle", "updated_at": %q%s}`, host, env, at.Format(time.RFC3339), extra))
}

// A host is eligible when live, of the run's env, publishing its workflow and (when listed) holding its variant;
// resident needs the model, variant and precision loaded. With none, the reason says which is missing.
func TestEligible(t *testing.T) {
	reg := &registry.Registry{}
	workflows := func(id, env string) []byte {
		return []byte(`{"workflows": [{"id": "` + id + `", "env": "` + env + `", "nodes": {}}]}`)
	}
	models := `{"models": [{"id": "qwen-2", "family": "qwen-image", "variants": [{"id": "base", "presence": "present",
		"precisions": ["bf16"]}, {"id": "turbo", "presence": "missing", "precisions": ["bf16"]}]}]}`
	_ = reg.SetRegistry("mac.torch.workflows", workflows("qwen-image", "torch"))
	_ = reg.SetRegistry("mac.torch.models", []byte(models))
	_ = reg.SetRegistry("gpu.torch.workflows", workflows("qwen-image", "torch"))
	_ = reg.SetRegistry("old.torch.workflows", workflows("qwen-image", "torch"))
	_ = reg.SetRegistry("fake.fake.workflows", workflows("echo", "fake"))
	_ = reg.SetHost("mac.torch", hostValue("mac", "torch", t0, `, "resident": [{"component": "transformer", "model": "qwen-2",
		"variant": "base", "precision": "bf16", "gb": 1, "pinned": false}]`))
	_ = reg.SetHost("gpu.torch", hostValue("gpu", "torch", t0, ""))
	_ = reg.SetHost("old.torch", hostValue("old", "torch", t0.Add(-time.Minute), ""))
	_ = reg.SetHost("fake.fake", hostValue("fake", "fake", t0, ""))
	run := func(workflow, env, variant, precision string) model.Run {
		graph, _ := json.Marshal(map[string]registry.Node{"prompt": {Kind: "prompt"},
			"model": {Kind: "qwen-image/model", Params: map[string]any{"variant": variant, "precision": precision}}})
		return model.Run{WorkflowID: workflow, Env: env, Nodes: graph}
	}

	slots, resident, reason := eligible(reg, reg.Hosts(), run("qwen-image", "torch", "base", "bf16"), t0)
	if reason != "" || len(slots) != 2 || slots["mac"] != "torch" || slots["gpu"] != "torch" || !resident["mac"] || resident["gpu"] {
		t.Fatal(slots, resident, reason) // old is stale; gpu lists no models: eligible, nothing resident
	}
	if _, resident, _ := eligible(reg, reg.Hosts(), run("qwen-image", "torch", "base", "int8"), t0); resident["mac"] {
		t.Fatal("another precision is not resident")
	}
	if slots, _, _ := eligible(reg, reg.Hosts(), run("qwen-image", "torch", "turbo", "bf16"), t0); len(slots) != 1 || slots["gpu"] == "" {
		t.Fatal("mac lacks turbo", slots)
	}
	// two model nodes: every variant must be present, and resident only when all are (whatever the map order)
	two := run("qwen-image", "torch", "base", "bf16")
	graph := map[string]registry.Node{}
	_ = json.Unmarshal(two.Nodes, &graph)
	graph["model_2"] = registry.Node{Kind: "qwen-image/model", Params: map[string]any{"variant": "turbo", "precision": "bf16"}}
	two.Nodes, _ = json.Marshal(graph)
	for range 20 {
		if slots, resident, _ := eligible(reg, reg.Hosts(), two, t0); len(slots) != 1 || slots["gpu"] == "" || resident["gpu"] {
			t.Fatal("mac lacks model_2's turbo", slots, resident)
		}
	}
	graph["model_2"] = graph["model"]
	two.Nodes, _ = json.Marshal(graph)
	if _, resident, _ := eligible(reg, reg.Hosts(), two, t0); !resident["mac"] {
		t.Fatal("both base bf16: resident on mac")
	}
	_ = reg.SetHost("gpu.torch", nil)
	if _, _, reason := eligible(reg, reg.Hosts(), run("qwen-image", "torch", "turbo", "bf16"), t0); reason !=
		"model qwen-image variant turbo is not present on any live host for env torch" {
		t.Fatal(reason)
	}
	if _, _, reason := eligible(reg, reg.Hosts(), run("qwen-image", "torch", "base", "bf16"), t0.Add(time.Minute)); reason !=
		"no live host for env torch" {
		t.Fatal(reason)
	}
	if _, _, reason := eligible(reg, reg.Hosts(), run("other", "torch", "base", "bf16"), t0); reason != "no live host for env torch" {
		t.Fatal("a host that does not publish the workflow is not eligible:", reason)
	}
	echo := model.Run{WorkflowID: "echo", Env: "fake", Nodes: json.RawMessage(`{"echo": {"kind": "echo/echo", "params": {}}}`)}
	if slots, _, reason := eligible(reg, reg.Hosts(), echo, t0); reason != "" || slots["fake"] != "fake" {
		t.Fatal(slots, reason)
	}
	// a user workflow's run: the host must publish every kind of its graph, not the workflow id
	version := 1
	mine := model.Run{WorkflowID: "6f1c2a4e-0b7d-4c8e-9a51-3d2e1f0a9b88", Env: "fake", Nodes: echo.Nodes, WorkflowVersion: &version}
	if _, _, reason := eligible(reg, reg.Hosts(), mine, t0); reason != "no live host for env fake" {
		t.Fatal("echo/echo is not in fake's nodes yet:", reason)
	}
	_ = reg.SetRegistry("fake.fake.nodes", []byte(`{"nodes": [{"kind": "echo/echo", "title": "Echo", "family": "echo",
		"params": [], "inputs": [], "outputs": []}]}`))
	if slots, _, reason := eligible(reg, reg.Hosts(), mine, t0); reason != "" || slots["fake"] != "fake" {
		t.Fatal(slots, reason)
	}
}

// A released run holds its host while the key is fresh: for Lease after the release whatever it says, then only
// while it names the run and its attempt (absent = 1).
func TestHolds(t *testing.T) {
	released := t0
	host := "mac"
	run := model.Run{ID: "r1", Env: "torch", Host: &host, Attempt: 1, ReleasedAt: &released}
	value := func(at time.Time, extra string) []registry.Host {
		reg := &registry.Registry{}
		if err := reg.SetHost("mac.torch", hostValue("mac", "torch", at, extra)); err != nil {
			t.Fatal(err)
		}
		return reg.Hosts()
	}
	later := t0.Add(Lease + time.Second)
	switch {
	case !holds(value(t0, ""), run, t0.Add(time.Second)):
		t.Fatal("inside Lease an idle key holds")
	case holds(value(t0, ""), run, later):
		t.Fatal("after Lease the key must name the run")
	case !holds(value(later, `, "run_id": "r1"`), run, later):
		t.Fatal("busy with it, attempt absent = 1")
	case holds(value(later, `, "run_id": "r1", "attempt": 2`), run, later):
		t.Fatal("another attempt")
	case holds(value(t0, `, "run_id": "r1"`), run, t0.Add(time.Minute)):
		t.Fatal("a stale key holds nothing")
	case holds(nil, run, t0):
		t.Fatal("no key holds nothing")
	}
	run.Attempt = 2
	if !holds(value(later, `, "run_id": "r1", "attempt": 2`), run, later) {
		t.Fatal("attempt 2 on its key")
	}
}

// A run with shared LoRAs goes only to hosts that list them all (own LoRAs to any); resident needs the same baked
// LoRAs in order, and an entry without LoRAs for a run that bakes none.
func TestEligibleLoras(t *testing.T) {
	reg := &registry.Registry{}
	for _, host := range []string{"mac", "gpu"} {
		_ = reg.SetRegistry(host+".torch.workflows", []byte(`{"workflows": [{"id": "qwen-image", "env": "torch", "nodes": {}}]}`))
		_ = reg.SetRegistry(host+".torch.models", []byte(`{"models": [{"id": "qwen-2", "family": "qwen-image",
			"variants": [{"id": "base", "presence": "present"}]}]}`))
	}
	_ = reg.SetRegistry("mac.torch.loras", []byte(`{"loras": [{"id": "ink", "name": "ink-wash"}, {"id": "oil", "name": "oil"}]}`))
	_ = reg.SetRegistry("gpu.torch.loras", []byte(`{"loras": [{"id": "ink", "name": "ink-wash"}]}`))
	_ = reg.SetHost("mac.torch", hostValue("mac", "torch", t0, `, "resident": [{"component": "transformer", "model": "qwen-2",
		"variant": "base", "precision": "int8", "gb": 1, "pinned": false, "loras": [{"lora_id": "ink", "name": "ink-wash",
		"strength": 0.5, "bytes": 1}]}]`))
	_ = reg.SetHost("gpu.torch", hostValue("gpu", "torch", t0, `, "resident": [{"component": "transformer", "model": "qwen-2",
		"variant": "base", "precision": "int8", "gb": 1, "pinned": false}]`))
	graph, _ := json.Marshal(map[string]registry.Node{"model": {Kind: "qwen-image/model",
		Params: map[string]any{"variant": "base", "precision": "int8"}}})
	run := func(used ...lora.Used) model.Run {
		return model.Run{WorkflowID: "qwen-image", Env: "torch", Nodes: graph, Loras: lorasJSON(used)}
	}
	ink := lora.Used{ID: "ink", Source: "shared", Name: "ink-wash", Strength: 0.5, Mode: "bake"}
	oil := lora.Used{ID: "oil", Source: "shared", Name: "oil", Strength: 1, Mode: "adapter"}
	mine := lora.Used{ID: "2c4e6a8b-1d3f-4a5b-9c7d-0e2f4a6b8c10", Source: "mine", Name: "own", Strength: 1, Mode: "adapter"}

	if slots, resident, _ := eligible(reg, reg.Hosts(), run(mine), t0); len(slots) != 2 || resident["mac"] || !resident["gpu"] {
		t.Fatal("own LoRAs go anywhere; nothing baked: the plain entry", slots, resident)
	}
	if slots, resident, _ := eligible(reg, reg.Hosts(), run(ink, oil), t0); len(slots) != 1 || slots["mac"] == "" || !resident["mac"] {
		t.Fatal("only mac has oil; ink baked at 0.5 is resident there", slots, resident)
	}
	ink.Strength = 1
	if _, resident, _ := eligible(reg, reg.Hosts(), run(ink), t0); resident["mac"] || resident["gpu"] {
		t.Fatal("another baked strength is another entry", resident)
	}
	_ = reg.SetRegistry("mac.torch.loras", nil)
	if _, _, reason := eligible(reg, reg.Hosts(), run(ink, oil), t0); reason != "no live host for env torch has LoRA oil" {
		t.Fatal(reason)
	}
}

// resolveLoras puts the command form in place of each {lora_id}: the own one's key under the run owner's prefix.
func TestResolveLoras(t *testing.T) {
	nodes := map[string]registry.Node{"lora": {Kind: "qwen-image/lora-stack", Params: map[string]any{"rows": []any{
		map[string]any{"lora": map[string]any{"lora_id": "aa"}, "strength": 1.0},
		map[string]any{"lora": map[string]any{"lora_id": "bb"}, "strength": 1.0}}}}}
	resolveLoras(nodes, []lora.Used{{Node: "lora", ID: "aa", Source: "mine", Name: "a", SHA256: "x", Bytes: 3},
		{Node: "lora", ID: "bb", Source: "shared", Name: "b", SHA256: "bb", Bytes: 4}}, "owner")
	rows := nodes["lora"].Params["rows"].([]any)
	own, shared := rows[0].(map[string]any)["lora"].(map[string]any), rows[1].(map[string]any)["lora"].(map[string]any)
	if own["key"] != "u/owner/loras/aa.safetensors" || own["source"] != "own" || shared["key"] != nil || shared["bytes"] != int64(4) {
		t.Fatal(own, shared)
	}
}

// macHosts is host mac's two env keys (budget 48 GB each) as the registry holds them: torch refreshed at torchAt
// holding a 38.1 GB transformer, mlx live and empty.
func macHosts(t *testing.T, torchAt time.Time) []registry.Host {
	t.Helper()
	reg := &registry.Registry{}
	memory := `, "memory": {"budget_gb": 48, "used_gb": 0}`
	resident := memory + `, "resident": [{"component": "transformer", "model": "qwen-image-2.1", "variant": "base",
		"precision": "bf16", "gb": 38.1, "pinned": false}]`
	for key, value := range map[string][]byte{"mac.torch": hostValue("mac", "torch", torchAt, resident),
		"mac.mlx": hostValue("mac", "mlx", t0, memory)} {
		if err := reg.SetHost(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return reg.Hosts()
}

// An mlx run with no plan (P = the budget) on mac: torch's transformer must go.
var mlxRun = placed{run: model.Run{ID: "m", Env: "mlx", Nodes: json.RawMessage(`{}`)}, host: "mac", env: "mlx"}

// An unload ok that answers a request sent before a run was released to that env opens nothing: the next pass asks
// again (the run released meanwhile may have loaded weights the hosts watch does not show yet). The request names the
// victims; an ok of the current generation stands for the env's key until the key is rewritten after it.
func TestHandoverIgnoresAnOkSentBeforeARelease(t *testing.T) {
	d := NewDispatcher(nil, &registry.Registry{}, nil, nil)
	reply := make(chan struct{})
	var bodies []string
	d.Unload = func(_ context.Context, subject string, body []byte) ([]byte, error) {
		bodies = append(bodies, subject+" "+string(body))
		<-reply
		return []byte(`{"unloaded_gb": 38.1, "freed": [{"component": "transformer", "model": "qwen-image-2.1",
			"variant": "base", "precision": "bf16"}], "resident": []}`), nil
	}
	pass := func(hosts []registry.Host, at time.Time) admission {
		got := d.handover(context.Background(), snapshot{values: hosts}, mlxRun, at)
		d.Settle()
		return got
	}
	got := d.handover(context.Background(), snapshot{values: macHosts(t, t0)}, mlxRun, t0)
	if got.decision != "unload" || len(got.unload["torch"]) != 1 {
		t.Fatal("torch holds weights:", got)
	}
	d.loads("mac.torch") // a torch run released while the ask is in flight
	close(reply)
	d.Settle()
	at := t0.Add(AskEvery)
	if got := pass(macHosts(t, at), at); got.decision != "unload" {
		t.Fatal("the late ok must not admit:", got)
	}
	if len(bodies) != 2 || bodies[1] != `runner.unload.mac.torch {"for_env":"mlx","run_id":"m","components":[{"component":`+
		`"transformer","model":"qwen-image-2.1","variant":"base","precision":"bf16"}]}` {
		t.Fatal("asked again, naming the victim:", bodies)
	}
	if got := pass(macHosts(t, at), at); got.decision != "release" || *got.budget != 48 {
		t.Fatal("an ok with no release in between stands for torch's key:", got)
	}
	later := time.Now().Add(time.Second)
	if got := pass(macHosts(t, later), later); got.decision != "unload" {
		t.Fatal("torch's key rewritten after the reply speaks for itself:", got)
	}
}

// At most one request in flight per env, and one every AskEvery; busy and malformed replies change nothing.
func TestHandoverAsksOncePerAskEvery(t *testing.T) {
	d := NewDispatcher(nil, &registry.Registry{}, nil, nil)
	answers := []string{`{"error": "a run is in progress", "code": "busy"}`, `{"unloaded_gb": 38.1}`}
	asked := 0
	d.Unload = func(context.Context, string, []byte) ([]byte, error) {
		asked++
		return []byte(answers[asked-1]), nil
	}
	for _, at := range []time.Time{t0, t0.Add(time.Second), t0.Add(AskEvery), t0.Add(AskEvery + time.Second)} {
		if got := d.handover(context.Background(), snapshot{values: macHosts(t, at)}, mlxRun, at); got.decision != "unload" {
			t.Fatal(at, got)
		}
		d.Settle()
	}
	if asked != 2 {
		t.Fatal("asked at t0 and t0+AskEvery:", asked)
	}
}

// A stale key that still lists resident weights holds them and is never asked (the runner may be cut off from NATS,
// not gone): the run waits, the stale env named; a deleted key holds nothing.
func TestHandoverFailsClosedOnAStaleHolder(t *testing.T) {
	d := NewDispatcher(nil, &registry.Registry{}, nil, nil)
	d.Unload = func(context.Context, string, []byte) ([]byte, error) {
		t.Fatal("a stale key is never asked")
		return nil, nil
	}
	later := t0.Add(10 * registry.Stale)
	if got := d.handover(context.Background(), snapshot{values: macHosts(t, t0)}, mlxRun, later); got.decision != "wait" ||
		got.holder != "torch" {
		t.Fatal("stale with weights holds:", got)
	}
	reg := &registry.Registry{}
	if err := reg.SetHost("mac.mlx", hostValue("mac", "mlx", later, `, "memory": {"budget_gb": 48, "used_gb": 0}`)); err != nil {
		t.Fatal(err)
	}
	if got := d.handover(context.Background(), snapshot{values: reg.Hosts()}, mlxRun, later); got.decision != "release" {
		t.Fatal("no torch key holds nothing:", got)
	}
}

// A torch key that still names a run (its run ended on the view before its next write, or lost its lease while it
// may still run) declares nothing resident, yet that run may use up to its budget_gb: an mlx run waits, nothing asked,
// torch named; torch's next write, idle, releases it.
func TestHandoverWaitsOnAnInRunKey(t *testing.T) {
	d := NewDispatcher(nil, &registry.Registry{}, nil, nil)
	d.Unload = func(context.Context, string, []byte) ([]byte, error) {
		t.Fatal("an in-run key is never asked")
		return nil, nil
	}
	hosts := func(extra string) []registry.Host {
		reg := &registry.Registry{}
		memory := `, "memory": {"budget_gb": 48, "used_gb": 0}, "resident": []` // the mlx run has no plan: P = B
		_ = reg.SetHost("mac.torch", hostValue("mac", "torch", t0, memory+extra))
		_ = reg.SetHost("mac.mlx", hostValue("mac", "mlx", t0, memory))
		return reg.Hosts()
	}
	inRun := `, "run_id": "3f0c7a52-8d1e-4b6a-9c2f-5e7d1a3b8c40", "attempt": 1`
	if got := d.handover(context.Background(), snapshot{values: hosts(inRun)}, mlxRun, t0); got.decision != "wait" ||
		got.holder != "torch" {
		t.Fatal("an in-run key holds the host:", got)
	}
	if got := d.handover(context.Background(), snapshot{values: hosts("")}, mlxRun, t0); got.decision != "release" {
		t.Fatal("the idle write decides:", got)
	}
}

// planOf reads the run's plan from its host's models key: needs in the plan's order, the lora component carrying the
// run's baked LoRAs, the peak adding its LoRA files; no memory item for the precision is no plan.
func TestPlanOf(t *testing.T) {
	reg := &registry.Registry{}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", "ltx.models.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRegistry("mac.mlx.models", data); err != nil {
		t.Fatal(err)
	}
	graph := func(precision string) json.RawMessage {
		nodes, _ := json.Marshal(map[string]registry.Node{"model": {Kind: "ltx/model",
			Params: map[string]any{"variant": "distilled", "precision": precision}}, "lora": {Kind: "ltx/lora-stack", In: map[string]string{"model": "model.model"}}})
		return nodes
	}
	grain := lora.Used{Node: "lora", ID: "1f3a5c7e9b2d4f6a8c0e1b3d5f7a9c2e4b6d8f0a1c3e5b7d9f2a4c6e8b0d1f3a", Source: "shared",
		Strength: 0.8, Mode: "bake", Bytes: 1 << 30}
	run := model.Run{Env: "mlx", Nodes: graph("int8"), Loras: lorasJSON([]lora.Used{grain})}
	p := planOf(reg, "mac", run)
	if p.peak != 4587+100 || len(p.needs) == 0 ||
		!slices.Contains(p.needs, "transformer ltx-2.5 distilled int8 "+grain.ID+"@0.8") {
		t.Fatal(p)
	}
	if p := planOf(reg, "mac", model.Run{Env: "mlx", Nodes: graph("int4")}); p.peak != -1 || p.needs != nil {
		t.Fatal("no memory item: no plan", p)
	}
	if p := planOf(reg, "gpu", run); p.peak != -1 {
		t.Fatal("no models key: no plan", p)
	}
}

// A run larger than its plan's planned_at (704 × 448, 49 frames) is planned at the whole budget, its own entries
// still reused: admission then makes room for all of it, not only for the default-size peak.
func TestPlanOfALargerRun(t *testing.T) {
	reg := &registry.Registry{}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", "ltx.models.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRegistry("mac.mlx.models", data); err != nil {
		t.Fatal(err)
	}
	qwen := []entry{
		newEntry("torch", registry.Resident{Component: "text_encoder", Model: "qwen-image-2.1", Variant: "base", Precision: "int8", GB: 9.28}, true),
		newEntry("torch", registry.Resident{Component: "transformer", Model: "qwen-image-2.1", Variant: "turbo", Precision: "int8", GB: 6.76}, true),
		newEntry("torch", registry.Resident{Component: "vae", Model: "qwen-image-2.1", Variant: "base", Precision: "bf16", GB: 0.63}, true),
	}
	ltxKept := newEntry("mlx", registry.Resident{Component: "transformer", Model: "ltx-2.5", Variant: "distilled", Precision: "int8", GB: 19.18}, true)
	for _, tc := range []struct {
		name          string
		width, height float64
		frames        any
		peak          int
		unload        []string // torch's victims at B = 52
	}{
		{"defaults", 704, 448, 49.0, 4587, []string{"text_encoder", "transformer"}},
		{"smaller", 448, 256, 49.0, 4587, []string{"text_encoder", "transformer"}},
		{"auto length", 704, 448, "auto", 4587, []string{"text_encoder", "transformer"}},
		{"larger canvas", 1280, 720, 49.0, -1, []string{"text_encoder", "transformer", "vae"}},
		{"longer clip", 704, 448, 121.0, -1, []string{"text_encoder", "transformer", "vae"}},
	} {
		nodes, _ := json.Marshal(map[string]registry.Node{
			"model":    {Kind: "ltx/model", Params: map[string]any{"variant": "distilled", "precision": "int8"}},
			"canvas":   {Kind: "canvas", Params: map[string]any{"width": tc.width, "height": tc.height}},
			"duration": {Kind: "ltx/duration", Params: map[string]any{"frames": tc.frames, "fps": 24.0}}})
		p := planOf(reg, "mac", model.Run{Env: "mlx", Nodes: nodes})
		if p.peak != tc.peak || !slices.Contains(p.needs, ltxKept.key) {
			t.Fatal(tc.name, p)
		}
		got := admit("mac", append(slices.Clone(qwen), ltxKept), 5200, "mlx", p.peak, p.needs, nil, "")
		var names []string
		for _, v := range got.unload["torch"] {
			names = append(names, v.res.Component)
		}
		if got.decision != "unload" || !slices.Equal(names, tc.unload) || len(got.unload["mlx"]) != 0 {
			t.Fatal(tc.name, got.decision, names, got.unload)
		}
	}
}

// Skip counts survive a batch pause: a held head keeps its count, so the bound is not reset by Pause / Resume; a run
// no longer waiting at all (released, ended, requeued) drops it.
func TestSkipCountsSurviveAHold(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	head := queued{run: model.Run{ID: "b-head", OwnerID: "b"}}
	steps := []struct {
		name   string
		passed bool   // a's resident run passed b's head over this pass
		state  string // b's head: queued, held or gone
		want   int
	}{
		{"passed over", true, "queued", 1},
		{"again", true, "queued", 2},
		{"again", true, "queued", 3},
		{"paused", false, "held", 3},
		{"still paused", false, "held", 3},
		{"resumed", true, "queued", 4},
		{"released", false, "gone", 0},
	}
	for i, step := range steps {
		decided := map[string][]string{}
		if step.passed {
			decided["a-"+strconv.Itoa(i)] = []string{head.run.ID}
		}
		snap := snapshot{held: map[string]bool{}}
		switch step.state {
		case "queued":
			snap.queue = []queued{head}
		case "held":
			snap.held[head.run.ID] = true
		}
		d.passOver(decided, snap, now.Add(time.Duration(i)*time.Second))
		if got := d.skipState()[head.run.ID]; got.n != step.want || step.want > 0 && !got.first.Equal(now) {
			t.Fatal(step.name, got)
		}
	}
}
