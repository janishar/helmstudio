package studioapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// The timeline (R44–R49, 05 §6, §7; docs/decisions.md M8 Q7–Q19).
//
// A sequence is the framework's document, and it is owned by the studio that
// made it — or by the launcher, which has no studio id. Reading, editing and
// exporting one is that studio's, and any studio holding gallery.read_all,
// which already means "everything you have ever made". Every clip must name an
// asset the caller may read, checked again at export, so a sequence can never
// carry bytes out of a studio that could not read them (Q11).

// RevisionsKept is how many earlier documents a sequence keeps. Undo is the
// previous revision (05 §6); keeping them for ever would hold footage against
// reclaim long after a clip was taken out (Q10).
const RevisionsKept = 100

type timelineRow struct {
	id       string
	studio   sql.NullString
	name     string
	target   helm.TimelineTarget
	tracks   []helm.TimelineTrack
	revision int64
	created  int64
	updated  int64
}

const timelineCols = `id, studio_id, name, target, tracks, revision, created_at, updated_at`

func scanTimeline(sc interface{ Scan(...any) error }) (*timelineRow, error) {
	var r timelineRow
	var target, tracks string
	if err := sc.Scan(&r.id, &r.studio, &r.name, &target, &tracks, &r.revision, &r.created, &r.updated); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(target), &r.target); err != nil {
		return nil, fmt.Errorf("sequence %s: reading its target: %w", r.id, err)
	}
	if err := json.Unmarshal([]byte(tracks), &r.tracks); err != nil {
		return nil, fmt.Errorf("sequence %s: reading its tracks: %w", r.id, err)
	}
	return &r, nil
}

func (r *timelineRow) view() helm.Timeline {
	out := helm.Timeline{
		ID: r.id, Name: r.name, Target: r.target, Tracks: r.tracks,
		Revision: r.revision, DurationS: timeline.Duration(r.target, r.tracks),
		ETag:      strconv.FormatInt(r.revision, 10),
		CreatedAt: fromMS(r.created), UpdatedAt: fromMS(r.updated),
	}
	if out.Tracks == nil {
		out.Tracks = []helm.TimelineTrack{}
	}
	if r.studio.Valid {
		out.StudioID = &r.studio.String
	}
	return out
}

// mayReach reports whether p may read and edit this sequence.
func (r *timelineRow) mayReach(p Principal) bool {
	if p.has(string(helm.CapabilityGalleryReadAll)) {
		return true
	}
	return r.studio.Valid && r.studio.String == p.StudioID
}

