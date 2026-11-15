package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// AgainBatch is "Run again" (contracts/README.md, Decisions; API 1.14): a new batch with an ended batch's snapshot,
// session, rows (inputs and seeds), elements and looks copies. decisions same sends replay_from (pipelines reuses the
// source's stored decisions whose replay keys match); again asks every model again; a batch of one workflow ignores it.
func (s *Service) AgainBatch(ctx context.Context, caller common.Caller, batchID string, body *dto.BatchAgain) (dto.Batch, error) {
	if !utility.IsUUID(batchID) {
		return dto.Batch{}, errNotFound
	}
	if !s.registry.Ready() {
		return dto.Batch{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.Batch
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		source, err := getBatch(ctx, tx, batchID, true)
		if err != nil {
			return err
		}
		if !ended(source.Status) {
			return network.NewConflictError(fmt.Sprintf("batch %s has not ended", batchID), nil)
		}
		if err := openBatchCap(ctx, tx, caller.ID); err != nil {
			return err
		}
		var first registry.Workflow
		var raw json.RawMessage
		var inputs map[string]any
		if err := tx.QueryRow(ctx, `SELECT snapshot, inputs FROM batches WHERE id = $1`, batchID).Scan(&raw, &inputs); err != nil {
			return err
		}
		_ = json.Unmarshal(raw, &first)
		stored, isPipeline, err := readStoredPipeline(ctx, tx, batchID)
		if err != nil {
			return err
		}
		if err := s.againHosts(first, stored, isPipeline); err != nil {
			return err
		}
		sessionID := ""
		if source.SessionID != nil { // its session, unless deleted since
			if tag, err := tx.Exec(ctx, `UPDATE sessions SET updated_at = clock_timestamp() WHERE id = $1`, *source.SessionID); err != nil {
				return err
			} else if tag.RowsAffected() == 1 {
				sessionID = *source.SessionID
			}
		}
		title := cutRunes(source.Title+" again", 200)
		if body.Title != nil {
			title = strings.TrimSpace(*body.Title)
		}
		if sessionID == "" {
			sessionID = utility.NewID()
			if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)`, sessionID, caller.ID, title); err != nil {
				return err
			}
		}
		same := body.Decisions == "same" && isPipeline
		newID := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO batches (id, owner_id, session_id, title, workflow_id, workflow_title, snapshot,
			inputs, workflow_version, pipeline_id, pipeline_title, pipeline_version, collection, pipeline, elements, looks, bounds,
			models, again_of, same_decisions)
			SELECT $2, owner_id, $3, $4, workflow_id, workflow_title, snapshot, inputs, workflow_version, pipeline_id,
			pipeline_title, pipeline_version, collection, pipeline, elements, looks, bounds, models, id, $5
			FROM batches WHERE id = $1`, batchID, newID, sessionID, title, same); err != nil {
			return err
		}
		rows, err := collect[message.SubmitRow](ctx, tx, `INSERT INTO batch_rows (batch_id, n, owner_id, inputs, seeds, looks, cells)
			SELECT $2, n, owner_id, inputs, seeds, looks, CASE WHEN cells IS NULL THEN NULL ELSE '[]'::jsonb END
			FROM batch_rows WHERE batch_id = $1 RETURNING n, inputs, coalesce(seeds, '{}')`, batchID, newID)
		if err != nil {
			return err
		}
		if err := appendBatchEvent(ctx, tx, newID, "batch", 0); err != nil {
			return err
		}
		submit := message.Submit{BatchID: newID, SessionID: sessionID, Inputs: inputs, Rows: rows,
			Snapshot: message.Snapshot{WorkflowID: first.ID, Workflow: snapshotWorkflow(first)}}
		if isPipeline {
			submit.Snapshot.Pipeline = stored.snapshot()
		}
		if same {
			submit.ReplayFrom = &batchID
		}
		if err := messaging.Enqueue(ctx, tx, messaging.Command{JobID: newID, OwnerID: caller.ID, Attempt: 1, Data: submit}); err != nil {
			return err
		}
		out, err = getBatch(ctx, tx, newID, false)
		return err
	})
	return out, err
}

// againHosts checks a live host for every workflow a batch run again will run, as Submit does (422 otherwise).
func (s *Service) againHosts(first registry.Workflow, stored storedPipeline, isPipeline bool) error {
	workflows := map[string]registry.Workflow{first.ID: first}
	if isPipeline {
		workflows = map[string]registry.Workflow{}
		for _, step := range stored.steps() {
			id := stored.Doc.Nodes[step].Workflow
			workflows[id] = stored.Workflows[id]
		}
	}
	for id, workflow := range workflows {
		graph, _ := json.Marshal(workflow.Nodes)
		probe := model.Run{WorkflowID: id, Env: workflow.Env, Nodes: graph, WorkflowVersion: stored.version(id)}
		if !isPipeline {
			probe.WorkflowVersion = nil
			if utility.IsUUID(id) {
				probe.WorkflowVersion = new(int) // a user workflow: its kinds must be published
			}
		}
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" {
			return network.NewUnprocessableError(reason, nil)
		}
	}
	if isPipeline {
		return s.askHosts(stored)
	}
	return nil
}
