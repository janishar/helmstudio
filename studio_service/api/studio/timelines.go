package studio

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
)

// Sequences (contracts/README.md "Timeline", API 1.17): a document normalized by the timeline rules on every write,
// stored as revisions (the last 100) with the undo and redo pointers, and the shot index that lets the projector fill a
// slot when its run (or re-render step) makes a take.

// MaxTimelines is how many sequences one user may have.
const MaxTimelines = 100

// keptRevisions is how many revisions of a sequence are kept.
const keptRevisions = 100

type timelineRow struct {
	ID, OwnerID, Name    string
	Doc, Defaults        json.RawMessage
	Revision             int
	SessionID            *string
	CreatedAt, UpdatedAt time.Time
}

const timelineCols = `id::text, owner_id::text, name, doc, defaults, revision, session_id::text, created_at, updated_at`

func (r timelineRow) doc() timeline.Doc {
	var d timeline.Doc
	_ = json.Unmarshal(r.Doc, &d)
	return d
}

// readTimeline reads one live sequence (lock: FOR UPDATE); unknown, malformed, deleted or another user's is 404.
func readTimeline(ctx context.Context, tx pgx.Tx, id string, lock bool) (timelineRow, error) {
	if !utility.IsUUID(id) {
		return timelineRow{}, errNotFound
	}
	q := `SELECT ` + timelineCols + ` FROM timelines WHERE id = $1 AND deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE`
	}
	return one[timelineRow](ctx, tx, q, id)
}

// TimelineError is a 422 with every broken rule.
type TimelineError struct {
	Detail   string                `json:"detail"`
	Problems []dto.TimelineProblem `json:"problems"`
}

func (e *TimelineError) Error() string { return e.Detail }

var problemText = map[string]string{
	"too_large":        fmt.Sprintf("the sequence is over %d bytes", timeline.MaxBytes),
	"track_layout":     "a sequence has one video track first, then at most 8 audio tracks",
	"too_many_clips":   fmt.Sprintf("a sequence has at most %d clips", timeline.MaxClips),
	"duplicate_clip":   "its id is used twice",
	"take_not_found":   "a take it names is not found",
	"shot_invalid":     "only studio sets a shot",
	"out_required":     "an offline clip needs out",
	"trim":             "in and out must be inside its take",
	"hold_on_video":    "a video take has no hold",
	"image_trim":       "an image or an empty slot has no in or out",
	"hold_required":    "an image or an empty slot needs hold",
	"hold":             "hold is under one frame",
	"wrong_track":      "an image or an empty slot cannot go on an audio track",
	"no_audio":         "its take has no sound",
	"at_required":      "an audio clip needs at",
	"overlap":          "it overlaps the clip before it",
	"past_end":         "it ends after the sequence",
	"dissolve_track":   "a dissolve goes on V1 only",
	"dissolve_first":   "the first clip cannot dissolve in",
	"dissolve_gap":     "a dissolve needs a take on both sides of the cut",
	"dissolve_handles": "the takes have too little handle for the dissolve",
	"dissolve_length":  "its dissolves are longer than the clip",
}

// problemsError maps the rules' problems to the 422.
func problemsError(probs []timeline.Problem) error {
	out := &TimelineError{Problems: []dto.TimelineProblem{}}
	for _, p := range probs {
		text := problemText[p.Code]
		if text == "" {
			text = p.Code
		}
		q := dto.TimelineProblem{Code: p.Code, Message: text}
		if p.Track != "" {
			q.Track = &p.Track
			q.Message = p.Track + ": " + text
		}
		if p.Clip != "" {
			q.Clip = &p.Clip
			q.Message = "clip " + p.Clip + ": " + text
		}
		out.Problems = append(out.Problems, q)
	}
	out.Detail = fmt.Sprintf("timeline has %d problem%s: %s", len(out.Problems), map[bool]string{true: "s"}[len(out.Problems) > 1], out.Problems[0].Message)
	return out
}

// editError is an edit studio applies that cannot apply.
func editError(code string, op timeline.Op) error {
	switch code {
	case "unknown_track":
		return network.NewUnprocessableError("unknown track "+op.Track, nil)
	case "unknown_clip":
		return network.NewUnprocessableError("unknown clip "+op.Clip, nil)
	}
	return network.NewUnprocessableError(code, nil)
}

// ------------------------------------------------------------------ takes

// docTakeIDs is every take a document names (clips and alternatives), once each, in order.
func docTakeIDs(docs ...timeline.Doc) []string {
	var out []string
	for _, d := range docs {
		for _, t := range d.Tracks {
			for _, c := range t.Clips {
				for _, id := range append([]string{c.Take()}, c.Alternatives...) {
					if id != "" && utility.IsUUID(id) && !slices.Contains(out, id) {
						out = append(out, id)
					}
				}
			}
		}
	}
	return out
}

