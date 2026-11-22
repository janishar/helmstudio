// Integration tests of timeline edits by ops (API 1.49, contracts/README.md "Timeline: edits by ops"; plan 24 28.12):
// /edits all or nothing with per-op errors, dry runs, stale and retried calls, fill refused, isolation, the op cap,
// studio-made ids, one undo per call, via and ops_summary, the history, restore (reconciling a finished shot), the
// 100 kept, the agent view and a stored frame at a time.
package tests

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func clipIDs(tl map[string]any) []string {
	var out []string
	for _, c := range v1(tl) {
		out = append(out, c.(map[string]any)["id"].(string))
	}
	return out
}

func TestTimelineEdits(t *testing.T) {
	e := setup(t)
	a, b, c := e.clipTake(userA, 2, true, map[string]any{"workflow_id": "echo-video"}), e.clipTake(userA, 2, true, nil),
		e.clipTake(userA, 2, true, nil)
	foreign := e.clipTake(userB, 2, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a, b, c}}, 201)
	base, ids := "/v1/studio/timelines/"+tl["id"].(string), clipIDs(tl)
	edits := func(revision int, dry bool, ops ...map[string]any) map[string]any {
		return map[string]any{"base_revision": revision, "dry_run": dry, "ops": ops}
	}
	split := map[string]any{"op": "split", "track": "V1", "at": 1}
	revisions := func() int { return e.count(`SELECT count(*) FROM timeline_revisions WHERE timeline_id = $1`, tl["id"]) }

	// another user's sequence is 404 on every new route
	for _, r := range []struct {
		method, path string
		body         any
	}{{"POST", "/edits", edits(1, false, split)}, {"GET", "/revisions", nil}, {"GET", "/revisions/1", nil},
		{"POST", "/restore", map[string]any{"revision": 1, "to": 1}}, {"POST", "/frame", map[string]any{"at": 0}},
		{"GET", "?view=agent", nil}} {
		e.call(userB, r.method, base+r.path, r.body, 404)
	}

	// a dry run answers the result and stores nothing; a stale one is the 409
	try := e.call(userA, "POST", base+"/edits", edits(1, true, split, map[string]any{"op": "remove", "clip": ids[1]}), 200)
	diff := try["diff"].(map[string]any)
	added := diff["added"].([]any)
	if try["revision"] != float64(1) || try["dry_run"] != true || len(added) != 1 ||
		!regexp.MustCompile(`^[a-z0-9]{8}$`).MatchString(added[0].(string)) || fmt.Sprint(diff["removed"]) != "["+ids[1]+"]" ||
		len(v1(try["timeline"].(map[string]any))) != 3 || try["timeline"].(map[string]any)["revision"] != float64(1) {
		t.Fatal("dry run", try)
	}
	if revisions() != 1 || e.call(userA, "GET", base, nil, 200)["revision"] != float64(1) {
		t.Fatal("a dry run stored")
	}
	e.call(userA, "POST", base+"/edits", edits(2, true, split), 409)

	// all or nothing: the failing op's index and code, nothing stored
	bad := e.call(userA, "POST", base+"/edits", edits(1, false, split, map[string]any{"op": "trim", "clip": "zz", "edge": "in", "delta": 1}), 422)
	if bad["detail"] != "ops[1]: unknown clip zz" || bad["op_index"] != float64(1) || bad["code"] != "unknown_clip" {
		t.Fatal(bad)
	}
	dissolve := map[string]any{"op": "set", "clip": ids[1], "transition_in": map[string]any{"type": "dissolve", "frames": 24}}
	bad = e.call(userA, "POST", base+"/edits", edits(1, false, dissolve), 422)
	if bad["op_index"] != float64(0) || bad["code"] != "dissolve_handles" || len(bad["problems"].([]any)) == 0 ||
		!strings.HasPrefix(bad["detail"].(string), "ops[0]: timeline has 1 problem") {
		t.Fatal(bad)
	}
	bad = e.call(userA, "POST", base+"/edits", edits(1, false, map[string]any{"op": "fill", "clip": ids[0], "take_id": a}), 422)
	if bad["detail"] != "ops[0]: fill is studio's" {
		t.Fatal(bad)
	}
	many := make([]map[string]any, 201)
	for i := range many {
		many[i] = map[string]any{"op": "track", "track": "V1", "mute": i%2 == 0}
	}
	if bad = e.call(userA, "POST", base+"/edits", edits(1, false, many...), 422); !strings.HasPrefix(bad["detail"].(string), "too_many_ops") {
		t.Fatal(bad)
	}
	e.call(userA, "POST", base+"/edits", edits(1, false, map[string]any{"op": "split", "track": "V1", "at": 1, "speed": 2}), 422)
	for _, op := range []map[string]any{{"op": "add", "take_ids": []string{foreign}}, {"op": "replace", "clip": ids[0], "take_id": foreign}} {
		if bad = e.call(userA, "POST", base+"/edits", edits(1, false, op), 422); bad["code"] != "take_not_found" ||
			bad["detail"] != "ops[0]: take "+foreign+" not found" {
			t.Fatal(op, bad)
		}
	}
	if revisions() != 1 {
		t.Fatal("a refused call stored")
	}

	// an apply: studio makes the ids nobody gave (unique, never one the call gives), one revision, via mcp
	ops := []map[string]any{split, {"op": "add", "take_ids": []string{a, b}, "ids": []string{"mine"}},
		{"op": "split", "track": "V1", "at": 3.01}, {"op": "trim", "clip": "mine", "edge": "out", "delta": -0.5}}
	done := e.callVia(userA, "mcp", "POST", base+"/edits", edits(1, false, ops...), 200)
	added = done["diff"].(map[string]any)["added"].([]any)
	seen := map[string]bool{}
	for _, id := range append(added, ids[0], ids[1], ids[2]) {
		seen[id.(string)] = true
	}
	if done["revision"] != float64(2) || len(added) != 4 || len(seen) != 7 || !seen["mine"] ||
		fmt.Sprint(done["warnings"]) != "[ops[2]: at 3.01 snapped to 3]" || len(v1(done["timeline"].(map[string]any))) != 7 {
		t.Fatal("apply", done)
	}
	// a retry of the call that landed is a 409, never a second write
	e.call(userA, "POST", base+"/edits", edits(1, false, ops...), 409)
	if revisions() != 2 {
		t.Fatal("retry wrote")
	}
	// one undo undoes the whole call
	tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": 2}, 200)
	if fmt.Sprint(clipIDs(tl)) != fmt.Sprint(ids) {
		t.Fatal("undo", clipIDs(tl))
	}

	// the history: newest first, who and what
	list := e.call(userA, "GET", base+"/revisions", nil, 200)["revisions"].([]any)
	summary := func(r any) string {
		m := r.(map[string]any)
		return fmt.Sprint(m["revision"], " ", m["by"], " ", m["via"], " ", m["ops_summary"], " ", m["clips"])
	}
	if len(list) != 3 || summary(list[0]) != "3 user api undo 3" || summary(list[1]) != "2 user mcp split, add, trim 7" ||
		summary(list[2]) != "1 user api create 3" {
		t.Fatal(list)
	}
	if page := e.call(userA, "GET", base+"/revisions?before=3&limit=1", nil, 200)["revisions"].([]any); len(page) != 1 ||
		page[0].(map[string]any)["revision"] != float64(2) {
		t.Fatal(page)
	}
	e.call(userA, "GET", base+"/revisions?limit=101", nil, 422)
	one := e.call(userA, "GET", base+"/revisions/2", nil, 200)
	if one["via"] != "mcp" || len(v1(one["document"].(map[string]any))) != 7 {
		t.Fatal(one)
	}
	e.call(userA, "GET", base+"/revisions/9", nil, 404)
	e.call(userA, "GET", base+"/revisions/x", nil, 404)

	// restore: revision 2 again as revision 4; undo goes back to 3
	tl = e.call(userA, "POST", base+"/restore", map[string]any{"revision": 3, "to": 2}, 200)
	if tl["revision"] != float64(4) || len(v1(tl)) != 7 || tl["can_undo"] != true || tl["can_redo"] != false {
		t.Fatal("restore", tl)
	}
	e.call(userA, "POST", base+"/restore", map[string]any{"revision": 3, "to": 2}, 409)
	if bad = e.call(userA, "POST", base+"/restore", map[string]any{"revision": 4, "to": 99}, 422); bad["detail"] != "revision 99 is not kept" {
		t.Fatal(bad)
	}
	if tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": 4}, 200); len(v1(tl)) != 3 {
		t.Fatal("undo a restore", tl)
	}

	// the agent view: flat, seconds and frames, titles, no signed links
	view := e.call(userA, "GET", base+"?view=agent", nil, 200)
	clips := view["clips"].([]any)
	second := clips[1].(map[string]any)
	if view["revision"] != float64(5) || len(clips) != 3 || second["kind"] != "video" || second["at_f"] != float64(48) ||
		second["length"] != float64(2) || second["take_title"] != "a fox" || view["takes"] != nil {
		t.Fatal(view)
	}
	e.call(userA, "GET", base+"?view=full", nil, 422)

	// a frame at a time: a run's poster near the start, the stored last frame near the end, an upload's last frame
	// (its poster is not frame 0), empty past the end
	frame := func(at float64) map[string]any {
		return e.call(userA, "POST", base+"/frame", map[string]any{"at": at}, 200)
	}
	if f := frame(0.25); f["clip"] != ids[0] || f["shown"] != "first" || !strings.Contains(f["url"].(string), "0.poster.png") {
		t.Fatal(f)
	}
	if f := frame(1.9); f["shown"] != "last" || f["shown_t"] != float64(47)/24 || !strings.Contains(f["url"].(string), "0.last.png") {
		t.Fatal(f)
	}
	if f := frame(2.1); f["clip"] != ids[1] || f["shown"] != "last" || f["t"] != float64(2)/24 {
		t.Fatal("an upload: no frame 0 stored, so its last", f)
	}
	if f := frame(60); f["empty"] != true || f["clip"] != nil || f["url"] != nil {
		t.Fatal(f)
	}
}

