package studioapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/export"
	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// A service with two studios, so what one may see of the other's work is a
// test rather than an assumption.
type timelineFixture struct {
	svc      *Service
	one, two Principal
	ctxOne   context.Context
	ctxTwo   context.Context
}

func newTimelineFixture(t *testing.T, find func() (*export.Tool, error)) *timelineFixture {
	t.Helper()
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	caps := []string{"assets", "gallery", "timeline"}
	one := Principal{StudioID: "one", Capabilities: caps}
	two := Principal{StudioID: "two", Capabilities: caps}
	svc := NewService(Config{
		Store:      st,
		Media:      &media.Engine{Assets: filepath.Join(d.Data(), "assets"), Derived: filepath.Join(d.Cache(), "derived"), Library: d.Library()},
		Paths:      DirPaths{StageRoot: d.Stage(), DataRoot: d.Data(), LogsRoot: d.Logs()},
		FindFFmpeg: find,
	})
	return &timelineFixture{svc: svc, one: one, two: two,
		ctxOne: WithPrincipal(ctx, one), ctxTwo: WithPrincipal(ctx, two)}
}

// noFFmpeg stands for a machine with none: everything that needs it answers
// 501, and everything else still works (M8 Q5).
func noFFmpeg() (*export.Tool, error) {
	return nil, fmt.Errorf("%w: ffmpeg is not on PATH", export.ErrMissing)
}

// upload puts bytes in the store as one studio's asset, with the duration and
// kind a studio would have given at adopt time.
func (f *timelineFixture) upload(t *testing.T, ctx context.Context, name string, kind helm.AssetKind, duration float64) string {
	t.Helper()
	data := []byte("fixture bytes for " + name)
	params := helm.AssetsUploadParams{Kind: kind, Filename: &name}
	if duration > 0 {
		params.DurationS = &duration
	}
	a, _, err := f.svc.AssetsUpload(ctx, bytes.NewReader(data), "video/mp4", int64(len(data)), params)
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return a.ID
}

func target() helm.TimelineTargetInput {
	return helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24}
}

func clips(ids ...string) []helm.TimelineClip {
	out := make([]helm.TimelineClip, len(ids))
	for i, id := range ids {
		out[i] = helm.TimelineClip{AssetID: id}
	}
	return out
}

func apiCode(err error) string {
	var e *helm.Error
	if errors.As(err, &e) {
		return fmt.Sprintf("%d %s", e.Status, e.Code)
	}
	if err == nil {
		return "no error"
	}
	return err.Error()
}

// A sequence is stored as it will be rendered: times on the target's grid,
// tracks named, clips laid end to end (M8 Q7, Q8).
func TestCreatingASequenceStoresItSnapped(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 5.04)
	b := f.upload(t, f.ctxOne, "b.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "café sequence", Target: target(), Clips: clips(a, b)})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Tracks) != 1 || tl.Tracks[0].Name == nil || *tl.Tracks[0].Name != "V1" {
		t.Fatalf("tracks %+v, want one named V1", tl.Tracks)
	}
	first := tl.Tracks[0].Clips[0]
	if *first.Out != 121.0/24 {
		t.Errorf("the first clip runs to %.6f, want %.6f: 5.04s is not a frame at 24 fps", *first.Out, 121.0/24)
	}
	if *tl.Tracks[0].Clips[1].At != *first.Out {
		t.Error("the second clip does not start where the first ends")
	}
	if tl.Target.SampleRate != 48000 {
		t.Errorf("sample rate %d, want the default 48000", tl.Target.SampleRate)
	}
	if tl.ETag != strconv.FormatInt(tl.Revision, 10) {
		t.Errorf("etag %q is not the revision %d", tl.ETag, tl.Revision)
	}
	if tl.StudioID == nil || *tl.StudioID != "one" {
		t.Errorf("studio %v, want the studio that made it", tl.StudioID)
	}
}

