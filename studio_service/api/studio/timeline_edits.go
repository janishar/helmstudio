package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
)

// Timeline edits by ops (contracts/README.md "Timeline: edits by ops", API 1.49; plan 24): named edits applied with
// edit.go's Apply under the row lock, all or nothing, tried first with dry_run; the history and restore; the agent
// view; what V1 shows at a time.

// TimelineEditError is /edits' 422 for an op that cannot apply: TimelineError plus the op and its code.
type TimelineEditError struct {
	Detail   string                `json:"detail"`
	OpIndex  int                   `json:"op_index"`
	Code     string                `json:"code"`
	Problems []dto.TimelineProblem `json:"problems"`
}

func (e *TimelineEditError) Error() string { return e.Detail }

// EditTimeline applies body.Ops in order to the sequence at body.BaseRevision and stores the result as one user
// revision, or (dry run) answers it without storing.
func (s *Service) EditTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineEdits) (dto.TimelineEditResult, error) {
	var out dto.TimelineEditResult
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if body.BaseRevision != row.Revision {
			return staleRevision(row)
		}
		stored := row.doc()
		ids := docTakeIDs(stored)
		for _, op := range body.Ops {
			for _, t := range append([]string{op.TakeID}, op.TakeIDs...) {
				if t != "" && !slices.Contains(ids, t) {
					ids = append(ids, t)
				}
			}
		}
		rules, _, err := loadTakes(ctx, tx, caller.ID, ids)
		if err != nil {
			return err
		}
		before := stored
		if n, probs := timeline.Normalize(stored, rules, &stored); probs == nil {
			before = *n
		}
		doc, warnings, err := applyOps(stored, body.Ops, rules)
		if err != nil {
			return err
		}
		out = dto.TimelineEditResult{Revision: row.Revision, DryRun: body.DryRun, Diff: timeline.DiffDocs(before, doc),
			Warnings: warnings}
		if body.DryRun {
			row.Doc, _ = json.Marshal(clean(doc, &stored)) // what it would be: nothing is written
		} else if err := storeUser(ctx, tx, &row, doc, userWrite(caller, opsSummary(body.Ops))); err != nil {
			return err
		}
		out.Revision = row.Revision
		out.Timeline, err = s.timelineDTO(ctx, tx, row)
		return err
	})
	return out, err
}

// applyOps applies ops in order (each edit, then normalize against the document before it): the document after the
// last, the warnings, or the first op's TimelineEditError. An op's absent new clip ids are made here (newClipID:
// unused in the document and by every id the call gives).
func applyOps(doc timeline.Doc, ops []timeline.Op, rules map[string]timeline.Take) (timeline.Doc, []string, error) {
	var given []string
	for _, op := range ops {
		for _, cid := range append([]string{op.ID, op.GapID}, op.IDs...) {
			if cid != "" {
				given = append(given, cid)
			}
		}
	}
	made := func() string {
		cid := newClipID(doc, given...)
		given = append(given, cid)
		return cid
	}
	g, warnings := timeline.NewGrid(doc.Target), []string{}
	for i, op := range ops {
		for _, t := range append([]string{op.TakeID}, op.TakeIDs...) {
			if _, ok := rules[t]; t != "" && !ok {
				return doc, nil, &TimelineEditError{Detail: fmt.Sprintf("ops[%d]: take %s not found", i, t), OpIndex: i,
					Code: "take_not_found", Problems: []dto.TimelineProblem{}}
			}
		}
		switch op.Op {
		case "split", "insert", "place":
			if op.ID == "" {
				op.ID = made()
			}
			if op.Op == "place" && op.GapID == "" {
				op.GapID = made()
			}
		case "add":
			op.IDs = slices.Clone(op.IDs)
			for len(op.IDs) < len(op.TakeIDs) {
				op.IDs = append(op.IDs, made())
			}
		}
		warnings = append(warnings, snapped(i, op, g)...)
		r := timeline.Apply(doc, op, rules)
		switch {
		case r.Error != "":
			text := r.Error
			switch r.Error {
			case "unknown_clip":
				text = "unknown clip " + op.Clip
			case "unknown_track":
				text = "unknown track " + op.Track
			}
			return doc, nil, &TimelineEditError{Detail: fmt.Sprintf("ops[%d]: %s", i, text), OpIndex: i, Code: r.Error,
				Problems: []dto.TimelineProblem{}}
		case len(r.Problems) > 0:
			var pe *TimelineError
			errors.As(problemsError(r.Problems), &pe)
			return doc, nil, &TimelineEditError{Detail: fmt.Sprintf("ops[%d]: %s", i, pe.Detail), OpIndex: i,
				Code: r.Problems[0].Code, Problems: pe.Problems}
		}
		doc = *r.Document
	}
	return doc, warnings, nil
}

