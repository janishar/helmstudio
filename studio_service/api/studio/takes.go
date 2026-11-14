package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// madeWith snapshots what made an output of run (Take.made_with): the workflow, every param of every node of the graph
// the run was sent with (resolved again from the registry for a run stored before it kept its graph) with seed in place
// of a random seed, the prompt node's text and the <family>/model node. With no graph it keeps the inputs as requested.
func madeWith(workflows *registry.Registry, run model.Run, seed *int64) gallerydto.MadeWith {
	inputs := map[string]any{}
	_ = json.Unmarshal(run.Inputs, &inputs)
	out := gallerydto.MadeWith{WorkflowID: run.WorkflowID, WorkflowTitle: run.WorkflowID, Inputs: inputs, Seed: seed}
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
	nodes := map[string]registry.Node{}
	_ = json.Unmarshal(run.Nodes, &nodes)
	if len(nodes) == 0 && found {
		nodes, _ = resolve(workflow, inputs) // nil when the workflow changed since
	}
	if len(nodes) == 0 {
		return out
	}
	out.Inputs, out.Prompt = map[string]any{}, nil
	for id, node := range nodes {
		for name, value := range node.Params {
			if seed != nil && isSeed(workflow, found, node.Kind, name) && randomSeed(value) {
				value = *seed
			}
			out.Inputs[id+"."+name] = value
		}
		switch {
		case node.Kind == "prompt":
			if text, ok := node.Params["text"].(string); ok {
				out.Prompt = &text
			}
		case strings.HasSuffix(node.Kind, "/model"):
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

// isSeed reports whether param name of a node of kind is a seed: its schema says so, or, without the workflow, it is
// named seed.
func isSeed(workflow registry.Workflow, found bool, kind, name string) bool {
	if !found {
		return name == "seed"
	}
	params := workflow.NodeSchemas[kind].Params
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
			err := row.Scan(&o.RunID, &o.Index, &o.Key, &o.Kind, &o.Width, &o.Height, &o.Seed, &o.TakeID, &o.Bytes)
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
			takeID, err := gallery.Record(ctx, tx, gallery.Output{OwnerID: run.OwnerID, SessionID: run.SessionID, RunID: run.ID,
				Index: o.Index, Key: o.Key, Kind: o.Kind, Width: o.Width, Height: o.Height, Bytes: o.Bytes,
				MadeWith: madeWith(p.Workflows, run, o.Seed), CreatedAt: cmp.Or(run.FinishedAt, &run.CreatedAt)})
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
