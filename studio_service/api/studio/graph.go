package studio

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// The graph rules (contracts/README.md "Graph rules (normative)"); contracts/check.py graph_problems is the reference
// and contracts/fixtures/graphs the shared tests.

const (
	// MaxNodes is the most nodes a workflow may have.
	MaxNodes = 64
	// MaxDocBytes is the most bytes a workflow document may take as compact JSON.
	MaxDocBytes = 262144
	// MaxOutputs is the most output nodes a workflow may run: a run's work budget, as each output pulls its own chain.
	// ponytail: a node count, not GPU time; a per-run wall-clock limit if a kind ever chains heavy stages behind one output
	MaxOutputs = 4
)

// runOnly are the codes that block running only; every other code blocks saving (and running).
var runOnly = map[string]bool{"unknown_kind": true, "env_mismatch": true, "required_input": true, "no_output": true,
	"too_many_outputs": true, "too_many_seeds": true, "lora_unknown": true, "lora_family": true}

// handles are the family-scoped port types.
var handles = map[string]bool{"Model": true, "Conditioning": true, "Latent": true}

// fits reports whether a wire of type got may feed a port of type want: the same type, or Media taking Image or Video.
func fits(got, want string) bool {
	return got == want || want == "Media" && (got == "Image" || got == "Video")
}

// workflowMedia is what a graph's takes are: text when it answers text, video when an output node's media is wired from a Video output, else
// image (contracts/README.md, Video).
func workflowMedia(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) string {
	if registry.IsText(nodes) {
		return "text" // an ask workflow (API 1.14): its answer is stored on the run
	}
	for _, node := range nodes {
		ref, ok := node.In["media"]
		if node.Kind != "output" || !ok {
			continue
		}
		srcID, out, _ := strings.Cut(ref, ".")
		for _, port := range schemas[nodes[srcID].Kind].Outputs {
			if port.Name == out && port.Type == "Video" {
				return "video"
			}
		}
	}
	return "image"
}

// wireable maps a value param's type to the port type a wire into it carries.
var wireable = map[string]string{"int": "Int", "float": "Float", "seed": "Seed", "string": "Text", "text": "Text", "image": "Image"}

// portType is the type a wire into key must carry: an input's type, or a wireable value param's; "" when neither.
func portType(schema registry.NodeSchema, key string) string {
	if i := slices.IndexFunc(schema.Inputs, func(p registry.Port) bool { return p.Name == key }); i >= 0 {
		return schema.Inputs[i].Type
	}
	if param, ok := paramOf(schema, key); ok {
		return wireable[param.Type]
	}
	return ""
}

// paramOf returns the schema's param named name.
func paramOf(schema registry.NodeSchema, name string) (registry.Param, bool) {
	i := slices.IndexFunc(schema.Params, func(p registry.Param) bool { return p.Name == name })
	if i < 0 {
		return registry.Param{}, false
	}
	return schema.Params[i], true
}

// valueBad reports whether a workflow may not hold value for param: the run rules (check), and no take (an image param
// stays null, a rows param whose item has an Image field holds no items).
func valueBad(param registry.Param, value any) bool {
	switch param.Type {
	case "image":
		return value != nil
	case "rows":
		if items, ok := value.([]any); ok && len(items) > 0 {
			for _, raw := range param.Item {
				if registry.IsImageField(raw) || registry.IsMediaField(raw) {
					return true
				}
			}
		}
	}
	_, detail := check(param, value)
	return detail != ""
}

