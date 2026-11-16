package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// Batches of a pipeline (contracts/README.md "Batches of a pipeline"): Submit checks every row against every step and
// stores what the batch runs (batches.pipeline): the document as it was, each workflow it uses with its schemas.

// storedPipeline is batches.pipeline: everything a pipeline batch runs, never the live documents.
type storedPipeline struct {
	ID         string                       `json:"id"`
	Title      string                       `json:"title"`
	Version    int                          `json:"version"`
	Doc        dto.PipelineDoc              `json:"doc"`
	Workflows  map[string]registry.Workflow `json:"workflows"`        // with node_schemas, as resolve needs them
	Versions   map[string]int               `json:"versions"`         // a user workflow's version at Submit
	Presets    map[string]map[string]any    `json:"presets"`          // step → its preset's values
	SeedParams map[string]string            `json:"seed_params"`      // step → its workflow's one unwired seed param
	Models     map[string]string            `json:"models,omitempty"` // judge, write, decide, agent → its ask workflow (API 1.14)
	Near       map[string]string            `json:"near,omitempty"`   // such a node → the step whose model a family ask reuses
	Bounds     dto.PipelineBounds           `json:"bounds"`
	// API 1.15: each model node's model as listed (server:<n>/<m>, remote:<p>/<m>) and where it runs (defs
	// modelSource), the providers as they were, the remote switch, the fan-outs, the custom nodes' registrations
	Listed    map[string]string              `json:"listed,omitempty"`
	Sources   map[string]map[string]string   `json:"sources,omitempty"`
	Providers map[string]map[string]any      `json:"providers,omitempty"`
	Remote    dto.RemoteSettings             `json:"remote"`
	Fan       map[string]string              `json:"fan,omitempty"`
	Lists     map[string]int                 `json:"lists,omitempty"`
	Custom    map[string]registry.CustomNode `json:"custom,omitempty"`
}

// placeholderText stands in for a text a step will get from an earlier node (a fanned-out item, a custom node's
// field): Submit checks the rest of the row.
const placeholderText = "a shot"

// placeholderTake stands in for a take a step will get from an earlier step: Submit checks the rest of the row.
const placeholderTake = "00000000-0000-4000-8000-000000000000"

// root is the document's inputs node.
func (p storedPipeline) root() string {
	for _, id := range slices.Sorted(maps.Keys(p.Doc.Nodes)) {
		if p.Doc.Nodes[id].Kind == "inputs" {
			return id
		}
	}
	return ""
}

// steps are the step ids, sorted.
func (p storedPipeline) steps() []string {
	var out []string
	for _, id := range slices.Sorted(maps.Keys(p.Doc.Nodes)) {
		if p.Doc.Nodes[id].Kind == "step" {
			out = append(out, id)
		}
	}
	return out
}

// firstStep is the first step along the edges from the inputs node (a branch's pass, a decide's first route by
// label), else the first by id.
func (p storedPipeline) firstStep() string {
	next := map[string]string{}
	for _, edge := range p.Doc.Edges {
		if _, taken := next[edge.From]; !taken {
			next[edge.From] = edge.Targets()[0] // to, a branch's pass, or a decide's first route
		}
	}
	at := p.root()
	for range len(p.Doc.Nodes) {
		at = next[at]
		if p.Doc.Nodes[at].Kind == "step" {
			return at
		}
	}
	return p.steps()[0]
}

// fixed is a step's inputs: its preset's values, then its own.
func (p storedPipeline) fixed(step string) map[string]any {
	out := maps.Clone(p.Presets[step])
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, p.Doc.Nodes[step].Fixed)
	return out
}

// stepInputs are the inputs a run of step for a row would get, as Submit checks them: fixed, then each bind (a
// pipeline input's value, or placeholder takes for an earlier step's), then the seed.
func (p storedPipeline) stepInputs(step string, row map[string]any, seed int64) map[string]any {
	node, workflow := p.Doc.Nodes[step], p.Workflows[p.Doc.Nodes[step].Workflow]
	inputs, root := p.fixed(step), p.root()
	for key, source := range node.Bind {
		if strings.Contains(key, "[") { // a re-render's row bind (API 1.17): the row's fixed value stands in for its take
			continue
		}
		param, _ := findParam(workflow, key)
		field, rowsOfImages := imageField(param)
		if name, ok := strings.CutPrefix(source, "$"); ok {
			value := row[root+"."+name]
			if rowsOfImages {
				value = []any{map[string]any{field: value}}
			}
			inputs[key] = value
			continue
		}
		if from, field, _ := strings.Cut(source, "."); field == "item" || p.Doc.Nodes[from].Kind == "custom" {
			if param.Type == "string" || param.Type == "text" { // a branch's item or a custom text (a custom take list: takes)
				inputs[key] = placeholderText
				continue
			}
		}
		ref := map[string]any{"take_id": placeholderTake}
		if rowsOfImages {
			inputs[key] = []any{map[string]any{field: ref}}
		} else {
			inputs[key] = ref
		}
	}
	if key := p.SeedParams[step]; key != "" {
		inputs[key] = float64(seed)
	}
	return inputs
}

