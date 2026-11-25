package studio

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// pipelineFixtures is contracts/fixtures/pipelines: the pipeline rules every implementation runs.
var pipelineFixtures = filepath.Join("..", "..", "..", "contracts", "fixtures", "pipelines")

// fixtureWorkflows reads workflows.json: the workflows the caller can run.
func fixtureWorkflows(t *testing.T) map[string]dto.WorkflowDoc {
	t.Helper()
	var file struct {
		Workflows map[string]dto.WorkflowDoc `json:"workflows"`
	}
	data, err := os.ReadFile(filepath.Join(pipelineFixtures, "workflows.json"))
	if err == nil {
		err = json.Unmarshal(data, &file)
	}
	if err != nil {
		t.Fatal(err)
	}
	for id, wf := range file.Workflows {
		if detail := wf.Validate(); detail != "" {
			t.Fatalf("workflows.json %s: %s", id, detail)
		}
		file.Workflows[id] = wf
	}
	return file.Workflows
}

// fixtureCustom reads the shipped custom nodes (fixtures/registry/pipelines.custom.valid.json).
func fixtureCustom(t *testing.T) []registry.CustomNode {
	t.Helper()
	var file struct {
		Nodes []registry.CustomNode `json:"nodes"`
	}
	data, err := os.ReadFile(filepath.Join(pipelineFixtures, "..", "registry", "pipelines.custom.valid.json"))
	if err == nil {
		err = json.Unmarshal(data, &file)
	}
	if err != nil {
		t.Fatal(err)
	}
	return file.Nodes
}