// A sequence belongs to the studio that made it. Another studio cannot read
// it, edit it or export it, and is told the same thing it would be told about
// a sequence that does not exist (M8 Q11).
func TestAnotherStudioCannotReachASequence(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "mine", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TimelineGet(f.ctxTwo, tl.ID); apiCode(err) != "404 not_found" {
		t.Errorf("get: %s, want 404 not_found", apiCode(err))
	}
	if _, err := f.svc.TimelineUpdate(f.ctxTwo, tl.ID, map[string]any{"name": "yours"}, helm.TimelineUpdateParams{IfMatch: tl.ETag}); apiCode(err) != "404 not_found" {
		t.Errorf("update: %s, want 404 not_found", apiCode(err))
	}
	if err := f.svc.TimelineDelete(f.ctxTwo, tl.ID, helm.TimelineDeleteParams{}); apiCode(err) != "404 not_found" {
		t.Errorf("delete: %s, want 404 not_found", apiCode(err))
	}
	if _, err := f.svc.TimelineExport(f.ctxTwo, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264}); apiCode(err) != "404 not_found" {
		t.Errorf("export: %s, want 404 not_found", apiCode(err))
	}
	page, err := f.svc.TimelineList(f.ctxTwo, helm.TimelineListParams{})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("list: %d sequences (%v), want none", len(page.Items), err)
	}
	// With gallery.read_all — "read everything you have ever made" — it can.
	reader := WithPrincipal(context.Background(), Principal{StudioID: "two", Capabilities: []string{"timeline", "assets", "gallery", "gallery.read_all"}})
	if _, err := f.svc.TimelineGet(reader, tl.ID); err != nil {
		t.Errorf("a studio holding gallery.read_all was refused: %v", err)
	}
}

// A clip may only name an asset the caller can read, whether it arrives by
// create, patch or append — or a sequence would be a way to have the daemon
// render bytes a studio may not touch (M8 Q11).
func TestASequenceCannotNameAnAssetTheCallerCannotRead(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	theirs := f.upload(t, f.ctxOne, "theirs.mp4", helm.AssetKindVideo, 2)
	mine := f.upload(t, f.ctxTwo, "mine.mp4", helm.AssetKindVideo, 2)

	_, err := f.svc.TimelineCreate(f.ctxTwo, &helm.TimelineCreate{Name: "sneaky", Target: target(), Clips: clips(theirs)})
	if apiCode(err) != "422 invalid_timeline" {
		t.Fatalf("create: %s, want 422 invalid_timeline", apiCode(err))
	}
	if strings.Contains(err.Error(), "exist") {
		t.Errorf("the refusal says whether the asset exists: %v", err)
	}
	tl, err := f.svc.TimelineCreate(f.ctxTwo, &helm.TimelineCreate{Name: "mine", Target: target(), Clips: clips(mine)})
	if err != nil {
		t.Fatal(err)
	}
	tracks := []any{map[string]any{"kind": "video", "clips": []any{
		map[string]any{"asset_id": mine}, map[string]any{"asset_id": theirs},
	}}}
	_, err = f.svc.TimelineUpdate(f.ctxTwo, tl.ID, map[string]any{"tracks": tracks}, helm.TimelineUpdateParams{IfMatch: tl.ETag})
	if apiCode(err) != "422 invalid_timeline" {
		t.Errorf("patch: %s, want 422 invalid_timeline", apiCode(err))
	}
	_, err = f.svc.TimelineAppend(f.ctxTwo, &helm.TimelineAppend{AssetID: theirs, TimelineID: &tl.ID})
	if apiCode(err) != "422 invalid_timeline" {
		t.Errorf("append: %s, want 422 invalid_timeline", apiCode(err))
	}
}

