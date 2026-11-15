package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
)

// Saved pipelines (contracts/README.md "Saved pipelines"): the caller's own pipeline documents in user_pipelines
// (row-level security on owner_id). The endpoints mirror /workflows; the 422 of a save is the same WorkflowError shape.

// MaxPipelines is how many pipelines one user may have.
const MaxPipelines = 100

// pipelineDoc is a stored pipeline's document.
func pipelineDoc(row model.UserPipeline) dto.PipelineDoc {
	doc := dto.PipelineDoc{Title: row.Title}
	_ = json.Unmarshal(row.Nodes, &doc.Nodes)
	_ = json.Unmarshal(row.Edges, &doc.Edges)
	return doc
}

// readPipeline reads one of the caller's pipelines in tx (lock: FOR UPDATE); another user's is errNotFound.
func readPipeline(ctx context.Context, tx pgx.Tx, id string, lock bool) (model.UserPipeline, error) {
	if !utility.IsUUID(id) {
		return model.UserPipeline{}, errNotFound
	}
	query := `SELECT ` + model.UserPipelineCols + ` FROM user_pipelines WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	return one[model.UserPipeline](ctx, tx, query, id)
}

// stepWorkflows returns every workflow the docs' steps name that the caller can run, by id: a template, or one of the
// caller's own as stored now (read in one query). Unknown ones are left out (unknown_workflow).
func (s *Service) stepWorkflows(ctx context.Context, tx pgx.Tx, docs ...dto.PipelineDoc) (map[string]dto.WorkflowDoc, error) {
	out, own := map[string]dto.WorkflowDoc{}, []string{}
	for _, doc := range docs {
		for _, node := range doc.Nodes {
			id := node.Workflow
			if node.Kind != "step" {
				continue
			}
			if utility.IsUUID(id) {
				own = append(own, id)
			} else if workflow, ok := s.registry.Workflow(id); ok {
				out[id] = templateDoc(workflow)
			}
		}
	}
	if len(own) == 0 {
		return out, nil
	}
	rows, err := collect[model.UserWorkflow](ctx, tx, `SELECT `+model.UserWorkflowCols+` FROM user_workflows
		WHERE id = ANY($1::uuid[])`, own)
	byID := map[string]dto.WorkflowDoc{}
	for _, row := range rows {
		byID[row.ID] = rowDoc(row)
	}
	for _, id := range own { // as the doc spells it
		if doc, ok := byID[strings.ToLower(id)]; ok {
			out[id] = doc
		}
	}
	return out, err
}

// pipelineProblemsNow checks doc as the caller (ownerID) in tx, against the live palette, and the caller's looks and
// LoRAs now.
func (s *Service) pipelineProblemsNow(ctx context.Context, tx pgx.Tx, ownerID string, doc dto.PipelineDoc) ([]dto.Problem, map[string]dto.WorkflowDoc, error) {
	workflows, err := s.stepWorkflows(ctx, tx, doc)
	if err != nil {
		return nil, nil, err
	}
	looks, err := readLooks(ctx, tx, s.registry, ownerID, stepLookIDs(doc), nil)
	if err != nil {
		return nil, nil, err
	}
	families := map[string]map[string]string{}
	for _, wf := range workflows {
		if _, done := families[wf.Env]; !done {
			if families[wf.Env], err = loraFamilies(ctx, tx, s.registry, ownerID, wf.Env, lookLoraIDs(looks)); err != nil {
				return nil, nil, err
			}
		}
	}
	return pipelineProblems(doc, workflows, s.palette(), looks, func(env string) map[string]string { return families[env] }),
		workflows, nil
}

// stepLookIDs are the distinct look ids the document's steps name.
func stepLookIDs(doc dto.PipelineDoc) []string {
	var out []string
	for _, node := range doc.Nodes {
		for _, id := range node.Looks {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// checkPipelineSave returns the document's problems, or the 422 when one blocks saving.
func (s *Service) checkPipelineSave(ctx context.Context, tx pgx.Tx, ownerID string, doc dto.PipelineDoc) ([]dto.Problem, error) {
	problems, _, err := s.pipelineProblemsNow(ctx, tx, ownerID, doc)
	if err != nil {
		return nil, err
	}
	if blocksSave(problems) {
		first := problems[slices.IndexFunc(problems, func(p dto.Problem) bool { return p.Blocks == "save" })]
		return nil, &WorkflowError{Detail: fmt.Sprintf("pipeline has %d problems: %s", len(problems), first.Message),
			Problems: problems}
	}
	return problems, nil
}

// pipelineDTO maps a stored pipeline with its step workflows' titles now and its newest batch; problems nil leaves
// them out.
func (s *Service) pipelineDTO(ctx context.Context, tx pgx.Tx, row model.UserPipeline, problems *[]dto.Problem) (dto.Pipeline, error) {
	out, err := s.pipelineDTOs(ctx, tx, []model.UserPipeline{row})
	if err != nil {
		return dto.Pipeline{}, err
	}
	out[0].Problems = problems
	return out[0], nil
}

// pipelineDTOs maps stored pipelines (without problems) in three reads: the rows given, every step workflow, and each
// pipeline's newest batch.
func (s *Service) pipelineDTOs(ctx context.Context, tx pgx.Tx, rows []model.UserPipeline) ([]dto.Pipeline, error) {
	docs, ids := make([]dto.PipelineDoc, len(rows)), make([]string, len(rows))
	for i, row := range rows {
		docs[i], ids[i] = pipelineDoc(row), row.ID
	}
	workflows, err := s.stepWorkflows(ctx, tx, docs...)
	if err != nil {
		return nil, err
	}
	lasts, err := collect[model.Batch](ctx, tx, model.BatchFrom+`WHERE b.id IN (SELECT DISTINCT ON (pipeline_id) id
		FROM batches WHERE pipeline_id = ANY($1::uuid[]) ORDER BY pipeline_id, created_at DESC, id DESC)`, ids)
	if err != nil {
		return nil, err
	}
	lastOf := map[string]dto.Batch{}
	for _, last := range lasts {
		lastOf[*last.PipelineID] = batchDTO(last)
	}
	out := make([]dto.Pipeline, len(rows))
	for i, row := range rows {
		out[i] = dto.Pipeline{PipelineDoc: docs[i], ID: row.ID, BasedOn: row.BasedOn, Version: row.Version,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, WorkflowTitles: map[string]string{}}
		for _, node := range docs[i].Nodes {
			if workflow, ok := workflows[node.Workflow]; ok && node.Kind == "step" {
				out[i].WorkflowTitles[node.Workflow] = workflow.Title
			}
		}
		if last, ok := lastOf[row.ID]; ok {
			out[i].LastBatch = &last
		}
	}
	return out, nil
}

// ListPipelines returns the caller's pipelines, newest first.
func (s *Service) ListPipelines(ctx context.Context, caller common.Caller) (dto.PipelineList, error) {
	out := dto.PipelineList{Pipelines: []dto.Pipeline{}}
	if err := s.ready(); err != nil {
		return out, err
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.UserPipeline](ctx, tx, `SELECT `+model.UserPipelineCols+` FROM user_pipelines
			ORDER BY updated_at DESC, id LIMIT $1`, MaxPipelines)
		if err != nil || len(rows) == 0 {
			return err
		}
		out.Pipelines, err = s.pipelineDTOs(ctx, tx, rows)
		return err
	})
	return out, err
}

// GetPipeline returns one of the caller's pipelines with its problems now.
func (s *Service) GetPipeline(ctx context.Context, caller common.Caller, id string) (dto.Pipeline, error) {
	if err := s.ready(); err != nil {
		return dto.Pipeline{}, err
	}
	var out dto.Pipeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readPipeline(ctx, tx, id, false)
		if err != nil {
			return err
		}
		problems, _, err := s.pipelineProblemsNow(ctx, tx, caller.ID, pipelineDoc(row))
		if err != nil {
			return err
		}
		out, err = s.pipelineDTO(ctx, tx, row, &problems)
		return err
	})
	return out, err
}

// CreatePipeline saves a new pipeline of the caller's: the document given, or a copy of based_on.
func (s *Service) CreatePipeline(ctx context.Context, caller common.Caller, body *dto.PipelineCreate) (dto.Pipeline, error) {
	if err := s.ready(); err != nil {
		return dto.Pipeline{}, err
	}
	var out dto.Pipeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		doc := body.PipelineDoc
		if body.BasedOn != nil { // a source the caller cannot read is a 404 before anything else
			row, err := readPipeline(ctx, tx, *body.BasedOn, false)
			if err != nil {
				return err
			}
			if body.IsCopy() {
				source := pipelineDoc(row)
				doc, doc.Title = source, cmp.Or(body.Title, cutRunes(source.Title+" copy", 80))
			}
		}
		var err error
		out, err = s.insertPipeline(ctx, tx, caller, doc, body.BasedOn)
		return err
	})
	return out, err
}

// insertPipeline stores doc as a new pipeline of the caller's after the cap and the save-blocking rules.
func (s *Service) insertPipeline(ctx context.Context, tx pgx.Tx, caller common.Caller, doc dto.PipelineDoc, basedOn *string) (dto.Pipeline, error) {
	doc.Title = strings.TrimSpace(doc.Title)
	// one create at a time per user, so concurrent creates can't all pass the cap
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('pipelines:' || $1::text))`, caller.ID); err != nil {
		return dto.Pipeline{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_pipelines`).Scan(&count); err != nil {
		return dto.Pipeline{}, err
	} else if count >= MaxPipelines {
		return dto.Pipeline{}, network.NewConflictError(fmt.Sprintf("You have %d pipelines; delete some first.", MaxPipelines), nil)
	}
	problems, err := s.checkPipelineSave(ctx, tx, caller.ID, doc)
	if err != nil {
		return dto.Pipeline{}, err
	}
	row, err := one[model.UserPipeline](ctx, tx, `INSERT INTO user_pipelines (id, owner_id, title, nodes, edges, based_on)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+model.UserPipelineCols,
		utility.NewID(), caller.ID, doc.Title, doc.Nodes, doc.Edges, basedOn)
	if err != nil {
		return dto.Pipeline{}, err
	}
	return s.pipelineDTO(ctx, tx, row, &problems)
}

