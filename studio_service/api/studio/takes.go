package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// madeWith snapshots what made an output of run (Take.made_with): the workflow (a user workflow's title and version as
// the run stored them), every settable param of every node of the graph the run was sent with (resolved again from
// the registry for a run stored before it kept its graph; a wired param is not settable) with seed in place of a
// random seed, the bypass switches the run asked for, the prompt node's text as typed and the <family>/model node, and
// what the mention and look expansion did (typed, elements, looks). A param the run set to "auto" holds the value the
// runner chose (resolved, "<node>.<param>" → value) with typed keeping "auto". With no graph it keeps the inputs as
// requested.
func madeWith(workflows *registry.Registry, run model.Run, seed *int64, resolved map[string]int64) gallerydto.MadeWith {
	inputs := map[string]any{}
	_ = json.Unmarshal(run.Inputs, &inputs)
	out := gallerydto.MadeWith{WorkflowID: run.WorkflowID, WorkflowTitle: run.WorkflowID, Inputs: inputs, Seed: seed,
		WorkflowVersion: run.WorkflowVersion, Host: run.Host}
	if text, ok := inputs["prompt.text"].(string); ok {
		out.Prompt = &text
	}
	var workflow registry.Workflow
	found := false
	if workflows != nil {
		workflow, found = workflows.Workflow(run.WorkflowID)
	}
	if found {
		out.WorkflowTitle = workflow.Title
	}
	if run.WorkflowTitle != nil {
		out.WorkflowTitle = *run.WorkflowTitle
	}
	schemas := workflow.NodeSchemas
	if !found && workflows != nil {
		schemas = workflows.Schemas()
	}
	nodes := map[string]registry.Node{}
	_ = json.Unmarshal(run.Nodes, &nodes)
	if len(nodes) == 0 && found {
		nodes, _ = resolve(workflow, inputs) // nil when the workflow changed since
	}
	if len(nodes) == 0 {
		return out
	}
	var exp expansion // the prompt as typed, the elements it mentioned
	_ = json.Unmarshal(run.Mentions, &exp)
	out.Typed, out.Elements, out.Looks = exp.Typed, exp.Elements, exp.Looks
	for _, u := range runUsed(run.Loras) {
		out.Loras = append(out.Loras, gallerydto.MadeWithLora{Node: u.Node, ID: u.ID, Source: u.Source, Name: u.Name,
			Strength: u.Strength, Mode: u.Mode, SHA256: u.SHA256})
	}
	out.Inputs, out.Prompt = map[string]any{}, nil
	for key, value := range inputs {
		if strings.HasSuffix(key, ".bypass") {
			out.Inputs[key] = value
		}
	}
	prompted := false
	for _, id := range slices.Sorted(maps.Keys(nodes)) { // the first prompt and model node by id: stable made_with
		node := nodes[id]
		for name, value := range node.Params {
			if _, wired := node.In[name]; wired {
				continue
			}
			if seed != nil && isSeed(schemas, node.Kind, name) && randomSeed(value) {
				value = *seed
			}
			if chosen, ok := resolved[id+"."+name]; ok && chosen >= 1 && value == "auto" {
				value = chosen
				out.Typed = setKey(out.Typed, id+"."+name, "auto")
			}
			out.Inputs[id+"."+name] = value
		}
		switch {
		case node.Kind == "prompt" && !prompted:
			prompted = true
			value, typed := exp.Typed[id+".text"]
			if !typed {
				value = node.Params["text"]
			}
			if text, ok := value.(string); ok {
				out.Prompt = &text
			}
		case strings.HasSuffix(node.Kind, "/model") && out.Model == nil:
			out.Model = &gallerydto.Model{Variant: stringParam(node.Params["variant"]), Precision: stringParam(node.Params["precision"])}
			if workflows != nil && run.Host != nil {
				if model, ok := workflows.Model(*run.Host+"."+run.Env, strings.TrimSuffix(node.Kind, "/model")); ok {
					out.Model.ID = &model.ID
				}
			}
		}
	}
	return out
}

