package timeline

import (
	"testing"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// probe is what ffprobe reports for a clip that may be copied: h3's real
// takes, at the target's size (docs/agents/reports/08-timeline-and-export.md,
// "What I measured").
func probe() Stream {
	return Stream{
		Container:     "mov,mp4,m4a,3gp,3g2,mj2",
		Codec:         "h264",
		Profile:       "High",
		Level:         30,
		Width:         1920,
		Height:        1080,
		PixelFormat:   "yuv420p",
		FieldOrder:    "progressive",
		SampleAspect:  "",
		TimeBase:      "1/12288",
		FrameRate:     Rate{24, 1},
		ParameterSets: "sps:a1b2",
		Duration:      5,
		HasAudio:      true,
		AudioCodec:    "aac",
		SampleRate:    32000,
		Channels:      2,
	}
}

// twoTakes is the common case the fast path exists for: a run of takes from
// one studio, used whole.
func twoTakes() ([]helm.TimelineTrack, Probes) {
	tracks := []helm.TimelineTrack{video(clip("take1", out(5)), clip("take2", out(5), at(5)))}
	tracks, err := Normalize(target(24), tracks, map[string]Source{
		"take1": {Kind: KindVideo, Duration: 5},
		"take2": {Kind: KindVideo, Duration: 5},
	})
	if err != nil {
		panic(err)
	}
	return tracks, Probes{"take1": probe(), "take2": probe()}
}

func hasReason(reasons []helm.ExportReason, code string) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestARunOfTakesIsCopied(t *testing.T) {
	tracks, probes := twoTakes()
	mode, reasons := Plan(target(24), tracks, probes)
	if mode != ModeCopy || len(reasons) != 0 {
		t.Fatalf("mode %q with %v, want copy with no reasons", mode, reasons)
	}
}

// Every fact the copy rests on, changed one at a time. A fast path taken
// illegally produces a file that plays on the developer's machine and nowhere
// else, so each of these has to stop it by itself.
func TestOneFactApartStopsTheCopy(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		bend func(second *Stream)
	}{
		{"another container", "container", func(s *Stream) { s.Container = "matroska,webm" }},
		{"another codec", "codec", func(s *Stream) { s.Codec = "hevc" }},
		{"another profile", "profile", func(s *Stream) { s.Profile = "Main" }},
		{"another level", "level", func(s *Stream) { s.Level = 31 }},
		{"another size", "size", func(s *Stream) { s.Width, s.Height = 1280, 720 }},
		{"non-square pixels", "aspect", func(s *Stream) { s.SampleAspect = "4:3" }},
		{"interlaced", "field_order", func(s *Stream) { s.FieldOrder = "tt" }},
		{"another pixel format", "pixel_format", func(s *Stream) { s.PixelFormat = "yuv422p" }},
		{"another time base", "time_base", func(s *Stream) { s.TimeBase = "1/90000" }},
		{"another frame rate", "frame_rate", func(s *Stream) { s.FrameRate = Rate{25, 1} }},
		{"a tagged colour range", "colour", func(s *Stream) { s.ColorRange = "pc" }},
		{"a tagged matrix", "colour", func(s *Stream) { s.ColorSpace = "bt709" }},
		{"tagged primaries", "colour", func(s *Stream) { s.ColorPrimaries = "bt709" }},
		{"a tagged transfer", "colour", func(s *Stream) { s.ColorTransfer = "bt709" }},
		{"other parameter sets", "parameter_sets", func(s *Stream) { s.ParameterSets = "sps:c3d4" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks, probes := twoTakes()
			second := probes["take2"]
			tc.bend(&second)
			probes["take2"] = second
			mode, reasons := Plan(target(24), tracks, probes)
			if mode != ModeConform {
				t.Fatalf("mode %q, want conform", mode)
			}
			if !hasReason(reasons, tc.code) {
				t.Fatalf("reasons %v, want one coded %s", reasons, tc.code)
			}
			for _, r := range reasons {
				if r.Track == nil || *r.Track != "V1" || r.Clip == nil || *r.Clip != 2 {
					t.Errorf("reason %q names %v clip %v, want V1 clip 2", r.Code, r.Track, r.Clip)
				}
			}
		})
	}
}

