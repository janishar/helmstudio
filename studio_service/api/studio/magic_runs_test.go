package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
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
		// a still with no size of its own: the board's shape at 1K (1280 × 720 → 1024 × 576), not its video size
		{"minimax-h3-image.", "mac.torch", "minimax-h3-image", "image", `{"prompt":"wet glass"}`, magicPics{}, "",
			`canvas.height=576 canvas.mode=fixed canvas.width=1024 encode.refs=[] prompt.text=wet glass seed.seed=42`},
		// LTX: 8 s at 24 fps is 193 frames on its 8k + 1 grid; the frames are keyframe rows at 0 s and the last frame
		{"ltx.", "mac.mlx", "ltx-image-to-video", "video", `{"prompt":"rain"}`, pics, "",
			`canvas.height=704 canvas.width=1280 duration.frames=193 keyframes.bypass=false keyframes.images=[map[image:map[take_id:` +
				take + `] seconds:0] map[image:map[take_id:` + frame + `] seconds:8]] prompt.text=rain seed.seed=42`},
		// no frame: LTX's keyframes node is bypassed (text to video)
		// Advanced keeps only the model's own (generate.steps is MiniMax H3's, kept from a switch: dropped)
		{"ltx.", "mac.mlx", "ltx-image-to-video", "video", `{"prompt":"rain","advanced":{"sample1.steps":8,"generate.steps":6}}`, magicPics{}, "",
			`canvas.height=704 canvas.width=1280 duration.frames=193 keyframes.bypass=true prompt.text=rain sample1.steps=8 seed.seed=42`},
		// API 1.94: the variant goes as Advanced model.variant; one this model lacks (Qwen's turbo on LTX) is dropped
		{"qwen-image.", "mac.torch", "qwen-image", "image", `{"prompt":"fox","advanced":{"model.variant":"turbo"}}`, magicPics{}, "",
			`canvas.height=576 canvas.mode=fixed canvas.width=1024 encode.refs=[] model.variant=turbo prompt.text=fox seed.seed=42`},
		{"ltx.", "mac.mlx", "ltx-image-to-video", "video", `{"prompt":"rain","advanced":{"model.variant":"turbo"}}`, magicPics{}, "",
			`canvas.height=704 canvas.width=1280 duration.frames=193 keyframes.bypass=true prompt.text=rain seed.seed=42`},
		{"minimax-h3.", "mac.native", "minimax-h3-image-to-video", "video", `{"prompt":"bus"}`, magicPics{First: take}, "",
			`canvas.height=736 canvas.width=1280 duration.frames=192 first.image=map[take_id:` + take + `] prompt.text=bus seed.seed=42`},
		{"qwen3-tts.", "mac.torch", "qwen3-tts-voice-over", "voice", `{"line":"Tonight I let it go."}`, magicPics{}, voice,
			`prompt.text=Tonight I let it go. seed.seed=42 speak.voice=map[voice_id:` + voice + `]`},
		{"zonos3.", "mac.native", "zonos3-voice-over", "voice", `{"line":"Hi."}`, magicPics{}, voice,
			`prompt.text=Hi. seed.seed=42 speak.voice=map[voice_id:` + voice + `]`},
		// API 1.85, music and sound: 8 s on each length param's grid and bounds (MiniMax's and ACE-Step's least is
		// 10 s); empty lyrics are an instrumental; a key only when the param has it; MiniMax takes no key or BPM
		{"minimax-music3.", "mac.torch", "minimax-music3", "music", `{"prompt":"quiet piano","key":"D minor","bpm":72}`, magicPics{}, "",
			`lm.instrumental=true lm.lyrics= lm.seconds=10 prompt.text=quiet piano seed.seed=42`},
		{"ace-step.", "mac.torch", "ace-step-music", "music", `{"prompt":"piano","lyrics":"[verse] la","key":"D minor","bpm":72.4}`, magicPics{}, "",
			`prompt.text=piano seed.seed=42 song.bpm=72 song.instrumental=false song.key=D minor song.lyrics=[verse] la song.seconds=10`},
		{"ace-step.", "mac.torch", "ace-step-music", "music", `{"prompt":"piano","key":"H major"}`, magicPics{}, "",
			`prompt.text=piano seed.seed=42 song.instrumental=true song.lyrics= song.seconds=10`},
		{"stable-audio-3.", "mac.torch", "sa3-sound", "sound", `{"prompt":"rain on a shelter"}`, magicPics{}, "",
			`prompt.text=rain on a shelter seed.seed=42 sound.seconds=8`},
		{"mmaudio.", "mac.torch", "mmaudio-sound", "sound", `{"prompt":"rain"}`, magicPics{}, "",
			`prompt.text=rain seed.seed=42 sound.seconds=8`},
		// echo video has no frames or seed: none is set
		{"", "fake.fake", "echo-video", "video", `{"prompt":"fox"}`, pics, "",
			`canvas.height=576 canvas.width=1024 duration.frames=121 prompt.text=fox`},
	}
	for _, c := range cases {
		w := canonical(t, c.prefix, c.source, c.id)
		if w.Standard == nil || w.Standard.Kind != c.kind {
			t.Fatalf("%s: standard %+v", c.id, w.Standard)
		}
		inputs := standardInputs(w, magic.Doc{Target: board}, block(c.kind, 192, c.request), c.pics, c.voice, 42)
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
// off; a board frame is served by the image models (API 1.83).
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
		"voice": "|", "music": "|", "sound": "|",
		"frame": "Echo[prompt refs]map[seed:Echo can't take a seed size:Echo can't take a size]|",
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
			if m.Family != "ltx" {
				t.Fatal(m.Family)
			}
		} else if m.Family != "" { // echo has no model node
			t.Fatal(m)
		}
	}
	// API 1.94: a request's variant is its Advanced model.variant when it is one of the param's values, else the
	// workflow's own; echo has none
	ltx := ws[3]
	own, _ := ltx.Nodes["model"].Params["variant"].(string)
	adv := func(v any) map[string]any { return map[string]any{"advanced": map[string]any{"model.variant": v}} }
	if own == "" || runVariant(ltx, nil) != own || runVariant(ltx, adv("dev")) != "dev" ||
		runVariant(ltx, adv("nope")) != own || runVariant(ltx, adv(3)) != own || runVariant(ws[0], adv("dev")) != "" {
		t.Fatal(own, runVariant(ltx, adv("dev")))
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
	} // stills at 1K whatever the board's size: a 480p frame is 1024 × 576; views 3:4, 1:1, a place the board's shape
	if w, h := stillSize(854, 480); w != 1024 || h != 576 {
		t.Fatal(w, h)
	}
	sd := magic.Doc{Target: magic.Target{Width: 854, Height: 480, FPS: 24}}
	for kind, want := range map[string][2]float64{"character": {768, 1024}, "prop": {1024, 1024}, "place": {1024, 576}} {
		b := viewBlocks(sd, &magic.Asset{ID: "a1", Handle: "x", Kind: kind}, &dto.MagicViewsCreate{Views: []string{"Front"}}, "")[0]
		if b.Request["width"] != want[0] || b.Request["height"] != want[1] {
			t.Error(kind, b.Request)
		}
	}
	// API 1.98: the batch's own size and advanced win
	w, h := 640, 960
	adv := map[string]any{"model.variant": "base"}
	b := viewBlocks(sd, &magic.Asset{ID: "a1", Handle: "x", Kind: "prop"}, &dto.MagicViewsCreate{Views: []string{"Front"},
		Width: &w, Height: &h, Advanced: adv}, "")[0]
	if b.Request["width"] != 640.0 || b.Request["height"] != 960.0 || b.Request["advanced"].(map[string]any)["model.variant"] != "base" {
		t.Error(b.Request)
	}
	ws := []registry.Workflow{{ID: "echo", Env: "fake", Standard: &registry.Standard{Kind: "image", Model: "Echo"}},
		{ID: "qwen", Env: "qwen-image", Standard: &registry.Standard{Kind: "image", Model: "Qwen"}}}
	if viewModel(ws, "") != "qwen" || viewModel(ws, "echo") != "echo" || viewModel(ws[:1], "") != "" {
		t.Fatal("views' model")
	}
}

