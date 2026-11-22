package messaging

import (
	"context"
	"time"

	"github.com/helmstudio/helmgo/micro"
	"github.com/jackc/pgx/v5"
)

// ProjectorConsumer is the durable consumer the projector reads EVT with: one message at a time, so the events of a
// run apply in order.
var ProjectorConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-projector", Subject: "evt.run.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// PipelineConsumer is the durable consumer the batch projector reads pipelines_service's evt.pipeline.> with, one
// message at a time.
var PipelineConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-pipelines", Subject: "evt.pipeline.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// ModelsConsumer is the durable consumer the model-job projector reads the runners' evt.model.> with (API 1.16).
var ModelsConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-models", Subject: "evt.model.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// MediaConsumer is the durable consumer the media-job projector reads the runners' evt.media.> with (API 1.17).
var MediaConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-media", Subject: "evt.media.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// AssistantConsumer is the durable consumer the assistant's steps (pipelines' evt.assistant.>) are read with (API 1.42),
// one message at a time: each chat's steps apply in order.
var AssistantConsumer = micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "studio-assistant", Subject: "evt.assistant.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}

// FirstSeen records (jobID, attempt, seq) inside the projector's system transaction and reports whether it is new; a
// false means the event was projected already (a redelivery) and must be skipped. seq restarts at 1 for each attempt.
func FirstSeen(ctx context.Context, tx pgx.Tx, jobID string, attempt, seq int) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO projector_seen (job_id, attempt, seq) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		jobID, attempt, seq)
	return err == nil && tag.RowsAffected() == 1, err
}
