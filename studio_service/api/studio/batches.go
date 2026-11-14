package studio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

// Batches (contracts/README.md "Batches and the pipeline engine"): studio owns the API and the record; pipelines_service
// runs them. Submit and every control write the batch and its cmd.pipeline.* command in one transaction.

// CreateBatch checks the whole batch and stores it queued with its snapshot, its rows and cmd.pipeline.submit.
func (s *Service) CreateBatch(ctx context.Context, caller common.Caller, body *dto.BatchCreate) (dto.Batch, error) {
	if !s.registry.Ready() {
		return dto.Batch{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.Batch
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if body.SessionID != nil { // another user's session is a 404 before any 409 or 422
			if tag, err := tx.Exec(ctx, `UPDATE sessions SET updated_at = clock_timestamp() WHERE id = $1`, *body.SessionID); err != nil {
				return err
			} else if tag.RowsAffected() == 0 {
				return errNotFound
			}
		}
		// one Submit at a time per user, so concurrent Submits can't all count the same open batches
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('batches:' || $1::text))`, caller.ID); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM batches WHERE owner_id = $1 AND status IN ('queued', 'running', 'paused')`,
			caller.ID).Scan(&open); err != nil {
			return err
		} else if open >= MaxOpenBatches {
			return network.NewConflictError(fmt.Sprintf("You have %d unfinished batches; wait for one to finish.", MaxOpenBatches), nil)
		}
		workflow, ok := s.registry.Workflow(body.WorkflowID)
		if !ok {
			return network.NewUnprocessableError("unknown workflow "+body.WorkflowID, nil)
		}
		rows, detail := expand(workflow, body, randomSeedStart)
		if detail != "" {
			return network.NewUnprocessableError(detail, nil)
		}
		if detail, err := s.checkRowImages(ctx, tx, caller.ID, workflow, rows); err != nil {
			return err
		} else if detail != "" {
			return network.NewUnprocessableError(detail, nil)
		}
		nodes, _ := resolve(workflow, rows[0])
		graph, _ := json.Marshal(nodes)
		probe := model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph}
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason != "" {
			return network.NewUnprocessableError(reason, nil)
		}
		title := ""
		if body.Title != nil {
			title = strings.TrimSpace(*body.Title)
		}
		if title == "" {
			title = fmt.Sprintf("%s × %d", workflow.Title, len(rows))
		}
		sessionID := ""
		if body.SessionID != nil {
			sessionID = *body.SessionID
		} else {
			sessionID = utility.NewID()
			if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)`,
				sessionID, caller.ID, title); err != nil {
				return err
			}
		}
		base := body.Inputs
		if base == nil {
			base = map[string]any{}
		}
		batchID := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO batches (id, owner_id, session_id, title, workflow_id, workflow_title, snapshot,
			inputs) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, batchID, caller.ID, sessionID, title, workflow.ID, workflow.Title,
			workflow, base); err != nil {
			return err
		}
		numbers := make([]int, len(rows))
		submitRows := make([]message.SubmitRow, len(rows))
		for i, inputs := range rows {
			numbers[i] = i + 1
			submitRows[i] = message.SubmitRow{Row: i + 1, Inputs: inputs}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO batch_rows (batch_id, n, owner_id, inputs)
			SELECT $1, n, $2, inputs FROM unnest($3::int[], $4::jsonb[]) AS u(n, inputs)`, batchID, caller.ID, numbers, rows); err != nil {
			return err
		}
		if err := appendBatchEvent(ctx, tx, batchID, "batch", 0); err != nil {
			return err
		}
		err := messaging.Enqueue(ctx, tx, messaging.Command{JobID: batchID, OwnerID: caller.ID, Attempt: 1,
			Data: message.Submit{BatchID: batchID, SessionID: sessionID, Inputs: base, Rows: submitRows,
				Snapshot: message.Snapshot{WorkflowID: workflow.ID, Workflow: message.SnapshotWorkflow{ID: workflow.ID,
					Title: workflow.Title, Family: workflow.Family, Env: workflow.Env, Nodes: workflow.Nodes}}}})
		if err != nil {
			return err
		}
		out, err = getBatch(ctx, tx, batchID, false)
		return err
	})
	return out, err
}

