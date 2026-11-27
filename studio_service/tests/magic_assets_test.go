// Integration tests of Magic's assets and storyboard (API 1.83, contracts/README.md "Magic: assets and the
// storyboard"; plan 39 phase E): a board frame runs on the image model with its style, framing and who and where in
// the prompt (the board's @handles as names with their picture's row) and keeps its style on the run; a shot made
// from a frame takes the frame's words and goes out of date when the frame changes; Make views runs one image per
// view whose takes join the asset's sheet, named, as studio revisions; a picture of your own joins a block, pinned;
// another user's board is 404.
package tests

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestMagicAssetsAndFrames(t *testing.T) {
	e := setup(t)
	face := e.image(userA)
	block := func(id, lane string, kind, name string, request map[string]any) map[string]any {
		request["kind"] = kind
		return map[string]any{"id": id, "lane": lane, "at": 0, "length": 48, "kind": kind, "name": name,
			"request": request, "takes": []string{}, "effects": []any{}}
	}
	doc := map[string]any{
		"target": map[string]any{"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
		"lanes": []any{map[string]any{"id": "story", "name": "Storyboard", "role": "storyboard"},
			map[string]any{"id": "shots", "name": "Shots", "role": "shots"}},
		"blocks": []any{
			block("f1", "story", "frame", "Frame 1", map[string]any{"style": "rough", "prompt": "@mara waits",
				"framing": map[string]any{"size": "wide", "angle": "low", "move": "push_in"},
				"refs":    []any{map[string]any{"asset": "a1"}}}),
		},
		"links": []any{}, "transitions": []any{}, "notes": []string{},
		"assets": []any{map[string]any{"id": "a1", "kind": "character", "handle": "mara", "name": "Mara",
			"sheet": []string{face}, "note": "yellow raincoat"}},
	}
	b := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Story", "doc": doc}, 201)
	id := b["id"].(string)
	base := "/v1/studio/magic/boards/" + id

	// the frame runs on the image model: its style, framing and words, @mara as her name and picture's row
	r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"f1"}}, 202)
	runID := r["started"].([]any)[0].(map[string]any)["run_id"].(string)
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	inputs := run["inputs"].(map[string]any)
	if run["workflow_id"] != "echo" || inputs["prompt.text"] != "A rough storyboard drawing: simple shapes, grey shading. "+
		"Wide shot, from a low angle. Mara <image1> waits." || fmt.Sprint(inputs["echo.refs"]) != "[map[image:map[take_id:"+face+"]]]" {
		t.Fatal(run)
	}
	if runs := r["board"].(map[string]any)["runs"].([]any); runs[0].(map[string]any)["style"] != "rough" {
		t.Fatal(runs)
	}
	session := r["board"].(map[string]any)["session_id"].(string)
	e.relay()
	frameTake := e.takeOf(userA, session, runID)
	// its file, so a run can take it as a picture
	key := "u/" + userA + "/runs/" + runID + "/1/0.png"
	if err := e.store.Put(context.Background(), key, bytes.NewReader([]byte("0123456789")), 10, "image/png"); err != nil {
		t.Fatal(err)
	}
	e.emit(userA, session, runID, "status", 3, map[string]any{"status": "succeeded"})

	// Generate from board: a shot under the frame with no prompt takes the frame's words and move
	now := e.call(userA, "GET", base, nil, 200)
	ed := e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": now["revision"], "ops": []any{
		map[string]any{"op": "add", "mode": "overwrite", "block": map[string]any{"lane": "shots", "at": 0, "length": 48,
			"kind": "video", "name": "Shot 1", "from_frame": "f1", "request": map[string]any{"kind": "video", "model": "echo-image-to-video"},
			"takes": []string{}, "effects": []any{}}}}}, 200)
	board := ed["board"].(map[string]any)
	r = e.call(userA, "POST", base+"/run", map[string]any{"revision": board["revision"], "blocks": []string{"b1"}}, 202)
	shotRun := e.call(userA, "GET", "/v1/studio/runs/"+r["started"].([]any)[0].(map[string]any)["run_id"].(string), nil, 200)
	shotIn := shotRun["inputs"].(map[string]any)
	if shotIn["prompt.text"] != "Wide shot, from a low angle. Mara waits. The camera slowly pushes in." ||
		!strings.Contains(fmt.Sprint(shotIn), frameTake) {
		t.Fatal(shotIn)
	}
	e.relay()
	e.takeOf(userA, session, shotRun["id"].(string))
	e.emit(userA, session, shotRun["id"].(string), "status", 3, map[string]any{"status": "succeeded"})
	now = e.call(userA, "GET", base, nil, 200)
	if statusOf(now, "b1") != "done" || statusOf(now, "f1") != "done" {
		t.Fatal(now["status"])
	}
	// the frame's words change: the frame and the shot made from it are out of date
	ed = e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": now["revision"], "ops": []any{
		map[string]any{"op": "block", "id": "f1", "set": map[string]any{"request": map[string]any{"kind": "frame",
			"style": "rough", "prompt": "@mara runs"}}}}}, 200)
	if b := ed["board"].(map[string]any); statusOf(b, "f1") != "out_of_date" || statusOf(b, "b1") != "out_of_date" {
		t.Fatal(b["status"])
	}

	// Make views: one image run per view, each take on the sheet, named
	rev := ed["board"].(map[string]any)["revision"]
	views := base + "/assets/a1/views"
	e.call(userA, "POST", views, map[string]any{"revision": 1, "views": []string{"Front"}}, 409)
	e.call(userA, "POST", base+"/assets/nobody/views", map[string]any{"revision": rev, "views": []string{"Front"}}, 422)
	e.call(userA, "POST", views, map[string]any{"revision": rev, "views": []string{}}, 422)
	e.call(userB, "POST", views, map[string]any{"revision": rev, "views": []string{"Front"}}, 404)
	v := e.call(userA, "POST", views, map[string]any{"revision": rev, "views": []string{"Side", "Back"}}, 202)
	started := v["started"].([]any)
	if len(started) != 2 || started[0].(map[string]any)["view"] != "Side" || len(v["board"].(map[string]any)["asset_runs"].([]any)) != 2 {
		t.Fatal(v)
	}
	sideRun := e.call(userA, "GET", "/v1/studio/runs/"+started[0].(map[string]any)["run_id"].(string), nil, 200)
	if p := sideRun["inputs"].(map[string]any)["prompt.text"]; p != "A character sheet view of Mara (yellow raincoat): side. "+
		"Full colour, plain light background, the same person in every view" {
		t.Fatal(p)
	}
	e.relay()
	side := e.takeOf(userA, session, sideRun["id"].(string))
	now = e.call(userA, "GET", base, nil, 200)
	asset := now["doc"].(map[string]any)["assets"].([]any)[0].(map[string]any)
	if fmt.Sprint(asset["sheet"]) != "["+face+" "+side+"]" || asset["views"].(map[string]any)[side] != "Side" ||
		now["takes"].(map[string]any)[side] == nil {
		t.Fatal(asset)
	}
	// block runs leave asset runs out
	for _, x := range now["runs"].([]any) {
		if x.(map[string]any)["block"] == "" {
			t.Fatal(now["runs"])
		}
	}

	// a picture of your own joins a block, pinned; deleting the asset takes its picks and mentions
	own := e.image(userA)
	ed = e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": now["revision"], "ops": []any{
		map[string]any{"op": "take_add", "id": "f1", "take": own},
		map[string]any{"op": "asset_delete", "id": "a1"}}}, 200)
	f1 := blockOf(ed["board"].(map[string]any), "f1")
	if f1["pinned"] != own || f1["request"].(map[string]any)["prompt"] != "Mara runs" {
		t.Fatal(f1)
	}
	e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": ed["board"].(map[string]any)["revision"], "ops": []any{
		map[string]any{"op": "take_add", "id": "f1", "take": e.image(userB)}}}, 422)
}
