package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
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
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		out, err = s.createRun(ctx, tx, caller, sessionID, body)
		return err
	})
	if err == nil {
		err = s.placeQueued(ctx, &out)
	}
	return out, err
}

// createRun is CreateRun inside the caller's transaction (New shot makes its run with the slot's revision).
func (s *Service) createRun(ctx context.Context, tx pgx.Tx, caller common.Caller, sessionID string, body *dto.RunRequest) (dto.Run, error) {
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	var out dto.Run
	err := func() error {
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
		workflow, probe, expansion, err := s.probeRun(ctx, tx, caller, body)
		if err != nil {
			return err
		}
		status, failure := "queued", (*string)(nil)
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" && probe.PinHost != nil {
			return network.NewUnprocessableError(reason, nil) // API 1.35: a run set to a host that cannot take it is never stored
		} else if reason != "" {
			status, failure = "failed", &reason
		}
		inputs, _ := json.Marshal(body.Inputs)
		run, err := one[model.Run](ctx, tx, `INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status, error,
			nodes, finished_at, workflow_version, workflow_title, mentions, loras, looks, media_at, pin_host) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
			CASE WHEN $7 = 'failed' THEN clock_timestamp() END, $10, $11, $12, $13, $14, $15, $16)
			RETURNING `+model.RunCols, utility.NewID(), caller.ID, sessionID, workflow.ID, workflow.Env, inputs, status, failure,
			probe.Nodes, probe.WorkflowVersion, workflow.Title, expansion, probe.Loras, looksJSON(body.Looks), probe.MediaAt, probe.PinHost)
		if err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, run, "status", time.Now(), statusData(status, failure, nil, run.Attempt)); err != nil {
			return err
		}
		out = s.runDTO(run, nil)
		return nil
	}()
	return out, err
}

// probeRun resolves a run request as a run would be made (its 422s) without storing it: the workflow, the run's
// host-check fields (graph, version, LoRAs, media sizes) and its mentions JSON.
func (s *Service) probeRun(ctx context.Context, tx pgx.Tx, caller common.Caller, body *dto.RunRequest) (registry.Workflow, model.Run, []byte, error) {
	workflow, version, err := s.runnable(ctx, tx, body.WorkflowID)
	if err == nil && !body.Ask {
		err = refuseText(workflow)
	}
	if err != nil {
		return workflow, model.Run{}, nil, err
	}
	graphs, details, _, err := s.runGraphs(ctx, tx, caller.ID, workflow, []map[string]any{body.Inputs},
		[][]string{body.Looks}, nil)
	if err != nil {
		return workflow, model.Run{}, nil, err
	} else if details[0] != "" {
		return workflow, model.Run{}, nil, network.NewUnprocessableError(details[0], nil)
	}
	nodes := graphs[0].Nodes
	if detail, err := checkImages(ctx, tx, s.store, caller.ID, nodes, workflow.NodeSchemas); err != nil {
		return workflow, model.Run{}, nil, err
	} else if detail != "" {
		return workflow, model.Run{}, nil, network.NewUnprocessableError(detail, nil)
	}
	graph, _ := json.Marshal(nodes)
	media, err := mediaAt(ctx, tx, caller.ID, nodes, workflow.NodeSchemas)
	if err != nil {
		return workflow, model.Run{}, nil, err
	}
	probe := model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph, WorkflowVersion: version,
		Loras: lorasJSON(graphs[0].Loras), MediaAt: media, PinHost: body.Host}
	return workflow, probe, mentionsJSON(graphs[0].Expansion), nil
}

// PlanRun is POST /runs/plan (API 1.25): the run CreateRun would make from body, not stored, and its planned peak
// on the first eligible host by name (contracts RunPlan).
func (s *Service) PlanRun(ctx context.Context, caller common.Caller, body *dto.RunRequest) (dto.RunPlan, error) {
	if !s.registry.Ready() {
		return dto.RunPlan{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	var out dto.RunPlan
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, probe, _, err := s.probeRun(ctx, tx, caller, body)
		if err != nil {
			return err
		}
		slots, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now())
		if reason != "" {
			out.Reason = &reason
			return nil
		}
		host := slices.Sorted(maps.Keys(slots))[0]
		out.Host = &host
		if p := planOf(s.registry, host, probe); p.peak >= 0 {
			gb := float64(p.peak) / 100
			out.PeakGB = &gb
		}
		return nil
	})
	return out, err
}

