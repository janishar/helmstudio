// Integration tests of Magic's standard request and runs (API 1.81, contracts/README.md "Magic: the standard request",
// "Magic: runs"; plan 39 phase D, §45.12): the kinds, a block's request run in the board's own session (hidden from
// Create's list), runs per block with their seeds, a first frame from another block's video, takes joining their
// block as studio revisions that undo keeps, the statuses, the estimate and the ask rule, the settings, and another
// user's board 404.
package tests

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func magicRunBoard(shotTake string) map[string]any {
	block := func(id, lane string, at, length int, kind, name string, request map[string]any) map[string]any {
		request["kind"] = kind
		b := map[string]any{"id": id, "lane": lane, "at": at, "length": length, "kind": kind, "name": name,
			"request": request, "takes": []string{}, "effects": []any{}}
		return b
	}
	s1 := block("s1", "shots", 0, 48, "video", "Shot 1", map[string]any{"prompt": "a fox", "model": "echo-video"})
	s1["takes"] = []string{shotTake}
	return map[string]any{
		"target": map[string]any{"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
		"lanes": []any{map[string]any{"id": "shots", "name": "Shots", "role": "shots"},
			map[string]any{"id": "lines", "name": "Voice", "role": "voice"},
			map[string]any{"id": "music", "name": "Music", "role": "music"}},
		"blocks": []any{s1,
			block("s2", "shots", 48, 48, "video", "Shot 2 · rain", map[string]any{"prompt": "rain", "model": "echo-image-to-video",
				"first": map[string]any{"block": "s1", "part": "last"}}),
			block("v1", "lines", 0, 48, "voice", "Line 1", map[string]any{"line": "Hello.", "seed": 7, "count": 2,
				"voice_id": "echo/ada"}),
			// API 1.85: music runs; this cue names a model the fake host has not got, so it is skipped with why
			block("m1", "music", 0, 96, "music", "Cue 1", map[string]any{"model": "ace-step-music"})},
		"links": []any{}, "transitions": []any{}, "assets": []any{}, "notes": []string{},
	}
}

func statusOf(board map[string]any, id string) string {
	return board["status"].(map[string]any)[id].(map[string]any)["status"].(string)
}

func TestMagicRuns(t *testing.T) {
	e := setup(t)
	e.voices()
	kinds := e.call(userA, "GET", "/v1/studio/magic/kinds", nil, 200)["kinds"].([]any)
	var got []string
	for _, k := range kinds {
		k := k.(map[string]any)
		var models []string
		for _, m := range k["models"].([]any) {
			models = append(models, m.(map[string]any)["workflow_id"].(string))
		}
		got = append(got, fmt.Sprintf("%v %v %v", k["kind"], models, k["off"]))
	}
	if fmt.Sprint(got) != "[video [echo-image-to-video echo-video] <nil> image [echo] <nil> voice [echo-voice-over] <nil> "+
		"music [echo-music] <nil> sound [echo-sound] <nil> frame [echo] <nil>]" {
		t.Fatal(got)
	}

	// settings: 5 by default, per user
	if s := e.call(userA, "GET", "/v1/studio/magic/settings", nil, 200); s["ask_above_gpu_minutes"] != float64(5) {
		t.Fatal(s)
	}
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 601}, 422)
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 0.001}, 200)
	if s := e.call(userB, "GET", "/v1/studio/magic/settings", nil, 200); s["ask_above_gpu_minutes"] != float64(5) {
		t.Fatal(s)
	}

	shot := e.clipTake(userA, 2, true, nil)
	b := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Rain", "doc": magicRunBoard(shot)}, 201)
	base := "/v1/studio/magic/boards/" + b["id"].(string)
	if b["session_id"] != nil || statusOf(b, "s1") != "done" || statusOf(b, "s2") != "draft" || b["takes"].(map[string]any)[shot] == nil {
		t.Fatal(b)
	}

	// the plan: the pack's rate, music not planned, the ask rule above 0.001 minutes
	plan := e.call(userA, "POST", base+"/plan", map[string]any{"stale": true}, 200)
	blocks := plan["blocks"].([]any)
	if fmt.Sprint(plan["order"]) != "[s2 v1 m1]" || len(blocks) != 3 || plan["ask"] != true || plan["seconds"] == nil {
		t.Fatal(plan)
	}
	if s2 := blocks[0].(map[string]any); s2["source"] != "pack" || s2["seconds"] != float64(1) || s2["host"] != "fake" {
		t.Fatal(s2) // Echo keyframes: 0.5 s a second at 720p, 2 s long
	}
	if m1 := blocks[2].(map[string]any); m1["reason"] != "its model ace-step-music is on no host" || m1["seconds"] != nil {
		t.Fatal(m1)
	}
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 5}, 200)
	if plan := e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"v1"}}, 200); plan["ask"] != false ||
		plan["ask_text"] != "1 line, about 1 s" {
		t.Fatal(plan) // two runs of 2 s at 0.2 s a second: 0.8 s
	}
	e.call(userA, "POST", base+"/plan", map[string]any{"stale": true, "lane": "shots"}, 422)

	// named blocks must all run; a stale revision is 409
	run := func(user string, body map[string]any, want int) map[string]any {
		return e.call(user, "POST", base+"/run", body, want)
	}
	if r := run(userA, map[string]any{"revision": 1, "blocks": []string{"m1"}}, 422); r["detail"] != "Cue 1: its model ace-step-music is on no host" {
		t.Fatal(r)
	}
	run(userA, map[string]any{"revision": 2, "blocks": []string{"s2"}}, 409)
	// another user's board is 404
	run(userB, map[string]any{"revision": 1, "blocks": []string{"s2"}}, 404)
	e.call(userB, "POST", base+"/plan", map[string]any{"stale": true}, 404)

	// the stale pick: Shot 2 from Shot 1's last frame, Line 1 twice (seeds 7 and 8), the cue skipped
	r := run(userA, map[string]any{"revision": 1, "stale": true}, 202)
	started, skipped := r["started"].([]any), r["skipped"].([]any)
	if len(started) != 3 || fmt.Sprint(skipped) != "[map[block:m1 reason:its model ace-step-music is on no host]]" {
		t.Fatal(r)
	}
	board := r["board"].(map[string]any)
	session := board["session_id"].(string)
	if statusOf(board, "s2") != "running" || statusOf(board, "v1") != "running" || len(board["runs"].([]any)) != 3 {
		t.Fatal(board)
	}
	for _, s := range e.call(userA, "GET", "/v1/studio/sessions", nil, 200)["sessions"].([]any) {
		if s.(map[string]any)["id"] == session {
			t.Fatal("the board's session is listed")
		}
	}
	runOf := func(block string) []map[string]any {
		var out []map[string]any
		for _, s := range started {
			if s := s.(map[string]any); s["block"] == block {
				out = append(out, e.call(userA, "GET", "/v1/studio/runs/"+s["run_id"].(string), nil, 200))
			}
		}
		return out
	}
	lines := runOf("v1")
	if len(lines) != 2 || lines[0]["inputs"].(map[string]any)["speak.seed"] != float64(7) ||
		lines[1]["inputs"].(map[string]any)["speak.seed"] != float64(8) || lines[0]["session_id"] != session ||
		fmt.Sprint(lines[0]["inputs"].(map[string]any)["speak.voice"]) != "map[voice_id:echo/ada]" {
		t.Fatal(lines)
	}
	s2 := runOf("s2")[0]
	rows := s2["inputs"].(map[string]any)["keyframes.images"].([]any)
	frameTake := rows[0].(map[string]any)["image"].(map[string]any)["take_id"].(string)
	if len(rows) != 1 || s2["inputs"].(map[string]any)["keyframes.bypass"] != false ||
		e.count(`SELECT count(*) FROM takes WHERE id = $1 AND frame_of->>'take_id' = $2 AND frame_of->>'at' = 'last'`, frameTake, shot) != 1 {
		t.Fatal(s2)
	}
	if r := run(userA, map[string]any{"revision": 1, "blocks": []string{"s2"}}, 409); r["detail"] != "Shot 2 is already running" {
		t.Fatal(r)
	}

	// the take joins its block as a studio revision; undo goes back past it but keeps the take
	e.relay()
	take := e.takeOf(userA, session, s2["id"].(string))
	e.emit(userA, session, s2["id"].(string), "status", 3, map[string]any{"status": "succeeded"})
	now := e.call(userA, "GET", base, nil, 200)
	s2Block := blockOf(now, "s2")
	if now["revision"] != float64(2) || fmt.Sprint(s2Block["takes"]) != "["+take+"]" || statusOf(now, "s2") != "done" {
		t.Fatal(now)
	}
	if mt := now["takes"].(map[string]any)[take].(map[string]any); mt["run_id"] != s2["id"] {
		t.Fatal(mt)
	}
	edited := e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": 2, "ops": []any{
		map[string]any{"op": "block", "id": "s2", "set": map[string]any{"request": map[string]any{"kind": "video",
			"prompt": "heavier rain", "model": "echo-image-to-video", "first": map[string]any{"block": "s1", "part": "last"}}}}}}, 200)
	if statusOf(edited["board"].(map[string]any), "s2") != "out_of_date" {
		t.Fatal(edited)
	}
	undone := e.call(userA, "POST", base+"/undo", map[string]any{"revision": 3}, 200)
	if fmt.Sprint(blockOf(undone, "s2")["takes"]) != "["+take+"]" || statusOf(undone, "s2") != "done" {
		t.Fatal(undone)
	}
	// the estimate from your own runs now: the take (as a 2 s 720p video) took 4 s on host fake: 2 s a unit
	e.exec(`UPDATE takes SET video = '{"duration_s": 2}', width = 1280, height = 720,
		made_with = made_with || '{"render_s": 4}' WHERE id = $1`, take)
	plan = e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"s2"}}, 200)
	if b := plan["blocks"].([]any)[0].(map[string]any); b["source"] != "yours" || b["seconds"] != float64(4) {
		t.Fatal(plan)
	}
	// the agent view's status is the same; another user still sees nothing
	agent := e.call(userA, "GET", base+"?view=agent", nil, 200)
	for _, b := range agent["blocks"].([]any) {
		if b := b.(map[string]any); b["id"] == "s2" && b["status"] != "done" || b["id"] == "v1" && b["status"] != "running" {
			t.Fatal(b)
		}
	}
	e.call(userB, "GET", base, nil, 404)
	if n := e.count(`SELECT count(*) FROM magic_runs WHERE board_id = $1`, b["id"]); n != 3 {
		t.Fatal(n)
	}
	raw, _ := json.Marshal(now["runs"])
	if !slices.Contains([]int{3}, len(now["runs"].([]any))) {
		t.Fatal(string(raw))
	}
}