// baseSeed is a row's base seed for step: the bound Seed input's value, else a fixed seed ≥ 0, else drawn.
func (p storedPipeline) baseSeed(step string, row map[string]any, draw func() int64) int64 {
	key := p.SeedParams[step]
	if name, ok := strings.CutPrefix(p.Doc.Nodes[step].Bind[key], "$"); ok {
		if value, ok := row[p.root()+"."+name].(float64); ok {
			return int64(value)
		}
	}
	if value, ok := p.fixed(step)[key].(float64); ok && value >= 0 {
		return int64(value)
	}
	return draw()
}

// snapshot is the pipeline as cmd.pipeline.submit carries it (defs pipelineSnapshot).
func (p storedPipeline) snapshot() *message.PipelineSnapshot {
	out := &message.PipelineSnapshot{ID: p.ID, Title: p.Title, Version: p.Version, Nodes: map[string]map[string]any{},
		Workflows: map[string]message.SnapshotWorkflow{}}
	out.Edges, _ = json.Marshal(p.Doc.Edges)
	for id, node := range p.Doc.Nodes {
		data, _ := json.Marshal(node)
		fields := map[string]any{}
		_ = json.Unmarshal(data, &fields)
		switch node.Kind {
		case "step":
			fields["seeds"], fields["inputs"], fields["bind"] = node.SeedsOr1(), p.fixed(id), node.Bind
			if node.Bind == nil {
				fields["bind"] = map[string]string{}
			}
			fields["seed_param"] = nil
			if key := p.SeedParams[id]; key != "" {
				fields["seed_param"] = key
			}
			if producer, ok := p.Fan[id]; ok {
				fields["fanout_max"] = p.Lists[producer]
			}
		case "gate":
			fields["keep"] = node.KeepOr1()
		case "judge":
			fields["keep"], fields["mode"], fields["model"] = node.KeepOr1(), node.ModeOr(), node.ModelOr()
			// both lists, [] when missing (the snapshot requires them; the struct leaves an empty list out)
			fields["criteria"] = map[string][]string{"must": nonNil(node.Criteria.Must), "nice": nonNil(node.Criteria.Nice)}
		case "write":
			fields["instruction"], fields["model"] = node.InstructionOr(), node.ModelOr()
		case "decide":
			fields["model"] = node.ModelOr()
		case "memory":
			if node.Op == "read" {
				fields["limit"] = node.LimitOr()
			}
		case "agent":
			fields["from"], fields["tools"], fields["caps"] = nonNil(node.Sources), nonNil(node.Tools), node.Caps.Filled()
			fields["director"], fields["model"] = node.IsDirector(), node.ModelOr()
			if node.IsDirector() {
				fields["wake_on_rows"], fields["max_wakes"] = node.WakeOnRowsOr(), node.MaxWakesOr()
			}
		case "custom":
			c := p.Custom[id]
			fields["version"], fields["network"] = c.Version, c.Network
			fields["inputs"], fields["bind"] = node.Fixed, node.Bind
			if node.Fixed == nil {
				fields["inputs"] = map[string]any{}
			}
			if node.Bind == nil {
				fields["bind"] = map[string]string{}
			}
		}
		if dto.HasModel(node.Kind) {
			fields["workflow"], fields["source"] = p.Models[id], p.Sources[id]
		}
		out.Nodes[id] = fields
	}
	for id, workflow := range p.Workflows {
		out.Workflows[id] = snapshotWorkflow(workflow)
	}
	out.Providers, out.Remote = p.Providers, &p.Remote
	if out.Providers == nil {
		out.Providers = map[string]map[string]any{}
	}
	return out
}

// snapshotWorkflow is a workflow as a submit carries it.
func snapshotWorkflow(workflow registry.Workflow) message.SnapshotWorkflow {
	return message.SnapshotWorkflow{ID: workflow.ID, Title: workflow.Title, Family: workflow.Family, Env: workflow.Env,
		Nodes: workflow.Nodes}
}

// version is a step workflow's version at Submit (nil for a template).
func (p storedPipeline) version(workflowID string) *int {
	if v, ok := p.Versions[workflowID]; ok {
		return &v
	}
	return nil
}

