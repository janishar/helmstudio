package export

import (
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

func f64(v float64) *float64 { return &v }

func scene() (helm.TimelineTarget, []helm.TimelineTrack, timeline.Probes, timeline.Sources) {
	target := helm.TimelineTarget{Width: 320, Height: 180, FPS: 24, SampleRate: 48000}
	tracks := []helm.TimelineTrack{{
		Kind: timeline.KindVideo,
		Name: strptr("V1"),
		Clips: []helm.TimelineClip{
			{AssetID: "a", In: f64(0), Out: f64(2), At: f64(0)},
			{AssetID: "b", In: f64(0), Out: f64(1), At: f64(2)},
		},
	}}
	probes := timeline.Probes{"a": {HasAudio: true, Duration: 2}, "b": {HasAudio: true, Duration: 1}}
	files := timeline.Sources{"a": "/tmp/a.mp4", "b": "/tmp/b.mp4"}
	return target, tracks, probes, files
}

func strptr(s string) *string { return &s }

// The licensing decision is an LGPL build with videotoolbox, so nothing the
// daemon runs may name another encoder — whichever path it takes
// (docs/decisions.md M8 Q4).
func TestEveryArgumentListNamesOnlyAllowedEncoders(t *testing.T) {
	target, tracks, probes, files := scene()
	for _, mode := range []string{timeline.ModeCopy, timeline.ModeConform} {
		r, err := timeline.Build(target, tracks, probes, files, mode, "/tmp/list.txt")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		args, err := r.EncodeArgs(target, helm.ExportPresetH264, "/tmp/out.mp4")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if err := CheckArgs(args); err != nil {
			t.Errorf("%s export: %v", mode, err)
		}
		if err := CheckArgs(r.HashArgs(target)); err != nil {
			t.Errorf("%s golden: %v", mode, err)
		}
		if strings.Contains(strings.Join(args, " "), "264") && !strings.Contains(strings.Join(args, " "), timeline.VideoEncoder) {
			t.Errorf("%s: an h264 encoder that is not %s", mode, timeline.VideoEncoder)
		}
	}
}

// The check has to be able to fail, or it is a comment.
func TestTheEncoderCheckCatchesAGPLEncoder(t *testing.T) {
	for _, args := range [][]string{
		{"-i", "in.mp4", "-c:v", "libx264", "out.mp4"},
		{"-i", "in.mp4", "-vcodec", "libx265", "out.mp4"},
		{"-i", "in.mp4", "-c:a", "libfdk_aac", "out.m4a"},
	} {
		if err := CheckArgs(args); err == nil {
			t.Errorf("%v passed", args)
		}
	}
}