func blockOf(board map[string]any, id string) map[string]any {
	for _, b := range board["doc"].(map[string]any)["blocks"].([]any) {
		if b := b.(map[string]any); b["id"] == id {
			return b
		}
	}
	return nil
}

// A lane's block refused on the way is skipped with why while the rest run (Shot 2 picks Shot 1, which has no take
// yet); named, it is the 422 with its name. A voice block with no voice cannot run (the plan says why).
func TestMagicRunsSkipRefused(t *testing.T) {
	e := setup(t)
	doc := magicRunBoard("")
	blocks := doc["blocks"].([]any)
	s1, s2, v1 := blocks[0].(map[string]any), blocks[1].(map[string]any), blocks[2].(map[string]any)
	s1["takes"] = []string{}
	delete(v1["request"].(map[string]any), "voice_id")
	doc["blocks"] = []any{s1, s2, v1}
	b := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Skip", "doc": doc}, 201)
	base := "/v1/studio/magic/boards/" + b["id"].(string)
	plan := e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"v1"}}, 200)
	if v := plan["blocks"].([]any)[0].(map[string]any); v["reason"] != "Echo voice needs a voice" {
		t.Fatal(plan)
	}
	if r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"s2"}}, 422); r["detail"] != "Shot 2: Shot 1 has no take yet" {
		t.Fatal(r)
	}
	r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "stale": true}, 202)
	if fmt.Sprint(r["started"].([]any)[0].(map[string]any)["block"]) != "s1" || len(r["started"].([]any)) != 1 ||
		fmt.Sprint(r["skipped"]) != "[map[block:s2 reason:Shot 1 has no take yet] map[block:v1 reason:Echo voice needs a voice]]" {
		t.Fatal(r)
	}
	if n := e.count(`SELECT count(*) FROM magic_runs WHERE board_id = $1`, b["id"]); n != 1 {
		t.Fatal(n)
	}
}