// loadTakes reads owner's ready, undeleted takes among ids: what the rules know of each, and the rows.
func loadTakes(ctx context.Context, tx pgx.Tx, ownerID string, ids []string) (map[string]timeline.Take, map[string]gallerymodel.Take, error) {
	rules, rows := map[string]timeline.Take{}, map[string]gallerymodel.Take{}
	if len(ids) == 0 {
		return rules, rows, nil
	}
	list, err := collect[gallerymodel.Take](ctx, tx, `SELECT `+gallerymodel.TakeCols+` FROM takes t
		WHERE t.id = ANY($1::uuid[]) AND t.owner_id = $2 AND t.deleted_at IS NULL AND t.ready`, ids, ownerID)
	for _, row := range list {
		rows[row.ID], rules[row.ID] = row, ruleTake(row)
	}
	return rules, rows, err
}

// ruleTake is what the rules know of a take.
func ruleTake(row gallerymodel.Take) timeline.Take {
	out := timeline.Take{Kind: row.Kind, Bytes: row.Bytes}
	var video *gallerydto.Video
	if json.Unmarshal(row.Video, &video) == nil && video != nil {
		out.DurationS, out.HasAudio = video.DurationS, video.HasAudio
		if len(video.Stream) > 0 && string(video.Stream) != "null" {
			_ = json.Unmarshal(video.Stream, &out.Stream)
		}
	}
	var made *gallerydto.MadeWith
	if json.Unmarshal(row.MadeWith, &made) == nil && made != nil {
		inputs := maps.Clone(made.Inputs)
		if inputs == nil {
			inputs = map[string]any{}
		}
		maps.Copy(inputs, made.Typed) // the reuse view: the prompt as typed
		out.MadeWith = &timeline.MadeWith{WorkflowID: made.WorkflowID, Inputs: inputs}
	}
	_ = json.Unmarshal(row.FrameOf, &out.FrameOf)
	return out
}

// timelineTake is a take as the answer signs it.
func (s *Service) timelineTake(row gallerymodel.Take) dto.TimelineTake {
	t := gallery.TakeDTO(s.signer, row)
	out := dto.TimelineTake{ID: t.ID, Kind: t.Kind, Source: t.Source, Title: t.Title, URL: t.URL, ThumbURL: t.ThumbURL,
		Width: t.Width, Height: t.Height, Video: t.Video}
	if m := t.MadeWith; m != nil {
		out.Seed, out.WorkflowID, out.WorkflowTitle = m.Seed, &m.WorkflowID, &m.WorkflowTitle
	}
	return out
}

// ------------------------------------------------------------------ the answer

// clean is a document as stored: no answer fields (offline, a shot's status).
func clean(d timeline.Doc) timeline.Doc {
	d = d.Clone()
	for ti := range d.Tracks {
		for ci := range d.Tracks[ti].Clips {
			c := &d.Tracks[ti].Clips[ci]
			c.Offline = false
			if c.Shot != nil {
				ref := c.Shot.Ref()
				c.Shot = &ref
			}
		}
	}
	return d
}

// shotState is where a slot's generation is: queued, running, succeeded (with its take), failed (with the error) or
// cancelled. A re-render step's is its newest run's; with no run yet, queued while the batch is open.
func shotState(ctx context.Context, tx pgx.Tx, ownerID string, ref timeline.ShotRef) (string, *string, string, error) {
	var run model.Run
	var err error
	if ref.RunID != "" {
		run, err = one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 AND owner_id = $2`, ref.RunID, ownerID)
		if errors.Is(err, errNotFound) {
			return "cancelled", nil, "", nil
		}
	} else {
		var runs []model.Run
		runs, err = collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE batch_id = $1 AND owner_id = $2
			ORDER BY created_at DESC, id`, ref.BatchID, ownerID)
		i := slices.IndexFunc(runs, func(r model.Run) bool {
			key, ok := runKey(r)
			return ok && key.Step == ref.Step && key.Turn < 0
		})
		if err == nil && i < 0 {
			var status string
			err = tx.QueryRow(ctx, `SELECT status FROM batches WHERE id = $1`, ref.BatchID).Scan(&status)
			if errors.Is(err, pgx.ErrNoRows) || slices.Contains([]string{"succeeded", "failed", "cancelled", "stopped"}, status) {
				return "cancelled", nil, "", nil
			}
			return "queued", nil, "", err
		}
		if i >= 0 {
			run = runs[i]
		}
	}
	if err != nil {
		return "", nil, "", err
	}
	if run.Status != "succeeded" {
		return run.Status, run.Error, "", nil
	}
	var take *string
	err = tx.QueryRow(ctx, `SELECT take_id::text FROM run_outputs WHERE run_id = $1 ORDER BY idx LIMIT 1`, run.ID).Scan(&take)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && take == nil) {
		text := "the run made no take"
		return "failed", &text, "", nil
	}
	return "succeeded", nil, *take, err
}

