// Package media_test is the export pipeline's golden test: it renders the
// fixtures beside it and checks what came out, frame by frame and sample by
// sample.
//
// What is compared, and why (docs/decisions.md M8 Q15):
//   - A copy must be bit-identical, so the export's decoded frames are compared
//     with its sources' decoded frames, which no encoder touched.
//   - A conform cannot be compared with anything but itself, and videotoolbox's
//     bytes move with the operating system, so the goldens hash what the filter
//     graph produced before any encoder saw it.
//   - The encoded file is then checked by probing it, never by its bytes.
package media_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/export"
	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// EnvAllowMissing lets a machine with no ffmpeg skip these, as a missing
// browser may skip the visual regression. Without it, a missing ffmpeg fails:
// a silent skip is how a gate stays green without ever running anything (M4
// second review #10).
const EnvAllowMissing = "HELM_ALLOW_MISSING_FFMPEG"

const (
	fixtures  = "fixtures"
	goldenDir = "golden"
	updateEnv = "HELM_UPDATE_GOLDEN"
)

func tool(t *testing.T) *export.Tool {
	t.Helper()
	tl, err := export.Find()
	if err != nil {
		if os.Getenv(EnvAllowMissing) != "" {
			t.Skipf("%v; skipped because %s is set", err, EnvAllowMissing)
		}
		t.Fatalf("%v; the export tests need ffmpeg, or set %s=1 to skip on purpose", err, EnvAllowMissing)
	}
	return tl
}