// API 1.85 (plan 39 phase H): a music block runs echo music with its lyrics, BPM and seconds; a sound made from a
// shot's video runs the model's video workflow on the shot's take and keeps only the audio take; the board's take
// carries its sound class.
func TestMagicMusicAndSound(t *testing.T) {
	e := setup(t)
	shot := e.clipTake(userA, 2, true, nil)
	e.exec(`UPDATE takes SET bytes = 5 WHERE id = $1`, shot) // its object's size, so a run may read it
	doc := magicRunBoard(shot)
	s1 := doc["blocks"].([]any)[0]
	doc["lanes"] = append(doc["lanes"].([]any), map[string]any{"id": "sound", "name": "Sound", "role": "sound"})
	music := map[string]any{"id": "m1", "lane": "music", "at": 0, "length": 96, "kind": "music", "name": "Cue 1",
		"request": map[string]any{"kind": "music", "prompt": "soft rain", "lyrics": "[verse]\nla", "bpm": 72, "key": "D minor"},
		"takes":   []string{}, "effects": []any{}}
	sound := map[string]any{"id": "x1", "lane": "sound", "at": 0, "length": 48, "kind": "sound", "name": "Bus",
		"request": map[string]any{"kind": "sound", "from": "s1", "prompt": "a bus pulls away"}, "takes": []string{}, "effects": []any{}}
	doc["blocks"] = []any{s1, music, sound}
	b := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Sound", "doc": doc}, 201)
	base := "/v1/studio/magic/boards/" + b["id"].(string)
	plan := e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"m1", "x1"}}, 200)
	if plan["ask_text"] != "2 blocks, about 2 s" { // 4 s at 0.3 and 2 s at 0.2
		t.Fatal(plan)
	}
	// the sound first: the fake host runs one at a time
	runs := map[string]map[string]any{}
	var session string
	for _, id := range []string{"x1", "m1"} {
		r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{id}}, 202)
		session = r["board"].(map[string]any)["session_id"].(string)
		s := r["started"].([]any)[0].(map[string]any)
		runs[id] = e.call(userA, "GET", "/v1/studio/runs/"+s["run_id"].(string), nil, 200)
		if id == "x1" {
			e.relay()
		}
	}
	if m := runs["m1"]; m["workflow_id"] != "echo-music" || flatInputs(m) != "music.bpm=72 music.instrumental=false "+
		"music.lyrics=[verse]\nla music.seconds=4 music.seed=-1 prompt.text=soft rain" {
		t.Fatal(flatInputs(m))
	}
	x := runs["x1"]
	if x["workflow_id"] != "echo-sound-video" || flatInputs(x) != "foley.seed=-1 foley.source=map[take_id:"+shot+"] prompt.text=a bus pulls away" {
		t.Fatal(x["workflow_id"], flatInputs(x))
	}
	// the sound run makes the audio and the video with it: the block keeps the audio
	run := x["id"].(string)
	prefix := "u/" + userA + "/runs/" + run + "/1/"
	e.emit(userA, session, run, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, session, run, "output", 2, map[string]any{"index": 0, "key": prefix + "0.wav", "kind": "audio",
		"bytes": 100, "audio": map[string]any{"duration_s": 2.0, "sample_rate": 44100, "channels": 2}})
	e.emit(userA, session, run, "output", 3, map[string]any{"index": 1, "key": prefix + "1.mp4", "kind": "video",
		"width": 256, "height": 128, "bytes": 1000, "video": map[string]any{"duration_s": 2.0, "fps": 24, "frames": 48,
			"has_audio": true, "poster": map[string]any{"key": prefix + "1.poster.png", "width": 256, "height": 128, "bytes": 10}}})
	e.emit(userA, session, run, "status", 4, map[string]any{"status": "succeeded", "host": "fake"})
	now := e.call(userA, "GET", base, nil, 200)
	takes := blockOf(now, "x1")["takes"].([]any)
	if len(takes) != 1 || statusOf(now, "x1") != "done" {
		t.Fatal(blockOf(now, "x1"), now["status"])
	}
	// the take's class is the projector's (plan 38); the board passes it on
	e.exec(`UPDATE takes SET sound = 'sound' WHERE id = $1`, takes[0])
	now = e.call(userA, "GET", base, nil, 200)
	mt := now["takes"].(map[string]any)[takes[0].(string)].(map[string]any)
	if mt["kind"] != "audio" || mt["sound"] != "sound" {
		t.Fatal(mt)
	}
	for _, h := range now["runs"].([]any) {
		if h := h.(map[string]any); h["block"] == "x1" && fmt.Sprint(h["takes"]) != fmt.Sprint(takes) {
			t.Fatal(h)
		}
	}
	// a new take of the shot puts the sound out of date
	other := e.clipTake(userA, 2, true, nil)
	edited := e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": now["revision"], "ops": []any{
		map[string]any{"op": "take_add", "id": "s1", "take": other}}}, 200)
	if statusOf(edited["board"].(map[string]any), "x1") != "out_of_date" {
		t.Fatal(edited["board"].(map[string]any)["status"])
	}
}

// flatInputs is a run's inputs as sorted k=v pairs.
func flatInputs(run map[string]any) string {
	in := run["inputs"].(map[string]any)
	var parts []string
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, in[k]))
	}
	return strings.Join(parts, " ")
}
