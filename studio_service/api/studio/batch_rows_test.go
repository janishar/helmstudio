package studio

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
)

// batchBody decodes a BatchCreate as a request body would be, and checks its shape.
func batchBody(t *testing.T, body string) (*dto.BatchCreate, string) {
	t.Helper()
	out := new(dto.BatchCreate)
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatal(err)
	}
	return out, out.Validate()
}

// Lines, a table and a grid build their rows over the base inputs; seeds repeat each row with consecutive seeds.
func TestExpand(t *testing.T) {
	fixed := func() int64 { return 7 }
	body, _ := batchBody(t, `{"workflow_id":"echo","inputs":{"echo.steps":2},
		"rows":{"lines":{"param":"prompt.text","values":["a fox","a heron"]}}}`)
	rows, detail := expand(echo(t), body, fixed)
	if detail != "" || len(rows) != 2 || rows[1]["prompt.text"] != "a heron" || rows[1]["echo.steps"] != float64(2) {
		t.Fatal(rows, detail)
	}
	body, _ = batchBody(t, `{"workflow_id":"echo","inputs":{"echo.steps":2},
		"rows":{"table":{"columns":["prompt.text","echo.steps"],"rows":[["a fox",3],["a heron",null]]}}}`)
	if rows, detail = expand(echo(t), body, fixed); detail != "" || rows[0]["echo.steps"] != float64(3) || rows[1]["echo.steps"] != float64(2) {
		t.Fatal("a null cell leaves the base", rows, detail)
	}
	body, _ = batchBody(t, `{"workflow_id":"echo","rows":{"grid":{"axes":[
		{"param":"echo.steps","values":[1,2]},{"param":"prompt.text","values":["a","b","c"]}]}}}`)
	rows, detail = expand(echo(t), body, fixed)
	if detail != "" || len(rows) != 6 || rows[0]["echo.steps"] != float64(1) || rows[2]["prompt.text"] != "c" || rows[3]["echo.steps"] != float64(2) {
		t.Fatal("the first axis varies slowest", rows, detail)
	}
	body, _ = batchBody(t, `{"workflow_id":"qwen-image","rows":{"lines":{"param":"prompt.text","values":["a","b"]}},
		"seeds":{"count":3,"start":1000}}`)
	rows, detail = expand(qwen(t), body, fixed)
	if detail != "" || len(rows) != 6 || rows[0]["seed.seed"] != float64(1000) || rows[2]["seed.seed"] != float64(1002) ||
		rows[3]["prompt.text"] != "b" || rows[3]["seed.seed"] != float64(1000) {
		t.Fatal("repeats sit together, seeds from start", rows, detail)
	}
	body, _ = batchBody(t, `{"workflow_id":"qwen-image","rows":{"lines":{"param":"prompt.text","values":["a"]}},"seeds":{"count":2}}`)
	if rows, detail = expand(qwen(t), body, fixed); detail != "" || rows[1]["seed.seed"] != float64(8) {
		t.Fatal("start drawn when absent", rows, detail)
	}
}

// Every failure is a detail; a bad row is named by its input row.
func TestExpandErrors(t *testing.T) {
	fixed := func() int64 { return 0 }
	for body, want := range map[string]string{
		`{"workflow_id":"echo","rows":{"table":{"columns":["prompt.text","echo.steps"],"rows":[["a",3],["b",0]]}}}`:                                                         "row 2: echo.steps: must be between 1 and 200",
		`{"workflow_id":"echo","rows":{"table":{"columns":["prompt.text"],"rows":[["a"],["b","c"]]}}}`:                                                                      "row 2: has 2 cells; the table has 1 columns",
		`{"workflow_id":"echo","rows":{"lines":{"param":"echo.steps","values":["1"]}}}`:                                                                                     "echo.steps: lines need a text, string or enum param",
		`{"workflow_id":"echo","rows":{"lines":{"param":"echo.nope","values":["1"]}}}`:                                                                                      "echo.nope: not a param of this workflow",
		`{"workflow_id":"echo","rows":{"lines":{"param":"prompt.text","values":["a"]}},"seeds":{"count":2}}`:                                                                "seeds needs a workflow with exactly one seed param; echo has 0",
		`{"workflow_id":"echo","rows":{"grid":{"axes":[{"param":"echo.steps","values":[` + strings.Repeat("1,", 300) + `1]},{"param":"prompt.text","values":["a","b"]}]}}}`: "a batch has 1 to 500 rows; this one has 602",
	} {
		parsed, shape := batchBody(t, body)
		if shape != "" {
			t.Fatal(body, shape)
		}
		if _, detail := expand(echo(t), parsed, fixed); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
	long := `{"workflow_id":"echo","rows":{"lines":{"param":"prompt.text","values":[` +
		strings.Repeat(`"`+strings.Repeat("x", 3999)+`",`, 250) + `"x"]}}}`
	parsed, _ := batchBody(t, long)
	if _, detail := expand(echo(t), parsed, fixed); detail != "batch inputs are too large; use fewer or shorter rows" {
		t.Fatal(detail)
	}
	for _, bad := range []string{`{"workflow_id":"echo","rows":{}}`,
		`{"workflow_id":"echo","rows":{"lines":{"param":"prompt.text","values":["a"]},"grid":{"axes":[]}}}`,
		`{"workflow_id":"echo","rows":{"grid":{"axes":[{"param":"a.b","values":[1]},{"param":"a.c","values":[1]},{"param":"a.d","values":[1]}]}}}`,
		`{"workflow_id":"echo","session_id":"x","rows":{"lines":{"param":"prompt.text","values":["a"]}}}`,
		`{"workflow_id":"echo","rows":{"lines":{"param":"prompt.text","values":["a"]}},"seeds":{"count":9}}`} {
		if _, shape := batchBody(t, bad); shape == "" {
			t.Errorf("%s accepted", bad)
		}
	}
}