// runKey is a batch run's parsed pipeline key.
func runKey(run model.Run) (pipelineKey, bool) {
	if run.Key == nil || len(strings.Split(*run.Key, ":")) != 6 {
		return pipelineKey{}, false
	}
	return parsePipelineKey(*run.Key)
}

// pointers are a revision's undo_to and redo_to.
func pointers(ctx context.Context, tx pgx.Tx, id string, revision int) (*int, *int, error) {
	var undo, redo *int
	err := tx.QueryRow(ctx, `SELECT undo_to, redo_to FROM timeline_revisions WHERE timeline_id = $1 AND revision = $2`,
		id, revision).Scan(&undo, &redo)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return undo, redo, err
}

// kept reports whether revision p is still kept.
func kept(ctx context.Context, tx pgx.Tx, id string, p *int) (bool, error) {
	if p == nil {
		return false, nil
	}
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timeline_revisions WHERE timeline_id = $1 AND revision = $2)`, id, *p).Scan(&found)
	return found, err
}

// timelineDTO is the sequence as answered: normalized against the takes now (offline marked), each shot's status, its
// takes signed, its defaults without deleted items, whether undo and redo can go.
func (s *Service) timelineDTO(ctx context.Context, tx pgx.Tx, row timelineRow) (dto.Timeline, error) {
	doc := row.doc()
	rules, rows, err := loadTakes(ctx, tx, row.OwnerID, docTakeIDs(doc))
	if err != nil {
		return dto.Timeline{}, err
	}
	if n, probs := timeline.Normalize(doc, rules, &doc); probs == nil {
		doc = *n
	}
	for ti := range doc.Tracks {
		for ci := range doc.Tracks[ti].Clips {
			c := &doc.Tracks[ti].Clips[ci]
			if c.Shot == nil {
				continue
			}
			status, failure, _, err := shotState(ctx, tx, row.OwnerID, *c.Shot)
			if err != nil {
				return dto.Timeline{}, err
			}
			switch status {
			case "succeeded", "cancelled": // the projector fills or drops it; until then it shows as the run left it
				text := "the generation ended without filling this slot"
				status, failure = "failed", &text
			case "failed":
				if failure == nil {
					text := "the run failed"
					failure = &text
				}
			}
			c.Shot.Status, c.Shot.Error = status, failure
		}
	}
	out := dto.Timeline{ID: row.ID, Name: row.Name, SessionID: row.SessionID, Revision: row.Revision, Target: doc.Target,
		Tracks: doc.Tracks, DurationS: doc.DurationS, Frames: doc.Frames, Takes: map[string]dto.TimelineTake{},
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	for id, take := range rows {
		out.Takes[id] = s.timelineTake(take)
	}
	if out.Defaults, err = s.liveDefaults(ctx, tx, row); err != nil {
		return out, err
	}
	undo, redo, err := pointers(ctx, tx, row.ID, row.Revision)
	if err == nil {
		out.CanUndo, err = kept(ctx, tx, row.ID, undo)
	}
	if err == nil {
		out.CanRedo, err = kept(ctx, tx, row.ID, redo)
	}
	return out, err
}

// liveDefaults is the stored defaults without an element or look that is gone (the library resolver decides looks).
func (s *Service) liveDefaults(ctx context.Context, tx pgx.Tx, row timelineRow) (dto.TimelineDefaults, error) {
	var d dto.TimelineDefaults
	_ = json.Unmarshal(row.Defaults, &d)
	out := d.Filled()
	if len(out.Cast) > 0 {
		rows, _ := tx.Query(ctx, `SELECT id::text FROM library_items WHERE owner_id = $1 AND kind = 'element'
			AND id = ANY($2::uuid[])`, row.OwnerID, out.Cast)
		live, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return out, err
		}
		out.Cast = slices.DeleteFunc(out.Cast, func(id string) bool { return !slices.Contains(live, id) })
	}
	if len(out.Looks) > 0 {
		looks, err := readLooks(ctx, tx, s.registry, row.OwnerID, out.Looks, nil)
		if err != nil {
			return out, err
		}
		out.Looks = slices.DeleteFunc(out.Looks, func(id string) bool {
			return !slices.ContainsFunc(looks, func(l lookItem) bool { return l.ID == id })
		})
	}
	return out, nil
}

// ------------------------------------------------------------------ storing

// store writes doc as the next revision with its undo and redo pointers and prunes the revisions past the last 100.
func store(ctx context.Context, tx pgx.Tx, row *timelineRow, doc timeline.Doc, by string, undo, redo *int) error {
	data, err := json.Marshal(clean(doc))
	if err != nil {
		return err
	}
	next := row.Revision + 1
	updated, err := one[timelineRow](ctx, tx, `UPDATE timelines SET doc = $2, revision = $3, updated_at = clock_timestamp()
		WHERE id = $1 RETURNING `+timelineCols, row.ID, data, next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO timeline_revisions (timeline_id, owner_id, revision, doc, by, undo_to, redo_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, row.ID, row.OwnerID, next, data, by, undo, redo); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM timeline_revisions WHERE timeline_id = $1 AND revision <= $2`, row.ID, next-keptRevisions)
	*row = updated
	return err
}

