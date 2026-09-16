package conformance

import (
	"bytes"
	"testing"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// The timeline, against both providers (R44–R49, 05 §6, §7; M8 Q7–Q19). The
// export pipeline itself needs real media and ffmpeg, and is checked in
// test/media; what these cases hold is the contract every provider serves:
// whose sequence is whose, what a write does to it, and what degrades.

func target() helm.TimelineTargetInput {
	return helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24}
}

// video uploads something the store will call a video, with a duration, so a
// document may name it without an out point.
func videoAsset(t *testing.T, c *helm.Client, name string) *helm.Asset {
	t.Helper()
	a, err := c.Assets.Upload(ctx, bytes.NewReader([]byte("fixture "+name)), "video/mp4",
		&helm.AssetsUploadParams{Kind: helm.AssetKindVideo, Filename: ptr(name), DurationS: ptr(2.0), FPS: ptr(24.0)})
	noErr(t, err)
	return a
}

func TestTimelineStoresWhatItWillRender(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "take-a.mp4")
		b := videoAsset(t, c, "take-b.mp4")
		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "a sequence", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: a.ID}, {AssetID: b.ID}}})
		noErr(t, err)

		if len(tl.Tracks) != 1 || tl.Tracks[0].Name == nil || *tl.Tracks[0].Name != "V1" {
			t.Fatalf("tracks %+v, want one named V1", tl.Tracks)
		}
		clips := tl.Tracks[0].Clips
		if len(clips) != 2 || clips[0].At == nil || *clips[0].At != 0 || clips[1].At == nil || *clips[1].At != 2 {
			t.Fatalf("clips are not laid end to end: %+v", clips)
		}
		if tl.Target.SampleRate != 48000 {
			t.Errorf("sample rate %d, want the default 48000", tl.Target.SampleRate)
		}
		if tl.DurationS != 4 {
			t.Errorf("duration %v, want 4", tl.DurationS)
		}
		if tl.StudioID == nil || *tl.StudioID != T {
			t.Errorf("studio %v, want %s", tl.StudioID, T)
		}
		// A clip carries the studio whose asset it is, for its hue and name.
		if clips[0].StudioID == nil || *clips[0].StudioID != T {
			t.Errorf("clip studio %v, want %s", clips[0].StudioID, T)
		}
		got, err := c.Timeline.Get(ctx, tl.ID)
		noErr(t, err)
		if got.Revision != tl.Revision || got.ETag != tl.ETag {
			t.Errorf("reading it back gives revision %d etag %q, want %d %q", got.Revision, got.ETag, tl.Revision, tl.ETag)
		}
	})
}

func TestTimelineWritesNeedTheRevisionTheyRead(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "take.mp4")
		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "first", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: a.ID}}})
		noErr(t, err)

		renamed, err := c.Timeline.Update(ctx, tl.ID, map[string]any{"name": "second"}, &helm.TimelineUpdateParams{IfMatch: tl.ETag})
		noErr(t, err)
		if renamed.Name != "second" || renamed.Revision != tl.Revision+1 {
			t.Fatalf("after a rename: %q at revision %d", renamed.Name, renamed.Revision)
		}
		_, err = c.Timeline.Update(ctx, tl.ID, map[string]any{"name": "third"}, &helm.TimelineUpdateParams{IfMatch: tl.ETag})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")

		// A target merges: the frame rate changes without resending the size.
		retimed, err := c.Timeline.Update(ctx, tl.ID, map[string]any{"target": map[string]any{"fps": 25}},
			&helm.TimelineUpdateParams{IfMatch: renamed.ETag})
		noErr(t, err)
		if retimed.Target.FPS != 25 || retimed.Target.Width != 320 {
			t.Errorf("target %+v, want 25 fps at the same size", retimed.Target)
		}

		// Every write keeps the document it replaced, and undo writes one of
		// them forward rather than rewinding.
		revs, err := c.Timeline.Revisions(ctx, tl.ID, nil)
		noErr(t, err)
		if len(revs.Items) != 2 {
			t.Fatalf("%d revisions, want the two that were replaced", len(revs.Items))
		}
		back, err := c.Timeline.Revert(ctx, tl.ID, helm.TimelineRevert{Revision: tl.Revision}, &helm.TimelineRevertParams{IfMatch: retimed.ETag})
		noErr(t, err)
		if back.Name != "first" || back.Target.FPS != 24 {
			t.Errorf("undo gave %q at %v fps, want the first document", back.Name, back.Target.FPS)
		}
		if back.Revision != retimed.Revision+1 {
			t.Errorf("undo is revision %d; it should write forward from %d", back.Revision, retimed.Revision)
		}
	})
}

