package studio

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// Projector applies evt.run.<session_id>.<kind> to the runs and run_outputs tables and appends each applied event to
// the session's log, as System.
type Projector struct {
	pool *pgxpool.Pool
}

// NewProjector returns the projector writing through pool.
func NewProjector(pool *pgxpool.Pool) *Projector {
	return &Projector{pool: pool}
}

// MountNatsJetStream registers one handler per evt.run.* kind.
func (p *Projector) MountNatsJetStream(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.run.*.status", handler(p, "status", applyStatus))
	group.AddHandler("evt.run.*.progress", handler(p, "progress", applyProgress))
	group.AddHandler("evt.run.*.output", handler(p, "output", applyOutput))
}

// based is an event's data: every one has a seq.
type based interface{ Common() message.Base }

// applyFunc projects one event onto its run (locked, open or not) and returns what run_events stores for it; nil
// stores nothing (an event the run's state no longer takes).
type applyFunc[D based] func(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[D]) (any, error)

// handler decodes the envelope (an undecodable one is dropped), projects it and acks; a database failure naks it.
func handler[D based](p *Projector, typ string, apply applyFunc[D]) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		if err := project(ctx, p.pool, typ, sessionOf(msg.Subject()), event, apply); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// sessionOf is the <session_id> of evt.run.<session_id>.<kind>.
func sessionOf(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) != 4 {
		return ""
	}
	return parts[2]
}

// project applies the event once per (job_id, seq) when its run belongs to the envelope's owner and the subject's
// session, then appends it to the session's log. Anything else is logged and skipped (acked).
func project[D based](ctx context.Context, pool *pgxpool.Pool, typ, sessionID string, event *gen.Message[D], apply applyFunc[D]) error {
	seq := event.Data.Common().Seq
	if event.JobID == nil || !utility.IsUUID(*event.JobID) || seq < 1 {
		log.Printf("projector: dropping %s without a job_id or seq", event.Type)
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 FOR UPDATE`, *event.JobID)
		if errors.Is(err, errNotFound) || (err == nil && (run.OwnerID != event.OwnerID || run.SessionID != sessionID)) {
			log.Printf("projector: %s for unknown run %s (owner %s, session %s)", event.Type, *event.JobID, event.OwnerID, sessionID)
			return nil
		} else if err != nil {
			return err
		}
		if first, err := messaging.FirstSeen(ctx, tx, run.ID, seq); err != nil || !first {
			return err
		}
		stored, err := apply(ctx, tx, run, event)
		if err != nil || stored == nil {
			return err
		}
		return appendEvent(ctx, tx, run, typ, eventTime(event.OccurredAt), stored)
	})
}

// applyStatus moves the run on when canMove allows it: running sets started_at and the host, a terminal status the
// error and finished_at. Anything else (a repeat, anything after the end) is ignored.
func applyStatus(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Status]) (any, error) {
	data := event.Data
	if !canMove(run.Status, data.Status) {
		return nil, nil
	}
	if data.Status != "failed" {
		data.Error = nil
	} else if data.Error == nil || *data.Error == "" {
		reason := "the run failed"
		data.Error = &reason
	}
	host := run.Host
	if data.Host != nil {
		host = data.Host
	}
	_, err := tx.Exec(ctx, `UPDATE runs SET status = $2, error = $3, host = $4, updated_at = clock_timestamp(),
		started_at = CASE WHEN $2 = 'running' THEN clock_timestamp() ELSE started_at END,
		finished_at = CASE WHEN $2 = 'running' THEN NULL ELSE clock_timestamp() END
		WHERE id = $1`, run.ID, data.Status, data.Error, host)
	return statusData(data.Status, data.Error, host), err
}

// applyProgress stores the run's last step; a run already ended takes none.
func applyProgress(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Progress]) (any, error) {
	if terminal[run.Status] {
		return nil, nil
	}
	data := event.Data
	progress := map[string]any{"node": data.Node, "step": data.Step, "of": data.Of}
	_, err := tx.Exec(ctx, `UPDATE runs SET progress = $2, updated_at = clock_timestamp() WHERE id = $1`, run.ID, progress)
	return progress, err
}

// applyOutput records an output stored under the run's own prefix; a key outside it, or a run already ended, is
// dropped. The stored event keeps the key: it is signed when read.
func applyOutput(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Output]) (any, error) {
	data := event.Data
	prefix := common.OwnerPrefix(run.OwnerID) + "runs/" + run.ID + "/"
	if terminal[run.Status] || !common.Owns(run.OwnerID, data.Key) || !strings.HasPrefix(data.Key, prefix) ||
		(data.Kind != "image" && data.Kind != "video") || data.Index < 0 {
		log.Printf("projector: dropping output %d of run %s (%s)", data.Index, run.ID, data.Key)
		return nil, nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO run_outputs (run_id, owner_id, idx, key, kind, width, height, bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (run_id, idx) DO NOTHING`,
		run.ID, run.OwnerID, data.Index, data.Key, data.Kind, data.Width, data.Height, data.Bytes)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET updated_at = clock_timestamp() WHERE id = $1`, run.ID); err != nil {
		return nil, err
	}
	return map[string]any{"index": data.Index, "key": data.Key, "kind": data.Kind, "width": data.Width, "height": data.Height}, nil
}

// eventTime is when a stored event happened; the zero time (a malformed envelope) is now.
func eventTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
