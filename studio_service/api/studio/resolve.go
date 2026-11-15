package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/registry"
)

// resolve returns the workflow's graph with every param of every node filled: the node schema's default, then the
// workflow's own value, then the run's input "<node id>.<param>" (or "<node id>.bypass" for a bypassable node). Bypassed
// nodes are then spliced out and positions dropped: the graph is what a command carries. The second result is the 422
// detail of the first bad input (in key order), "" when all are good.
func resolve(workflow registry.Workflow, inputs map[string]any) (map[string]registry.Node, string) {
	nodes := map[string]registry.Node{}
	for id, node := range workflow.Nodes {
		params := map[string]any{}
		for _, param := range workflow.NodeSchemas[node.Kind].Params {
			params[param.Name] = param.Default
			if value, ok := node.Params[param.Name]; ok {
				if param.Type == "rows" { // items as the workflow keeps them: fill each item's defaults
					filled, detail := checkRows(param, value)
					if detail != "" {
						return nil, join(id+"."+param.Name, detail)
					}
					value = filled
				}
				params[param.Name] = value
			}
		}
		nodes[id] = registry.Node{Kind: node.Kind, Params: params, In: maps.Clone(node.In), Bypass: node.Bypass}
	}
	for _, key := range slices.Sorted(maps.Keys(inputs)) {
		nodeID, name, _ := strings.Cut(key, ".")
		if node, ok := nodes[nodeID]; ok && name == "bypass" {
			bypass, isBool := inputs[key].(bool)
			switch {
			case workflow.NodeSchemas[node.Kind].Passthrough() == nil:
				return nil, key + ": " + nodeID + " cannot be bypassed"
			case !isBool:
				return nil, key + ": must be true or false"
			}
			node.Bypass = bypass
			nodes[nodeID] = node
			continue
		}
		param, ok := findParam(workflow, key)
		if !ok {
			return nil, key + ": not a param of this workflow"
		}
		if ref, wired := nodes[nodeID].In[name]; wired {
			return nil, key + ": wired from " + ref + "; it cannot be set"
		}
		value, detail := check(param, inputs[key])
		if detail != "" {
			return nil, join(key, detail)
		}
		nodes[nodeID].Params[param.Name] = value
	}
	// what a default or the workflow left unfilled: an image param needs an image, a rows param its min items, a node
	// its inputs (a bypassed one only those it passes through); a bypassed node's params do not matter
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node, schema := nodes[id], workflow.NodeSchemas[nodes[id].Kind]
		needs := schema.Passthrough()
		if !node.Bypass {
			needs = nil
			for _, input := range schema.Inputs {
				if !input.Optional {
					needs = append(needs, input.Name)
				}
			}
		}
		for _, input := range needs {
			if _, wired := node.In[input]; !wired {
				return nil, id + "." + input + ": not wired"
			}
		}
		if node.Bypass {
			continue
		}
		for _, param := range schema.Params {
			value := node.Params[param.Name]
			if _, wired := node.In[param.Name]; wired {
				continue
			}
			switch {
			case param.Type == "image" && value == nil:
				return nil, id + "." + param.Name + ": an image is required"
			case param.Type == "rows" && param.Min != nil:
				if items, _ := value.([]any); float64(len(items)) < *param.Min {
					return nil, fmt.Sprintf("%s.%s: at least %s items", id, param.Name, format(*param.Min))
				}
			}
			if detail := valuesByDetail(param, node.Params); detail != "" {
				return nil, id + "." + param.Name + ": " + detail
			}
		}
	}
	if errs := positionErrors(nodes, workflow.NodeSchemas); len(errs) > 0 {
		return nil, errs[0]
	}
	splice(nodes, workflow.NodeSchemas)
	return nodes, ""
}

// join puts a key before a check's detail: "encode.refs: at most 10 items", "encode.refs[2].image: …".
func join(key, detail string) string {
	if strings.HasPrefix(detail, "[") {
		return key + detail
	}
	return key + ": " + detail
}