// snapped is a warning for each of op's times that the frame grid moves.
func snapped(i int, op timeline.Op, g timeline.Grid) []string {
	var out []string
	check := func(name string, v float64) {
		if s := g.Snap(v); s != v {
			out = append(out, fmt.Sprintf("ops[%d]: %s %g snapped to %g", i, name, v, s))
		}
	}
	if op.At != nil {
		check("at", *op.At)
	}
	switch op.Op {
	case "insert", "place":
		check("hold", op.Hold)
	case "trim":
		check("delta", op.Delta)
	}
	return out
}

// opsSummary is an /edits call's distinct op names in order: "split, remove".
func opsSummary(ops []timeline.Op) string {
	var names []string
	for _, op := range ops {
		if !slices.Contains(names, op.Op) {
			names = append(names, op.Op)
		}
	}
	return strings.Join(names, ", ")
}

// ------------------------------------------------------------------ revisions

type revisionRow struct {
	Revision   int
	By         string
	Via        *string
	SavedAt    time.Time
	OpsSummary *string
	Clips      int
	DurationS  float64
	Doc        json.RawMessage
}

// a user write from before 1.49 has no via: it came from the web
const revisionCols = `revision, by, CASE WHEN by = 'user' THEN coalesce(via, 'web') END, saved_at, ops_summary,
	coalesce(jsonb_array_length(doc->'tracks'->0->'clips'), 0), coalesce((doc->>'duration_s')::float8, 0)`

func (r revisionRow) item() dto.TimelineRevisionItem {
	return dto.TimelineRevisionItem{Revision: r.Revision, By: r.By, Via: r.Via, SavedAt: r.SavedAt, OpsSummary: r.OpsSummary,
		Clips: r.Clips, DurationS: r.DurationS}
}

// ListTimelineRevisions is the kept revisions below before (nil: all), newest first, at most limit.
func (s *Service) ListTimelineRevisions(ctx context.Context, caller common.Caller, id string, before *int, limit int) (dto.TimelineRevisionList, error) {
	out := dto.TimelineRevisionList{Revisions: []dto.TimelineRevisionItem{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readTimeline(ctx, tx, id, false); err != nil {
			return err
		}
		rows, err := collect[revisionRow](ctx, tx, `SELECT `+revisionCols+`, NULL::jsonb FROM timeline_revisions
			WHERE timeline_id = $1 AND ($2::int IS NULL OR revision < $2) ORDER BY revision DESC LIMIT $3`, id, before, limit)
		for _, r := range rows {
			out.Revisions = append(out.Revisions, r.item())
		}
		return err
	})
	return out, err
}

// GetTimelineRevision is one kept revision with its stored document; one not kept is 404.
func (s *Service) GetTimelineRevision(ctx context.Context, caller common.Caller, id string, revision int) (dto.TimelineRevisionDoc, error) {
	var out dto.TimelineRevisionDoc
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readTimeline(ctx, tx, id, false); err != nil {
			return err
		}
		r, err := one[revisionRow](ctx, tx, `SELECT `+revisionCols+`, doc FROM timeline_revisions
			WHERE timeline_id = $1 AND revision = $2`, id, revision)
		out = dto.TimelineRevisionDoc{TimelineRevisionItem: r.item(), Document: r.Doc}
		return err
	})
	return out, err
}