// getBatch reads one batch (locked with lock) as its DTO.
func getBatch(ctx context.Context, tx pgx.Tx, batchID string, lock bool) (dto.Batch, error) {
	if lock { // FOR UPDATE can't go with the counts' aggregate: lock the row first
		if _, err := one[struct{ ID string }](ctx, tx, `SELECT id::text FROM batches WHERE id = $1 FOR UPDATE`, batchID); err != nil {
			return dto.Batch{}, err
		}
	}
	row, err := one[model.Batch](ctx, tx, model.BatchFrom+`WHERE b.id = $1`, batchID)
	return batchDTO(row), err
}

// batchDTO maps a row.
func batchDTO(row model.Batch) dto.Batch {
	return dto.Batch{ID: row.ID, Title: row.Title, WorkflowID: row.WorkflowID, WorkflowTitle: row.WorkflowTitle,
		SessionID: row.SessionID, Status: row.Status, Control: row.Control, Reason: row.Reason, CreatedAt: row.CreatedAt,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Counts: dto.BatchCounts{Total: row.Total, Queued: row.Queued,
			Running: row.Running, Succeeded: row.Succeeded, Failed: row.Failed, Cancelled: row.Cancelled}}
}

// rowDTO maps a row, signing its first take's key for the thumbnail.
func (s *Service) rowDTO(row model.BatchRow) dto.BatchRow {
	out := dto.BatchRow{Row: row.Row, Attempt: row.Attempt, Inputs: map[string]any{}, Status: row.Status, RunID: row.RunID,
		TakeIDs: row.TakeIDs, Error: row.Error, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt}
	_ = json.Unmarshal(row.Inputs, &out.Inputs)
	if len(row.TakeKeys) > 0 {
		out.ThumbURL = s.signer.SignPtr(&row.TakeKeys[0])
	}
	return out
}

// GetBatch returns one batch.
func (s *Service) GetBatch(ctx context.Context, caller common.Caller, batchID string) (dto.Batch, error) {
	if !utility.IsUUID(batchID) {
		return dto.Batch{}, errNotFound
	}
	var out dto.Batch
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var err error
		out, err = getBatch(ctx, tx, batchID, false)
		return err
	})
	return out, err
}

// Page is a list's cursor and size: After is the opaque next_cursor of the page before.
type Page struct {
	After string
	Limit int
}

// ParsePage reads cursor and limit (1-200, default 60); the second result is the 422 detail.
func ParsePage(cursor, limit string) (Page, string) {
	page := Page{After: cursor, Limit: 60}
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n < 1 || n > 200 {
			return page, "limit must be a whole number from 1 to 200"
		}
		page.Limit = n
	}
	if cursor != "" {
		if _, err := base64.RawURLEncoding.DecodeString(cursor); err != nil {
			return page, "cursor is not a next_cursor"
		}
	}
	return page, ""
}

// ListBatches returns a page of the caller's batches, newest first; the cursor is the last one's (created_at, id).
func (s *Service) ListBatches(ctx context.Context, caller common.Caller, page Page) (dto.BatchPage, error) {
	out := dto.BatchPage{Batches: []dto.Batch{}}
	where, args := `WHERE true`, []any{page.Limit + 1}
	if page.After != "" {
		raw, _ := base64.RawURLEncoding.DecodeString(page.After)
		stamp, id, _ := strings.Cut(string(raw), "|")
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || !utility.IsUUID(id) {
			return out, network.NewUnprocessableError("cursor is not a next_cursor", nil)
		}
		where, args = `WHERE (b.created_at, b.id) < ($2, $3::uuid)`, append(args, at, id)
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.Batch](ctx, tx, model.BatchFrom+where+` ORDER BY b.created_at DESC, b.id DESC LIMIT $1`, args...)
		if len(rows) > page.Limit {
			rows = rows[:page.Limit]
			last := rows[len(rows)-1]
			next := base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
			out.NextCursor = &next
		}
		for _, row := range rows {
			out.Batches = append(out.Batches, batchDTO(row))
		}
		return err
	})
	return out, err
}

