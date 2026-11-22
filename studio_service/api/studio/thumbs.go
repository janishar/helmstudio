package studio

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Thumbnails (contracts/README.md "Thumbnails", API 1.48): every image and video take without one gets it from a
// thumbs media job: QueueThumbs picks them, the media projector stores what the runner made (endThumbs). The state is
// takes.thumb itself, so a lost job, a redelivery or a restart only picks the same takes again (at most ThumbTries).

// ThumbsTick is how often RunThumbs looks for takes without a thumbnail.
const ThumbsTick = 10 * time.Second

// thumbsTodo selects, from takes aliased t, the ready, live image and video takes still without a thumbnail whose file
// (a video: its poster) is under the owner's prefix.
const thumbsTodo = `t.thumb IS NULL AND t.deleted_at IS NULL AND t.ready AND t.thumb_tries < 3 -- ThumbTries
	AND ((t.kind = 'image' AND starts_with(t.key, 'u/' || t.owner_id::text || '/'))
		OR (t.kind = 'video' AND starts_with(t.video->'poster'->>'key', 'u/' || t.owner_id::text || '/')))`

// QueueThumbs sends one thumbs job (as System) for the oldest takes without a thumbnail, of one owner, at most
// ThumbsPerJob, unless one is open already or no live env can run it; it returns the job id ("" for none).
func QueueThumbs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry) (string, error) {
	env, ok := mediaEnvOf(reg, nil, true) // never an older runner: it would fail every job
	if !ok {
		return "", nil
	}
	id := ""
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('media:thumbs'))`); err != nil {
			return err
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_jobs WHERE kind = 'thumbs'
			AND status IN ('queued', 'running'))`).Scan(&open); err != nil || open {
			return err
		}
		type pick struct{ ID, Owner, Key string }
		picks, err := collect[pick](ctx, tx, `WITH o AS (SELECT t.owner_id FROM takes t WHERE `+thumbsTodo+`
				ORDER BY t.created_at, t.id LIMIT 1)
			SELECT t.id::text, t.owner_id::text, CASE WHEN t.kind = 'video' THEN t.video->'poster'->>'key' ELSE t.key END
			FROM takes t JOIN o ON o.owner_id = t.owner_id WHERE `+thumbsTodo+`
			ORDER BY t.created_at, t.id LIMIT $1 FOR UPDATE OF t SKIP LOCKED`, ThumbsPerJob)
		if err != nil || len(picks) == 0 {
			return err
		}
		var ids, bad []string
		var inputs []timeline.MediaInput
		for _, p := range picks {
			if !common.Owns(p.Owner, p.Key) { // a key with .. or //: never sent, never picked again
				bad = append(bad, p.ID)
				continue
			}
			ids, inputs = append(ids, p.ID), append(inputs, timeline.MediaInput{Key: p.Key, Kind: "image"})
		}
		if len(bad) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE takes SET thumb_tries = $2 WHERE id = ANY($1::uuid[])`, bad, ThumbTries); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		job := mediaRow{ID: utility.NewID(), OwnerID: picks[0].Owner, Kind: "thumbs", Host: env.Host, Env: env.Env, TakeIDs: ids}
		_, err = insertMediaJob(ctx, tx, job, mediaCommand{Kind: "thumbs", Inputs: inputs, OutputPrefix: thumbsPrefix(job)})
		id = job.ID
		return err
	})
	if err != nil {
		id = ""
	}
	return id, err
}

// RunThumbs queues thumbs jobs every ThumbsTick until ctx ends.
func RunThumbs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry) {
	tick := time.NewTicker(ThumbsTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if !reg.Ready() {
			continue
		}
		if _, err := QueueThumbs(ctx, pool, reg); err != nil && ctx.Err() == nil {
			log.Printf("thumbs: %v", err)
		}
	}
}

// endThumbs applies a thumbs job's terminal status: succeeded stores each result on its take (by input index; only
// the key <prefix><i>.webp, only on a live take still without one, else the object is deleted at once), a skip ends
// the take's tries, an error or a missing result adds one; failed or cancelled adds a try to every take and deletes
// the keys the job could have written that no take points at.
func (p *Projector) endThumbs(ctx context.Context, tx pgx.Tx, job mediaRow, data mediaStatus) error {
	switch data.Status {
	case "failed", "cancelled":
		var failure any
		if data.Status == "failed" {
			failure = map[string]string{"class": "internal", "detail": "the thumbs job failed"}
		}
		if err := endMedia(ctx, tx, &job, data.Status, failure); err != nil {
			return err
		}
		unused, err := unusedThumbs(ctx, tx, job)
		if err == nil {
			deleteKeys(ctx, p.Store, unused)
		}
		return err
	case "succeeded":
	default:
		log.Printf("media projector: thumbs job %s: unknown status %q", job.ID, data.Status)
		return nil
	}
	var results []thumbResult
	if data.Result != nil {
		results = data.Result.Thumbs
	}
	done := make([]bool, len(job.TakeIDs))
	var late []string
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(job.TakeIDs) || done[r.Index] {
			continue
		}
		done[r.Index] = true
		take, tries := job.TakeIDs[r.Index], "thumb_tries + 1"
		switch {
		case r.Key == thumbsPrefix(job)+strconv.Itoa(r.Index)+".webp" && r.Width > 0 && r.Height > 0 && r.Bytes > 0:
			thumb := gallerydto.Poster{Key: r.Key, Width: r.Width, Height: r.Height, Bytes: r.Bytes}
			tag, err := tx.Exec(ctx, `UPDATE takes SET thumb = $2 WHERE id = $1 AND owner_id = $3 AND thumb IS NULL
				AND deleted_at IS NULL`, take, thumb, job.OwnerID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 { // deleted (or given one) meanwhile: nobody points at it
				late = append(late, r.Key)
			}
			continue
		case r.Key == "" && r.Skip:
			tries = strconv.Itoa(ThumbTries) // none needed: never tried again
		case r.Key != "":
			log.Printf("media projector: thumbs job %s: input %d result %q is not its key", job.ID, r.Index, r.Key)
		}
		if _, err := tx.Exec(ctx, `UPDATE takes SET thumb_tries = `+tries+` WHERE id = $1 AND thumb IS NULL`, take); err != nil {
			return err
		}
	}
	var missing []string
	for i, ok := range done {
		if !ok {
			missing = append(missing, job.TakeIDs[i])
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE takes SET thumb_tries = thumb_tries + 1 WHERE id = ANY($1::uuid[]) AND thumb IS NULL`,
		missing); err != nil {
		return err
	}
	deleteKeys(ctx, p.Store, late)
	_, err := tx.Exec(ctx, `UPDATE media_jobs SET status = 'succeeded', finished_at = clock_timestamp(),
		started_at = coalesce(started_at, clock_timestamp()) WHERE id = $1`, job.ID)
	return err
}
