package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

const (
	// MaxRows is the most rows a batch may have (after seed repetition).
	MaxRows = 500
	// MaxOpenBatches is how many queued, running or paused batches one user may have.
	MaxOpenBatches = 5
	// MaxBatchInputs bounds the rows' inputs together, so cmd.pipeline.submit fits NATS (max_payload 1 MB).
	MaxBatchInputs = 768 << 10
)

// expand builds a batch's rows from the body against workflow: each input row (a line, a table row, a grid
// combination) over the base inputs, repeated per seeds with consecutive seeds, each checked like RunRequest.inputs,
// and each row's looks (its looks cell, else the body's). The last result is the 422 detail, naming the input row
// (from 1). randomStart draws seeds.start when absent.
func expand(workflow registry.Workflow, body *dto.BatchCreate, randomStart func() int64) ([]map[string]any, [][]string, string) {
	repeat := 1
	if body.Seeds != nil {
		repeat = body.Seeds.Count
	}
	values, detail := inputRows(workflow, body.Rows, repeat)
	if detail != "" {
		return nil, nil, detail
	}
	if n := len(values) * repeat; n < 1 || n > MaxRows {
		return nil, nil, fmt.Sprintf("a batch has 1 to %d rows; this one has %d", MaxRows, n)
	}
	looks := make([][]string, len(values))
	for i, own := range values {
		cell, given := own["looks"]
		delete(own, "looks")
		if looks[i] = body.Looks; !given || cell == nil {
			continue
		}
		list, _ := cell.([]any)
		if _, isList := cell.([]any); !isList {
			return nil, nil, fmt.Sprintf("row %d: looks: must be a list of look ids", i+1)
		}
		looks[i] = []string{}
		for _, v := range list {
			id, _ := v.(string)
			looks[i] = append(looks[i], id)
		}
		if detail := dto.CheckLookIDs("looks", looks[i]); detail != "" {
			return nil, nil, fmt.Sprintf("row %d: %s", i+1, detail)
		}
	}
	seedKey, start := "", int64(0)
	if body.Seeds != nil {
		var keys []string
		for _, id := range slices.Sorted(maps.Keys(workflow.Nodes)) {
			for _, param := range workflow.NodeSchemas[workflow.Nodes[id].Kind].Params {
				if param.Type == "seed" {
					keys = append(keys, id+"."+param.Name)
				}
			}
		}
		if len(keys) != 1 {
			return nil, nil, fmt.Sprintf("seeds needs a workflow with exactly one seed param; %s has %d", workflow.ID, len(keys))
		}
		seedKey = keys[0]
		if body.Seeds.Start != nil {
			start = *body.Seeds.Start
		} else {
			start = randomStart()
		}
		if start+int64(repeat)-1 > maxSafe {
			return nil, nil, "seeds.start + seeds.count - 1 must be at most 9007199254740991"
		}
	}
	var rows []map[string]any
	var rowLooks [][]string
	size := 0
	for i, own := range values {
		for r := range repeat {
			inputs := maps.Clone(body.Inputs)
			if inputs == nil {
				inputs = map[string]any{}
			}
			maps.Copy(inputs, own)
			if seedKey != "" {
				inputs[seedKey] = float64(start + int64(r)) // as JSON decodes it: resolve checks it so
			}
			if _, detail := resolve(workflow, inputs); detail != "" {
				return nil, nil, fmt.Sprintf("row %d: %s", i+1, detail)
			}
			data, _ := json.Marshal(inputs)
			if size += len(data); size > MaxBatchInputs {
				return nil, nil, "batch inputs are too large; use fewer or shorter rows"
			}
			rows = append(rows, inputs)
			rowLooks = append(rowLooks, looks[i])
		}
	}
	return rows, rowLooks, ""
}

// inputRows returns each input row's own values (before the base and seeds). A grid's size is checked against
// MaxRows before it is built.
func inputRows(workflow registry.Workflow, in dto.BatchRowsIn, repeat int) ([]map[string]any, string) {
	var out []map[string]any
	switch {
	case in.Lines != nil:
		param, ok := findParam(workflow, in.Lines.Param)
		if !ok {
			return nil, in.Lines.Param + ": not a param of this workflow"
		}
		field, takesImages := param.ImageInput()
		if param.Type != "string" && param.Type != "text" && param.Type != "enum" && !takesImages {
			return nil, in.Lines.Param + ": lines need a text, string, enum or image param"
		}
		for _, value := range in.Lines.Values {
			var cell any = value
			if takesImages { // each line a take id: {take_id}, or the one-item list [{<field>: {take_id}}]
				cell = map[string]any{"take_id": value}
				if param.Type == "rows" {
					cell = []any{map[string]any{field: cell}}
				}
			}
			out = append(out, map[string]any{in.Lines.Param: cell})
		}
	case in.Table != nil:
		for i, cells := range in.Table.Rows {
			if len(cells) != len(in.Table.Columns) {
				return nil, fmt.Sprintf("row %d: has %d cells; the table has %d columns", i+1, len(cells), len(in.Table.Columns))
			}
			row := map[string]any{}
			for j, cell := range cells {
				if cell != nil { // a null cell leaves the input unset
					row[in.Table.Columns[j]] = cell
				}
			}
			out = append(out, row)
		}
	case in.Grid != nil:
		n := repeat
		for _, axis := range in.Grid.Axes {
			n *= len(axis.Values)
		}
		if n > MaxRows {
			return nil, fmt.Sprintf("a batch has 1 to %d rows; this one has %d", MaxRows, n)
		}
		out = []map[string]any{{}}
		for _, axis := range in.Grid.Axes { // the first axis varies slowest
			var next []map[string]any
			for _, row := range out {
				for _, value := range axis.Values {
					combined := maps.Clone(row)
					combined[axis.Param] = value
					next = append(next, combined)
				}
			}
			out = next
		}
	}
	return out, ""
}

// randomSeedStart draws a seeds.start that leaves room for 8 consecutive seeds.
func randomSeedStart() int64 { return rand.Int64N(maxSafe - 8) }
