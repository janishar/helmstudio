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

// Publishes and Model answer per runner; a model's variant counts only when present, and a model without variants
// always has it.
func TestPublishesAndModels(t *testing.T) {
	var r Registry
	_ = r.SetRegistry("mac.torch.workflows", fixture(t, "qwen-image.workflows"))
	_ = r.SetRegistry("mac.torch.models", []byte(`{"models": [{"id": "qwen-image-2.1", "family": "qwen-image", "variants": [
		{"id": "base", "presence": "present", "precisions": ["bf16"]}, {"id": "turbo", "presence": "missing", "precisions": ["bf16"]}]}]}`))
	_ = r.SetRegistry("fake.fake.models", fixture(t, "models"))
	if !r.Publishes("mac.torch", "qwen-image") || r.Publishes("mac.torch", "echo") || r.Publishes("gpu.torch", "qwen-image") {
		t.Fatal("Publishes")
	}
	qwen, ok := r.Model("mac.torch", "qwen-image")
	if !ok || qwen.ID != "qwen-image-2.1" || !qwen.Has("base") || qwen.Has("turbo") || qwen.Has("nope") {
		t.Fatalf("%+v", qwen)
	}
	if echo, ok := r.Model("fake.fake", "echo"); !ok || !echo.Has("anything") {
		t.Fatalf("%+v", echo)
	}
	if _, ok := r.Model("mac.torch", "echo"); ok {
		t.Fatal("no echo on mac")
	}
	_ = r.SetRegistry("mac.torch.models", nil)
	if _, ok := r.Model("mac.torch", "qwen-image"); ok {
		t.Fatal("a deleted models key is gone")
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
	if hosts := r.Hosts(); len(hosts) != 1 || !hosts[0].Live(now) || hosts[0].Live(now.Add(Stale+time.Second)) {
		t.Fatal("its own value, live until Stale")
	}
	_ = r.SetHost("fake.fake", nil)
	if len(r.Hosts()) != 0 {
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
	if host.RunID == nil || host.Attempt != nil {
		t.Fatalf("busy with a run, attempt absent: %+v", host)
	}
}
