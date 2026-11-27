// The Assistant's Magic tools (API 1.87, plan 39 phase F, contracts/README.md "Magic: the Assistant"): reads as the
// thread's owner, a dry run that stores nothing, a card tool's preview before the user's Yes and the write after it
// (via assistant, the revision's summary in words), the ask rule (frames start at once, the rest after the Yes; within
// the limit no preview at all), a board frame, a send, and another user's board.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
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
	if r := result(ask(userA, fresh(), "run_blocks", false, map[string]any{"board_id": id, "revision": 3, "blocks": []string{"v1"}})); len(r["started"].([]any)) != 2 {
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

	// another user's board, bad args
	other := e.call(userB, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	raw, _ := json.Marshal(map[string]any{"board_id": id})
	if r := e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: userB, ThreadID: other, CallID: other + ":1:0",
		Tool: "show_board", Args: raw}); r.Code != "not_found" {
		t.Fatal(r)
	}
	for _, args := range []map[string]any{{"board_id": "x"}, {"board_id": id, "lane": "shots"}, {}} {
		if r := ask(userA, fresh(), "show_board", false, args); r.Code != "invalid" {
			t.Fatal(args, r)
		}
	}
}