func TestTimelineAppendGoesToTheCallersOwnSequence(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "one.mp4")
		_, err := c.Timeline.Append(ctx, helm.TimelineAppend{AssetID: a.ID})
		wantErr(t, err, helm.KindConflict, "no_timeline")

		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "the open one", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: a.ID}}})
		noErr(t, err)
		got, err := c.Timeline.Append(ctx, helm.TimelineAppend{AssetID: a.ID})
		noErr(t, err)
		if got.ID != tl.ID || len(got.Tracks[0].Clips) != 2 {
			t.Fatalf("appended to %s with %d clips, want %s with 2", got.ID, len(got.Tracks[0].Clips), tl.ID)
		}
	})
}

// A sequence is the studio's own. Another studio is told what it would be told
// about one that does not exist, and a clip may only name an asset the caller
// can read (M8 Q11).
func TestTimelineIsolationBetweenStudios(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		mine := videoAsset(t, e.C(T), "mine.mp4")
		tl, err := e.C(T).Timeline.Create(ctx, helm.TimelineCreate{Name: "mine", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: mine.ID}}})
		noErr(t, err)

		_, err = e.C(U).Timeline.Get(ctx, tl.ID)
		wantErr(t, err, helm.KindNotFound, "")
		_, err = e.C(U).Timeline.Update(ctx, tl.ID, map[string]any{"name": "yours"}, &helm.TimelineUpdateParams{IfMatch: tl.ETag})
		wantErr(t, err, helm.KindNotFound, "")
		list, err := e.C(U).Timeline.List(ctx, nil)
		noErr(t, err)
		if len(list.Items) != 0 {
			t.Errorf("another studio lists %d sequences, want none", len(list.Items))
		}

		// gallery.read_all is "read everything you have ever made", so it does
		// reach them.
		if _, err := e.C(R).Timeline.Get(ctx, tl.ID); err != nil {
			t.Errorf("a studio holding gallery.read_all was refused: %v", err)
		}

		// A clip it cannot read is refused, and the refusal never says whether
		// the asset exists.
		_, err = e.C(U).Timeline.Create(ctx, helm.TimelineCreate{Name: "sneaky", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: mine.ID}}})
		wantErr(t, err, helm.KindInvalid, "invalid_timeline")
	})
}

func TestTimelineNeedsItsCapability(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		_, err := e.C(C).Timeline.Create(ctx, helm.TimelineCreate{Name: "no capability", Target: target()})
		wantErr(t, err, helm.KindForbidden, "capability_required")
		_, err = e.C(C).Timeline.List(ctx, nil)
		wantErr(t, err, helm.KindForbidden, "capability_required")
	})
}

// The data operation always works and only the presentation degrades: :open
// answers 501 until a framework surface exists to open it in (R45, 05 §5a).
func TestTimelineOpenDegrades(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "take.mp4")
		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "open me", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: a.ID}}})
		noErr(t, err)
		_, err = c.Timeline.Open(ctx, tl.ID)
		wantErr(t, err, helm.KindUnsupported, "unsupported")
	})
}

// A sequence with no picture has nothing to export, and says so before ffmpeg
// is even looked for.
func TestExportingASequenceWithNoPictureIsRefused(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "empty", Target: target()})
		noErr(t, err)
		_, err = c.Timeline.Export(ctx, tl.ID, helm.ExportRequest{Preset: helm.ExportPresetH264})
		wantErr(t, err, helm.KindInvalid, "invalid_timeline")
		page, err := c.Timeline.Exports(ctx, tl.ID, nil)
		noErr(t, err)
		if len(page.Items) != 0 {
			t.Errorf("%d export jobs, want none", len(page.Items))
		}
	})
}

// Deleting is a soft delete: the sequence stops being listed and readable, and
// what it exported keeps naming it (M8 Q12).
func TestDeletingASequence(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "take.mp4")
		tl, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "going", Target: target(),
			Clips: []helm.TimelineClip{{AssetID: a.ID}}})
		noErr(t, err)
		noErr(t, c.Timeline.Delete(ctx, tl.ID, &helm.TimelineDeleteParams{IfMatch: ptr(tl.ETag)}))
		_, err = c.Timeline.Get(ctx, tl.ID)
		wantErr(t, err, helm.KindNotFound, "")
		list, err := c.Timeline.List(ctx, nil)
		noErr(t, err)
		for _, it := range list.Items {
			if it.ID == tl.ID {
				t.Error("a deleted sequence is still listed")
			}
		}
	})
}

// A document that breaks the rules is refused with the clip named, and nothing
// is stored.
func TestAnInvalidDocumentIsRefusedWithTheClipNamed(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(T)
		a := videoAsset(t, c, "take.mp4")
		// The asset is 2s long, and the clip runs to 9s.
		_, err := c.Timeline.Create(ctx, helm.TimelineCreate{Name: "too long", Target: target(),
			Tracks: []helm.TimelineTrack{{Kind: "video", Clips: []helm.TimelineClip{{AssetID: a.ID, Out: ptr(9.0)}}}}})
		wantErr(t, err, helm.KindInvalid, "invalid_timeline")
		list, err := c.Timeline.List(ctx, nil)
		noErr(t, err)
		if len(list.Items) != 0 {
			t.Errorf("%d sequences were stored by a refused create", len(list.Items))
		}
	})
}