// ------------------------------------------------------------------ reading for agents

// GetTimelineAgent is GET …/{id}?view=agent: t_agent_view of the sequence as GET answers it, with take titles.
func (s *Service) GetTimelineAgent(ctx context.Context, caller common.Caller, id string) (dto.TimelineAgentView, error) {
	tl, err := s.GetTimeline(ctx, caller, id)
	if err != nil {
		return dto.TimelineAgentView{}, err
	}
	takes := map[string]timeline.Take{}
	for tid, t := range tl.Takes {
		takes[tid] = timeline.Take{Kind: t.Kind}
	}
	v := timeline.Agent(timeline.Doc{Target: tl.Target, Tracks: tl.Tracks, DurationS: tl.DurationS, Frames: tl.Frames}, takes)
	out := dto.TimelineAgentView{ID: tl.ID, Name: tl.Name, Revision: tl.Revision, Target: v.Target, DurationS: v.DurationS,
		Frames: v.Frames, Tracks: v.Tracks, Clips: []dto.TimelineAgentClip{}}
	for _, c := range v.Clips {
		ac := dto.TimelineAgentClip{AgentClip: c}
		if t, ok := tl.Takes[deref(c.TakeID)]; ok {
			ac.TakeTitle = &t.Title
		}
		out.Clips = append(out.Clips, ac)
	}
	return out, nil
}

// TimelineFrame is what V1 shows at body.At (frame_at_time) with its stored image signed: an image take's image, a
// video's nearer stored frame (a run's poster is frame 0; the stored last frame), else the other one, else (an upload
// or import with no last frame) its poster. Nothing is made.
func (s *Service) TimelineFrame(ctx context.Context, caller common.Caller, id string, body *dto.TimelineFrameRequest) (dto.TimelineFrame, error) {
	var out dto.TimelineFrame
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, false)
		if err != nil {
			return err
		}
		doc := row.doc()
		rules, rows, err := loadTakes(ctx, tx, row.OwnerID, docTakeIDs(doc))
		if err != nil {
			return err
		}
		if n, probs := timeline.Normalize(doc, rules, &doc); probs == nil {
			doc = *n // offline marked
		}
		out.FrameTime = timeline.FrameAtTime(doc, *body.At, rules)
		if out.TakeID == nil || out.Offline {
			return nil
		}
		take := rows[*out.TakeID]
		if *out.Kind == "image" {
			link := gallery.TakeDTO(s.signer, take).URL
			out.URL = &link
			return nil
		}
		var v gallerydto.Video
		_ = json.Unmarshal(take.Video, &v)
		stored := map[string]*gallerydto.Poster{"last": v.Last}
		if take.Source == "create" || take.Source == "pipeline" { // a run's poster is its frame 0
			stored["first"] = &v.Poster
		}
		for k, p := range stored {
			if p == nil || p.Key == "" || !common.Owns(row.OwnerID, p.Key) {
				delete(stored, k) // never sign another user's object
			}
		}
		other := map[string]string{"first": "last", "last": "first"}[*out.Shown]
		switch {
		case stored[*out.Shown] != nil:
		case stored[other] != nil:
			shown := other
			t := 0.0
			if shown == "last" {
				g := timeline.NewGrid(doc.Target)
				t = g.S(g.F(rules[*out.TakeID].DurationS) - 1)
			}
			out.Shown, out.ShownT = &shown, &t
		case v.Poster.Key != "" && common.Owns(row.OwnerID, v.Poster.Key):
			shown := "poster"
			out.Shown, out.ShownT, stored["poster"] = &shown, nil, &v.Poster
		default: // no stored frame at all: no image
			out.Shown, out.ShownT = nil, nil
			return nil
		}
		link := s.signer.Sign(stored[*out.Shown].Key)
		out.URL = &link
		return nil
	})
	return out, err
}
