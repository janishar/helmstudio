package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// scripts.json: a dialogue graph, the caller's elements and voices → expand_script.
func TestScriptFixtures(t *testing.T) {
	schemas := paletteSchemas(t)
	var fx struct {
		Workflows map[string]struct {
			Nodes map[string]registry.Node `json:"nodes"`
		} `json:"workflows"`
		Library []element                  `json:"library"`
		Voices  map[string]dto.LibraryItem `json:"voices"`
		Cases   []struct {
			Description, Workflow string
			Params                map[string]any
			Expect                json.RawMessage
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "voices", "scripts.json"))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) < 10 {
		t.Fatal(err)
	}
	voices := map[string]voiceItem{}
	for id, item := range fx.Voices {
		voices[id] = voiceOf(item)
	}
	for _, c := range fx.Cases {
		var nodes map[string]registry.Node
		raw, _ := json.Marshal(fx.Workflows[c.Workflow].Nodes)
		_ = json.Unmarshal(raw, &nodes)
		for k, v := range c.Params {
			id, name, _ := strings.Cut(k, ".")
			n := nodes[id]
			if n.Params == nil {
				n.Params = map[string]any{}
			}
			n.Params[name] = v
			nodes[id] = n
		}
		res, errs := expandScript(nodes, schemas, nodes, fx.Library, voices)
		var got any = map[string]any{"inputs": res.Inputs, "typed": res.Typed, "elements": res.Elements, "voices": res.Voices,
			"parents": res.Parents}
		if errs != nil {
			got = map[string]any{"errors": errs}
		}
		var g, w any
		gb, _ := json.Marshal(got)
		_ = json.Unmarshal(gb, &g)
		_ = json.Unmarshal(c.Expect, &w)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s:\n got  %s\n want %s", c.Description, gb, c.Expect)
		}
	}
}

func TestScriptCastOnlyLetsAScriptWriteReadTheLibrary(t *testing.T) {
	script, text := "script", "text"
	cases := []struct {
		node dto.PipelineNode
		tool string
		want bool
	}{
		{dto.PipelineNode{Kind: "write", Form: &script}, "library.read", true},
		{dto.PipelineNode{Kind: "write", Form: &script}, "rows.add", false},
		{dto.PipelineNode{Kind: "write", Form: &text}, "library.read", false},
		{dto.PipelineNode{Kind: "write"}, "library.read", false},
		{dto.PipelineNode{Kind: "decide", Form: &script}, "library.read", false},
	}
	for _, c := range cases {
		if got := scriptCast(c.node, c.tool); got != c.want {
			t.Errorf("scriptCast(%s %v, %s) = %v", c.node.Kind, c.node.Form, c.tool, got)
		}
	}
}
