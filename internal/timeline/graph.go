package timeline

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Render is one export, as arguments rather than as a command: the inputs, the
// filter graph, and what to map. Keeping it in parts is what lets the golden
// tests take the same graph and hash what comes out of it, frame by frame,
// before any encoder touches it (M8 Q15).
type Render struct {
	Mode string
	// ConcatList is the concat demuxer's list file, empty unless the picture is
	// copied. The caller writes it beside the output and passes its path in.
	ConcatList string
	InputArgs  []string
	Filter     string
	VideoMap   string
	AudioMap   string
	Duration   float64
	Frames     int64
	Samples    int64
}

// Sources are the files behind the assets a document names. Each path is
// absolute: the concat demuxer reads a relative one against the list file's
// own directory, not the working directory, which would send it looking in the
// wrong place.
type Sources map[string]string

// Build turns a document into the render that produces it. The picture is
// copied only when Plan said so; sound always goes through the graph, each
// clip's trimmed or padded to its picture and laid at its exact sample, so
// nothing drifts at a cut (M8 Q13, Q14).
func Build(target helm.TimelineTarget, tracks []helm.TimelineTrack, probes Probes, files Sources, mode, listPath string) (*Render, error) {
	rate, ok := RateOf(target.FPS)
	if !ok {
		return nil, fmt.Errorf("%g is not a frame rate the daemon knows exactly", target.FPS)
	}
	vi := TrackNamed(tracks, "V1")
	if vi < 0 || len(tracks[vi].Clips) == 0 {
		return nil, fmt.Errorf("the sequence has no video clip to export")
	}
	for _, id := range Assets(tracks) {
		if path, ok := files[id]; !ok {
			return nil, fmt.Errorf("no file for asset %s", id)
		} else if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("the file for asset %s is %q, which is not absolute", id, path)
		}
	}
	duration := Duration(target, tracks)
	r := &Render{
		Mode:     mode,
		Duration: duration,
		Frames:   Frames(target, tracks),
		Samples:  Samples(target, tracks),
	}

	// One input per clip, in the order the graph names them. The same file
	// appears as often as it is used, which keeps clip and input in step.
	type inputRef struct {
		index int
		clip  helm.TimelineClip
		track string
		gain  float64
		start float64 // where the render reads from in the source
		end   float64
		at    float64 // where it sits in the sequence
		fade  float64 // the dissolve this clip starts, 0 when there is none
		image bool
		sound bool
	}
	var refs []inputRef
	var chains []string

	video := tracks[vi]
	handles := handlesOf(video.Clips, rate)
	// What each video clip is worth on the timeline once its dissolves have
	// widened it; a dissolve then takes its duration back out of the join.
	vlen := make([]float64, len(handles))
	for i, h := range handles {
		vlen[i] = h.end - h.start
	}
	next := 0
	if mode == ModeCopy {
		// Input 0 is the concat demuxer over the clips' own files; the clips are
		// still opened again, for their sound.
		var list strings.Builder
		for _, c := range video.Clips {
			path, ok := files[c.AssetID]
			if !ok {
				return nil, fmt.Errorf("no file for asset %s", c.AssetID)
			}
			fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(path, "'", `'\''`))
		}
		r.ConcatList = list.String()
		r.InputArgs = append(r.InputArgs, "-f", "concat", "-safe", "0", "-i", listPath)
		r.VideoMap = "0:v:0"
		next = 1
	}

	for i, c := range video.Clips {
		path, ok := files[c.AssetID]
		if !ok {
			return nil, fmt.Errorf("no file for asset %s", c.AssetID)
		}
		p := probes[c.AssetID]
		ref := inputRef{index: next, clip: c, track: "V1", at: atOf(c), gain: gainOf(c, video.GainDb)}
		ref.image = c.Hold != nil
		ref.start, ref.end = handles[i].start, handles[i].end
		ref.fade = handles[i].fade
		ref.sound = !ref.image && p.HasAudio && (c.Audio == nil || *c.Audio)
		if ref.image {
			r.InputArgs = append(r.InputArgs, "-loop", "1", "-framerate", rate.String(), "-t", secs(handles[i].end-handles[i].start), "-i", path)
		} else {
			r.InputArgs = append(r.InputArgs, "-i", path)
		}
		refs = append(refs, ref)
		next++
	}
	for _, tr := range tracks {
		if tr.Kind != KindAudio {
			continue
		}
		for _, c := range tr.Clips {
			path, ok := files[c.AssetID]
			if !ok {
				return nil, fmt.Errorf("no file for asset %s", c.AssetID)
			}
			name := "A"
			if tr.Name != nil {
				name = *tr.Name
			}
			ref := inputRef{index: next, clip: c, track: name, at: atOf(c), gain: gainOf(c, tr.GainDb), sound: true}
			ref.start, ref.end = inOf(c), outOf(c)
			r.InputArgs = append(r.InputArgs, "-i", path)
			refs = append(refs, ref)
			next++
		}
	}

	// The picture: trim, fit inside the target, square the pixels, land on its
	// frames, and read colour the way the file was written (M8 Q9).
	if mode != ModeCopy {
		var vlabels []string
		for i, ref := range refs {
			if ref.track != "V1" {
				continue
			}
			p := probes[ref.clip.AssetID]
			label := fmt.Sprintf("v%d", i)
			var steps []string
			if ref.image {
				steps = append(steps, fmt.Sprintf("trim=duration=%s", secs(ref.end-ref.start)))
			} else {
				steps = append(steps, fmt.Sprintf("trim=start=%s:end=%s", secs(ref.start), secs(ref.end)))
			}
			steps = append(steps,
				"setpts=PTS-STARTPTS",
				fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=bicubic+accurate_rnd+bitexact:in_color_matrix=%s:in_range=%s:out_color_matrix=bt709:out_range=tv",
					target.Width, target.Height, inMatrix(p), inRange(p)),
				fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", target.Width, target.Height),
				"setsar=1",
				fmt.Sprintf("fps=%s", rate.String()),
				"format=yuv420p",
				// Every chain ends on one timebase. Without this a dissolve is
				// refused as soon as one of its sides has been through a concat,
				// which leaves the two links counting in different units.
				"settb=AVTB",
				// The frames carry the colour they were converted to, rather
				// than the encoder being asked for it afterwards: videotoolbox
				// writes its own VUI and drops primaries and transfer.
				"setparams=range=tv:color_primaries=bt709:color_trc=bt709:colorspace=bt709",
			)
			chains = append(chains, fmt.Sprintf("[%d:v]%s[%s]", ref.index, strings.Join(steps, ","), label))
			vlabels = append(vlabels, label)
		}
		// Join them: a cut is a concat, a dissolve an xfade centred on it.
		acc, accLen := vlabels[0], vlen[0]
		for n := 1; n < len(vlabels); n++ {
			out := fmt.Sprintf("vj%d", n)
			clip := video.Clips[n]
			length := vlen[n]
			if clip.TransitionIn != nil {
				d := clip.TransitionIn.Duration
				chains = append(chains, fmt.Sprintf("[%s][%s]xfade=transition=fade:duration=%s:offset=%s[%s]",
					acc, vlabels[n], secs(d), secs(accLen-d), out))
				accLen += length - d
			} else {
				chains = append(chains, fmt.Sprintf("[%s][%s]concat=n=2:v=1:a=0[%s]", acc, vlabels[n], out))
				accLen += length
			}
			acc = out
		}
		chains = append(chains, fmt.Sprintf("[%s]trim=duration=%s,setpts=PTS-STARTPTS[vout]", acc, secs(duration)))
		r.VideoMap = "[vout]"
	}

	// The sound: every clip trimmed or padded to its picture, laid at its exact
	// sample over silence of the sequence's length, and summed without amix
	// dividing by how many there are (M8 Q14).
	silence := fmt.Sprintf("anullsrc=r=%d:cl=stereo", target.SampleRate)
	chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[abase]", silence, secs(duration)))
	alabels := []string{"abase"}
	for i, ref := range refs {
		if !ref.sound {
			continue
		}
		label := fmt.Sprintf("a%d", i)
		body := label + "b"
		length := ref.end - ref.start
		steps := []string{
			fmt.Sprintf("atrim=start=%s:end=%s", secs(ref.start), secs(ref.end)),
			"asetpts=N/SR/TB",
			fmt.Sprintf("aresample=%d:async=1:first_pts=0", target.SampleRate),
			"aformat=sample_fmts=fltp:channel_layouts=stereo",
			// A clip's sound and its picture rarely end together: an h3 take's
			// sound outlasts its picture by 8 ms. Pad, then cut to the picture.
			"apad",
			fmt.Sprintf("atrim=duration=%s", secs(length)),
		}
		if ref.gain != 0 {
			steps = append(steps, fmt.Sprintf("volume=%sdB", secs(ref.gain)))
		}
		if ref.fade > 0 {
			steps = append(steps, fmt.Sprintf("afade=t=out:st=%s:d=%s:curve=tri", secs(length-ref.fade), secs(ref.fade)))
		}
		if ref.track == "V1" && ref.clip.TransitionIn != nil {
			steps = append(steps, fmt.Sprintf("afade=t=in:st=0:d=%s:curve=tri", secs(ref.clip.TransitionIn.Duration)))
		}
		steps = append(steps, "asetpts=N/SR/TB")
		chains = append(chains, fmt.Sprintf("[%d:a]%s[%s]", ref.index, strings.Join(steps, ","), body))
		// Silence before it, so it lands on its own sample rather than on a
		// millisecond adelay could name.
		start := ref.at
		if ref.track == "V1" && ref.clip.TransitionIn != nil {
			start -= ref.clip.TransitionIn.Duration / 2
		}
		if start > 0 {
			chains = append(chains, fmt.Sprintf("%s,atrim=duration=%s,asetpts=N/SR/TB[%sp]", silence, secs(start), label))
			chains = append(chains, fmt.Sprintf("[%sp][%s]concat=n=2:v=0:a=1[%s]", label, body, label))
		} else {
			chains = append(chains, fmt.Sprintf("[%s]anull[%s]", body, label))
		}
		alabels = append(alabels, label)
	}
	if len(alabels) == 1 {
		chains = append(chains, fmt.Sprintf("[abase]atrim=duration=%s,asetpts=N/SR/TB[aout]", secs(duration)))
	} else {
		chains = append(chains, fmt.Sprintf("[%s]amix=inputs=%d:normalize=0:duration=longest,atrim=duration=%s,asetpts=N/SR/TB[aout]",
			strings.Join(alabels, "]["), len(alabels), secs(duration)))
	}
	r.AudioMap = "[aout]"
	r.Filter = strings.Join(chains, ";")
	return r, nil
}