// The document's own shape stops a copy too: a trim cuts at packets, a still
// is drawn, and a dissolve is rendered.
func TestTheDocumentCanStopTheCopy(t *testing.T) {
	src := map[string]Source{
		"take1": {Kind: KindVideo, Duration: 5},
		"take2": {Kind: KindVideo, Duration: 5},
		"still": {Kind: KindImage},
	}
	for _, tc := range []struct {
		name   string
		code   string
		tracks []helm.TimelineTrack
	}{
		{"a trimmed clip", "trimmed", []helm.TimelineTrack{video(clip("take1", out(5)), clip("take2", in(1), out(4)))}},
		{"a still", "image", []helm.TimelineTrack{video(clip("take1", out(5)), clip("still", hold(2)))}},
		{"a dissolve", "transition", []helm.TimelineTrack{video(clip("take1", out(5)), clip("take2", in(1), out(5), dissolve(0.5)))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks, err := Normalize(target(24), tc.tracks, src)
			if err != nil {
				t.Fatal(err)
			}
			probes := Probes{"take1": probe(), "take2": probe(), "still": {}}
			mode, reasons := Plan(target(24), tracks, probes)
			if mode != ModeConform || !hasReason(reasons, tc.code) {
				t.Fatalf("mode %q reasons %v, want conform coded %s", mode, reasons, tc.code)
			}
		})
	}
}

// Sound never decides the picture's path: it is re-encoded either way, so a
// voice line and a gain leave the copy alone (M8 Q13).
func TestSoundAndGainDoNotStopTheCopy(t *testing.T) {
	src := map[string]Source{
		"take1": {Kind: KindVideo, Duration: 5},
		"take2": {Kind: KindVideo, Duration: 5},
		"line":  {Kind: KindAudio, Duration: 3},
	}
	tracks, err := Normalize(target(24), []helm.TimelineTrack{
		video(clip("take1", out(5), gain(-3)), clip("take2", out(5))),
		audio(clip("line", out(3), gain(2))),
	}, src)
	if err != nil {
		t.Fatal(err)
	}
	probes := Probes{"take1": probe(), "take2": probe(), "line": {}}
	if mode, reasons := Plan(target(24), tracks, probes); mode != ModeCopy {
		t.Fatalf("mode %q with %v, want copy", mode, reasons)
	}
}

func TestAnUnprobedClipOrNoPictureConforms(t *testing.T) {
	tracks, probes := twoTakes()
	delete(probes, "take2")
	if mode, reasons := Plan(target(24), tracks, probes); mode != ModeConform || !hasReason(reasons, "not_probed") {
		t.Errorf("mode %q reasons %v, want conform coded not_probed", mode, reasons)
	}
	src := map[string]Source{"line": {Kind: KindAudio, Duration: 3}}
	soundOnly, err := Normalize(target(24), []helm.TimelineTrack{audio(clip("line", out(3)))}, src)
	if err != nil {
		t.Fatal(err)
	}
	if mode, reasons := Plan(target(24), soundOnly, Probes{}); mode != ModeConform || !hasReason(reasons, "no_video") {
		t.Errorf("mode %q reasons %v, want conform coded no_video", mode, reasons)
	}
}

// The facts come from the probe, never from what a studio said when it adopted
// the asset (M8 Q6): a lying hint cannot buy a copy.
func TestAdoptHintsCannotBuyACopy(t *testing.T) {
	// The recorded duration says 5s and the file says 4s, so the clip is not
	// whole and a copy would cut at a packet.
	tracks, err := Normalize(target(24), []helm.TimelineTrack{video(clip("take1", out(5)))}, map[string]Source{
		"take1": {Kind: KindVideo, Duration: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := probe()
	p.Duration = 4
	if mode, reasons := Plan(target(24), tracks, Probes{"take1": p}); mode != ModeConform || !hasReason(reasons, "trimmed") {
		t.Fatalf("mode %q reasons %v, want conform coded trimmed", mode, reasons)
	}
}
