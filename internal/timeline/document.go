// Package timeline is the sequence document: the rules it must satisfy, the
// grid it snaps to, and the two decisions an export rests on — whether the
// picture may be copied, and what ffmpeg is asked to do (R44–R47,
// docs/design/05-sdk-and-custom-studios.md §6, docs/decisions.md M8 Q7–Q15).
//
// Everything here is a pure function over the document, the facts the asset
// store recorded, and — for an export — the facts ffprobe found. Nothing here
// runs a command or touches a file: internal/export does that. That split is
// what lets the gate check the rules and the filter graph without a model, a
// GPU or a person watching.
package timeline

import (
	"fmt"
	"math"
	"sort"
	"strings"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Kinds a clip's asset may be.
const (
	KindImage = "image"
	KindVideo = "video"
	KindAudio = "audio"
)

// MaxAudioTracks is how many sound tracks a sequence may carry (M8 Q8).
const MaxAudioTracks = 8

// Source is what the rules need to know about a clip's asset: what it is, how
// long the asset store recorded it to be, and whose it is. The duration is a
// recorded fact, which may be a hint the studio gave at adopt time (M4 Q13);
// an export probes the file again and refuses a document the probe contradicts
// (M8 Q7).
type Source struct {
	Kind     string
	Duration float64
	StudioID string
}

// Fault is one thing wrong with a document, at the clip it is wrong at. An
// asset that is missing and one the caller may not read give the same fault,
// so a refusal never says which (M8 Q11).
type Fault struct {
	Track   string `json:"track,omitempty"`
	Clip    int    `json:"clip,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Invalid is every fault in one document.
type Invalid struct{ Faults []Fault }

func (e *Invalid) Error() string {
	parts := make([]string, 0, len(e.Faults))
	for _, f := range e.Faults {
		where := ""
		switch {
		case f.Track != "" && f.Clip >= 0:
			where = fmt.Sprintf("%s clip %d: ", f.Track, f.Clip+1)
		case f.Track != "":
			where = f.Track + ": "
		}
		parts = append(parts, where+f.Message)
	}
	return strings.Join(parts, "; ")
}

type faults struct {
	list  []Fault
	track string
	clip  int
}

func (f *faults) at(track string, clip int) { f.track, f.clip = track, clip }

func (f *faults) add(code, format string, args ...any) {
	f.list = append(f.list, Fault{Track: f.track, Clip: f.clip, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (f *faults) err() error {
	if len(f.list) == 0 {
		return nil
	}
	return &Invalid{Faults: f.list}
}

// Target checks a target as a request gave it and fills its default sample
// rate (M8 Q9).
func Target(in helm.TimelineTargetInput) (helm.TimelineTarget, error) {
	out := helm.TimelineTarget{Width: in.Width, Height: in.Height, FPS: in.FPS, SampleRate: 48000}
	if in.SampleRate != nil {
		out.SampleRate = *in.SampleRate
	}
	return out, CheckTarget(out)
}

// CheckTarget refuses a target the pipeline cannot render to.
func CheckTarget(t helm.TimelineTarget) error {
	var f faults
	f.at("", -1)
	if t.Width%2 != 0 || t.Height%2 != 0 {
		f.add("target", "the target's width and height must be even; %dx%d is not", t.Width, t.Height)
	}
	if t.Width < 2 || t.Width > 8192 || t.Height < 2 || t.Height > 8192 {
		f.add("target", "the target's width and height must be between 2 and 8192")
	}
	if _, ok := RateOf(t.FPS); !ok {
		f.add("target", "%g is not a frame rate the daemon knows exactly; use one of %s", t.FPS, rateList())
	}
	if t.SampleRate != 44100 && t.SampleRate != 48000 {
		f.add("target", "the target's sample rate must be 44100 or 48000")
	}
	return f.err()
}

// Normalize checks a document against the rules and returns it as it will be
// stored: tracks named, defaults filled, and every time snapped to the target's
// grid, so writing the result back changes nothing (M8 Q7).
func Normalize(target helm.TimelineTarget, tracks []helm.TimelineTrack, sources map[string]Source) ([]helm.TimelineTrack, error) {
	if err := CheckTarget(target); err != nil {
		return nil, err
	}
	rate, _ := RateOf(target.FPS)
	var f faults
	f.at("", -1)

	video, audio := -1, 0
	for i, tr := range tracks {
		switch tr.Kind {
		case KindVideo:
			if video >= 0 {
				f.add("tracks", "a sequence has one video track; this one has %d", video+2)
				continue
			}
			video = i
		case KindAudio:
			audio++
		default:
			f.add("tracks", "a track is video or audio, not %q", tr.Kind)
		}
	}
	if audio > MaxAudioTracks {
		f.add("tracks", "a sequence has at most %d audio tracks; this one has %d", MaxAudioTracks, audio)
	}
	if err := f.err(); err != nil {
		return nil, err
	}

	out := make([]helm.TimelineTrack, len(tracks))
	audioSeen := 0
	for i, tr := range tracks {
		name := "V1"
		if tr.Kind == KindAudio {
			audioSeen++
			name = fmt.Sprintf("A%d", audioSeen)
		}
		out[i] = helm.TimelineTrack{Kind: tr.Kind, Name: strptr(name), GainDb: tr.GainDb, Clips: make([]helm.TimelineClip, len(tr.Clips))}
		if tr.GainDb != nil {
			if g := *tr.GainDb; g < -60 || g > 12 {
				f.at(name, -1)
				f.add("gain", "a gain is between -60 and 12 dB; %g is not", g)
			}
		}
		end := 0.0
		for j, c := range tr.Clips {
			f.at(name, j)
			clip, length := normalizeClip(&f, rate, target, tr.Kind, c, sources, end, j)
			out[i].Clips[j] = clip
			// atOf, not *clip.At: a clip that faulted never got one.
			end = atOf(clip) + length
		}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	// Sound may sit anywhere inside the sequence, but not past its end.
	duration := Duration(target, out)
	for i, tr := range out {
		if tr.Kind != KindAudio {
			continue
		}
		for j, c := range tr.Clips {
			f.at(*out[i].Name, j)
			if end := atOf(c) + clipLength(c); end > duration+halfFrame(rate) {
				f.add("past_end", "this clip ends at %.3fs, past the sequence's %.3fs", end, duration)
			}
		}
	}
	return out, f.err()
}

// normalizeClip fills a clip's defaults, snaps its times and checks its rules.
// prevEnd is where the track has reached, and index the clip's place in it.
func normalizeClip(f *faults, rate Rate, target helm.TimelineTarget, trackKind string, c helm.TimelineClip, sources map[string]Source, prevEnd float64, index int) (helm.TimelineClip, float64) {
	src, known := sources[c.AssetID]
	if !known {
		// One wording whether the asset is gone or simply not this caller's.
		f.add("asset_not_found", "no asset %s you can use", c.AssetID)
		return c, 0
	}
	out := helm.TimelineClip{AssetID: c.AssetID, GainDb: c.GainDb, TransitionIn: c.TransitionIn}
	if src.StudioID != "" {
		out.StudioID = strptr(src.StudioID)
	}
	switch {
	case trackKind == KindVideo && src.Kind != KindVideo && src.Kind != KindImage:
		f.add("wrong_track", "a %s asset belongs on an audio track", src.Kind)
		return out, 0
	case trackKind == KindAudio && src.Kind != KindAudio:
		f.add("wrong_track", "a %s asset belongs on the video track", src.Kind)
		return out, 0
	}
	if c.GainDb != nil {
		if g := *c.GainDb; g < -60 || g > 12 {
			f.add("gain", "a gain is between -60 and 12 dB; %g is not", g)
		}
	}
	if c.Audio != nil {
		if src.Kind != KindVideo {
			f.add("audio_flag", "only a video clip has sound of its own to turn off")
		} else {
			out.Audio = c.Audio
		}
	}

	var length float64
	if src.Kind == KindImage {
		switch {
		case c.In != nil || c.Out != nil:
			f.add("image_trim", "an image has no in or out point; give it a hold")
		case c.Hold == nil:
			f.add("hold_required", "an image needs a hold: how long it stays on screen")
		default:
			frames := rate.Frames(*c.Hold)
			if frames < 1 {
				frames = 1
			}
			length = rate.Seconds(frames)
			out.Hold = f64ptr(length)
		}
	} else {
		if c.Hold != nil {
			f.add("hold_on_video", "a hold is for an image; a video or sound clip is trimmed with in and out")
		}
		in, out2, ok := trimOf(f, rate, target, src, c, trackKind)
		if ok {
			out.In, out.Out = f64ptr(in), f64ptr(out2)
			length = out2 - in
		}
	}

	at := prevEnd
	if c.At != nil {
		at = rate.Seconds(rate.Frames(*c.At))
		if trackKind == KindVideo && math.Abs(at-prevEnd) > halfFrame(rate) {
			f.add("not_contiguous", "the video track has no gaps: this clip starts at %.3fs, where %.3fs is expected", at, prevEnd)
		}
		if trackKind == KindAudio && at < prevEnd-halfFrame(rate) {
			f.add("overlap", "this clip starts at %.3fs, over the one before it, which ends at %.3fs", at, prevEnd)
		}
	}
	out.At = f64ptr(at)

	if c.TransitionIn != nil {
		checkTransition(f, rate, trackKind, index, c, length)
		if d := rate.EvenFrames(c.TransitionIn.Duration); d > 0 {
			out.TransitionIn = &helm.TimelineTransition{Type: c.TransitionIn.Type, Duration: rate.Seconds(d)}
		}
	}
	return out, length
}

// trimOf reads a clip's in and out points, snapped: frames for a video clip,
// samples for one on an audio track (M8 Q7).
func trimOf(f *faults, rate Rate, target helm.TimelineTarget, src Source, c helm.TimelineClip, trackKind string) (in, out float64, ok bool) {
	snap := func(v float64) float64 { return rate.Seconds(rate.Frames(v)) }
	if trackKind == KindAudio {
		snap = func(v float64) float64 {
			return float64(int64(math.Round(v*float64(target.SampleRate)))) / float64(target.SampleRate)
		}
	}
	in = 0
	if c.In != nil {
		in = snap(*c.In)
	}
	switch {
	case c.Out != nil:
		out = snap(*c.Out)
	case src.Duration > 0:
		out = snap(src.Duration)
	default:
		f.add("out_required", "out is required: no duration is recorded for this asset")
		return 0, 0, false
	}
	if in < 0 {
		f.add("trim", "in is before the start of the clip")
		return 0, 0, false
	}
	if out <= in {
		f.add("trim", "out (%.3fs) must be after in (%.3fs)", out, in)
		return 0, 0, false
	}
	if src.Duration > 0 && out > src.Duration+halfFrame(rate) {
		f.add("trim", "out (%.3fs) is past the end of a clip %.3fs long", out, src.Duration)
		return 0, 0, false
	}
	return in, out, true
}

// checkTransition holds the one transition to what the design allows: on the
// video track, never on the first clip, centred on the cut and paid for with
// handles from both sides (01 §14, M8 Q8).
func checkTransition(f *faults, rate Rate, trackKind string, index int, c helm.TimelineClip, length float64) {
	t := c.TransitionIn
	if trackKind != KindVideo {
		f.add("transition_track", "a transition belongs on the video track")
		return
	}
	if t.Type != "dissolve" {
		f.add("transition_type", "the one transition is a dissolve, not %q", t.Type)
		return
	}
	if index == 0 {
		f.add("transition_first", "the first clip has nothing to dissolve from")
		return
	}
	frames := rate.EvenFrames(t.Duration)
	if frames <= 0 {
		f.add("transition_length", "a dissolve is at least two frames long")
		return
	}
	if d := rate.Seconds(frames); d > length+halfFrame(rate) {
		f.add("transition_length", "a %.3fs dissolve is longer than the %.3fs clip it starts", d, length)
	}
}

// Duration is where the sequence ends: the video track's end, or the last
// sound to stop when it has no video track.
func Duration(target helm.TimelineTarget, tracks []helm.TimelineTrack) float64 {
	rate, ok := RateOf(target.FPS)
	if !ok {
		return 0
	}
	end := 0.0
	for _, tr := range tracks {
		if tr.Kind != KindVideo {
			continue
		}
		for _, c := range tr.Clips {
			end = math.Max(end, atOf(c)+clipLength(c))
		}
		return rate.Seconds(rate.Frames(end))
	}
	for _, tr := range tracks {
		for _, c := range tr.Clips {
			end = math.Max(end, atOf(c)+clipLength(c))
		}
	}
	return rate.Seconds(rate.Frames(end))
}

// Frames is the sequence's length in whole frames of the target.
func Frames(target helm.TimelineTarget, tracks []helm.TimelineTrack) int64 {
	rate, ok := RateOf(target.FPS)
	if !ok {
		return 0
	}
	return rate.Frames(Duration(target, tracks))
}

// Samples is the sequence's length in whole samples of the target, which is
// what an export's sound must come to exactly (M8 Q15).
func Samples(target helm.TimelineTarget, tracks []helm.TimelineTrack) int64 {
	return int64(math.Round(Duration(target, tracks) * float64(target.SampleRate)))
}

// Lay puts loose clips end to end from 0: video and images on V1, sound on A1
// (05 §7's create form).
func Lay(clips []helm.TimelineClip, sources map[string]Source) ([]helm.TimelineTrack, error) {
	var video, audio []helm.TimelineClip
	var f faults
	for i, c := range clips {
		src, ok := sources[c.AssetID]
		if !ok {
			f.at("", i)
			f.add("asset_not_found", "no asset %s you can use", c.AssetID)
			continue
		}
		c.At = nil // laid end to end, whatever a caller sent
		switch src.Kind {
		case KindAudio:
			audio = append(audio, c)
		default:
			video = append(video, c)
		}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	var out []helm.TimelineTrack
	if len(video) > 0 {
		out = append(out, helm.TimelineTrack{Kind: KindVideo, Clips: video})
	}
	if len(audio) > 0 {
		out = append(out, helm.TimelineTrack{Kind: KindAudio, Clips: audio})
	}
	return out, nil
}

// Assets lists every asset the document names, in the order it first appears.
func Assets(tracks []helm.TimelineTrack) []string {
	var out []string
	seen := map[string]bool{}
	for _, tr := range tracks {
		for _, c := range tr.Clips {
			if !seen[c.AssetID] {
				seen[c.AssetID] = true
				out = append(out, c.AssetID)
			}
		}
	}
	return out
}

// TrackNamed finds a track by the name Normalize gave it.
func TrackNamed(tracks []helm.TimelineTrack, name string) int {
	for i, tr := range tracks {
		if tr.Name != nil && *tr.Name == name {
			return i
		}
	}
	return -1
}

// ClipCount is how many clips a document holds, for a revision's summary.
func ClipCount(tracks []helm.TimelineTrack) int64 {
	var n int64
	for _, tr := range tracks {
		n += int64(len(tr.Clips))
	}
	return n
}

func clipLength(c helm.TimelineClip) float64 {
	switch {
	case c.Hold != nil:
		return *c.Hold
	case c.In != nil && c.Out != nil:
		return *c.Out - *c.In
	case c.Out != nil:
		return *c.Out
	}
	return 0
}

func atOf(c helm.TimelineClip) float64 {
	if c.At != nil {
		return *c.At
	}
	return 0
}

func halfFrame(r Rate) float64 { return r.Seconds(1) / 2 }

func strptr(s string) *string { return &s }

func f64ptr(v float64) *float64 { return &v }

func rateList() string {
	var out []string
	for fps := range rates {
		out = append(out, fmt.Sprintf("%g", fps))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
