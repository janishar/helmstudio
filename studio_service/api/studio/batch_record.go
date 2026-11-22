package studio

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/messaging"
)

// What a batch run's changes do to its batch: every place a run's status changes calls these in its transaction.

// runEnded is called in the transaction that made run terminal: for a batch run it writes evt.studio.run_finished to
// the outbox (so pipelines learns of it even across a crash) and logs the row and batch as changed.
func runEnded(ctx context.Context, tx pgx.Tx, run model.Run) error {
	if err := shotEnded(ctx, tx, run); err != nil { // API 1.17: a cancelled New shot leaves its slot
		return err
	}
	if run.Key == nil || (run.Purpose == nil && (run.BatchID == nil || run.BatchRow == nil)) {
		return nil
	}
	rows, _ := tx.Query(ctx, `SELECT take_id::text FROM run_outputs WHERE run_id = $1 AND take_id IS NOT NULL ORDER BY idx`, run.ID)
	takeIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if takeIDs == nil {
		takeIDs = []string{}
	}
	failure := run.Error
	if run.Status != "failed" {
		failure = nil
	}
	err = messaging.Enqueue(ctx, tx, messaging.Command{JobID: run.ID, OwnerID: run.OwnerID, MsgID: run.ID + ":finished",
		Data: message.RunFinished{RunID: run.ID, BatchID: run.BatchID, Row: run.BatchRow, IdempotencyKey: *run.Key,
			Status: run.Status, TakeIDs: takeIDs, Error: failure, Answer: answerOf(run), SessionID: run.SessionID}})
	if err != nil {
		return err
	}
	return runMoved(ctx, tx, run)
}

// answerOf is a run's stored answer (a text workflow's), nil without one.
func answerOf(run model.Run) json.RawMessage {
	if len(run.Answer) == 0 || string(run.Answer) == "null" {
		return nil
	}
	return run.Answer
}

// runMoved logs a batch run's row (its shown status follows the run) and its batch (the counts) as changed.
func runMoved(ctx context.Context, tx pgx.Tx, run model.Run) error {
	if run.BatchID == nil || run.BatchRow == nil {
		return nil
	}
	return batchChanged(ctx, tx, *run.BatchID, *run.BatchRow)
}

// batchChanged appends a row event for row (0: none) and a batch event to the batch's log. Events carry no data: a
// reader gets the row and the batch as they are then (each is a whole state, so the newest is all a client needs).
func batchChanged(ctx context.Context, tx pgx.Tx, batchID string, row int) error {
	if row > 0 {
		if err := appendBatchEvent(ctx, tx, batchID, "row", row); err != nil {
			return err
		}
	}
	return appendBatchEvent(ctx, tx, batchID, "batch", 0)
}

// appendBatchEvent adds one event under the batch's next seq.
func appendBatchEvent(ctx context.Context, tx pgx.Tx, batchID, typ string, row int) error {
	_, err := tx.Exec(ctx, `WITH next AS (UPDATE batches SET event_seq = event_seq + 1 WHERE id = $1 RETURNING owner_id, event_seq)
		INSERT INTO batch_events (batch_id, seq, owner_id, type, n) SELECT $1, event_seq, owner_id, $2, nullif($3, 0) FROM next`,
		batchID, typ, row)
	return err
}