// API 1.83: a frame's prompt is its style, framing and words with who and where; a shot made from a frame with none
// of its own takes the frame's; the board's @handles become their names, with their picture's row.
func TestMagicPrompt(t *testing.T) {
	d := magic.Doc{Target: board, Assets: []*magic.Asset{{ID: "a1", Handle: "mara", Name: "Mara"},
		{ID: "a2", Handle: "bus-stop", Name: "Bus stop"}}}
	frame := block("frame", 96, `{"style":"rough","framing":{"size":"wide","angle":"low","move":"push_in"},
		"prompt":"@mara waits in the rain.","refs":[{"asset":"a1"},{"asset":"a2"}]}`)
	frame.ID = "f1"
	shot := block("video", 96, `{}`)
	shot.FromFrame = &frame.ID
	d.Blocks = []*magic.Block{frame, shot}
	cases := []struct {
		b       *magic.Block
		mention string
		refs    []string
		want    string
	}{
		{frame, "<image{n}>", []string{"a1", "a2"}, "A rough storyboard drawing: simple shapes, grey shading. Wide shot, from a low angle. " +
			"Mara <image1> waits in the rain. With Bus stop <image2>."},
		{frame, "", []string{"a1"}, "A rough storyboard drawing: simple shapes, grey shading. Wide shot, from a low angle. Mara waits in the rain. " +
			"With Bus stop."}, // a pick with no picture yet is still named
		{shot, "", nil, "Wide shot, from a low angle. Mara waits in the rain. The camera slowly pushes in."},
		{block("video", 96, `{"prompt":"@mara runs, @fox follows"}`), "", nil, "Mara runs, @fox follows"},
	}
	for i, c := range cases {
		if got := magicPrompt(d, c.b, c.mention, c.refs); got != c.want {
			t.Errorf("%d:\n got %s\nwant %s", i, got, c.want)
		}
	}
}

