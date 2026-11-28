package magic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/timeline"
)

const (
	tA = "aaaaaaaa-0000-4000-8000-000000000001"
	tB = "aaaaaaaa-0000-4000-8000-000000000002"
	tC = "aaaaaaaa-0000-4000-8000-000000000003"
	tM = "aaaaaaaa-0000-4000-8000-000000000004"
	tV = "aaaaaaaa-0000-4000-8000-000000000005"
)

func sendBoard() Doc {
	str := func(s string) *string { return &s }
	return Doc{Target: DefaultTarget,
		Lanes: []*Lane{{ID: "story", Name: "Storyboard", Role: "storyboard"}, {ID: "shots", Name: "Shots", Role: "shots"},
			{ID: "lines", Name: "Lines", Role: "voice"}, {ID: "broll", Name: "B-roll", Role: "shots"}},
		Blocks: []*Block{
			{ID: "f1", Lane: "story", At: 0, Length: 48, Kind: "frame", Name: "Frame 1", Takes: []string{tC}},
			{ID: "s1", Lane: "shots", At: 0, Length: 96, Kind: "video", Name: "Shot 1 · pier", Takes: []string{tA}},
			{ID: "s2", Lane: "shots", At: 96, Length: 96, Kind: "video", Name: "Shot 2", Takes: []string{tB},
				Effects: []Effect{{Name: "Grade · warm", Params: map[string]any{"temperature": 12.0, "contrast": 4.0}}}},
			{ID: "s3", Lane: "shots", At: 192, Length: 96, Kind: "video", Name: "Shot 3", Takes: []string{tA}},
			{ID: "s4", Lane: "shots", At: 300, Length: 48, Kind: "video", Name: "Shot 4"},
			{ID: "v1", Lane: "lines", At: 100, Length: 300, Kind: "voice", Name: "Line 1", Takes: []string{tV}},
			{ID: "b1", Lane: "broll", At: 0, Length: 48, Kind: "video", Name: "Cutaway", Takes: []string{tA}},
		},
		Links: []*Link{{Block: "v1", To: []string{"s2"}}},
		Transitions: []*Transition{
			{ID: "t1", Start: str("shots"), Kind: "fade", Length: 24},
			{ID: "t2", Between: &[2]string{"s1", "s2"}, Kind: "wipe", Length: 12},
			{ID: "t3", Between: &[2]string{"s2", "s3"}, Kind: "morph", Length: 24, Takes: []string{tM}},
		},
	}.filled()
}

func sendTakes() map[string]timeline.Take {
	return map[string]timeline.Take{tA: {Kind: "video", DurationS: 4, HasAudio: true}, tB: {Kind: "video", DurationS: 4},
		tC: {Kind: "image"}, tM: {Kind: "video", DurationS: 1}, tV: {Kind: "audio", DurationS: 20}}
}

