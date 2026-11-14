package messaging

import (
	"context"
	"time"

	"github.com/helmstudio/orago/micro"
	"github.com/jackc/pgx/v5"
)

// ProjectorConsumer is the durable consumer the projector reads EVT with: one message at a time, so the events of a
// run apply in order.
var ProjectorConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-projector", Subject: "evt.run.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// FirstSeen records (jobID, seq) inside the projector's system transaction and reports whether it is new; a false
// means the event was projected already (a redelivery) and must be skipped.
func FirstSeen(ctx context.Context, tx pgx.Tx, jobID string, seq int) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO projector_seen (job_id, seq) VALUES ($1, $2) ON CONFLICT DO NOTHING`, jobID, seq)
	return err == nil && tag.RowsAffected() == 1, err
}
