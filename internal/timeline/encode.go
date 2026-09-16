package timeline

import (
	"fmt"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Encoders helmstudio may name. The licensing decision is an LGPL build with
// videotoolbox, so no argument list here mentions x264 or x265, and a test
// holds that line (docs/decisions.md M8 Q4).
const (
	VideoEncoder = "h264_videotoolbox"
	AudioEncoder = "aac"
	AudioBitrate = "192k"
)

// AllowedEncoders is every encoder an argument list may name: the two the
// preset uses, the copy that is not an encoder at all, and the uncompressed
// pair the goldens and a poster frame read through.
var AllowedEncoders = map[string]bool{
	VideoEncoder: true,
	AudioEncoder: true,
	"copy":       true,
	"rawvideo":   true,
	"pcm_s16le":  true,
	"mjpeg":      true,
}

// commonArgs are the arguments every invocation starts with: no stdin, errors
// only, and a container that carries nothing of the sources' metadata.
func commonArgs() []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-fflags", "+bitexact"}
}

// EncodeArgs is the whole argument list for an export. Progress is asked for
// on stdout, so the runner can count frames without parsing ffmpeg's log.
func (r *Render) EncodeArgs(target helm.TimelineTarget, preset helm.ExportPreset, outPath string) ([]string, error) {
	if preset != helm.ExportPresetH264 {
		return nil, fmt.Errorf("%q is not a preset this daemon has", preset)
	}
	args := append(commonArgs(), "-progress", "pipe:1", "-nostats")
	args = append(args, r.InputArgs...)
	if r.Filter != "" {
		args = append(args, "-filter_complex", r.Filter)
	}
	args = append(args, "-map", r.VideoMap, "-map", r.AudioMap, "-map_metadata", "-1")
	if r.Mode == ModeCopy {
		// The picture is the sources' own, colour tags included: nothing here
		// converted it, so nothing here may relabel it (M8 Q9).
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", VideoEncoder,
			"-profile:v", "high",
			"-allow_sw", "1",
			"-pix_fmt", "yuv420p",
			"-b:v", fmt.Sprintf("%d", bitrate(target)),
			"-fps_mode", "cfr",
			"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
		)
	}
	args = append(args,
		"-c:a", AudioEncoder, "-b:a", AudioBitrate,
		"-ar", fmt.Sprintf("%d", target.SampleRate), "-ac", "2",
		"-movflags", "+faststart", "-f", "mp4", outPath,
	)
	return args, nil
}

// HashArgs maps the same inputs and the same graph to frame hashes instead of
// an encoder, which is how a golden checks what the graph produced rather than
// what an encoder made of it. videotoolbox's bytes move with the OS; decoded
// frames do not (M8 Q15).
func (r *Render) HashArgs(target helm.TimelineTarget) []string {
	args := append(commonArgs(), "-nostats")
	args = append(args, r.InputArgs...)
	if r.Filter != "" {
		args = append(args, "-filter_complex", r.Filter)
	}
	args = append(args, "-map", r.VideoMap, "-map", r.AudioMap, "-map_metadata", "-1",
		"-c:v", "rawvideo", "-pix_fmt", "yuv420p",
		"-c:a", "pcm_s16le", "-ar", fmt.Sprintf("%d", target.SampleRate), "-ac", "2",
		"-f", "framemd5", "-")
	return args
}

// bitrate is what the preset asks of the encoder for a target this size. The
// figure is a judgement call: enough that a generated clip is not visibly
// softened, and bounded so a large target cannot ask for an absurd file.
func bitrate(target helm.TimelineTarget) int64 {
	fps := target.FPS
	if fps <= 0 {
		fps = 24
	}
	b := int64(float64(target.Width*target.Height) * fps * 0.15)
	switch {
	case b < 1_000_000:
		return 1_000_000
	case b > 40_000_000:
		return 40_000_000
	}
	return b
}
