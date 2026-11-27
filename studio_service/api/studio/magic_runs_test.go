package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/registry"
)

// Plan 39 phase D (API 1.81): the standard request → a run's inputs per pack (each resolves under the run rules),
// the greyed fields, the status derivation, the estimate's units and the ask card's words.

var board = magic.Target{Width: 1280, Height: 720, FPS: 24, SampleRate: 48000}

func block(kind string, length int, request string) *magic.Block {
	var req map[string]any
	if err := json.Unmarshal([]byte(request), &req); err != nil {
		panic(err)
	}
	req["kind"] = kind
	return &magic.Block{ID: "b1", Lane: "l1", Length: length, Kind: kind, Name: "Shot 1", Request: req}
}

// Each pack's standard block fills its params so that the run resolves; what it cannot fill is left out.
func TestStandardInputsPerPack(t *testing.T) {
	take, frame := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	pics := magicPics{First: take, Last: frame, Refs: []string{take}}
	voice := "55555555-5555-4555-8555-555555555555"
	cases := []struct {
		prefix, source, id, kind, request string
		pics                              magicPics
		voice                             string
		want                              string
	}{
		{"qwen-image.", "mac.torch", "qwen-image", "image", `{"prompt":"a title card","width":1920,"height":1080}`, pics, "",
			`canvas.height=1088 canvas.mode=fixed canvas.width=1920 encode.refs=[map[image:map[take_id:` + take +
				`]]] prompt.text=a title card seed.seed=42`},
		{"minimax-h3-image.", "mac.torch", "minimax-h3-image", "image", `{"prompt":"wet glass"}`, magicPics{}, "",
			`canvas.height=736 canvas.mode=fixed canvas.width=1280 encode.refs=[] prompt.text=wet glass seed.seed=42`},
		// LTX: 8 s at 24 fps is 193 frames on its 8k + 1 grid; the frames are keyframe rows at 0 s and the last frame
		{"ltx.", "mac.mlx", "ltx-image-to-video", "video", `{"prompt":"rain"}`, pics, "",
			`canvas.height=704 canvas.width=1280 duration.frames=193 keyframes.bypass=false keyframes.images=[map[image:map[take_id:` +
				take + `] seconds:0] map[image:map[take_id:` + frame + `] seconds:8]] prompt.text=rain seed.seed=42`},
		// no frame: LTX's keyframes node is bypassed (text to video)
		// Advanced keeps only the model's own (generate.steps is MiniMax H3's, kept from a switch: dropped)
		{"ltx.", "mac.mlx", "ltx-image-to-video", "video", `{"prompt":"rain","advanced":{"sample1.steps":8,"generate.steps":6}}`, magicPics{}, "",
			`canvas.height=704 canvas.width=1280 duration.frames=193 keyframes.bypass=true prompt.text=rain sample1.steps=8 seed.seed=42`},
		{"minimax-h3.", "mac.native", "minimax-h3-image-to-video", "video", `{"prompt":"bus"}`, magicPics{First: take}, "",
			`canvas.height=736 canvas.width=1280 duration.frames=192 first.image=map[take_id:` + take + `] prompt.text=bus seed.seed=42`},
		{"qwen3-tts.", "mac.torch", "qwen3-tts-voice-over", "voice", `{"line":"Tonight I let it go."}`, magicPics{}, voice,
			`prompt.text=Tonight I let it go. seed.seed=42 speak.voice=map[voice_id:` + voice + `]`},
		{"zonos3.", "mac.native", "zonos3-voice-over", "voice", `{"line":"Hi."}`, magicPics{}, voice,
			`prompt.text=Hi. seed.seed=42 speak.voice=map[voice_id:` + voice + `]`},
		// echo video has no frames or seed: none is set
		{"", "fake.fake", "echo-video", "video", `{"prompt":"fox"}`, pics, "",
			`canvas.height=576 canvas.width=1024 duration.frames=121 prompt.text=fox`},
	}
	for _, c := range cases {
		w := canonical(t, c.prefix, c.source, c.id)
		if w.Standard == nil || w.Standard.Kind != c.kind {
			t.Fatalf("%s: standard %+v", c.id, w.Standard)
		}
		inputs := standardInputs(w, block(c.kind, 192, c.request), board, c.pics, c.voice, 42)
		if got := flat(inputs); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.id, got, c.want)
		}
		if _, detail := resolve(w, inputs); detail != "" {
			t.Errorf("%s: %s", c.id, detail)
		}
	}
}

