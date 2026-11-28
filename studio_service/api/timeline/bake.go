package timeline

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A bake (API 1.82, contracts/README.md "Magic: Send to Timeline"): a short clip rendered from one or two takes so
// the Timeline never needs a transition or an effect it does not have: a fade (through black), a wipe or a dissolve
// between two shots, a lane's fade in or out, a shot's effects. The same placeholders and picture steps as an
// export's conform; the runner runs it as one.

// BakeSide is what one input gives a bake: Start to End seconds of its take (End past the take's end is not read),
// slowed by Rate (1: as it is), then its first frame held Pre frames before and its last held Post frames after.
type BakeSide struct {
	Input    int     // its index in the job's inputs
	Kind     string  // video | image
	HasAudio bool    // a video with sound
	Stream   *Stream // a video's facts (its colour)
	Start    float64 // seconds in the take
	End      float64
	Rate     float64 // the Slow effect's rate (0.5 = half speed); 0 or 1: as it is
	Pre      int     // frames of its first frame held before
	Post     int     // frames of its last frame held after
	Filters  []string
}

// BakeKinds are the bakes: two sides (fade, wipe, dissolve) or one (fade_in, fade_out, effects).
var BakeKinds = map[string]string{"fade": "fadeblack", "wipe": "wipeleft", "dissolve": "fade"}

// BakeJob is a bake command's body (cmd.media.job bake).
type BakeJob struct {
	Target       ExportTarget `json:"target"`
	Frames       int          `json:"frames"`
	Samples      int          `json:"samples"`
	VideoBitrate int          `json:"video_bitrate"`
	Args         []string     `json:"args"`
}