// storeUser is a user's write: undo goes back to the revision before, redo is cleared.
func storeUser(ctx context.Context, tx pgx.Tx, row *timelineRow, doc timeline.Doc) error {
	before := row.Revision
	return store(ctx, tx, row, doc, "user", &before, nil)
}

// storeStudio is studio's own write (a fill, a cancelled shot dropped): it keeps the pointers, so undo skips it.
func storeStudio(ctx context.Context, tx pgx.Tx, row *timelineRow, doc timeline.Doc) error {
	undo, redo, err := pointers(ctx, tx, row.ID, row.Revision)
	if err != nil {
		return err
	}
	return store(ctx, tx, row, doc, "studio", undo, redo)
}

// newClipID is a clip id not in doc.
func newClipID(doc timeline.Doc, taken ...string) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	used := map[string]bool{}
	for _, t := range doc.Tracks {
		for _, c := range t.Clips {
			used[c.ID] = true
		}
	}
	for _, id := range taken {
		used[id] = true
	}
	for {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
		if !used[string(b)] {
			return string(b)
		}
	}
}

// addOp is the add edit of takeIDs to track, each with a new clip id.
func addOp(doc timeline.Doc, track string, takeIDs []string) timeline.Op {
	op := timeline.Op{Op: "add", Track: track, TakeIDs: takeIDs}
	for range takeIDs {
		op.IDs = append(op.IDs, newClipID(doc, op.IDs...))
	}
	return op
}

// checkAdd refuses takes that cannot go on track, by position.
func checkAdd(doc timeline.Doc, track string, takeIDs []string, takes map[string]timeline.Take) error {
	if !slices.ContainsFunc(doc.Tracks, func(t timeline.Track) bool { return t.Name == track }) {
		return network.NewUnprocessableError("unknown track "+track, nil)
	}
	for i, id := range takeIDs {
		tk, ok := takes[id]
		switch {
		case !ok:
			return network.NewUnprocessableError(fmt.Sprintf("take_ids[%d]: take %s not found", i, id), nil)
		case track != "V1" && tk.Kind == "image":
			return network.NewUnprocessableError(fmt.Sprintf("take_ids[%d]: an image cannot go on an audio track", i), nil)
		case track != "V1" && !tk.HasAudio:
			return network.NewUnprocessableError(fmt.Sprintf("take_ids[%d]: take %s has no sound", i, id), nil)
		}
	}
	return nil
}

// apply runs an edit studio makes and normalizes it, or the 422.
func apply(doc timeline.Doc, op timeline.Op, takes map[string]timeline.Take) (timeline.Doc, error) {
	r := timeline.Apply(doc, op, takes)
	switch {
	case r.Error != "":
		return doc, editError(r.Error, op)
	case len(r.Problems) > 0:
		return doc, problemsError(r.Problems)
	}
	return *r.Document, nil
}

// ------------------------------------------------------------------ sequences