// The send keeps every board time: V1's clips add up to the last shot's end, the joins' bakes and the morph take the
// halves of their shots, the effects bake the rest of Shot 2, the gap before Shot 4 and Shot 4 itself are slots,
// the voice is linked to Shot 2's clip and cut at V1's end; the sequence passes the Timeline's rules.
func TestBuildSend(t *testing.T) {
	out := BuildSend(sendBoard(), sendTakes(), map[string]string{"s3": "out_of_date"}, nil, true)
	v1 := out.Doc.Tracks[0].Clips
	g := timeline.NewGrid(out.Doc.Target)
	total := 0
	for _, c := range v1 {
		switch {
		case c.Hold != nil:
			total += g.F(*c.Hold)
		default:
			total += g.F(*c.Out) - g.F(*c.In)
		}
	}
	if total != 348 {
		t.Fatalf("V1 is %d frames, want 348: %+v", total, v1)
	}
	kinds := map[string]int{}
	for _, b := range out.Bakes {
		kinds[b.Kind]++
	}
	if kinds["fade_in"] != 1 || kinds["wipe"] != 1 || kinds["effects"] != 1 || len(out.Bakes) != 3 {
		t.Fatalf("bakes %v", kinds)
	}
	for _, b := range out.Bakes {
		if b.Kind == "wipe" {
			if b.Frames != 12 || len(b.Sides) != 2 || b.Sides[0].Start != 90.0/24 || b.Sides[1].Pre != 6 {
				t.Fatalf("wipe %+v", b)
			}
			if len(b.Sides[1].Filters) != 2 {
				t.Fatalf("the wipe's second side has Shot 2's grade: %+v", b.Sides[1])
			}
		}
	}
	hows := map[string]string{}
	for _, j := range out.Joins {
		hows[j.Transition] = j.How
	}
	if hows["t1"] != "baked" || hows["t2"] != "baked" || hows["t3"] != "clip" {
		t.Fatalf("joins %v", hows)
	}
	if len(out.Doc.Tracks) != 2 {
		t.Fatalf("tracks %d", len(out.Doc.Tracks))
	}
	line := out.Doc.Tracks[1].Clips[0]
	if line.Link == nil || *line.Out != (348.0-100)/24 {
		t.Fatalf("line %+v", line)
	}
	linked := ""
	for _, c := range v1 {
		if c.ID == *line.Link {
			linked = c.ID
		}
	}
	if linked == "" {
		t.Fatalf("the line's link %s is not a V1 clip", *line.Link)
	}
	want := []string{"Shot 3 is out of date", "Shot 4 has no take", "B-roll stays here", "Line 1 is cut"}
	for _, w := range want {
		found := false
		for _, got := range out.Warnings {
			found = found || strings.HasPrefix(got, w)
		}
		if !found {
			t.Fatalf("no warning %q in %q", w, out.Warnings)
		}
	}
	takes := sendTakes()
	if _, probs := timeline.Normalize(out.Doc, takes, nil); probs != nil {
		t.Fatalf("the sequence breaks the Timeline's rules: %v", probs)
	}
	for _, b := range out.Bakes {
		job, err := timeline.BuildBake(out.Doc.Target, b.Kind, make([]timeline.BakeSide, len(b.Sides)), b.Frames)
		if err != nil || !strings.Contains(strings.Join(job.Args, " "), "{out}") {
			t.Fatalf("bake %s: %v", b.Kind, err)
		}
	}
}

// A dissolve goes as the Timeline's own when both takes have the media past the cut (the second clip starts half
// the dissolve into its take), else it is baked; with keep false it is always baked.
func TestBuildSendDissolve(t *testing.T) {
	d := sendBoard()
	d.Transitions = []*Transition{{ID: "t2", Between: &[2]string{"s1", "s2"}, Kind: "dissolve", Length: 12}}
	d.Blocks[2].Effects = nil
	takes := sendTakes()
	takes[tB] = timeline.Take{Kind: "video", DurationS: 4.5}
	takes[tA] = timeline.Take{Kind: "video", DurationS: 4.5}
	out := BuildSend(d, takes, nil, nil, true)
	if out.Joins[0].How != "kept" || len(out.Bakes) != 0 {
		t.Fatalf("want a kept dissolve: %+v %+v", out.Joins, out.Bakes)
	}
	c := out.Doc.Tracks[0].Clips[1]
	if c.TransitionIn == nil || *c.In != 0.25 {
		t.Fatalf("the second clip %+v", c)
	}
	if _, probs := timeline.Normalize(out.Doc, takes, nil); probs != nil {
		t.Fatalf("rules: %v", probs)
	}
	if out := BuildSend(d, takes, nil, nil, false); out.Joins[0].How != "baked" || len(out.Bakes) != 1 {
		t.Fatalf("keep false: %+v", out.Joins)
	}
	takes[tB] = timeline.Take{Kind: "video", DurationS: 4}
	if out := BuildSend(d, takes, nil, nil, true); out.Joins[0].How != "baked" {
		t.Fatalf("no handle: %+v", out.Joins)
	}
}

