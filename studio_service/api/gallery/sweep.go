package gallery

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/storage"

	"github.com/helmstudio/studio_service/common"
)

// Grace is how long a deleted take keeps its object (a link already handed out keeps working meanwhile).
const Grace = time.Hour

// sweepBatch is the most takes one Sweep removes.
const sweepBatch = 500

// Sweeper removes the objects, then the rows, of takes deleted for more than Grace and of uploads left pending for
// more than UploadTTL.
type Sweeper struct {
	pool  *pgxpool.Pool
	store storage.Store
}

// NewSweeper returns a Sweeper.
func NewSweeper(pool *pgxpool.Pool, store storage.Store) *Sweeper {
	return &Sweeper{pool: pool, store: store}
}

// Sweep removes one batch as System and returns how many takes it removed. It only ever deletes a key under its own
// take's owner prefix u/<owner_id>/ (anything else is logged and left, row included) and never a key another live
// take still points at. The objects go first: a failure keeps the rows for the next sweep.
func (s *Sweeper) Sweep(ctx context.Context) (int, error) {
	removed := 0
	err := common.System(ctx, s.pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT t.id::text, t.owner_id::text, t.key FROM takes t
			WHERE (t.deleted_at < clock_timestamp() - make_interval(secs => $1)
				OR (NOT t.ready AND t.created_at < clock_timestamp() - make_interval(secs => $2)))
			AND NOT EXISTS (SELECT 1 FROM takes o WHERE o.key = t.key AND o.id <> t.id AND o.deleted_at IS NULL)
			ORDER BY t.created_at LIMIT $3 FOR UPDATE SKIP LOCKED`, Grace.Seconds(), UploadTTL.Seconds(), sweepBatch)
		found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct{ ID, Owner, Key string }])
		if err != nil {
			return err
		}
		var ids, keys []string
		for _, row := range found {
			if !common.Owns(row.Owner, row.Key) || strings.HasSuffix(row.Key, "/") {
				log.Printf("take sweep: take %s key outside u/%s/, left alone", row.ID, row.Owner)
				continue
			}
			ids, keys = append(ids, row.ID), append(keys, row.Key)
		}
		if len(ids) == 0 {
			return nil
		}
		if err := s.store.Delete(ctx, keys...); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM takes WHERE id = ANY($1::uuid[])`, ids)
		removed = int(tag.RowsAffected())
		return err
	})
	return removed, err
}

// Run sweeps every minute until ctx ends.
func (s *Sweeper) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("take sweep: %v", err)
		}
	}
}