// CreateTimeline makes a sequence (revision 1) and its own session, its target from the first take where not given.
func (s *Service) CreateTimeline(ctx context.Context, caller common.Caller, body *dto.TimelineCreate) (dto.Timeline, error) {
	var out dto.Timeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM timelines WHERE deleted_at IS NULL`).Scan(&count); err != nil {
			return err
		} else if count >= MaxTimelines {
			return network.NewConflictError(fmt.Sprintf("You have %d sequences; delete some first.", MaxTimelines), nil)
		}
		rules, rows, err := loadTakes(ctx, tx, caller.ID, body.TakeIDs)
		if err != nil {
			return err
		}
		for i, id := range body.TakeIDs {
			if _, ok := rules[id]; !ok {
				return network.NewUnprocessableError(fmt.Sprintf("take_ids[%d]: take %s not found", i, id), nil)
			}
		}
		target := timeline.Target{Width: 1280, Height: 720, FPS: 24, SampleRate: 48000}
		if len(body.TakeIDs) > 0 {
			target = firstTarget(rows[body.TakeIDs[0]])
		}
		if t := body.Target; t != nil {
			target.Width, target.Height = orElse(t.Width, target.Width), orElse(t.Height, target.Height)
			target.FPS, target.SampleRate = orElse(t.FPS, target.FPS), orElse(t.SampleRate, target.SampleRate)
		}
		doc := timeline.Doc{Target: target, Tracks: []timeline.Track{{Kind: "video", Clips: []timeline.Clip{}}}}
		n, probs := timeline.Normalize(doc, rules, nil)
		if probs != nil {
			return problemsError(probs)
		}
		doc = *n
		if len(body.TakeIDs) > 0 {
			if doc, err = apply(doc, addOp(doc, "V1", body.TakeIDs), rules); err != nil {
				return err
			}
		}
		name := body.NameOrDefault()
		sessionID := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)`, sessionID, caller.ID, name); err != nil {
			return err
		}
		data, _ := json.Marshal(clean(doc))
		row, err := one[timelineRow](ctx, tx, `INSERT INTO timelines (id, owner_id, name, doc, defaults, session_id)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+timelineCols, utility.NewID(), caller.ID, name, data,
			body.Defaults.Filled(), sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO timeline_revisions (timeline_id, owner_id, revision, doc, by)
			VALUES ($1, $2, 1, $3, 'user')`, row.ID, caller.ID, data); err != nil {
			return err
		}
		out, err = s.timelineDTO(ctx, tx, row)
		return err
	})
	return out, err
}

func orElse[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// firstTarget is the target a first take gives: its size rounded down to even (at least 16), its video rate when in
// the table (else 24), 48000 Hz.
func firstTarget(row gallerymodel.Take) timeline.Target {
	size := func(p *int, fallback int) int {
		if p == nil {
			return fallback
		}
		return min(max(*p/2*2, 16), 4096)
	}
	t := timeline.Target{Width: size(row.Width, 1280), Height: size(row.Height, 720), FPS: 24, SampleRate: 48000}
	var video *gallerydto.Video
	if json.Unmarshal(row.Video, &video) == nil && video != nil {
		if _, _, ok := timeline.Rate(video.FPS); ok {
			t.FPS = video.FPS
		}
	}
	return t
}

// ListTimelines is the caller's sequences, newest first.
func (s *Service) ListTimelines(ctx context.Context, caller common.Caller) (dto.TimelineList, error) {
	out := dto.TimelineList{Timelines: []dto.TimelineSummary{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[timelineRow](ctx, tx, `SELECT `+timelineCols+` FROM timelines WHERE deleted_at IS NULL
			ORDER BY updated_at DESC, id LIMIT 100`)
		if err != nil {
			return err
		}
		var firsts []string
		docs := make([]timeline.Doc, len(rows))
		for i, row := range rows {
			docs[i] = row.doc()
			if v1 := docs[i].Tracks[0].Clips; len(v1) > 0 && v1[0].Take() != "" {
				firsts = append(firsts, v1[0].Take())
			}
		}
		type thumb struct{ ID, Key string }
		thumbs, err := collect[thumb](ctx, tx, `SELECT t.id::text, `+gallerymodel.ThumbKey+` FROM takes t
			WHERE t.id = ANY($1::uuid[]) AND t.deleted_at IS NULL AND t.ready`, firsts)
		if err != nil {
			return err
		}
		for i, row := range rows {
			d := docs[i]
			item := dto.TimelineSummary{ID: row.ID, Name: row.Name, Target: d.Target, Revision: row.Revision,
				DurationS: d.DurationS, Clips: len(d.Tracks[0].Clips), UpdatedAt: row.UpdatedAt}
			if v1 := d.Tracks[0].Clips; len(v1) > 0 {
				if k := slices.IndexFunc(thumbs, func(t thumb) bool { return t.ID == v1[0].Take() }); k >= 0 {
					link := s.signer.Sign(thumbs[k].Key)
					item.ThumbURL = &link
				}
			}
			out.Timelines = append(out.Timelines, item)
		}
		return nil
	})
	return out, err
}

// GetTimeline is one sequence at its current revision.
func (s *Service) GetTimeline(ctx context.Context, caller common.Caller, id string) (dto.Timeline, error) {
	var out dto.Timeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, false)
		if err == nil {
			out, err = s.timelineDTO(ctx, tx, row)
		}
		return err
	})
	return out, err
}

// staleRevision is the 409 of an edit made on another revision.
func staleRevision(row timelineRow) error {
	return network.NewConflictError(fmt.Sprintf("timeline changed since you opened it (now revision %d)", row.Revision), nil)
}

