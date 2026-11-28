// The Assistant's Magic tools (API 1.87, plan 39 phase F, contracts/README.md "Magic: the Assistant"): reads as the
// thread's owner, a dry run that stores nothing, a card tool's preview before the user's Yes and the write after it
// (via assistant, the revision's summary in words), the ask rule (frames start at once, the rest after the Yes; within
// the limit no preview at all), a board frame, a send, and another user's board.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestAssistantMagic(t *testing.T) {
	e := setup(t)
	e.voices()
	e.assistantOn()
	thread := e.call(userA, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	shot := e.clipTake(userA, 2, true, nil)
	doc := magicRunBoard(shot)
	doc["lanes"] = append([]any{map[string]any{"id": "story", "name": "Storyboard", "role": "storyboard"}}, doc["lanes"].([]any)...)
	doc["blocks"] = append(doc["blocks"].([]any), map[string]any{"id": "f1", "lane": "story", "at": 0, "length": 48,
		"kind": "frame", "name": "Frame 1", "request": map[string]any{"kind": "frame", "prompt": "rain"}, "takes": []string{},
		"effects": []any{}})
	board := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Rain", "doc": doc}, 201)
	id := board["id"].(string)
	base := "/v1/studio/magic/boards/" + id

	n := 0
	ask := func(owner, call, tool string, confirmed bool, args map[string]any) message.ToolsReply {
		raw, _ := json.Marshal(args)
		return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: owner, ThreadID: thread,
			CallID: thread + ":1:" + call, Tool: tool, Args: raw, Confirmed: confirmed})
	}
	fresh := func() string { n++; return fmt.Sprint(n) }
	asMap := func(v any) map[string]any {
		raw, _ := json.Marshal(v)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	result := func(r message.ToolsReply) map[string]any {
		t.Helper()
		if r.Code != "" || r.Preview != nil {
			t.Fatal(r)
		}
		return asMap(r.Result)
	}
	preview := func(r message.ToolsReply) map[string]any {
		t.Helper()
		if r.Code != "" || r.Result != nil || r.Preview == nil {
			t.Fatal(r)
		}
		return asMap(r.Preview)
	}
	revision := func() int {
		return int(e.call(userA, "GET", base, nil, 200)["revision"].(float64))
	}

	// reads
	if got := result(ask(userA, fresh(), "list_boards", false, map[string]any{})); len(got["boards"].([]any)) != 1 {
		t.Fatal(got)
	}
	shown := result(ask(userA, fresh(), "show_board", false, map[string]any{"board_id": id}))
	if shown["board"].(map[string]any)["revision"] != float64(1) || shown["plan"].(map[string]any)["order"] == nil {
		t.Fatal(shown)
	}
	// API 1.93: every take id, newest first, and whether the shown one is pinned
	for _, b := range shown["board"].(map[string]any)["blocks"].([]any) {
		if b := b.(map[string]any); b["id"] == "s1" && (fmt.Sprint(b["take_ids"]) != "["+shot+"]" || b["pinned"] != false) {
			t.Fatal(b)
		}
	}
	if got := result(ask(userA, fresh(), "list_boards", false, map[string]any{"q": "nothing like it"})); len(got["boards"].([]any)) != 0 {
		t.Fatal(got)
	}
	if got := result(ask(userA, fresh(), "list_boards", false, map[string]any{"q": "rain"})); len(got["boards"].([]any)) != 1 {
		t.Fatal(got)
	}
	// magic_kinds: the models of one kind (workflow ids for a request's model)
	kinds := result(ask(userA, fresh(), "magic_kinds", false, map[string]any{"kind": "video"}))["kinds"].([]any)
	if len(kinds) != 1 || kinds[0].(map[string]any)["kind"] != "video" || len(kinds[0].(map[string]any)["models"].([]any)) == 0 {
		t.Fatal(kinds)
	}
	// create_board: an empty board, no card; asked again under the same call: the same board, not a second one
	call0 := fresh()
	made := result(ask(userA, call0, "create_board", false, map[string]any{"name": "Brief"}))
	if again := result(ask(userA, call0, "create_board", false, map[string]any{"name": "Brief"})); made["name"] != "Brief" ||
		made["revision"] != float64(1) || again["board_id"] != made["board_id"] {
		t.Fatal(made, again)
	}
	if n := len(e.call(userA, "GET", "/v1/studio/magic/boards", nil, 200)["boards"].([]any)); n != 2 {
		t.Fatal(n)
	}
	e.call(userA, "DELETE", "/v1/studio/magic/boards/"+made["board_id"].(string), nil, 204)
	add := []any{map[string]any{"op": "add", "mode": "insert", "block": map[string]any{"id": "s3", "lane": "shots", "at": 96,
		"length": 48, "kind": "video", "name": "Shot 3", "request": map[string]any{"kind": "video", "prompt": "a bus",
			"model": "echo-video"}, "takes": []any{}, "effects": []any{}}}}
	tried := result(ask(userA, fresh(), "try_board_edit", false, map[string]any{"board_id": id, "base_revision": 1, "ops": add}))
	if tried["summary"] != "Added Shot 3" || tried["revision"] != float64(1) || revision() != 1 {
		t.Fatal(tried)
	}
	bad := ask(userA, fresh(), "try_board_edit", false, map[string]any{"board_id": id, "base_revision": 1,
		"ops": []any{map[string]any{"op": "lane_delete", "id": "gone"}}})
	if bad.Code != "invalid" || bad.Error != "ops[0]: That lane is gone." {
		t.Fatal(bad)
	}
	// asset pictures go on the asset's sheet: a lane named for them, or an image on one, is refused (try and edit alike)
	sheet := "Asset pictures go on the asset's sheet: use make_views {asset, views}, not a lane or blocks of their own."
	cast := []any{map[string]any{"op": "lane_add", "lane": map[string]any{"id": "cast", "name": "Characters", "role": "free"}},
		map[string]any{"op": "add", "mode": "insert", "block": map[string]any{"id": "c1", "lane": "cast", "at": 0, "length": 24,
			"kind": "image", "name": "Mara", "request": map[string]any{"kind": "image", "prompt": "@mara front view", "model": "echo-image"},
			"takes": []any{}, "effects": []any{}}}}
	for _, tool := range []string{"try_board_edit", "edit_board"} {
		if r := ask(userA, fresh(), tool, false, map[string]any{"board_id": id, "base_revision": 1, "ops": cast}); r.Code != "invalid" || r.Error != sheet {
			t.Fatal(tool, r)
		}
	}
	free := []any{map[string]any{"op": "lane_add", "lane": map[string]any{"id": "b2", "name": "B roll", "role": "free"}}}
	if r := ask(userA, fresh(), "try_board_edit", false, map[string]any{"board_id": id, "base_revision": 1, "ops": free}); r.Code != "" {
		t.Fatal(r) // another name passes
	}

	// edit_board: the preview (the summary and the estimate of what it adds), nothing stored; the Yes saves it
	call := fresh()
	asked := preview(ask(userA, call, "edit_board", false, map[string]any{"board_id": id, "base_revision": 1, "ops": add}))
	est := asked["estimate"].(map[string]any)
	if asked["summary"] != "Added Shot 3" || est["host"] != "fake" || est["blocks"] != float64(1) || est["seconds"] == nil || revision() != 1 {
		t.Fatal(asked)
	}
	if again := preview(ask(userA, call, "edit_board", false, map[string]any{"board_id": id, "base_revision": 1, "ops": add})); again["summary"] != "Added Shot 3" {
		t.Fatal(again) // asked again without the Yes: the same preview
	}
	done := result(ask(userA, call, "edit_board", true, map[string]any{"board_id": id, "base_revision": 1, "ops": add}))
	if done["revision"] != float64(2) || done["summary"] != "Added Shot 3" {
		t.Fatal(done)
	}
	if again := result(ask(userA, call, "edit_board", true, map[string]any{"board_id": id, "base_revision": 1, "ops": add})); again["revision"] != float64(2) || revision() != 2 {
		t.Fatal(again) // the Yes asked again: the same reply, nothing done twice
	}
	revs := e.call(userA, "GET", base+"/revisions", nil, 200)["revisions"].([]any)
	if top := revs[0].(map[string]any); top["via"] != "assistant" || top["by"] != "user" || top["ops_summary"] != "Added Shot 3" {
		t.Fatal(top)
	}

	// restore_board: the revision it brings back, then a new revision
	call = fresh()
	if p := preview(ask(userA, call, "restore_board", false, map[string]any{"board_id": id, "revision": 2, "to": 1})); p["to"].(map[string]any)["revision"] != float64(1) {
		t.Fatal(p)
	}
	if r := result(ask(userA, call, "restore_board", true, map[string]any{"board_id": id, "revision": 2, "to": 1})); r["revision"] != float64(3) {
		t.Fatal(r)
	}
	if r := ask(userA, fresh(), "restore_board", false, map[string]any{"board_id": id, "revision": 3, "to": 99}); r.Code != "invalid" {
		t.Fatal(r)
	}

	// the ask rule: above the limit the frame starts at once and the rest waits for the Yes
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 0}, 200)
	call = fresh()
	pick := map[string]any{"board_id": id, "revision": 3, "blocks": []string{"f1", "s2"}}
	p := preview(ask(userA, call, "run_blocks", false, pick))
	frames := p["frames"].([]any)
	if p["plan"].(map[string]any)["ask"] != true || len(frames) != 1 || frames[0].(map[string]any)["block"] != "f1" ||
		frames[0].(map[string]any)["run_id"] == "" || frames[0].(map[string]any)["name"] != "Frame 1" {
		t.Fatal(p)
	}
	if rest := p["rest"].(map[string]any); fmt.Sprint(rest["names"]) != "[Shot 2]" || rest["host"] != "fake" ||
		rest["ask_text"] != "1 shot, about 1 s" {
		t.Fatal(rest)
	}
	r := result(ask(userA, call, "run_blocks", true, pick))
	if started := r["started"].([]any); len(started) != 1 || started[0].(map[string]any)["block"] != "s2" {
		t.Fatal(r)
	}
	b := e.call(userA, "GET", base, nil, 200)
	for _, run := range b["runs"].([]any) {
		if run := run.(map[string]any); run["by"] != "assistant" {
			t.Fatal(run)
		}
	}
	// API 1.88: board frames never ask, so they are left out of the ask rule's total (a limit of 0 included)
	if p := e.call(userA, "POST", base+"/plan", map[string]any{"blocks": []string{"f1"}}, 200); p["ask"] != false || p["seconds"] == nil {
		t.Fatal(p)
	}
	// within the limit: it runs at once, no preview
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 5}, 200)
	if r := result(ask(userA, fresh(), "run_blocks", false, map[string]any{"board_id": id, "revision": 3, "blocks": []string{"v1"}})); len(r["started"].([]any)) != 2 || r["seconds"] == nil {
		t.Fatal(r)
	}
	if r := ask(userA, fresh(), "run_blocks", false, map[string]any{"board_id": id, "revision": 1, "blocks": []string{"v1"}}); r.Code != "conflict" {
		t.Fatal(r)
	}

	// a board frame: what each lane shows at 1 s
	f := result(ask(userA, fresh(), "board_frame", false, map[string]any{"board_id": id, "at": 1}))
	var lanes []string
	for _, s := range f["shown"].([]any) {
		s := s.(map[string]any)
		lanes = append(lanes, fmt.Sprint(s["lane"], " ", s["block"], " ", s["offset_s"]))
		if s["block"] == "s1" && s["take_id"] != shot {
			t.Fatal(s)
		}
	}
	if f["frame"] != float64(24) || fmt.Sprint(lanes) != "[story f1 1 shots s1 1 lines v1 1 music m1 1]" {
		t.Fatal(f)
	}

	// send_to_timeline: the plan, then the new sequence
	call = fresh()
	if p := preview(ask(userA, call, "send_to_timeline", false, map[string]any{"board_id": id, "revision": 3})); p["plan"] == nil {
		t.Fatal(p)
	}
	// the Yes asked twice at once (a timeout's retry): one send, both answers the same
	var wg sync.WaitGroup
	sent := make([]map[string]any, 2)
	for i := range sent {
		wg.Go(func() {
			sent[i] = asMap(ask(userA, call, "send_to_timeline", true, map[string]any{"board_id": id, "revision": 3}).Result)
		})
	}
	wg.Wait()
	s0, s1 := sent[0]["send"], sent[1]["send"]
	if s0 == nil || fmt.Sprint(s0) != fmt.Sprint(s1) {
		t.Fatal(sent)
	}
	if l := e.call(userA, "GET", base+"/sends", nil, 200)["sends"].([]any); len(l) != 1 {
		t.Fatal(l)
	}

	// API 1.93: a failed block says why; a dialogue's turns; an asset's sheet, voice and where it is used
	s2run := r["started"].([]any)[0].(map[string]any)["run_id"].(string)
	e.exec(`UPDATE runs SET status = 'failed', error = 'out of memory' WHERE id = $1`, s2run) // as the projector ends it
	ed := e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": revision(), "ops": []any{
		map[string]any{"op": "asset_add", "asset": map[string]any{"id": "mara", "kind": "character", "handle": "mara",
			"name": "Mara", "voice_id": "echo/ada", "note": "a fisher"}},
		map[string]any{"op": "add", "mode": "insert", "block": map[string]any{"id": "d1", "lane": "lines", "at": 96, "length": 48,
			"kind": "voice", "name": "Talk", "request": map[string]any{"kind": "voice", "turns": []any{
				map[string]any{"speaker": "mara", "text": "Hi."}, map[string]any{"speaker": "mara", "text": "Bye."}}},
			"takes": []any{}, "effects": []any{}}}}}, 200)
	rev := int(ed["board"].(map[string]any)["revision"].(float64))
	view := result(ask(userA, fresh(), "show_board", false, map[string]any{"board_id": id}))["board"].(map[string]any)
	byID := map[string]map[string]any{}
	for _, b := range view["blocks"].([]any) {
		byID[b.(map[string]any)["id"].(string)] = b.(map[string]any)
	}
	if s2 := byID["s2"]; s2["status"] != "failed" || s2["error"] != "out of memory" {
		t.Fatal(s2)
	}
	if d1 := byID["d1"]; len(d1["turns"].([]any)) != 2 || d1["turns"].([]any)[0].(map[string]any)["status"] != "draft" {
		t.Fatal(d1)
	}
	if m := view["assets"].([]any)[0].(map[string]any); m["voice_id"] != "echo/ada" || m["note"] != "a fisher" ||
		fmt.Sprint(m["sheet"]) != "[]" || fmt.Sprint(m["used_in"]) != "[d1]" {
		t.Fatal(m)
	}

	// make_views: within the limit the views start at once (no card) with their estimate
	// API 1.98: its size and advanced are taken (they were refused as unknown args)
	views := map[string]any{"board_id": id, "revision": rev, "asset": "mara", "views": []string{"Front"},
		"width": 768, "height": 1024, "advanced": map[string]any{}}
	made = result(ask(userA, fresh(), "make_views", false, views))
	if len(made["started"].([]any)) != 1 || made["seconds"] == nil || made["revision"] == nil {
		t.Fatal(made)
	}
	// above the limit: the card first (run_blocks' preview shape), the Yes starts them
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 0}, 200)
	call = fresh()
	views["views"], views["revision"] = []string{"Side", "Back"}, revision()
	p = preview(ask(userA, call, "make_views", false, views))
	if rest := p["rest"].(map[string]any); fmt.Sprint(rest["names"]) != "[Side Back]" || rest["host"] != "fake" ||
		!strings.HasPrefix(rest["ask_text"].(string), "2 views, about ") || len(p["frames"].([]any)) != 0 {
		t.Fatal(p)
	}
	if got := result(ask(userA, call, "make_views", true, views)); len(got["started"].([]any)) != 2 {
		t.Fatal(got)
	}
	if bad := ask(userA, fresh(), "make_views", false, map[string]any{"board_id": id, "revision": revision(), "asset": "nobody",
		"views": []string{"Front"}}); bad.Code != "invalid" || bad.Error != "unknown asset nobody" {
		t.Fatal(bad)
	}
	// ask (Auto past its hour's caps): the preview even within the limit, for run_blocks and make_views
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 5}, 200)
	asks := func(tool string, args map[string]any) message.ToolsReply {
		raw, _ := json.Marshal(args)
		return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: userA, ThreadID: thread,
			CallID: thread + ":1:" + fresh(), Tool: tool, Args: raw, Ask: true})
	}
	if p := preview(asks("run_blocks", map[string]any{"board_id": id, "revision": revision(), "blocks": []string{"v1"}})); fmt.Sprint(p["rest"].(map[string]any)["names"]) != "[Line 1]" {
		t.Fatal(p)
	}
	if p := preview(asks("make_views", map[string]any{"board_id": id, "revision": revision(), "asset": "mara", "views": []string{"Top"}})); fmt.Sprint(p["rest"].(map[string]any)["names"]) != "[Top]" {
		t.Fatal(p)
	}

	// another user's board, bad args
	other := e.call(userB, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	raw, _ := json.Marshal(map[string]any{"board_id": id})
	if r := e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: userB, ThreadID: other, CallID: other + ":1:0",
		Tool: "show_board", Args: raw}); r.Code != "not_found" {
		t.Fatal(r)
	}
	// API 1.93: make_views on another user's board is not found, and nothing runs
	raw, _ = json.Marshal(map[string]any{"board_id": id, "revision": revision(), "asset": "mara", "views": []string{"Back"}})
	if r := e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: userB, ThreadID: other, CallID: other + ":1:1",
		Tool: "make_views", Args: raw, Confirmed: true}); r.Code != "not_found" {
		t.Fatal(r)
	}
	for _, args := range []map[string]any{{"board_id": "x"}, {"board_id": id, "lane": "shots"}, {}} {
		if r := ask(userA, fresh(), "show_board", false, args); r.Code != "invalid" {
			t.Fatal(args, r)
		}
	}
}

