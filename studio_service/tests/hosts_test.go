// Integration tests of several hosts (contracts/README.md "Several hosts", "Hosts with a discrete GPU"): host mac
// (torch and mlx) and a cuda host gcp (torch) share one queue; a run records its host; a host that stops responding
// loses its run to another host (or fails it), and its model and media jobs fail host_lost; a read-only models root
// refuses model jobs.
package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio"
)

// gcp publishes host gcp's torch key as a cuda runner (94 GB of VRAM budget, 176 GB of host RAM) refreshed at, holding
// resident, with the qwen-image registry values; extra overrides fields.
func (e *env) gcp(at time.Time, resident []map[string]any, extra map[string]any) {
	e.t.Helper()
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "qwen-image."+kind+".valid.json"))
		if err == nil {
			err = e.registry.SetRegistry("gcp.torch."+kind, data)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	value := map[string]any{"host": "gcp", "env": "torch", "state": "idle", "run_id": nil, "updated_at": at.UTC(),
		"resident": resident, "memory": map[string]any{"budget_gb": 94, "used_gb": 1.2},
		"host_ram":    map[string]any{"budget_gb": 176, "used_gb": 3.5},
		"device":      map[string]any{"kind": "cuda", "name": "NVIDIA RTX PRO 6000", "memory_total_gb": 95.6, "capability": "12.0"},
		"models_disk": map[string]any{"root": "/models", "free_bytes": 1000, "total_bytes": 5000, "writable": false}}
	for k, v := range extra {
		value[k] = v
	}
	raw, _ := json.Marshal(value)
	if err := e.registry.SetHost("gcp.torch", raw); err != nil {
		e.t.Fatal(err)
	}
}

// Two free hosts take two runs in one pass: the run resident on mac goes there, the next is lined up again on gcp
// (mac is busy now) with gcp's own budget; the take records its host; GET /hosts and the Models screen show both.
func TestTwoHostsShareTheQueue(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	e.mac("torch", qwenResident)
	e.gcp(time.Now(), nil, nil)
	a1, _ := e.start(userA, "qwen-image", turbo)
	b1, sessionB := e.start(userB, "qwen-image", turbo)
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	if got := e.gatePass(d, time.Now()); !slices.Equal(got, []string{"cmd.run.mac.torch " + a1, "cmd.run.gcp.torch " + b1}) {
		t.Fatal(got)
	}
	if budgets[b1] != float64(94) {
		t.Fatal("gcp's budget, nothing of mac's counted:", budgets[b1])
	}
	if run := e.call(userB, "GET", "/v1/studio/runs/"+b1, nil, 200); run["host"] != "gcp" || run["lost_host"] != nil {
		t.Fatal(run)
	}
	e.emit(userB, sessionB, b1, "status", 1, map[string]any{"status": "running", "host": "gcp"})
	e.emit(userB, sessionB, b1, "output", 2, map[string]any{"index": 0, "key": "u/" + userB + "/runs/" + b1 + "/1/0.png",
		"kind": "image", "bytes": 5, "width": 64, "height": 64})
	if n := e.count(`SELECT count(*) FROM takes WHERE run_id = $1 AND made_with->>'host' = 'gcp'`, b1); n != 1 {
		t.Fatal("made_with.host:", n)
	}

	kinds := map[string]string{}
	for _, h := range e.call(userA, "GET", "/v1/studio/hosts", nil, 200)["hosts"].([]any) {
		host := h.(map[string]any)
		if m, ok := host["host_memory"].(map[string]any); ok {
			kinds[host["host"].(string)] = m["kind"].(string)
		}
		if host["host"] == "gcp" && (host["device"].(map[string]any)["kind"] != "cuda" ||
			host["host_ram"].(map[string]any)["budget_gb"] != float64(176) || host["host_memory"].(map[string]any)["budget_gb"] != float64(94)) {
			t.Fatal(host)
		}
	}
	if kinds["gcp"] != "vram" || kinds["mac"] != "unified" {
		t.Fatal(kinds)
	}
	for _, host := range []string{"mac", "gcp"} {
		if got := e.variantOn(userA, "qwen-image-2.1", "turbo", host); got["state"] != "present" {
			t.Fatal(host, got)
		}
	}
}