// editTimeline runs fn on the locked sequence and answers it as it is then.
func (s *Service) editTimeline(ctx context.Context, caller common.Caller, id string, fn func(tx pgx.Tx, row *timelineRow) error) (dto.Timeline, error) {
	var out dto.Timeline
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := fn(tx, &row); err != nil {
			return err
		}
		out, err = s.timelineDTO(ctx, tx, row)
		return err
	})
	return out, err
}

// UpdateTimeline is an edit: the whole document at its revision, normalized and stored as the next.
func (s *Service) UpdateTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineUpdate) (dto.Timeline, error) {
	return s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		if body.Revision != row.Revision {
			return staleRevision(*row)
		}
		doc, previous := timeline.Doc{Target: *body.Target, Tracks: body.Tracks}, row.doc()
		rules, _, err := loadTakes(ctx, tx, caller.ID, docTakeIDs(doc))
		if err != nil {
			return err
		}
		n, probs := timeline.Normalize(doc, rules, &previous)
		if probs != nil {
			return problemsError(probs)
		}
		return storeUser(ctx, tx, row, *n)
	})
}

// PatchTimeline renames the sequence or sets its defaults (no revision).
func (s *Service) PatchTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelinePatch) (dto.Timeline, error) {
	return s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		name, defaults := row.Name, row.Defaults
		if body.Name != nil {
			name = (&dto.TimelineCreate{Name: body.Name}).NameOrDefault()
		}
		if body.Defaults != nil {
			defaults, _ = json.Marshal(body.Defaults.Filled())
		}
		updated, err := one[timelineRow](ctx, tx, `UPDATE timelines SET name = $2, defaults = $3, updated_at = clock_timestamp()
			WHERE id = $1 RETURNING `+timelineCols, row.ID, name, defaults)
		*row = updated
		return err
	})
}

// DeleteTimeline soft-deletes the sequence and cancels its unfinished exports (their runners are asked to stop).
func (s *Service) DeleteTimeline(ctx context.Context, caller common.Caller, id string) error {
	var jobs []string
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readTimeline(ctx, tx, id, true); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE timelines SET deleted_at = clock_timestamp() WHERE id = $1`, id); err != nil {
			return err
		}
		rows, _ := tx.Query(ctx, `UPDATE media_jobs SET status = 'cancelled', finished_at = clock_timestamp()
			WHERE timeline_id = $1 AND status IN ('queued', 'running') RETURNING id::text`, id)
		var err error
		jobs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	for _, job := range jobs {
		if err := s.cancel(ctx, job); err != nil {
			log.Printf("timelines: cancel export %s: %v", job, err) // ended here; the runner's events are ignored
		}
	}
	return err
}

// AddTimelineClips is "Add to timeline": the add edit applied to the current revision.
func (s *Service) AddTimelineClips(ctx context.Context, caller common.Caller, id string, body *dto.TimelineAddClips) (dto.Timeline, error) {
	return s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		doc := row.doc()
		rules, _, err := loadTakes(ctx, tx, caller.ID, append(docTakeIDs(doc), body.TakeIDs...))
		if err != nil {
			return err
		}
		if err := checkAdd(doc, body.Track, body.TakeIDs, rules); err != nil {
			return err
		}
		if doc, err = apply(doc, addOp(doc, body.Track, body.TakeIDs), rules); err != nil {
			return err
		}
		return storeUser(ctx, tx, row, doc)
	})
}

// UndoTimeline writes the revision undo goes back to as the next one; RedoTimeline the one redo goes forward to.
func (s *Service) UndoTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineRevision) (dto.Timeline, error) {
	return s.restore(ctx, caller, id, body.Revision, true)
}

// RedoTimeline: see UndoTimeline.
func (s *Service) RedoTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineRevision) (dto.Timeline, error) {
	return s.restore(ctx, caller, id, body.Revision, false)
}

func (s *Service) restore(ctx context.Context, caller common.Caller, id string, revision int, undo bool) (dto.Timeline, error) {
	return s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		if revision != row.Revision {
			return staleRevision(*row)
		}
		curUndo, curRedo, err := pointers(ctx, tx, row.ID, row.Revision)
		if err != nil {
			return err
		}
		to, nothing := curRedo, "nothing to redo"
		if undo {
			to, nothing = curUndo, "nothing to undo"
		}
		if ok, err := kept(ctx, tx, row.ID, to); err != nil || !ok {
			if err == nil {
				err = network.NewConflictError(nothing, nil)
			}
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT doc FROM timeline_revisions WHERE timeline_id = $1 AND revision = $2`,
			row.ID, *to).Scan(&raw); err != nil {
			return err
		}
		var doc timeline.Doc
		_ = json.Unmarshal(raw, &doc)
		toUndo, toRedo, err := pointers(ctx, tx, row.ID, *to)
		if err != nil {
			return err
		}
		if doc, err = reconcile(ctx, tx, row.OwnerID, doc); err != nil {
			return err
		}
		current := row.Revision
		if undo {
			return store(ctx, tx, row, doc, "user", toUndo, &current)
		}
		return store(ctx, tx, row, doc, "user", &current, toRedo)
	})
}

