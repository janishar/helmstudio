// Magic morphs (API 1.82, plan 39 phase G, contracts/README.md "Magic: Send to Timeline"): a morph transition runs as
// a block does, on a model that takes a first and a last frame (the first shot's last frame, the second's first),
// its take joins the transition as a studio revision and its status is the board's under the transition's id.
package tests

import (
	"fmt"
	"testing"
)

func TestMagicMorph(t *testing.T) {
	e := setup(t)
	e.mediaHost(0)
	a := e.clipTake(userA, 2, true, nil)
	b := e.clipTake(userA, 2, true, map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{}}) // its first frame at once
	doc := magicRunBoard(a)
	blocks := doc["blocks"].([]any)
	s2 := blocks[1].(map[string]any)
	s2["takes"] = []string{b}
	doc["blocks"] = blocks[:2]
	doc["transitions"] = []any{map[string]any{"id": "t1", "between": []any{"s1", "s2"}, "kind": "morph", "length": 24,
		"request": map[string]any{"kind": "video", "prompt": "the fox melts into rain"}}}
	board := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Melt", "doc": doc}, 201)
	base := "/v1/studio/magic/boards/" + board["id"].(string)
	if statusOf(board, "t1") != "draft" {
		t.Fatal(board["status"])
	}
	if plan := e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"t1"}}, 200); plan["blocks"].([]any)[0].(map[string]any)["seconds"] == nil {
		t.Fatal(plan)
	}
	e.putObject("u/"+userA+"/runs/"+b+"/1/0.poster.png", "0123456789") // the size its poster says
	r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"t1"}}, 202)
	started := r["started"].([]any)
	if len(started) != 1 || started[0].(map[string]any)["block"] != "t1" || statusOf(r["board"].(map[string]any), "t1") != "running" {
		t.Fatal(r)
	}
	runID := started[0].(map[string]any)["run_id"].(string)
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	rows, _ := run["inputs"].(map[string]any)["keyframes.images"].([]any)
	if run["workflow_id"] != "echo-image-to-video" || len(rows) != 2 {
		t.Fatal(run["workflow_id"], run["inputs"])
	}
	// a second run while it runs is the 409
	if r := e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"t1"}}, 409); r["detail"] != "Morph is already running" {
		t.Fatal(r)
	}
	e.relay()
	session := r["board"].(map[string]any)["session_id"]
	take := e.takeOf(userA, fmt.Sprint(session), runID)
	e.emit(userA, fmt.Sprint(session), runID, "status", 3, map[string]any{"status": "succeeded"})
	now := e.call(userA, "GET", base, nil, 200)
	tr := now["doc"].(map[string]any)["transitions"].([]any)[0].(map[string]any)
	if fmt.Sprint(tr["takes"]) != "["+take+"]" || statusOf(now, "t1") != "done" || now["takes"].(map[string]any)[take] == nil {
		t.Fatal(now)
	}
	// pinned and unpinned by the transition op
	edited := e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": now["revision"], "ops": []any{
		map[string]any{"op": "transition", "between": []any{"s1", "s2"}, "kind": "morph", "pinned": take}}}, 200)
	if tr := edited["board"].(map[string]any)["doc"].(map[string]any)["transitions"].([]any)[0].(map[string]any); tr["pinned"] != take {
		t.Fatal(tr)
	}
	edited = e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": edited["revision"], "ops": []any{
		map[string]any{"op": "transition", "between": []any{"s1", "s2"}, "kind": "morph", "pinned": nil}}}, 200)
	if tr := edited["board"].(map[string]any)["doc"].(map[string]any)["transitions"].([]any)[0].(map[string]any); tr["pinned"] != nil {
		t.Fatal(tr)
	}
	e.call(userB, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"t1"}}, 404)
}
