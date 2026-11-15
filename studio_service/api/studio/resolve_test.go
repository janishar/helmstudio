package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/registry"
)

// canonical is a canonical workflow from contracts/fixtures/registry: prefix "" (echo, fake.fake) or "qwen-image."
// (mac.torch).
func canonical(t *testing.T, prefix, source, id string) registry.Workflow {
	t.Helper()
	var r registry.Registry
	for _, kind := range []string{"nodes", "workflows"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", prefix+kind+".valid.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SetRegistry(source+"."+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	workflow, ok := r.Workflow(id)
	if !ok {
		t.Fatal("no workflow " + id)
	}
	return workflow
}

func echo(t *testing.T) registry.Workflow { return canonical(t, "", "fake.fake", "echo") }

func qwen(t *testing.T) registry.Workflow {
	return canonical(t, "qwen-image.", "mac.torch", "qwen-image")
}

// inputs decodes JSON as a request body would be.
func inputs(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every param is filled: schema defaults, the workflow's values, then the inputs; the wiring is kept.
func TestResolveFillsDefaults(t *testing.T) {
	nodes, detail := resolve(echo(t), inputs(t, `{"prompt.text": "a red fox", "echo.steps": 20}`))
	if detail != "" {
		t.Fatal(detail)
	}
	want := `{"echo":{"kind":"echo/echo","params":{"delay_ms":300,"fail":false,"refs":[],"steps":20},"in":{"text":"prompt.text"}},` +
		`"output":{"kind":"output","params":{},"in":{"media":"echo.image"}},"prompt":{"kind":"prompt","params":{"text":"a red fox"}}}`
	if got, _ := json.Marshal(nodes); string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	nodes, _ = resolve(echo(t), nil)
	if nodes["echo"].Params["steps"] != float64(10) || nodes["prompt"].Params["text"] != "" {
		t.Fatalf("defaults: %+v", nodes)
	}
}

// A bad input is a 422 detail naming the key; the first bad key in key order wins.
func TestResolveRejects(t *testing.T) {
	for body, want := range map[string]string{
		`{"echo.steps": 0}`:                       "echo.steps: must be between 1 and 200",
		`{"echo.steps": 201}`:                     "echo.steps: must be between 1 and 200",
		`{"echo.steps": 2.5}`:                     "echo.steps: must be a whole number",
		`{"echo.steps": "3"}`:                     "echo.steps: must be a whole number",
		`{"echo.fail": "yes"}`:                    "echo.fail: must be true or false",
		`{"echo.fail": null}`:                     "echo.fail: must be true or false",
		`{"prompt.text": 3}`:                      "prompt.text: must be text",
		`{"echo.nope": 1}`:                        "echo.nope: not a param of this workflow",
		`{"nope.steps": 1}`:                       "nope.steps: not a param of this workflow",
		`{"steps": 1}`:                            "steps: not a param of this workflow",
		`{"output.media": "x"}`:                   "output.media: not a param of this workflow",
		`{"echo.steps": 5, "echo.delay_ms": -1}`:  "echo.delay_ms: must be between 0 and 10000",
		`{"echo.delay_ms": 1, "echo.steps": 999}`: "echo.steps: must be between 1 and 200",
	} {
		if _, detail := resolve(echo(t), inputs(t, body)); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
}

// check covers the types the echo family doesn't use.
func TestCheck(t *testing.T) {
	low, high := 0.5, 2.0
	long := 3.0
	for _, test := range []struct {
		param registry.Param
		value any
		want  string
	}{
		{registry.Param{Type: "float", Min: &low, Max: &high}, 1.5, ""},
		{registry.Param{Type: "float", Min: &low}, 0.1, "must be at least 0.5"},
		{registry.Param{Type: "float", Max: &high}, 3.0, "must be at most 2"},
		{registry.Param{Type: "float"}, true, "must be a number"},
		{registry.Param{Type: "string", Max: &long}, "abcd", "must be at most 3 characters long"},
		{registry.Param{Type: "string", Max: &long}, "äöü", ""},
		{registry.Param{Type: "enum", Enum: []string{"a", "b"}}, "c", "must be one of a, b"},
		{registry.Param{Type: "enum", Enum: []string{"a", "b"}}, "b", ""},
		{registry.Param{Type: "text", Max: &long}, "ab\ncd", "must be at most 3 characters long"},
		{registry.Param{Type: "int"}, 1e300, "must be a whole number"},
		{registry.Param{Type: "seed"}, -1.0, ""},
		{registry.Param{Type: "seed"}, nil, ""},
	} {
		if _, detail := check(test.param, test.value); detail != test.want {
			t.Errorf("%+v %v: %q, want %q", test.param, test.value, detail, test.want)
		}
	}
	if value, _ := check(registry.Param{Type: "int"}, float64(7)); value != int64(7) {
		t.Fatalf("an int is sent as an integer: %#v", value)
	}
}

// The qwen-image graph: seed, enum, int with step and bool params; when and step are carried, null seed stays null.
func TestResolveQwen(t *testing.T) {
	workflow := qwen(t)
	if param, _ := findParam(workflow, "sample.steps"); param.When != "model.variant != 'turbo'" || *param.Step != 1 {
		t.Fatalf("when and step are carried: %+v", param)
	}
	nodes, detail := resolve(workflow, inputs(t, `{"prompt.text": "a fox", "seed.seed": null, "model.variant": "turbo",
		"canvas.width": 768, "decode.tiled": "on", "encode.transparent": true}`))
	if detail != "" {
		t.Fatal(detail)
	}
	if v, ok := nodes["seed"].Params["seed"]; !ok || v != nil {
		t.Fatalf("a null seed is sent as null: %#v", nodes["seed"].Params)
	}
	if nodes["canvas"].Params["width"] != int64(768) || nodes["canvas"].Params["height"] != float64(1024) ||
		nodes["model"].Params["precision"] != "int8" || nodes["sample"].Params["steps"] != float64(40) {
		t.Fatalf("%+v", nodes)
	}
	nodes, _ = resolve(workflow, inputs(t, `{"seed.seed": 9007199254740991}`))
	if nodes["seed"].Params["seed"] != int64(9007199254740991) {
		t.Fatalf("the largest seed is exact: %#v", nodes["seed"].Params["seed"])
	}
	for body, want := range map[string]string{
		`{"seed.seed": -2}`:               "seed.seed: must be null, -1 or a whole number from 0 to 9007199254740991",
		`{"seed.seed": 1.5}`:              "seed.seed: must be null, -1 or a whole number from 0 to 9007199254740991",
		`{"seed.seed": "7"}`:              "seed.seed: must be null, -1 or a whole number from 0 to 9007199254740991",
		`{"seed.seed": 9007199254740992}`: "seed.seed: must be null, -1 or a whole number from 0 to 9007199254740991",
		`{"model.variant": "large"}`:      "model.variant: must be one of base, turbo",
		`{"canvas.width": 100}`:           "canvas.width: must be between 256 and 4096",
		`{"canvas.width": null}`:          "canvas.width: must be a whole number",
		`{"encode.transparent": 1}`:       "encode.transparent: must be true or false",
		`{"decode.tiled": null}`:          "decode.tiled: must be one of auto, on, off",
	} {
		if _, detail := resolve(workflow, inputs(t, body)); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
}

const (
	takeA = "9b2f4c6e-1a3d-4e5f-8a7b-0c1d2e3f4a51"
	takeB = "4e6a8c0b-2d4f-4a6c-9e8b-1f3a5c7e9b62"
)

// Image values: {take_id} alone or in rows items, checked for shape and count; items are named from 1; an image param
// left null is required. The walker lists them in input order and parents dedupes them.
func TestResolveImages(t *testing.T) {
	workflow := qwen(t)
	nodes, detail := resolve(workflow, inputs(t, `{"encode.refs": [{"image": {"take_id": "`+takeA+`"}},
		{"image": {"take_id": "`+takeB+`"}}, {"image": {"take_id": "`+takeA+`"}}]}`))
	if detail != "" {
		t.Fatal(detail)
	}
	got := images(nodes)
	if len(got) != 3 || got[1] != (imageAt{Path: "encode.refs[2].image", TakeID: takeB}) {
		t.Fatalf("%+v", got)
	}
	graph, _ := json.Marshal(nodes)
	if p := parents(graph); len(p) != 2 || p[0] != takeA || p[1] != takeB {
		t.Fatalf("parents %v", p)
	}
	eleven := `[` + strings.Repeat(`{"image": {"take_id": "`+takeA+`"}},`, 10) + `{"image": {"take_id": "` + takeA + `"}}]`
	for body, want := range map[string]string{
		`{"encode.refs": ` + eleven + `}`:                                          "encode.refs: at most 10 items",
		`{"encode.refs": {"image": {"take_id": "` + takeA + `"}}}`:                 "encode.refs: must be a list of items",
		`{"encode.refs": [{"image": {"take_id": "x"}}]}`:                           `encode.refs[1].image: must be {"take_id": <uuid>}`,
		`{"encode.refs": [{"image": {"take_id": "` + takeA + `", "key": "u/a"}}]}`: `encode.refs[1].image: must be {"take_id": <uuid>}`,
		`{"encode.refs": [{"image": {"url": "/files/x"}}]}`:                        `encode.refs[1].image: must be {"take_id": <uuid>}`,
		`{"encode.refs": [{"image": "` + takeA + `"}]}`:                            `encode.refs[1].image: must be {"take_id": <uuid>}`,
		`{"encode.refs": [{"image": {"take_id": "` + takeA + `"}}, {}]}`:           "encode.refs[2].image: an image is required",
		`{"encode.refs": [{"image": {"take_id": "` + takeA + `"}, "w": 1}]}`:       "encode.refs[1]: unknown field w",
		`{"encode.refs": ["x"]}`:                                                   "encode.refs[1]: must be an object",
	} {
		if _, detail := resolve(workflow, inputs(t, body)); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
	// the image kind and an item param: null is required, an item param gets its default
	workflow.Nodes["image"] = registry.Node{Kind: "image", Params: map[string]any{}}
	workflow.NodeSchemas["image"] = registry.NodeSchema{Kind: "image", Params: []registry.Param{{Name: "image", Type: "image"}}}
	if _, detail := resolve(workflow, nil); detail != "image.image: an image is required" {
		t.Fatal(detail)
	}
	nodes, detail = resolve(workflow, inputs(t, `{"image.image": {"take_id": "`+takeB+`"}}`))
	if detail != "" || images(nodes)[0] != (imageAt{Path: "image.image", TakeID: takeB}) {
		t.Fatal(detail, images(nodes))
	}
	one := 1.0
	rows := registry.Param{Type: "rows", Min: &one, Item: map[string]json.RawMessage{"image": json.RawMessage(`"Image"`),
		"weight": json.RawMessage(`{"type": "float", "default": 1, "max": 2}`)}}
	if value, detail := check(rows, []any{map[string]any{"image": map[string]any{"take_id": takeA}}}); detail != "" ||
		value.([]any)[0].(map[string]any)["weight"] != float64(1) {
		t.Fatal(value, detail)
	}
	if _, detail := check(rows, []any{}); detail != "at least 1 items" {
		t.Fatal(detail)
	}
	if _, detail := check(rows, []any{map[string]any{"image": map[string]any{"take_id": takeA}, "weight": 3.0}}); detail != "[1].weight: must be at most 2" {
		t.Fatal(detail)
	}
	if field, ok := rows.ImageInput(); !ok || field != "image" {
		t.Fatal("rows with one Image field takes images")
	}
}

// Lines on an image input: each line a take id, as {take_id} or a one-item rows list.
func TestExpandImageLines(t *testing.T) {
	parsed, shape := batchBody(t, `{"workflow_id":"qwen-image","rows":{"lines":{"param":"encode.refs","values":["`+takeA+`","`+takeB+`"]}}}`)
	if shape != "" {
		t.Fatal(shape)
	}
	rows, _, detail := expand(qwen(t), parsed, func() int64 { return 0 })
	if detail != "" || len(rows) != 2 {
		t.Fatal(rows, detail)
	}
	if got, _ := json.Marshal(rows[1]["encode.refs"]); string(got) != `[{"image":{"take_id":"`+takeB+`"}}]` {
		t.Fatalf("%s", got)
	}
	parsed, _ = batchBody(t, `{"workflow_id":"qwen-image","rows":{"lines":{"param":"encode.refs","values":["nope"]}}}`)
	if _, _, detail := expand(qwen(t), parsed, func() int64 { return 0 }); detail != `row 1: encode.refs[1].image: must be {"take_id": <uuid>}` {
		t.Fatal(detail)
	}
}