func TestTimelineRestoreReconciles(t *testing.T) {
	e := setup(t)
	a := e.clipTake(userA, 2, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a}}, 201)
	base := "/v1/studio/timelines/" + tl["id"].(string)
	run := map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{"prompt.text": "a fox"}}
	shot := e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"at": 2, "length": 1, "id": "gap"}, "run": run}, 202)
	runID, sessionID := shot["run"].(map[string]any)["id"].(string), tl["session_id"].(string)
	waiting := int(shot["timeline"].(map[string]any)["revision"].(float64))
	e.relay()
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := videoOutput(userA, runID)
	out["width"], out["height"] = 256, 128
	e.emit(userA, sessionID, runID, "output", 2, out)
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded"})
	tl = e.call(userA, "GET", base, nil, 200)
	if list := e.call(userA, "GET", base+"/revisions?limit=1", nil, 200)["revisions"].([]any); list[0].(map[string]any)["by"] != "studio" ||
		list[0].(map[string]any)["via"] != nil || list[0].(map[string]any)["ops_summary"] != "fill" {
		t.Fatal("the fill's revision", list)
	}
	// the revision with the shot still waiting comes back filled: a finished shot never returns as a placeholder
	tl = e.call(userA, "POST", base+"/restore", map[string]any{"revision": tl["revision"], "to": waiting}, 200)
	if slot := v1(tl)[1].(map[string]any); slot["take_id"] == nil || slot["shot"] != nil {
		t.Fatal("restore did not reconcile", slot)
	}

	// the last 100 are kept; an older one is no longer restorable
	for range 101 {
		tl = e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": tl["revision"],
			"ops": []any{map[string]any{"op": "track", "track": "V1", "mute": tl["revision"].(float64) > 0 && int(tl["revision"].(float64))%2 == 0}}}, 200)["timeline"].(map[string]any)
	}
	if n := e.count(`SELECT count(*) FROM timeline_revisions WHERE timeline_id = $1`, tl["id"]); n != 100 {
		t.Fatal("kept", n)
	}
	if list := e.call(userA, "GET", base+"/revisions?limit=100", nil, 200)["revisions"].([]any); len(list) != 100 {
		t.Fatal(len(list))
	}
	e.call(userA, "POST", base+"/restore", map[string]any{"revision": tl["revision"], "to": 1}, 422)
}