// findParam returns the schema of the param "<node id>.<param>" names.
func findParam(workflow registry.Workflow, key string) (registry.Param, bool) {
	nodeID, name, ok := strings.Cut(key, ".")
	node, found := workflow.Nodes[nodeID]
	if !ok || !found {
		return registry.Param{}, false
	}
	for _, param := range workflow.NodeSchemas[node.Kind].Params {
		if param.Name == name {
			return param, true
		}
	}
	return registry.Param{}, false
}

// maxSafe is the largest integer a JSON number carries exactly (2^53-1): the ceiling of int and seed values.
const maxSafe = 1<<53 - 1

// check returns value as the param's type, or the reason it is not one.
func check(param registry.Param, value any) (any, string) {
	switch param.Type {
	case "int", "float", "seed":
		if value == nil && param.Type == "seed" {
			return nil, "" // random: the runner picks one and reports it
		}
		number, ok := value.(float64)
		whole := number == math.Trunc(number) && math.Abs(number) <= maxSafe && (param.Type != "seed" || number >= -1)
		if !ok || (param.Type != "float" && !whole) {
			return nil, map[string]string{"int": "must be a whole number", "seed": "must be null, -1 or a whole number from 0 to 9007199254740991",
				"float": "must be a number"}[param.Type]
		}
		if detail := bounds(param, number, ""); detail != "" {
			return nil, detail
		}
		if param.Type == "int" && len(param.Values) > 0 && !slices.Contains(param.Values, int64(number)) {
			return nil, "must be one of " + joinInts(param.Values)
		}
		if param.Type != "float" {
			return int64(number), ""
		}
		return number, ""
	case "bool":
		if _, ok := value.(bool); !ok {
			return nil, "must be true or false"
		}
		return value, ""
	case "string", "text":
		text, ok := value.(string)
		if !ok {
			return nil, "must be text"
		}
		return text, bounds(param, float64(utf8.RuneCountInString(text)), " characters long")
	case "enum":
		text, ok := value.(string)
		if !ok || !slices.Contains(param.Enum, text) {
			return nil, "must be one of " + strings.Join(param.Enum, ", ")
		}
		return text, ""
	case "image":
		if value == nil {
			return nil, "" // resolve says it is required
		}
		return imageRef(value)
	case "rows":
		return checkRows(param, value)
	case "frames":
		return checkFrames(param, value)
	}
	return nil, "has an unknown type " + param.Type
}

// checkFrames returns a frame count on the param's grid, min + k × step up to max, or "auto" where the param offers
// it (the runner predicts the length), or why it is neither.
func checkFrames(param registry.Param, value any) (any, string) {
	if value == "auto" {
		if param.Auto == nil {
			return nil, "auto length is not offered"
		}
		return "auto", ""
	}
	low, step, high := deref(param.Min), deref(param.Step), deref(param.Max)
	number, ok := value.(float64)
	if ok && number == math.Trunc(number) && number >= low && number <= high && step > 0 && math.Mod(number-low, step) == 0 {
		return int64(number), ""
	}
	shown := fmt.Sprint(value)
	if ok {
		shown = format(number)
	}
	return nil, fmt.Sprintf("%s is not on the frame grid %s + %sk up to %s", shown, format(low), format(step), format(high))
}

// imageRef returns an Image value as {"take_id": id}, or why it is not one.
func imageRef(value any) (any, string) {
	ref, ok := value.(map[string]any)
	id, isText := ref["take_id"].(string)
	if !ok || len(ref) != 1 || !isText || !utility.IsUUID(id) {
		return nil, `must be {"take_id": <uuid>}`
	}
	return map[string]any{"take_id": id}, ""
}

// loraRef returns a Lora value as {"lora_id": id}, or why it is not one.
func loraRef(value any) (any, string) {
	ref, ok := value.(map[string]any)
	id, isText := ref["lora_id"].(string)
	if !ok || len(ref) != 1 || !isText || !lora.IsID(id) {
		return nil, `must be {"lora_id": <id>}`
	}
	return map[string]any{"lora_id": id}, ""
}

