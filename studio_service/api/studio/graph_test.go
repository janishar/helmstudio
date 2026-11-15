package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// graphFixtures is contracts/fixtures/graphs: the graph rules every implementation runs.
var graphFixtures = filepath.Join("..", "..", "..", "contracts", "fixtures", "graphs")

// fixturePalette reads palette.json as kind → palette node.
func fixturePalette(t *testing.T) map[string]registry.PaletteNode {
	t.Helper()
	var palette struct {
		Groups []struct {
			Nodes []registry.PaletteNode `json:"nodes"`
		} `json:"groups"`
	}
	data, err := os.ReadFile(filepath.Join(graphFixtures, "palette.json"))
	if err == nil {
		err = json.Unmarshal(data, &palette)
	}
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]registry.PaletteNode{}
	for _, group := range palette.Groups {
		for _, node := range group.Nodes {
			out[node.Kind] = node
		}
	}
	return out
}

// TestGraphFixtures runs every valid.* and invalid.* fixture: the set of (code, node, port, param) must be the
// expected one. autowire.* and wiredrop.* are the editor's (client) rules and are not run here.
func TestGraphFixtures(t *testing.T) {
	palette := fixturePalette(t)
	files, _ := filepath.Glob(filepath.Join(graphFixtures, "*valid.*.json"))
	if len(files) < 19 {
		t.Fatalf("found %d graph fixtures", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var fixture struct {
				Workflow dto.WorkflowDoc   `json:"workflow"`
				Problems []dto.Problem     `json:"problems"`
				Loras    map[string]string `json:"loras"`
			}
			data, _ := os.ReadFile(file)
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			if detail := fixture.Workflow.Validate(); detail != "" {
				t.Fatalf("not a WorkflowDoc: %s", detail)
			}
			key := func(p dto.Problem) string { return strings.Join([]string{p.Code, p.Node, p.Port, p.Param}, "|") }
			want, got := map[string]bool{}, map[string]bool{}
			for _, p := range fixture.Problems {
				want[key(p)] = true
			}
			if fixture.Loras == nil {
				fixture.Loras = map[string]string{}
			}
			problems := graphProblems(fixture.Workflow, palette, fixture.Loras)
			for _, p := range problems {
				got[key(p)] = true
				if p.Message == "" || (p.Blocks == "run") != runOnly[p.Code] {
					t.Errorf("problem %+v", p)
				}
			}
			if !maps.Equal(got, want) {
				t.Errorf("got %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
		})
	}
}

// TestHugeGraphIsCheapToRefuse: a chain far over MaxNodes gets only too_many_nodes, fast (no cycle walk per node).
func TestHugeGraphIsCheapToRefuse(t *testing.T) {
	nodes := map[string]registry.Node{"a0": {Kind: "x"}}
	for i := 1; i < 30000; i++ {
		nodes[fmt.Sprintf("a%d", i)] = registry.Node{Kind: "x", In: map[string]string{"i": fmt.Sprintf("a%d.o", i-1)}}
	}
	start := time.Now()
	problems := graphProblems(dto.WorkflowDoc{Title: "t", Family: "echo", Env: "fake", Nodes: nodes}, fixturePalette(t), nil)
	if len(problems) != 2 || problems[0].Code != "too_large" || problems[1].Code != "too_many_nodes" {
		t.Errorf("problems %+v", problems)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s", took)
	}
}

// TestSplice: a chain of bypassed nodes is followed to the real source, and they leave the graph.
func TestSplice(t *testing.T) {
	palette := fixturePalette(t)
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range palette {
		schemas[kind] = node.NodeSchema
	}
	nodes := map[string]registry.Node{
		"prompt":    {Kind: "prompt", Params: map[string]any{}, Position: &registry.Position{X: 1}},
		"enhance":   {Kind: "test/enhance", Params: map[string]any{}, In: map[string]string{"prompt": "prompt.text"}, Bypass: true},
		"enhance_2": {Kind: "test/enhance", Params: map[string]any{}, In: map[string]string{"prompt": "enhance.text"}, Bypass: true},
		"echo":      {Kind: "echo/echo", Params: map[string]any{}, In: map[string]string{"text": "enhance_2.text"}},
	}
	if schemas["test/enhance"].Outputs[0].Name != "text" {
		t.Fatalf("palette.json changed: %+v", schemas["test/enhance"])
	}
	splice(nodes, schemas)
	if len(nodes) != 2 || nodes["echo"].In["text"] != "prompt.text" || nodes["prompt"].Position != nil {
		t.Fatalf("spliced: %+v", nodes)
	}
}

// TestResolveGraph: an optional input left unwired, a wired param that cannot be set, bypass from the inputs.
func TestResolveGraph(t *testing.T) {
	palette := fixturePalette(t)
	workflow := registry.Workflow{ID: "w", Family: "test", Env: "fake", NodeSchemas: map[string]registry.NodeSchema{},
		Nodes: map[string]registry.Node{
			"noise":  {Kind: "test/noise"},
			"count":  {Kind: "test/count"},
			"mix":    {Kind: "test/mix", In: map[string]string{"a": "noise.image", "strength": "count.value"}},
			"output": {Kind: "output", In: map[string]string{"media": "mix.image"}},
		}}
	for _, node := range workflow.Nodes {
		workflow.NodeSchemas[node.Kind] = palette[node.Kind].NodeSchema
	}
	nodes, detail := resolve(workflow, nil)
	if detail != "" || nodes["mix"].In["b"] != "" || nodes["mix"].In["strength"] != "count.value" || nodes["output"].In["media"] != "mix.image" {
		t.Fatal(detail, nodes)
	}
	for body, want := range map[string]string{
		`{"mix.strength": 3}`:     "mix.strength: wired from count.value; it cannot be set",
		`{"mix.bypass": "yes"}`:   "mix.bypass: must be true or false",
		`{"noise.bypass": true}`:  "noise.bypass: noise cannot be bypassed",
		`{"ghost.bypass": true}`:  "ghost.bypass: not a param of this workflow",
		`{"mix.blend": 0.5}`:      "",
		`{"mix.bypass": false}`:   "",
		`{"count.value": 2}`:      "",
		`{"output.bypass": true}`: "output.bypass: output cannot be bypassed",
	} {
		if _, detail := resolve(workflow, inputs(t, body)); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
	nodes, detail = resolve(workflow, inputs(t, `{"mix.bypass": true}`))
	if _, kept := nodes["mix"]; detail != "" || kept || nodes["output"].In["media"] != "noise.image" {
		t.Fatal(detail, nodes)
	}
}
