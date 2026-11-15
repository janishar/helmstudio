package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// MountPipeline registers the handlers of pipelines_service's evt.pipeline.<batch_id>.batch, .row and .step (durable
// studio-pipelines): they build the batch record the UI reads.
func (p *Projector) MountPipeline(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.pipeline.*.batch", pipelineHandler(p, applyPipelineBatch))
	group.AddHandler("evt.pipeline.*.row", pipelineHandler(p, applyPipelineRow))
	group.AddHandler("evt.pipeline.*.step", pipelineHandler(p, applyPipelineStep))
}

// pipelineApply applies one event to its batch, locked and checked; it returns the row it changed (0: none) and
// whether anything changed.
type pipelineApply[D any] func(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data D) (int, bool, error)

// pipelineHandler decodes the envelope (an undecodable one is dropped) and applies it once per (batch, seq) when the
// batch belongs to the envelope's owner and the subject names it; a database failure naks it.
func pipelineHandler[D interface{ PipelineSeq() int64 }](p *Projector, apply pipelineApply[D]) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		parts := strings.Split(msg.Subject(), ".")
		seq := event.Data.PipelineSeq()
		if event.JobID == nil || !utility.IsUUID(*event.JobID) || len(parts) != 4 || parts[2] != *event.JobID || seq < 1 {
			log.Printf("batch projector: dropping %s on %s without a matching job_id or a seq", event.Type, msg.Subject())
			micro.AckNatsJetStreamMessage(msg)
			return
		}
		batchID := *event.JobID
		err = common.System(ctx, p.pool, func(tx pgx.Tx) error {
			var owner string
			err := tx.QueryRow(ctx, `SELECT owner_id::text FROM batches WHERE id = $1 FOR UPDATE`, batchID).Scan(&owner)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != event.OwnerID) {
				log.Printf("batch projector: %s for unknown batch %s (owner %s)", event.Type, batchID, event.OwnerID)
				return nil
			} else if err != nil {
				return err
			}
			// attempt 0: a batch's seq space, apart from any run's (job_id differs anyway)
			if first, err := messaging.FirstSeen(ctx, tx, batchID, 0, int(seq)); err != nil || !first {
				return err
			}
			row, changed, err := apply(ctx, tx, batchID, owner, event.Data)
			if err != nil || !changed {
				return err
			}
			return batchChanged(ctx, tx, batchID, row)
		})
		if err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// batchStatuses are the statuses pipelines reports for a batch.
var batchStatuses = map[string]bool{"running": true, "paused": true, "done": true, "failed": true, "stopped": true}

// applyPipelineBatch sets the batch's status when the event is newer than the last one applied: reason with paused or
// failed, started_at at the first running, finished_at while done, failed or stopped.
func applyPipelineBatch(ctx context.Context, tx pgx.Tx, batchID, _ string, data message.PipelineBatch) (int, bool, error) {
	if !batchStatuses[data.Status] {
		log.Printf("batch projector: batch %s: unknown status %q", batchID, data.Status)
		return 0, false, nil
	}
	if data.Status != "paused" && data.Status != "failed" {
		data.Reason = nil
	}
	tag, err := tx.Exec(ctx, `UPDATE batches SET status = $2, reason = $3, pipeline_seq = $4,
		started_at = CASE WHEN $2 = 'running' THEN coalesce(started_at, clock_timestamp()) ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('done', 'failed', 'stopped') THEN coalesce(finished_at, clock_timestamp()) END
		WHERE id = $1 AND pipeline_seq < $4`, batchID, data.Status, data.Reason, data.Seq)
	return 0, err == nil && tag.RowsAffected() == 1, err
}

// rowStatuses are the statuses pipelines reports for a row.
var rowStatuses = map[string]bool{"queued": true, "running": true, "waiting": true, "succeeded": true, "failed": true,
	"cancelled": true}