// checkRows returns a rows value with each item's fields checked against param.Item (an Image or Lora field required, an item
// param its default when left out), or why it is not one; an item's detail starts with [n] (from 1).
func checkRows(param registry.Param, value any) (any, string) {
	items, ok := value.([]any)
	if !ok {
		return nil, "must be a list of items"
	}
	if param.Max != nil && float64(len(items)) > *param.Max {
		return nil, fmt.Sprintf("at most %s items", format(*param.Max))
	}
	_, takesImages := param.ImageInput()
	// a workflow holds no takes: an image rows param may be [] under its min; the run needs min items (resolve)
	if param.Min != nil && float64(len(items)) < *param.Min && (len(items) > 0 || !takesImages) {
		return nil, fmt.Sprintf("at least %s items", format(*param.Min))
	}
	out := make([]any, len(items))
	for i, item := range items {
		fields, ok := item.(map[string]any)
		at := fmt.Sprintf("[%d]", i+1)
		if !ok {
			return nil, at + ": must be an object"
		}
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			if _, known := param.Item[name]; !known {
				return nil, at + ": unknown field " + name
			}
		}
		filled := map[string]any{}
		for _, name := range slices.Sorted(maps.Keys(param.Item)) {
			raw := param.Item[name]
			field, given := fields[name]
			if registry.IsImageField(raw) {
				if !given || field == nil {
					return nil, at + "." + name + ": an image is required"
				}
				ref, detail := imageRef(field)
				if detail != "" {
					return nil, at + "." + name + ": " + detail
				}
				filled[name] = ref
				continue
			}
			if registry.IsLoraField(raw) {
				if !given || field == nil {
					return nil, at + "." + name + ": a LoRA is required"
				}
				ref, detail := loraRef(field)
				if detail != "" {
					return nil, at + "." + name + ": " + detail
				}
				filled[name] = ref
				continue
			}
			var itemParam registry.Param
			if err := json.Unmarshal(raw, &itemParam); err != nil {
				return nil, at + "." + name + ": has an unknown type"
			}
			if !given { // the merged item is checked: a default may not do (a beat's text)
				field = itemParam.Default
			}
			checked, detail := check(itemParam, field)
			if detail != "" {
				return nil, at + "." + name + ": " + detail
			}
			filled[name] = checked
		}
		out[i] = filled
	}
	return out, ""
}

// joinInts writes values as "3, 8".
func joinInts(values []int64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, ", ")
}

// number is a JSON or checked number as a float64.
func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// valuesByDetail says why the node's merged value of an int param with values_by is not allowed for the node's value
// of its enum param (an enum value not listed allows all of values); "" when it is, or when the value is not one of
// values at all (check says so).
func valuesByDetail(param registry.Param, params map[string]any) string {
	by := param.ValuesBy
	if by == nil {
		return ""
	}
	choice, _ := params[by.Param].(string)
	allowed, listed := by.Values[choice]
	if !listed {
		allowed = param.Values
	}
	n, ok := number(params[param.Name])
	if !ok || n != math.Trunc(n) || !slices.Contains(param.Values, int64(n)) || slices.Contains(allowed, int64(n)) {
		return ""
	}
	return "must be one of " + joinInts(allowed)
}

// merged is a node's params over its schema's defaults.
func merged(schema registry.NodeSchema, params map[string]any) map[string]any {
	out := map[string]any{}
	for _, p := range schema.Params {
		out[p.Name] = p.Default
	}
	maps.Copy(out, params)
	return out
}

// clipOf is the frames value (a number or "auto") and fps of the graph's one unwired frames param on a known, not
// bypassed node whose fps param is unwired; ok false when there is not exactly one.
func clipOf(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) (frames, fps any, ok bool) {
	found := 0
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node, schema := nodes[id], schemas[nodes[id].Kind]
		if node.Bypass || schema.Kind == "" {
			continue
		}
		for _, p := range schema.Params {
			_, wired := node.In[p.Name]
			_, fpsWired := node.In[p.FPS]
			if p.Type == "frames" && !wired && !fpsWired {
				values := merged(schema, node.Params)
				frames, fps, found = values[p.Name], values[p.FPS], found+1
			}
		}
	}
	return frames, fps, found == 1
}