// API 1.111: design_voice and run_blocks (its preview and the run) take a revision stale only by run takes (a run or
// Make views finishing); a user's edit since keeps the conflict.
func TestAssistantStaleByTakes(t *testing.T) {
	e := setup(t)
	e.voices()
	e.assistantOn()
	thread := e.call(userA, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	shot := e.clipTake(userA, 2, true, nil)
	b := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Rain", "doc": magicRunBoard(shot)}, 201)
	id := b["id"].(string)
	base := "/v1/studio/magic/boards/" + id
	revision := func() float64 { return e.call(userA, "GET", base, nil, 200)["revision"].(float64) }
	e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": 1, "ops": []any{map[string]any{"op": "asset_add",
		"asset": map[string]any{"id": "mara", "kind": "character", "handle": "mara", "name": "Mara"}}}}, 200)
	started := e.call(userA, "POST", base+"/run", map[string]any{"revision": 2, "blocks": []string{"s2"}}, 202)
	runID := started["started"].([]any)[0].(map[string]any)["run_id"].(string)
	session := started["board"].(map[string]any)["session_id"].(string)
	e.relay()
	e.takeOf(userA, session, runID)
	e.emit(userA, session, runID, "status", 3, map[string]any{"status": "succeeded"})
	if r := revision(); r != 3 {
		t.Fatal(r)
	}

	n := 0
	ask := func(call, tool string, confirmed bool, args map[string]any) message.ToolsReply {
		raw, _ := json.Marshal(args)
		return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: userA, ThreadID: thread,
			CallID: thread + ":1:" + call, Tool: tool, Args: raw, Confirmed: confirmed})
	}
	fresh := func() string { n++; return fmt.Sprint(n) }
	voice := func(rev int) map[string]any {
		return map[string]any{"board_id": id, "revision": rev, "asset": "mara", "design": "low and warm", "line": "Hello there."}
	}
	pick := func(rev int) map[string]any {
		return map[string]any{"board_id": id, "revision": rev, "blocks": []string{"v1"}}
	}
	// at 2, only the run's take since (3): the voice is designed and the preview and the run go ahead
	if r := ask(fresh(), "design_voice", true, voice(2)); r.Code != "" || r.Result == nil {
		t.Fatal(r)
	}
	e.call(userA, "PUT", "/v1/studio/magic/settings", map[string]any{"ask_above_gpu_minutes": 0}, 200)
	call := fresh()
	if r := ask(call, "run_blocks", false, pick(2)); r.Code != "" || r.Preview == nil {
		t.Fatal(r)
	}
	if r := ask(call, "run_blocks", true, pick(2)); r.Code != "" || r.Result == nil {
		t.Fatal(r)
	}
	// a user's edit since (4): both still refuse, as the route does
	e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": 3, "ops": []any{
		map[string]any{"op": "block", "id": "s2", "set": map[string]any{"name": "Rain"}}}}, 200)
	stale := "board changed since you opened it (now revision 4)"
	if r := ask(fresh(), "design_voice", true, voice(3)); r.Code != "conflict" || r.Error != stale {
		t.Fatal(r)
	}
	if r := ask(fresh(), "run_blocks", false, pick(3)); r.Code != "conflict" || r.Error != stale {
		t.Fatal(r)
	}
	e.call(userA, "POST", base+"/run", map[string]any{"revision": 3, "blocks": []string{"s1"}}, 409)
}
