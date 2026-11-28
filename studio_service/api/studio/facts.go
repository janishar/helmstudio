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

// Take facts (contracts/README.md "Take facts", API 1.104; plan 42): a video or audio take gets its ffmpeg facts and
// a video its frame grid from a facts media job, measured on demand (API 1.108; owner, 2026-11-28: "Only on demand"):
// MeasureFacts sends the job the first time the Assistant or helm mcp asks for them, the media projector stores what
// the runner measured (endFacts), and they are kept on the take. The state is takes.facts itself.

// FactsPerJob is the most takes one facts job holds; FactsTries how often a take is tried; FactsMaxSeconds the longest
// take measured (a longer one is never picked: ponytail, raise it when hours-long exports need facts).
const (
	FactsPerJob     = 4
	FactsTries      = 3
	FactsMaxSeconds = 20 * 60
)

// factsReady selects, from takes aliased t, the ready, live video and audio takes still without facts, at most
// FactsMaxSeconds long, whose file is under the owner's prefix.
const factsReady = `t.facts IS NULL AND t.deleted_at IS NULL AND t.ready AND t.facts_tries < 3 -- FactsTries
	AND t.kind IN ('video', 'audio') AND starts_with(t.key, 'u/' || t.owner_id::text || '/')
	AND coalesce((coalesce(t.video, t.audio)->>'duration_s')::float8, 0) <= 1200 -- FactsMaxSeconds
`

// factsOpen holds when take t is in an open facts job.
const factsOpen = `EXISTS (SELECT 1 FROM media_jobs j WHERE j.kind = 'facts' AND j.status IN ('queued', 'running')
	AND t.id = ANY(j.take_ids))`

// FactsWait is how long MeasureFacts waits for the facts (a var so tests can shorten it).
var FactsWait = 20 * time.Second

// factsPick is one take a facts job measures.
type factsPick struct{ ID, Owner, Kind, Key string }

// The plain notes MeasureFacts answers (API 1.108): the Assistant passes them on.
const (
	FactsMeasuringNote = "The facts are being measured; ask again in a minute."
	FactsNoRunnerNote  = "No runner is on to measure the facts; ask again once one is on."
)

// MeasureFacts measures on demand (API 1.108, Take facts) the owner's takes of ids that the Assistant or helm mcp asks
// about without facts: facts jobs (as System) for those factsReady in no open facts job, as few as can hold them
// (FactsPerJob each), then a wait of up to FactsWait while any of ids is in an open facts job. It answers "" when
// nothing is left to wait for, FactsMeasuringNote when one is still measured, and at once FactsNoRunnerNote when one
// needs measuring but no live env can run a facts job (nothing is sent and no try is counted). An error answers "".
func MeasureFacts(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry, owner string, ids []string) string {
	env, live := mediaEnvOf(reg, nil, func(m registry.Media) bool { return m.Facts })
	offline := false
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		if !live {
			return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM takes t WHERE t.id = ANY($1::uuid[]) AND t.owner_id = $2
				AND `+factsReady+`)`, ids, owner).Scan(&offline)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('media:facts'))`); err != nil {
			return err
		}
		picks, err := collect[factsPick](ctx, tx, `SELECT t.id::text, t.owner_id::text, t.kind, t.key FROM takes t
			WHERE t.id = ANY($1::uuid[]) AND t.owner_id = $2 AND `+factsReady+` AND NOT `+factsOpen+`
			ORDER BY t.created_at DESC, t.id FOR UPDATE OF t SKIP LOCKED`, ids, owner)
		for chunk := range slices.Chunk(picks, FactsPerJob) {
			if _, err := sendFacts(ctx, tx, env, chunk); err != nil {
				return err
			}
		}
		return err
	})
	switch {
	case err != nil:
		log.Printf("facts on demand: %v", err)
		return ""
	case offline:
		return FactsNoRunnerNote
	case !live:
		return ""
	}
	deadline := time.Now().Add(FactsWait)
	for {
		var pending bool
		err := common.System(ctx, pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM takes t WHERE t.id = ANY($1::uuid[])
				AND t.owner_id = $2 AND t.facts IS NULL AND `+factsOpen+`)`, ids, owner).Scan(&pending)
		})
		switch {
		case err != nil || !pending:
			return ""
		case time.Now().After(deadline):
			return FactsMeasuringNote
		}
		select {
		case <-ctx.Done():
			return FactsMeasuringNote
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// sendFacts inserts one facts job for picks (one owner's), never sending a key outside its owner's prefix; it returns
// the job id ("" for none).
func sendFacts(ctx context.Context, tx pgx.Tx, env registry.Host, picks []factsPick) (string, error) {
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
			return "", err
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	job := mediaRow{ID: utility.NewID(), OwnerID: picks[0].Owner, Kind: "facts", Host: env.Host, Env: env.Env, TakeIDs: ids}
	_, err := insertMediaJob(ctx, tx, job, mediaCommand{Kind: "facts", Inputs: inputs, OutputPrefix: factsPrefix(job)})
	return job.ID, err
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
	rows, _ := tx.Query(ctx, `SELECT facts->>'key' FROM takes WHERE facts->>'key' = ANY($1)`, keys)
	used, err := pgx.CollectRows(rows, pgx.RowTo[string]) // a string is no struct: never collect[string]
	return slices.DeleteFunc(keys, func(k string) bool { return slices.Contains(used, k) }), err
}
