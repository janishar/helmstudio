// Integration tests of Magic boards (API 1.80, contracts/README.md "Magic"; plan 39 phase C, §45.12): create, list,
// rename and delete, the 100-board cap, ops applied all or nothing, a stale base revision 409, dry runs storing
// nothing, studio-made ids, takes checked as the caller's, undo and redo, restore, snapshots (a snapshot's revision
// never pruned, restored as a new revision), the agent view and another user's board 404 everywhere.
package tests

import (
	"fmt"
	"testing"
)

func magicBoard() map[string]any {
	block := func(id, lane string, at, length int, kind, name string) map[string]any {
		return map[string]any{"id": id, "lane": lane, "at": at, "length": length, "kind": kind, "name": name,
			"request": map[string]any{"kind": kind}, "takes": []string{}, "effects": []any{}}
	}
	return map[string]any{
		"target": map[string]any{"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
		"lanes": []any{map[string]any{"id": "shots", "name": "Shots", "role": "shots"},
			map[string]any{"id": "lines", "name": "Lines", "role": "voice"}},
		"blocks": []any{block("s2", "shots", 96, 96, "video", "Shot 2"), block("s1", "shots", 0, 96, "video", "Shot 1"),
			block("v1", "lines", 10, 120, "voice", "Line 1")},
		"links":       []any{map[string]any{"block": "v1", "to": []string{"s1"}}},
		"transitions": []any{map[string]any{"id": "t1", "between": []string{"s1", "s2"}, "kind": "dissolve", "length": 12}},
		"assets":      []any{}, "notes": []string{"grey"},
	}
}

func blockIDs(board map[string]any) []string {
	var out []string
	for _, b := range board["doc"].(map[string]any)["blocks"].([]any) {
		out = append(out, b.(map[string]any)["id"].(string))
	}
	return out
}

func TestMagicBoards(t *testing.T) {
	e := setup(t)
	const boards = "/v1/studio/magic/boards"

	// an empty body is an empty board at the default target; a list answers it
	empty := e.call(userA, "POST", boards, nil, 201)
	if empty["name"] != "Untitled board" || empty["revision"] != float64(1) || empty["can_undo"] != false ||
		fmt.Sprint(empty["doc"].(map[string]any)["target"]) != "map[fps:24 height:720 sample_rate:48000 width:1280]" {
		t.Fatal(empty)
	}
	// from a document: its blocks in lane then time order
	b := e.call(userA, "POST", boards, map[string]any{"name": "The Last Tide", "doc": magicBoard()}, 201)
	base := boards + "/" + b["id"].(string)
	if fmt.Sprint(blockIDs(b)) != "[s1 s2 v1]" {
		t.Fatal(blockIDs(b))
	}
	list := e.call(userA, "GET", boards, nil, 200)["boards"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["blocks"] != float64(3) || list[0].(map[string]any)["frames"] != float64(192) {
		t.Fatal(list)
	}
	for _, bad := range []map[string]any{{"name": ""}, {"doc": magicBoard(), "target": magicBoard()["target"]},
		{"target": map[string]any{"width": 1, "height": 720, "fps": 24, "sample_rate": 48000}}} {
		e.call(userA, "POST", boards, bad, 422)
	}
	overlap := magicBoard()
	overlap["blocks"].([]any)[0].(map[string]any)["at"] = 50
	if got := e.call(userA, "POST", boards, map[string]any{"doc": overlap}, 422); got["detail"] != "doc: blocks: s2 overlaps the block before it on its lane" {
		t.Fatal(got)
	}
	foreignTake := e.clipTake(userB, 2, true, nil)
	withTake := magicBoard()
	withTake["blocks"].([]any)[0].(map[string]any)["takes"] = []string{foreignTake}
	if got := e.call(userA, "POST", boards, map[string]any{"doc": withTake}, 422); got["detail"] != "take "+foreignTake+" not found" {
		t.Fatal(got)
	}

	edits := func(revision int, dry bool, ops ...map[string]any) map[string]any {
		return map[string]any{"base_revision": revision, "dry_run": dry, "ops": ops}
	}
	add := map[string]any{"op": "add", "mode": "insert", "block": map[string]any{"lane": "shots", "at": 96, "length": 48,
		"kind": "video", "name": "Shot 3", "request": map[string]any{"kind": "video"}, "takes": []string{}, "effects": []any{}}}
	revisions := func() int { return e.count(`SELECT count(*) FROM magic_board_revisions WHERE board_id = $1`, b["id"]) }

	// another user's board is 404 on every route; so is a malformed id
	for _, r := range []struct {
		method, path string
		body         any
	}{{"GET", "", nil}, {"GET", "?view=agent", nil}, {"PATCH", "", map[string]any{"name": "x"}}, {"DELETE", "", nil},
		{"POST", "/edits", edits(1, false, add)}, {"GET", "/revisions", nil}, {"POST", "/undo", map[string]any{"revision": 1}},
		{"POST", "/redo", map[string]any{"revision": 1}}, {"POST", "/restore", map[string]any{"revision": 1, "to": 1}},
		{"GET", "/snapshots", nil}, {"POST", "/snapshots", map[string]any{}}} {
		e.call(userB, r.method, base+r.path, r.body, 404)
	}
	e.call(userA, "GET", boards+"/not-a-uuid", nil, 404)
	if len(e.call(userB, "GET", boards, nil, 200)["boards"].([]any)) != 0 {
		t.Fatal("userB sees userA's boards")
	}

	// a dry run answers the result, its studio-made id and the diff, and stores nothing; a stale one is the 409
	try := e.call(userA, "POST", base+"/edits", edits(1, true, add), 200)
	diff := try["diff"].(map[string]any)
	if try["revision"] != float64(1) || try["dry_run"] != true || fmt.Sprint(diff["blocks"]) != "map[added:[b1] changed:[s2] removed:[]]" ||
		fmt.Sprint(diff["transitions"]) != "map[added:[] changed:[] removed:[t1]]" || fmt.Sprint(blockIDs(try["board"].(map[string]any))) != "[s1 b1 s2 v1]" {
		t.Fatal(try)
	}
	if revisions() != 1 || e.call(userA, "GET", base, nil, 200)["revision"] != float64(1) {
		t.Fatal("a dry run stored")
	}
	if got := e.call(userA, "POST", base+"/edits", edits(2, true, add), 409); got["detail"] != "board changed since you opened it (now revision 1)" {
		t.Fatal(got)
	}

	// all or nothing: the failing op's index and the board's words; a body that does not fit is {detail}
	bad := e.call(userA, "POST", base+"/edits", edits(1, false, add, map[string]any{"op": "trim", "id": "zz", "length": 4}), 422)
	if bad["detail"] != "That block is gone." || bad["op_index"] != float64(1) || revisions() != 1 {
		t.Fatal(bad)
	}
	bad = e.call(userA, "POST", base+"/edits", edits(1, false, map[string]any{"op": "move", "ids": []string{"s1"}, "dt": 3, "mode": "insert", "at": 3}), 422)
	if bad["detail"] != "ops[0]: unknown field at" {
		t.Fatal(bad)
	}
	many := make([]map[string]any, 201)
	for i := range many {
		many[i] = map[string]any{"op": "unlink", "block": "v1"}
	}
	if bad = e.call(userA, "POST", base+"/edits", edits(1, false, many...), 422); bad["detail"] != "too_many_ops: at most 200 ops per call" {
		t.Fatal(bad)
	}
	pin := map[string]any{"op": "block", "id": "s1", "set": map[string]any{"pinned": foreignTake}}
	if bad = e.call(userA, "POST", base+"/edits", edits(1, false, pin), 422); bad["detail"] != "blocks[0]: pinned is one of its takes" {
		t.Fatal(bad)
	}

	// applied: one revision, via the client, its ops' names; the warning of the line running past Shot 1
	done := e.callVia(userA, "mcp", "POST", base+"/edits", edits(1, false, add, map[string]any{"op": "effect", "ids": []string{"s1"}, "effect": map[string]any{"name": "Grade"}}), 200)
	board := done["board"].(map[string]any)
	if done["revision"] != float64(2) || board["revision"] != float64(2) || board["can_undo"] != true ||
		fmt.Sprint(done["warnings"]) != "[Line 1 runs 1.4 s past Shot 1]" {
		t.Fatal(done)
	}
	hist := e.call(userA, "GET", base+"/revisions", nil, 200)["revisions"].([]any)
	if r := hist[0].(map[string]any); len(hist) != 2 || r["ops_summary"] != "add, effect" || r["via"] != "mcp" || r["blocks"] != float64(4) {
		t.Fatal(hist)
	}

	// undo and redo write forward; redo after an undo, then an edit clears redo
	undone := e.call(userA, "POST", base+"/undo", map[string]any{"revision": 2}, 200)
	if undone["revision"] != float64(3) || len(blockIDs(undone)) != 3 || undone["can_redo"] != true {
		t.Fatal(undone)
	}
	e.call(userA, "POST", base+"/undo", map[string]any{"revision": 2}, 409)
	redone := e.call(userA, "POST", base+"/redo", map[string]any{"revision": 3}, 200)
	if redone["revision"] != float64(4) || len(blockIDs(redone)) != 4 || redone["can_redo"] != false {
		t.Fatal(redone)
	}
	if got := e.call(userA, "POST", base+"/redo", map[string]any{"revision": 4}, 409); got["detail"] != "nothing to redo" {
		t.Fatal(got)
	}

	// snapshots: the current revision by default, named in turn; renamed; restored as a new revision
	snap := e.call(userA, "POST", base+"/snapshots", nil, 201)
	if snap["name"] != "Snapshot 1" || snap["revision"] != float64(4) || snap["blocks"] != float64(4) {
		t.Fatal(snap)
	}
	old := e.call(userA, "POST", base+"/snapshots", map[string]any{"name": "First", "revision": 1}, 201)
	e.call(userA, "POST", base+"/snapshots", map[string]any{"revision": 99}, 422)
	e.call(userA, "PATCH", base+"/snapshots/"+old["id"].(string), map[string]any{"name": "The first"}, 200)
	e.call(userB, "PATCH", base+"/snapshots/"+old["id"].(string), map[string]any{"name": "x"}, 404)
	snaps := e.call(userA, "GET", base+"/snapshots", nil, 200)["snapshots"].([]any)
	if len(snaps) != 2 || snaps[0].(map[string]any)["name"] != "The first" {
		t.Fatal(snaps)
	}
	// 100 more edits: revision 1 stays, kept by its snapshot; 2 and 3 are pruned
	rev := 4
	for i := 0; i < 100; i++ {
		e.call(userA, "POST", base+"/edits", edits(rev, false, map[string]any{"op": "lane", "id": "lines", "set": map[string]any{"hidden": i%2 == 0}}), 200)
		rev++
	}
	if e.count(`SELECT count(*) FROM magic_board_revisions WHERE board_id = $1 AND revision IN (1, 2, 3)`, b["id"]) != 1 {
		t.Fatal("pruning")
	}
	back := e.call(userA, "POST", base+"/restore", map[string]any{"revision": rev, "to": 1}, 200)
	if back["revision"] != float64(rev+1) || len(blockIDs(back)) != 3 || back["can_undo"] != true {
		t.Fatal(back)
	}
	if got := e.call(userA, "POST", base+"/restore", map[string]any{"revision": rev + 1, "to": 2}, 422); got["detail"] != "revision 2 is not kept" {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", base+"/snapshots/"+old["id"].(string), nil, 204)
	e.call(userA, "DELETE", base+"/snapshots/"+old["id"].(string), nil, 404)

	// the agent view
	agent := e.call(userA, "GET", base+"?view=agent", nil, 200)
	blocks := agent["blocks"].([]any)
	v1 := blocks[2].(map[string]any)
	if agent["frames"] != float64(192) || len(blocks) != 3 || v1["status"] != "draft" || fmt.Sprint(v1["tied_to"]) != "[s1]" ||
		v1["at_s"] != 0.417 || agent["transitions"].([]any)[0].(map[string]any)["at_s"] != float64(4) {
		t.Fatal(agent)
	}
	if req, _ := v1["request"].(map[string]any); req["kind"] == nil { // API 1.88: the whole request, to send back
		t.Fatal(v1)
	}
	e.call(userA, "GET", base+"?view=x", nil, 422)

	// rename, delete
	if got := e.call(userA, "PATCH", base, map[string]any{"name": "Tide"}, 200); got["name"] != "Tide" || got["revision"] != float64(rev+1) {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", base, nil, 204)
	e.call(userA, "GET", base, nil, 404)
	if e.count(`SELECT count(*) FROM magic_snapshots`) != 0 || revisions() != 0 {
		t.Fatal("a deleted board's revisions and snapshots stay")
	}

	// 100 boards per user
	for i := 0; i < 99; i++ {
		e.call(userA, "POST", boards, nil, 201)
	}
	if got := e.call(userA, "POST", boards, nil, 409); got["detail"] != "You have 100 boards; delete some first." {
		t.Fatal(got)
	}
	e.call(userB, "POST", boards, nil, 201)
}
