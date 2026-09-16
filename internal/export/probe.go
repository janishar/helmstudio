package export

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/timeline"
)

// Probe reads what a file really is. Everything the fast path compares comes
// from here and nothing from what a studio said when it adopted the asset: a
// hint carries no level, no parameter sets and no colour tags, and it is the
// studio's word for the rest (M8 Q6).
func (t *Tool) Probe(ctx context.Context, path string) (timeline.Stream, error) {
	cmd := exec.CommandContext(ctx, t.FFprobe,
		"-v", "error",
		"-show_streams", "-show_format",
		"-show_data_hash", "sha256",
		"-of", "json",
		path,
	)
	var out strings.Builder
	cmd.Stdout = &out
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := platform.RunInGroup(cmd); err != nil {
		return timeline.Stream{}, fmt.Errorf("probing %s: %v: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return parseProbe(out.String())
}

type probeJSON struct {
	Streams []struct {
		CodecType     string `json:"codec_type"`
		CodecName     string `json:"codec_name"`
		Profile       string `json:"profile"`
		Level         int64  `json:"level"`
		Width         int64  `json:"width"`
		Height        int64  `json:"height"`
		PixFmt        string `json:"pix_fmt"`
		FieldOrder    string `json:"field_order"`
		SampleAspect  string `json:"sample_aspect_ratio"`
		TimeBase      string `json:"time_base"`
		RFrameRate    string `json:"r_frame_rate"`
		AvgFrameRate  string `json:"avg_frame_rate"`
		ColorRange    string `json:"color_range"`
		ColorSpace    string `json:"color_space"`
		ColorPrim     string `json:"color_primaries"`
		ColorTransfer string `json:"color_transfer"`
		ExtradataHash string `json:"extradata_hash"`
		Duration      string `json:"duration"`
		SampleRate    string `json:"sample_rate"`
		Channels      int64  `json:"channels"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

func parseProbe(raw string) (timeline.Stream, error) {
	var p probeJSON
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return timeline.Stream{}, fmt.Errorf("reading ffprobe's answer: %w", err)
	}
	s := timeline.Stream{Container: p.Format.FormatName}
	formatDuration, _ := strconv.ParseFloat(p.Format.Duration, 64)
	for _, st := range p.Streams {
		switch st.CodecType {
		case "video":
			if s.Codec != "" {
				continue // the first video stream is the one that plays
			}
			s.Codec, s.Profile, s.Level = st.CodecName, st.Profile, st.Level
			s.Width, s.Height = st.Width, st.Height
			s.PixelFormat, s.FieldOrder = st.PixFmt, st.FieldOrder
			s.SampleAspect = st.SampleAspect
			s.TimeBase = st.TimeBase
			s.FrameRate = parseRate(st.RFrameRate)
			s.ColorRange, s.ColorSpace = st.ColorRange, st.ColorSpace
			s.ColorPrimaries, s.ColorTransfer = st.ColorPrim, st.ColorTransfer
			s.ParameterSets = st.ExtradataHash
			if d, err := strconv.ParseFloat(st.Duration, 64); err == nil && d > 0 {
				s.Duration = d
			}
		case "audio":
			if s.HasAudio {
				continue
			}
			s.HasAudio, s.AudioCodec, s.Channels = true, st.CodecName, st.Channels
			s.SampleRate, _ = strconv.ParseInt(st.SampleRate, 10, 64)
			if s.Duration == 0 {
				if d, err := strconv.ParseFloat(st.Duration, 64); err == nil {
					s.Duration = d
				}
			}
		}
	}
	if s.Duration == 0 {
		s.Duration = formatDuration
	}
	if s.Codec == "" && !s.HasAudio {
		return s, fmt.Errorf("the file has neither a video nor an audio stream")
	}
	return s, nil
}

// parseRate reads ffprobe's "24/1" or "24000/1001".
func parseRate(v string) timeline.Rate {
	num, den, ok := strings.Cut(v, "/")
	if !ok {
		return timeline.Rate{}
	}
	n, err1 := strconv.ParseInt(num, 10, 64)
	d, err2 := strconv.ParseInt(den, 10, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return timeline.Rate{}
	}
	return timeline.Rate{Num: n, Den: d}
}

// PosterFrame writes one frame of a video as raw RGB, which the existing
// thumbnailer scales and encodes exactly as it does an image's (M8 Q6). The
// frame is taken a tenth of the way in, so a clip that fades up from black
// does not give a black poster.
func (t *Tool) PosterFrame(ctx context.Context, path string, duration float64) ([]byte, int, int, error) {
	at := duration / 10
	if at < 0 || at > duration {
		at = 0
	}
	probe, err := t.Probe(ctx, path)
	if err != nil {
		return nil, 0, 0, err
	}
	if probe.Width <= 0 || probe.Height <= 0 {
		return nil, 0, 0, fmt.Errorf("%s has no picture to take a poster from", path)
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-fflags", "+bitexact",
		"-ss", fmt.Sprintf("%.3f", at), "-i", path, "-frames:v", "1",
		"-c:v", "rawvideo", "-pix_fmt", "rgb24", "-f", "rawvideo", "-"}
	if err := CheckArgs(args); err != nil {
		return nil, 0, 0, err
	}
	cmd := exec.CommandContext(ctx, t.FFmpeg, args...)
	var out strings.Builder
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := platform.RunInGroup(cmd); err != nil {
		return nil, 0, 0, fmt.Errorf("reading a frame of %s: %v: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	want := int(probe.Width * probe.Height * 3)
	if len(out.String()) < want {
		return nil, 0, 0, fmt.Errorf("reading a frame of %s: got %d bytes, want %d", path, len(out.String()), want)
	}
	return []byte(out.String())[:want], int(probe.Width), int(probe.Height), nil
}