// reconcile normalizes a restored document and settles its finished shots: a succeeded one fills its slot, a
// cancelled one is dropped, so undo never brings back a finished placeholder.
func reconcile(ctx context.Context, tx pgx.Tx, ownerID string, doc timeline.Doc) (timeline.Doc, error) {
	type fill struct{ clip, take string }
	var fills []fill
	for ti := range doc.Tracks {
		for ci := range doc.Tracks[ti].Clips {
			c := &doc.Tracks[ti].Clips[ci]
			if c.Shot == nil {
				continue
			}
			status, _, take, err := shotState(ctx, tx, ownerID, *c.Shot)
			switch {
			case err != nil:
				return doc, err
			case status == "succeeded":
				fills = append(fills, fill{c.ID, take})
			case status == "cancelled":
				c.Shot = nil
			}
		}
	}
	ids := docTakeIDs(doc)
	for _, f := range fills {
		ids = append(ids, f.take)
	}
	rules, _, err := loadTakes(ctx, tx, ownerID, ids)
	if err != nil {
		return doc, err
	}
	n, probs := timeline.Normalize(doc, rules, &doc)
	if probs != nil {
		return doc, problemsError(probs)
	}
	doc = *n
	for _, f := range fills {
		if _, ok := rules[f.take]; !ok {
			continue // its take is deleted since: the slot keeps its shot's place
		}
		if r := timeline.Apply(doc, timeline.Op{Op: "fill", Clip: f.clip, TakeID: f.take}, rules); r.Document != nil {
			doc = *r.Document
		}
	}
	return doc, nil
}

// ------------------------------------------------------------------ New shot

// CreateShot makes the run in the sequence's session and puts its shot on the slot (an inserted slot first).
func (s *Service) CreateShot(ctx context.Context, caller common.Caller, id string, body *dto.TimelineShotCreate) (dto.TimelineShot, error) {
	if !s.registry.Ready() {
		return dto.TimelineShot{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.TimelineShot
	tl, err := s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		doc := row.doc()
		rules, _, err := loadTakes(ctx, tx, caller.ID, docTakeIDs(doc))
		if err != nil {
			return err
		}
		clip := body.Slot.Clip
		if clip == "" {
			clip = body.Slot.ID
			if clip == "" {
				clip = newClipID(doc)
			}
			if slices.Contains(clipIDs(doc), clip) {
				return network.NewUnprocessableError("clip "+clip+" already exists", nil)
			}
			if doc, err = apply(doc, timeline.Op{Op: "insert", At: body.Slot.At, Hold: *body.Slot.Length, ID: clip}, rules); err != nil {
				return err
			}
		}
		at := slices.IndexFunc(doc.Tracks[0].Clips, func(c timeline.Clip) bool { return c.ID == clip })
		switch {
		case at < 0 && slices.Contains(clipIDs(doc), clip):
			return network.NewUnprocessableError("clip "+clip+" is not on V1", nil)
		case at < 0:
			return network.NewUnprocessableError("unknown clip "+clip, nil)
		}
		if shot := doc.Tracks[0].Clips[at].Shot; shot != nil {
			status, _, _, err := shotState(ctx, tx, caller.ID, *shot)
			if err != nil {
				return err
			} else if status == "queued" || status == "running" {
				return network.NewConflictError("clip "+clip+" already has a shot running", nil)
			}
		}
		sessionID, err := s.timelineSession(ctx, tx, row)
		if err != nil {
			return err
		}
		if out.Run, err = s.createRun(ctx, tx, caller, sessionID, body.Run); err != nil {
			return err
		}
		doc.Tracks[0].Clips[at].Shot = &timeline.ShotRef{RunID: out.Run.ID}
		if _, err := tx.Exec(ctx, `INSERT INTO timeline_shots (owner_id, timeline_id, clip, run_id) VALUES ($1, $2, $3, $4)`,
			caller.ID, row.ID, clip, out.Run.ID); err != nil {
			return err
		}
		return storeUser(ctx, tx, row, doc)
	})
	if err != nil {
		return dto.TimelineShot{}, err
	}
	out.Timeline = tl
	return out, s.placeQueued(ctx, &out.Run)
}

// clipIDs is every clip id of doc.
func clipIDs(doc timeline.Doc) []string {
	var out []string
	for _, t := range doc.Tracks {
		for _, c := range t.Clips {
			out = append(out, c.ID)
		}
	}
	return out
}

