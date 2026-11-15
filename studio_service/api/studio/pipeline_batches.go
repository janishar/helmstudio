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
	Workflows  map[string]registry.Workflow `json:"workflows"`   // with node_schemas, as resolve needs them
	Versions   map[string]int               `json:"versions"`    // a user workflow's version at Submit
	Presets    map[string]map[string]any    `json:"presets"`     // step → its preset's values
	SeedParams map[string]string            `json:"seed_params"` // step → its workflow's one unwired seed param
}

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

// firstStep is the first step along the edges from the inputs node (a gate's pass), else the first by id.
func (p storedPipeline) firstStep() string {
	next := map[string]string{}
	for _, edge := range p.Doc.Edges {
		if _, taken := next[edge.From]; !taken {
			next[edge.From] = cmpOr(edge.To, func() string { return edge.Branch.Pass })
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

// cmpOr is value, or fallback() when value is empty.
func cmpOr(value string, fallback func() string) string {
	if value != "" {
		return value
	}
	return fallback()
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
		case "gate":
			fields["keep"] = node.KeepOr1()
		}
		out.Nodes[id] = fields
	}
	for id, workflow := range p.Workflows {
		out.Workflows[id] = snapshotWorkflow(workflow)
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
	problems, docs, err := s.pipelineProblemsNow(ctx, tx, caller.ID, doc)
	if err != nil {
		return dto.Batch{}, err
	} else if len(problems) > 0 {
		return dto.Batch{}, unprocessable("pipeline cannot run: %s", problems[0].Message)
	}
	stored := storedPipeline{ID: row.ID, Title: row.Title, Version: row.Version, Doc: doc,
		Workflows: map[string]registry.Workflow{}, Versions: map[string]int{}, Presets: map[string]map[string]any{},
		SeedParams: map[string]string{}}
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
	rows, detail := pipelineRows(stored, body, randomBase)
	if detail != "" {
		return dto.Batch{}, unprocessable("%s", detail)
	}
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
	bad, err := vetTakes(ctx, tx, s.store, caller.ID, all)
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
	if _, err := tx.Exec(ctx, `INSERT INTO batches (id, owner_id, session_id, title, workflow_id, workflow_title, snapshot,
		inputs, workflow_version, pipeline_id, pipeline_title, pipeline_version, collection, pipeline, elements, looks)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`, batchID, caller.ID, sessionID, title,
		first.ID, first.Title, first, base, stored.version(first.ID), row.ID, row.Title, row.Version, collection, stored,
		lib, looks); err != nil {
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
		if _, detail := checkLoras(eachLora(g.Nodes, g.Schemas), nodeKinds(g.Nodes), usable[g.Env]); detail != "" {
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
			for _, key := range slices.Sorted(maps.Keys(inputs)) {
				typ, known := types[key]
				if !known {
					return nil, fmt.Sprintf("row %d: %s: not an input of this pipeline", i+1, key)
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
					return nil, fmt.Sprintf("row %d: %s", i+1, join(key, detail))
				}
				inputs[key] = value
			}
			for _, key := range slices.Sorted(maps.Keys(types)) {
				if _, given := inputs[key]; given {
					continue
				}
				if types[key] != "Seed" {
					return nil, fmt.Sprintf("row %d: %s: a value is required", i+1, key)
				}
				inputs[key] = float64(randomStart())
			}
			rows = append(rows, inputs)
		}
	}
	return rows, ""
}

// ------------------------------------------------------------------ studio.runs.create for a pipeline step

// pipelineKey is a pipeline batch's idempotency key, parsed: <batch>:<row>:<attempt>:<step>:<visit>:<seed index>.
type pipelineKey struct {
	Attempt, Visit, Index int
	Step                  string
}

// parsePipelineKey reads a key runsCreateKey already matched; ok false when the branch is not a seed index.
func parsePipelineKey(key string) (pipelineKey, bool) {
	parts := strings.Split(key, ":")
	attempt, _ := strconv.Atoi(parts[2])
	visit, _ := strconv.Atoi(parts[4])
	index, err := strconv.Atoi(parts[5])
	return pipelineKey{Attempt: attempt, Step: parts[3], Visit: visit, Index: index}, err == nil && index >= 0
}

// pipelineStepRun picks what a pipeline batch's run is made from: the step the key names must be one of the
// snapshot's, its workflow the request's, the attempt the row's current one and the seed index below its seeds. The
// inputs are the request's (pipelines fills the binds from earlier steps), resolved against that workflow's snapshot.
func pipelineStepRun(p storedPipeline, req *message.RunsCreate, attempt int) (registry.Workflow, string) {
	key, ok := parsePipelineKey(req.IdempotencyKey)
	node, known := p.Doc.Nodes[key.Step]
	switch {
	case !ok || !known || node.Kind != "step" || key.Index >= node.SeedsOr1():
		return registry.Workflow{}, "idempotency_key must name a step of this pipeline and a seed index below its seeds"
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
