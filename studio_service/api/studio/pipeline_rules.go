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
	// MaxPipelineCalls bounds the judge, write and decide visits (LLM calls) of one row (API 1.14).
	MaxPipelineCalls = 32
)

// pipelineRunOnly are the codes that block running only; every other code blocks saving (and running).
var pipelineRunOnly = map[string]bool{"unreachable": true, "dead_end": true, "no_output": true, "unknown_workflow": true,
	"workflow_cannot_run": true, "unknown_preset": true, "step_input_invalid": true, "bind_unknown": true, "bind_type": true,
	"unbound_input": true, "step_seeds": true, "unknown_look": true, "look_fit": true, "unknown_model": true}

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
	gate, fail string // the branching node and the capped edge's target
	max        int
}

// pipelineInfo is what the rules learn besides problems: the worst case per row, each judge, write and decide node's
// ask workflow as resolved, and the step whose model a family ask reuses (its takes step, or the nearest before it).
type pipelineInfo struct {
	Bounds dto.PipelineBounds
	Models map[string]string
	Near   map[string]string
}

// isText reports whether a workflow document answers text (an ask workflow).
func isText(wf dto.WorkflowDoc) bool { return registry.IsText(wf.Nodes) }

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
// workflows must also hold every text template (auto resolves to them) and the ask workflows nodes name.
func pipelineProblems(doc dto.PipelineDoc, workflows map[string]dto.WorkflowDoc, palette map[string]registry.PaletteNode,
	looks []lookItem, loras func(env string) map[string]string) ([]dto.Problem, pipelineInfo) {
	info := pipelineInfo{Models: map[string]string{}, Near: map[string]string{}}
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
		return sortedProblems(found), info // nothing else is checked: the work stays bounded
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
	outdeg, routed := map[string]int{}, map[string]bool{}
	for _, edge := range doc.Edges {
		from, targets := edge.From, edge.Targets()
		_, known := nodes[from]
		if !known || slices.ContainsFunc(targets, func(t string) bool { _, ok := nodes[t]; return !ok }) {
			add("unknown_node", from, "", "", "%s: an edge names a node that is not in the pipeline", from)
			continue
		}
		kind := nodes[from].Kind
		if (edge.Branch != nil && kind != "gate" && kind != "judge") || (edge.Routes != nil && kind != "decide") {
			add("branch_not_gate", from, "", "", "%s: only a gate or a judge can branch, and only a decide has routes", from)
			continue
		}
		if kind == "decide" && (edge.Routes == nil || !maps.EqualFunc(edge.Routes.Routes, nodes[from].Routes,
			func(string, string) bool { return true }) || !hasKey(edge.Routes.Routes, edge.Routes.Then)) {
			add("decide_routes", from, "", "", "%s: its one edge maps each of its routes to a node, and then names one", from)
			continue
		}
		outdeg[from]++
		if outdeg[from] > 1 || nodes[from].Kind == "output" ||
			slices.ContainsFunc(targets, func(t string) bool { return nodes[t].Kind == "inputs" }) {
			add("edge_invalid", from, "", "", "%s: a node has one way out, an output none, and nothing leads into the inputs node", from)
			continue
		}
		switch {
		case edge.Branch != nil:
			b := edge.Branch
			graph.succ[from] = append(graph.succ[from], pipelineHop{b.Pass, false}, pipelineHop{b.Fail, true}, pipelineHop{b.Then, false})
			graph.capped = append(graph.capped, cappedEdge{from, b.Fail, b.Max})
		case edge.Routes != nil: // every route but then counts against the decide's max
			routed[from] = true
			for _, label := range slices.Sorted(maps.Keys(edge.Routes.Routes)) {
				to, capped := edge.Routes.Routes[label], label != edge.Routes.Then
				graph.succ[from] = append(graph.succ[from], pipelineHop{to, capped})
				if capped {
					graph.capped = append(graph.capped, cappedEdge{from, to, edge.Routes.Max})
				}
			}
		default:
			graph.succ[from] = append(graph.succ[from], pipelineHop{edge.To, false})
		}
	}
	for _, id := range of("decide") {
		if !routed[id] {
			add("decide_routes", id, "", "", "%s: a decide needs its routes edge", id)
		}
	}
	reach := func(id string) map[string]bool { return graph.reach(id, "", false) }
	for _, id := range ids { // ponytail: a walk per node, O(n·e); fine at 24 nodes
		if graph.reach(id, "", true)[id] {
			add("uncapped_cycle", id, "", "", "%s: it loops back to itself without a capped fail edge", id)
		}
	}
	visits := func(id string) int {
		n := 1
		for _, c := range graph.capped {
			if (id == c.fail || reach(c.fail)[id]) && reach(id)[c.gate] {
				n += c.max
			}
		}
		return n
	}
	runs, calls := 0, 0
	for _, id := range ids {
		switch kind := nodes[id].Kind; {
		case kind == "step":
			runs += nodes[id].SeedsOr1() * visits(id)
		case dto.Asks(kind):
			calls += visits(id)
		}
	}
	info.Bounds = dto.PipelineBounds{Runs: runs, LLMCalls: calls}
	if runs > MaxPipelineRuns {
		add("too_many_runs", "", "", "", "a row may make %d runs; at most %d", runs, MaxPipelineRuns)
	}
	if calls > MaxPipelineCalls {
		add("too_many_calls", "", "", "", "a row may ask a model %d times; at most %d", calls, MaxPipelineCalls)
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
	// source is what a source names: an input's type, at most how many takes, or text; ok false when it names nothing
	type sourced struct {
		input string // the input's type, "" for takes or text
		takes int
		text  bool // a judge's reasons, a memory read's notes
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
		case what == "picked" && (n.Kind == "gate" || n.Kind == "judge"):
			return sourced{takes: n.KeepOr1()}, true
		case (what == "reasons" && n.Kind == "judge") || (what == "notes" && n.Kind == "memory" && n.Op == "read"):
			return sourced{text: true}, true
		}
		return sourced{}, false
	}
	for _, id := range append(of("gate"), of("judge")...) {
		gate := nodes[id]
		step, known := nodes[gate.Takes]
		if !known || step.Kind != "step" || !dominates(gate.Takes, id) {
			add("gate_takes", id, "", "", "%s: takes must name a step on every path to it", id)
		} else if gate.KeepOr1() > step.SeedsOr1() {
			add("gate_keep", id, "", "", "%s: keeps %d of %s's %d takes", id, gate.KeepOr1(), gate.Takes, step.SeedsOr1())
		}
	}
	for _, id := range of("judge") {
		n := nodes[id]
		lines := n.Criteria.Lines()
		if len(lines) < 1 || len(lines) > 12 || len(slices.Compact(slices.Sorted(slices.Values(lines)))) < len(lines) {
			add("judge_criteria", id, "", "", "%s: a checklist has 1 to 12 lines, none twice", id)
		}
		if m := n.Memory; m != nil {
			if mem, ok := nodes[*m]; !ok || mem.Kind != "memory" || mem.Op != "read" || !dominates(*m, id) {
				add("bind_source", id, "memory", "", "%s.memory: %s is not a memory read before this judge", id, *m)
			}
		}
	}
	for _, id := range of("write") {
		if inputs[strings.TrimPrefix(nodes[id].To, "$")] != "Text" {
			add("write_target", id, "", "", "%s.to: %s is not a Text input", id, nodes[id].To)
		}
	}
	for _, id := range ids {
		if n := nodes[id]; n.Kind == "write" || n.Kind == "decide" || (n.Kind == "memory" && n.Op == "write") {
			for _, src := range n.Sources {
				s, ok := source(src)
				before, _, _ := strings.Cut(src, ".")
				if !ok || (!s.text && s.input != "Text") || (!strings.HasPrefix(src, "$") && !dominates(before, id)) {
					add("bind_source", id, "from", "", "%s.from: %s is not a Text input or a text source before this node", id, src)
				}
			}
		}
	}
	pipelineModels(doc, workflows, of("step"), dominates, &info, add)
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
			if !ok || src.text || (!strings.HasPrefix(value, "$") && !dominates(before, id)) {
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
		} else if isText(wf) {
			add("workflow_cannot_run", id, "", "", "%s: workflow %s answers text; only a judge, write or decide node runs it", id, n.Workflow)
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
	return sortedProblems(found), info
}

// pipelineModels resolves each judge, write and decide node's model (contracts/README.md, Agent nodes: model): a named
// one must be a text workflow the caller can run; auto is the ask template of the family of a judge's takes step, or
// of the nearest step before a write or decide (the step before it that every other step before it precedes), else
// llm-ask; none is unknown_model.
func pipelineModels(doc dto.PipelineDoc, workflows map[string]dto.WorkflowDoc, steps []string, dominates func(a, b string) bool,
	info *pipelineInfo, add func(code, node, port, param, message string, args ...any)) {
	text := func(id string) bool { wf, ok := workflows[id]; return ok && isText(wf) }
	familyAsk := func(step string) string {
		node, ok := doc.Nodes[step]
		family := ""
		if ok && node.Kind == "step" {
			family = workflows[node.Workflow].Family
		}
		for _, id := range slices.Sorted(maps.Keys(workflows)) {
			if family != "" && !uuidLike(id) && workflows[id].Family == family && text(id) {
				return id
			}
		}
		if text("llm-ask") {
			return "llm-ask"
		}
		return ""
	}
	for _, id := range slices.Sorted(maps.Keys(doc.Nodes)) {
		n := doc.Nodes[id]
		if !dto.Asks(n.Kind) {
			continue
		}
		near := n.Takes
		if n.Kind != "judge" {
			var before []string
			for _, s := range steps {
				if s != id && dominates(s, id) {
					before = append(before, s)
				}
			}
			at := slices.IndexFunc(before, func(d string) bool {
				return !slices.ContainsFunc(before, func(o string) bool { return o != d && !dominates(o, d) })
			})
			near = ""
			if at >= 0 {
				near = before[at]
			}
		}
		wid := ""
		if m := n.ModelOr(); m != "auto" {
			if text(m) {
				wid = m
			}
		} else {
			wid = familyAsk(near)
		}
		if wid == "" {
			add("unknown_model", id, "", "", "%s: model %s is not an ask workflow you can run", id, n.ModelOr())
			continue
		}
		info.Models[id] = wid
		if near != "" && workflows[wid].Family == workflows[doc.Nodes[near].Workflow].Family {
			info.Near[id] = near // a family ask runs on that step's model variant and precision
		}
	}
}

// uuidLike reports whether a workflow id is a user workflow's (a uuid) rather than a template slug.
func uuidLike(id string) bool { return len(id) == 36 && strings.Count(id, "-") == 4 }

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