// ListBatchRows returns a page of the batch's rows by number; the cursor is the last row's number.
func (s *Service) ListBatchRows(ctx context.Context, caller common.Caller, batchID string, page Page) (dto.BatchRowPage, error) {
	out := dto.BatchRowPage{Rows: []dto.BatchRow{}}
	if !utility.IsUUID(batchID) {
		return out, errNotFound
	}
	after := 0
	if page.After != "" {
		raw, _ := base64.RawURLEncoding.DecodeString(page.After)
		n, err := strconv.Atoi(strings.TrimPrefix(string(raw), "row "))
		if err != nil || !strings.HasPrefix(string(raw), "row ") {
			return out, network.NewUnprocessableError("cursor is not a next_cursor", nil)
		}
		after = n
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := one[struct{ ID string }](ctx, tx, `SELECT id::text FROM batches WHERE id = $1`, batchID); err != nil {
			return err
		}
		rows, err := collect[model.BatchRow](ctx, tx, model.BatchRowFrom+`WHERE br.batch_id = $1 AND br.n > $2
			ORDER BY br.n LIMIT $3`, batchID, after, page.Limit+1)
		if len(rows) > page.Limit {
			rows = rows[:page.Limit]
			next := base64.RawURLEncoding.EncodeToString([]byte("row " + strconv.Itoa(rows[len(rows)-1].Row)))
			out.NextCursor = &next
		}
		for _, row := range rows {
			out.Rows = append(out.Rows, s.rowDTO(row))
		}
		return err
	})
	return out, err
}

// ------------------------------------------------------------------ control

// control changes the locked batch with change when allowed reports "" (else that 409 detail), then writes
// cmd.pipeline.<kind> under the batch's next command number and logs the batch as changed.
func (s *Service) control(ctx context.Context, caller common.Caller, batchID, kind string,
	allowed func(b dto.Batch) string, change func(tx pgx.Tx, b dto.Batch) error) (dto.Batch, error) {
	if !utility.IsUUID(batchID) {
		return dto.Batch{}, errNotFound
	}
	var out dto.Batch
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		batch, err := getBatch(ctx, tx, batchID, true)
		if err != nil {
			return err
		}
		if detail := allowed(batch); detail != "" {
			return network.NewConflictError(detail, nil)
		}
		if err := change(tx, batch); err != nil {
			return err
		}
		if err := s.command(ctx, tx, batchID, caller.ID, message.Control{Kind: kind, BatchID: batchID}); err != nil {
			return err
		}
		if err := appendBatchEvent(ctx, tx, batchID, "batch", 0); err != nil {
			return err
		}
		out, err = getBatch(ctx, tx, batchID, false)
		return err
	})
	return out, err
}

// command writes a cmd.pipeline.* for the batch with Nats-Msg-Id <batch_id>:<n>, n its next command number.
func (s *Service) command(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.Control) error {
	var n int
	if err := tx.QueryRow(ctx, `UPDATE batches SET commands = commands + 1 WHERE id = $1 RETURNING commands`, batchID).Scan(&n); err != nil {
		return err
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: batchID, OwnerID: ownerID, Attempt: n, Data: data})
}

// setControl is a change that only sets control.
func setControl(ctx context.Context, control string) func(tx pgx.Tx, b dto.Batch) error {
	return func(tx pgx.Tx, b dto.Batch) error {
		_, err := tx.Exec(ctx, `UPDATE batches SET control = $2 WHERE id = $1`, b.ID, control)
		return err
	}
}

// ended reports whether a batch status is final.
func ended(status string) bool { return status == "done" || status == "failed" || status == "stopped" }

// PauseBatch holds the batch's queued runs (the dispatcher skips them) and asks pipelines to start no new rows.
func (s *Service) PauseBatch(ctx context.Context, caller common.Caller, batchID string) (dto.Batch, error) {
	return s.control(ctx, caller, batchID, "pause", func(b dto.Batch) string {
		if (b.Status != "queued" && b.Status != "running") || b.Control != "run" {
			return "Only a queued or running batch can be paused."
		}
		return ""
	}, setControl(ctx, "pause"))
}

// ResumeBatch lets its held runs go again and asks pipelines to continue every unfinished row.
func (s *Service) ResumeBatch(ctx context.Context, caller common.Caller, batchID string) (dto.Batch, error) {
	return s.control(ctx, caller, batchID, "resume", func(b dto.Batch) string {
		if (b.Status != "paused" && b.Control != "pause") || b.Control == "stop" {
			return "Only a paused batch can be resumed."
		}
		return ""
	}, func(tx pgx.Tx, b dto.Batch) error { // held runs go now, not once pipelines confirms running
		_, err := tx.Exec(ctx, `UPDATE batches SET control = 'run', resumed_seq = pipeline_seq WHERE id = $1`, b.ID)
		return err
	})
}