// graphProblems checks doc against the palette (the live kinds) with every graph rule, sorted as the API answers them.
// loras is the caller's library (the LoRAs the caller can use now, id → detected family); nil leaves lora_unknown and
// lora_family unchecked (a run checks its LoRAs once its inputs are merged).
func graphProblems(doc dto.WorkflowDoc, palette map[string]registry.PaletteNode, loras map[string]string) []dto.Problem {
	found := map[[4]string]dto.Problem{}
	add := func(code, node, port, param, message string, args ...any) {
		blocks := "save"
		if runOnly[code] {
			blocks = "run"
		}
		found[[4]string{code, node, port, param}] = dto.Problem{Code: code, Node: node, Port: port, Param: param,
			Blocks: blocks, Message: fmt.Sprintf(message, args...)}
	}
	nodes := doc.Nodes
	if len(nodes) > MaxNodes {
		add("too_many_nodes", "", "", "", "the workflow has %d nodes; at most %d", len(nodes), MaxNodes)
	}
	// ponytail: Go escapes <, > and & where check.py does not; a document right at the limit may count a few bytes more
	if data, _ := json.Marshal(doc); len(data) > MaxDocBytes {
		add("too_large", "", "", "", "the workflow takes %d bytes; at most %d", len(data), MaxDocBytes)
	}
	if len(found) > 0 {
		return sortedProblems(found) // nothing else is checked: the work stays bounded
	}
	outputs, seeds := 0, 0
	for _, node := range nodes {
		if node.Kind == "output" {
			outputs++
		}
		for _, param := range palette[node.Kind].Params {
			if _, wired := node.In[param.Name]; param.Type == "seed" && !wired {
				seeds++
			}
		}
	}
	if outputs > MaxOutputs {
		add("too_many_outputs", "", "", "", "the workflow has %d output nodes; at most %d", outputs, MaxOutputs)
	}
	if seeds > 1 {
		add("too_many_seeds", "", "", "", "the workflow has %d unwired seeds; at most one (wire one seed into every stage)", seeds)
	}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		if family := registry.Family(node.Kind); family != "" && family != doc.Family {
			add("wrong_family", id, "", "", "%s: %s is not of family %s", id, node.Kind, doc.Family)
		}
		kind, ok := palette[node.Kind]
		if !ok {
			add("unknown_kind", id, "", "", "%s: %s is not published by a live runner", id, node.Kind)
			continue
		}
		schema := kind.NodeSchema
		if !slices.Contains(kind.Envs, doc.Env) {
			add("env_mismatch", id, "", "", "%s: %s does not run in env %s", id, node.Kind, doc.Env)
		}
		for _, name := range slices.Sorted(maps.Keys(node.Params)) {
			param, ok := paramOf(schema, name)
			switch {
			case !ok:
				add("unknown_param", id, "", name, "%s.%s: %s has no such param", id, name, node.Kind)
			case valueBad(param, node.Params[name]):
				add("param_invalid", id, "", name, "%s.%s: %s", id, name, invalidReason(param, node.Params[name]))
			case param.LoraField() != "":
				loraProblems(id, node, param, loras, add)
			}
		}
		for _, param := range schema.Params { // steps per role, on the node's merged values (Video: Steps)
			value, given := node.Params[param.Name]
			if detail := valuesByDetail(param, merged(schema, node.Params)); detail != "" && (!given || !valueBad(param, value)) {
				add("param_invalid", id, "", param.Name, "%s.%s: %s", id, param.Name, detail)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(node.In)) {
			want := portType(schema, key)
			if want == "" {
				add("unknown_input", id, key, "", "%s.%s: %s has no such input", id, key, node.Kind)
				continue
			}
			srcID, out, _ := strings.Cut(node.In[key], ".")
			src, exists := nodes[srcID]
			srcKind, known := palette[src.Kind]
			if exists && !known {
				continue // an unknown kind's outputs are not checked
			}
			i := slices.IndexFunc(srcKind.Outputs, func(p registry.Port) bool { return p.Name == out })
			switch {
			case !exists || i < 0:
				add("unknown_source", id, key, "", "%s.%s: %s is not an output in this workflow", id, key, node.In[key])
			case !fits(srcKind.Outputs[i].Type, want):
				add("type_mismatch", id, key, "", "%s.%s: expects %s, got %s from %s", id, key, want, srcKind.Outputs[i].Type, node.In[key])
			case handles[want] && registry.Family(src.Kind) != registry.Family(node.Kind):
				add("type_mismatch", id, key, "", "%s.%s: a %s handle cannot join %s to %s", id, key, want, src.Kind, node.Kind)
			}
		}
		required := []string{}
		if node.Bypass {
			required = schema.Passthrough()
			if required == nil {
				add("not_bypassable", id, "", "", "%s: %s cannot be bypassed", id, node.Kind)
			}
		} else {
			for _, input := range schema.Inputs {
				if !input.Optional {
					required = append(required, input.Name)
				}
			}
		}
		for _, input := range required {
			if _, wired := node.In[input]; !wired {
				add("required_input", id, input, "", "%s.%s: not wired", id, input)
			}
		}
	}
	for _, id := range onCycles(nodes) {
		add("cycle", id, "", "", "%s: its wires lead back to itself", id)
	}
	if !slices.ContainsFunc(slices.Collect(maps.Values(nodes)), func(n registry.Node) bool { return n.Kind == "output" || n.Kind == "answer" }) {
		add("no_output", "", "", "", "the workflow has no output node")
	}
	studioProblems(doc, palette, add)
	return sortedProblems(found)
}