// readStoredPipeline reads a batch's stored pipeline; ok false for a batch of one workflow.
func readStoredPipeline(ctx context.Context, tx pgx.Tx, batchID string) (storedPipeline, bool, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT pipeline FROM batches WHERE id = $1`, batchID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedPipeline{}, false, errNotFound
	} else if err != nil || raw == nil {
		return storedPipeline{}, false, err
	}
	var out storedPipeline
	return out, true, json.Unmarshal(raw, &out)
}

// createPipelineBatch is Submit with pipeline_id, inside CreateBatch's transaction (session checked, cap held).
func (s *Service) createPipelineBatch(ctx context.Context, tx pgx.Tx, caller common.Caller, body *dto.BatchCreate) (dto.Batch, error) {
	unprocessable := func(format string, args ...any) error {
		return network.NewUnprocessableError(fmt.Sprintf(format, args...), nil)
	}
	row, err := readPipeline(ctx, tx, body.PipelineID, false)
	if errors.Is(err, errNotFound) {
		return dto.Batch{}, unprocessable("unknown pipeline %s", body.PipelineID)
	} else if err != nil {
		return dto.Batch{}, err
	}
	doc := pipelineDoc(row)
	// the step workflows stay as checked until commit, so the snapshot (runnable's read below) is what was checked
	var own []string
	for _, node := range doc.Nodes {
		if node.Kind == "step" && utility.IsUUID(node.Workflow) {
			own = append(own, node.Workflow)
		}
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM user_workflows WHERE id = ANY($1::uuid[]) FOR SHARE`, own); err != nil {
		return dto.Batch{}, err
	}
	problems, docs, info, err := s.pipelineProblemsNow(ctx, tx, caller.ID, doc)
	if err != nil {
		return dto.Batch{}, err
	} else if len(problems) > 0 {
		return dto.Batch{}, unprocessable("pipeline cannot run: %s", problems[0].Message)
	}
	stored := storedPipeline{ID: row.ID, Title: row.Title, Version: row.Version, Doc: doc,
		Workflows: map[string]registry.Workflow{}, Versions: map[string]int{}, Presets: map[string]map[string]any{},
		SeedParams: map[string]string{}, Models: map[string]string{}, Near: info.Near, Bounds: info.Bounds,
		Listed: info.Models, Sources: map[string]map[string]string{}, Providers: map[string]map[string]any{},
		Remote: doc.RemoteFilled(), Fan: info.Fan, Lists: info.Lists, Custom: map[string]registry.CustomNode{}}
	if err := s.modelSources(ctx, tx, &stored); err != nil {
		return dto.Batch{}, err
	}
	palette := s.palette()
	for _, step := range stored.steps() {
		node := doc.Nodes[step]
		if _, done := stored.Workflows[node.Workflow]; !done {
			workflow, version, err := s.runnable(ctx, tx, node.Workflow)
			if err != nil {
				return dto.Batch{}, err
			}
			stored.Workflows[node.Workflow] = workflow
			if version != nil {
				stored.Versions[node.Workflow] = *version
			}
		}
		if node.Preset != nil {
			stored.Presets[step] = docs[node.Workflow].Studio.Presets[*node.Preset]
		}
		if key := seedParam(docs[node.Workflow], palette); key != "" {
			stored.SeedParams[step] = key
		}
	}
	if err := s.askWorkflows(ctx, tx, &stored); err != nil {
		return dto.Batch{}, err
	}
	rows, detail := pipelineRows(stored, body, randomBase)
	if detail != "" {
		return dto.Batch{}, unprocessable("%s", detail)
	}
	return s.submitPipeline(ctx, tx, caller, body, stored, info, rows, &row.ID, nil)
}

