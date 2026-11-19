package studio

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

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
	"unbound_input": true, "step_seeds": true, "unknown_look": true, "look_fit": true, "unknown_model": true,
	"remote_not_enabled": true, "remote_off": true, "unknown_custom": true, "custom_input": true}

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

// pipelineInfo is what the rules learn besides problems: the worst case per row, each model node's model as resolved
// (an ask workflow id, `server:<name>/<model>` or `remote:<provider>/<model>`), the step whose model a family ask
// reuses (its takes step, or the nearest before it), each fanned-out step's producer, what leaves the machine and the
// servers' reserved memory (API 1.15).
type pipelineInfo struct {
	Bounds   dto.PipelineBounds
	Models   map[string]string
	Near     map[string]string
	Fan      map[string]string // fanned-out step → its producer
	Lists    map[string]int    // producer → its list max
	Egress   []dto.PipelineEgress
	Reserved []dto.PipelineReserved
}

// providerInfo is a model provider as the rules see it (enabled ones only count).
type providerInfo struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"` // server | remote
	Enabled      *bool    `json:"enabled"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
	Host         string   `json:"host"`
	MemoryGB     float64  `json:"memory_gb"`
}

// ruleEnv is what the API 1.15 rules read besides the documents: the providers, the deployment's remote switch and the
// registered custom nodes.
type ruleEnv struct {
	Providers []providerInfo
	RemoteOn  bool
	Custom    []registry.CustomNode
}

// promptTokens bounds one prompt for the remote worst case (32768 characters / 4); answerTokens is each kind's
// max_tokens (contracts/README.md, Model sources: what leaves the machine).
const promptTokens = 8192

var answerTokens = map[string]int{"write": 1400, "write-list": 4096, "decide": 800}

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

// sourced is what a source names: kind input (typ: the input's type), takes (n at most), text (custom: a custom node's
// string field), item / items (producer: a list's node), any (a field of a custom node not registered).
type sourced struct {
	kind     string
	typ      string
	n        int
	producer string
	custom   bool
}

// fieldKind is what a source may fill in a custom field: text (a string), takes (n = maxItems ≤ 8 take ids), else "".
func fieldKind(raw json.RawMessage) (string, int) {
	var f struct {
		Type  string `json:"type"`
		Items struct {
			Type   string `json:"type"`
			Format string `json:"format"`
		} `json:"items"`
		MaxItems *int `json:"maxItems"`
	}
	_ = json.Unmarshal(raw, &f)
	switch {
	case f.Type == "string":
		return "text", 0
	case f.Type == "array" && f.Items.Type == "string" && f.Items.Format == "uuid" && f.MaxItems != nil && *f.MaxItems <= 8:
		return "takes", *f.MaxItems
	}
	return "", 0
}

// pipelineProblems checks doc with every pipeline rule, sorted as the API answers them. workflows are the documents the
// caller can run (templates and their own) by id; palette the live kinds; looks the looks the caller can use now, loras
// the LoRA id → family the caller can use now in an env (the fit rule); env the providers, the remote switch and the
// custom nodes. workflows must also hold every text template (auto resolves to them) and the ask workflows nodes name.
func pipelineProblems(doc dto.PipelineDoc, workflows map[string]dto.WorkflowDoc, palette map[string]registry.PaletteNode,
	looks []lookItem, loras func(env string) map[string]string, env ruleEnv) ([]dto.Problem, pipelineInfo) {
	info := pipelineInfo{Models: map[string]string{}, Near: map[string]string{}, Fan: map[string]string{},
		Lists: map[string]int{}, Egress: []dto.PipelineEgress{}, Reserved: []dto.PipelineReserved{}}
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
	// a decide, or an agent with routes: its one edge is a routes edge with its labels
	routers := func(id string) bool {
		return nodes[id].Kind == "decide" || (nodes[id].Kind == "agent" && nodes[id].Routes != nil)
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
		if (edge.Branch != nil && kind != "gate" && kind != "judge") || (edge.Routes != nil && kind != "decide" && kind != "agent") {
			add("branch_not_gate", from, "", "", "%s: only a gate or a judge can branch, and only a decide or an agent has routes", from)
			continue
		}
		if (routers(from) && (edge.Routes == nil || !maps.EqualFunc(edge.Routes.Routes, nodes[from].Routes,
			func(string, string) bool { return true }) || !hasKey(edge.Routes.Routes, edge.Routes.Then))) ||
			(kind == "agent" && edge.Routes != nil && !routers(from)) {
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
		case edge.Routes != nil: // every route but then counts against the node's max
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
	for _, id := range ids {
		if routers(id) && !routed[id] {
			add("decide_routes", id, "", "", "%s: a decide (or an agent with routes) needs its routes edge", id)
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
	root := ""
	if len(roots) == 1 {
		root = roots[0]
	}
	// a before b on every path from the inputs node (unchecked without exactly one inputs node)
	dominates := func(a, b string) bool {
		return root == "" || a == root || (a != b && !graph.reach(root, a, false)[b])
	}
	for _, id := range ids {
		if n := nodes[id]; (n.Kind == "write" || n.Kind == "agent") && n.List != nil {
			info.Lists[id] = n.List.Max
		}
	}
	for _, id := range of("step") {
		if each := nodes[id].Each; each != nil {
			producer, _, _ := strings.Cut(*each, ".")
			if _, ok := info.Lists[producer]; !ok || !dominates(producer, id) {
				add("fanout_source", id, "each", "", "%s.each: %s is not an agent or write with a list before this step", id, *each)
			} else {
				info.Fan[id] = producer
			}
		}
	}
	branches := func(step string) int {
		if producer, ok := info.Fan[step]; ok {
			return info.Lists[producer]
		}
		return 1
	}
	runs, calls, toolCalls := 0, 0, 0
	for _, id := range ids {
		switch n := nodes[id]; {
		case n.Kind == "step":
			runs += n.SeedsOr1() * branches(id) * visits(id)
		case dto.Asks(n.Kind):
			calls += visits(id)
		case n.Kind == "agent":
			caps := n.Caps.Filled()
			calls += visits(id) * caps.MaxTurns
			toolCalls += visits(id) * caps.MaxToolCalls
		}
	}
	info.Bounds = dto.PipelineBounds{Runs: runs, LLMCalls: calls, ToolCalls: toolCalls}
	for _, id := range ids {
		n := nodes[id]
		if n.Kind != "agent" {
			continue
		}
		if n.IsDirector() { // per batch, whatever its model source
			caps, w := n.Caps.Filled(), n.MaxWakesOr()
			info.Bounds.Chat = &dto.ChatBound{Calls: w * caps.MaxTurns * 2, Tokens: w * caps.MaxTokens, ToolCalls: w * caps.MaxToolCalls}
		}
		if slices.Contains(n.Tools, "rows.add") {
			info.Bounds.AddedRows = MaxAddedRows
		}
	}
	if runs > MaxPipelineRuns {
		add("too_many_runs", "", "", "", "a row may make %d runs; at most %d", runs, MaxPipelineRuns)
	}
	if calls > MaxPipelineCalls {
		add("too_many_calls", "", "", "", "a row may ask a model %d times; at most %d", calls, MaxPipelineCalls)
	}
	if root != "" {
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
	registered := map[string]registry.CustomNode{}
	for _, c := range env.Custom {
		registered[c.Name] = c
	}
	source := func(src string) (sourced, bool) {
		if name, ok := strings.CutPrefix(src, "$"); ok {
			typ, known := inputs[name]
			return sourced{kind: "input", typ: typ}, known
		}
		id, what, _ := strings.Cut(src, ".")
		n, known := nodes[id]
		switch {
		case !known:
			return sourced{}, false
		case what == "takes" && n.Kind == "step":
			return sourced{kind: "takes", n: n.SeedsOr1() * branches(id)}, true
		case what == "picked" && (n.Kind == "gate" || n.Kind == "judge"):
			return sourced{kind: "takes", n: n.KeepOr1()}, true
		case (what == "reasons" && n.Kind == "judge") || (what == "notes" && n.Kind == "memory" && n.Op == "read"):
			return sourced{kind: "text"}, true
		case (what == "item" || what == "items") && hasKey(info.Lists, id):
			return sourced{kind: what, producer: id}, true
		case n.Kind == "custom":
			c, ok := registered[n.CustomNode]
			if !ok {
				return sourced{kind: "any"}, true
			}
			raw, ok := c.Output.Properties[what]
			if !ok {
				return sourced{}, false
			}
			switch fk, max := fieldKind(raw); fk {
			case "text":
				return sourced{kind: "text", custom: true}, true
			case "takes":
				return sourced{kind: "takes", n: max}, true
			}
		}
		return sourced{}, false
	}
	isTextInput := func(s sourced) bool { return s.kind == "input" && s.typ == "Text" }
	for _, id := range append(of("gate"), of("judge")...) {
		gate := nodes[id]
		step, known := nodes[gate.Takes]
		switch {
		case !known || step.Kind != "step" || !dominates(gate.Takes, id):
			add("gate_takes", id, "", "", "%s: takes must name a step on every path to it", id)
		case hasKey(info.Fan, gate.Takes) && step.SeedsOr1()*branches(gate.Takes) > 8:
			add("fanout_takes", id, "", "", "%s: %s fans out to %d takes; a review shows at most 8", id, gate.Takes,
				step.SeedsOr1()*branches(gate.Takes))
		case gate.KeepOr1() > step.SeedsOr1()*branches(gate.Takes):
			add("gate_keep", id, "", "", "%s: keeps %d of %s's %d takes", id, gate.KeepOr1(), gate.Takes, step.SeedsOr1()*branches(gate.Takes))
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
		if m := n.ModelOr(); strings.HasPrefix(m, "server:") || strings.HasPrefix(m, "remote:") {
			add("model_media", id, "", "", "%s: a judge sends thumbnails; %s gets text only, so pick a local model", id, m)
		}
	}
	for _, id := range of("write") {
		n := nodes[id]
		if (n.List != nil) == (n.To != "") || (n.To != "" && inputs[strings.TrimPrefix(n.To, "$")] != "Text") {
			add("write_target", id, "", "", "%s: a write sets one Text input (to), or answers a list without to", id)
		}
	}
	for _, id := range ids {
		if n := nodes[id]; n.Kind == "write" || n.Kind == "decide" || n.Kind == "agent" || (n.Kind == "memory" && n.Op == "write") {
			for _, src := range n.Sources {
				s, ok := source(src)
				before, _, _ := strings.Cut(src, ".")
				if !ok || s.kind == "takes" || s.kind == "item" || s.kind == "items" || (s.kind == "input" && s.typ != "Text") ||
					(!strings.HasPrefix(src, "$") && !dominates(before, id)) {
					add("bind_source", id, "from", "", "%s.from: %s is not a Text input or a text source before this node", id, src)
				}
			}
		}
	}
	var directors []string
	for _, id := range of("agent") {
		if nodes[id].IsDirector() {
			directors = append(directors, id)
		}
	}
	for _, id := range of("agent") {
		if slices.Contains(nodes[id].Tools, "rows.edit") && !nodes[id].IsDirector() {
			add("agent_tools", id, "", "", "%s: only the director may change rows (rows.edit)", id)
		}
		if len(directors) > 1 && nodes[id].IsDirector() {
			add("director_count", id, "", "", "%s: a pipeline has at most one director; it has %d", id, len(directors))
		}
	}
	remote := doc.RemoteFilled()
	providers := map[string]providerInfo{}
	for _, p := range env.Providers {
		if p.Enabled == nil || *p.Enabled {
			providers[p.Name] = p
		}
	}
	// remoteOK is the deployment's switch, then the pipeline's (name "": a network custom node); it adds the problem
	remoteOK := func(id, name string) bool {
		switch {
		case !env.RemoteOn:
			add("remote_off", id, "", "", "%s: remote models are off on this deployment", id)
		case !remote.Enabled || (name != "" && !slices.Contains(remote.Providers, name)):
			add("remote_not_enabled", id, "", "", "%s: turn on remote calls for this pipeline and allow %s first", id, cmp.Or(name, "the network"))
		default:
			return true
		}
		return false
	}
	pipelineModels(doc, workflows, of("step"), dominates, providers, remoteOK, &info, add)
	for _, id := range of("custom") {
		n := nodes[id]
		c, ok := registered[n.CustomNode]
		if !ok {
			add("unknown_custom", id, "", "", "%s: custom node %s is not registered now", id, n.CustomNode)
			continue
		}
		if c.Network {
			remoteOK(id, "")
		}
		fields := c.Input.Properties
		for _, field := range c.Input.Required {
			if !hasKey(n.Fixed, field) && !hasKey(n.Bind, field) {
				add("custom_input", id, field, "", "%s.%s: a value is required", id, field)
			}
		}
		for _, field := range slices.Sorted(maps.Keys(n.Fixed)) {
			if raw, ok := fields[field]; !ok || !fitsSchema(raw, n.Fixed[field]) {
				add("custom_input", id, field, "", "%s.inputs.%s: not a field of %s, or not a value it takes", id, field, n.CustomNode)
			}
		}
		for _, field := range slices.Sorted(maps.Keys(n.Bind)) {
			src := n.Bind[field]
			s, ok := source(src)
			before, _, _ := strings.Cut(src, ".")
			if !ok || s.kind == "item" || s.kind == "items" || (!strings.HasPrefix(src, "$") && !dominates(before, id)) {
				add("bind_source", id, field, "", "%s.bind.%s: %s names no input or source before this node", id, field, src)
				continue
			}
			if s.kind == "any" {
				continue
			}
			fk, max := "", 0
			if raw, ok := fields[field]; ok {
				fk, max = fieldKind(raw)
			}
			if fk == "" || (fk == "text") != (s.kind == "text" || isTextInput(s)) || (fk == "takes" && (s.kind != "takes" || s.n > max)) {
				add("custom_input", id, field, "", "%s.bind.%s: %s does not fit this field", id, field, src)
			}
		}
	}
	pipelineEgress(doc, registered, providers, visits, &info)
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
			if !ok || (src.kind == "text" && !src.custom) || src.kind == "items" || (src.kind == "item" && info.Fan[id] != src.producer) ||
				(!strings.HasPrefix(value, "$") && src.kind != "item" && !dominates(before, id)) {
				add("bind_source", id, key, "", "%s.bind.%s: %s names no input, or no step or gate before this step", id, key, value)
				binds[key] = nil
				continue
			}
			if src.kind == "item" || src.kind == "text" { // a branch's item, a custom text: bound like a Text input
				src = sourced{kind: "input", typ: "Text"}
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
			if src == nil || src.kind == "any" {
				continue
			}
			_, rowsOfImages := imageField(param)
			var fits bool
			if src.kind == "input" {
				fits = slices.Contains(inputFits[src.typ], param.Type) || (src.typ == "Image" && rowsOfImages)
			} else {
				fits = ((param.Type == "image" || param.Type == "media") && src.n == 1) ||
					(rowsOfImages && (param.Max == nil || float64(src.n) <= *param.Max) && (src.n == 1 || !timed(param)))
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
			needsOne := param.Type == "image" || param.Type == "media" || param.Type == "span" ||
				(rowsOfImages && param.Min != nil && *param.Min >= 1)
			_, bound := n.Bind[key]
			wn, _, _ := strings.Cut(key, ".")
			optional := wf.Nodes[wn].Optional // API 1.22: an optional node's image may stay empty
			if _, set := n.Fixed[key]; needsOne && !bound && !set && !optional {
				what := map[string]string{"media": "a take", "span": "a range"}[param.Type]
				add("unbound_input", id, key, "", "%s: %s needs %s; bind it", id, key, cmp.Or(what, "an image"))
			}
		}
		if n.SeedsOr1() > 1 && !hasSeed {
			add("step_seeds", id, "", "", "%s: seeds needs a workflow with a seed param", id)
		}
	}
	return sortedProblems(found), info
}

// pipelineModels resolves each judge, write, decide and agent node's model (contracts/README.md, Agent nodes: model;
// Model sources): a named one must be a text workflow the caller can run; auto is the ask template of the family of a
// judge's takes step, or of the nearest step before the node (the step before it that every other step before it
// precedes), else llm-ask; `server:<name>[/<model>]` an enabled server with that model (default filled);
// `remote:<provider>/<model>` an enabled remote one, both switches on (remoteOK). None is unknown_model.
func pipelineModels(doc dto.PipelineDoc, workflows map[string]dto.WorkflowDoc, steps []string, dominates func(a, b string) bool,
	providers map[string]providerInfo, remoteOK func(id, name string) bool, info *pipelineInfo,
	add func(code, node, port, param, message string, args ...any)) {
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
		if !dto.HasModel(n.Kind) {
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
		wid, m := "", n.ModelOr()
		if where, rest, ok := strings.Cut(m, ":"); ok && (where == "server" || where == "remote") {
			name, model, _ := strings.Cut(rest, "/")
			if where == "remote" && !remoteOK(id, name) {
				continue
			}
			p, ok := providers[name]
			model = cmp.Or(model, p.DefaultModel)
			if ok && p.Kind == where && slices.Contains(p.Models, model) {
				wid = where + ":" + name + "/" + model
			}
		} else if m != "auto" {
			if text(m) {
				wid = m
			}
		} else {
			wid = familyAsk(near)
		}
		if wid == "" {
			add("unknown_model", id, "", "", "%s: model %s is not an ask workflow you can run or a model of an enabled provider", id, m)
			continue
		}
		info.Models[id] = wid
		if near != "" && workflows[wid].Family == workflows[doc.Nodes[near].Workflow].Family {
			info.Near[id] = near // a family ask runs on that step's model variant and precision
		}
	}
}

// pipelineEgress lists what leaves the machine (contracts/README.md, Model sources: what leaves the machine): each node
// whose model resolved to a server or remote one (a judge on one is model_media) and each network custom node, by node
// id, with its worst case per row (and per batch for the director's chat); the servers' reserved memory; and the
// remote totals on the bounds.
func pipelineEgress(doc dto.PipelineDoc, registered map[string]registry.CustomNode, providers map[string]providerInfo,
	visits func(string) int, info *pipelineInfo) {
	reserved := map[string]dto.PipelineReserved{}
	for _, id := range slices.Sorted(maps.Keys(doc.Nodes)) {
		n := doc.Nodes[id]
		if c, ok := registered[n.CustomNode]; n.Kind == "custom" && ok && c.Network {
			names := slices.Collect(maps.Keys(n.Fixed))
			for k := range n.Bind {
				if !hasKey(n.Fixed, k) {
					names = append(names, k)
				}
			}
			fields := []string{}
			for _, f := range slices.Sorted(slices.Values(names)) {
				fields = append(fields, "input:"+f)
			}
			info.Egress = append(info.Egress, dto.PipelineEgress{Node: id, Where: "network", Fields: fields})
		}
		where, rest, ok := strings.Cut(info.Models[id], ":")
		if !ok || (where != "server" && where != "remote") || n.Kind == "judge" {
			continue
		}
		name, model, _ := strings.Cut(rest, "/")
		if where == "server" {
			reserved[name] = dto.PipelineReserved{Provider: name, Host: providers[name].Host, MemoryGB: providers[name].MemoryGB}
		}
		v := visits(id)
		entry := dto.PipelineEgress{Node: id, Where: where, Provider: &name, Model: &model}
		if n.Kind == "agent" {
			caps := n.Caps.Filled()
			entry.Fields = append([]string{"instruction"}, n.Sources...)
			if n.Routes != nil {
				entry.Fields = append(entry.Fields, "routes")
			}
			for _, tool := range n.Tools {
				entry.Fields = append(entry.Fields, "tool:"+tool)
			}
			if n.IsDirector() {
				entry.Fields = append(entry.Fields, "chat")
				entry.Chat = &dto.EgressCount{Calls: n.MaxWakesOr() * caps.MaxTurns * 2, Tokens: n.MaxWakesOr() * caps.MaxTokens}
			}
			entry.PerRow = dto.EgressCount{Calls: v * caps.MaxTurns * 2, Tokens: v * caps.MaxTokens}
		} else {
			entry.Fields = []string{"question", "routes"}
			if n.Kind == "write" {
				entry.Fields = []string{"instruction"}
			}
			entry.Fields = append(entry.Fields, n.Sources...)
			kind := n.Kind
			if n.List != nil {
				kind = "write-list"
			}
			entry.PerRow = dto.EgressCount{Calls: v * 2, Tokens: v * 2 * (promptTokens + answerTokens[kind])}
		}
		info.Egress = append(info.Egress, entry)
		if where == "remote" {
			info.Bounds.RemoteCalls += entry.PerRow.Calls
			info.Bounds.RemoteTokens += entry.PerRow.Tokens
		}
	}
	for _, name := range slices.Sorted(maps.Keys(reserved)) {
		info.Reserved = append(info.Reserved, reserved[name])
	}
}

// fitsSchema reports whether value fits a custom field's JSON schema: the subset pydantic writes for plain fields
// (type, enum, string lengths, number bounds, array lengths and items).
// ponytail: a subset validator, not full JSON Schema; enough for pydantic's flat models, extend as packs need
func fitsSchema(raw json.RawMessage, value any) bool {
	var s struct {
		Type      any             `json:"type"`
		Enum      []any           `json:"enum"`
		MinLength *int            `json:"minLength"`
		MaxLength *int            `json:"maxLength"`
		Minimum   *float64        `json:"minimum"`
		Maximum   *float64        `json:"maximum"`
		MinItems  *int            `json:"minItems"`
		MaxItems  *int            `json:"maxItems"`
		Items     json.RawMessage `json:"items"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	if s.Enum != nil && !slices.ContainsFunc(s.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(value) }) {
		return false
	}
	types := []string{}
	switch t := s.Type.(type) {
	case string:
		types = append(types, t)
	case []any:
		for _, x := range t {
			types = append(types, fmt.Sprint(x))
		}
	}
	is := func(typ string) bool {
		switch v := value.(type) {
		case nil:
			return typ == "null"
		case string:
			n := utf8.RuneCountInString(v)
			return typ == "string" && (s.MinLength == nil || n >= *s.MinLength) && (s.MaxLength == nil || n <= *s.MaxLength)
		case bool:
			return typ == "boolean"
		case float64:
			ok := typ == "number" || (typ == "integer" && v == float64(int64(v)))
			return ok && (s.Minimum == nil || v >= *s.Minimum) && (s.Maximum == nil || v <= *s.Maximum)
		case []any:
			if typ != "array" || (s.MinItems != nil && len(v) < *s.MinItems) || (s.MaxItems != nil && len(v) > *s.MaxItems) {
				return false
			}
			return len(s.Items) == 0 || !slices.ContainsFunc(v, func(item any) bool { return !fitsSchema(s.Items, item) })
		case map[string]any:
			return typ == "object"
		}
		return false
	}
	return len(types) == 0 || slices.ContainsFunc(types, is)
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