// positionErrors are a graph's keyframe position errors (contracts/README.md, Video: keyframes): each
// `position: seconds` item field of an unwired rows param of a known, not bypassed node is the pixel frame
// min(round(s·fps), F − 1) of the clip; past the clip, or a frame an earlier row of that param has, is refused. With
// auto length only the second rule runs (the runner checks the first on the resolved length); no single clip: none.
func positionErrors(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []string {
	frames, fpsValue, ok := clipOf(nodes, schemas)
	if !ok {
		return nil
	}
	fps, _ := number(fpsValue)
	length, fixed := number(frames)
	var out []string
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node, schema := nodes[id], schemas[nodes[id].Kind]
		if node.Bypass {
			continue
		}
		for _, p := range schema.Params {
			if _, wired := node.In[p.Name]; p.Type != "rows" || wired {
				continue
			}
			rows, _ := merged(schema, node.Params)[p.Name].([]any)
			for _, field := range slices.Sorted(maps.Keys(p.Item)) {
				var item registry.Param
				if json.Unmarshal(p.Item[field], &item) != nil || item.Position != "seconds" {
					continue
				}
				seen := map[float64]int{}
				for i, row := range rows {
					fields, _ := row.(map[string]any)
					value, given := fields[field]
					if !given {
						value = item.Default
					}
					seconds, _ := number(value)
					frame := math.RoundToEven(seconds * fps)
					where := fmt.Sprintf("%s.%s[%d].%s", id, p.Name, i+1, field)
					if fixed && frame > length {
						out = append(out, fmt.Sprintf("%s: frame %s is past the clip's last frame %s", where, format(frame), format(length-1)))
						continue
					}
					if fixed {
						frame = min(frame, length-1)
					}
					if first, used := seen[frame]; used {
						out = append(out, fmt.Sprintf("%s: frame %s is already used by row %d", where, format(frame), first))
					} else {
						seen[frame] = i + 1
					}
				}
			}
		}
	}
	return out
}

// imageAt is one Image value of a graph: where it is ("encode.refs[2].image", items from 1) and its take.
type imageAt struct {
	Path, TakeID string
}

// eachImage visits every {take_id} value of nodes in input order (node id, param name, item, field name) and puts
// what fn returns in its place.
func eachImage(nodes map[string]registry.Node, fn func(at imageAt) any) {
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		params := nodes[id].Params
		for _, name := range slices.Sorted(maps.Keys(params)) {
			key := id + "." + name
			switch value := params[name].(type) {
			case map[string]any:
				if takeID, ok := value["take_id"].(string); ok {
					params[name] = fn(imageAt{Path: key, TakeID: takeID})
				}
			case []any:
				for i, item := range value {
					fields, _ := item.(map[string]any)
					for _, field := range slices.Sorted(maps.Keys(fields)) {
						if ref, ok := fields[field].(map[string]any); ok {
							if takeID, ok := ref["take_id"].(string); ok {
								fields[field] = fn(imageAt{Path: fmt.Sprintf("%s[%d].%s", key, i+1, field), TakeID: takeID})
							}
						}
					}
				}
			}
		}
	}
}

// images lists the Image values of nodes in input order.
func images(nodes map[string]registry.Node) []imageAt {
	var out []imageAt
	eachImage(nodes, func(at imageAt) any {
		out = append(out, at)
		return map[string]any{"take_id": at.TakeID}
	})
	return out
}

// parents are the takes of a stored graph's Image values, each once, in input order: a take's lineage.
func parents(graph []byte) []string {
	var nodes map[string]registry.Node
	_ = json.Unmarshal(graph, &nodes)
	var out []string
	for _, at := range images(nodes) {
		if !slices.Contains(out, at.TakeID) {
			out = append(out, at.TakeID)
		}
	}
	return out
}

// bounds checks number against the param's min and max; unit follows the numbers in the detail.
func bounds(param registry.Param, number float64, unit string) string {
	low := param.Min == nil || number >= *param.Min
	high := param.Max == nil || number <= *param.Max
	switch {
	case low && high:
		return ""
	case param.Min != nil && param.Max != nil:
		return fmt.Sprintf("must be between %s and %s%s", format(*param.Min), format(*param.Max), unit)
	case param.Min != nil:
		return fmt.Sprintf("must be at least %s%s", format(*param.Min), unit)
	}
	return fmt.Sprintf("must be at most %s%s", format(*param.Max), unit)
}

// format writes a number the shortest way (10, not 10.000000).
func format(number float64) string { return strconv.FormatFloat(number, 'f', -1, 64) }