// loraProblems checks a valid rows value with a Lora field: an id twice (blocks saving) and, on a node that is not
// bypassed, an id the caller cannot use or one detected for another family (block running).
func loraProblems(id string, node registry.Node, param registry.Param, loras map[string]string,
	add func(code, node, port, param, message string, args ...any)) {
	field := param.LoraField()
	items, _ := node.Params[param.Name].([]any)
	var ids []string
	for i, item := range items {
		loraID := item.(map[string]any)[field].(map[string]any)["lora_id"].(string)
		if slices.Contains(ids, loraID) {
			add("lora_duplicate", id, "", param.Name, "%s.%s[%d]: LoRA %s is already in row %d", id, param.Name, i+1, loraID,
				slices.Index(ids, loraID)+1)
		}
		ids = append(ids, loraID)
	}
	if node.Bypass || loras == nil {
		return
	}
	family := registry.Family(node.Kind)
	for i, loraID := range ids {
		found, ok := loras[loraID]
		switch {
		case !ok:
			add("lora_unknown", id, "", param.Name, "%s.%s[%d]: LoRA %s not found", id, param.Name, i+1, loraID)
		case found == "unknown":
			add("lora_family", id, "", param.Name, "%s.%s[%d]: LoRA %s does not fit %s (its model is unknown)", id,
				param.Name, i+1, loraID, family)
		case found != family:
			add("lora_family", id, "", param.Name, "%s.%s[%d]: LoRA %s does not fit %s (it is for %s)", id, param.Name, i+1,
				loraID, family, found)
		}
	}
}

// docLoraIDs lists every LoRA id a document's rows values name (presets are not looked up).
func docLoraIDs(doc dto.WorkflowDoc) []string {
	var ids []string
	for _, node := range doc.Nodes {
		for _, value := range node.Params {
			items, _ := value.([]any)
			for _, item := range items {
				fields, _ := item.(map[string]any)
				for _, v := range fields {
					ref, _ := v.(map[string]any)
					if loraID, ok := ref["lora_id"].(string); ok && !slices.Contains(ids, loraID) {
						ids = append(ids, loraID)
					}
				}
			}
		}
	}
	return ids
}

// sortedProblems is found as the API answers it.
func sortedProblems(found map[[4]string]dto.Problem) []dto.Problem {
	out := append([]dto.Problem{}, slices.Collect(maps.Values(found))...)
	sortProblems(out)
	return out
}

// invalidReason says why a workflow may not hold value for param.
func invalidReason(param registry.Param, value any) string {
	if _, detail := check(param, value); detail != "" {
		return detail
	}
	return "a workflow holds no image; leave it empty"
}

