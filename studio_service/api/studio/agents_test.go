package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/registry"
)

// An ask run is planned with its workflow's own memory item (qwen-image-ask: the text encoder only), within the
// thumbnails it was planned at; more thumbnails, or another workflow, take the variant's general item.
func TestAskPlan(t *testing.T) {
	var reg registry.Registry
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", "qwen-image.models.valid.json"))
	if err == nil {
		err = reg.SetRegistry("mac.torch.models", data)
	}
	if err != nil {
		t.Fatal(err)
	}
	graph, _ := json.Marshal(map[string]registry.Node{"model": {Kind: "qwen-image/model",
		Params: map[string]any{"variant": "turbo", "precision": "int8"}}})
	thumbs := func(n int) *int { return &n }
	ask := model.Run{WorkflowID: "qwen-image-ask", Env: "torch", Nodes: graph, Thumbs: thumbs(4)}
	if got := planOf(&reg, "mac", ask); got.peak != 1700 || !slices.Equal(got.needs, []string{entryKey("text_encoder", "qwen-image-2.1", "base", "int8", nil)}) {
		t.Fatalf("%+v", got)
	}
	ask.Thumbs = thumbs(5) // more than planned_at.images: planned at the whole budget, its entries still reused
	if got := planOf(&reg, "mac", ask); got.peak != -1 || len(got.needs) != 1 {
		t.Fatalf("%+v", got)
	}
	still := model.Run{WorkflowID: "qwen-image", Env: "torch", Nodes: graph}
	if got := planOf(&reg, "mac", still); got.peak != 2920 || len(got.needs) < 2 {
		t.Fatalf("%+v", got)
	}
}

// A family ask runs on the reused step's merged model variant and precision; another family's ask keeps its own.
func TestAskModel(t *testing.T) {
	qwen := registry.Workflow{ID: "qwen-image", Family: "qwen-image", Nodes: map[string]registry.Node{
		"model": {Kind: "qwen-image/model", Params: map[string]any{"variant": "base", "precision": "bf16"}}}}
	ask := registry.Workflow{ID: "qwen-image-ask", Family: "qwen-image", Nodes: map[string]registry.Node{
		"model": {Kind: "qwen-image/model", Params: map[string]any{"variant": "base", "precision": "int8"}}, "ask": {Kind: "qwen-image/ask"}}}
	p := storedPipeline{Doc: dto.PipelineDoc{Nodes: map[string]dto.PipelineNode{
		"still": {Kind: "step", Workflow: "qwen-image", Fixed: map[string]any{"model.variant": "turbo"}},
		"judge": {Kind: "judge", Takes: "still"}, "check": {Kind: "decide"}}},
		Workflows: map[string]registry.Workflow{"qwen-image": qwen, "qwen-image-ask": ask},
		Presets:   map[string]map[string]any{"still": {"model.precision": "int8"}},
		Models:    map[string]string{"judge": "qwen-image-ask", "check": "llm-ask"}, Near: map[string]string{"judge": "still"}}
	if got := p.askModel("judge"); len(got) != 2 || got["model.variant"] != "turbo" || got["model.precision"] != "int8" {
		t.Fatal(got)
	}
	if got := p.askModel("check"); len(got) != 0 {
		t.Fatal(got)
	}
}

// A pick at a judge agrees when it is the suggestion as a set (reject all with nothing suggested too), and lands on
// the judge cell's summary.
func TestJudgePickAgreed(t *testing.T) {
	if !sameSet([]string{"b", "a"}, []string{"a", "b"}) || sameSet([]string{"a"}, []string{"b"}) || !sameSet(nil, []string{}) {
		t.Fatal("sameSet")
	}
	cells := json.RawMessage(`[{"node": "judge", "kind": "judge", "attempts": [{"visit": 1, "status": "waiting",
		"judged": {"verdict": "pass", "picked": 1, "agreed": null}}]}]`)
	agreed := false
	out := markGateCell(cells, "judge", 1, pickRecord{TakeIDs: []string{"x", "y"}}, &agreed)
	var list []struct {
		Attempts []struct {
			Status string
			Judged struct {
				Picked int
				Agreed *bool
			}
		}
	}
	_ = json.Unmarshal(out, &list)
	if a := list[0].Attempts[0]; a.Status != "picked" || a.Judged.Picked != 2 || a.Judged.Agreed == nil || *a.Judged.Agreed {
		t.Fatalf("%s", out)
	}
}

// A judge, write or decide key: the node's ask workflow, branch 0 or 1, the row's attempt, a visit within the bound.
func TestAskRunKey(t *testing.T) {
	p := storedPipeline{Doc: dto.PipelineDoc{Nodes: map[string]dto.PipelineNode{"judge": {Kind: "judge"}}},
		Workflows: map[string]registry.Workflow{"echo-ask": {ID: "echo-ask"}}, Models: map[string]string{"judge": "echo-ask"}}
	batch := "5a2e9c4b-1d7f-4b3a-9c8e-6f0d2a1b3c47"
	for key, ok := range map[string]bool{":1:1:judge:0:0": true, ":1:1:judge:3:1": true, ":1:1:judge:0:2": false,
		":1:2:judge:0:0": false, ":1:1:judge:32:0": false} {
		_, detail := pipelineStepRun(p, &message.RunsCreate{WorkflowID: "echo-ask", Row: 1, IdempotencyKey: batch + key}, 1)
		if (detail == "") != ok {
			t.Error(key, detail)
		}
	}
	if _, detail := pipelineStepRun(p, &message.RunsCreate{WorkflowID: "echo", Row: 1, IdempotencyKey: batch + ":1:1:judge:0:0"}, 1); detail == "" {
		t.Error("a step workflow for a judge")
	}
}

// The first step is found past a decide's routes edge too (a routes edge has no branch: it once panicked).
func TestFirstStepPastRoutes(t *testing.T) {
	var doc dto.PipelineDoc
	raw := `{"title": "t", "nodes": {"inputs": {"kind": "inputs", "inputs": []}, "check": {"kind": "decide"},
		"still": {"kind": "step"}, "out": {"kind": "output"}},
		"edges": [["inputs", "check"], ["check", {"routes": {"go": "still", "stop": "out"}, "max": 1, "then": "go"}],
		["still", "out"]]}`
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if got := (storedPipeline{Doc: doc}).firstStep(); got != "still" {
		t.Fatalf("firstStep = %q", got)
	}
}
