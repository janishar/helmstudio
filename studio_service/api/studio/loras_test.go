package studio

import (
	"encoding/json"
	"testing"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/registry"
)

// Bake rows are listed as the runner keys a baked component: graph order (an upstream stack first, whatever the node
// ids), one LoRA twice once with the summed strength; adapter rows are left out.
func TestBakedIsInGraphOrderAndMerged(t *testing.T) {
	graph := map[string]registry.Node{
		"model":  {Kind: "qwen-image/model"},
		"style":  {Kind: "qwen-image/lora-stack", In: map[string]string{"model": "model.model"}},
		"detail": {Kind: "qwen-image/lora-stack", In: map[string]string{"model": "style.model"}},
	}
	used := []lora.Used{ // node id order, as eachLora lists them
		{Node: "detail", ID: "b", Strength: 0.5, Mode: "bake"},
		{Node: "detail", ID: "a", Strength: 0.25, Mode: "bake"},
		{Node: "style", ID: "a", Strength: 0.5, Mode: "bake"},
		{Node: "style", ID: "c", Strength: 1, Mode: "adapter"},
	}
	got, _ := json.Marshal(baked(used, graph))
	want := `[{"lora_id":"a","name":null,"strength":0.75,"bytes":0},{"lora_id":"b","name":null,"strength":0.5,"bytes":0}]`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if baked(nil, graph) != nil {
		t.Fatal("no bake rows: none")
	}
}

// A workflow's rows items are filled with their defaults, as inputs are: a saved row without strength or mode runs
// at strength 1, mode adapter.
func TestResolveFillsWorkflowRowItems(t *testing.T) {
	workflow := qwen(t)
	node := workflow.Nodes["lora"]
	node.Params = map[string]any{"rows": []any{map[string]any{"lora": map[string]any{"lora_id": "3f0a" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"}}}}
	workflow.Nodes["lora"] = node
	nodes, detail := resolve(workflow, nil)
	if detail != "" {
		t.Fatal(detail)
	}
	item := nodes["lora"].Params["rows"].([]any)[0].(map[string]any)
	if item["strength"] != float64(1) || item["mode"] != "adapter" {
		t.Fatalf("%#v", item)
	}
	node.Params = map[string]any{"rows": []any{map[string]any{"strength": 1.0}}}
	workflow.Nodes["lora"] = node
	if _, detail := resolve(workflow, nil); detail != "lora.rows[1].lora: a LoRA is required" {
		t.Fatalf("%q", detail)
	}
}

// A LoRA the family's block lists as prefused for the graph's model variant is refused (it would apply twice); on
// another variant it is fine. A bake-only stack refuses mode adapter by its enum.
func TestPrefusedLoraIsRefused(t *testing.T) {
	workflow := ltxWorkflow(t, "ltx-text-to-video")
	row := `{"lora.rows": [{"lora": {"lora_id": "` + sixtyFour + `"}, "strength": 0.5}]}`
	nodes, detail := resolve(workflow, inputs(t, row))
	if detail != "" || nodes["lora"].Params["rows"].([]any)[0].(map[string]any)["mode"] != "bake" {
		t.Fatal(detail)
	}
	usable := map[string]lora.Info{sixtyFour: {ID: sixtyFour, Source: "shared", Name: "distilled-lora", Family: "ltx"}}
	blocks := map[string]registry.LoraBlock{"ltx": {Prefused: []registry.Prefused{{LoraID: sixtyFour, Variant: "distilled"}}}}
	values := eachLora(nodes, workflow.NodeSchemas)
	if _, detail := checkLoras(values, nodes, blocks, usable); detail != "lora.rows[1].lora: LoRA distilled-lora is already part of the distilled weights" {
		t.Fatalf("%q", detail)
	}
	nodes["model"].Params["variant"] = "dev"
	if used, detail := checkLoras(values, nodes, blocks, usable); detail != "" || len(used) != 1 || used[0].Mode != "bake" {
		t.Fatal(detail, used)
	}
}
