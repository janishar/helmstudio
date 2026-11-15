package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/registry"
)

// mentionFixtures is contracts/fixtures/mentions: the mention rules every implementation runs.
var mentionFixtures = filepath.Join("..", "..", "..", "contracts", "fixtures", "mentions")

func TestScanFixtures(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Text  string `json:"text"`
			Parts []part `json:"parts"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(mentionFixtures, "scan.json"))
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil || len(fixture.Cases) < 12 {
		t.Fatal(err, len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		got := scan(c.Text)
		joined := ""
		for _, p := range got {
			joined += p.Text + p.Raw
		}
		if !reflect.DeepEqual(got, c.Parts) || joined != c.Text {
			t.Errorf("%q: got %+v, want %+v", c.Text, got, c.Parts)
		}
	}
}

func TestResolveFixtures(t *testing.T) {
	palette := fixturePalette(t)
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range palette {
		schemas[kind] = node.NodeSchema
	}
	files, _ := filepath.Glob(filepath.Join(mentionFixtures, "resolve.*.json"))
	if len(files) < 10 {
		t.Fatalf("found %d resolve fixtures", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var fixture struct {
				Workflow struct {
					Nodes map[string]registry.Node `json:"nodes"`
				} `json:"workflow"`
				Library []element         `json:"library"`
				Sizes   map[string][2]int `json:"sizes"`
				Looks   []lookItem        `json:"looks"`
				Chosen  []string          `json:"chosen"`
				Loras   map[string]string `json:"loras"`
				Expect  struct {
					Inputs   map[string]any               `json:"inputs"`
					Typed    map[string]any               `json:"typed"`
					Elements []gallerydto.MadeWithElement `json:"elements"`
					Looks    []gallerydto.MadeWithLook    `json:"looks"`
					Errors   []string                     `json:"errors"`
				} `json:"expect"`
			}
			data, _ := os.ReadFile(file)
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			got, errs := expandMentions(fixture.Workflow.Nodes, schemas, fixture.Library, fixture.Sizes, fixture.Looks,
				fixture.Chosen, fixture.Loras)
			want := fixture.Expect
			if len(want.Errors) > 0 || len(errs) > 0 {
				if !reflect.DeepEqual(errs, want.Errors) {
					t.Fatalf("errors %q, want %q", errs, want.Errors)
				}
				return
			}
			for _, m := range []*map[string]any{&want.Inputs, &want.Typed} {
				if *m == nil {
					*m = map[string]any{}
				}
			}
			if want.Elements == nil {
				want.Elements = []gallerydto.MadeWithElement{}
			}
			if want.Looks == nil {
				want.Looks = []gallerydto.MadeWithLook{}
			}
			// compare as JSON decodes it
			norm := func(v any) any {
				data, _ := json.Marshal(v)
				var out any
				_ = json.Unmarshal(data, &out)
				return out
			}
			if !reflect.DeepEqual(norm(got.Inputs), norm(want.Inputs)) || !reflect.DeepEqual(norm(got.Typed), norm(want.Typed)) ||
				!reflect.DeepEqual(got.Elements, want.Elements) || !reflect.DeepEqual(got.Looks, want.Looks) {
				t.Fatalf("got %s %s\nwant %s", norm(got.Inputs), norm(got), norm(map[string]any{"inputs": want.Inputs,
					"typed": want.Typed, "elements": want.Elements, "looks": want.Looks}))
			}
		})
	}
}

// TestFitFixtures runs fit.json: an item against a workflow → the reason it does not fit, or null.
func TestFitFixtures(t *testing.T) {
	palette := fixturePalette(t)
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range palette {
		schemas[kind] = node.NodeSchema
	}
	var fixture struct {
		Workflows map[string]struct {
			Nodes map[string]registry.Node `json:"nodes"`
		} `json:"workflows"`
		Cases []struct {
			Description string            `json:"description"`
			Workflow    string            `json:"workflow"`
			Item        json.RawMessage   `json:"item"`
			Loras       map[string]string `json:"loras"`
			Expect      *string           `json:"expect"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(mentionFixtures, "fit.json"))
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil || len(fixture.Cases) < 14 {
		t.Fatal(err, len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		var item lookItem
		var kind struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(c.Item, &item)
		_ = json.Unmarshal(c.Item, &kind)
		got, want := lookFit(fixture.Workflows[c.Workflow].Nodes, schemas, kind.Kind, item, c.Loras), ""
		if c.Expect != nil {
			want = *c.Expect
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", c.Description, got, want)
		}
	}
}

// A batch's failing rows over 10 are counted, not listed.
func TestJoinRows(t *testing.T) {
	var rows []string
	for range 12 {
		rows = append(rows, "row")
	}
	if got := joinRows(rows); !strings.HasSuffix(got, "row; and 2 more rows") || strings.Count(got, "row") != 11 {
		t.Fatal(got)
	}
}

// autoSize rounds as the runner does (Python round: half to even).
func TestAutoSize(t *testing.T) {
	for _, c := range [][4]int{{1920, 1080, 1376, 768}, {64, 32, 1440, 736}, {4225, 4096, 1024, 1024}} {
		if w, h := autoSize(c[0], c[1]); w != c[2] || h != c[3] {
			t.Errorf("%dx%d: got %dx%d", c[0], c[1], w, h)
		}
	}
}
