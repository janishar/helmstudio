package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture reads a canonical registry value from contracts/fixtures/registry.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", name+".valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The fake runner's values give the echo workflow with the schemas of its three kinds; a workflow with a kind its
// runner did not publish is left out.
func TestWorkflows(t *testing.T) {
	var r Registry
	if len(r.Workflows()) != 0 {
		t.Fatal("empty registry")
	}
	if err := r.SetRegistry("fake.fake.workflows", fixture(t, "workflows")); err != nil {
		t.Fatal(err)
	}
	if len(r.Workflows()) != 0 {
		t.Fatal("workflows without their node schemas are not runnable")
	}
	_ = r.SetRegistry("fake.fake.nodes", fixture(t, "nodes"))
	_ = r.SetRegistry("fake.fake.models", fixture(t, "models"))
	workflow, ok := r.Workflow("echo")
	if !ok || workflow.Env != "fake" || len(workflow.NodeSchemas) != 3 || workflow.NodeSchemas["echo/echo"].Params[0].Name != "steps" {
		t.Fatalf("%+v", workflow)
	}
	_ = r.SetRegistry("other.fake.workflows", []byte(`{"workflows": [{"id": "echo", "env": "fake", "nodes": {"a": {"kind": "prompt"}}},
		{"id": "lost", "env": "fake", "nodes": {"a": {"kind": "nope"}}}]}`))
	if list := r.Workflows(); len(list) != 1 || list[0].ID != "echo" {
		t.Fatalf("one echo, no lost: %+v", list)
	}
	if _, ok := r.Workflow("lost"); ok {
		t.Fatal("lost has no schema for its kind")
	}
}

// Place picks a live host of the env, idle first; a stale key is offline.
func TestPlace(t *testing.T) {
	var r Registry
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	put := func(key, state string, age time.Duration) {
		_ = r.SetHost(key, []byte(`{"host": "`+key[:len(key)-5]+`", "env": "fake", "state": "`+state+`", "updated_at": "`+
			now.Add(-age).Format(time.RFC3339)+`"}`))
	}
	if _, ok := r.Place("fake", now); ok {
		t.Fatal("no hosts")
	}
	put("aaaa.fake", "busy", 5*time.Second)
	put("bbbb.fake", "idle", 31*time.Second)
	if host, ok := r.Place("fake", now); !ok || host != "aaaa" {
		t.Fatalf("the live one: %s %v", host, ok)
	}
	put("cccc.fake", "idle", 0)
	if host, _ := r.Place("fake", now); host != "cccc" {
		t.Fatalf("idle first: %s", host)
	}
	if _, ok := r.Place("mlx", now); ok {
		t.Fatal("no host has mlx")
	}
	_ = r.SetHost("dddd.fake", []byte(`{"host": "dddd", "env": "fake", "state": "busy", "run_id": "r1", "updated_at": "`+
		now.Format(time.RFC3339)+`"}`))
	switch {
	case !r.Holds("dddd", "fake", "r1", false, now):
		t.Fatal("busy with the run")
	case !r.Holds("dddd", "fake", "r2", true, now):
		t.Fatal("a queued run waits behind another")
	case r.Holds("dddd", "fake", "r2", false, now):
		t.Fatal("a running run the runner is not on is lost")
	case r.Holds("cccc", "fake", "r1", true, now):
		t.Fatal("an idle runner holds no run")
	case r.Holds("aaaa", "fake", "r1", true, now) == false:
		t.Fatal("aaaa is busy: a queued run waits")
	case r.Holds("bbbb", "fake", "r1", true, now) || r.Holds("zzzz", "fake", "r1", true, now):
		t.Fatal("stale or unknown hosts hold nothing")
	}
}

// A value speaks only for its key's runner: a hosts value naming another host or env, and a workflow of another env
// or one whose id a runner of another env publishes, are refused. A deleted key is gone at once.
func TestValuesAreTiedToTheirKeys(t *testing.T) {
	var r Registry
	now := time.Now()
	if err := r.SetHost("fake.fake", []byte(`{"host": "gpu1", "env": "qwen", "state": "idle", "updated_at": "`+
		now.Format(time.RFC3339)+`"}`)); err == nil || len(r.Hosts()) != 0 {
		t.Fatal("a hosts value for another host was taken")
	}
	_ = r.SetHost("fake.fake", []byte(`{"host": "fake", "env": "fake", "state": "idle", "updated_at": "`+now.Format(time.RFC3339)+`"}`))
	if _, ok := r.Place("fake", now); !ok {
		t.Fatal("its own value")
	}
	_ = r.SetHost("fake.fake", nil)
	if _, ok := r.Place("fake", now); ok || len(r.Hosts()) != 0 {
		t.Fatal("a deleted hosts key is offline at once")
	}

	if err := r.SetRegistry("fake.fake.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "qwen", "nodes": {}}]}`)); err == nil {
		t.Fatal("a workflow of another env was taken")
	}
	_ = r.SetRegistry("gpu1.qwen.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "qwen", "nodes": {}}]}`))
	_ = r.SetRegistry("aaaa.fake.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "fake", "nodes": {}}]}`))
	if workflow, ok := r.Workflow("qwen.edit"); !ok || workflow.Env != "qwen" {
		t.Fatalf("another env took the id: %+v", workflow)
	}
	_ = r.SetRegistry("gpu1.qwen.workflows", nil)
	if _, ok := r.Workflow("qwen.edit"); ok {
		t.Fatal("a deleted registry key is gone")
	}
}

// The torch runner's canonical values give the qwen-image workflow; its hosts value carries memory and resident.
func TestQwenAndHostMemory(t *testing.T) {
	var r Registry
	_ = r.SetRegistry("mac.torch.nodes", fixture(t, "qwen-image.nodes"))
	_ = r.SetRegistry("mac.torch.workflows", fixture(t, "qwen-image.workflows"))
	workflow, ok := r.Workflow("qwen-image")
	if !ok || len(workflow.NodeSchemas) != 8 || workflow.Env != "torch" {
		t.Fatalf("%+v", workflow)
	}
	host3, _ := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "host.valid.3.json"))
	if err := r.SetHost("mac.torch", host3); err != nil {
		t.Fatal(err)
	}
	host := r.Hosts()[0]
	if host.Memory == nil || host.Memory.BudgetGB != 52 || len(host.Resident) != 2 || !host.Resident[0].Pinned ||
		host.Resident[0].Precision != "int8" {
		t.Fatalf("%+v", host)
	}
	// Busy with the run on a live key holds it however long a model load takes.
	if !r.Holds("mac", "torch", *host.RunID, false, host.UpdatedAt.Add(Stale)) {
		t.Fatal("a live runner busy with the run holds it")
	}
}
