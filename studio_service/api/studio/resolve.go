package studio

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/helmstudio/studio_service/registry"
)

// resolve returns the workflow's graph with every param of every node filled: the node schema's default, then the
// workflow's own value, then the run's input "<node id>.<param>". The second result is the 422 detail of the first bad
// input (in key order), "" when all are good.
func resolve(workflow registry.Workflow, inputs map[string]any) (map[string]registry.Node, string) {
	nodes := map[string]registry.Node{}
	for id, node := range workflow.Nodes {
		params := map[string]any{}
		for _, param := range workflow.NodeSchemas[node.Kind].Params {
			params[param.Name] = param.Default
			if value, ok := node.Params[param.Name]; ok {
				params[param.Name] = value
			}
		}
		nodes[id] = registry.Node{Kind: node.Kind, Params: params, In: maps.Clone(node.In)}
	}
	for _, key := range slices.Sorted(maps.Keys(inputs)) {
		param, ok := findParam(workflow, key)
		if !ok {
			return nil, key + ": not a param of this workflow"
		}
		value, detail := check(param, inputs[key])
		if detail != "" {
			return nil, key + ": " + detail
		}
		nodeID, _, _ := strings.Cut(key, ".")
		nodes[nodeID].Params[param.Name] = value
	}
	return nodes, ""
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
	}
	return nil, "has an unknown type " + param.Type
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
