package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/utility"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
)

// Publish sends one envelope to JetStream with a dedupe id; tests pass a fake.
type Publish func(ctx context.Context, subject string, envelope *gen.Envelope, msgID string) error

// NatsJetStreamPublisher is the production Publish: micro.NatsJetStreamPublish, with msgID as the Nats-Msg-Id.
func NatsJetStreamPublisher(client micro.NatsClient) Publish {
	return func(ctx context.Context, subject string, envelope *gen.Envelope, msgID string) error {
		return micro.NatsJetStreamPublish(ctx, client, subject, envelope, msgID)
	}
}

// RelayOnce publishes up to 100 outbox rows and deletes them, in one transaction (SKIP LOCKED: replicas share the
// work). A failed publish ends the round: that row and the later ones stay. Returns how many rows were published.
func RelayOnce(ctx context.Context, pool *pgxpool.Pool, publish Publish) (int, error) {
	n := 0
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, type, subject, job_id::text, owner_id::text, attempt, data, created_at FROM outbox
			ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		type row struct {
			ID                          int64
			Type, Subject, JobID, Owner string
			Attempt                     int
			Data                        json.RawMessage
			CreatedAt                   time.Time
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
		if err != nil {
			return err
		}
		var done []int64
		for _, r := range list {
			jobID := r.JobID
			env := gen.Envelope{Type: r.Type, Version: 1, ID: utility.NewID(), JobID: &jobID, OwnerID: r.Owner,
				OccurredAt: r.CreatedAt.UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: r.Data}
			msgID := fmt.Sprintf("%s:%d", r.JobID, r.Attempt)
			if err := publish(ctx, r.Subject, &env, msgID); err != nil {
				if ctx.Err() == nil { // a shutdown is not a failure: the row stays for the next start
					log.Printf("outbox relay: %s %s: %v", r.Subject, r.JobID, err)
				}
				break // keep order: retry this row and the rest next round
			}
			done = append(done, r.ID)
		}
		n = len(done)
		if n == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `DELETE FROM outbox WHERE id = ANY($1)`, done)
		return err
	})
	return n, err
}

// Relay runs RelayOnce until ctx ends. ponytail: polls every 250 ms; LISTEN/NOTIFY if the latency matters.
func Relay(ctx context.Context, pool *pgxpool.Pool, publish Publish) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		n, err := RelayOnce(ctx, pool, publish)
		if err != nil && ctx.Err() == nil {
			log.Printf("outbox relay: %v", err)
		}
		if n == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