// API 1.85 (plan 39 phase H): Music and Sound. MiniMax Music 3 is the default; MMAudio's two workflows are one model
// whose `video` runs a request with `from`; a sound from a shot needs its take; a block keeps only takes of its kind.
func TestMagicMusicAndSound(t *testing.T) {
	ws := []registry.Workflow{canonical(t, "ace-step.", "mac.torch", "ace-step-music"),
		canonical(t, "minimax-music3.", "mac.torch", "minimax-music3"), canonical(t, "stable-audio-3.", "mac.torch", "sa3-sound"),
		canonical(t, "mmaudio.", "mac.torch", "mmaudio-video"), canonical(t, "mmaudio.", "mac.torch", "mmaudio-sound")}
	got := map[string]string{}
	for _, k := range magicKinds(ws).Kinds {
		var models []string
		for _, m := range k.Models {
			s := fmt.Sprintf("%s%v%v", m.WorkflowID, m.Fields, m.Greyed)
			if m.Video != nil {
				s += " video " + m.Video.WorkflowID
			}
			models = append(models, s)
		}
		got[k.Kind] = strings.Join(models, "; ")
	}
	want := map[string]string{
		"music": "minimax-music3[prompt lyrics length seed]map[bpm:MiniMax Music 3 can't take a BPM key:MiniMax Music 3 can't take a key]; " +
			"ace-step-music[prompt lyrics length key bpm seed]map[]",
		"sound": "sa3-sound[prompt length seed]map[from:Stable Audio 3 Small-SFX can't take a shot's video]; " +
			"mmaudio-sound[from prompt length seed]map[] video mmaudio-video",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s:\n got %s\nwant %s", k, got[k], v)
		}
	}
	take := "66666666-6666-4666-8666-666666666666"
	shot := block("video", 144, `{"prompt":"a bus"}`)
	shot.ID, shot.Name = "s2", "Shot 2 · bus"
	sound := block("sound", 96, `{"from":"s2"}`)
	d := magic.Doc{Target: board, Blocks: []*magic.Block{shot, sound}}
	if w, why := magicModel(ws, "sound", sound.Request); w.ID != "mmaudio-video" || why != "" {
		t.Fatal(w.ID, why) // no model: the first that takes a video
	}
	if got := whyNot(ws, d, sound); got != "Shot 2 has no take yet" {
		t.Fatal(got)
	}
	shot.Takes = []string{take}
	sound.Request["model"] = "mmaudio-sound"
	if got := whyNot(ws, d, sound); got != "" {
		t.Fatal(got)
	}
	w, _ := magicModel(ws, "sound", sound.Request)
	inputs := standardInputs(w, d, sound, magicPics{}, "", 7)
	if got := flat(inputs); w.ID != "mmaudio-video" || got != "foley.seconds=4 foley.source=map[take_id:"+take+"] prompt.text= seed.seed=7" {
		t.Fatal(w.ID, got)
	}
	if _, detail := resolve(w, inputs); detail != "" {
		t.Fatal(detail)
	}
	// a model named by its video workflow's id, made from a prompt again: the model's prompt workflow runs
	if w, why := magicModel(ws, "sound", map[string]any{"kind": "sound", "model": "mmaudio-video"}); w.ID != "mmaudio-sound" || why != "" {
		t.Fatal(w.ID, why)
	}
	sound.Request["model"] = "sa3-sound"
	if got := whyNot(ws, d, sound); got != "Stable Audio 3 Small-SFX can't take a shot's video" {
		t.Fatal(got)
	}
	sound.Request["from"] = "gone"
	delete(sound.Request, "model")
	if got := whyNot(ws, d, sound); got != "its shot is not on the board" {
		t.Fatal(got)
	}
	// the shot's take is an input: a new take puts the sound out of date
	sound.Request["from"] = "s2"
	before := inputsHash(d, sound)
	shot.Takes = append(shot.Takes, "77777777-7777-4777-8777-777777777777")
	if inputsHash(d, sound) == before {
		t.Fatal("the shot's take is not an input")
	}
	r := magicRunRow{Takes: []string{"a", "v"}, TakeKinds: []string{"audio", "video"}}
	if got := fitTakes(sound, r); fmt.Sprint(got) != "[a]" {
		t.Fatal(got)
	}
	if got := fitTakes(shot, r); fmt.Sprint(got) != "[a v]" {
		t.Fatal(got)
	}
	if got := askText([]string{"music", "music"}, ptr(30.0)); got != "2 cues, about 30 s" {
		t.Fatal(got)
	}
}

