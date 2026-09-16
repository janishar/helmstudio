package timeline

import (
	"math"
	"strings"
	"testing"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

func target(fps float64) helm.TimelineTarget {
	return helm.TimelineTarget{Width: 1920, Height: 1080, FPS: fps, SampleRate: 48000}
}

func sources() map[string]Source {
	return map[string]Source{
		"take1": {Kind: KindVideo, Duration: 5.175, StudioID: "h3-studio"},
		"take2": {Kind: KindVideo, Duration: 6.575, StudioID: "h3-studio"},
		"still": {Kind: KindImage, StudioID: "iris-studio"},
		"line":  {Kind: KindAudio, Duration: 3.2, StudioID: "auk-studio"},
		"long":  {Kind: KindVideo, Duration: 60, StudioID: "ltx-studio"},
	}
}

func video(clips ...helm.TimelineClip) helm.TimelineTrack {
	return helm.TimelineTrack{Kind: KindVideo, Clips: clips}
}

func audio(clips ...helm.TimelineClip) helm.TimelineTrack {
	return helm.TimelineTrack{Kind: KindAudio, Clips: clips}
}

func clip(asset string, fields ...func(*helm.TimelineClip)) helm.TimelineClip {
	c := helm.TimelineClip{AssetID: asset}
	for _, f := range fields {
		f(&c)
	}
	return c
}

func in(v float64) func(*helm.TimelineClip)   { return func(c *helm.TimelineClip) { c.In = &v } }
func out(v float64) func(*helm.TimelineClip)  { return func(c *helm.TimelineClip) { c.Out = &v } }
func at(v float64) func(*helm.TimelineClip)   { return func(c *helm.TimelineClip) { c.At = &v } }
func hold(v float64) func(*helm.TimelineClip) { return func(c *helm.TimelineClip) { c.Hold = &v } }
func gain(v float64) func(*helm.TimelineClip) { return func(c *helm.TimelineClip) { c.GainDb = &v } }

func dissolve(d float64) func(*helm.TimelineClip) {
	return func(c *helm.TimelineClip) { c.TransitionIn = &helm.TimelineTransition{Type: "dissolve", Duration: d} }
}

func normalize(t *testing.T, tg helm.TimelineTarget, tracks ...helm.TimelineTrack) []helm.TimelineTrack {
	t.Helper()
	out, err := Normalize(tg, tracks, sources())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return out
}

func faultCodes(err error) []string {
	var inv *Invalid
	if err == nil {
		return nil
	}
	if !asInvalid(err, &inv) {
		return []string{"not-invalid"}
	}
	var out []string
	for _, f := range inv.Faults {
		out = append(out, f.Code)
	}
	return out
}

func asInvalid(err error, dst **Invalid) bool {
	inv, ok := err.(*Invalid)
	if ok {
		*dst = inv
	}
	return ok
}

// Every time in a stored document lands on the target's grid, so that reading
// one and writing it back is not an edit (M8 Q7).
func TestTimesSnapToTheTargetsGrid(t *testing.T) {
	tg := target(24)
	// 5.04s is 120.96 frames at 24 fps: not a frame.
	got := normalize(t, tg, video(clip("take1", out(5.04)), clip("take2", out(2))))
	first := got[0].Clips[0]
	if *first.Out != 121.0/24 {
		t.Errorf("out snapped to %.6f, want %.6f (frame 121)", *first.Out, 121.0/24)
	}
	if *got[0].Clips[1].At != 121.0/24 {
		t.Errorf("the second clip starts at %.6f, want the first clip's end", *got[0].Clips[1].At)
	}
	// Writing the snapped document back changes nothing.
	again, err := Normalize(tg, got, sources())
	if err != nil {
		t.Fatalf("re-normalising: %v", err)
	}
	if *again[0].Clips[0].Out != *first.Out || *again[0].Clips[1].At != *got[0].Clips[1].At {
		t.Error("a second write moved times that were already on the grid")
	}
}

// 23.976 is 24000/1001, which no JSON number writes: the arithmetic has to use
// the ratio or every frame after the first drifts.
func TestNTSCRatesRoundTrip(t *testing.T) {
	tg := target(23.976)
	got := normalize(t, tg, video(clip("long", out(10))))
	frames := int64(math.Round(10 * 24000.0 / 1001.0))
	want := float64(frames) * 1001 / 24000
	if *got[0].Clips[0].Out != want {
		t.Errorf("out is %.9f, want %.9f (frame %d at 24000/1001)", *got[0].Clips[0].Out, want, frames)
	}
	if d := Duration(tg, got); d != want {
		t.Errorf("duration is %.9f, want %.9f", d, want)
	}
}

func TestTheVideoTrackHasNoGaps(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(
		clip("take1", out(2)),
		clip("take2", out(2), at(3)), // a second late
	)}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "not_contiguous" {
		t.Fatalf("faults %v, want one not_contiguous", codes)
	}
}

