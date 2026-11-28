package gallery

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/storage"

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
// take still points at, nor (API 1.31) a take one of its owner's voices keeps as its clip (deleting the voice frees it
// for a later sweep; contracts/README.md, Voices: cloned voices). A video's poster goes with its MP4, its thumbnail (API 1.48) and frame grid (1.104) with it, and the LLM thumbnails a judge made of it
// (u/<owner_id>/llm/thumbs/, a copy of the take) unless a live take's decision shows the same one. The objects go
// first: a failure keeps the rows for the next sweep. A frame take made before API 1.48 may share its key with its
// video's last frame: neither is swept while the other is live (ponytail: scans video->'last'; an index if sweeps slow).
func (s *Sweeper) Sweep(ctx context.Context) (int, error) {
	removed := 0
	err := common.System(ctx, s.pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT t.id::text, t.owner_id::text, t.key, t.video->'poster'->>'key', t.video->'last'->>'key',
			t.thumb->>'key', t.facts->>'key' FROM takes t
			WHERE (t.deleted_at < clock_timestamp() - make_interval(secs => $1)
				OR (NOT t.ready AND t.created_at < clock_timestamp() - make_interval(secs =>
					CASE WHEN t.source = 'import' THEN $4::float8 ELSE $2::float8 END)))
			AND NOT EXISTS (SELECT 1 FROM takes o WHERE o.key = t.key AND o.id <> t.id AND o.deleted_at IS NULL)
			AND NOT EXISTS (SELECT 1 FROM takes o WHERE o.id <> t.id AND o.deleted_at IS NULL
				AND (o.video->'last'->>'key' = t.key OR o.key = t.video->'last'->>'key'))
			AND NOT EXISTS (SELECT 1 FROM library_items l WHERE l.owner_id = t.owner_id AND l.kind = 'voice'
				AND l.voice->'clip'->>'take_id' = t.id::text)
			ORDER BY t.created_at LIMIT $3 FOR UPDATE SKIP LOCKED`, Grace.Seconds(), UploadTTL.Seconds(), sweepBatch,
			ImportLink.Seconds())
		found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
			ID, Owner, Key      string
			Poster, Last, Thumb *string
			Grid                *string // API 1.104: a video's frame grid (Take facts)
		}])
		if err != nil {
			return err
		}
		var ids, keys []string
		for _, row := range found {
			bad := func(key string) bool { return !common.Owns(row.Owner, key) || strings.HasSuffix(key, "/") }
			if bad(row.Key) || (row.Poster != nil && bad(*row.Poster)) || (row.Last != nil && bad(*row.Last)) ||
				(row.Thumb != nil && bad(*row.Thumb)) || (row.Grid != nil && bad(*row.Grid)) {
				log.Printf("take sweep: take %s key outside u/%s/, left alone", row.ID, row.Owner)
				continue
			}
			ids, keys = append(ids, row.ID), append(keys, row.Key)
			if row.Poster != nil {
				keys = append(keys, *row.Poster)
			}
			if row.Last != nil { // API 1.17: the stored last frame (a frame take is a copy of it, never this object)
				keys = append(keys, *row.Last)
			}
			if row.Thumb != nil { // API 1.48: its thumbnail, never shared with another take
				keys = append(keys, *row.Thumb)
			}
			if row.Grid != nil { // API 1.104: its frame grid, never shared either
				keys = append(keys, *row.Grid)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		thumbs, err := sweptThumbs(ctx, tx, ids)
		if err != nil {
			return err
		}
		keys = append(keys, thumbs...)
		if err := s.store.Delete(ctx, keys...); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM takes WHERE id = ANY($1::uuid[])`, ids)
		removed = int(tag.RowsAffected())
		return err
	})
	return removed, err
}

// sweptThumbs are the LLM thumbnail keys the decisions saw of the takes ids (decisions.data seen[]), under their
// owner's u/<owner_id>/llm/thumbs/, that no decision shows of a take not deleted. ponytail: scans the owner's decisions
// for the shared check; a thumbs table keyed by take if that grows slow.
func sweptThumbs(ctx context.Context, tx pgx.Tx, ids []string) ([]string, error) {
	rows, _ := tx.Query(ctx, `SELECT DISTINCT s->>'key' FROM decisions d, jsonb_array_elements(d.data->'seen') s
		WHERE d.take_ids && $1::uuid[] AND s->>'take_id' = ANY($1::uuid[]::text[])
		AND starts_with(s->>'key', 'u/' || d.owner_id::text || '/llm/thumbs/')
		AND NOT EXISTS (SELECT 1 FROM decisions o, jsonb_array_elements(o.data->'seen') os
			JOIN takes t ON t.id::text = os->>'take_id' AND t.deleted_at IS NULL
			WHERE o.owner_id = d.owner_id AND os->>'key' = s->>'key')`, ids)
	return pgx.CollectRows(rows, pgx.RowTo[string])
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
