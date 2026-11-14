package studio

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
)

// terminal holds the statuses that end a run.
var terminal = map[string]bool{"succeeded": true, "failed": true, "cancelled": true}

// canMove reports whether a run may go from status from to status to: queued → running → one terminal status, or
// queued straight to a terminal one. Nothing follows a terminal status, and running is entered once.
func canMove(from, to string) bool {
	switch from {
	case "queued":
		return to == "running" || terminal[to]
	case "running":
		return terminal[to]
	}
	return false
}

// CreateRun resolves the workflow's graph with the inputs, places it on a live host of its env and, in one
// transaction, stores the run and queues cmd.run.execute through the outbox. With no live host the run is stored
// failed at once and nothing is sent.
func (s *Service) CreateRun(ctx context.Context, caller common.Caller, sessionID string, body *dto.RunRequest) (dto.Run, error) {
	if !utility.IsUUID(sessionID) {
		return dto.Run{}, errNotFound
	}
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		// The session first: another user's session is a 404 before any 422.
		if tag, err := tx.Exec(ctx, `UPDATE sessions SET updated_at = clock_timestamp() WHERE id = $1`, sessionID); err != nil {
			return err
		} else if tag.RowsAffected() == 0 {
			return errNotFound
		}
		workflow, ok := s.registry.Workflow(body.WorkflowID)
		if !ok {
			return network.NewUnprocessableError("unknown workflow "+body.WorkflowID, nil)
		}
		nodes, detail := resolve(workflow, body.Inputs)
		if detail != "" {
			return network.NewUnprocessableError(detail, nil)
		}
		runID := utility.NewID()
		host, placed := s.registry.Place(workflow.Env, time.Now())
		status, failure, hostValue := "queued", (*string)(nil), &host
		if !placed {
			reason := "no live host for env " + workflow.Env
			status, failure, hostValue = "failed", &reason, nil
		}
		inputs, _ := json.Marshal(body.Inputs)
		run, err := one[model.Run](ctx, tx, `INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status, error, host,
			finished_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CASE WHEN $7 = 'failed' THEN clock_timestamp() END)
			RETURNING `+model.RunCols, runID, caller.ID, sessionID, workflow.ID, workflow.Env, inputs, status, failure, hostValue)
		if err != nil {
			return err
		}
		if placed {
			err = messaging.Enqueue(ctx, tx, messaging.Command{JobID: runID, OwnerID: caller.ID, Attempt: 1, Data: message.Execute{
				RunID: runID, SessionID: sessionID, Attempt: 1, Workflow: message.GraphWorkflow{ID: workflow.ID, Nodes: nodes},
				OutputPrefix: common.OwnerPrefix(caller.ID) + "runs/" + runID + "/", Host: host, Env: workflow.Env,
			}})
			if err != nil {
				return err
			}
		}
		if err := appendEvent(ctx, tx, run, "status", time.Now(), statusData(status, failure, hostValue)); err != nil {
			return err
		}
		out = s.runDTO(run, nil)
		return nil
	})
	return out, err
}

// GetRun returns one run with its outputs.
func (s *Service) GetRun(ctx context.Context, caller common.Caller, runID string) (dto.Run, error) {
	if !utility.IsUUID(runID) {
		return dto.Run{}, errNotFound
	}
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1`, runID)
		if err != nil {
			return err
		}
		out, err = s.withOutputs(ctx, tx, run)
		return err
	})
	return out, err
}

// ListRuns returns the session's newest 100 runs.
func (s *Service) ListRuns(ctx context.Context, caller common.Caller, sessionID string) (dto.RunList, error) {
	if !utility.IsUUID(sessionID) {
		return dto.RunList{}, errNotFound
	}
	out := dto.RunList{Runs: []dto.Run{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := one[model.Session](ctx, tx, `SELECT `+model.SessionCols+` FROM sessions WHERE id = $1`, sessionID); err != nil {
			return err
		}
		runs, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE session_id = $1
			ORDER BY created_at DESC, id LIMIT 100`, sessionID)
		if err != nil {
			return err
		}
		ids := make([]string, len(runs))
		for i, run := range runs {
			ids[i] = run.ID
		}
		outputs, err := collect[model.Output](ctx, tx, `SELECT `+model.OutputCols+` FROM run_outputs
			WHERE run_id = ANY($1::uuid[]) ORDER BY idx`, ids)
		byRun := map[string][]model.Output{}
		for _, output := range outputs {
			byRun[output.RunID] = append(byRun[output.RunID], output)
		}
		for _, run := range runs {
			out.Runs = append(out.Runs, s.runDTO(run, byRun[run.ID]))
		}
		return err
	})
	return out, err
}