func TestAClipCannotRunPastItsSource(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("take1", out(9)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "trim" {
		t.Fatalf("faults %v, want one trim", codes)
	}
}

func TestAnAssetWithNoRecordedDurationNeedsAnOut(t *testing.T) {
	src := sources()
	src["take1"] = Source{Kind: KindVideo} // adopted with no duration hint
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("take1"))}, src)
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "out_required" {
		t.Fatalf("faults %v, want one out_required", codes)
	}
}

// An asset the caller may not read and one that does not exist are the same
// refusal, so a sequence cannot be used to ask what another studio has (Q11).
func TestAnUnreadableAssetIsTheSameFaultAsAMissingOne(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("somebody-elses", out(1)))}, sources())
	var inv *Invalid
	if !asInvalid(err, &inv) || len(inv.Faults) != 1 {
		t.Fatalf("want one fault, got %v", err)
	}
	if inv.Faults[0].Code != "asset_not_found" {
		t.Errorf("code %q, want asset_not_found", inv.Faults[0].Code)
	}
	if strings.Contains(inv.Faults[0].Message, "exist") || strings.Contains(inv.Faults[0].Message, "permission") {
		t.Errorf("the message says which it was: %q", inv.Faults[0].Message)
	}
}

func TestAnImageNeedsAHoldAndAVideoDoesNot(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("still"))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "hold_required" {
		t.Fatalf("faults %v, want one hold_required", codes)
	}
	_, err = Normalize(target(24), []helm.TimelineTrack{video(clip("take1", out(1), hold(2)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "hold_on_video" {
		t.Fatalf("faults %v, want one hold_on_video", codes)
	}
}

func TestSoundGoesOnAnAudioTrackAndPicturesDoNot(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("line", out(1)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "wrong_track" {
		t.Fatalf("faults %v, want one wrong_track", codes)
	}
	_, err = Normalize(target(24), []helm.TimelineTrack{video(clip("take1", out(2))), audio(clip("take2", out(1)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "wrong_track" {
		t.Fatalf("faults %v, want one wrong_track", codes)
	}
}

func TestSoundMayNotRunPastThePicture(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{
		video(clip("take1", out(2))),
		audio(clip("line", out(3.2))),
	}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "past_end" {
		t.Fatalf("faults %v, want one past_end", codes)
	}
}

func TestSoundClipsOnOneTrackMayNotOverlap(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{
		video(clip("long", out(20))),
		audio(clip("line", out(3)), clip("line", out(3), at(1))),
	}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "overlap" {
		t.Fatalf("faults %v, want one overlap", codes)
	}
}

// A dissolve is centred on the cut and paid for with handles, so adding one
// moves no clip and the sequence keeps its length (M8 Q8).
func TestADissolveNeedsHandlesAndMovesNothing(t *testing.T) {
	tg := target(24)
	got := normalize(t, tg, video(
		clip("take1", out(4)),
		clip("take2", in(1), out(5), dissolve(0.5)),
	))
	if *got[0].Clips[1].At != 4 {
		t.Errorf("the second clip starts at %v, want 4: a dissolve moves nothing", *got[0].Clips[1].At)
	}
	if d := Duration(tg, got); d != 8 {
		t.Errorf("duration %v, want 8", d)
	}
	// The first clip has 1.175s beyond its out; a 4s dissolve needs 2s of it.
	_, err := Normalize(tg, []helm.TimelineTrack{video(
		clip("take1", out(4)),
		clip("take2", in(3), out(6), dissolve(4)),
	)}, sources())
	if codes := faultCodes(err); len(codes) == 0 {
		t.Fatal("a dissolve longer than the clip it starts was accepted")
	}
	// The first clip cannot dissolve from nothing.
	_, err = Normalize(tg, []helm.TimelineTrack{video(clip("take1", out(4), dissolve(0.5)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "transition_first" {
		t.Fatalf("faults %v, want one transition_first", codes)
	}
}

// Half a dissolve must be a whole frame, or its two sides land between frames.
func TestADissolveIsAnEvenNumberOfFrames(t *testing.T) {
	tg := target(24)
	got := normalize(t, tg, video(
		clip("long", out(4)),
		clip("long", in(2), out(6), dissolve(5.0/24)), // five frames
	))
	d := got[0].Clips[1].TransitionIn.Duration
	if frames := int64(math.Round(d * 24)); frames%2 != 0 {
		t.Errorf("the dissolve is %d frames, which has no middle", frames)
	}
}

func TestTracksAreNamedByKindAndPosition(t *testing.T) {
	got := normalize(t, target(24), video(clip("take1", out(2))), audio(clip("line", out(1))), audio(clip("line", out(1))))
	for i, want := range []string{"V1", "A1", "A2"} {
		if got[i].Name == nil || *got[i].Name != want {
			t.Errorf("track %d is %v, want %s", i, got[i].Name, want)
		}
	}
}

func TestOnlyOneVideoTrack(t *testing.T) {
	_, err := Normalize(target(24), []helm.TimelineTrack{video(clip("take1", out(1))), video(clip("take2", out(1)))}, sources())
	if codes := faultCodes(err); len(codes) != 1 || codes[0] != "tracks" {
		t.Fatalf("faults %v, want one tracks", codes)
	}
}

func TestClipsAreLaidEndToEnd(t *testing.T) {
	tracks, err := Lay([]helm.TimelineClip{clip("take1", out(2)), clip("line", out(1)), clip("take2", out(3))}, sources())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Normalize(target(24), tracks, sources())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != KindVideo || got[1].Kind != KindAudio {
		t.Fatalf("tracks %v, want a video track then an audio one", got)
	}
	if *got[0].Clips[1].At != 2 {
		t.Errorf("the second video clip starts at %v, want 2", *got[0].Clips[1].At)
	}
	if *got[1].Clips[0].At != 0 {
		t.Errorf("the sound starts at %v, want 0", *got[1].Clips[0].At)
	}
}

func TestDurationIsTheVideoTracksEnd(t *testing.T) {
	tg := target(25)
	got := normalize(t, tg, video(clip("take1", out(4)), clip("still", hold(2))), audio(clip("line", out(1))))
	if d := Duration(tg, got); d != 6 {
		t.Errorf("duration %v, want 6", d)
	}
	if f := Frames(tg, got); f != 150 {
		t.Errorf("frames %d, want 150", f)
	}
	if s := Samples(tg, got); s != 6*48000 {
		t.Errorf("samples %d, want %d", s, 6*48000)
	}
}

func TestATargetMustBeOneThePipelineCanRender(t *testing.T) {
	for _, tc := range []struct {
		name string
		tg   helm.TimelineTarget
	}{
		{"odd size", helm.TimelineTarget{Width: 1921, Height: 1080, FPS: 24, SampleRate: 48000}},
		{"unknown rate", helm.TimelineTarget{Width: 1920, Height: 1080, FPS: 23.5, SampleRate: 48000}},
		{"unknown sample rate", helm.TimelineTarget{Width: 1920, Height: 1080, FPS: 24, SampleRate: 32000}},
	} {
		if err := CheckTarget(tc.tg); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	if err := CheckTarget(target(24)); err != nil {
		t.Errorf("a plain target was refused: %v", err)
	}
}

func TestTargetFillsTheDefaultSampleRate(t *testing.T) {
	got, err := Target(helm.TimelineTargetInput{Width: 1920, Height: 1080, FPS: 24})
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 48000 {
		t.Errorf("sample rate %d, want 48000", got.SampleRate)
	}
}