// onCycles lists the nodes that can reach themselves through wires (a self-wire counts).
func onCycles(nodes map[string]registry.Node) []string {
	next := map[string][]string{}
	for id, node := range nodes {
		for _, ref := range node.In {
			if src, _, _ := strings.Cut(ref, "."); nodes[src].Kind != "" {
				next[src] = append(next[src], id)
			}
		}
	}
	var out []string
	for id := range nodes { // ponytail: a walk per node, O(n·e); fine at 64 nodes (graphProblems stops above that)
		seen, todo := map[string]bool{}, slices.Clone(next[id])
		for len(todo) > 0 {
			x := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			if !seen[x] {
				seen[x] = true
				todo = append(todo, next[x]...)
			}
		}
		if seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// studioProblems checks the Studio block: unknown paths, duplicates, presets.
func studioProblems(doc dto.WorkflowDoc, palette map[string]registry.PaletteNode, add func(code, node, port, param, message string, args ...any)) {
	studio := doc.Studio
	if studio == nil {
		return
	}
	// bad reports whether a path names nothing settable; an unknown kind's params are not checked
	bad := func(path string) bool {
		id, name, _ := strings.Cut(path, ".")
		node, exists := doc.Nodes[id]
		if !exists {
			return true
		}
		kind, known := palette[node.Kind]
		if !known {
			return false
		}
		if name == "bypass" {
			return kind.Passthrough() == nil
		}
		_, isParam := paramOf(kind.NodeSchema, name)
		_, wired := node.In[name]
		return !isParam || wired
	}
	placed := studio.Placed()
	for _, path := range append(slices.Clone(placed), slices.Collect(maps.Keys(studio.Labels))...) {
		if !dto.IsTile(path) && bad(path) {
			id, name, _ := strings.Cut(path, ".")
			add("studio_unknown_path", id, "", name, "studio: %s is not a param this workflow can show", path)
		}
	}
	counts := map[string]int{}
	for _, path := range placed {
		if counts[path]++; counts[path] == 2 {
			id, name, _ := strings.Cut(path, ".")
			if dto.IsTile(path) { // a tile twice: no node, the tile as param
				id, name = "", path
			}
			add("studio_duplicate", id, "", name, "studio: %s is placed more than once", path)
		}
	}
	for _, preset := range slices.Sorted(maps.Keys(studio.Presets)) {
		for path, value := range studio.Presets[preset] {
			id, name, _ := strings.Cut(path, ".")
			// the node's values with this preset's on top: a preset's steps are checked against the node's role
			values := maps.Clone(doc.Nodes[id].Params)
			for other, v := range studio.Presets[preset] {
				if otherID, otherName, _ := strings.Cut(other, "."); otherID == id && otherName != "bypass" {
					values = setKey(values, otherName, v)
				}
			}
			node, exists := doc.Nodes[id]
			kind, known := palette[node.Kind]
			switch {
			case bad(path):
				add("preset_invalid", id, "", name, "preset %s: %s is not a param this workflow can show", preset, path)
			case !exists || !known:
			case name == "bypass":
				if _, ok := value.(bool); !ok {
					add("preset_invalid", id, "", name, "preset %s: %s must be true or false", preset, path)
				}
			default:
				param, _ := paramOf(kind.NodeSchema, name)
				if valueBad(param, value) {
					add("preset_invalid", id, "", name, "preset %s: %s: %s", preset, path, invalidReason(param, value))
				} else if detail := valuesByDetail(param, merged(kind.NodeSchema, values)); detail != "" {
					add("preset_invalid", id, "", name, "preset %s: %s: %s", preset, path, detail)
				}
			}
		}
	}
}

// setKey sets m[key] = value, making m when it is nil.
func setKey(m map[string]any, key string, value any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	return m
}

// sortProblems orders problems by node (whole-workflow ones first), then code, port, param.
func sortProblems(problems []dto.Problem) {
	slices.SortFunc(problems, func(a, b dto.Problem) int {
		return cmp.Or(strings.Compare(a.Node, b.Node), strings.Compare(a.Code, b.Code), strings.Compare(a.Port, b.Port),
			strings.Compare(a.Param, b.Param))
	})
}

// blocksSave reports whether any problem blocks saving.
func blocksSave(problems []dto.Problem) bool {
	return slices.ContainsFunc(problems, func(p dto.Problem) bool { return p.Blocks == "save" })
}

// splice takes every bypassed node out of a resolved graph: each consumer of a bypassed node's output is rewired to
// the source of that output's pass-through input, following chains of bypassed nodes. The graph must have passed the
// graph rules (no cycle, pass-through inputs wired). Positions are dropped too.
func splice(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) {
	// source follows ref through bypassed nodes to the output that really feeds it
	source := func(ref string) string {
		for range len(nodes) + 1 {
			id, out, _ := strings.Cut(ref, ".")
			node := nodes[id]
			if !node.Bypass {
				return ref
			}
			schema := schemas[node.Kind]
			i := slices.IndexFunc(schema.Outputs, func(p registry.Port) bool { return p.Name == out })
			if i < 0 {
				return ref
			}
			j := slices.IndexFunc(schema.Inputs, func(p registry.Port) bool { return p.Type == schema.Outputs[i].Type })
			if j < 0 {
				return ref
			}
			ref = node.In[schema.Inputs[j].Name]
		}
		return ref
	}
	for id, node := range nodes {
		if node.Bypass {
			continue
		}
		for key, ref := range node.In {
			node.In[key] = source(ref)
		}
		node.Position = nil
		nodes[id] = node
	}
	for id, node := range nodes {
		if node.Bypass {
			delete(nodes, id)
		}
	}
}