// ReplacePipeline saves an edited document over one of the caller's pipelines when body.Version is the stored one.
func (s *Service) ReplacePipeline(ctx context.Context, caller common.Caller, id string, body *dto.PipelineUpdate) (dto.Pipeline, error) {
	if err := s.ready(); err != nil {
		return dto.Pipeline{}, err
	}
	var out dto.Pipeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readPipeline(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.Version != body.Version {
			return network.NewConflictError(fmt.Sprintf("pipeline changed since you opened it (now version %d)", row.Version), nil)
		}
		doc := body.PipelineDoc
		doc.Title = strings.TrimSpace(doc.Title)
		problems, err := s.checkPipelineSave(ctx, tx, caller.ID, doc)
		if err != nil {
			return err
		}
		row, err = one[model.UserPipeline](ctx, tx, `UPDATE user_pipelines SET title = $2, nodes = $3, edges = $4,
			version = version + 1, updated_at = clock_timestamp() WHERE id = $1 RETURNING `+model.UserPipelineCols,
			id, doc.Title, doc.Nodes, doc.Edges)
		if err != nil {
			return err
		}
		out, err = s.pipelineDTO(ctx, tx, row, &problems)
		return err
	})
	return out, err
}

// RenamePipeline sets the title of one of the caller's pipelines (version + 1).
func (s *Service) RenamePipeline(ctx context.Context, caller common.Caller, id string, body *dto.PipelinePatch) (dto.Pipeline, error) {
	if err := s.ready(); err != nil {
		return dto.Pipeline{}, err
	}
	var out dto.Pipeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if !utility.IsUUID(id) {
			return errNotFound
		}
		row, err := one[model.UserPipeline](ctx, tx, `UPDATE user_pipelines SET title = $2, version = version + 1,
			updated_at = clock_timestamp() WHERE id = $1 RETURNING `+model.UserPipelineCols, id, strings.TrimSpace(body.Title))
		if err != nil {
			return err
		}
		out, err = s.pipelineDTO(ctx, tx, row, nil)
		return err
	})
	return out, err
}