// StopBatch cancels every open run of the batch (queued ones at once, released ones through the KV cancels) and asks
// pipelines to cancel the rows not started. Final.
func (s *Service) StopBatch(ctx context.Context, caller common.Caller, batchID string) (dto.Batch, error) {
	return s.control(ctx, caller, batchID, "stop", func(b dto.Batch) string {
		if ended(b.Status) || b.Control == "stop" {
			return "This batch has already ended or is stopping."
		}
		return ""
	}, func(tx pgx.Tx, b dto.Batch) error {
		if _, err := tx.Exec(ctx, `UPDATE batches SET control = 'stop' WHERE id = $1`, b.ID); err != nil {
			return err
		}
		runs, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE batch_id = $1
			AND status IN ('queued', 'running') ORDER BY created_at FOR UPDATE`, b.ID)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.Cancelling {
				continue
			}
			if _, err := s.cancelRun(ctx, tx, run); err != nil {
				return err
			}
		}
		return nil
	})
}

// RetryBatchRow queues a failed row again under attempt + 1 (a new run; the old one and its takes stay).
func (s *Service) RetryBatchRow(ctx context.Context, caller common.Caller, batchID, rowText string) (dto.BatchRow, error) {
	row, err := strconv.Atoi(rowText)
	if !utility.IsUUID(batchID) || err != nil || row < 1 || row > MaxRows || strconv.Itoa(row) != rowText {
		return dto.BatchRow{}, errNotFound
	}
	var out dto.BatchRow
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		batch, err := getBatch(ctx, tx, batchID, true)
		if err != nil {
			return err
		}
		var status string
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT status, attempt FROM batch_rows WHERE batch_id = $1 AND n = $2 FOR UPDATE`,
			batchID, row).Scan(&status, &attempt); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if status != "failed" || batch.Status == "failed" || batch.Status == "stopped" || batch.Control == "stop" {
			return network.NewConflictError("Only a failed row of a batch that has not failed or stopped can be retried.", nil)
		}
		attempt++
		if _, err := tx.Exec(ctx, `UPDATE batch_rows SET status = 'queued', attempt = $3, run_id = NULL, take_ids = '{}',
			error = NULL, finished_at = NULL WHERE batch_id = $1 AND n = $2`, batchID, row, attempt); err != nil {
			return err
		}
		if err := s.command(ctx, tx, batchID, caller.ID, message.Control{Kind: "retry", BatchID: batchID, Row: &row,
			Attempt: &attempt}); err != nil {
			return err
		}
		if err := batchChanged(ctx, tx, batchID, row); err != nil {
			return err
		}
		read, err := one[model.BatchRow](ctx, tx, model.BatchRowFrom+`WHERE br.batch_id = $1 AND br.n = $2`, batchID, row)
		out = s.rowDTO(read)
		return err
	})
	return out, err
}

// ------------------------------------------------------------------ events

// BatchEvents returns the batch's events with seq > since (at most 500), each with the batch or row as it is now; of
// several events of one row (or of the batch) in the page only the last is kept. With nothing new it reads only the
// batch's event_seq.
func (s *Service) BatchEvents(ctx context.Context, caller common.Caller, batchID string, since int64) (dto.BatchEventPage, error) {
	out := dto.BatchEventPage{Events: []dto.BatchEvent{}, LastSeq: since}
	if !utility.IsUUID(batchID) {
		return out, errNotFound
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var last int64
		if err := tx.QueryRow(ctx, `SELECT event_seq FROM batches WHERE id = $1`, batchID).Scan(&last); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil || last <= since {
			return err
		}
		type event struct {
			Seq  int64
			Type string
			Row  *int
			TS   time.Time
		}
		events, err := collect[event](ctx, tx, `SELECT seq, type, n, ts FROM batch_events WHERE batch_id = $1 AND seq > $2
			ORDER BY seq LIMIT 500`, batchID, since)
		if err != nil || len(events) == 0 {
			return err
		}
		out.LastSeq = events[len(events)-1].Seq
		batch, err := getBatch(ctx, tx, batchID, false)
		if err != nil {
			return err
		}
		var numbers []int
		lastOf := map[int]int64{} // row (0: the batch) → its last event's seq in the page
		for _, e := range events {
			n := 0
			if e.Type == "row" && e.Row != nil {
				n = *e.Row
				numbers = append(numbers, n)
			}
			lastOf[n] = e.Seq
		}
		rows, err := collect[model.BatchRow](ctx, tx, model.BatchRowFrom+`WHERE br.batch_id = $1 AND br.n = ANY($2)`, batchID, numbers)
		if err != nil {
			return err
		}
		byRow := map[int]dto.BatchRow{}
		for _, row := range rows {
			byRow[row.Row] = s.rowDTO(row)
		}
		for _, e := range events {
			n, data := 0, any(batch)
			if e.Type == "row" && e.Row != nil {
				n, data = *e.Row, byRow[*e.Row]
			}
			if lastOf[n] == e.Seq {
				out.Events = append(out.Events, dto.BatchEvent{Seq: e.Seq, Type: e.Type, TS: e.TS, Data: data})
			}
		}
		return nil
	})
	return out, err
}

