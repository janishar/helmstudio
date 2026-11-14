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