// handle is where the render reads a video clip from, which a dissolve widens
// by half its duration on each side, and the dissolve the clip starts.
type handle struct {
	start, end, fade float64
}

func handlesOf(clips []helm.TimelineClip, rate Rate) []handle {
	out := make([]handle, len(clips))
	for i, c := range clips {
		if c.Hold != nil {
			out[i] = handle{start: 0, end: *c.Hold}
		} else {
			out[i] = handle{start: inOf(c), end: outOf(c)}
		}
	}
	for i, c := range clips {
		if c.TransitionIn == nil || i == 0 {
			continue
		}
		half := c.TransitionIn.Duration / 2
		out[i].start -= half
		out[i-1].end += half
		out[i-1].fade = c.TransitionIn.Duration
		if out[i].start < 0 {
			out[i].start = 0
		}
	}
	return out
}

func inOf(c helm.TimelineClip) float64 {
	if c.In != nil {
		return *c.In
	}
	return 0
}

func outOf(c helm.TimelineClip) float64 {
	if c.Out != nil {
		return *c.Out
	}
	return 0
}

func gainOf(c helm.TimelineClip, track *float64) float64 {
	g := 0.0
	if track != nil {
		g += *track
	}
	if c.GainDb != nil {
		g += *c.GainDb
	}
	return g
}

// inMatrix and inRange say how to read a file's colour. A file that says
// nothing was written by a scaler that used BT.601, which is what h3's and
// ltx's pipelines do (M8 Q9).
func inMatrix(p Stream) string {
	switch p.ColorSpace {
	case "bt709":
		return "bt709"
	case "bt2020nc", "bt2020_ncl":
		return "bt2020nc"
	}
	return "bt601"
}

func inRange(p Stream) string {
	if p.ColorRange == "pc" || p.ColorRange == "full" {
		return "pc"
	}
	return "tv"
}

// secs writes a time the way ffmpeg reads it, with enough places to name a
// sample at 48 kHz and no exponent.
func secs(v float64) string {
	if math.Abs(v) < 1e-9 {
		return "0"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
}
