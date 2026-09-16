package timeline

import (
	"fmt"
	"math"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Export modes. Copy copies the picture; nothing ever copies sound, because a
// copied AAC stream carries its own priming and padding into every cut — two
// real h3 takes joined that way put the second take's sound 81 ms behind its
// picture (docs/agents/reports/08-timeline-and-export.md, "What I measured").
const (
	ModeCopy    = "copy"
	ModeConform = "conform"
)

// Stream is what ffprobe found about one clip's file: every fact the fast path
// has to compare, and nothing it does not. It is read from the file at export
// time and never from what a studio said when it adopted the asset (M8 Q6).
type Stream struct {
	Container      string // the demuxer's name: mov,mp4,m4a,3gp,3g2,mj2 for an MP4
	Codec          string
	Profile        string
	Level          int64
	Width          int64
	Height         int64
	PixelFormat    string
	FieldOrder     string
	SampleAspect   string // "1:1", or "" when the file sets none
	TimeBase       string
	FrameRate      Rate
	ColorRange     string
	ColorSpace     string
	ColorPrimaries string
	ColorTransfer  string
	// ParameterSets is a digest of the codec's extradata — H.264's SPS and PPS.
	// Two files can agree on codec, profile, level, size and rate and still
	// differ here, and a player that reads one set for the whole file then
	// decodes the rest wrongly. Among h3's takes it is equal only within one
	// size.
	ParameterSets string
	Duration      float64

	HasAudio   bool
	AudioCodec string
	SampleRate int64
	Channels   int64
}

// Probes is what was found for each asset the document names.
type Probes map[string]Stream

// containersThatCopy are the containers the concat demuxer joins by copying.
var containersThatCopy = map[string]bool{
	"mov,mp4,m4a,3gp,3g2,mj2": true,
	"mp4":                     true,
	"mov":                     true,
}

// PixelFormat and profile the h264 preset encodes to. A copy has to match
// them, so that a sequence exported cheaply and one exported the slow way are
// the same kind of file.
const (
	presetPixelFormat = "yuv420p"
	presetCodec       = "h264"
)

// Plan decides whether an export may copy the picture, and says why not for
// every clip that stops it (R47, M8 Q13). It compares only probed facts.
func Plan(target helm.TimelineTarget, tracks []helm.TimelineTrack, probes Probes) (mode string, reasons []helm.ExportReason) {
	rate, ok := RateOf(target.FPS)
	if !ok {
		return ModeConform, []helm.ExportReason{{Code: "frame_rate", Message: fmt.Sprintf("%g is not a frame rate the daemon knows exactly", target.FPS)}}
	}
	vi := TrackNamed(tracks, "V1")
	if vi < 0 || len(tracks[vi].Clips) == 0 {
		return ModeConform, []helm.ExportReason{{Code: "no_video", Message: "the sequence has no video clip"}}
	}
	clips := tracks[vi].Clips

	add := func(index int, code, format string, args ...any) {
		n := int64(index + 1)
		v1 := "V1"
		reasons = append(reasons, helm.ExportReason{Code: code, Message: fmt.Sprintf(format, args...), Track: &v1, Clip: &n})
	}

	var first *Stream
	for i, c := range clips {
		p, known := probes[c.AssetID]
		if !known {
			add(i, "not_probed", "this clip's file could not be probed")
			continue
		}
		switch {
		case c.Hold != nil:
			add(i, "image", "a still is drawn, not copied")
			continue
		case c.TransitionIn != nil:
			add(i, "transition", "a dissolve has to be rendered")
		}
		if !containersThatCopy[p.Container] {
			add(i, "container", "the concat demuxer copies MP4 and MOV; this clip is %s", p.Container)
		}
		if p.Codec != presetCodec {
			add(i, "codec", "the clip is %s and the preset is %s", p.Codec, presetCodec)
		}
		if p.PixelFormat != presetPixelFormat {
			add(i, "pixel_format", "the clip is %s and the preset writes %s", p.PixelFormat, presetPixelFormat)
		}
		if p.Width != target.Width || p.Height != target.Height {
			add(i, "size", "the clip is %dx%d and the target is %dx%d", p.Width, p.Height, target.Width, target.Height)
		}
		if p.SampleAspect != "" && p.SampleAspect != "1:1" {
			add(i, "aspect", "the clip's sample aspect ratio is %s, not square", p.SampleAspect)
		}
		if !progressive(p.FieldOrder) {
			add(i, "field_order", "the clip is %s, not progressive", p.FieldOrder)
		}
		if !p.FrameRate.Equal(rate) {
			add(i, "frame_rate", "the clip runs at %s and the target at %s", p.FrameRate, rate)
		}
		if !wholeClip(c, p, rate) {
			add(i, "trimmed", "a copy cuts at packets, not frames, so only a whole clip is copied")
		}
		if first == nil {
			s := p
			first = &s
			continue
		}
		// Everything below has to hold between clips, or the file plays as its
		// first clip and decodes the rest wrongly.
		if p.Profile != first.Profile {
			add(i, "profile", "this clip is %s and the first is %s", p.Profile, first.Profile)
		}
		if p.Level != first.Level {
			add(i, "level", "this clip is level %d and the first is level %d", p.Level, first.Level)
		}
		if p.TimeBase != first.TimeBase {
			add(i, "time_base", "this clip's time base is %s and the first is %s", p.TimeBase, first.TimeBase)
		}
		if p.PixelFormat != first.PixelFormat {
			add(i, "pixel_format", "this clip is %s and the first is %s", p.PixelFormat, first.PixelFormat)
		}
		if p.ColorRange != first.ColorRange || p.ColorSpace != first.ColorSpace ||
			p.ColorPrimaries != first.ColorPrimaries || p.ColorTransfer != first.ColorTransfer {
			add(i, "colour", "this clip's colour is %s and the first's is %s; untagged counts as a value", colour(p), colour(*first))
		}
		if p.ParameterSets != first.ParameterSets {
			add(i, "parameter_sets", "this clip's H.264 parameter sets differ from the first's, so a player set up for one would decode the other wrongly")
		}
	}
	if len(reasons) > 0 {
		return ModeConform, reasons
	}
	return ModeCopy, nil
}

// wholeClip reports whether the clip is used from its first frame to its last.
// A copy cuts at packet boundaries, so a trim is frame-accurate only when
// there is none.
func wholeClip(c helm.TimelineClip, p Stream, rate Rate) bool {
	in, out := 0.0, p.Duration
	if c.In != nil {
		in = *c.In
	}
	if c.Out != nil {
		out = *c.Out
	}
	if p.Duration <= 0 {
		return false
	}
	return math.Abs(in) <= halfFrame(rate) && math.Abs(out-p.Duration) <= halfFrame(rate)
}

// progressive reads ffprobe's field order. A file that is interlaced says so;
// one that says nothing is taken as progressive, which is what every studio
// here writes.
func progressive(order string) bool {
	switch order {
	case "", "unknown", "progressive":
		return true
	}
	return false
}

func colour(p Stream) string {
	return fmt.Sprintf("%s/%s/%s/%s", or(p.ColorRange), or(p.ColorSpace), or(p.ColorPrimaries), or(p.ColorTransfer))
}

func or(v string) string {
	if v == "" || v == "unknown" {
		return "unset"
	}
	return v
}

// CheckAgainstProbes holds a stored document to what its files really are, at
// the moment of export. The rules were checked against the durations the asset
// store recorded, which for video and sound are what a studio said when it
// adopted them; this is where the file itself gets a say, so an export is
// never something other than the sequence on screen (M8 Q6, Q7).
func CheckAgainstProbes(target helm.TimelineTarget, tracks []helm.TimelineTrack, probes Probes) error {
	rate, ok := RateOf(target.FPS)
	if !ok {
		return fmt.Errorf("%g is not a frame rate the daemon knows exactly", target.FPS)
	}
	var f faults
	for _, tr := range tracks {
		name := ""
		if tr.Name != nil {
			name = *tr.Name
		}
		for j, c := range tr.Clips {
			f.at(name, j)
			p, known := probes[c.AssetID]
			switch {
			case !known:
				f.add("not_probed", "this clip's file could not be read")
			case c.Hold != nil:
				// A still is held for as long as the document says.
			case p.Duration <= 0:
				f.add("not_probed", "this clip's file reports no duration")
			case outOf(c) > p.Duration+halfFrame(rate):
				f.add("trim", "this clip runs to %.3fs and its file is %.3fs long", outOf(c), p.Duration)
			}
		}
	}
	return f.err()
}