// TestTimelineToolOps: plan 23's ops through /edits (API 1.50): studio makes the ids split_all, the ranges and a
// marker need; links and markers in the answer, the diff and the agent view; the new codes.
func TestTimelineToolOps(t *testing.T) {
	e := setup(t)
	a, b, c := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a, b, c}}, 201)
	base, ids := "/v1/studio/timelines/"+tl["id"].(string), clipIDs(tl)
	edits := func(revision int, ops ...map[string]any) map[string]any {
		return map[string]any{"base_revision": revision, "ops": ops}
	}
	done := e.call(userA, "POST", base+"/edits", edits(1,
		map[string]any{"op": "add_track"},
		map[string]any{"op": "add", "take_ids": []string{a}, "track": "A2", "ids": []string{"s1"}},
		map[string]any{"op": "set", "clip": "s1", "link": ids[1]},
		map[string]any{"op": "split_all", "at": 1},
		map[string]any{"op": "marker", "at": 0.5, "name": "Beat", "color": "red"},
		map[string]any{"op": "trim", "clip": ids[1], "edge": "out", "delta": -0.5},
		map[string]any{"op": "roll", "clip": ids[2], "delta": 0.25}), 200)
	diff, got := done["diff"].(map[string]any), done["timeline"].(map[string]any)
	marks := got["markers"].([]any)
	if len(diff["added"].([]any)) != 3 || len(diff["markers"].(map[string]any)["added"].([]any)) != 1 || len(marks) != 1 ||
		marks[0].(map[string]any)["name"] != "Beat" || len(v1(got)) != 4 || done["warnings"] == nil {
		t.Fatal("apply", done)
	}
	view := e.call(userA, "GET", base+"?view=agent", nil, 200)
	if links := view["links"].(map[string]any)[ids[1]].([]any); len(links) != 2 || len(view["markers"].([]any)) != 1 {
		t.Fatal("agent view", view)
	}
	for _, x := range []struct {
		op   map[string]any
		code string
	}{{map[string]any{"op": "remove_marker", "id": "zz"}, "unknown_marker"},
		{map[string]any{"op": "roll", "clip": ids[0], "delta": 0.1}, "roll_first"},
		{map[string]any{"op": "slide", "clip": "s1", "delta": 0.1}, "slide_track"},
		{map[string]any{"op": "remove_range", "from": 1, "to": 1}, "range_empty"}} {
		if bad := e.call(userA, "POST", base+"/edits", edits(2, x.op), 422); bad["code"] != x.code || bad["op_index"] != float64(0) {
			t.Fatal(x.code, bad)
		}
	}
	if bad := e.call(userA, "POST", base+"/edits", edits(2, map[string]any{"op": "marker", "at": 1, "color": "pink"}), 422); !strings.Contains(bad["detail"].(string), "color must be") {
		t.Fatal(bad)
	}
	// a range with no ids: studio makes as many as it needs (V1 and A2 cut at 0.25)
	done = e.call(userA, "POST", base+"/edits", edits(2, map[string]any{"op": "lift_range", "from": 0.25, "to": 0.5}), 200)
	if done["revision"] != float64(3) || len(done["diff"].(map[string]any)["added"].([]any)) != 3 {
		t.Fatal("lift_range", done["diff"])
	}
}
