package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// "Save as pipeline" (contracts/README.md "Save as pipeline"): the chain of runs that made a take becomes a pipeline,
// oldest run first: inputs → step_1 … step_n → output, each step binding the take of the step before it.

// MaxSavedSteps is the longest lineage Save as pipeline turns into steps.
const MaxSavedSteps = 8

// lineageTake is one take on the chain: its made_with and its parents that are not deleted, in input order.
type lineageTake struct {
	ID       string
	Title    string
	MadeWith json.RawMessage
	Parents  []string // not deleted
	RunTakes []string // the parents with a made_with
}

// readLineageTake reads one of the caller's takes; another user's, deleted or pending is errNotFound.
func readLineageTake(ctx context.Context, tx pgx.Tx, id string) (lineageTake, error) {
	if !utility.IsUUID(id) {
		return lineageTake{}, errNotFound
	}
	return one[lineageTake](ctx, tx, `SELECT t.id::text, t.title, t.made_with,
		coalesce((SELECT array_agg(p.id::text ORDER BY e.idx, p.id) FROM take_parents e JOIN takes p ON p.id = e.parent_id
			AND p.deleted_at IS NULL AND p.ready WHERE e.take_id = t.id), '{}'),
		coalesce((SELECT array_agg(p.id::text ORDER BY e.idx, p.id) FROM take_parents e JOIN takes p ON p.id = e.parent_id
			AND p.deleted_at IS NULL AND p.ready AND p.made_with IS NOT NULL WHERE e.take_id = t.id), '{}')
		FROM takes t WHERE t.id = $1 AND t.deleted_at IS NULL AND t.ready`, id)
}

// imageValues lists the take ids an input value holds: an image's {take_id}, or a rows list's Image fields.
func imageValues(value any) []string {
	if ref, ok := value.(map[string]any); ok {
		if id, ok := ref["take_id"].(string); ok && len(ref) == 1 {
			return []string{id}
		}
		return nil
	}
	var out []string
	items, _ := value.([]any)
	for _, item := range items {
		fields, _ := item.(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			out = append(out, imageValues(fields[name])...)
		}
	}
	return out
}

// SaveTakeAsPipeline saves a new pipeline of the caller's from the take's lineage.
func (s *Service) SaveTakeAsPipeline(ctx context.Context, caller common.Caller, takeID string, body *dto.TakePipeline) (dto.Pipeline, error) {
	if err := s.ready(); err != nil {
		return dto.Pipeline{}, err
	}
	unprocessable := func(format string, args ...any) error {
		return network.NewUnprocessableError(fmt.Sprintf(format, args...), nil)
	}
	var out dto.Pipeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		take, err := readLineageTake(ctx, tx, takeID)
		if err != nil {
			return err
		}
		if take.MadeWith == nil {
			return unprocessable("an upload has no lineage to save")
		}
		chain := []lineageTake{take}
		for at := take; ; {
			if len(at.RunTakes) > 1 {
				return unprocessable("take %s was made from several runs' takes; a pipeline is a chain", at.ID)
			}
			if len(at.RunTakes) == 0 {
				break
			}
			if len(chain) == MaxSavedSteps {
				return unprocessable("the lineage is longer than %d steps", MaxSavedSteps)
			}
			if at, err = readLineageTake(ctx, tx, at.RunTakes[0]); err != nil {
				return err
			}
			chain = append([]lineageTake{at}, chain...)
		}
		palette := s.palette()
		doc := dto.PipelineDoc{Nodes: map[string]dto.PipelineNode{}, Title: cutRunes(take.Title+" pipeline", 80)}
		if body.Title != nil {
			doc.Title = strings.TrimSpace(*body.Title)
		}
		columns := []dto.PipelineInput{} // prompt (when step_1 binds it), then each upload
		uploads := map[string]string{}   // upload take → its input name
		previous := "inputs"
		doc.Edges = []dto.PipelineEdge{}
		for k, link := range chain {
			var made gallerydto.MadeWith
			if err := json.Unmarshal(link.MadeWith, &made); err != nil {
				return err
			}
			if made.Inputs != nil {
				maps.Copy(made.Inputs, made.Typed) // the typed view: element and look additions come back through mentions and looks
			}
			var workflow dto.WorkflowDoc
			if utility.IsUUID(made.WorkflowID) {
				row, err := readWorkflow(ctx, tx, made.WorkflowID, false)
				if errors.Is(err, errNotFound) {
					return unprocessable("workflow %s no longer exists", made.WorkflowID)
				} else if err != nil {
					return err
				}
				workflow = rowDoc(row)
			} else if registered, ok := s.registry.Workflow(made.WorkflowID); ok {
				workflow = templateDoc(registered)
			} else {
				return unprocessable("workflow %s no longer exists", made.WorkflowID)
			}
			params := settable(workflow, palette)
			step := fmt.Sprintf("step_%d", k+1)
			node := dto.PipelineNode{Kind: "step", Workflow: made.WorkflowID, Fixed: map[string]any{}, Bind: map[string]string{}}
			for _, l := range made.Looks { // the looks come back through the step, as the take was made
				node.Looks = append(node.Looks, l.ID)
			}
			if _, ok := params["prompt.text"]; ok && k == 0 {
				node.Bind["prompt.text"] = "$prompt"
				columns = append(columns, dto.PipelineInput{Name: "prompt", Type: "Text"})
			}
			seed := seedParam(workflow, palette)
			for _, key := range slices.Sorted(maps.Keys(made.Inputs)) {
				value := made.Inputs[key]
				ids := imageValues(value)
				if len(ids) == 0 {
					if _, bound := node.Bind[key]; key != seed && !bound {
						node.Fixed[key] = value
					}
					continue
				}
				for _, id := range ids {
					if !slices.Contains(link.Parents, id) {
						return unprocessable("take %s in the lineage was deleted", id)
					}
				}
				if len(ids) > 1 {
					return unprocessable("take %s mixes several sources in %s", link.ID, key)
				}
				switch id := ids[0]; {
				case k > 0 && id == chain[k-1].ID:
					node.Bind[key] = fmt.Sprintf("step_%d.takes", k)
				case uploads[id] != "":
					node.Bind[key] = "$" + uploads[id]
				default:
					name := "image"
					if len(uploads) > 0 {
						name = fmt.Sprintf("image_%d", len(uploads)+1)
					}
					uploads[id] = name
					columns = append(columns, dto.PipelineInput{Name: name, Type: "Image"})
					node.Bind[key] = "$" + name
				}
			}
			if len(node.Fixed) == 0 {
				node.Fixed = nil
			}
			doc.Nodes[step] = node
			doc.Edges = append(doc.Edges, dto.PipelineEdge{From: previous, To: step})
			previous = step
		}
		doc.Nodes["inputs"] = dto.PipelineNode{Kind: "inputs", Columns: columns}
		doc.Nodes["output"] = dto.PipelineNode{Kind: "output", From: previous + ".takes", Collection: doc.Title}
		doc.Edges = append(doc.Edges, dto.PipelineEdge{From: previous, To: "output"})
		if detail := doc.Validate(); detail != "" {
			return unprocessable("%s", detail)
		}
		out, err = s.insertPipeline(ctx, tx, caller, doc, nil)
		return err
	})
	return out, err
}