// submitPipeline checks every row against every step and stores and submits the batch: a saved pipeline's
// (pipelineID), or a snapshot studio built (a sequence's Re-render: timelineID, API 1.17).
func (s *Service) submitPipeline(ctx context.Context, tx pgx.Tx, caller common.Caller, body *dto.BatchCreate,
	stored storedPipeline, info pipelineInfo, rows []map[string]any, pipelineID, timelineID *string) (dto.Batch, error) {
	unprocessable := func(format string, args ...any) error {
		return network.NewUnprocessableError(fmt.Sprintf(format, args...), nil)
	}
	doc := stored.Doc
	// the rows' own takes as Create checks them; takes from earlier steps are checked when their runs are made
	root, columns := stored.root(), doc.Nodes[stored.root()].Columns
	perRow := make([][]imageAt, len(rows))
	var all []imageAt
	for i, inputs := range rows {
		for _, column := range columns {
			if ref, ok := inputs[root+"."+column.Name].(map[string]any); ok && column.Type == "Image" {
				perRow[i] = append(perRow[i], imageAt{Path: root + "." + column.Name, TakeID: ref["take_id"].(string)})
			}
		}
		all = append(all, perRow[i]...)
	}
	bad, err := vetTakes(ctx, tx, s.store, caller.ID, all, false)
	if err != nil {
		return dto.Batch{}, err
	}
	for i, refs := range perRow {
		if detail := firstBad(refs, bad); detail != "" {
			return dto.Batch{}, unprocessable("row %d: %s", i+1, detail)
		}
	}
	// every row against every step, with seeds drawn per row and step
	seeds := make([]map[string]int64, len(rows))
	repeat := 1 // an input row is named once, as pipelineRows names it
	if body.Seeds != nil {
		repeat = body.Seeds.Count
	}
	var graphs []stepGraph
	for i, inputs := range rows {
		seeds[i] = map[string]int64{}
		for _, step := range stored.steps() {
			base := int64(0)
			if stored.SeedParams[step] != "" {
				base = stored.baseSeed(step, inputs, randomBase)
				seeds[i][step] = base
			}
			workflow := stored.Workflows[doc.Nodes[step].Workflow]
			nodes, detail := resolve(workflow, stored.stepInputs(step, inputs, base))
			if detail != "" {
				return dto.Batch{}, unprocessable("row %d: %s: %s", i+1, step, detail)
			}
			if i%repeat == 0 {
				graphs = append(graphs, stepGraph{fmt.Sprintf("row %d: %s", i/repeat+1, step), nodes, workflow.NodeSchemas,
					workflow.Env})
			}
			if i > 0 {
				continue
			}
			graph, _ := json.Marshal(nodes)
			probe := model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph,
				WorkflowVersion: stored.version(workflow.ID)}
			if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" {
				return dto.Batch{}, unprocessable("%s", reason)
			}
		}
	}
	lib, detail, err := s.pipelineMentions(ctx, tx, caller.ID, graphs)
	if err != nil {
		return dto.Batch{}, err
	} else if detail != "" {
		return dto.Batch{}, unprocessable("%s", detail)
	}
	looks, err := readLooks(ctx, tx, s.registry, caller.ID, stepLookIDs(doc), nil) // the batch's copy (checked above)
	if err != nil {
		return dto.Batch{}, err
	}
	if detail, err := s.pipelineLoras(ctx, tx, caller.ID, graphs); err != nil {
		return dto.Batch{}, err
	} else if detail != "" {
		return dto.Batch{}, unprocessable("%s", detail)
	}
	snapshot := stored.snapshot()
	size, _ := json.Marshal(snapshot)
	total := len(size)
	for _, inputs := range rows {
		data, _ := json.Marshal(inputs)
		total += len(data)
	}
	if total > MaxBatchInputs {
		return dto.Batch{}, unprocessable("batch inputs are too large; use fewer or shorter rows")
	}
	title, sessionID, err := batchSession(ctx, tx, caller, body, doc.Title, len(rows))
	if err != nil {
		return dto.Batch{}, err
	}
	first := stored.Workflows[doc.Nodes[stored.firstStep()].Workflow]
	var collection *string
	for _, id := range slices.Sorted(maps.Keys(doc.Nodes)) {
		if doc.Nodes[id].Kind == "output" && collection == nil {
			name := doc.Nodes[id].Collection
			collection = &name
		}
	}
	base := body.Inputs
	if base == nil {
		base = map[string]any{}
	}
	batchID := utility.NewID()
	var director, remote any // API 1.15: the director's chat, the remote counters
	for _, id := range slices.Sorted(maps.Keys(doc.Nodes)) {
		if n := doc.Nodes[id]; n.IsDirector() {
			director = dto.BatchDirector{Node: id, State: "idle", MaxWakes: n.MaxWakesOr()}
		}
	}
	if stored.Remote.Enabled {
		remote = dto.BatchRemote{MaxCalls: stored.Remote.MaxCalls, MaxTokens: stored.Remote.MaxTokens}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO batches (id, owner_id, session_id, title, workflow_id, workflow_title, snapshot,
		inputs, workflow_version, pipeline_id, pipeline_title, pipeline_version, collection, pipeline, elements, looks, bounds,
		models, providers, egress, reserved, director, remote, timeline_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		$14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`, batchID, caller.ID,
		sessionID, title, first.ID, first.Title, first, base, stored.version(first.ID), pipelineID, stored.Title, stored.Version,
		collection, stored, lib, looks, stored.Bounds, stored.Listed, append([]string{}, slices.Sorted(maps.Keys(stored.Providers))...), info.Egress,
		info.Reserved, director, remote, timelineID); err != nil {
		return dto.Batch{}, err
	}
	numbers := make([]int, len(rows))
	submitRows := make([]message.SubmitRow, len(rows))
	for i, inputs := range rows {
		numbers[i] = i + 1
		submitRows[i] = message.SubmitRow{Row: i + 1, Inputs: inputs, Seeds: seeds[i]}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO batch_rows (batch_id, n, owner_id, inputs, seeds, cells)
		SELECT $1, n, $2, inputs, seeds, '[]' FROM unnest($3::int[], $4::jsonb[], $5::jsonb[]) AS u(n, inputs, seeds)`,
		batchID, caller.ID, numbers, rows, seeds); err != nil {
		return dto.Batch{}, err
	}
	if err := appendBatchEvent(ctx, tx, batchID, "batch", 0); err != nil {
		return dto.Batch{}, err
	}
	err = messaging.Enqueue(ctx, tx, messaging.Command{JobID: batchID, OwnerID: caller.ID, Attempt: 1,
		Data: message.Submit{BatchID: batchID, SessionID: sessionID, Inputs: base, Rows: submitRows,
			Snapshot: message.Snapshot{WorkflowID: first.ID, Workflow: snapshotWorkflow(first), Pipeline: snapshot}}})
	if err != nil {
		return dto.Batch{}, err
	}
	return getBatch(ctx, tx, batchID, false)
}