// timelineSession is the sequence's session; one deleted since is made again.
func (s *Service) timelineSession(ctx context.Context, tx pgx.Tx, row *timelineRow) (string, error) {
	if row.SessionID != nil {
		return *row.SessionID, nil
	}
	id := utility.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)`, id, row.OwnerID, row.Name); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE timelines SET session_id = $2 WHERE id = $1`, row.ID, id); err != nil {
		return "", err
	}
	row.SessionID = &id
	return id, nil
}

// shotsOf are the (timeline, clip) slots a run fills: a New shot's by run id, a re-render step's by batch and step.
func shotsOf(ctx context.Context, tx pgx.Tx, run model.Run) ([]timeline.ShotRef, []string, []string, error) {
	type slot struct{ Timeline, Clip string }
	var slots []slot
	var ref timeline.ShotRef
	var err error
	if run.BatchID != nil {
		key, ok := runKey(run)
		if !ok || key.Turn >= 0 {
			return nil, nil, nil, nil
		}
		ref = timeline.ShotRef{BatchID: *run.BatchID, Step: key.Step}
		slots, err = collect[slot](ctx, tx, `SELECT timeline_id::text, clip FROM timeline_shots WHERE batch_id = $1 AND step = $2
			AND owner_id = $3`, *run.BatchID, key.Step, run.OwnerID)
	} else {
		ref = timeline.ShotRef{RunID: run.ID}
		slots, err = collect[slot](ctx, tx, `SELECT timeline_id::text, clip FROM timeline_shots WHERE run_id = $1
			AND owner_id = $2`, run.ID, run.OwnerID)
	}
	var refs []timeline.ShotRef
	var timelines, clips []string
	for _, sl := range slots {
		refs, timelines, clips = append(refs, ref), append(timelines, sl.Timeline), append(clips, sl.Clip)
	}
	return refs, timelines, clips, err
}

// settleShot applies fn to every slot still waiting on run's shot, as studio revisions (in the projector's or a
// cancel's transaction).
func settleShot(ctx context.Context, tx pgx.Tx, run model.Run, extra []string,
	fn func(doc timeline.Doc, clip string, takes map[string]timeline.Take) (timeline.Doc, bool)) error {
	refs, timelines, clips, err := shotsOf(ctx, tx, run)
	if err != nil {
		return err
	}
	for i := range refs {
		row, err := one[timelineRow](ctx, tx, `SELECT `+timelineCols+` FROM timelines WHERE id = $1 AND owner_id = $2
			AND deleted_at IS NULL FOR UPDATE`, timelines[i], run.OwnerID)
		if errors.Is(err, errNotFound) {
			continue
		} else if err != nil {
			return err
		}
		doc := row.doc()
		k := slices.IndexFunc(doc.Tracks[0].Clips, func(c timeline.Clip) bool {
			return c.ID == clips[i] && c.Shot != nil && c.Shot.Ref() == refs[i]
		})
		if k < 0 {
			continue // the slot was removed or redone meanwhile: the take stays in the gallery
		}
		rules, _, err := loadTakes(ctx, tx, run.OwnerID, append(docTakeIDs(doc), extra...))
		if err != nil {
			return err
		}
		if next, ok := fn(doc, clips[i], rules); ok {
			if err := storeStudio(ctx, tx, &row, next); err != nil {
				return err
			}
		}
	}
	return nil
}

// fillShot fills the slots of run's shot with its first take (the projector, after storing it).
func fillShot(ctx context.Context, tx pgx.Tx, run model.Run, takeID string) error {
	return settleShot(ctx, tx, run, []string{takeID}, func(doc timeline.Doc, clip string, takes map[string]timeline.Take) (timeline.Doc, bool) {
		r := timeline.Apply(doc, timeline.Op{Op: "fill", Clip: clip, TakeID: takeID}, takes)
		if r.Document == nil {
			log.Printf("timelines: fill of clip %s with take %s: %v %v", clip, takeID, r.Error, r.Problems)
			return doc, false
		}
		return *r.Document, true
	})
}

// shotEnded drops a cancelled run's shot from its slots (an empty slot stays as it is); a failed one keeps it.
func shotEnded(ctx context.Context, tx pgx.Tx, run model.Run) error {
	if run.Status != "cancelled" || run.BatchID != nil {
		return nil
	}
	return settleShot(ctx, tx, run, nil, func(doc timeline.Doc, clip string, takes map[string]timeline.Take) (timeline.Doc, bool) {
		k := slices.IndexFunc(doc.Tracks[0].Clips, func(c timeline.Clip) bool { return c.ID == clip })
		doc.Tracks[0].Clips[k].Shot = nil
		return doc, true
	})
}
