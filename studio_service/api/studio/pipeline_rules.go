package studio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// The pipeline rules (contracts/README.md "Pipeline rules (normative)"); contracts/check.py pipeline_problems is the
// reference and contracts/fixtures/pipelines the shared tests.

const (
	// MaxPipelineNodes is the most nodes a pipeline may have.
	MaxPipelineNodes = 24
	// MaxPipelineBytes is the most bytes a pipeline document may take as compact JSON.
	MaxPipelineBytes = 65536
	// MaxPipelineRuns bounds the runs one row may make (Submit's worst case).
	MaxPipelineRuns = 64
)

// pipelineRunOnly are the codes that block running only; every other code blocks saving (and running).
var pipelineRunOnly = map[string]bool{"unreachable": true, "dead_end": true, "no_output": true, "unknown_workflow": true,
	"workflow_cannot_run": true, "unknown_preset": true, "step_input_invalid": true, "bind_unknown": true, "bind_type": true,
	"unbound_input": true, "step_seeds": true, "unknown_look": true, "look_fit": true}

// inputFits maps a pipeline input type to the param types it fills.
var inputFits = map[string][]string{"Text": {"string", "text"}, "Int": {"int"}, "Float": {"float"}, "Seed": {"seed"},
	"Image": {"image"}}

// imageField is a rows param's one Image field (a list of takes binds to it); ok false for any other param.
func imageField(param registry.Param) (string, bool) {
	if param.Type != "rows" {
		return "", false
	}
	return param.ImageInput()
}

// timed reports whether a rows param's item has a `position: seconds` field: every bound row would take its default
// time, so only one take fits.
func timed(param registry.Param) bool {
	for _, raw := range param.Item {
		var item registry.Param
		if json.Unmarshal(raw, &item) == nil && item.Position == "seconds" {
			return true
		}
	}
	return false
}

// settable is `<node>.<param>` → schema for every unwired param of a known kind: what a step may set or bind.
func settable(wf dto.WorkflowDoc, palette map[string]registry.PaletteNode) map[string]registry.Param {
	out := map[string]registry.Param{}
	for id, node := range wf.Nodes {
		kind, ok := palette[node.Kind]
		if !ok {
			continue
		}
		for _, param := range kind.Params {
			if _, wired := node.In[param.Name]; !wired {
				out[id+"."+param.Name] = param
			}
		}
	}
	return out
}

// compactSize is the document's size as compact JSON without HTML escaping (as check.py counts it).
func compactSize(doc any) int {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(doc)
	return buffer.Len() - 1 // Encode's newline
}

// pipelineGraph is a document's accepted edges: successors (with capped = a branch's fail edge) and the capped edges.
type pipelineGraph struct {
	succ   map[string][]pipelineHop
	capped []cappedEdge
}

type pipelineHop struct {
	to     string
	capped bool
}

type cappedEdge struct {
	gate, fail string
	max        int
}

// reach is the nodes reachable from src in one edge or more, never through avoid ("" = none); uncappedOnly skips
// capped edges.
func (g pipelineGraph) reach(src, avoid string, uncappedOnly bool) map[string]bool {
	seen, todo := map[string]bool{}, []string{src}
	for len(todo) > 0 {
		at := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, hop := range g.succ[at] {
			if hop.to != avoid && !seen[hop.to] && !(uncappedOnly && hop.capped) {
				seen[hop.to] = true
				todo = append(todo, hop.to)
			}
		}
	}
	return seen
}