// A take far shorter than its block holds its nearest frame for the whole span of a bake: never a negative hold.
func TestBuildSendShortTake(t *testing.T) {
	d := sendBoard()
	d.Transitions = []*Transition{{ID: "t2", Between: &[2]string{"s1", "s2"}, Kind: "dissolve", Length: 12}}
	takes := sendTakes()
	takes[tA] = timeline.Take{Kind: "video", DurationS: 1}
	takes[tB] = timeline.Take{Kind: "video", DurationS: 1}
	out := BuildSend(d, takes, nil, nil, true)
	for _, b := range out.Bakes {
		for _, s := range b.Sides {
			if s.Pre < 0 || s.Post < 0 {
				t.Fatalf("%s: a negative hold %+v", b.Title, s)
			}
		}
	}
}

// API 1.89: a dialogue's turns go one after another, the first linked to its shot, a missing turn warned; a Loop's
// take repeats to fill its block, the last cut at its end.
func TestBuildSendTurnsAndLoop(t *testing.T) {
	d := sendBoard()
	d.Blocks[5].Request = map[string]any{"kind": "voice", "turns": []any{}} // v1, 300 frames from 100
	d.Lanes = append(d.Lanes, &Lane{ID: "fx", Name: "Sound", Role: "sound"})
	d.Blocks = append(d.Blocks, &Block{ID: "x1", Lane: "fx", At: 0, Length: 120, Kind: "sound", Name: "Rain",
		Request: map[string]any{"kind": "sound", "loop": true}, Takes: []string{tV}})
	takes := sendTakes()
	takes[tV] = timeline.Take{Kind: "audio", DurationS: 2} // 48 frames
	out := BuildSend(d, takes, nil, map[string][]string{"v1": {tV, "", tV}}, true)
	at := func(track int) (s []string) {
		for _, c := range out.Doc.Tracks[track].Clips {
			s = append(s, strings.Join([]string{ftoa(*c.At), ftoa(*c.Out)}, "+"))
		}
		return s
	}
	if got := strings.Join(at(1), " "); got != "4.17+2 6.17+2" { // 100/24 s, then right after it
		t.Fatal(got, out.Warnings)
	}
	if c := out.Doc.Tracks[1].Clips; c[0].Link == nil || c[1].Link != nil {
		t.Fatal("the first turn is linked to its shot")
	}
	if got := strings.Join(at(2), " "); got != "0+2 2+2 4+1" { // 120 frames: 48, 48, then 24
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(out.Warnings, "; "), "Line 1: line 2 has no take") || out.Goes[2].Blocks != 1 {
		t.Fatal(out.Warnings, out.Goes)
	}
	// API 1.90: Loop is off on a sound made from a shot's video
	d.Blocks[len(d.Blocks)-1].Request["from"] = "s1"
	out = BuildSend(d, takes, nil, nil, true)
	if got := strings.Join(at(2), " "); got != "0+2" {
		t.Fatal(got)
	}
}

func ftoa(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
}

