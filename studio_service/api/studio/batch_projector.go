package studio

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// MountPipeline registers the handlers of pipelines_service's evt.pipeline.<batch_id>.batch and .row (durable
// studio-pipelines): they build the batch record the UI reads.
func (p *Projector) MountPipeline(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.pipeline.*.batch", pipelineHandler(p, applyPipelineBatch))
	group.AddHandler("evt.pipeline.*.row", pipelineHandler(p, applyPipelineRow))
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
var rowStatuses = map[string]bool{"queued": true, "running": true, "succeeded": true, "failed": true, "cancelled": true}

// applyPipelineRow sets the row's whole state when the event is newer than the last one applied to it and not of an
// older attempt. The run must be a run of this batch, and only the owner's takes are kept.
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
	tag, err := tx.Exec(ctx, `UPDATE batch_rows br SET status = $3, attempt = $4, pipeline_seq = $5, error = $6,
		run_id = (SELECT id FROM runs WHERE id = $7::uuid AND batch_id = $1 AND batch_row = $2),
		take_ids = coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($8::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $9), '{}'),
		finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'cancelled') THEN coalesce(br.finished_at, clock_timestamp()) END
		WHERE batch_id = $1 AND n = $2 AND pipeline_seq < $5 AND attempt <= $4`,
		batchID, data.Row, data.Status, data.Attempt, data.Seq, data.Error, data.RunID, data.TakeIDs, ownerID)
	return data.Row, err == nil && tag.RowsAffected() == 1, err
}