// modelSources fills where each model node runs (contracts/README.md, Model sources): a local ask workflow, the
// built-in server-ask on a local server's host, or a remote provider (workflow `remote`, no run); the providers as
// they are now (the batch calls them as at Submit); and the custom nodes' registrations.
func (s *Service) modelSources(ctx context.Context, tx pgx.Tx, stored *storedPipeline) error {
	providers, err := readProviders(ctx, tx, true)
	if err != nil {
		return err
	}
	for _, node := range slices.Sorted(maps.Keys(stored.Listed)) {
		listed := stored.Listed[node]
		where, rest, ok := strings.Cut(listed, ":")
		if !ok {
			stored.Models[node] = listed
			stored.Sources[node] = map[string]string{"kind": "local", "workflow": listed}
			continue
		}
		name, model, _ := strings.Cut(rest, "/")
		at := slices.IndexFunc(providers, func(p providerRow) bool { return p.Name == name })
		if at < 0 { // deleted since the rules read it (same transaction: never), kept for safety
			return network.NewUnprocessableError(fmt.Sprintf("pipeline cannot run: %s: provider %s is gone", node, name), nil)
		}
		p := providers[at]
		stored.Providers[name] = p.snapshot()
		stored.Sources[node] = map[string]string{"kind": where, "provider": name, "model": model}
		stored.Models[node] = "remote"
		if where == "server" {
			stored.Models[node], stored.Sources[node]["host"] = ServerAsk, deref(p.Host)
		}
	}
	for id, n := range stored.Doc.Nodes {
		if n.Kind == "custom" {
			for _, c := range s.registry.Custom() {
				if c.Name == n.CustomNode {
					stored.Custom[id] = c
				}
			}
		}
	}
	return nil
}

// ServerAsk is the built-in text workflow a local server's calls run as (pipelines' server executor publishes it).
const ServerAsk = "server-ask"

// askWorkflows adds every resolved ask workflow to the stored pipeline (as runnable reads it now) and checks a live
// host for each (askHosts). A remote model has none: pipelines calls it, never as a run.
func (s *Service) askWorkflows(ctx context.Context, tx pgx.Tx, stored *storedPipeline) error {
	for _, node := range slices.Sorted(maps.Keys(stored.Models)) {
		id := stored.Models[node]
		if id == "remote" {
			continue
		}
		if _, done := stored.Workflows[id]; !done {
			workflow, version, err := s.runnable(ctx, tx, id)
			if err != nil {
				return err
			}
			stored.Workflows[id] = workflow
			if version != nil {
				stored.Versions[id] = *version
			}
		}
	}
	return s.askHosts(*stored)
}

// askHosts checks a live host for every ask workflow of the stored pipeline as its runs will ask: with the reused
// step's model variant and precision (none: 422 no live host for env <env>).
func (s *Service) askHosts(stored storedPipeline) error {
	for _, node := range slices.Sorted(maps.Keys(stored.Models)) {
		id := stored.Models[node]
		if id == "remote" {
			continue
		}
		workflow := stored.Workflows[id]
		nodes, detail := resolve(workflow, stored.askModel(node))
		if detail != "" {
			return network.NewUnprocessableError(fmt.Sprintf("%s: %s", node, detail), nil)
		}
		graph, _ := json.Marshal(nodes)
		probe := model.Run{WorkflowID: id, Env: workflow.Env, Nodes: graph, WorkflowVersion: stored.version(id),
			PinHost: stored.pinHost(node)}
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" {
			return network.NewUnprocessableError(reason, nil)
		}
	}
	return nil
}

// pinHost is the host a model node's server-ask runs must go to (its provider's), nil for any other source.
func (p storedPipeline) pinHost(node string) *string {
	if source := p.Sources[node]; source["kind"] == "server" {
		host := source["host"]
		return &host
	}
	return nil
}