// Every write is a revision, undo writes an old one forward, and a write
// against a revision somebody else has moved on from is refused (M8 Q10).
func TestEditingKeepsRevisionsAndRefusesAStaleWrite(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	b := f.upload(t, f.ctxOne, "b.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "one clip", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatal(err)
	}
	stale := tl.ETag
	two, err := f.svc.TimelineAppend(f.ctxOne, &helm.TimelineAppend{AssetID: b, TimelineID: &tl.ID})
	if err != nil {
		t.Fatal(err)
	}
	if two.Revision != tl.Revision+1 || len(two.Tracks[0].Clips) != 2 {
		t.Fatalf("after append: revision %d with %d clips", two.Revision, len(two.Tracks[0].Clips))
	}
	if _, err := f.svc.TimelineUpdate(f.ctxOne, tl.ID, map[string]any{"name": "late"}, helm.TimelineUpdateParams{IfMatch: stale}); apiCode(err) != "409 etag_mismatch" {
		t.Errorf("a stale write: %s, want 409 etag_mismatch", apiCode(err))
	}
	revs, err := f.svc.TimelineRevisions(f.ctxOne, tl.ID, helm.TimelineRevisionsParams{})
	if err != nil || len(revs.Items) != 1 || revs.Items[0].Revision != tl.Revision {
		t.Fatalf("revisions %+v (%v), want the document the append replaced", revs.Items, err)
	}
	back, err := f.svc.TimelineRevert(f.ctxOne, tl.ID, &helm.TimelineRevert{Revision: tl.Revision}, helm.TimelineRevertParams{IfMatch: two.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Tracks[0].Clips) != 1 {
		t.Errorf("after undo: %d clips, want 1", len(back.Tracks[0].Clips))
	}
	if back.Revision != two.Revision+1 {
		t.Errorf("undo went back to revision %d; it should write forward", back.Revision)
	}
}

// A clip in a live sequence holds its footage; one in a deleted sequence, or
// in a revision nobody is looking at, does not (M8 Q10, Q12).
func TestOnlyALiveSequenceHoldsItsFootage(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	b := f.upload(t, f.ctxOne, "b.mp4", helm.AssetKindVideo, 2)
	item, err := f.svc.GalleryAdd(f.ctxOne, &helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: a, Params: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GalleryAdd(f.ctxOne, &helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: b, Params: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "holds them", Target: target(), Clips: clips(a, b)})
	if err != nil {
		t.Fatal(err)
	}
	// Both items go, so only the sequence keeps the assets alive.
	if err := f.svc.GalleryDelete(f.ctxOne, item.ID); err != nil {
		t.Fatal(err)
	}
	items, err := f.svc.GalleryQuery(f.ctxOne, helm.GalleryQueryParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items.Items {
		if err := f.svc.GalleryDelete(f.ctxOne, it.ID); err != nil {
			t.Fatal(err)
		}
	}
	reclaimable := func() int {
		t.Helper()
		p, err := f.svc.ReclaimPreview(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Items)
	}
	if n := reclaimable(); n != 0 {
		t.Errorf("%d assets are reclaimable while a live sequence names them", n)
	}
	// Taking a clip out leaves it only in an old revision, which holds nothing.
	tracks := []any{map[string]any{"kind": "video", "clips": []any{map[string]any{"asset_id": a}}}}
	if _, err := f.svc.TimelineUpdate(f.ctxOne, tl.ID, map[string]any{"tracks": tracks}, helm.TimelineUpdateParams{IfMatch: tl.ETag}); err != nil {
		t.Fatal(err)
	}
	if n := reclaimable(); n != 1 {
		t.Errorf("%d assets are reclaimable after a clip was taken out, want 1", n)
	}
	cur, err := f.svc.TimelineGet(f.ctxOne, tl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.TimelineDelete(f.ctxOne, tl.ID, helm.TimelineDeleteParams{IfMatch: &cur.ETag}); err != nil {
		t.Fatal(err)
	}
	if n := reclaimable(); n != 2 {
		t.Errorf("%d assets are reclaimable after the sequence was deleted, want 2", n)
	}
}

// Without ffmpeg the data operations still work and only the ones that need it
// degrade, which is what "the data operation always works" means (R45, Q5).
func TestWithoutFFmpegASequenceCanStillBeMadeAndEdited(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "no ffmpeg here", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatalf("creating a sequence needed ffmpeg: %v", err)
	}
	if _, err := f.svc.TimelineUpdate(f.ctxOne, tl.ID, map[string]any{"name": "still fine"}, helm.TimelineUpdateParams{IfMatch: tl.ETag}); err != nil {
		t.Fatalf("editing a sequence needed ffmpeg: %v", err)
	}
	if _, err := f.svc.TimelinePlan(f.ctxOne, tl.ID, helm.TimelinePlanParams{}); apiCode(err) != "501 unsupported" {
		t.Errorf("plan: %s, want 501 unsupported", apiCode(err))
	}
	_, err = f.svc.TimelineExport(f.ctxOne, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264})
	if apiCode(err) != "501 unsupported" {
		t.Errorf("export: %s, want 501 unsupported", apiCode(err))
	}
	var e *helm.Error
	if errors.As(err, &e) {
		if e.Details["tool"] != "ffmpeg" || e.Details["code"] != "tool_missing" {
			t.Errorf("details %v, want the tool named", e.Details)
		}
	}
}

// :open is the one place the design lets presentation degrade while the data
// operation stands (R45, 05 §5a).
func TestOpenSaysThereIsNoWindowYet(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "open me", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TimelineOpen(f.ctxOne, tl.ID); apiCode(err) != "501 unsupported" {
		t.Errorf("open: %s, want 501 unsupported", apiCode(err))
	}
	if _, err := f.svc.TimelineOpen(f.ctxTwo, tl.ID); apiCode(err) != "404 not_found" {
		t.Errorf("another studio's open: %s, want 404 not_found", apiCode(err))
	}
}

// Appending without naming a sequence goes to the caller's most recent one,
// and says so plainly when it has none (R45, Q19).
func TestAppendWithoutASequenceSaysSo(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	if _, err := f.svc.TimelineAppend(f.ctxOne, &helm.TimelineAppend{AssetID: a}); apiCode(err) != "409 no_timeline" {
		t.Fatalf("append with no sequence: %s, want 409 no_timeline", apiCode(err))
	}
	if _, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "first", Target: target(), Clips: clips(a)}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.TimelineAppend(f.ctxOne, &helm.TimelineAppend{AssetID: a})
	if err != nil {
		t.Fatalf("append to the studio's own sequence: %v", err)
	}
	if len(got.Tracks[0].Clips) != 2 {
		t.Errorf("%d clips after appending, want 2", len(got.Tracks[0].Clips))
	}
}

