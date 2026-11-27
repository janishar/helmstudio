package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
)

// Send to Timeline (API 1.82, plan 39 phase G; contracts/README.md "Magic: Send to Timeline"): a board becomes a new
// sequence every time. magic.BuildSend lays it out; its fades, wipes, unhandled dissolves, lane fades and effects
// are bake media jobs (timeline.BuildBake) whose takes fill their slots when the last one is in, in the projector's
// transaction; no bakes, and the sequence is made at once.

const (
	maxSendBakes   = 100 // bake jobs one send may need
	bakeBaseS      = 4.0 // the estimate of one bake: seconds to start, then per frame
	bakePerFrameS  = 0.25
	sendStillBakes = "This board is still sending; wait for it."
)

type sendRow struct {
	ID, OwnerID, BoardID string
	N                    int
	Name                 string
	Revision             int
	Status               string
	Plan                 json.RawMessage
	TimelineID           *string
	Failure              *string
	MadeAt               time.Time
	Bakes, Baked         int
	TimelineGone         bool
}

const sendCols = `s.id::text, s.owner_id::text, s.board_id::text, s.n, s.name, s.revision, s.status, s.plan,
	s.timeline_id::text, s.failure, s.made_at,
	(SELECT count(*) FROM media_jobs j WHERE j.send_id = s.id)::int,
	(SELECT count(*) FROM media_jobs j WHERE j.send_id = s.id AND j.status = 'succeeded')::int,
	(s.timeline_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM timelines t WHERE t.id = s.timeline_id AND t.deleted_at IS NULL))`

func (r sendRow) dto() dto.MagicSend {
	return dto.MagicSend{ID: r.ID, Name: r.Name, Revision: r.Revision, Status: r.Status, TimelineID: r.TimelineID,
		TimelineGone: r.TimelineGone, Bakes: r.Bakes, Baked: r.Baked, Failure: r.Failure, MadeAt: r.MadeAt}
}

// sendPlanned is what a baking send keeps: the sequence's document and the V1 clip each bake job fills.
type sendPlanned struct {
	Doc   timeline.Doc      `json:"doc"`
	Clips map[string]string `json:"clips"` // job id → clip id
}

// timelinesFull is the Timeline's 409 when the caller has MaxTimelines sequences (under the Timeline's own lock).
func timelinesFull(ctx context.Context, tx pgx.Tx, ownerID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('timelines:' || $1::text))`, ownerID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM timelines WHERE owner_id = $1 AND deleted_at IS NULL`, ownerID).Scan(&count); err != nil {
		return err
	} else if count >= MaxTimelines {
		return network.NewConflictError(fmt.Sprintf("You have %d sequences; delete some first.", MaxTimelines), nil)
	}
	return nil
}

// buildSend is board row's document as a sequence that passes the Timeline's rules: its dissolves kept where the
// takes allow, all baked when the rules refuse one.
func buildSend(d magic.Doc, rules map[string]timeline.Take, status map[string]string) (magic.Send, error) {
	for _, keep := range []bool{true, false} {
		out := magic.BuildSend(d, rules, status, keep)
		if _, probs := timeline.Normalize(out.Doc, rules, nil); probs == nil {
			return out, nil
		} else if !keep {
			return out, fmt.Errorf("the send breaks the Timeline's rules: %v", probs)
		}
	}
	return magic.Send{}, nil
}

// bakeEstimate is the seconds the bakes take: the caller's recent bakes per frame (the median of the last 5), else
// a declared rate; always a start-up per bake.
func bakeEstimate(ctx context.Context, tx pgx.Tx, ownerID string, bakes []magic.SendBake) (*float64, error) {
	if len(bakes) == 0 {
		return nil, nil
	}
	type perFrame struct{ S float64 }
	rows, err := collect[perFrame](ctx, tx, `SELECT extract(epoch FROM finished_at - started_at)::float8 / greatest(frames, 1)
		FROM media_jobs WHERE owner_id = $1 AND kind = 'bake' AND status = 'succeeded' AND started_at IS NOT NULL
		AND frames IS NOT NULL ORDER BY created_at DESC LIMIT 5`, ownerID)
	if err != nil {
		return nil, err
	}
	rate := bakePerFrameS
	if len(rows) > 0 {
		var rates []float64
		for _, r := range rows {
			rates = append(rates, r.S)
		}
		rate = median(rates)
	}
	total := 0.0
	for _, b := range bakes {
		total += bakeBaseS + rate*float64(b.Frames)
	}
	total = math.Round(total)
	return &total, nil
}

