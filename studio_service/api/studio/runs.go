package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
)

// MaxOpenRuns is how many queued or running interactive runs one user may have: it bounds what one account adds to
// every line-up. Batch runs are bounded by MaxOpenBatches and the pipelines engine's WINDOW (rows of one batch asked and
// not ended) instead.
const MaxOpenRuns = 100

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

// CreateRun resolves the workflow's graph with the inputs and stores the run queued (lane interactive, attempt 1,
// no host) with that graph: the dispatcher releases it later. With no eligible host now the run is stored failed at
// once and nothing is sent. The answer shows where it is lined up.
func (s *Service) CreateRun(ctx context.Context, caller common.Caller, sessionID string, body *dto.RunRequest) (dto.Run, error) {
	if !utility.IsUUID(sessionID) {
		return dto.Run{}, errNotFound
	}
	if !s.registry.Ready() { // a view not loaded yet would read as an unknown workflow or no live host
		return dto.Run{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
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
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE owner_id = $1 AND lane = 'interactive'
			AND status IN ('queued', 'running')`,
			caller.ID).Scan(&open); err != nil {
			return err
		} else if open >= MaxOpenRuns {
			return network.NewConflictError(fmt.Sprintf("You have %d runs queued or running; wait for some to finish.", MaxOpenRuns), nil)
		}
		workflow, version, err := s.runnable(ctx, tx, body.WorkflowID)
		if err != nil {
			return err
		}
		graphs, details, _, err := s.runGraphs(ctx, tx, caller.ID, workflow, []map[string]any{body.Inputs}, nil)
		if err != nil {
			return err
		} else if details[0] != "" {
			return network.NewUnprocessableError(details[0], nil)
		}
		nodes := graphs[0].Nodes
		if detail, err := checkImages(ctx, tx, s.store, caller.ID, nodes); err != nil {
			return err
		} else if detail != "" {
			return network.NewUnprocessableError(detail, nil)
		}
		graph, _ := json.Marshal(nodes)
		status, failure := "queued", (*string)(nil)
		probe := model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph, WorkflowVersion: version}
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" {
			status, failure = "failed", &reason
		}
		inputs, _ := json.Marshal(body.Inputs)
		run, err := one[model.Run](ctx, tx, `INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status, error,
			nodes, finished_at, workflow_version, workflow_title, mentions) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
			CASE WHEN $7 = 'failed' THEN clock_timestamp() END, $10, $11, $12)
			RETURNING `+model.RunCols, utility.NewID(), caller.ID, sessionID, workflow.ID, workflow.Env, inputs, status, failure,
			graph, version, workflow.Title, mentionsJSON(graphs[0].Expansion))
		if err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, run, "status", time.Now(), statusData(status, failure, nil, run.Attempt)); err != nil {
			return err
		}
		out = s.runDTO(run, nil)
		return nil
	})
	if err == nil {
		err = s.placeQueued(ctx, &out)
	}
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
	if err == nil {
		err = s.placeQueued(ctx, &out)
	}
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
	if err == nil {
		pointers := make([]*dto.Run, len(out.Runs))
		for i := range out.Runs {
			pointers[i] = &out.Runs[i]
		}
		err = s.placeQueued(ctx, pointers...)
	}
	return out, err
}

// CancelRun asks for the run to stop: 409 when it has ended. A run the dispatcher has not released, or whose command
// is still in the outbox, was never sent: the command is dropped and the run is cancelled at once. Otherwise the run is
// marked and its id written to the KV cancels before the mark commits, so a failed write (503) leaves no mark the
// runner never saw; the runner reports cancelled, or a lost lease ends it cancelled. The row lock orders this with a
// dispatcher pass releasing the run.
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
		if run, err = s.cancelRun(ctx, tx, run); err != nil {
			return err
		}
		out, err = s.withOutputs(ctx, tx, run)
		return err
	})
	if err == nil {
		err = s.placeQueued(ctx, &out)
	}
	return out, err
}

// cancelRun stops an open run locked in tx: never sent (not released, or its command still in the outbox) is
// cancelled at once; otherwise it is marked and its id written to the KV cancels before the mark commits.
func (s *Service) cancelRun(ctx context.Context, tx pgx.Tx, run model.Run) (model.Run, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, run.ID)
	if err != nil {
		return run, err
	}
	if run.ReleasedAt == nil || tag.RowsAffected() > 0 {
		return finish(ctx, tx, run, "cancelled", nil)
	}
	run, err = one[model.Run](ctx, tx, `UPDATE runs SET cancel_requested_at = coalesce(cancel_requested_at, clock_timestamp())
		WHERE id = $1 RETURNING `+model.RunCols, run.ID)
	if err == nil {
		if err = s.cancel(ctx, run.ID); err != nil {
			log.Printf("cancel %s: %v", run.ID, err)
			return run, network.NewServiceUnavailableError("could not reach the runners; try again", err)
		}
	}
	return run, err
}

// finish moves an open run to a terminal status (studio decides it: cancelled before sending, no eligible host, or a
// lost lease) and
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
	if err := appendEvent(ctx, tx, updated, "status", time.Now(), statusData(status, failure, updated.Host, updated.Attempt)); err != nil {
		return run, err
	}
	return updated, runEnded(ctx, tx, updated)
}

// statusData is a stored status event's data, with the attempt it belongs to.
func statusData(status string, failure, host *string, attempt int) map[string]any {
	return map[string]any{"status": status, "error": failure, "host": host, "attempt": attempt}
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
		Status: run.Status, Error: run.Error, Outputs: []dto.RunOutput{}, Host: run.Host, Lane: run.Lane,
		Attempt: run.Attempt, CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		BatchID: run.BatchID, BatchRow: run.BatchRow, WorkflowVersion: run.WorkflowVersion}
	_ = json.Unmarshal(run.Inputs, &out.Inputs)
	_ = json.Unmarshal(run.Resolved, &out.Resolved) // null or absent leaves it nil (omitted)
	if len(run.Progress) > 0 && string(run.Progress) != "null" {
		out.Progress = &dto.RunProgress{}
		_ = json.Unmarshal(run.Progress, out.Progress)
	}
	for _, output := range outputs {
		out.Outputs = append(out.Outputs, dto.RunOutput{Index: output.Index, Key: output.Key, Kind: output.Kind,
			URL: s.signer.Sign(output.Key), Width: output.Width, Height: output.Height, Seed: output.Seed, TakeID: output.TakeID})
	}
	return out
}