// gcp stops responding with a run: attempt 2 is lined up on mac (lost_host gcp) and released there; a second host
// loss fails it as before.
func TestHostLossRetriesOnAnotherHost(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	e.gcp(time.Now(), qwenResident, nil)
	runID, sessionID := e.start(userA, "qwen-image", turbo)
	var purged []string
	d := studio.NewDispatcher(e.pool, e.registry, func(_ context.Context, subject string) error {
		purged = append(purged, subject)
		return nil
	}, nil)
	now := time.Now()
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.gcp.torch " + runID}) {
		t.Fatal("resident on gcp:", got)
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "gcp"})
	e.gcp(now.Add(-2*time.Minute), qwenResident, nil) // preempted: the key goes stale
	e.gatePass(d, now)
	if got := e.gatePass(d, now.Add(studio.Confirm)); len(got) != 0 || !slices.Equal(purged, []string{"cmd.run.gcp.torch"}) {
		t.Fatal(got, purged)
	}
	if got := e.gatePass(d, now.Add(studio.Confirm)); !slices.Equal(got, []string{"cmd.run.mac.torch " + runID}) {
		t.Fatal("attempt 2 goes to mac:", got)
	}
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	if run["attempt"] != float64(2) || run["host"] != "mac" || run["lost_host"] != "gcp" {
		t.Fatal(run)
	}
}

// With no other eligible host, attempt 2 fails with the lost host's text.
func TestHostLossWithNoOtherHost(t *testing.T) {
	e := setup(t)
	e.gcp(time.Now(), nil, nil)
	runID, sessionID := e.start(userA, "qwen-image", turbo)
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	now := time.Now()
	if got := e.gatePass(d, now); !slices.Equal(got, []string{"cmd.run.gcp.torch " + runID}) {
		t.Fatal(got)
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "gcp"})
	e.gcp(now.Add(-2*time.Minute), nil, nil)
	e.gatePass(d, now)
	e.gatePass(d, now.Add(studio.Confirm)) // lost: attempt 2 queued
	e.gatePass(d, now.Add(studio.Confirm))
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	if run["status"] != "failed" || run["error"] != "runner on host gcp stopped responding; no other live host for env torch" ||
		run["lost_host"] != "gcp" {
		t.Fatal(run)
	}
}

// A released model job on a host that stopped responding fails host_lost with its command purged; a read-only models
// root refuses a download and a delete.
func TestModelJobsOnALostOrReadOnlyHost(t *testing.T) {
	e := setup(t)
	e.modelsSetup("missing")
	job := e.call(admin, "POST", "/v1/studio/admin/models/qwen-image-2.1/download", map[string]any{"host": "mac",
		"variant": "turbo", "accept_licence": true}, 202)["id"].(string)
	if subjects, _, _ := e.release(); len(subjects) != 1 {
		t.Fatal(subjects)
	}
	e.modelHost("torch", time.Now().Add(-2*time.Minute), nil, nil)
	var purged []string
	sweep := func(now time.Time) {
		if err := studio.ReleaseModelJobs(context.Background(), e.pool, e.registry, func(_ context.Context, subject string) error {
			purged = append(purged, subject)
			return nil
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	sweep(time.Now()) // heard within 60 s (a clean restart deletes the key, then reports again): no loss yet
	if got := e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+job, nil, 200); got["state"] == "failed" || len(purged) > 0 {
		t.Fatal(got, purged)
	}
	sweep(time.Now().Add(studio.StaleJobKey + time.Second))
	got := e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+job, nil, 200)
	if failure, _ := got["failure"].(map[string]any); got["state"] != "failed" || failure["class"] != "host_lost" ||
		failure["detail"] != "host mac stopped responding" || !slices.Equal(purged, []string{"cmd.model.mac.torch"}) {
		t.Fatal(got, purged)
	}

	e.modelHost("torch", time.Now(), nil, map[string]any{"models_disk": map[string]any{"root": "/models", "free_bytes": 1,
		"total_bytes": 2, "writable": false}})
	for _, call := range []struct{ method, path string }{{"POST", "/v1/studio/admin/models/qwen-image-2.1/download"},
		{"DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=base"}} {
		got := e.call(admin, call.method, call.path, map[string]any{"host": "mac", "variant": "turbo", "accept_licence": true}, 409)
		if got["detail"] != "models root on mac is read-only" {
			t.Fatal(call, got)
		}
	}
}