// SendMagicBoard answers POST …/send: the plan (a dry run), the new sequence (201) or the send baking (202).
func (s *Service) SendMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicSendCreate) (dto.MagicSendResult, int, error) {
	var out dto.MagicSendResult
	code := 200
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if body.Revision != row.Revision {
			return staleBoard(row)
		}
		d := row.doc()
		runs, err := boardRuns(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		status := map[string]string{}
		for k, v := range blockStatuses(d, runs) {
			status[k] = v.Status
		}
		rules, rows, err := loadTakes(ctx, tx, row.OwnerID, magic.TakeIDs(d))
		if err != nil {
			return err
		}
		send, err := buildSend(d, rules, status)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(send.Doc.Tracks, func(t timeline.Track) bool {
			return slices.ContainsFunc(t.Clips, func(c timeline.Clip) bool { return c.Take() != "" })
		}) && len(send.Bakes) == 0 {
			return network.NewUnprocessableError("Nothing to send: add a shot with a take first.", nil)
		}
		if len(send.Bakes) > maxSendBakes {
			return network.NewUnprocessableError(fmt.Sprintf("A send bakes at most %d joins and effects; this one needs %d.",
				maxSendBakes, len(send.Bakes)), nil)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM magic_sends WHERE board_id = $1`, row.ID).Scan(&n); err != nil {
			return err
		}
		name := fmt.Sprintf("%s · send %d", row.Name, n+1)
		if body.Name != nil {
			name = strings.TrimSpace(*body.Name)
		}
		plan := dto.MagicSendPlan{Name: name, Revision: row.Revision, Goes: send.Goes, Joins: send.Joins, Effects: send.Effects,
			Warnings: send.Warnings, Bakes: len(send.Bakes)}
		if plan.BakeSeconds, err = bakeEstimate(ctx, tx, row.OwnerID, send.Bakes); err != nil {
			return err
		}
		env, live := s.mediaEnv(nil)
		if live && len(send.Bakes) > 0 {
			plan.Host = &env.Host
		}
		out.Plan = plan
		if err := timelinesFull(ctx, tx, row.OwnerID); err != nil {
			return err
		}
		var baking bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM magic_sends WHERE board_id = $1 AND status = 'baking'
			AND removed_at IS NULL)`, row.ID).Scan(&baking); err != nil {
			return err
		} else if baking {
			return network.NewConflictError(sendStillBakes, nil)
		}
		if len(send.Bakes) > 0 && !live {
			return network.NewConflictError(noHost, nil)
		}
		if body.DryRun {
			return nil
		}
		sendID := utility.NewID()
		var jobs []mediaRow
		if len(send.Bakes) == 0 {
			norm, _ := timeline.Normalize(send.Doc, rules, nil)
			tl, err := insertTimelineRow(ctx, tx, row.OwnerID, common.ViaOf(caller.Via), name, "user", *norm,
				(*dto.TimelineDefaults)(nil).Filled())
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO magic_sends (id, owner_id, board_id, n, name, revision, status, plan, timeline_id)
				VALUES ($1, $2, $3, $4, $5, $6, 'done', '{}', $7)`, sendID, row.OwnerID, row.ID, n+1, name, row.Revision, tl.ID); err != nil {
				return err
			}
			code = 201
		} else {
			planned := sendPlanned{Doc: send.Doc, Clips: map[string]string{}}
			type queued struct {
				row mediaRow
				cmd mediaCommand
			}
			var q []queued
			for _, bk := range send.Bakes {
				job, cmd, err := bakeJob(row, sendID, send.Doc.Target, bk, rules, rows)
				if err != nil {
					return err
				}
				job.Host, job.Env = env.Host, env.Env
				planned.Clips[job.ID] = bk.Clip
				q = append(q, queued{job, cmd})
			}
			data, _ := json.Marshal(planned)
			if _, err := tx.Exec(ctx, `INSERT INTO magic_sends (id, owner_id, board_id, n, name, revision, status, plan)
				VALUES ($1, $2, $3, $4, $5, $6, 'baking', $7)`, sendID, row.OwnerID, row.ID, n+1, name, row.Revision, data); err != nil {
				return err
			}
			for _, it := range q {
				job, err := insertMediaJob(ctx, tx, it.row, it.cmd)
				if err != nil {
					return err
				}
				jobs = append(jobs, job)
			}
			code = 202
		}
		sr, err := one[sendRow](ctx, tx, `SELECT `+sendCols+` FROM magic_sends s WHERE s.id = $1`, sendID)
		if err != nil {
			return err
		}
		sd := sr.dto()
		for _, j := range jobs {
			sd.Jobs = append(sd.Jobs, j.dto())
		}
		out.Send = &sd
		return nil
	})
	return out, code, err
}

// bakeJob is bake bk of a send as a media job and its command: its sides' takes as inputs (each once), the graph.
func bakeJob(board boardRow, sendID string, target timeline.Target, bk magic.SendBake,
	rules map[string]timeline.Take, rows map[string]gallerymodel.Take) (mediaRow, mediaCommand, error) {
	jobID := utility.NewID()
	var inputs []timeline.MediaInput
	var parents []string
	var sides []timeline.BakeSide
	for _, sd := range bk.Sides {
		take, ok := rows[sd.TakeID]
		if !ok {
			return mediaRow{}, mediaCommand{}, network.NewConflictError("a take the send needs is gone; try again", nil)
		}
		idx := slices.Index(parents, sd.TakeID)
		if idx < 0 {
			idx = len(parents)
			parents = append(parents, sd.TakeID)
			inputs = append(inputs, timeline.MediaInput{Key: take.Key, Kind: take.Kind})
		}
		tk := rules[sd.TakeID]
		sides = append(sides, timeline.BakeSide{Input: idx, Kind: take.Kind, HasAudio: tk.HasAudio, Stream: tk.Stream,
			Start: sd.Start, End: sd.End, Rate: sd.Rate, Pre: sd.Pre, Post: sd.Post, Filters: sd.Filters})
	}
	job, err := timeline.BuildBake(target, bk.Kind, sides, bk.Frames)
	if err != nil {
		return mediaRow{}, mediaCommand{}, err
	}
	title := board.Name + " · " + bk.Title
	ps, _ := json.Marshal(parents)
	frames := bk.Frames
	return mediaRow{ID: jobID, OwnerID: board.OwnerID, Kind: "bake", TimelineName: &title, Parents: ps, Frames: &frames,
			SendID: &sendID},
		mediaCommand{Kind: "bake", Inputs: inputs, OutputPrefix: common.OwnerPrefix(board.OwnerID) + "bakes/" + jobID + "/", Bake: &job},
		nil
}

// storeBakeTake stores a bake's take: source bake, the send's title for it, parents the takes it read.
func storeBakeTake(ctx context.Context, tx pgx.Tx, job mediaRow, id string, r *mediaResult) (string, string, error) {
	prefix := common.OwnerPrefix(job.OwnerID) + "bakes/" + job.ID + "/"
	inside := func(key, suffix string) bool {
		return common.Owns(job.OwnerID, key) && strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
	}
	v := r.Video
	if !inside(r.Key, "bake.mp4") || v == nil || !inside(v.Poster.Key, "bake.poster.png") || v.Last == nil ||
		!inside(v.Last.Key, "bake.last.png") {
		return "", "the result is outside the job's prefix", nil
	}
	thumb := r.Thumb
	if thumb != nil && (thumb.Key != prefix+"bake.thumb.webp" || thumb.Width < 1 || thumb.Height < 1) {
		thumb = nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes,
		etag, video, ready, thumb) VALUES ($1, $2, 'video', 'bake', $3, $4, 'video/mp4', $5, $6, $7, $8, $9, true, $10)`,
		id, job.OwnerID, deref(job.TimelineName), r.Key, r.Width, r.Height, r.Bytes, r.Etag, v, thumb); err != nil {
		return "", "", err
	}
	_, err := tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx)
		SELECT $1, parent::uuid, $2, idx - 1 FROM jsonb_array_elements_text(coalesce($3::jsonb, '[]')) WITH ORDINALITY AS u(parent, idx)
		ON CONFLICT DO NOTHING`, id, job.OwnerID, string(job.Parents))
	return id, "", err
}

// bakeFailed fails the send of a bake that failed or was cancelled (its other bakes are left to end).
func bakeFailed(ctx context.Context, tx pgx.Tx, job mediaRow) error {
	if job.SendID == nil {
		return nil
	}
	why := "a bake was stopped"
	var f struct{ Detail string }
	if json.Unmarshal(job.Failure, &f) == nil && f.Detail != "" {
		why = "a bake failed: " + f.Detail
	}
	_, err := tx.Exec(ctx, `UPDATE magic_sends SET status = 'failed', failure = $2 WHERE id = $1 AND status = 'baking'`,
		*job.SendID, why)
	return err
}

// bakeDone makes a send's sequence once its last bake succeeded: each baked slot takes its take, then the
// sequence (the Timeline's rules and its 100 cap: the send fails with either).
func bakeDone(ctx context.Context, tx pgx.Tx, job mediaRow) error {
	if job.SendID == nil {
		return nil
	}
	sr, err := one[sendRow](ctx, tx, `SELECT `+sendCols+` FROM magic_sends s WHERE s.id = $1 FOR UPDATE OF s`, *job.SendID)
	if errors.Is(err, errNotFound) || err == nil && sr.Status != "baking" {
		return nil
	} else if err != nil {
		return err
	}
	type bake struct {
		ID, Status string
		TakeID     *string
	}
	bakes, err := collect[bake](ctx, tx, `SELECT id::text, status, take_id::text FROM media_jobs WHERE send_id = $1`, sr.ID)
	if err != nil || slices.ContainsFunc(bakes, func(b bake) bool { return b.Status != "succeeded" }) {
		return err // one still open (or ended otherwise: bakeFailed has it)
	}
	fail := func(why string) error {
		_, err := tx.Exec(ctx, `UPDATE magic_sends SET status = 'failed', failure = $2 WHERE id = $1`, sr.ID, why)
		return err
	}
	var planned sendPlanned
	if err := json.Unmarshal(sr.Plan, &planned); err != nil || len(planned.Doc.Tracks) == 0 {
		return fail("the send's plan is unreadable")
	}
	doc := planned.Doc
	var ids []string
	for _, b := range bakes {
		if b.TakeID != nil {
			ids = append(ids, *b.TakeID)
		}
	}
	ids = append(ids, timelineTakeIDs(doc)...)
	rules, _, err := loadTakes(ctx, tx, sr.OwnerID, ids)
	if err != nil {
		return err
	}
	v1 := doc.Tracks[0].Clips
	for _, b := range bakes {
		clip := planned.Clips[b.ID]
		i := slices.IndexFunc(v1, func(c timeline.Clip) bool { return c.ID == clip })
		tk, ok := rules[deref(b.TakeID)]
		if i < 0 || !ok || v1[i].Hold == nil {
			return fail("a baked clip is missing")
		}
		take, hold := *b.TakeID, *v1[i].Hold
		out := min(hold, tk.DurationS)
		zero := 0.0
		v1[i].TakeID, v1[i].In, v1[i].Out, v1[i].Hold = &take, &zero, &out, nil
		if rest := hold - out; rest > 1e-6 { // a bake a frame short (the runner allows one): the time stays
			v1 = slices.Insert(v1, i+1, timeline.Clip{ID: clip + "p", Hold: &rest, Alternatives: []string{}})
		}
	}
	doc.Tracks[0].Clips = v1
	n, probs := timeline.Normalize(doc, rules, nil)
	if probs != nil {
		log.Printf("magic send %s: %v", sr.ID, probs)
		return fail("the sequence breaks the Timeline's rules")
	}
	if err := timelinesFull(ctx, tx, sr.OwnerID); err != nil {
		var api *network.APIError
		if errors.As(err, &api) {
			return fail(api.Message)
		}
		return err
	}
	tl, err := insertTimelineRow(ctx, tx, sr.OwnerID, "web", sr.Name, "user", *n, (*dto.TimelineDefaults)(nil).Filled())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE magic_sends SET status = 'done', timeline_id = $2 WHERE id = $1`, sr.ID, tl.ID)
	return err
}