// pipelineProblems checks doc with every pipeline rule, sorted as the API answers them. workflows are the documents the
// caller can run (templates and their own) by id; palette the live kinds; looks the looks the caller can use now, loras
// the LoRA id → family the caller can use now in an env (the fit rule).
func pipelineProblems(doc dto.PipelineDoc, workflows map[string]dto.WorkflowDoc, palette map[string]registry.PaletteNode,
	looks []lookItem, loras func(env string) map[string]string) []dto.Problem {
	found := map[[4]string]dto.Problem{}
	add := func(code, node, port, param, message string, args ...any) {
		blocks := "save"
		if pipelineRunOnly[code] {
			blocks = "run"
		}
		found[[4]string{code, node, port, param}] = dto.Problem{Code: code, Node: node, Port: port, Param: param,
			Blocks: blocks, Message: fmt.Sprintf(message, args...)}
	}
	nodes := doc.Nodes
	if len(nodes) > MaxPipelineNodes {
		add("too_many_nodes", "", "", "", "the pipeline has %d nodes; at most %d", len(nodes), MaxPipelineNodes)
	}
	if size := compactSize(doc); size > MaxPipelineBytes {
		add("too_large", "", "", "", "the pipeline takes %d bytes; at most %d", size, MaxPipelineBytes)
	}
	if len(found) > 0 {
		return sortedProblems(found) // nothing else is checked: the work stays bounded
	}
	ids := slices.Sorted(maps.Keys(nodes))
	of := func(kind string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return nodes[id].Kind != kind })
	}
	roots, inputs := of("inputs"), map[string]string{}
	if len(roots) != 1 {
		add("inputs_node", "", "", "", "a pipeline needs exactly one inputs node; it has %d", len(roots))
	}
	for _, id := range roots {
		counts := map[string]int{}
		for _, input := range nodes[id].Columns {
			if counts[input.Name]++; counts[input.Name] == 2 {
				add("duplicate_input", id, "", input.Name, "%s: input %s is named twice", id, input.Name)
			}
			inputs[input.Name] = input.Type
		}
	}
	graph := pipelineGraph{succ: map[string][]pipelineHop{}}
	outdeg := map[string]int{}
	for _, edge := range doc.Edges {
		from, targets := edge.From, edge.Targets()
		_, known := nodes[from]
		if !known || slices.ContainsFunc(targets, func(t string) bool { _, ok := nodes[t]; return !ok }) {
			add("unknown_node", from, "", "", "%s: an edge names a node that is not in the pipeline", from)
			continue
		}
		if edge.Branch != nil && nodes[from].Kind != "gate" {
			add("branch_not_gate", from, "", "", "%s: only a gate can branch", from)
			continue
		}
		outdeg[from]++
		if outdeg[from] > 1 || nodes[from].Kind == "output" ||
			slices.ContainsFunc(targets, func(t string) bool { return nodes[t].Kind == "inputs" }) {
			add("edge_invalid", from, "", "", "%s: a node has one way out, an output none, and nothing leads into the inputs node", from)
			continue
		}
		if edge.Branch == nil {
			graph.succ[from] = append(graph.succ[from], pipelineHop{edge.To, false})
		} else {
			b := edge.Branch
			graph.succ[from] = append(graph.succ[from], pipelineHop{b.Pass, false}, pipelineHop{b.Fail, true}, pipelineHop{b.Then, false})
			graph.capped = append(graph.capped, cappedEdge{from, b.Fail, b.Max})
		}
	}
	reach := func(id string) map[string]bool { return graph.reach(id, "", false) }
	for _, id := range ids { // ponytail: a walk per node, O(n·e); fine at 24 nodes
		if graph.reach(id, "", true)[id] {
			add("uncapped_cycle", id, "", "", "%s: it loops back to itself without a capped fail edge", id)
		}
	}
	runs := 0
	for _, id := range of("step") {
		visits := 1
		for _, c := range graph.capped {
			if (id == c.fail || reach(c.fail)[id]) && reach(id)[c.gate] {
				visits += c.max
			}
		}
		runs += nodes[id].SeedsOr1() * visits
	}
	if runs > MaxPipelineRuns {
		add("too_many_runs", "", "", "", "a row may make %d runs; at most %d", runs, MaxPipelineRuns)
	}
	root := ""
	if len(roots) == 1 {
		root = roots[0]
		fromRoot := reach(root)
		for _, id := range ids {
			if id != root && !fromRoot[id] {
				add("unreachable", id, "", "", "%s: no edge from the inputs node leads here", id)
			}
		}
	}
	outputs := of("output")
	if len(outputs) == 0 {
		add("no_output", "", "", "", "the pipeline has no output node")
	}
	for _, id := range ids {
		r := reach(id)
		if len(outputs) > 0 && nodes[id].Kind != "output" && !slices.ContainsFunc(outputs, func(o string) bool { return r[o] }) {
			add("dead_end", id, "", "", "%s: no path from here reaches an output", id)
		}
	}
	// a before b on every path from the inputs node (unchecked without exactly one inputs node)
	dominates := func(a, b string) bool {
		return root == "" || a == root || (a != b && !graph.reach(root, a, false)[b])
	}
	// source is what a source names: an input's type, or at most how many takes; ok false when it names nothing fit
	type sourced struct {
		input string // the input's type, "" for takes
		takes int
	}
	source := func(src string) (sourced, bool) {
		if name, ok := strings.CutPrefix(src, "$"); ok {
			typ, known := inputs[name]
			return sourced{input: typ}, known
		}
		id, what, _ := strings.Cut(src, ".")
		n, known := nodes[id]
		switch {
		case !known:
			return sourced{}, false
		case what == "takes" && n.Kind == "step":
			return sourced{takes: n.SeedsOr1()}, true
		case what == "picked" && n.Kind == "gate":
			return sourced{takes: n.KeepOr1()}, true
		}
		return sourced{}, false
	}
	for _, id := range of("gate") {
		gate := nodes[id]
		step, known := nodes[gate.Takes]
		if !known || step.Kind != "step" || !dominates(gate.Takes, id) {
			add("gate_takes", id, "", "", "%s: takes must name a step on every path to the gate", id)
		} else if gate.KeepOr1() > step.SeedsOr1() {
			add("gate_keep", id, "", "", "%s: keeps %d of %s's %d takes", id, gate.KeepOr1(), gate.Takes, step.SeedsOr1())
		}
	}
	for _, id := range outputs {
		from := nodes[id].From
		src, _, _ := strings.Cut(from, ".")
		if _, ok := source(from); !ok || (root != "" && !reach(src)[id]) {
			add("bind_source", id, "from", "", "%s.from: %s is not a step's takes or a gate's pick before this output", id, from)
		}
	}
	for _, id := range of("step") {
		n := nodes[id]
		binds := map[string]*sourced{}
		for _, key := range slices.Sorted(maps.Keys(n.Bind)) {
			value := n.Bind[key]
			src, ok := source(value)
			before, _, _ := strings.Cut(value, ".")
			if !ok || (!strings.HasPrefix(value, "$") && !dominates(before, id)) {
				add("bind_source", id, key, "", "%s.bind.%s: %s names no input, or no step or gate before this step", id, key, value)
				binds[key] = nil
				continue
			}
			binds[key] = &src
		}
		wf, ok := workflows[n.Workflow]
		if !ok {
			add("unknown_workflow", id, "", "", "%s: workflow %s is not a template or one of yours", id, n.Workflow)
			continue
		}
		if problems := graphProblems(wf, palette, nil); len(problems) > 0 {
			add("workflow_cannot_run", id, "", "", "%s: workflow %s cannot run: %s", id, n.Workflow, problems[0].Message)
		}
		if n.Preset != nil {
			if wf.Studio == nil || !hasKey(wf.Studio.Presets, *n.Preset) {
				add("unknown_preset", id, "", "", "%s: workflow %s has no preset %s", id, n.Workflow, *n.Preset)
			}
		}
		pipelineLooks(id, n.Looks, wf, palette, looks, loras, add)
		params := settable(wf, palette)
		for _, key := range slices.Sorted(maps.Keys(n.Fixed)) {
			value := n.Fixed[key]
			wn, name, _ := strings.Cut(key, ".")
			if name == "bypass" {
				kind, known := palette[wf.Nodes[wn].Kind]
				if _, isBool := value.(bool); !known || kind.Passthrough() == nil || !isBool {
					add("step_input_invalid", id, "", key, "%s.inputs.%s: not a node that can be bypassed, or not true or false", id, key)
				}
				continue
			}
			param, ok := params[key]
			if !ok {
				add("step_input_invalid", id, "", key, "%s.inputs.%s: not a param this workflow can set", id, key)
			} else if valueBad(param, value) {
				add("step_input_invalid", id, "", key, "%s.inputs.%s: %s", id, key, invalidReason(param, value))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(binds)) {
			src := binds[key]
			param, ok := params[key]
			if !ok {
				add("bind_unknown", id, key, "", "%s.bind.%s: not a param this workflow can set", id, key)
				continue
			}
			if src == nil {
				continue
			}
			_, rowsOfImages := imageField(param)
			var fits bool
			if src.input != "" {
				fits = slices.Contains(inputFits[src.input], param.Type) || (src.input == "Image" && rowsOfImages)
			} else {
				fits = (param.Type == "image" && src.takes == 1) ||
					(rowsOfImages && (param.Max == nil || float64(src.takes) <= *param.Max) && (src.takes == 1 || !timed(param)))
			}
			if !fits {
				add("bind_type", id, key, "", "%s.bind.%s: %s does not fit a %s param", id, key, n.Bind[key], param.Type)
			}
		}
		hasSeed := false
		for _, key := range slices.Sorted(maps.Keys(params)) {
			param := params[key]
			hasSeed = hasSeed || param.Type == "seed"
			_, rowsOfImages := imageField(param)
			needsOne := param.Type == "image" || (rowsOfImages && param.Min != nil && *param.Min >= 1)
			if _, bound := n.Bind[key]; needsOne && !bound {
				add("unbound_input", id, key, "", "%s: %s needs an image; bind it", id, key)
			}
		}
		if n.SeedsOr1() > 1 && !hasSeed {
			add("step_seeds", id, "", "", "%s: seeds needs a workflow with a seed param", id)
		}
	}
	return sortedProblems(found)
}

