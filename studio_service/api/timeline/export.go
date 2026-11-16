package timeline

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The export's ffmpeg arguments: one filter graph, ported from the old studio's internal/timeline Build and
// EncodeArgs (contracts/README.md, Timeline: export). Placeholders the runner fills: {in:<n>} = inputs[n]'s local
// path, {list} = the concat list it writes from Concat, {out} = the output file, {venc} = its video encoder's
// arguments.

// MediaInput is one object a media job reads (cmd.media.job inputs).
type MediaInput struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // video | image
}

// ExportTarget is cmd.media.job's export.target.
type ExportTarget struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	Rate       string  `json:"rate"`
	SampleRate int     `json:"sample_rate"`
}

// Expect is a copy plan's stream facts of one video input.
type Expect struct {
	Input  int    `json:"input"`
	Stream Stream `json:"stream"`
}

// ExportJob is what an export command carries besides its kind and output prefix.
type ExportJob struct {
	Inputs       []MediaInput
	Mode         string // copy | conform
	Target       ExportTarget
	Frames       int
	Samples      int
	VideoBitrate int
	Args         []string
	Concat       []int    // copy only
	Expect       []Expect // copy only
	ConformArgs  []string // copy only
}

// BuildExport builds the export of a normalized document. keys: take id → its object key (every take the document
// names). The plan decides the mode; a copy plan also carries the conform arguments to fall back to.
func BuildExport(doc Doc, takes map[string]Take, keys map[string]string, plan Plan) (ExportJob, error) {
	g := NewGrid(doc.Target)
	job := ExportJob{Mode: "conform", Frames: doc.Frames, VideoBitrate: plan.Bitrate, Inputs: []MediaInput{}, // [] not null: a sequence of empty slots reads nothing
		Target: ExportTarget{doc.Target.Width, doc.Target.Height, doc.Target.FPS, g.RateString(), doc.Target.SampleRate}}
	if doc.Frames < 1 || len(doc.Tracks[0].Clips) == 0 {
		return job, errors.New("the sequence is empty")
	}
	job.Samples = Rnd(float64(doc.Frames) * float64(doc.Target.SampleRate) * float64(g.den) / float64(g.num))
	index := map[string]int{}
	for _, t := range doc.Tracks {
		for _, c := range t.Clips {
			tid := c.Take()
			if tid == "" {
				continue
			}
			if _, done := index[tid]; done {
				continue
			}
			key, ok := keys[tid]
			tk, known := takes[tid]
			if !ok || !known {
				return job, fmt.Errorf("clip %s is offline", c.ID)
			}
			index[tid] = len(job.Inputs)
			job.Inputs = append(job.Inputs, MediaInput{Key: key, Kind: tk.Kind})
		}
	}
	conform := buildArgs(doc, takes, index, g, false)
	if !plan.StreamCopy {
		job.Args = conform
		return job, nil
	}
	job.Mode, job.ConformArgs, job.Args = "copy", conform, buildArgs(doc, takes, index, g, true)
	for _, c := range doc.Tracks[0].Clips {
		n := index[c.Take()]
		job.Concat = append(job.Concat, n)
		if s := takes[c.Take()].Stream; s != nil && !hasExpect(job.Expect, n) {
			job.Expect = append(job.Expect, Expect{Input: n, Stream: *s})
		}
	}
	return job, nil
}

func hasExpect(list []Expect, n int) bool {
	for _, e := range list {
		if e.Input == n {
			return true
		}
	}
	return false
}

// span is where the export reads a clip from (a dissolve widens it by half on each side) and the dissolve it ends.
type span struct{ start, end, fadeOut float64 }

