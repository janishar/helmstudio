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
			block("m1", "music", 0, 96, "music", "Cue 1", map[string]any{})},
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
		"music [] Comes with Create's Music and Sound sound [] Comes with Create's Music and Sound frame [] Board frames come with the storyboard]" {
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
	if m1 := blocks[2].(map[string]any); m1["reason"] != "Music comes with Create's Music and Sound" || m1["seconds"] != nil {
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
	if r := run(userA, map[string]any{"revision": 1, "blocks": []string{"m1"}}, 422); r["detail"] != "Cue 1: Music comes with Create's Music and Sound" {
		t.Fatal(r)
	}
	run(userA, map[string]any{"revision": 2, "blocks": []string{"s2"}}, 409)
	// another user's board is 404
	run(userB, map[string]any{"revision": 1, "blocks": []string{"s2"}}, 404)
	e.call(userB, "POST", base+"/plan", map[string]any{"stale": true}, 404)

	// the stale pick: Shot 2 from Shot 1's last frame, Line 1 twice (seeds 7 and 8), the cue skipped
	r := run(userA, map[string]any{"revision": 1, "stale": true}, 202)
	started, skipped := r["started"].([]any), r["skipped"].([]any)
	if len(started) != 3 || fmt.Sprint(skipped) != "[map[block:m1 reason:Music comes with Create's Music and Sound]]" {
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