// askModel is the model a family ask of node runs with: the reused step's merged variant and precision
// (`<model node>.variant`, `.precision` of the ask workflow), none for another family's ask.
func (p storedPipeline) askModel(node string) map[string]any {
	out := map[string]any{}
	step, ok := p.Near[node]
	if !ok {
		return out
	}
	stepWorkflow, ask := p.Workflows[p.Doc.Nodes[step].Workflow], p.Workflows[p.Models[node]]
	from, to := modelNode(stepWorkflow), modelNode(ask)
	if from == "" || to == "" {
		return out
	}
	fixed := p.fixed(step)
	for _, param := range []string{"variant", "precision"} {
		value, set := fixed[from+"."+param]
		if !set {
			value = stepWorkflow.Nodes[from].Params[param]
		}
		if value != nil {
			out[to+"."+param] = value
		}
	}
	return out
}

// modelNode is a workflow's <family>/model node id, "" without one.
func modelNode(workflow registry.Workflow) string {
	for _, id := range slices.Sorted(maps.Keys(workflow.Nodes)) {
		if strings.HasSuffix(workflow.Nodes[id].Kind, "/model") {
			return id
		}
	}
	return ""
}

// stepGraph is one input row's resolved graph of one step, named for the 422 detail ("row 3: step_1").
type stepGraph struct {
	Where   string
	Nodes   map[string]registry.Node
	Schemas map[string]registry.NodeSchema
	Env     string
}

// pipelineLoras checks a pipeline batch's LoRA values at Submit as each step's run will (checkLoras, as the caller,
// per env): the first bad one ("row 1: step_1: lora.rows[1].lora: LoRA <id> not found"). Each run checks again when
// it is made.
func (s *Service) pipelineLoras(ctx context.Context, tx pgx.Tx, ownerID string, graphs []stepGraph) (string, error) {
	byEnv := map[string][]loraAt{}
	for _, g := range graphs {
		byEnv[g.Env] = append(byEnv[g.Env], eachLora(g.Nodes, g.Schemas)...)
	}
	usable := map[string]map[string]lora.Info{}
	for env, values := range byEnv {
		found, err := lora.Usable(ctx, tx, s.registry, ownerID, env, loraIDs(values))
		if err != nil {
			return "", err
		}
		usable[env] = found
	}
	for _, g := range graphs {
		if _, detail := checkLoras(eachLora(g.Nodes, g.Schemas), g.Nodes, s.registry.LoraBlocks(), usable[g.Env]); detail != "" {
			return g.Where + ": " + detail, nil
		}
	}
	return "", nil
}

// pipelineMentions checks a pipeline batch's mentions at Submit, unknown handles only (the rest is checked when each
// run is made), in every step's resolved graph per input row, as the run will resolve it ("row 3: step_1: prompt.text:
// unknown element @wolf"). The elements they mention are the batch's copy.
func (s *Service) pipelineMentions(ctx context.Context, tx pgx.Tx, ownerID string, graphs []stepGraph) ([]element, string, error) {
	var handles []string
	for _, g := range graphs {
		for _, handle := range mentionedHandles(g.Nodes, g.Schemas) {
			if !slices.Contains(handles, handle) {
				handles = append(handles, handle)
			}
		}
	}
	lib, err := readElements(ctx, tx, ownerID, handles, nil)
	if err != nil {
		return nil, "", err
	}
	var failed []string
	for _, g := range graphs {
		for _, p := range prompts(g.Nodes, g.Schemas) {
			for _, handle := range mentioned(p.Parts) {
				if !slices.ContainsFunc(lib, func(e element) bool { return e.Handle == handle }) {
					failed = append(failed, fmt.Sprintf("%s: %s.%s: unknown element @%s", g.Where, p.Node, p.Param.Name, handle))
				}
			}
		}
	}
	return lib, joinRows(failed), nil
}

// inputParams are the param schemas the run rules check a pipeline input's value with.
var inputParams = map[string]registry.Param{"Text": {Type: "text"}, "Int": {Type: "int"}, "Float": {Type: "float"},
	"Seed": {Type: "seed"}, "Image": {Type: "image"}}