// BuildBake is the bake of kind over sides into a clip of frames on target's grid. A two-side bake crosses them
// over the whole clip (each side frames long); a one-side one fades it in from black, out to black, or (effects)
// is the side as it is. Each side's sound is laid with it (faded across with the picture), silence where it has none.
func BuildBake(target Target, kind string, sides []BakeSide, frames int) (BakeJob, error) {
	g := NewGrid(target)
	rate, dur := g.RateString(), secs(g.S(frames))
	job := BakeJob{Target: ExportTarget{target.Width, target.Height, target.FPS, rate, target.SampleRate}, Frames: frames,
		Samples:      Rnd(float64(frames) * float64(target.SampleRate) * float64(g.den) / float64(g.num)),
		VideoBitrate: min(max(Rnd(float64(target.Width*target.Height)*float64(g.num)/float64(g.den)*0.15), 1_000_000), 40_000_000)}
	_, two := BakeKinds[kind]
	switch {
	case frames < 1:
		return job, errors.New("a bake is at least a frame long")
	case two && len(sides) != 2, !two && len(sides) != 1:
		return job, fmt.Errorf("a %s bake takes %d sides", kind, map[bool]int{true: 2, false: 1}[two])
	case !two && kind != "fade_in" && kind != "fade_out" && kind != "effects":
		return job, fmt.Errorf("unknown bake %s", kind)
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-fflags", "+bitexact", "-progress", "pipe:1", "-nostats"}
	silence := fmt.Sprintf("anullsrc=r=%d:cl=stereo", target.SampleRate)
	var chains, vlabels, alabels []string
	for i, sd := range sides {
		r := sd.Rate
		if r <= 0 {
			r = 1
		}
		v, a := "v"+strconv.Itoa(i), "a"+strconv.Itoa(i)
		var steps []string
		if sd.Kind == "image" {
			args = append(args, "-loop", "1", "-framerate", rate, "-t", dur, "-i", in(sd.Input))
			steps = []string{fmt.Sprintf("[%d:v:0]trim=duration=%s", i, dur), "setpts=PTS-STARTPTS"}
		} else {
			args = append(args, "-i", in(sd.Input))
			steps = []string{fmt.Sprintf("[%d:v:0]trim=start=%s:end=%s", i, secs(sd.Start), secs(sd.End)), "setpts=PTS-STARTPTS"}
			if r != 1 {
				steps = append(steps, "setpts=PTS/"+secs(r))
			}
		}
		steps = append(steps, sd.Filters...)
		steps = append(steps, pictureSteps(target, rate, sd.Stream)...)
		// its held frames, and one more at the end so a source a frame short still fills it; then exactly frames of it,
		// fps again (tpad leaves the frame rate unset, and xfade in ffmpeg 7 needs a constant one)
		steps = append(steps, fmt.Sprintf("tpad=start_mode=clone:start_duration=%s:stop_mode=clone:stop_duration=%s",
			secs(g.S(sd.Pre)), secs(g.S(sd.Post+1))), fmt.Sprintf("trim=end_frame=%d", frames), "setpts=PTS-STARTPTS", "fps="+rate)
		chains = append(chains, strings.Join(steps, ",")+"["+v+"]")
		vlabels = append(vlabels, v)
		// its sound: the same span, held silent where the picture is held, faded with the picture
		fade := ""
		switch {
		case two && i == 0, kind == "fade_out":
			fade = fmt.Sprintf(",afade=t=out:st=0:d=%s:curve=tri", dur)
		case two, kind == "fade_in":
			fade = fmt.Sprintf(",afade=t=in:st=0:d=%s:curve=tri", dur)
		}
		if sd.Kind == "video" && sd.HasAudio {
			tempo := ""
			if r != 1 {
				tempo = ",atempo=" + secs(r)
			}
			chains = append(chains, fmt.Sprintf("[%d:a:0]atrim=start=%s:end=%s,asetpts=N/SR/TB%s,aresample=%d:async=1:first_pts=0,aformat=sample_fmts=fltp:channel_layouts=stereo[%sb]",
				i, secs(sd.Start), secs(sd.End), tempo, target.SampleRate, a))
			if sd.Pre > 0 {
				chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[%sp]", silence, secs(g.S(sd.Pre)), a),
					fmt.Sprintf("[%sp][%sb]concat=n=2:v=0:a=1[%sc]", a, a, a))
			} else {
				chains = append(chains, fmt.Sprintf("[%sb]anull[%sc]", a, a))
			}
			chains = append(chains, fmt.Sprintf("[%sc]apad,atrim=duration=%s,asetpts=N/SR/TB%s[%s]", a, dur, fade, a))
		} else {
			chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[%s]", silence, dur, a))
		}
		alabels = append(alabels, a)
	}
	switch {
	case two:
		chains = append(chains, fmt.Sprintf("[v0][v1]xfade=transition=%s:duration=%s:offset=0,%s[vout]", BakeKinds[kind], dur,
			exact(g, frames, rate)))
		chains = append(chains, fmt.Sprintf("[a0][a1]amix=inputs=2:normalize=0:duration=longest,atrim=duration=%s,asetpts=N/SR/TB[aout]", dur))
	default:
		fade := "null"
		switch kind {
		case "fade_in":
			fade = "fade=t=in:st=0:d=" + dur
		case "fade_out":
			fade = "fade=t=out:st=0:d=" + dur
		}
		chains = append(chains, "[v0]"+fade+","+exact(g, frames, rate)+"[vout]", "[a0]anull[aout]")
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"),
		"-map", "[vout]", "{venc}", "-r", rate, "-pix_fmt", "yuv420p", "-fps_mode", "cfr",
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
		"-map", "[aout]", "-map_metadata", "-1", "-c:a", "aac", "-b:a", "192k",
		"-ar", strconv.Itoa(target.SampleRate), "-ac", "2", "-movflags", "+faststart", "-f", "mp4", "{out}")
	job.Args = args
	return job, nil
}

// exact is the steps that make a picture exactly frames long: its last frame held once more, then cut there.
func exact(g Grid, frames int, rate string) string {
	return fmt.Sprintf("tpad=stop_mode=clone:stop_duration=%s,trim=end_frame=%d,setpts=PTS-STARTPTS,fps=%s", secs(g.S(1)), frames, rate)
}

// EffectFilters is an effect badge's picture filters (its name before " · ": Grade, Slow, Stabilize) and its rate
// (Slow); ok false for one that does not bake. Grade: temperature (red up and blue down, ±100) and contrast (±100);
// Slow: rate 0.5-2; Stabilize: deshake.
func EffectFilters(name string, params map[string]any) (filters []string, rate float64, ok bool) {
	num := func(k string) float64 {
		switch v := params[k].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			f, _ := strconv.ParseFloat(v, 64)
			return f
		}
		return 0
	}
	head, _, _ := strings.Cut(name, " · ")
	switch head {
	case "Grade":
		t, c := min(max(num("temperature"), -100), 100), min(max(num("contrast"), -100), 100)
		return []string{fmt.Sprintf("colorbalance=rm=%s:gm=0:bm=%s", secs(t/200), secs(-t/200)),
			fmt.Sprintf("eq=contrast=%s", secs(1+c/100))}, 1, true
	case "Slow":
		r := num("rate")
		if r < 0.5 || r > 2 {
			return nil, 1, false
		}
		return nil, r, true
	case "Stabilize":
		return []string{"deshake"}, 1, true
	}
	return nil, 1, false
}