// pipelineLooks adds a step's look problems: unknown_look (a look the caller cannot use now), else look_fit (one that
// does not fit the step's workflow as stored).
func pipelineLooks(id string, ids []string, wf dto.WorkflowDoc, palette map[string]registry.PaletteNode, looks []lookItem,
	loras func(env string) map[string]string, add func(code, node, port, param, message string, args ...any)) {
	if len(ids) == 0 {
		return
	}
	var picked []lookItem
	for _, lookID := range ids {
		at := slices.IndexFunc(looks, func(l lookItem) bool { return l.ID == lookID })
		if at < 0 {
			add("unknown_look", id, "", "looks", "%s.looks: unknown look %s", id, lookID)
			return
		}
		picked = append(picked, looks[at])
	}
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range palette {
		schemas[kind] = node.NodeSchema
	}
	families := loras(wf.Env)
	for _, l := range picked {
		if reason := lookFit(wf.Nodes, schemas, "look", l, families); reason != "" {
			add("look_fit", id, "", "looks", "%s.looks: look %s does not fit workflow %s (%s)", id, l.Name, wf.Title, reason)
			return
		}
	}
}

// seedParam is the workflow's one unwired seed param (`<node>.<param>`), "" without exactly one.
func seedParam(wf dto.WorkflowDoc, palette map[string]registry.PaletteNode) string {
	var found []string
	for key, param := range settable(wf, palette) {
		if param.Type == "seed" {
			found = append(found, key)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

// hasKey reports whether m has key.
func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}