// pipelineRows builds a pipeline batch's rows (columns `<inputs node>.<name>`): each input row over the base inputs,
// repeated per seeds over the one Seed input, every input given (a Seed drawn when not) and of its type. The second
// result is the 422 detail, naming the input row.
func pipelineRows(p storedPipeline, body *dto.BatchCreate, randomStart func() int64) ([]map[string]any, string) {
	root := p.root()
	types := map[string]string{}
	var seedKeys []string
	for _, column := range p.Doc.Nodes[root].Columns {
		types[root+"."+column.Name] = column.Type
		if column.Type == "Seed" {
			seedKeys = append(seedKeys, root+"."+column.Name)
		}
	}
	repeat, seedKey, start := 1, "", int64(0)
	if body.Seeds != nil {
		if len(seedKeys) != 1 {
			return nil, fmt.Sprintf("seeds needs a pipeline with exactly one Seed input; %s has %d", p.Title, len(seedKeys))
		}
		repeat, seedKey = body.Seeds.Count, seedKeys[0]
		start = randomStart()
		if body.Seeds.Start != nil {
			start = *body.Seeds.Start
		}
		if start+int64(repeat)-1 > maxSafe {
			return nil, "seeds.start + seeds.count - 1 must be at most 9007199254740991"
		}
	}
	var values []map[string]any
	if lines := body.Rows.Lines; lines != nil {
		typ := types[lines.Param]
		if typ != "Text" && typ != "Image" {
			return nil, lines.Param + ": lines need a Text or Image input of this pipeline"
		}
		for _, line := range lines.Values {
			var cell any = line
			if typ == "Image" {
				cell = map[string]any{"take_id": line}
			}
			values = append(values, map[string]any{lines.Param: cell})
		}
	} else {
		var detail string
		if values, detail = inputRows(registry.Workflow{}, body.Rows, repeat); detail != "" {
			return nil, detail
		}
	}
	if n := len(values) * repeat; n < 1 || n > MaxRows {
		return nil, fmt.Sprintf("a batch has 1 to %d rows; this one has %d", MaxRows, n)
	}
	var rows []map[string]any
	for i, own := range values {
		for r := range repeat {
			inputs := maps.Clone(body.Inputs)
			if inputs == nil {
				inputs = map[string]any{}
			}
			maps.Copy(inputs, own)
			if seedKey != "" {
				inputs[seedKey] = float64(start + int64(r))
			}
			if detail := typedRow(types, inputs, randomStart); detail != "" {
				return nil, fmt.Sprintf("row %d: %s", i+1, detail)
			}
			rows = append(rows, inputs)
		}
	}
	return rows, ""
}

// inputTypes is the pipeline's columns (`<inputs node>.<name>`) → their types.
func (p storedPipeline) inputTypes() map[string]string {
	root, out := p.root(), map[string]string{}
	for _, column := range p.Doc.Nodes[root].Columns {
		out[root+"."+column.Name] = column.Type
	}
	return out
}

// typedRow checks one row's values in place by type (an Image as {take_id}, whole numbers as JSON decodes them), every
// input given, a Seed drawn when missing; the detail names the input.
func typedRow(types map[string]string, inputs map[string]any, randomStart func() int64) string {
	for _, key := range slices.Sorted(maps.Keys(inputs)) {
		typ, known := types[key]
		if !known {
			return key + ": not an input of this pipeline"
		}
		value, detail := check(inputParams[typ], inputs[key])
		if whole, ok := value.(int64); ok {
			value = float64(whole) // as JSON decodes it, like every other row value
		}
		if detail == "" && value == nil { // a seed's (or an image's) null: drawn below, or required
			delete(inputs, key)
			continue
		}
		if detail == "" && typ == "Seed" && value.(float64) < 0 {
			detail = "must be a seed from 0"
		}
		if detail != "" {
			return join(key, detail)
		}
		inputs[key] = value
	}
	for _, key := range slices.Sorted(maps.Keys(types)) {
		if _, given := inputs[key]; given {
			continue
		}
		if types[key] != "Seed" {
			return key + ": a value is required"
		}
		inputs[key] = float64(randomStart())
	}
	return ""
}

// ------------------------------------------------------------------ studio.runs.create for a pipeline step

// pipelineKey is a pipeline batch's idempotency key, parsed: <batch>:<row>:<attempt>:<node>:<visit>:<branch>, the
// branch a seed index (`<i>`), a fanned-out step's `<item>-<i>` (API 1.15), or an agent turn's `t<turn>` and its
// re-ask's `t<turn>r` (API 1.15; then Index is -1).
type pipelineKey struct {
	Attempt, Visit, Index int
	Step                  string
	Item                  int  // a fanned-out step's branch, -1 otherwise
	Turn                  int  // an agent's turn from 0, -1 otherwise
	ReAsk                 bool // an agent turn's re-ask
}

// parsePipelineKey reads a key runsCreateKey already matched; ok false when the branch is none of those forms.
func parsePipelineKey(key string) (pipelineKey, bool) {
	parts := strings.Split(key, ":")
	attempt, _ := strconv.Atoi(parts[2])
	visit, _ := strconv.Atoi(parts[4])
	out := pipelineKey{Attempt: attempt, Step: parts[3], Visit: visit, Index: -1, Item: -1, Turn: -1}
	number := func(text string) (int, bool) {
		n, err := strconv.Atoi(text)
		return n, err == nil && n >= 0 && strconv.Itoa(n) == text
	}
	branch := parts[5]
	if turn, ok := strings.CutPrefix(branch, "t"); ok {
		turn, out.ReAsk = strings.CutSuffix(turn, "r")
		n, ok := number(turn)
		out.Turn = n
		return out, ok
	}
	if item, index, fanned := strings.Cut(branch, "-"); fanned {
		n, ok1 := number(item)
		i, ok2 := number(index)
		out.Item, out.Index = n, i
		return out, ok1 && ok2
	}
	n, ok := number(branch)
	out.Index = n
	return out, ok
}

