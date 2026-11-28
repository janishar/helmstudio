package timeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestExportRuns runs the built arguments through a local ffmpeg on two small generated clips and an image (skipped
// without ffmpeg): a conform export with dissolves (into a video and into an image), a gap, an image and an audio-track clip, then a copy export,
// each probed for its size, rate and length (the runner's check).
func TestExportRuns(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	clip := func(name string, freq int) string {
		path := filepath.Join(dir, name)
		run("-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=256x128:r=24:d=2", "-f", "lavfi",
			"-i", "sine=f="+strconv.Itoa(freq)+":d=2:r=48000", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
			"-shortest", path)
		return path
	}
	a, b := clip("a.mp4", 440), clip("b.mp4", 660)
	img := filepath.Join(dir, "i.png")
	run("-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=200x100", "-frames:v", "1", img)
	files := map[string]string{"A": a, "B": b, "I": img}
	vt := func(sha string) *Stream {
		return &Stream{Container: "mp4", Codec: "h264", Profile: "High", Level: 12, PixFmt: "yuv420p", Width: 256, Height: 128,
			FrameRate: "24/1", TimeBase: "1/12288", Color: map[string]any{}, ExtradataSHA256: sha}
	}
	takes := map[string]Take{"A": {Kind: "video", DurationS: 2, HasAudio: true, Stream: vt("x")},
		"B": {Kind: "video", DurationS: 2, HasAudio: true, Stream: vt("x")}, "I": {Kind: "image"}}
	keys := map[string]string{"A": "u/o/a.mp4", "B": "u/o/b.mp4", "I": "u/o/i.png"}
	id := func(s string) *string { return &s }
	written := Doc{Target: Target{256, 128, 24, 48000}, Tracks: []Track{{Kind: "video", Clips: []Clip{
		{ID: "a", TakeID: id("A"), In: new(0.0), Out: new(1.5)},
		{ID: "b", TakeID: id("B"), In: new(0.5), Out: new(1.75), TransitionIn: &Dissolve{"dissolve", 12}},
		{ID: "i", TakeID: id("I"), Hold: new(0.5), TransitionIn: &Dissolve{"dissolve", 6}}, // into an image: no source time
		{ID: "s", Hold: new(0.5)},
	}}, {Kind: "audio", Clips: []Clip{{ID: "m", TakeID: id("A"), In: new(0.0), Out: new(1.0), At: new(0.25), GainDB: new(-6.0)}}}}}
	doc, probs := Normalize(written, takes, nil)
	if probs != nil {
		t.Fatal(probs)
	}
	export := func(doc Doc, wantMode string) {
		t.Helper()
		byKey := map[string]string{}
		for take, key := range keys {
			byKey[key] = take
		}
		job, err := BuildExport(doc, takes, keys, ExportPlan(doc, takes))
		if err != nil || job.Mode != wantMode {
			t.Fatalf("%v %s", err, job.Mode)
		}
		out := filepath.Join(dir, wantMode+".mp4")
		list := filepath.Join(dir, "list.txt")
		var lines []string
		for _, n := range job.Concat {
			lines = append(lines, "file '"+files[byKey[job.Inputs[n].Key]]+"'")
		}
		var args []string
		for _, arg := range job.Args {
			switch {
			case arg == "{venc}":
				args = append(args, "-c:v", "libx264", "-b:v", strconv.Itoa(job.VideoBitrate))
			case arg == "{out}":
				args = append(args, out)
			case arg == "{list}":
				args = append(args, list)
			case strings.HasPrefix(arg, "{in:"):
				n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(arg, "{in:"), "}"))
				args = append(args, files[byKey[job.Inputs[n].Key]])
			default:
				args = append(args, arg)
			}
		}
		if len(lines) > 0 {
			if err := os.WriteFile(list, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		run(args...)
		probe, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_entries",
			"stream=codec_type,width,height,r_frame_rate,sample_rate,nb_read_frames", "-of", "csv=p=0", out).Output()
		if err != nil {
			t.Fatal(err)
		}
		got := string(probe)
		if !strings.Contains(got, "256,128,24/1,"+strconv.Itoa(doc.Frames)) || !strings.Contains(got, "48000") {
			t.Fatalf("%s export: %s (want %d frames)", wantMode, got, doc.Frames)
		}
	}
	export(*doc, "conform")
	whole, probs := Normalize(Doc{Target: written.Target, Tracks: []Track{{Kind: "video", Clips: []Clip{
		{ID: "a", TakeID: id("A")}, {ID: "b", TakeID: id("B"), GainDB: new(3.0)}}}}}, takes, nil)
	if probs != nil {
		t.Fatal(probs)
	}
	export(*whole, "copy")
	// 1.106: a V1 fade in and out, a crossfade that blends, one that dips, a fade out and ducking (music under a line)
	wav := func(name string, freq int) string {
		path := filepath.Join(dir, name)
		run("-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=f="+strconv.Itoa(freq)+":d=2:r=48000", path)
		return path
	}
	files["M"], files["O"] = wav("m.wav", 220), wav("o.wav", 880)
	takes["M"] = Take{Kind: "audio", DurationS: 2, HasAudio: true, Sound: "music"}
	takes["O"] = Take{Kind: "audio", DurationS: 2, HasAudio: true, Sound: "voice"}
	keys["M"], keys["O"] = "u/o/m.wav", "u/o/o.wav"
	faded, probs := Normalize(Doc{Target: written.Target, Ducking: &Ducking{On: true, DB: new(-12.0)}, Tracks: []Track{
		{Kind: "video", Clips: []Clip{{ID: "a", TakeID: id("A"), FadeIn: new(0.25), FadeOut: new(0.5)}}},
		{Kind: "audio", Clips: []Clip{
			{ID: "m1", TakeID: id("M"), In: new(0.0), Out: new(0.5), At: new(0.0), FadeIn: new(0.125), Crossfade: new(0.25)},
			{ID: "m2", TakeID: id("M"), In: new(1.5), Out: new(2.0), At: new(0.5), Crossfade: new(0.25)}, // no sound past: a dip
			{ID: "m3", TakeID: id("M"), In: new(0.0), Out: new(0.5), At: new(1.0), FadeOut: new(0.25)}}},
		{Kind: "audio", Clips: []Clip{{ID: "vo", TakeID: id("O"), In: new(0.0), Out: new(0.5), At: new(0.75)}}}}}, takes, nil)
	if probs != nil {
		t.Fatal(probs)
	}
	if dips := Dips(*faded, takes); len(dips) != 1 || !strings.HasPrefix(dips[0], "crossfade_dip: m2 ") {
		t.Fatal(dips)
	}
	job, _ := BuildExport(*faded, takes, keys, ExportPlan(*faded, takes))
	graph := strings.Join(job.Args, " ")
	for _, want := range []string{"afade=t=in:st=0:d=0.25:curve=tri", "afade=t=out:st=1.5:d=0.5:curve=tri",
		"atrim=start=0:end=0.75", "afade=t=out:st=0.5:d=0.25:curve=tri", // m1 blends: 0.25 past its out, faded over it
		"afade=t=out:st=0.375:d=0.125:curve=tri", // m2 dips: its last half
		"volume=eval=frame:volume='pow(10,-12*clip(min((t-(0.45))/0.3,(1.55-t)/0.3),0,1)/20)'"} {
		if !strings.Contains(graph, want) {
			t.Errorf("the graph has no %s:\n%s", want, graph)
		}
	}
	export(*faded, "copy")
}

// ducking under many lines stays a shallow expression (ffmpeg refuses one nested past about 100 deep)
func TestDuckExprShallow(t *testing.T) {
	spans := make([][2]float64, 1000)
	for i := range spans {
		spans[i] = [2]float64{float64(2 * i), float64(2*i + 1)}
	}
	depth, most := 0, 0
	for _, r := range duckExpr(-12, spans) {
		switch r {
		case '(':
			depth++
			most = max(most, depth)
		case ')':
			depth--
		}
	}
	if most > 40 {
		t.Fatalf("nested %d deep", most)
	}
}
