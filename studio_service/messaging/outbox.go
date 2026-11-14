// Package messaging: transactional outbox → CMD, and the EVT consumer settings and dedupe the projector shares.
package messaging

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Data is the data of one command message. It names its own envelope type and subject, so Enqueue can't queue it
// under the wrong ones.
type Data interface {
	Type() string
	Subject() string
}

// Command is one message the relay publishes (a cmd.*, or an evt.studio.*). JobID is the work's id (a run or batch
// id); Nats-Msg-Id = MsgID, or <job_id>:<attempt> when MsgID is empty.
type Command struct {
	JobID   string
	OwnerID string
	Attempt int // 1 for new work; a batch's command counter for cmd.pipeline.*
	MsgID   string
	Data    Data
}

// Enqueue stores the command in the outbox inside the caller's transaction; the relay publishes it after commit.
func Enqueue(ctx context.Context, tx pgx.Tx, c Command) error {
	data, err := json.Marshal(c.Data)
	if err != nil {
		return err
	}
	if c.Attempt < 1 {
		c.Attempt = 1
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox (type, subject, job_id, owner_id, attempt, data, msg_id)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7, ''))`, c.Data.Type(), c.Data.Subject(), c.JobID, c.OwnerID, c.Attempt, data, c.MsgID)
	return err
}
