package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/registry"
)

// made_with carries every param of the graph the run was sent with, the used seed in place of a random one, the
// prompt and the model (its id from the host's models); without the registry it keeps the inputs as requested.
func TestMadeWith(t *testing.T) {
	workflows := &registry.Registry{}
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", "qwen-image."+kind+".valid.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := workflows.SetRegistry("mac.torch."+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	workflow, ok := workflows.Workflow("qwen-image")
	if !ok {
		t.Fatal("no qwen-image workflow in the fixtures")
	}
	inputs := map[string]any{"prompt.text": "Fox at dusk", "seed.seed": -1.0}
	nodes, detail := resolve(workflow, inputs)
	if detail != "" {
		t.Fatal(detail)
	}
	rawInputs, _ := json.Marshal(inputs)
	rawNodes, _ := json.Marshal(nodes)
	host, seed := "mac", int64(43)
	run := model.Run{WorkflowID: "qwen-image", Env: "torch", Host: &host, Inputs: rawInputs, Nodes: rawNodes}

	got := madeWith(workflows, run, &seed)
	if got.WorkflowTitle != workflow.Title || got.Prompt == nil || *got.Prompt != "Fox at dusk" || *got.Seed != 43 ||
		got.Inputs["seed.seed"] != int64(43) || got.Inputs["prompt.text"] != "Fox at dusk" || got.Inputs["sample.steps"] == nil ||
		got.Model == nil || got.Model.ID == nil || *got.Model.ID != "qwen-image-2.1" || *got.Model.Variant != "base" {
		t.Fatalf("%+v %+v", got, got.Model)
	}
	run.Nodes = nil // a run stored before it kept its graph: resolved again
	if again := madeWith(workflows, run, &seed); again.Inputs["seed.seed"] != int64(43) || again.Inputs["sample.steps"] == nil {
		t.Fatalf("%+v", again)
	}
	bare := madeWith(nil, run, &seed)
	if bare.WorkflowTitle != "qwen-image" || bare.Model != nil || *bare.Prompt != "Fox at dusk" || len(bare.Inputs) != 2 {
		t.Fatalf("%+v", bare)
	}
}