// ------------------------------------------------------------------ studio.runs.create

// runsCreateKey is the idempotency key's shape (contracts defs idempotencyKey).
var runsCreateKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[1-9][0-9]{0,2}:[1-9][0-9]{0,3}:[a-z][a-z0-9_]{0,63}:(0|[1-9][0-9]{0,4}):[a-z0-9_-]{1,64}$`)

// CreateBatchRun answers studio.runs.create as System: the run of a batch row's step, made once per idempotency key.
// Owner, session and graph come from the batch record (its snapshot); the request must name them. A stopped batch's
// run is created cancelled, one with no eligible host failed, any other queued (held by the dispatcher while paused).
func (s *Service) CreateBatchRun(ctx context.Context, req *message.RunsCreate) (message.RunsCreated, *message.RunsCreateError) {
	invalid := func(detail string) *message.RunsCreateError {
		return &message.RunsCreateError{Error: detail, Code: "invalid"}
	}
	notFound := &message.RunsCreateError{Error: "no such batch, session or workflow for this owner", Code: "not_found"}
	if !runsCreateKey.MatchString(req.IdempotencyKey) || !strings.HasPrefix(req.IdempotencyKey, fmt.Sprintf("%s:%d:", req.BatchID, req.Row)) {
		return message.RunsCreated{}, invalid("idempotency_key must be <batch_id>:<row>:<attempt>:<node>:<loop>:<branch> of this batch and row")
	}
	if req.Lane != "batch" || !utility.IsUUID(req.BatchID) || !utility.IsUUID(req.OwnerID) || !utility.IsUUID(req.SessionID) {
		return message.RunsCreated{}, invalid("lane must be batch, and owner_id, session_id and batch_id uuids")
	}
	if !s.registry.Ready() {
		return message.RunsCreated{}, &message.RunsCreateError{Error: "studio is starting; try again", Code: "internal"}
	}
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}
	var out message.RunsCreated
	var refused *message.RunsCreateError
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		type batchRecord struct {
			OwnerID   string
			SessionID *string
			Status    string
			Control   string
			Snapshot  json.RawMessage
		}
		batch, err := one[batchRecord](ctx, tx, `SELECT owner_id::text, session_id::text, status, control, snapshot FROM batches
			WHERE id = $1 FOR UPDATE`, req.BatchID) // serialises two asks under one key
		var workflow registry.Workflow
		if err == nil {
			err = json.Unmarshal(batch.Snapshot, &workflow)
		}
		if errors.Is(err, errNotFound) || (err == nil && (batch.OwnerID != req.OwnerID || batch.SessionID == nil ||
			*batch.SessionID != req.SessionID || workflow.ID != req.WorkflowID)) {
			refused = notFound
			return nil
		} else if err != nil {
			return err
		}
		// the row's current attempt and the inputs stored at Submit: the run is made from those, never from the request
		var attempt int
		var stored map[string]any
		if err := tx.QueryRow(ctx, `SELECT attempt, inputs FROM batch_rows WHERE batch_id = $1 AND n = $2`, req.BatchID,
			req.Row).Scan(&attempt, &stored); errors.Is(err, pgx.ErrNoRows) {
			refused = notFound
			return nil
		} else if err != nil {
			return err
		}
		if !strings.HasSuffix(req.IdempotencyKey, fmt.Sprintf(":%d:step:0:0", attempt)) { // M1: one step, no loops or branches
			refused = invalid(fmt.Sprintf("idempotency_key must be <batch_id>:<row>:%d:step:0:0 (the row's attempt)", attempt))
			return nil
		}
		req.Inputs = stored
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE idempotency_key = $1`, req.IdempotencyKey)
		created := errors.Is(err, errNotFound)
		if created {
			nodes, detail := resolve(workflow, req.Inputs)
			if detail == "" {
				if detail, err = checkImages(ctx, tx, s.store, req.OwnerID, nodes); err != nil {
					return err
				}
			}
			if detail != "" {
				refused = invalid(detail)
				return nil
			}
			if run, err = createBatchRun(ctx, tx, s.registry, workflow, nodes, batch.Status, batch.Control, req); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		rows, _ := tx.Query(ctx, `SELECT take_id::text FROM run_outputs WHERE run_id = $1 AND take_id IS NOT NULL ORDER BY idx`, run.ID)
		takeIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if takeIDs == nil {
			takeIDs = []string{}
		}
		out = message.RunsCreated{RunID: run.ID, Status: run.Status, Created: created, TakeIDs: takeIDs, Error: run.Error}
		return err
	})
	if err != nil {
		return out, &message.RunsCreateError{Error: "internal error", Code: "internal"}
	}
	return out, refused
}