// applyPipelineRow sets the row's whole state when the event is newer than the last one applied to it and not of an
// older attempt. The run must be a run of this batch, and only the owner's takes are kept. A pipeline row also takes
// node, cells and, while waiting, parked; a waiting for a gate visit already picked (pipelines republished it before it
// read the pick) shows running.
func applyPipelineRow(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineRow) (int, bool, error) {
	if !rowStatuses[data.Status] {
		log.Printf("batch projector: batch %s row %d: unknown status %q", batchID, data.Row, data.Status)
		return 0, false, nil
	}
	if data.Status != "failed" {
		data.Error = nil
	}
	if data.TakeIDs == nil {
		data.TakeIDs = []string{}
	}
	data.TakeIDs = slices.DeleteFunc(data.TakeIDs, func(id string) bool { return !utility.IsUUID(id) })
	if data.RunID != nil && !utility.IsUUID(*data.RunID) {
		data.RunID = nil
	}
	var parked *dto.BatchRowParked
	if data.Status == "waiting" {
		if json.Unmarshal(data.Parked, &parked) != nil || parked == nil || parked.Gate == "" || len(parked.Candidates) == 0 {
			log.Printf("batch projector: batch %s row %d: waiting without parked", batchID, data.Row)
			return 0, false, nil
		}
		var picked bool
		if err := tx.QueryRow(ctx, `SELECT picks ? $3 FROM batch_rows WHERE batch_id = $1 AND n = $2`, batchID, data.Row,
			pickKey(data.Attempt, parked.Gate, parked.Visit)).Scan(&picked); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, err
		} else if picked {
			data.Status, parked = "running", nil
		}
	}
	var cells any // absent: the row keeps its cells
	if len(data.Cells) > 0 && string(data.Cells) != "null" {
		cells = data.Cells
	}
	tag, err := tx.Exec(ctx, `UPDATE batch_rows br SET status = $3, attempt = $4, pipeline_seq = $5, error = $6,
		run_id = (SELECT id FROM runs WHERE id = $7::uuid AND batch_id = $1 AND batch_row = $2),
		take_ids = coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($8::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $9), '{}'),
		finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'cancelled') THEN coalesce(br.finished_at, clock_timestamp()) END,
		node = $10, parked = $11, cells = CASE WHEN br.cells IS NULL THEN NULL ELSE coalesce($12::jsonb, br.cells) END
		WHERE batch_id = $1 AND n = $2 AND pipeline_seq < $5 AND attempt <= $4`,
		batchID, data.Row, data.Status, data.Attempt, data.Seq, data.Error, data.RunID, data.TakeIDs, ownerID, data.Node,
		parked, cells)
	return data.Row, err == nil && tag.RowsAffected() == 1, err
}

// stepParts and stepStatuses are what evt.pipeline.step may carry.
var (
	stepParts    = map[string]bool{"inputs": true, "submit": true, "wait": true, "gate": true, "route": true, "output": true}
	stepStatuses = map[string]bool{"finished": true, "resumed": true, "current": true}
)

// applyPipelineStep stores one graph step of a pipeline row when it is newer than the one stored for (row, attempt,
// step). Only runs of this batch row and the owner's takes are kept.
func applyPipelineStep(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineStep) (int, bool, error) {
	if !stepParts[data.Part] || !stepStatuses[data.Status] || data.Step < 1 || data.Attempt < 1 || data.Node == "" {
		log.Printf("batch projector: batch %s row %d: dropping step %d (%s %s)", batchID, data.Row, data.Step, data.Part, data.Status)
		return 0, false, nil
	}
	keep := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !utility.IsUUID(id) })
	}
	edge := any(nil)
	if len(data.Edge) > 0 && string(data.Edge) != "null" {
		edge = data.Edge
	}
	if data.Changed == nil {
		data.Changed = []string{}
	}
	if data.Loops == nil {
		data.Loops = map[string]int{}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO batch_steps (batch_id, n, attempt, step, owner_id, seq, node, part, visit, status,
		what, changed, edge, loops, run_ids, take_ids, error, checkpoint_id)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14,
		coalesce((SELECT array_agg(r.id ORDER BY u.ord) FROM unnest($15::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN runs r ON r.id = u.id AND r.batch_id = $1 AND r.batch_row = $2), '{}'),
		coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($16::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $5), '{}'), $17, $18
		FROM batch_rows WHERE batch_id = $1 AND n = $2
		ON CONFLICT (batch_id, n, attempt, step) DO UPDATE SET seq = excluded.seq, node = excluded.node, part = excluded.part,
		visit = excluded.visit, status = excluded.status, what = excluded.what, changed = excluded.changed,
		edge = excluded.edge, loops = excluded.loops, run_ids = excluded.run_ids, take_ids = excluded.take_ids,
		error = excluded.error, checkpoint_id = excluded.checkpoint_id, at = excluded.at
		WHERE batch_steps.seq < excluded.seq`,
		batchID, data.Row, data.Attempt, data.Step, ownerID, data.Seq, data.Node, data.Part, max(data.Visit, 1), data.Status,
		data.What, data.Changed, edge, data.Loops, keep(data.RunIDs), keep(data.TakeIDs), data.Error, data.CheckpointID)
	return data.Row, err == nil && tag.RowsAffected() == 1, err
}

// pickKey names one gate visit of a row attempt in batch_rows.picks.
func pickKey(attempt int, gate string, visit int) string {
	return fmt.Sprintf("%d:%s:%d", attempt, gate, visit)
}
