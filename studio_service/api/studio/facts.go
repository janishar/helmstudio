package studio

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Take facts (contracts/README.md "Take facts", API 1.104; plan 42): every video and audio take gets its ffmpeg facts
// and a video its frame grid from a facts media job, as thumbnails do: QueueFacts picks them, the media projector
// stores what the runner measured (endFacts). The state is takes.facts itself.

// FactsPerJob is the most takes one facts job holds; FactsTries how often a take is tried; FactsMaxSeconds the longest
// take measured (a longer one is never picked: ponytail, raise it when hours-long exports need facts).
const (
	FactsPerJob     = 4
	FactsTries      = 3
	FactsMaxSeconds = 20 * 60
)

// factsTodo selects, from takes aliased t, the ready, live video and audio takes still without facts, at most
// FactsMaxSeconds long, whose file is under the owner's prefix.
const factsTodo = `t.facts IS NULL AND t.deleted_at IS NULL AND t.ready AND t.facts_tries < 3 -- FactsTries
	AND t.kind IN ('video', 'audio') AND starts_with(t.key, 'u/' || t.owner_id::text || '/')
	AND coalesce((coalesce(t.video, t.audio)->>'duration_s')::float8, 0) <= 1200 -- FactsMaxSeconds
`

// QueueFacts sends one facts job (as System) for the newest takes without facts, of one owner, at most FactsPerJob,
// unless one is open already or no live env can run it; it returns the job id ("" for none).
func QueueFacts(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry) (string, error) {
	env, ok := mediaEnvOf(reg, nil, func(m registry.Media) bool { return m.Facts })
	if !ok {
		return "", nil
	}
	id := ""
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('media:facts'))`); err != nil {
			return err
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_jobs WHERE kind = 'facts'
			AND status IN ('queued', 'running'))`).Scan(&open); err != nil || open {
			return err
		}
		type pick struct{ ID, Owner, Kind, Key string }
		picks, err := collect[pick](ctx, tx, `WITH o AS (SELECT t.owner_id FROM takes t WHERE `+factsTodo+`
				ORDER BY t.created_at DESC, t.id LIMIT 1)
			SELECT t.id::text, t.owner_id::text, t.kind, t.key FROM takes t JOIN o ON o.owner_id = t.owner_id
			WHERE `+factsTodo+` ORDER BY t.created_at DESC, t.id LIMIT $1 FOR UPDATE OF t SKIP LOCKED`, FactsPerJob)
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
			ids, inputs = append(ids, p.ID), append(inputs, timeline.MediaInput{Key: p.Key, Kind: p.Kind})
		}
		if len(bad) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE takes SET facts_tries = $2 WHERE id = ANY($1::uuid[])`, bad, FactsTries); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		job := mediaRow{ID: utility.NewID(), OwnerID: picks[0].Owner, Kind: "facts", Host: env.Host, Env: env.Env, TakeIDs: ids}
		_, err = insertMediaJob(ctx, tx, job, mediaCommand{Kind: "facts", Inputs: inputs, OutputPrefix: factsPrefix(job)})
		id = job.ID
		return err
	})
	if err != nil {
		id = ""
	}
	return id, err
}

// RunFacts queues facts jobs every ThumbsTick until ctx ends.
func RunFacts(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry) {
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
		if _, err := QueueFacts(ctx, pool, reg); err != nil && ctx.Err() == nil {
			log.Printf("facts: %v", err)
		}
	}
}

// factsResult is one item of a facts job's result.facts (evt.media.status, API 1.104).
type factsResult struct {
	Index  int             `json:"index"`
	Facts  json.RawMessage `json:"facts"`
	Key    string          `json:"key"`
	Width  int             `json:"width"`
	Height int             `json:"height"`
	Bytes  int64           `json:"bytes"`
	At     []float64       `json:"at"`
	Error  *string         `json:"error"`
}

// takeFacts is takes.facts: the facts, and a video's grid (key null for audio).
type takeFacts struct {
	Facts  json.RawMessage `json:"facts"`
	Key    *string         `json:"key"`
	Width  int             `json:"width,omitempty"`
	Height int             `json:"height,omitempty"`
	Bytes  int64           `json:"bytes,omitempty"`
	At     []float64       `json:"at"`
}

// endFacts applies a facts job's terminal status: succeeded stores each result on its live take still without facts
// (a grid only at exactly <prefix><i>.jpg, else the object is deleted); an error or a missing result adds a try; failed
// or cancelled adds a try to every take (endMedia) and deletes the grids no take points at.
func (p *Projector) endFacts(ctx context.Context, tx pgx.Tx, job mediaRow, data mediaStatus) error {
	switch data.Status {
	case "failed", "cancelled":
		var failure any
		if data.Status == "failed" {
			failure = map[string]string{"class": "internal", "detail": "the facts job failed"}
		}
		if err := endMedia(ctx, tx, &job, data.Status, failure); err != nil {
			return err
		}
		unused, err := unusedFacts(ctx, tx, job)
		if err == nil {
			deleteKeys(ctx, p.Store, unused)
		}
		return err
	case "succeeded":
	default:
		log.Printf("media projector: facts job %s: unknown status %q", job.ID, data.Status)
		return nil
	}
	var results []factsResult
	if data.Result != nil {
		results = data.Result.Facts
	}
	done := make([]bool, len(job.TakeIDs))
	var late []string
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(job.TakeIDs) || done[r.Index] {
			continue
		}
		done[r.Index] = true
		take, want := job.TakeIDs[r.Index], factsPrefix(job)+strconv.Itoa(r.Index)+".jpg"
		if r.Key != "" && r.Key != want {
			log.Printf("media projector: facts job %s: input %d result %q is not its key", job.ID, r.Index, r.Key)
			r.Error = new(string)
		}
		if r.Error != nil || len(r.Facts) == 0 {
			if _, err := tx.Exec(ctx, `UPDATE takes SET facts_tries = facts_tries + 1 WHERE id = $1 AND facts IS NULL`, take); err != nil {
				return err
			}
			continue
		}
		stored := takeFacts{Facts: r.Facts, At: r.At}
		if stored.At == nil {
			stored.At = []float64{}
		}
		if r.Key != "" {
			stored.Key, stored.Width, stored.Height, stored.Bytes = &r.Key, r.Width, r.Height, r.Bytes
		}
		tag, err := tx.Exec(ctx, `UPDATE takes SET facts = $2 WHERE id = $1 AND owner_id = $3 AND facts IS NULL
			AND deleted_at IS NULL`, take, stored, job.OwnerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 && r.Key != "" { // deleted (or measured) meanwhile: nobody points at it
			late = append(late, r.Key)
		}
	}
	var missing []string
	for i, ok := range done {
		if !ok {
			missing = append(missing, job.TakeIDs[i])
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE takes SET facts_tries = facts_tries + 1 WHERE id = ANY($1::uuid[]) AND facts IS NULL`,
		missing); err != nil {
		return err
	}
	deleteKeys(ctx, p.Store, late)
	_, err := tx.Exec(ctx, `UPDATE media_jobs SET status = 'succeeded', finished_at = clock_timestamp(),
		started_at = coalesce(started_at, clock_timestamp()) WHERE id = $1`, job.ID)
	return err
}

// factsPrefix is a facts job's output_prefix.
func factsPrefix(job mediaRow) string {
	return common.OwnerPrefix(job.OwnerID) + "facts/" + job.ID + "/"
}

// unusedFacts are the grids a facts job could have written (<prefix><i>.jpg per take) that no take points at.
func unusedFacts(ctx context.Context, tx pgx.Tx, job mediaRow) ([]string, error) {
	keys := make([]string, len(job.TakeIDs))
	for i := range job.TakeIDs {
		keys[i] = factsPrefix(job) + strconv.Itoa(i) + ".jpg"
	}
	used, err := collect[string](ctx, tx, `SELECT facts->>'key' FROM takes WHERE facts->>'key' = ANY($1)`, keys)
	return slices.DeleteFunc(keys, func(k string) bool { return slices.Contains(used, k) }), err
}
