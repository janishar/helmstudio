package studio

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/registry"
)

// contracts/fixtures/video/rules.json (API 1.47): the expression language, every rule a filled canonical workflow
// breaks (resolve answers the first), and the run's own planned peak.
func TestRulesFixtures(t *testing.T) {
	var fx struct {
		Expressions []struct {
			Expr   string
			Values map[string]any
			Expect any
		}
		Cases []struct {
			Description, Workflow string
			Inputs                map[string]any
			Errors                []string
			PeakGB                *float64 `json:"peak_gb"`
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "video", "rules.json"))
	if err != nil || json.Unmarshal(data, &fx) != nil {
		t.Fatal(err)
	}
	for _, c := range fx.Expressions {
		got, err := evalExpr(c.Expr, func(ref string) (any, error) {
			if v, ok := c.Values[ref]; ok {
				return v, nil
			}
			return nil, errNoRef
		})
		if err != nil {
			got = "error"
		}
		if !reflect.DeepEqual(got, c.Expect) {
			t.Errorf("%q: got %v, want %v", c.Expr, got, c.Expect)
		}
	}
	for _, c := range fx.Cases {
		w := h3(t, c.Workflow)
		nodes := map[string]registry.Node{} // check.py filled: defaults, the workflow's values, then the inputs
		for id, n := range w.Nodes {
			params := map[string]any{}
			for _, p := range w.NodeSchemas[n.Kind].Params {
				params[p.Name] = p.Default
			}
			maps.Copy(params, n.Params)
			nodes[id] = registry.Node{Kind: n.Kind, Params: params, In: n.In}
		}
		for k, v := range c.Inputs {
			id, name, _ := strings.Cut(k, ".")
			nodes[id].Params[name] = v
		}
		if got := ruleErrors(nodes, w.NodeSchemas); !reflect.DeepEqual(got, c.Errors) && (len(got) > 0 || len(c.Errors) > 0) {
			t.Errorf("%s: got %q", c.Description, got)
		}
		resolved, detail := resolve(w, c.Inputs) // then the media checks (fake takes), then the rules
		if detail == "" {
			detail = firstRule(resolved, w.NodeSchemas)
		}
		if want := append(c.Errors, "")[0]; detail != want {
			t.Errorf("%s: answered %q, want %q", c.Description, detail, want)
		}
		if c.PeakGB != nil {
			got, ok := runPeak(nodes, w.NodeSchemas)
			if !ok || math.Round(got*100)/100 != *c.PeakGB {
				t.Errorf("%s: peak %v %v, want %v", c.Description, got, ok, *c.PeakGB)
			}
		}
	}
}

// Lineage lists an item's optional media (the soundtrack) after its video (API 1.47).
func TestParentsSoundtrackAfterVideo(t *testing.T) {
	w := h3(t, "minimax-h3-references")
	graph := `{"generate": {"kind": "minimax-h3/generate", "params": {"videos": [{"video": {"take_id": "v"},
		"sound": "replace", "soundtrack": {"take_id": "s"}}]}}}`
	if got := parents([]byte(graph), w.NodeSchemas); !reflect.DeepEqual(got, []string{"v", "s"}) {
		t.Fatal(got)
	}
}