// TestPipelineFixtures runs every valid.* and invalid.* fixture: the set of (code, node, port, param) must be the
// expected one, each problem with a message and the right blocks.
func TestPipelineFixtures(t *testing.T) {
	palette, workflows := fixturePalette(t), fixtureWorkflows(t)
	files, _ := filepath.Glob(filepath.Join(pipelineFixtures, "*valid.*.json"))
	if len(files) < 10 {
		t.Fatalf("found %d pipeline fixtures", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var fixture struct {
				Pipeline  dto.PipelineDoc         `json:"pipeline"`
				Problems  []dto.Problem           `json:"problems"`
				Looks     []lookItem              `json:"looks"`
				Loras     map[string]string       `json:"loras"`
				Bounds    *dto.PipelineBounds     `json:"bounds"`
				Models    map[string]string       `json:"models"`
				Egress    *[]dto.PipelineEgress   `json:"egress"`
				Reserved  *[]dto.PipelineReserved `json:"reserved"`
				Providers []providerInfo          `json:"providers"`
				Remote    bool                    `json:"remote_deployment"`
				AutoModel string                  `json:"auto_model"`
				Custom    []registry.CustomNode   `json:"custom"`
			}
			data, _ := os.ReadFile(file)
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			if detail := fixture.Pipeline.Validate(); detail != "" {
				t.Fatalf("not a PipelineDoc: %s", detail)
			}
			key := func(p dto.Problem) string { return strings.Join([]string{p.Code, p.Node, p.Port, p.Param}, "|") }
			want, got := map[string]bool{}, map[string]bool{}
			for _, p := range fixture.Problems {
				want[key(p)] = true
			}
			if fixture.Custom == nil {
				fixture.Custom = fixtureCustom(t)
			}
			problems, info := pipelineProblems(fixture.Pipeline, workflows, palette, fixture.Looks,
				func(string) map[string]string { return fixture.Loras },
				ruleEnv{Providers: fixture.Providers, RemoteOn: fixture.Remote, Custom: fixture.Custom, Auto: fixture.AutoModel})
			for _, p := range problems {
				got[key(p)] = true
				if p.Message == "" || (p.Blocks == "run") != pipelineRunOnly[p.Code] {
					t.Errorf("problem %+v", p)
				}
			}
			if !maps.Equal(got, want) {
				t.Errorf("got %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
			if fixture.Bounds != nil && !reflect.DeepEqual(info.Bounds, *fixture.Bounds) {
				t.Errorf("bounds %+v, want %+v", info.Bounds, *fixture.Bounds)
			}
			if fixture.Models != nil && !maps.Equal(info.Models, fixture.Models) {
				t.Errorf("models %v, want %v", info.Models, fixture.Models)
			}
			if fixture.Egress != nil && mustJSON(info.Egress) != mustJSON(*fixture.Egress) {
				t.Errorf("egress %s, want %s", mustJSON(info.Egress), mustJSON(*fixture.Egress))
			}
			if fixture.Reserved != nil && mustJSON(info.Reserved) != mustJSON(*fixture.Reserved) {
				t.Errorf("reserved %s, want %s", mustJSON(info.Reserved), mustJSON(*fixture.Reserved))
			}
		})
	}
}

// TestPipelineDocRoundTrip: a document decodes and encodes to the same JSON (stored documents are never reshaped), and
// a field of another kind is refused.
func TestPipelineDocRoundTrip(t *testing.T) {
	for _, name := range []string{"valid.text-to-edit.json", "valid.judge-best-of-4.json", "valid.judge-ask-me.json", "valid.decide.json",
		"valid.director-shots.json", "valid.server-remote.json", "valid.custom.json"} {
		data, _ := os.ReadFile(filepath.Join(pipelineFixtures, name))
		var fixture struct {
			Pipeline json.RawMessage `json:"pipeline"`
		}
		_ = json.Unmarshal(data, &fixture)
		var doc dto.PipelineDoc
		if err := json.Unmarshal(fixture.Pipeline, &doc); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(doc)
		var a, b any
		_ = json.Unmarshal(fixture.Pipeline, &a)
		_ = json.Unmarshal(again, &b)
		if x, y := mustJSON(a), mustJSON(b); x != y {
			t.Fatalf("%s:\n%s\n%s", name, x, y)
		}
	}
	for _, bad := range []string{`{"kind": "gate", "takes": "s", "workflow": "echo"}`, `{"kind": "agent", "instruction": "x", "takes": "s"}`, `{"kind": "judge", "takes": "s", "criteria": {"must": ["a"]}, "to": "$x"}`,
		`{"kind": "inputs", "inputs": {"a": 1}}`} {
		var node dto.PipelineNode
		if err := json.Unmarshal([]byte(bad), &node); err == nil {
			t.Errorf("%s decoded", bad)
		}
	}
	var edge dto.PipelineEdge
	// API 1.28: a branch may leave then out (a judge's Ask me; a gate's is edge_invalid), but never send it empty
	if err := json.Unmarshal([]byte(`["g", {"pass": "a", "fail": "b", "max": 2}]`), &edge); err != nil || edge.Branch.Then != "" {
		t.Error("a branch without then refused", err)
	} else if err := json.Unmarshal([]byte(`["g", {"pass": "a", "fail": "b", "max": 2, "then": ""}]`), &edge); err == nil {
		t.Error("a branch with an empty then decoded")
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// A bound take list into rows of one media field (API 1.32: H3's audios) takes that field, not `image`.
func TestMediaRowItems(t *testing.T) {
	item, _ := json.Marshal(map[string]any{"type": "media", "accepts": []string{"audio"}})
	wf := registry.Workflow{Nodes: map[string]registry.Node{"generate": {Kind: "k"}}, NodeSchemas: map[string]registry.NodeSchema{
		"k": {Params: []registry.Param{{Name: "audios", Type: "rows", Item: map[string]json.RawMessage{"audio": item}}}}}}
	inputs := map[string]any{"generate.audios": []any{map[string]any{"image": map[string]any{"take_id": "t"}}}}
	mediaRowItems(wf, inputs)
	if got, _ := json.Marshal(inputs); string(got) != `{"generate.audios":[{"audio":{"take_id":"t"}}]}` {
		t.Fatal(string(got))
	}
}

// TestPipelineModelPutValidate: the pipelines default model's modes (API 1.74), a provider only with remote.
func TestPipelineModelPutValidate(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, c := range []struct {
		body dto.PipelineModelPut
		ok   bool
	}{{dto.PipelineModelPut{Mode: "local"}, true}, {dto.PipelineModelPut{Mode: "assistant"}, true},
		{dto.PipelineModelPut{Mode: "remote", Provider: s("ollama")}, true}, {dto.PipelineModelPut{Mode: "remote", Provider: s("ollama"), Model: s("m")}, true},
		{dto.PipelineModelPut{Mode: "agent"}, false}, {dto.PipelineModelPut{Mode: "remote"}, false},
		{dto.PipelineModelPut{Mode: "local", Provider: s("ollama")}, false}, {dto.PipelineModelPut{Mode: "assistant", Model: s("m")}, false},
		{dto.PipelineModelPut{Mode: "remote", Provider: s("Bad Name")}, false}, {dto.PipelineModelPut{Mode: "remote", Provider: s("x"), Model: s("")}, false}} {
		if got := c.body.Validate(); (got == "") != c.ok {
			t.Errorf("%+v: %q", c.body, got)
		}
	}
}