// pipelineStepRun picks what a pipeline batch's run is made from: the step the key names must be one of the
// snapshot's, its workflow the request's, the attempt the row's current one and the seed index below its seeds (a
// fanned-out step's item below its list max). The inputs are the request's (pipelines fills the binds from earlier
// steps), resolved against that workflow's snapshot.
//
// A judge, write or decide node's ask run (API 1.14): its workflow must be the node's resolved ask workflow, the branch
// 0 or 1 (the re-ask), the visit within the LLM-call bound. An agent's turn (API 1.15): branch t<turn>[r], the turn
// below its max_turns; row 0 is the director's chat (attempt 1, loop = the wake, below max_wakes). A local server's
// run must name the node's provider and model (ask.provider, ask.model); a remote model has no run.
func pipelineStepRun(p storedPipeline, req *message.RunsCreate, attempt int) (registry.Workflow, string) {
	key, ok := parsePipelineKey(req.IdempotencyKey)
	node, known := p.Doc.Nodes[key.Step]
	if req.Row == 0 && (!known || !node.IsDirector()) {
		return registry.Workflow{}, "row 0 is the director's chat: idempotency_key must name the director"
	}
	if known && dto.HasModel(node.Kind) {
		caps := node.Caps.Filled()
		switch {
		case !ok || (node.Kind == "agent") != (key.Turn >= 0):
			return registry.Workflow{}, "idempotency_key's branch must be 0 or 1 (a judge, write or decide), or t<turn>[r] (an agent)"
		case node.Kind != "agent" && key.Index > 1:
			return registry.Workflow{}, "idempotency_key's branch must be 0, or 1 for the re-ask"
		case node.Kind == "agent" && key.Turn >= caps.MaxTurns:
			return registry.Workflow{}, fmt.Sprintf("idempotency_key's turn must be below %s's %d turns", key.Step, caps.MaxTurns)
		case p.Models[key.Step] == "remote":
			return registry.Workflow{}, fmt.Sprintf("%s calls a remote model: pipelines calls it, never as a run", key.Step)
		case p.Models[key.Step] != req.WorkflowID:
			return registry.Workflow{}, fmt.Sprintf("workflow_id must be %s, the ask workflow of %s", p.Models[key.Step], key.Step)
		case key.Attempt != attempt:
			return registry.Workflow{}, fmt.Sprintf("idempotency_key must carry the row's attempt %d", attempt)
		case req.Row == 0 && key.Visit >= node.MaxWakesOr():
			return registry.Workflow{}, "idempotency_key's wake is beyond the director's max_wakes"
		case req.Row > 0 && key.Visit >= MaxPipelineCalls:
			return registry.Workflow{}, "idempotency_key's visit is beyond any cap"
		}
		if source := p.Sources[key.Step]; source["kind"] == "server" &&
			(req.Inputs["ask.provider"] != source["provider"] || req.Inputs["ask.model"] != source["model"]) {
			return registry.Workflow{}, fmt.Sprintf("ask.provider and ask.model must be %s and %s, %s's", source["provider"],
				source["model"], key.Step)
		}
		return p.Workflows[req.WorkflowID], ""
	}
	_, fanned := p.Fan[key.Step]
	switch {
	case !ok || !known || node.Kind != "step" || key.Turn >= 0 || key.Index >= node.SeedsOr1() || fanned != (key.Item >= 0) ||
		(fanned && key.Item >= p.Lists[p.Fan[key.Step]]):
		return registry.Workflow{}, "idempotency_key must name a step of this pipeline, a seed index below its seeds and, for a fan-out, an item below its max"
	case node.Workflow != req.WorkflowID:
		return registry.Workflow{}, fmt.Sprintf("workflow_id must be %s, the workflow of step %s", node.Workflow, key.Step)
	case key.Attempt != attempt:
		return registry.Workflow{}, fmt.Sprintf("idempotency_key must carry the row's attempt %d", attempt)
	case key.Visit > MaxPipelineRuns:
		return registry.Workflow{}, "idempotency_key's visit is beyond any cap"
	}
	return p.Workflows[node.Workflow], ""
}

// randomBase draws a base seed that leaves room for every visit's seeds.
func randomBase() int64 { return rand.Int64N(maxSafe - 8*MaxPipelineRuns) }