// pins ties the goldens to the build that made them. An encoder is not
// involved, but a scaler and a resampler are, and both are free to improve.
func checkPin(t *testing.T, tl *export.Tool) {
	t.Helper()
	pin := filepath.Join(goldenDir, "FFMPEG_MAJOR")
	want := fmt.Sprintf("%d %s\n", tl.Major, runtime.GOARCH)
	if os.Getenv(updateEnv) != "" {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pin, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(pin)
	if err != nil {
		t.Fatalf("no goldens (%v); run make golden-media", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		t.Fatalf("the goldens were made with ffmpeg %s and this is %s; scalers and resamplers change between majors, so regenerate deliberately with make golden-media and look at what moved",
			strings.TrimSpace(string(got)), strings.TrimSpace(want))
	}
}

func probeFixture(t *testing.T, tl *export.Tool, name string) timeline.Stream {
	t.Helper()
	s, err := tl.Probe(context.Background(), filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("probing %s: %v", name, err)
	}
	return s
}

// scene is a document and everything the pipeline needs to render it.
type scene struct {
	target helm.TimelineTarget
	tracks []helm.TimelineTrack
	probes timeline.Probes
	files  timeline.Sources
}

// build assembles a document from fixture files, probing each one, so the
// sources the rules see are the files themselves.
func build(t *testing.T, tl *export.Tool, target helm.TimelineTarget, tracks []helm.TimelineTrack, names map[string]string) scene {
	t.Helper()
	probes := timeline.Probes{}
	files := timeline.Sources{}
	sources := map[string]timeline.Source{}
	for id, name := range names {
		path, err := filepath.Abs(filepath.Join(fixtures, name))
		if err != nil {
			t.Fatal(err)
		}
		files[id] = path
		p := probeFixture(t, tl, name)
		probes[id] = p
		kind := timeline.KindVideo
		switch {
		case strings.HasSuffix(name, ".png"):
			kind = timeline.KindImage
		case p.Codec == "" && p.HasAudio:
			kind = timeline.KindAudio
		}
		sources[id] = timeline.Source{Kind: kind, Duration: p.Duration, StudioID: "fixture-studio"}
	}
	normalized, err := timeline.Normalize(target, tracks, sources)
	if err != nil {
		t.Fatalf("the document is not valid: %v", err)
	}
	return scene{target: target, tracks: normalized, probes: probes, files: files}
}

// render runs an export and returns the file it wrote.
func (s scene) render(t *testing.T, tl *export.Tool, mode string) string {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "concat.txt")
	out := filepath.Join(dir, "export.mp4")
	r, err := timeline.Build(s.target, s.tracks, s.probes, s.files, mode, list)
	if err != nil {
		t.Fatalf("building the render: %v", err)
	}
	if r.ConcatList != "" {
		if err := os.WriteFile(list, []byte(r.ConcatList), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args, err := r.EncodeArgs(s.target, helm.ExportPresetH264, out)
	if err != nil {
		t.Fatal(err)
	}
	run := export.Run{Tool: tl, Args: args, Log: &testWriter{t}}
	if err := run.Do(context.Background()); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return out
}

type testWriter struct{ t *testing.T }

func (w *testWriter) Write(p []byte) (int, error) {
	if s := strings.TrimSpace(string(p)); s != "" {
		w.t.Logf("ffmpeg: %s", s)
	}
	return len(p), nil
}

// frameHashes is one md5 per decoded frame of a stream, which is what "the
// picture is bit-identical" means when no encoder is allowed to differ.
func frameHashes(t *testing.T, tl *export.Tool, path, stream string) []string {
	t.Helper()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", path, "-map", stream, "-f", "framemd5", "-"}
	out := mustRun(t, tl.FFmpeg, args...)
	var hashes []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		hashes = append(hashes, strings.TrimSpace(fields[len(fields)-1]))
	}
	return hashes
}

// mono decodes a file's sound to one channel of 16-bit samples at rate, so a
// test can say exactly where a sound begins.
func mono(t *testing.T, tl *export.Tool, path string, rate int64) []int16 {
	t.Helper()
	cmd := exec.Command(tl.FFmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", path, "-map", "0:a:0", "-f", "s16le", "-acodec", "pcm_s16le", "-ac", "1", "-ar", strconv.FormatInt(rate, 10), "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("decoding the sound of %s: %v", path, err)
	}
	raw := out.Bytes()
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples
}

// bursts is where each 20 ms tone begins, in samples. The fixtures put one at
// the very first sample of every clip, so these are where the export believes
// its clips start.
func bursts(samples []int16, rate int64) []int64 {
	const loud = 6000 // the tone decodes near 17000; silence is 0
	quiet := int64(rate / 10)
	var out []int64
	var since int64 = quiet
	for i, s := range samples {
		if abs16(s) < loud {
			since++
			continue
		}
		if since >= quiet {
			out = append(out, int64(i))
		}
		since = 0
	}
	return out
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func mustRun(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v: %s", bin, strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

func clip(asset string, fields ...func(*helm.TimelineClip)) helm.TimelineClip {
	c := helm.TimelineClip{AssetID: asset}
	for _, f := range fields {
		f(&c)
	}
	return c
}

func out(v float64) func(*helm.TimelineClip)  { return func(c *helm.TimelineClip) { c.Out = &v } }
func in(v float64) func(*helm.TimelineClip)   { return func(c *helm.TimelineClip) { c.In = &v } }
func at(v float64) func(*helm.TimelineClip)   { return func(c *helm.TimelineClip) { c.At = &v } }
func hold(v float64) func(*helm.TimelineClip) { return func(c *helm.TimelineClip) { c.Hold = &v } }

func dissolve(d float64) func(*helm.TimelineClip) {
	return func(c *helm.TimelineClip) { c.TransitionIn = &helm.TimelineTransition{Type: "dissolve", Duration: d} }
}

func target(w, h int64, fps float64) helm.TimelineTarget {
	return helm.TimelineTarget{Width: w, Height: h, FPS: fps, SampleRate: 48000}
}

func videoTrack(clips ...helm.TimelineClip) helm.TimelineTrack {
	return helm.TimelineTrack{Kind: timeline.KindVideo, Clips: clips}
}

func audioTrack(clips ...helm.TimelineClip) helm.TimelineTrack {
	return helm.TimelineTrack{Kind: timeline.KindAudio, Clips: clips}
}

// twoTakes is the run of takes the fast path exists for.
func twoTakes(t *testing.T, tl *export.Tool) scene {
	return build(t, tl, target(320, 180, 24),
		[]helm.TimelineTrack{videoTrack(clip("a", out(2)), clip("b", out(1.5)))},
		map[string]string{"a": "take-a.mp4", "b": "take-b.mp4"})
}

// The common case takes the fast path, and its picture comes out frame for
// frame identical to the takes that went in.
func TestACopiedExportKeepsItsPictureBitForBit(t *testing.T) {
	tl := tool(t)
	s := twoTakes(t, tl)
	mode, reasons := timeline.Plan(s.target, s.tracks, s.probes)
	if mode != timeline.ModeCopy {
		t.Fatalf("mode %q because %v, want copy", mode, reasons)
	}
	out := s.render(t, tl, mode)

	want := append(frameHashes(t, tl, filepath.Join(fixtures, "take-a.mp4"), "0:v:0"),
		frameHashes(t, tl, filepath.Join(fixtures, "take-b.mp4"), "0:v:0")...)
	got := frameHashes(t, tl, out, "0:v:0")
	if len(got) != len(want) {
		t.Fatalf("the export has %d frames and its sources have %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d differs from the source it was copied from", i)
		}
	}
}

// The sound is never copied: every clip's own priming and padding is dropped,
// so each clip's sound starts with its picture and the export is exactly as
// long as the sequence (M8 Q13, Q15).
func TestACopiedExportsSoundStartsWithEveryClip(t *testing.T) {
	tl := tool(t)
	s := twoTakes(t, tl)
	out := s.render(t, tl, timeline.ModeCopy)
	checkSound(t, tl, out, s, []int64{0, 2 * 48000})
}

// A clip with no sound of its own gets silence for exactly its span, and the
// clips after it still land where their pictures do (M8 Q14). Without that the
// graph would either refuse the clip for having no audio input to take, or
// close the gap and pull every later clip early.
func TestAClipWithNoSoundGetsSilenceForItsSpan(t *testing.T) {
	tl := tool(t)
	s := build(t, tl, target(320, 180, 24),
		[]helm.TimelineTrack{videoTrack(clip("a", out(2)), clip("e", out(1)), clip("b", out(1.5)))},
		map[string]string{"a": "take-a.mp4", "e": "take-e-silent.mp4", "b": "take-b.mp4"})

	if p := s.probes["e"]; p.HasAudio {
		t.Fatal("take-e-silent.mp4 has sound, so this test would prove nothing")
	}
	mode, reasons := timeline.Plan(s.target, s.tracks, s.probes)
	if mode != timeline.ModeCopy {
		t.Fatalf("mode %q because %v, want copy: a silent clip is still copyable picture", mode, reasons)
	}
	out := s.render(t, tl, mode)
	// Two tones, not three: take-a's at zero and take-b's after the silent
	// clip's whole second, which is where its picture starts.
	checkSound(t, tl, out, s, []int64{0, 3 * 48000})
}

// checkSound holds an export's sound to the sequence: the right length, and a
// clip's tone where that clip starts.
func checkSound(t *testing.T, tl *export.Tool, path string, s scene, wantBursts []int64) {
	t.Helper()
	const tolerance = 1100 // one AAC frame: a transient spreads over its window
	samples := mono(t, tl, path, s.target.SampleRate)
	want := timeline.Samples(s.target, s.tracks)
	if drift := int64(len(samples)) - want; drift < -tolerance || drift > tolerance {
		t.Errorf("the export has %d samples and the sequence is %d: %+d", len(samples), want, drift)
	}
	got := bursts(samples, s.target.SampleRate)
	if len(got) != len(wantBursts) {
		t.Fatalf("found %v tone(s), want %d at %v", got, len(wantBursts), wantBursts)
	}
	for i, w := range wantBursts {
		if d := got[i] - w; d < -tolerance || d > tolerance {
			t.Errorf("clip %d's sound starts at sample %d, want %d: %+d samples (%.0f ms) out",
				i+1, got[i], w, d, float64(d)*1000/float64(s.target.SampleRate))
		}
	}
}

// The check above has to be able to fail, or it proves nothing. A plain concat
// stream copy — the obvious implementation, and the one the design's own words
// suggest — drags each clip's priming into the cut.
func TestTheSoundCheckCatchesAPlainStreamCopy(t *testing.T) {
	tl := tool(t)
	s := twoTakes(t, tl)
	dir := t.TempDir()
	list := filepath.Join(dir, "concat.txt")
	naive := filepath.Join(dir, "naive.mp4")
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	if err := os.WriteFile(list, []byte(fmt.Sprintf("file '%s'\nfile '%s'\n",
		abs(filepath.Join(fixtures, "take-a.mp4")), abs(filepath.Join(fixtures, "take-b.mp4")))), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, tl.FFmpeg, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-movflags", "+faststart", naive)

	samples := mono(t, tl, naive, s.target.SampleRate)
	got := bursts(samples, s.target.SampleRate)
	if len(got) != 2 {
		t.Fatalf("found %v tones in the naive copy, want 2", got)
	}
	drift := got[1] - 2*48000
	if drift <= 1100 {
		t.Fatalf("the naive copy put the second clip's sound %d samples out, which this test would not catch", drift)
	}
	t.Logf("a plain stream copy puts the second clip's sound %d samples (%.0f ms) late", drift, float64(drift)*1000/48000)
}

// mixed is everything the conform path has to deal with at once: a clip that
// already matches, one at another size, a still, one at another frame rate, a
// dissolve, and a voice line at another sample rate with one channel.
func mixed(t *testing.T, tl *export.Tool) scene {
	return build(t, tl, target(320, 180, 24),
		[]helm.TimelineTrack{
			videoTrack(
				clip("a", out(2)),
				clip("d", out(1.5)),
				clip("still", hold(1)),
				clip("c", in(0.25), out(1.5), dissolve(0.5)),
			),
			audioTrack(clip("voice", out(1), at(1))),
		},
		map[string]string{
			"a": "take-a.mp4", "d": "take-d-640x360.mp4",
			"still": "still.png", "c": "take-c-25fps.mp4", "voice": "voice.wav",
		})
}

// Everything that stops a copy is named, one reason per clip that stops it.
func TestAMixedSequenceSaysWhyItCannotCopy(t *testing.T) {
	tl := tool(t)
	s := mixed(t, tl)
	mode, reasons := timeline.Plan(s.target, s.tracks, s.probes)
	if mode != timeline.ModeConform {
		t.Fatalf("mode %q, want conform", mode)
	}
	want := map[string]bool{"size": false, "image": false, "frame_rate": false, "transition": false, "trimmed": false}
	for _, r := range reasons {
		if _, ok := want[r.Code]; ok {
			want[r.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("no reason coded %s in %v", code, reasons)
		}
	}
}

// The conform graph is checked by what it produced, frame by frame and sample
// by sample, before any encoder saw it: videotoolbox's bytes move with the
// operating system, and these do not (M8 Q15).
func TestTheConformGraphMatchesItsGolden(t *testing.T) {
	tl := tool(t)
	checkPin(t, tl)
	s := mixed(t, tl)
	r, err := timeline.Build(s.target, s.tracks, s.probes, s.files, timeline.ModeConform, "")
	if err != nil {
		t.Fatalf("building the render: %v", err)
	}
	got := mustRun(t, tl.FFmpeg, r.HashArgs(s.target)...)
	golden := filepath.Join(goldenDir, "mixed.framemd5")
	if os.Getenv(updateEnv) != "" {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run make golden-media", err)
	}
	if got != string(want) {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range gotLines {
			if i >= len(wantLines) || gotLines[i] != wantLines[i] {
				t.Fatalf("the graph's output differs from its golden at line %d:\n got %s\nwant %s",
					i+1, gotLines[i], lineAt(wantLines, i))
			}
		}
		t.Fatalf("the graph produced %d lines and its golden has %d", len(gotLines), len(wantLines))
	}
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "(nothing: the golden ends here)"
}

// The encoded file is checked by probing it, never by its bytes.
func TestAConformedExportIsTheTargetItDeclared(t *testing.T) {
	tl := tool(t)
	s := mixed(t, tl)
	out := s.render(t, tl, timeline.ModeConform)
	p, err := tl.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	want := timeline.Duration(s.target, s.tracks)
	switch {
	case p.Codec != "h264":
		t.Errorf("codec %q, want h264", p.Codec)
	case p.Width != 320 || p.Height != 180:
		t.Errorf("size %dx%d, want 320x180", p.Width, p.Height)
	case !p.FrameRate.Equal(timeline.Rate{Num: 24, Den: 1}):
		t.Errorf("frame rate %s, want 24/1", p.FrameRate)
	case p.PixelFormat != "yuv420p":
		t.Errorf("pixel format %q, want yuv420p", p.PixelFormat)
	case p.ColorSpace != "bt709" || p.ColorPrimaries != "bt709" || p.ColorTransfer != "bt709" || p.ColorRange != "tv":
		t.Errorf("colour is %s/%s/%s/%s, want bt709 tv throughout", p.ColorRange, p.ColorSpace, p.ColorPrimaries, p.ColorTransfer)
	case p.SampleRate != 48000 || p.Channels != 2:
		t.Errorf("sound is %d Hz with %d channels, want 48000 stereo", p.SampleRate, p.Channels)
	}
	if d := p.Duration - want; d < -0.05 || d > 0.05 {
		t.Errorf("the export is %.3fs and the sequence is %.3fs", p.Duration, want)
	}
}

// A copy keeps its sources' colour tags, because nothing converted the picture
// and relabelling it would be a lie (M8 Q9, the correction to Q13).
func TestACopiedExportKeepsItsSourcesColourTags(t *testing.T) {
	tl := tool(t)
	s := twoTakes(t, tl)
	out := s.render(t, tl, timeline.ModeCopy)
	got, err := tl.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	src := s.probes["a"]
	if got.ColorSpace != src.ColorSpace || got.ColorRange != src.ColorRange ||
		got.ColorPrimaries != src.ColorPrimaries || got.ColorTransfer != src.ColorTransfer {
		t.Errorf("the copy is tagged %s/%s/%s/%s and its sources are %s/%s/%s/%s",
			got.ColorRange, got.ColorSpace, got.ColorPrimaries, got.ColorTransfer,
			src.ColorRange, src.ColorSpace, src.ColorPrimaries, src.ColorTransfer)
	}
}