// API 1.89: a dialogue runs a line per turn: why it can't, each turn's status (one line changed puts only its turn
// out of date), the block's from its turns', and Run on and Loop are not inputs.
func TestDialogueTurns(t *testing.T) {
	ws := []registry.Workflow{canonical(t, "qwen3-tts.", "mac.torch", "qwen3-tts-voice-over")}
	voice := "55555555-5555-4555-8555-555555555555"
	d := magic.Doc{Target: board, Assets: []*magic.Asset{{ID: "a1", Kind: "character", Handle: "mara", Name: "Mara"},
		{ID: "a2", Kind: "character", Handle: "eli", Name: "Eli", VoiceID: &voice}}}
	b := block("voice", 96, `{"turns":[]}`)
	d.Blocks = []*magic.Block{b}
	for _, c := range []struct{ turns, want string }{
		{`[]`, "A dialogue needs a line"},
		{`[{"speaker":"a2","text":" "}]`, "Line 1 has no words"},
		{`[{"speaker":"a2","text":"Hi."},{"speaker":"gone","text":"Yo."}]`, "Line 2 has no speaker"},
		{`[{"speaker":"a2","text":"Hi."},{"speaker":"a1","text":"Yo."}]`, "Line 2's @mara has no voice"},
	} {
		var turns []any
		_ = json.Unmarshal([]byte(c.turns), &turns)
		b.Request["turns"] = turns
		if got := whyNot(ws, d, b); got != c.want {
			t.Fatalf("%s: %q", c.turns, got)
		}
	}
	d.Assets[0].VoiceID = &voice
	if got := whyNot(ws, d, b); got != "" {
		t.Fatal(got)
	}
	// the turn's run is the line block: its text, its speaker's voice, one run
	tb := turnBlock(b, 1)
	if tb.Request["line"] != "Yo." || tb.Request["voice"] != "a1" || tb.Request["turns"] != nil || b.Request["line"] != nil {
		t.Fatal(tb.Request)
	}
	if got := flat(standardInputs(ws[0], d, tb, magicPics{}, blockVoice(d, tb), 7)); !strings.Contains(got, "prompt.text=Yo.") ||
		!strings.Contains(got, voice) {
		t.Fatal(got)
	}
	t1, t2 := "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	zero, one := 0, 1
	runs := []magicRunRow{{Block: "b1", Turn: &zero, Status: "succeeded", Takes: []string{t1}, InputsHash: inputsHash(d, turnBlock(b, 0))},
		{Block: "b1", Turn: &one, Status: "running", Progress: json.RawMessage(`{"step":1,"of":2}`)}}
	b.Takes = []string{t1}
	st := blockStatuses(d, runs)["b1"]
	if st.Status != "running" || *st.Progress != 50 || len(st.Turns) != 2 || st.Turns[0].Status != "done" ||
		*st.Turns[0].Take != t1 || st.Turns[1].Status != "running" {
		t.Fatal(st)
	}
	runs[1] = magicRunRow{Block: "b1", Turn: &one, Status: "succeeded", Takes: []string{t2}, InputsHash: inputsHash(d, turnBlock(b, 1))}
	b.Takes = []string{t1, t2}
	if st := blockStatuses(d, runs)["b1"]; st.Status != "done" || *st.Turns[1].Take != t2 {
		t.Fatal(st)
	}
	b.Request["turns"].([]any)[1].(map[string]any)["text"] = "Yo, later."
	if st := blockStatuses(d, runs)["b1"]; st.Status != "out_of_date" || st.Turns[0].Status != "done" || st.Turns[1].Status != "out_of_date" {
		t.Fatal(st)
	}
	b.Request["turns"] = append(b.Request["turns"].([]any), map[string]any{"speaker": "a2", "text": "Bye."})
	if st := blockStatuses(d, runs)["b1"]; st.Status != "out_of_date" || st.Turns[2].Status != "draft" {
		t.Fatal(st) // a new line with no take yet: out of date
	}
	// API 1.90: a run's turn follows its line: moved up, then the line before it removed, it keeps its take current,
	// even past a newer run of the turn it was put in for a line now gone
	b.Request["turns"] = []any{map[string]any{"speaker": "a2", "text": "Hi."}, map[string]any{"speaker": "a1", "text": "Yo."}}
	h0, h1 := inputsHash(d, turnBlock(b, 0)), inputsHash(d, turnBlock(b, 1))
	t3 := "66666666-6666-4666-8666-666666666666"
	runs = []magicRunRow{{Block: "b1", Turn: &zero, Status: "succeeded", Takes: []string{t1}, InputsHash: h0},
		{Block: "b1", Turn: &one, Status: "succeeded", Takes: []string{t2}, InputsHash: h1}}
	b.Takes = []string{t1, t2}
	b.Request["turns"] = []any{b.Request["turns"].([]any)[1], b.Request["turns"].([]any)[0]} // Move up
	if st := blockStatuses(d, runs)["b1"]; st.Status != "done" || *st.Turns[0].Take != t2 || *st.Turns[1].Take != t1 {
		t.Fatal(st)
	}
	if runTurn(turnHashes(d, b), runs[0]) != 1 || runTurn(turnHashes(d, b), runs[1]) != 0 {
		t.Fatal("a moved line's runs follow it")
	}
	b.Request["turns"] = []any{map[string]any{"speaker": "a2", "text": "Hi."}, map[string]any{"speaker": "a1", "text": "Yo."}}
	runs = append(runs, magicRunRow{Block: "b1", Turn: &zero, Status: "succeeded", Takes: []string{t3}, InputsHash: h0})
	b.Takes = append(b.Takes, t3)
	b.Request["turns"] = b.Request["turns"].([]any)[1:] // Remove line 1: "Yo." is line 1 now
	if st := blockStatuses(d, runs)["b1"]; st.Status != "done" || len(st.Turns) != 1 || *st.Turns[0].Take != t2 {
		t.Fatal(st)
	}
	b.Request["turns"].([]any)[0].(map[string]any)["text"] = "Yo, later." // changed: its old take, out of date
	if st := blockStatuses(d, runs)["b1"]; st.Status != "out_of_date" || *st.Turns[0].Take != t3 {
		t.Fatal(st) // no run matches now: the newest run of the turn it ran for
	}
	// Run on and Loop change no take
	shot := block("sound", 96, `{"prompt":"rain"}`)
	h := inputsHash(d, shot)
	shot.Request["host"], shot.Request["loop"] = "gcp", true
	if inputsHash(d, shot) != h || *hostOf(shot) != "gcp" {
		t.Fatal("host and loop are inputs")
	}
}