// CancelRun asks for the run to stop: 409 when it has ended. A run whose command is still in the outbox was never
// sent: the command is dropped and the run is cancelled at once. Otherwise the run is marked and its id written to
// the KV cancels before the mark commits, so a failed write (503) leaves no mark the runner never saw; the runner
// reports cancelled, or the lease sweep ends it.
func (s *Service) CancelRun(ctx context.Context, caller common.Caller, runID string) (dto.Run, error) {
	if !utility.IsUUID(runID) {
		return dto.Run{}, errNotFound
	}
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 FOR UPDATE`, runID)
		if err != nil {
			return err
		}
		if terminal[run.Status] {
			return network.NewConflictError("This run has already ended.", nil)
		}
		tag, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, runID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			run, err = finish(ctx, tx, run, "cancelled", nil)
		} else {
			run, err = one[model.Run](ctx, tx, `UPDATE runs SET cancel_requested_at = coalesce(cancel_requested_at, clock_timestamp())
				WHERE id = $1 RETURNING `+model.RunCols, runID)
			if err == nil {
				if err = s.cancel(ctx, runID); err != nil {
					log.Printf("cancel %s: %v", runID, err)
					return network.NewServiceUnavailableError("could not reach the runners; try again", err)
				}
			}
		}
		if err != nil {
			return err
		}
		out, err = s.withOutputs(ctx, tx, run)
		return err
	})
	return out, err
}

// finish moves an open run to a terminal status (studio decides it: cancelled before sending, or a lost lease) and
// appends the status event. It returns the run as it is now; a run already ended is left as it is.
func finish(ctx context.Context, tx pgx.Tx, run model.Run, status string, failure *string) (model.Run, error) {
	updated, err := one[model.Run](ctx, tx, `UPDATE runs SET status = $2, error = $3, finished_at = clock_timestamp(),
		updated_at = clock_timestamp() WHERE id = $1 AND status IN ('queued', 'running') RETURNING `+model.RunCols,
		run.ID, status, failure)
	if errors.Is(err, errNotFound) {
		return run, nil
	} else if err != nil {
		return run, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, run.ID); err != nil {
		return run, err
	}
	return updated, appendEvent(ctx, tx, updated, "status", time.Now(), statusData(status, failure, updated.Host))
}

// statusData is a stored status event's data.
func statusData(status string, failure, host *string) map[string]any {
	return map[string]any{"status": status, "error": failure, "host": host}
}

// appendEvent adds one event of run to its session's log under the session's next seq.
func appendEvent(ctx context.Context, tx pgx.Tx, run model.Run, typ string, ts time.Time, data any) error {
	_, err := tx.Exec(ctx, `WITH next AS (UPDATE sessions SET event_seq = event_seq + 1 WHERE id = $1 RETURNING event_seq)
		INSERT INTO run_events (session_id, seq, owner_id, run_id, type, ts, data)
		SELECT $1, event_seq, $2, $3, $4, $5, $6 FROM next`, run.SessionID, run.OwnerID, run.ID, typ, ts.UTC(), data)
	return err
}

// withOutputs maps run with its outputs read in tx.
func (s *Service) withOutputs(ctx context.Context, tx pgx.Tx, run model.Run) (dto.Run, error) {
	outputs, err := collect[model.Output](ctx, tx, `SELECT `+model.OutputCols+` FROM run_outputs WHERE run_id = $1 ORDER BY idx`, run.ID)
	return s.runDTO(run, outputs), err
}

// runDTO maps a run and its outputs, signing their keys.
func (s *Service) runDTO(run model.Run, outputs []model.Output) dto.Run {
	out := dto.Run{ID: run.ID, SessionID: run.SessionID, WorkflowID: run.WorkflowID, Inputs: map[string]any{},
		Status: run.Status, Error: run.Error, Outputs: []dto.RunOutput{}, Host: run.Host, CreatedAt: run.CreatedAt,
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt}
	_ = json.Unmarshal(run.Inputs, &out.Inputs)
	if len(run.Progress) > 0 && string(run.Progress) != "null" {
		out.Progress = &dto.RunProgress{}
		_ = json.Unmarshal(run.Progress, out.Progress)
	}
	for _, output := range outputs {
		out.Outputs = append(out.Outputs, dto.RunOutput{Index: output.Index, Key: output.Key, Kind: output.Kind,
			URL: s.signer.Sign(output.Key), Width: output.Width, Height: output.Height})
	}
	return out
}