// API 1.101: an audio block's level goes onto its clips' gain; the board rules keep it to audio kinds and -60..12 dB.
func TestBlockLevel(t *testing.T) {
	d := sendBoard()
	for _, b := range d.Blocks {
		b.Request = map[string]any{"kind": b.Kind}
	}
	r := Apply(d, Op{Name: "block", Raw: []byte(`{"op":"block","id":"v1","set":{"gain_db":-6}}`)})
	if r.Error != "" || Check(r.Doc) != "" {
		t.Fatalf("set: %q %q", r.Error, Check(r.Doc))
	}
	out := BuildSend(r.Doc, sendTakes(), nil, nil, true)
	if c := out.Doc.Tracks[1].Clips[0]; c.GainDB == nil || *c.GainDB != -6 {
		t.Fatalf("clip gain: %v", c.GainDB)
	}
	if av := AgentView(r.Doc); av.Blocks[5].GainDB != -6 {
		t.Fatalf("agent view: %v", av.Blocks[5])
	}
	for raw, want := range map[string]string{
		`{"op":"block","id":"s1","set":{"gain_db":-6}}`: "gain_db is for voice, music and sound blocks",
		`{"op":"block","id":"v1","set":{"gain_db":20}}`: "gain_db is -60 to 12",
	} {
		r := Apply(d, Op{Name: "block", Raw: []byte(raw)})
		if r.Error != "" || !strings.Contains(Check(r.Doc), want) {
			t.Errorf("%s: %q %q", raw, r.Error, Check(r.Doc))
		}
	}
}

// API 1.106: a music lane with a crossfade between its two cues, a lane fade out and block fades goes with its fades,
// the outgoing clip's crossfade (a blend: its take ran with the tail) and the board's ducking; the tail and the dip
// warning follow the takes' lengths.
func TestSendSoundJoins(t *testing.T) {
	str := func(s string) *string { return &s }
	d := sendBoard()
	d.Lanes = append(d.Lanes, &Lane{ID: "music", Name: "Music", Role: "music"})
	d.Blocks = append(d.Blocks,
		&Block{ID: "m1", Lane: "music", At: 0, Length: 96, Kind: "music", Name: "Soft rain theme", Takes: []string{tM}, FadeIn: new(0.5)},
		&Block{ID: "m2", Lane: "music", At: 96, Length: 96, Kind: "music", Name: "Storm", Takes: []string{tV}})
	d.Transitions = append(d.Transitions, &Transition{ID: "x1", Between: &[2]string{"m1", "m2"}, Kind: "crossfade", Length: 48},
		&Transition{ID: "x2", End: str("music"), Kind: "fade", Length: 24})
	d = d.filled()
	for _, b := range d.Blocks {
		b.Request = map[string]any{"kind": b.Kind}
	}
	if why := Check(d); why != "" {
		t.Fatal(why)
	}
	if Tail(d, d.block("m1")) != 48 || Tail(d, d.block("m2")) != 0 || Tail(d, d.block("v1")) != 0 {
		t.Fatal("tails")
	}
	takes := sendTakes()
	takes[tM] = timeline.Take{Kind: "audio", DurationS: 6, HasAudio: true} // 96 + 48 frames at 24 fps
	secs := func(id string) (float64, bool) { tk, ok := takes[id]; return tk.DurationS, ok }
	if m := JoinModes(d, secs); m["x1"] != "blend" || len(DipWarnings(d, secs)) != 0 {
		t.Fatalf("blend: %v %v", m, DipWarnings(d, secs))
	}
	out := BuildSend(d, takes, nil, nil, true)
	music := out.Doc.Tracks[len(out.Doc.Tracks)-1].Clips
	if len(music) != 2 || val(music[0].FadeIn) != 0.5 || val(music[0].Crossfade) != 2 || val(music[1].FadeOut) != 1 {
		t.Fatalf("music clips: %+v", music)
	}
	if k := out.Doc.Ducking; k == nil || !k.On || k.Amount() != -12 {
		t.Fatalf("ducking: %+v", k)
	}
	if !strings.Contains(fmt.Sprint(out.Joins), "{x1 crossfade crossfade}") || !strings.Contains(fmt.Sprint(out.Joins), "{x2 fade_out fades}") {
		t.Fatalf("joins: %v", out.Joins)
	}
	takes[tM] = timeline.Take{Kind: "audio", DurationS: 4, HasAudio: true} // made before the crossfade
	if w := DipWarnings(d, secs); len(w) != 1 || w[0] != "Soft rain theme was made before the crossfade: it dips instead." {
		t.Fatalf("dip: %v", w)
	}
}
