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
				ruleEnv{Providers: fixture.Providers, RemoteOn: fixture.Remote, Custom: fixture.Custom})
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
	for _, name := range []string{"valid.text-to-edit.json", "valid.judge-best-of-4.json", "valid.decide.json",
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
	if err := json.Unmarshal([]byte(`["g", {"pass": "a", "fail": "b", "max": 2}]`), &edge); err == nil {
		t.Error("a branch without then decoded")
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