// A model's required picks are why a block cannot run: MiniMax H3's first frame, a voice model's voice.
func TestWhyNotNeeds(t *testing.T) {
	ws := []registry.Workflow{canonical(t, "minimax-h3.", "mac.native", "minimax-h3-image-to-video"),
		canonical(t, "qwen3-tts.", "mac.torch", "qwen3-tts-voice-over")}
	d := magic.Doc{Target: board}
	shot, line := block("video", 96, `{"prompt":"bus"}`), block("voice", 48, `{"line":"hi"}`)
	line.ID = "b2"
	d.Blocks = []*magic.Block{shot, line}
	if got := whyNot(ws, d, shot); got != "MiniMax H3 needs a first frame" {
		t.Fatal(got)
	}
	if got := whyNot(ws, d, line); got != "Qwen3-TTS needs a voice" {
		t.Fatal(got)
	}
	shot.Request["first"] = map[string]any{"block": "b2"}
	line.Request["voice_id"] = "55555555-5555-4555-8555-555555555555"
	if got := whyNot(ws, d, shot) + whyNot(ws, d, line); got != "" {
		t.Fatal(got)
	}
	shot.Request["first"] = map[string]any{"block": "gone"} // a pick of a block no longer on the board
	if got := whyNot(ws, d, shot); got != "MiniMax H3 needs a first frame" {
		t.Fatal(got)
	}
}

func flat(m map[string]any) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// GET /magic/kinds: the fake host's models, fastest first, each field it cannot fill greyed with why; Music, Sound
// and board frames off.
func TestMagicKindsGreying(t *testing.T) {
	ws := []registry.Workflow{canonical(t, "", "fake.fake", "echo-video"), canonical(t, "", "fake.fake", "echo-image-to-video"),
		canonical(t, "", "fake.fake", "echo"), canonical(t, "ltx.", "mac.mlx", "ltx-image-to-video")}
	kinds := magicKinds(ws)
	got := map[string]string{}
	for _, k := range kinds.Kinds {
		var models []string
		for _, m := range k.Models {
			models = append(models, fmt.Sprintf("%s%v%v", m.Name, m.Fields, m.Greyed))
		}
		got[k.Kind] = strings.Join(models, "; ") + "|" + k.Off
	}
	want := map[string]string{
		"video": "Echo keyframes[prompt first last length size]map[seed:Echo keyframes can't take a seed]; " +
			"Echo video[prompt length size]map[first:Echo video can't take a first frame last:Echo video can't take a last frame seed:Echo video can't take a seed]; " +
			"LTX 2.5[prompt first last length size seed]map[]|",
		"image": "Echo[prompt refs]map[seed:Echo can't take a seed size:Echo can't take a size]|",
		"voice": "|", "music": "|" + offMusicSound, "sound": "|" + offMusicSound, "frame": "|" + offFrame,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s:\n got %s\nwant %s", k, got[k], v)
		}
	}
	// a model's Advanced: the params its fields do not fill, the workflow's own value as the default
	for _, m := range kinds.Kinds[0].Models {
		if m.WorkflowID == "ltx-image-to-video" {
			var paths []string
			for _, p := range m.Advanced {
				paths = append(paths, p.Path)
			}
			if s := strings.Join(paths, " "); !strings.Contains(s, "sample1.steps") || strings.Contains(s, "prompt.text") ||
				strings.Contains(s, "duration.frames") || strings.Contains(s, "keyframes.images") {
				t.Fatal(s)
			}
		}
	}
}