// timelineTakeIDs is every take a written document names.
func timelineTakeIDs(d timeline.Doc) []string {
	var out []string
	for _, t := range d.Tracks {
		for _, c := range t.Clips {
			if id := c.Take(); id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// ListMagicSends is the board's listed sends, newest first.
func (s *Service) ListMagicSends(ctx context.Context, caller common.Caller, id string) (dto.MagicSendList, error) {
	out := dto.MagicSendList{Sends: []dto.MagicSend{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, false)
		if err != nil {
			return err
		}
		rows, err := collect[sendRow](ctx, tx, `SELECT `+sendCols+` FROM magic_sends s WHERE s.board_id = $1
			AND s.removed_at IS NULL ORDER BY s.made_at DESC, s.n DESC`, row.ID)
		for _, r := range rows {
			out.Sends = append(out.Sends, r.dto())
		}
		return err
	})
	return out, err
}

// DeleteMagicSend takes a send off the list (its sequence stays); one still baking stops, its jobs cancelled.
func (s *Service) DeleteMagicSend(ctx context.Context, caller common.Caller, id, sendID string) error {
	if !utility.IsUUID(sendID) {
		return errNotFound
	}
	var open []string
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE magic_sends SET removed_at = clock_timestamp(),
			status = CASE WHEN status = 'baking' THEN 'failed' ELSE status END,
			failure = CASE WHEN status = 'baking' THEN 'removed from the list' ELSE failure END
			WHERE id = $1 AND board_id = $2 AND removed_at IS NULL`, sendID, row.ID)
		if err != nil {
			return err
		} else if tag.RowsAffected() == 0 {
			return errNotFound
		}
		open, err = collect[string](ctx, tx, `SELECT id::text FROM media_jobs WHERE send_id = $1 AND status IN ('queued', 'running')`, sendID)
		return err
	})
	for _, job := range open {
		if _, err := s.CancelMediaJob(ctx, caller, job); err != nil {
			log.Printf("magic send %s: cancel bake %s: %v", sendID, job, err) // the sweeper ends it if its runner is gone
		}
	}
	return err
}