// buildArgs is the whole argument list: copy = the picture through the concat demuxer, conform = re-encoded.
func buildArgs(doc Doc, takes map[string]Take, index map[string]int, g Grid, copyMode bool) []string {
	tg, v1 := doc.Target, doc.Tracks[0]
	rate, dur := g.RateString(), secs(g.S(doc.Frames))
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-fflags", "+bitexact", "-progress", "pipe:1", "-nostats"}
	next := 0
	if copyMode {
		args = append(args, "-f", "concat", "-safe", "0", "-i", "{list}")
		next = 1
	}
	spans := make([]span, len(v1.Clips))
	for i, c := range v1.Clips {
		if c.In != nil {
			spans[i] = span{start: *c.In, end: *c.Out}
		} else {
			spans[i] = span{end: val(c.Hold)}
		}
	}
	for i, c := range v1.Clips {
		if i == 0 || c.TransitionIn == nil {
			continue
		}
		half := g.S(c.TransitionIn.Frames / 2)
		if c.In != nil {
			spans[i].start = max(0, spans[i].start-half) // a video's incoming handle (Normalize checked it)
		} else {
			spans[i].end += half // an image or a slot has no source time: it holds half a dissolve longer
		}
		spans[i-1].end += half
		spans[i-1].fadeOut = g.S(c.TransitionIn.Frames)
	}
	var chains, vlabels []string
	type sound struct {
		input             int
		start, end, at    float64
		gain, fadeIn, out float64
	}
	var sounds []sound
	for i, c := range v1.Clips {
		kind, label := Kind(c, takes), "v"+strconv.Itoa(i)
		length := spans[i].end - spans[i].start
		var steps []string
		switch kind {
		case "slot": // black, no sound
			steps = []string{fmt.Sprintf("color=c=black:s=%dx%d:r=%s:d=%s", tg.Width, tg.Height, rate, secs(length))}
		case "image":
			args = append(args, "-loop", "1", "-framerate", rate, "-t", secs(length), "-i", in(index[c.Take()]))
			steps = []string{fmt.Sprintf("[%d:v:0]trim=duration=%s", next, secs(length))}
			next++
		default:
			args = append(args, "-i", in(index[c.Take()]))
			steps = []string{fmt.Sprintf("[%d:v:0]trim=start=%s:end=%s", next, secs(spans[i].start), secs(spans[i].end))}
			tk := takes[c.Take()]
			if tk.HasAudio && !*c.Mute && !*v1.Mute {
				s := sound{input: next, start: spans[i].start, end: spans[i].end, at: *c.At, gain: *v1.GainDB + *c.GainDB, out: spans[i].fadeOut}
				if c.TransitionIn != nil && i > 0 {
					s.fadeIn = g.S(c.TransitionIn.Frames)
					s.at -= g.S(c.TransitionIn.Frames / 2)
				}
				sounds = append(sounds, s)
			}
			next++
		}
		matrix, rng := "bt601", "tv"
		if s := takes[c.Take()].Stream; s != nil {
			switch s.Color["space"] {
			case "bt709":
				matrix = "bt709"
			case "bt2020nc", "bt2020_ncl":
				matrix = "bt2020nc"
			}
			if r := s.Color["range"]; r == "pc" || r == "full" {
				rng = "pc"
			}
		}
		steps = append(steps, "setpts=PTS-STARTPTS",
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=bicubic+accurate_rnd+bitexact:in_color_matrix=%s:in_range=%s:out_color_matrix=bt709:out_range=tv",
				tg.Width, tg.Height, matrix, rng),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", tg.Width, tg.Height),
			"setsar=1", "fps="+rate, "format=yuv420p",
			// every chain on one timebase: xfade refuses two sides counting in different units after a concat
			"settb=AVTB",
			// the frames carry their colour: videotoolbox writes its own VUI and drops primaries and transfer
			"setparams=range=tv:color_primaries=bt709:color_trc=bt709:colorspace=bt709")
		chains = append(chains, strings.Join(steps, ",")+"["+label+"]")
		vlabels = append(vlabels, label)
	}
	for _, t := range doc.Tracks[1:] {
		for _, c := range t.Clips {
			if c.Offline || *c.Mute || *t.Mute {
				continue
			}
			args = append(args, "-i", in(index[c.Take()]))
			sounds = append(sounds, sound{input: next, start: *c.In, end: *c.Out, at: *c.At, gain: *t.GainDB + *c.GainDB})
			next++
		}
	}
	if copyMode {
		chains = nil // the picture is the concat demuxer's
	} else {
		acc, accLen := vlabels[0], spans[0].end-spans[0].start
		for n := 1; n < len(vlabels); n++ {
			out, length := "vj"+strconv.Itoa(n), spans[n].end-spans[n].start
			if d := v1.Clips[n].TransitionIn; d != nil {
				ds := g.S(d.Frames)
				chains = append(chains, fmt.Sprintf("[%s][%s]xfade=transition=fade:duration=%s:offset=%s[%s]", acc, vlabels[n], secs(ds), secs(accLen-ds), out))
				accLen += length - ds
			} else {
				chains = append(chains, fmt.Sprintf("[%s][%s]concat=n=2:v=1:a=0[%s]", acc, vlabels[n], out))
				accLen += length
			}
			acc = out
		}
		chains = append(chains, fmt.Sprintf("[%s]trim=duration=%s,setpts=PTS-STARTPTS[vout]", acc, dur))
	}
	// sound: each clip's trimmed and padded to its picture, laid at its exact sample with a silence prefix (never
	// adelay), mixed without amix dividing by its input count (normalize=0), encoded once (the 81 ms AAC lesson)
	silence := fmt.Sprintf("anullsrc=r=%d:cl=stereo", tg.SampleRate)
	chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[abase]", silence, dur))
	alabels := []string{"abase"}
	for i, s := range sounds {
		label := "a" + strconv.Itoa(i)
		length := s.end - s.start
		steps := []string{fmt.Sprintf("atrim=start=%s:end=%s", secs(s.start), secs(s.end)), "asetpts=N/SR/TB",
			fmt.Sprintf("aresample=%d:async=1:first_pts=0", tg.SampleRate), "aformat=sample_fmts=fltp:channel_layouts=stereo",
			"apad", "atrim=duration=" + secs(length)}
		if s.gain != 0 {
			steps = append(steps, "volume="+secs(s.gain)+"dB")
		}
		if s.out > 0 {
			steps = append(steps, fmt.Sprintf("afade=t=out:st=%s:d=%s:curve=tri", secs(length-s.out), secs(s.out)))
		}
		if s.fadeIn > 0 {
			steps = append(steps, fmt.Sprintf("afade=t=in:st=0:d=%s:curve=tri", secs(s.fadeIn)))
		}
		steps = append(steps, "asetpts=N/SR/TB")
		chains = append(chains, fmt.Sprintf("[%d:a:0]%s[%sb]", s.input, strings.Join(steps, ","), label))
		if s.at > 0 {
			chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[%sp]", silence, secs(s.at), label),
				fmt.Sprintf("[%sp][%sb]concat=n=2:v=0:a=1[%s]", label, label, label))
		} else {
			chains = append(chains, fmt.Sprintf("[%sb]anull[%s]", label, label))
		}
		alabels = append(alabels, label)
	}
	if len(alabels) == 1 {
		chains = append(chains, fmt.Sprintf("[abase]atrim=duration=%s,asetpts=N/SR/TB[aout]", dur))
	} else {
		chains = append(chains, fmt.Sprintf("[%s]amix=inputs=%d:normalize=0:duration=longest,atrim=duration=%s,asetpts=N/SR/TB[aout]",
			strings.Join(alabels, "]["), len(alabels), dur))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"))
	if copyMode {
		// the picture is the sources' own, colour tags included: nothing converted it, so nothing relabels it
		args = append(args, "-map", "0:v:0", "-c:v", "copy")
	} else {
		// -r: the graph's rate does not always reach the muxer (ffmpeg 7 writes 25 fps after concat/xfade without it)
		args = append(args, "-map", "[vout]", "{venc}", "-r", rate, "-pix_fmt", "yuv420p", "-fps_mode", "cfr",
			"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv")
	}
	return append(args, "-map", "[aout]", "-map_metadata", "-1", "-c:a", "aac", "-b:a", "192k",
		"-ar", strconv.Itoa(tg.SampleRate), "-ac", "2", "-movflags", "+faststart", "-f", "mp4", "{out}")
}

func in(n int) string { return "{in:" + strconv.Itoa(n) + "}" }

// secs writes a time the way ffmpeg reads it: 6 places (enough to name a 48 kHz sample), no exponent.
func secs(v float64) string {
	if math.Abs(v) < 1e-9 {
		return "0"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
}