// readTimeline reads a sequence the caller may reach. Any other is not found,
// so a sequence cannot be used to learn what another studio is working on.
func (s *Service) readTimeline(ctx context.Context, q querier, p Principal, id string) (*timelineRow, error) {
	r, err := scanTimeline(q.QueryRowContext(ctx, `SELECT `+timelineCols+` FROM timelines WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no sequence %s", id)
	}
	if err != nil {
		return nil, err
	}
	if !r.mayReach(p) {
		return nil, notFound("no sequence %s", id)
	}
	return r, nil
}

// invalidTimeline turns the document's own faults into one refusal that names
// every clip that is wrong, and never says whether an asset it could not use
// exists (Q11).
func invalidTimeline(err error) error {
	var inv *timeline.Invalid
	if !errors.As(err, &inv) {
		return unprocessable("invalid_timeline", "%v", err)
	}
	e := unprocessable("invalid_timeline", "%v", inv)
	faults := make([]any, 0, len(inv.Faults))
	for _, f := range inv.Faults {
		fault := map[string]any{"code": f.Code, "message": f.Message}
		if f.Track != "" {
			fault["track"] = f.Track
		}
		if f.Clip >= 0 {
			fault["clip"] = f.Clip + 1
		}
		faults = append(faults, fault)
	}
	e.Details = map[string]any{"faults": faults}
	return e
}

// sourcesFor is what the rules need to know about the assets a document names,
// for the assets this caller may read. One it may not read is simply absent,
// and the document then faults as it would for an asset that never existed.
func (s *Service) sourcesFor(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, p Principal, ids []string) (map[string]timeline.Source, error) {
	out := map[string]timeline.Source{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{"", readAll(p), p.StudioID}
	holes := make([]string, len(ids))
	for i, id := range ids {
		holes[i] = "?"
		args = append(args, id)
	}
	// ?1, ?2 and ?3 are the asset id, the read-all flag and the studio, as
	// readableAsset spells them; the asset id is compared in the IN clause
	// instead, so ?1 goes unused here.
	q2 := `SELECT a.id, a.kind, a.duration_s, a.origin_studio,
		(a.origin_studio = ?3
		 OR ?2 = 1
		 OR EXISTS (SELECT 1 FROM items i WHERE i.asset_id = a.id AND i.studio_id = ?3)
		 OR EXISTS (SELECT 1 FROM inbox n JOIN items i ON i.id = n.item_id WHERE n.to_studio = ?3
			AND (i.asset_id = a.id OR EXISTS (SELECT 1 FROM item_inputs x WHERE x.item_id = i.id AND x.asset_id = a.id)))) AS origin_visible
		FROM assets a WHERE a.id IN (` + strings.Join(holes, ",") + `) AND ` + readableAsset
	rows, err := q.QueryContext(ctx, q2, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind string
		var duration sql.NullFloat64
		var origin sql.NullString
		var originVisible int64
		if err := rows.Scan(&id, &kind, &duration, &origin, &originVisible); err != nil {
			return nil, err
		}
		src := timeline.Source{Kind: kind, Duration: duration.Float64}
		if origin.Valid && originVisible == 1 {
			src.StudioID = origin.String
		}
		out[id] = src
	}
	return out, rows.Err()
}

// normalize checks a document against the assets this caller may read, and
// returns it as it will be stored.
func (s *Service) normalize(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, p Principal, target helm.TimelineTarget, tracks []helm.TimelineTrack) ([]helm.TimelineTrack, error) {
	sources, err := s.sourcesFor(ctx, q, p, timeline.Assets(tracks))
	if err != nil {
		return nil, err
	}
	out, err := timeline.Normalize(target, tracks, sources)
	if err != nil {
		return nil, invalidTimeline(err)
	}
	return out, nil
}

func (s *Service) TimelineCreate(ctx context.Context, body *helm.TimelineCreate) (*helm.Timeline, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if len(body.Clips) > 0 && len(body.Tracks) > 0 {
		return nil, badRequest("give clips or tracks, not both: clips are laid end to end and tracks are the whole document")
	}
	target, err := timeline.Target(body.Target)
	if err != nil {
		return nil, invalidTimeline(err)
	}
	var out *helm.Timeline
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		tracks := body.Tracks
		if len(body.Clips) > 0 {
			sources, err := s.sourcesFor(ctx, tx, p, assetsOfClips(body.Clips))
			if err != nil {
				return err
			}
			if tracks, err = timeline.Lay(body.Clips, sources); err != nil {
				return invalidTimeline(err)
			}
		}
		normalized, err := s.normalize(ctx, tx, p, target, tracks)
		if err != nil {
			return err
		}
		id, now := s.newID(), ms(s.now())
		targetJSON, tracksJSON, err := encodeDocument(target, normalized)
		if err != nil {
			return err
		}
		// A sequence is owned by the studio that made it, or by the launcher
		// (01 §6 R44), and null is what the launcher's ownership is: the
		// launcher has no studio id, so an empty string would be an owner
		// whose hue and name nothing could look up.
		owner := sql.NullString{String: p.StudioID, Valid: p.StudioID != ""}
		if _, err := tx.ExecContext(ctx, `INSERT INTO timelines (id, studio_id, name, target, tracks, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, id, owner, body.Name, targetJSON, tracksJSON, now, now); err != nil {
			return err
		}
		r, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		v := r.view()
		out = &v
		return nil
	})
	return out, err
}

func assetsOfClips(clips []helm.TimelineClip) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range clips {
		if !seen[c.AssetID] {
			seen[c.AssetID] = true
			out = append(out, c.AssetID)
		}
	}
	return out
}