// createBatchRun stores the run of a batch row's step: cancelled for a stopped batch, failed with no eligible host,
// else queued (lane batch); its status event goes to the session's log, and a terminal one to pipelines.
func createBatchRun(ctx context.Context, tx pgx.Tx, reg *registry.Registry, workflow registry.Workflow,
	nodes map[string]registry.Node, batchStatus, control string, req *message.RunsCreate) (model.Run, error) {
	graph, _ := json.Marshal(nodes)
	status, failure := "queued", (*string)(nil)
	if control == "stop" || batchStatus == "stopped" {
		status = "cancelled"
	} else if _, _, reason := eligible(reg, reg.Hosts(), model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph},
		time.Now()); reason != "" {
		status, failure = "failed", &reason
	}
	inputs, _ := json.Marshal(req.Inputs)
	run, err := one[model.Run](ctx, tx, `INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status, error,
		nodes, lane, batch_id, batch_row, idempotency_key, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'batch', $10, $11, $12, CASE WHEN $7 <> 'queued' THEN clock_timestamp() END)
		RETURNING `+model.RunCols, utility.NewID(), req.OwnerID, req.SessionID, workflow.ID, workflow.Env, inputs, status,
		failure, graph, req.BatchID, req.Row, req.IdempotencyKey)
	if err != nil {
		return run, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET updated_at = clock_timestamp() WHERE id = $1`, req.SessionID); err != nil {
		return run, err
	}
	// the row shows its run at once (running once it leaves the queue); pipelines reports the same run later
	attempt := strings.Split(req.IdempotencyKey, ":")[2]
	if _, err := tx.Exec(ctx, `UPDATE batch_rows SET run_id = $3 WHERE batch_id = $1 AND n = $2 AND attempt::text = $4
		AND run_id IS NULL`, req.BatchID, req.Row, run.ID, attempt); err != nil {
		return run, err
	}
	if err := appendEvent(ctx, tx, run, "status", time.Now(), statusData(status, failure, nil, run.Attempt)); err != nil {
		return run, err
	}
	if terminal[status] {
		return run, runEnded(ctx, tx, run)
	}
	return run, nil
}

// checkRowImages checks the Image values of every row as Create does, each distinct take once: the detail of the first
// bad one ("row 3: encode.refs[1].image: take <id> not found"), "" when all are good.
func (s *Service) checkRowImages(ctx context.Context, tx pgx.Tx, ownerID string, workflow registry.Workflow,
	rows []map[string]any) (string, error) {
	perRow := make([][]imageAt, len(rows))
	var all []imageAt
	for i, inputs := range rows {
		nodes, _ := resolve(workflow, inputs) // expand checked them
		perRow[i] = images(nodes)
		all = append(all, perRow[i]...)
	}
	problems, err := vetTakes(ctx, tx, s.store, ownerID, all)
	if err != nil {
		return "", err
	}
	for i, refs := range perRow {
		if detail := firstBad(refs, problems); detail != "" {
			return fmt.Sprintf("row %d: %s", i+1, detail), nil
		}
	}
	return "", nil
}