// withPipeline adds made_with.pipeline to a pipeline batch run's take: the batch's pipeline as snapshot, and the step
// node and visit its idempotency key names.
func withPipeline(ctx context.Context, tx pgx.Tx, run model.Run, made gallerydto.MadeWith) (gallerydto.MadeWith, error) {
	if run.BatchID == nil || run.BatchRow == nil || run.Key == nil {
		return made, nil
	}
	key, ok := parsePipelineKey(*run.Key)
	ok = ok && key.Turn < 0
	var pipeline gallerydto.MadeWithPipeline
	// a sequence's Re-render (API 1.17) has no pipeline: its snapshot's id is the timeline's
	err := tx.QueryRow(ctx, `SELECT coalesce(pipeline_id, timeline_id)::text, pipeline_title, pipeline_version FROM batches
		WHERE id = $1 AND (pipeline_id IS NOT NULL OR timeline_id IS NOT NULL)`, *run.BatchID).Scan(&pipeline.ID, &pipeline.Title, &pipeline.Version)
	if errors.Is(err, pgx.ErrNoRows) || !ok {
		return made, nil
	} else if err != nil {
		return made, err
	}
	pipeline.Node, pipeline.Attempt, pipeline.Row = key.Step, key.Visit+1, *run.BatchRow
	made.Pipeline = &pipeline
	return made, nil
}

// isSeed reports whether param name of a node of kind is a seed: its schema says so, or, without a schema, it is
// named seed.
func isSeed(schemas map[string]registry.NodeSchema, kind, name string) bool {
	schema, ok := schemas[kind]
	if !ok {
		return name == "seed"
	}
	params := schema.Params
	index := slices.IndexFunc(params, func(param registry.Param) bool { return param.Name == name })
	return index >= 0 && params[index].Type == "seed"
}

// randomSeed reports whether a seed value asks the runner for a random one: null or -1.
func randomSeed(value any) bool {
	switch number := value.(type) {
	case nil:
		return true
	case int64:
		return number == -1
	case float64:
		return number == -1
	}
	return false
}

// stringParam is a string param, or nil.
func stringParam(value any) *string {
	if s, ok := value.(string); ok {
		return &s
	}
	return nil
}

// Backfill makes a take of every run output stored before the gallery (run_outputs.take_id is null), as System, and
// returns how many it made, each dated when its run finished (gallery order). Idempotent: an output that already has a take keeps it.
func (p *Projector) Backfill(ctx context.Context) (int, error) {
	made := 0
	err := common.System(ctx, p.pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT `+model.OutputCols+`, o.bytes FROM run_outputs o JOIN runs r ON r.id = o.run_id
			WHERE o.take_id IS NULL ORDER BY r.created_at, r.id, o.idx`)
		outputs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
			model.Output
			Bytes int64
		}, error) {
			var o struct {
				model.Output
				Bytes int64
			}
			err := row.Scan(&o.RunID, &o.Index, &o.Key, &o.Kind, &o.Width, &o.Height, &o.Seed, &o.TakeID, &o.Video, &o.Bytes)
			return o, err
		})
		if err != nil {
			return err
		}
		for _, o := range outputs {
			run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1`, o.RunID)
			if err != nil {
				return err
			}
			with, err := withPipeline(ctx, tx, run, madeWith(p.Workflows, run, o.Seed, resolvedParams(run.Resolved)))
			if err != nil {
				return err
			}
			takeID, err := gallery.Record(ctx, tx, gallery.Output{OwnerID: run.OwnerID, SessionID: run.SessionID, RunID: run.ID,
				Index: o.Index, Key: o.Key, Kind: o.Kind, Width: o.Width, Height: o.Height, Bytes: o.Bytes,
				MadeWith: with, CreatedAt: cmp.Or(run.FinishedAt, &run.CreatedAt),
				BatchID: run.BatchID, Parents: parents(typedView(run.Nodes, run.Mentions))})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE run_outputs SET take_id = $3 WHERE run_id = $1 AND idx = $2`, o.RunID, o.Index, takeID); err != nil {
				return err
			}
			made++
		}
		return nil
	})
	return made, err
}