func encodeDocument(target helm.TimelineTarget, tracks []helm.TimelineTrack) (string, string, error) {
	if tracks == nil {
		tracks = []helm.TimelineTrack{}
	}
	t, err := json.Marshal(target)
	if err != nil {
		return "", "", err
	}
	tr, err := json.Marshal(tracks)
	if err != nil {
		return "", "", err
	}
	return string(t), string(tr), nil
}

func (s *Service) TimelineList(ctx context.Context, params helm.TimelineListParams) (*helm.TimelinePage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("timelines", p.StudioID, readAll(p))
	after, err := decodeCursor(params.Cursor, digest, 2)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + timelineCols + ` FROM timelines WHERE deleted_at IS NULL AND (studio_id = ? OR ? = 1)`
	args := []any{p.StudioID, readAll(p)}
	if after != nil {
		at, ok1 := cursorInt(after[0])
		id, ok2 := cursorString(after[1])
		if !ok1 || !ok2 {
			return nil, badCursor()
		}
		q += ` AND (updated_at, id) < (?, ?)`
		args = append(args, at, id)
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &helm.TimelinePage{Items: []helm.Timeline{}}
	for rows.Next() {
		r, err := scanTimeline(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = encodeCursor(digest, ms(last.UpdatedAt), last.ID)
			break
		}
		page.Items = append(page.Items, r.view())
	}
	return page, rows.Err()
}

func (s *Service) TimelineGet(ctx context.Context, id string) (*helm.Timeline, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.readTimeline(ctx, s.st.Reader(), p, id)
	if err != nil {
		return nil, err
	}
	v := r.view()
	return &v, nil
}

// TimelineUpdate applies a merge patch over name, target and tracks. The
// document the patch produces is validated whole, and the one it replaces is
// kept as the previous revision (Q10).
func (s *Service) TimelineUpdate(ctx context.Context, id string, body map[string]any, params helm.TimelineUpdateParams) (*helm.Timeline, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Timeline
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := matchRevision(params.IfMatch, cur); err != nil {
			return err
		}
		name, target, tracks := cur.name, cur.target, cur.tracks
		if v, ok := body["name"]; ok {
			n, isString := v.(string)
			if !isString || n == "" || len([]rune(n)) > 200 {
				return badRequest("name is a string of 1 to 200 characters")
			}
			name = n
		}
		if v, ok := body["target"]; ok {
			patch, isObject := v.(map[string]any)
			if !isObject {
				return badRequest("target is an object")
			}
			if target, err = patchTarget(cur.target, patch); err != nil {
				return err
			}
		}
		if v, ok := body["tracks"]; ok {
			if tracks, err = decodeTracks(v); err != nil {
				return err
			}
		}
		normalized, err := s.normalize(ctx, tx, p, target, tracks)
		if err != nil {
			return err
		}
		if err := s.saveRevision(ctx, tx, cur, name, target, normalized); err != nil {
			return err
		}
		r, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		v := r.view()
		out = &v
		return nil
	})
	return out, err
}

// matchRevision holds a write to the revision its caller last read.
func matchRevision(ifMatch string, cur *timelineRow) error {
	if unquoteETag(ifMatch) != strconv.FormatInt(cur.revision, 10) {
		return etagMismatch("sequence " + cur.id)
	}
	return nil
}

// patchTarget merges a patch into the current target, so a caller may change
// the frame rate without resending the size.
func patchTarget(cur helm.TimelineTarget, patch map[string]any) (helm.TimelineTarget, error) {
	as := map[string]any{"width": cur.Width, "height": cur.Height, "fps": cur.FPS, "sample_rate": cur.SampleRate}
	merged := mergePatch(as, patch)
	raw, err := json.Marshal(merged)
	if err != nil {
		return cur, badRequest("target: %v", err)
	}
	var out helm.TimelineTarget
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return cur, badRequest("target: %v", err)
	}
	if err := timeline.CheckTarget(out); err != nil {
		return cur, invalidTimeline(err)
	}
	return out, nil
}