// looksJSON is runs.looks (batch_rows.looks): the ids as requested, null without any.
func looksJSON(ids []string) []byte {
	if len(ids) == 0 {
		return nil
	}
	data, _ := json.Marshal(ids)
	return data
}

// GetRun returns one run with its outputs.
func (s *Service) GetRun(ctx context.Context, caller common.Caller, runID string) (dto.Run, error) {
	if !utility.IsUUID(runID) {
		return dto.Run{}, errNotFound
	}
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 AND deleted_at IS NULL`, runID)
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

// ListRuns returns the session's newest 100 runs: never a deleted one or its hidden naming run (API 1.43).
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
			AND deleted_at IS NULL AND purpose IS NULL ORDER BY created_at DESC, id LIMIT 100`, sessionID)
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
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, runID)
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

// DeleteRun is DELETE /runs/{id} (API 1.43): a finished run leaves its session (soft; its takes stay). 409 while it is
// queued or running, or for a batch's run; 404 for an unknown, another user's, a deleted or a hidden naming run.
func (s *Service) DeleteRun(ctx context.Context, caller common.Caller, runID string) error {
	if !utility.IsUUID(runID) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 AND deleted_at IS NULL
			AND purpose IS NULL FOR UPDATE`, runID)
		switch {
		case err != nil:
			return err
		case !terminal[run.Status]:
			return network.NewConflictError("stop the run first", nil)
		case run.BatchID != nil:
			return network.NewConflictError("a batch's run goes with its batch", nil)
		}
		_, err = tx.Exec(ctx, `UPDATE runs SET deleted_at = clock_timestamp() WHERE id = $1`, runID)
		return err
	})
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

// appendEvent adds one event of run to its session's log under the session's next seq; a hidden naming run's events
// are never in it (API 1.43).
func appendEvent(ctx context.Context, tx pgx.Tx, run model.Run, typ string, ts time.Time, data any) error {
	if run.Purpose != nil {
		return nil
	}
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
		Status: run.Status, Error: run.Error, Outputs: []dto.RunOutput{}, Host: run.Host, LostHost: run.LostHost, Lane: run.Lane,
		Attempt: run.Attempt, CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		BatchID: run.BatchID, BatchRow: run.BatchRow, WorkflowVersion: run.WorkflowVersion, Purpose: run.Purpose}
	_ = json.Unmarshal(run.Inputs, &out.Inputs)
	_ = json.Unmarshal(run.Resolved, &out.Resolved) // null or absent leaves it nil (omitted)
	_ = json.Unmarshal(run.Looks, &out.Looks)
	var answer message.Answer
	if json.Unmarshal(run.Answer, &answer) == nil && len(answer.Tries) > 0 {
		out.Answer = &dto.RunAnswer{Text: answer.Tries[len(answer.Tries)-1].Text, TokensIn: answer.TokensIn,
			TokensOut: answer.TokensOut, Seconds: answer.Seconds}
	}
	if len(run.Progress) > 0 && string(run.Progress) != "null" {
		out.Progress = &dto.RunProgress{}
		_ = json.Unmarshal(run.Progress, out.Progress)
	}
	for _, output := range outputs {
		out.Outputs = append(out.Outputs, dto.RunOutput{Index: output.Index, Key: output.Key, Kind: output.Kind,
			URL: s.signer.Sign(output.Key), Width: output.Width, Height: output.Height, Seed: output.Seed, TakeID: output.TakeID,
			Video: gallerydto.Info(output.Video, s.signer.Sign), Audio: audioInfo(output.Audio), ThumbURL: s.signer.SignPtr(output.Thumb)})
	}
	return out
}

// audioInfo is a run_outputs.audio value, nil for null.
func audioInfo(raw []byte) *gallerydto.AudioInfo {
	var out *gallerydto.AudioInfo
	_ = json.Unmarshal(raw, &out)
	return out
}