// The status: running while a run is open, failed with no take after a failed run, done while the shown take's run
// was made from the inputs as they are, out of date once they change; a block's own inputs include what it picks.
func TestBlockStatuses(t *testing.T) {
	t1 := "33333333-3333-4333-8333-333333333333"
	d := magic.Doc{Target: board, Lanes: []*magic.Lane{{ID: "l1", Name: "Shots", Role: "shots"}}}
	a := block("video", 96, `{"prompt":"a"}`)
	b := block("video", 96, `{"prompt":"b","first":{"block":"b1","part":"last"}}`)
	b.ID, b.At = "b2", 96
	d.Blocks = []*magic.Block{a, b}
	runs := []magicRunRow{{Block: "b1", Status: "failed", Error: ptr("out of memory")},
		{Block: "b2", Status: "running", Progress: json.RawMessage(`{"step":3,"of":4}`)}}
	st := blockStatuses(d, runs)
	if st["b1"].Status != "failed" || st["b1"].Error != "out of memory" || st["b2"].Status != "running" || *st["b2"].Progress != 75 {
		t.Fatal(st)
	}
	a.Takes = []string{t1}
	runs = []magicRunRow{{Block: "b1", Status: "succeeded", Takes: []string{t1}, InputsHash: inputsHash(d, a)},
		{Block: "b2", Status: "succeeded", Takes: []string{t1}, InputsHash: inputsHash(d, b)}}
	b.Takes = []string{t1}
	if st := blockStatuses(d, runs); st["b1"].Status != "done" || st["b2"].Status != "done" {
		t.Fatal(st)
	}
	// Shot 1's take changes (a new one pinned): Shot 2, made from its last frame, is out of date; so is a changed prompt
	t2 := "44444444-4444-4444-8444-444444444444"
	a.Takes = append(a.Takes, t2)
	if st := blockStatuses(d, runs); st["b2"].Status != "out_of_date" || st["b1"].Status != "done" {
		t.Fatal(st) // b1 shows t2, made by no board run: done
	}
	a.Takes = a.Takes[:1]
	a.Request["prompt"] = "a, later"
	if st := blockStatuses(d, runs); st["b1"].Status != "out_of_date" || st["b2"].Status != "done" {
		t.Fatal(st)
	}
	if st := blockStatuses(d, nil); st["b1"].Status != "done" { // a take from no board run (placed): done
		t.Fatal(st)
	}
	b.Takes = nil
	if st := blockStatuses(d, nil); st["b2"].Status != "draft" {
		t.Fatal(st)
	}
}

func ptr[T any](v T) *T { return &v }

// The estimate's units and median, and the ask card's line.
func TestEstimateParts(t *testing.T) {
	if u := magicUnits("video", 1280, 720, 8); u != 8 {
		t.Fatal(u)
	}
	if u := magicUnits("image", 1000, 1000, 0); u != 1 {
		t.Fatal(u)
	}
	if m := median([]float64{5, 1, 3, 100}); m != 4 {
		t.Fatal(m)
	}
	d := magic.Doc{Target: board}
	if u := blockUnits(d, block("video", 96, `{"width":640,"height":360}`)); u != 1 {
		t.Fatal(u) // 4 s at a quarter of 720p
	}
	six, forty := 360.0, 40.0
	for _, c := range []struct {
		kinds []string
		s     *float64
		want  string
	}{{[]string{"video", "video", "video"}, &six, "3 shots, about 6 min"}, {[]string{"voice"}, &forty, "1 line, about 40 s"},
		{[]string{"video", "image"}, nil, "2 blocks, time unknown"}} {
		if got := askText(c.kinds, c.s); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	// a size scaled into the params' bounds keeps its aspect, then sits on their steps
	max, step := 1024.0, 64.0
	p := registry.Param{Min: &step, Max: &max, Step: &step}
	if w, h := fitSize(1920, 1080, p, p); w != 1024 || h != 576 {
		t.Fatal(w, h)
	}
}