// decodeTracks reads the tracks a patch replaced them with, refusing a member
// no track or clip has rather than ignoring it.
func decodeTracks(v any) ([]helm.TimelineTrack, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, badRequest("tracks: %v", err)
	}
	var out []helm.TimelineTrack
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, badRequest("tracks: %v", err)
	}
	return out, nil
}

// saveRevision keeps the document being replaced and writes the new one.
func (s *Service) saveRevision(ctx context.Context, tx *sql.Tx, cur *timelineRow, name string, target helm.TimelineTarget, tracks []helm.TimelineTrack) error {
	oldTarget, oldTracks, err := encodeDocument(cur.target, cur.tracks)
	if err != nil {
		return err
	}
	now := ms(s.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO timeline_revisions (timeline_id, revision, name, target, tracks, saved_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, cur.id, cur.revision, cur.name, oldTarget, oldTracks, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM timeline_revisions WHERE timeline_id = ? AND revision <= ?`,
		cur.id, cur.revision-RevisionsKept); err != nil {
		return err
	}
	newTarget, newTracks, err := encodeDocument(target, tracks)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE timelines SET name = ?, target = ?, tracks = ?, revision = revision + 1, updated_at = ? WHERE id = ?`,
		name, newTarget, newTracks, now, cur.id)
	return err
}

func (s *Service) TimelineDelete(ctx context.Context, id string, params helm.TimelineDeleteParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if params.IfMatch != nil {
			if err := matchRevision(*params.IfMatch, cur); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE timelines SET deleted_at = ? WHERE id = ?`, ms(s.now()), id)
		return err
	})
}

func (s *Service) TimelineRevisions(ctx context.Context, id string, params helm.TimelineRevisionsParams) (*helm.TimelineRevisionPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.readTimeline(ctx, s.st.Reader(), p, id); err != nil {
		return nil, err
	}
	digest := filterDigest("timeline-revisions", id)
	after, err := decodeCursor(params.Cursor, digest, 1)
	if err != nil {
		return nil, err
	}
	q := `SELECT revision, target, tracks, saved_at FROM timeline_revisions WHERE timeline_id = ?`
	args := []any{id}
	if after != nil {
		rev, ok := cursorInt(after[0])
		if !ok {
			return nil, badCursor()
		}
		q += ` AND revision < ?`
		args = append(args, rev)
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY revision DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &helm.TimelineRevisionPage{Items: []helm.TimelineRevision{}}
	for rows.Next() {
		var rev, saved int64
		var target, tracks string
		if err := rows.Scan(&rev, &target, &tracks, &saved); err != nil {
			return nil, err
		}
		var tg helm.TimelineTarget
		var trs []helm.TimelineTrack
		if json.Unmarshal([]byte(target), &tg) != nil || json.Unmarshal([]byte(tracks), &trs) != nil {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = encodeCursor(digest, page.Items[limit-1].Revision)
			break
		}
		page.Items = append(page.Items, helm.TimelineRevision{
			Revision: rev, SavedAt: fromMS(saved),
			DurationS: timeline.Duration(tg, trs), ClipCount: timeline.ClipCount(trs),
		})
	}
	return page, rows.Err()
}

// TimelineRevert writes an earlier revision forward as the newest one, so
// undoing loses nothing (Q10). A revision naming an asset that has since been
// reclaimed, or one this caller may not read, is refused with the clips named.
func (s *Service) TimelineRevert(ctx context.Context, id string, body *helm.TimelineRevert, params helm.TimelineRevertParams) (*helm.Timeline, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Timeline
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := matchRevision(params.IfMatch, cur); err != nil {
			return err
		}
		var name, target, tracks string
		err = tx.QueryRowContext(ctx, `SELECT name, target, tracks FROM timeline_revisions WHERE timeline_id = ? AND revision = ?`,
			id, body.Revision).Scan(&name, &target, &tracks)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("sequence %s has no revision %d to go back to", id, body.Revision)
		}
		if err != nil {
			return err
		}
		var tg helm.TimelineTarget
		var trs []helm.TimelineTrack
		if err := json.Unmarshal([]byte(target), &tg); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(tracks), &trs); err != nil {
			return err
		}
		normalized, err := s.normalize(ctx, tx, p, tg, trs)
		if err != nil {
			return err
		}
		if err := s.saveRevision(ctx, tx, cur, name, tg, normalized); err != nil {
			return err
		}
		r, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		v := r.view()
		out = &v
		return nil
	})
	return out, err
}

// TimelineAppend adds one asset to the end of a track. It applies to whatever
// the current revision is, which is what "without stealing focus" needs: the
// studio that just made a take has no revision in its hand (R45, Q19).
func (s *Service) TimelineAppend(ctx context.Context, body *helm.TimelineAppend) (*helm.Timeline, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Timeline
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		id := ""
		if body.TimelineID != nil {
			id = *body.TimelineID
		} else {
			err := tx.QueryRowContext(ctx, `SELECT id FROM timelines WHERE studio_id = ? AND deleted_at IS NULL
				ORDER BY updated_at DESC, id DESC LIMIT 1`, p.StudioID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return conflict("no_timeline", "this studio has no sequence to append to; create one with POST /timeline")
			}
			if err != nil {
				return err
			}
		}
		cur, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		sources, err := s.sourcesFor(ctx, tx, p, []string{body.AssetID})
		if err != nil {
			return err
		}
		src, ok := sources[body.AssetID]
		if !ok {
			return invalidTimeline(&timeline.Invalid{Faults: []timeline.Fault{{
				Clip: -1, Code: "asset_not_found", Message: fmt.Sprintf("no asset %s you can use", body.AssetID)}}})
		}
		want := "V1"
		if src.Kind == timeline.KindAudio {
			want = "A1"
		}
		if body.Track != nil {
			want = *body.Track
		}
		if (strings.HasPrefix(want, "V") && src.Kind == timeline.KindAudio) || (strings.HasPrefix(want, "A") && src.Kind != timeline.KindAudio) {
			return badRequest("a %s asset does not belong on %s", src.Kind, want)
		}
		tracks := appendTo(cur.tracks, want, helm.TimelineClip{AssetID: body.AssetID})
		normalized, err := s.normalize(ctx, tx, p, cur.target, tracks)
		if err != nil {
			return err
		}
		if err := s.saveRevision(ctx, tx, cur, cur.name, cur.target, normalized); err != nil {
			return err
		}
		r, err := s.readTimeline(ctx, tx, p, id)
		if err != nil {
			return err
		}
		v := r.view()
		out = &v
		return nil
	})
	return out, err
}

// appendTo puts a clip at the end of the named track, making the track when
// the sequence has none by that name.
func appendTo(tracks []helm.TimelineTrack, name string, c helm.TimelineClip) []helm.TimelineTrack {
	out := make([]helm.TimelineTrack, len(tracks))
	copy(out, tracks)
	if i := timeline.TrackNamed(out, name); i >= 0 {
		clips := make([]helm.TimelineClip, len(out[i].Clips), len(out[i].Clips)+1)
		copy(clips, out[i].Clips)
		out[i].Clips = append(clips, c)
		return out
	}
	kind := timeline.KindVideo
	if strings.HasPrefix(name, "A") {
		kind = timeline.KindAudio
	}
	out = append(out, helm.TimelineTrack{Kind: kind, Clips: []helm.TimelineClip{c}})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind == timeline.KindVideo && out[j].Kind != timeline.KindVideo })
	return out
}

// TimelineOpen asks the framework to show its editor. Until the launcher has a
// Timeline screen — which waits for M9's cookie, because it reads every
// studio's items and bytes (Q20) — there is no surface to open, and a studio
// hides its button on this answer (R45, 05 §5a).
func (s *Service) TimelineOpen(ctx context.Context, id string) (*helm.TimelineOpened, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.readTimeline(ctx, s.st.Reader(), p, id); err != nil {
		return nil, err
	}
	return nil, unsupported("this helmstudio has no window to open a sequence in: the launcher's timeline screen ships with the Mac app, and until then a sequence is edited in a studio. The sequence itself is made and exported either way")
}