// DeletePipeline deletes one of the caller's pipelines: 409 while a batch of it is unfinished. Batches and takes keep
// their snapshots.
func (s *Service) DeletePipeline(ctx context.Context, caller common.Caller, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readPipeline(ctx, tx, id, true); err != nil {
			return err
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM batches WHERE pipeline_id = $1
			AND status IN ('queued', 'running', 'paused'))`, id).Scan(&open); err != nil {
			return err
		} else if open {
			return network.NewConflictError("a batch of this pipeline is unfinished; stop it or wait", nil)
		}
		_, err := tx.Exec(ctx, `DELETE FROM user_pipelines WHERE id = $1`, id)
		return err
	})
}

// ValidatePipeline checks a document with the pipeline rules as the caller and saves nothing.
func (s *Service) ValidatePipeline(ctx context.Context, caller common.Caller, doc dto.PipelineDoc) (dto.WorkflowValidation, error) {
	if err := s.ready(); err != nil {
		return dto.WorkflowValidation{}, err
	}
	var out dto.WorkflowValidation
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		problems, _, err := s.pipelineProblemsNow(ctx, tx, caller.ID, doc)
		out = dto.WorkflowValidation{Problems: problems, CanSave: !blocksSave(problems), CanRun: len(problems) == 0}
		return err
	})
	return out, err
}