// An export left running by a helmstudio that was killed is recorded
// interrupted, and what it was writing is removed (M8 Q16).
func TestTheSweepEndsAnInterruptedExport(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "was rendering", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "export-work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "export.mp4"), []byte("half a render"), 0o600); err != nil {
		t.Fatal(err)
	}
	jobID := f.svc.newID()
	ctx := context.Background()
	if err := f.svc.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, created_at, started_at, work_path)
			VALUES (?, 'export', 'one', 'running', 'timeline', ?, 3, 48, ?, ?, ?)`, jobID, tl.ID, 1, 1, work)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.svc.SweepExports(ctx)
	page, err := f.svc.TimelineExports(f.ctxOne, tl.ID, helm.TimelineExportsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].State != helm.JobStateInterrupted {
		t.Fatalf("exports %+v, want one interrupted", page.Items)
	}
	if page.Items[0].LastError == nil || page.Items[0].LastError.Code != "interrupted" {
		t.Errorf("last error %+v, want interrupted", page.Items[0].LastError)
	}
	if _, err := os.Stat(work); err == nil {
		t.Error("what the export was writing is still there")
	}
}

// The document the rules were checked against is the asset store's; the file
// gets its say at export (M8 Q7's correction).
func TestARecordedDurationIsReCheckedAgainstTheFile(t *testing.T) {
	probes := timeline.Probes{}
	target := helm.TimelineTarget{Width: 320, Height: 180, FPS: 24, SampleRate: 48000}
	out := 5.0
	at := 0.0
	tracks := []helm.TimelineTrack{{Kind: timeline.KindVideo, Name: strPtr("V1"),
		Clips: []helm.TimelineClip{{AssetID: "a", In: &at, Out: &out, At: &at}}}}
	probes["a"] = timeline.Stream{Duration: 3, Codec: "h264"}
	if err := timeline.CheckAgainstProbes(target, tracks, probes); err == nil {
		t.Fatal("a clip running past the end of its file was accepted")
	}
	probes["a"] = timeline.Stream{Duration: 5, Codec: "h264"}
	if err := timeline.CheckAgainstProbes(target, tracks, probes); err != nil {
		t.Fatalf("a clip within its file was refused: %v", err)
	}
}

func strPtr(s string) *string { return &s }

// A parameter that carries an action is more specific than a bare one, and the
// router has to try it first: /timeline/{id} and /timeline/{id}:plan both match
// the same path, and reading the plan as an id ending in ":plan" refuses it for
// not looking like an id. Found by the conformance suite's route sweep.
func TestAnActionRouteWinsOverTheBareIDRoute(t *testing.T) {
	f := newTimelineFixture(t, noFFmpeg)
	a := f.upload(t, f.ctxOne, "a.mp4", helm.AssetKindVideo, 2)
	tl, err := f.svc.TimelineCreate(f.ctxOne, &helm.TimelineCreate{Name: "routed", Target: target(), Clips: clips(a)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(f.svc, func(r *http.Request) (context.Context, error) { return f.ctxOne, nil }, t.Logf)
	for _, tc := range []struct{ path, want string }{
		{"/api/v1/timeline/" + tl.ID, "200"},
		// The plan needs ffmpeg, which this service has none of: reaching 501
		// is what proves it was routed to the plan rather than to the read.
		{"/api/v1/timeline/" + tl.ID + ":plan", "501"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := strconv.Itoa(rec.Code); got != tc.want {
			t.Errorf("GET %s: %s %s, want %s", tc.path, got, rec.Body.String(), tc.want)
		}
	}
}
