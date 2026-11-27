// cancel_runs (alpha.6; owner, 2026-11-27: "yes build it"): the Assistant stops a board's runs still going with no
// card, of the blocks named, a lane's, the runs named or all; each by its run's own cancel (a queued run drops at once);
// a run already ended is left out; the args must name exactly one; another user's board is not found.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestAssistantCancelRuns(t *testing.T) {
	e := setup(t)
	e.assistantOn()
	thread := e.call(userA, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	other := e.call(userB, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)["id"].(string)
	board := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Rain",
		"doc": magicRunBoard(e.clipTake(userA, 2, true, nil))}, 201)
	id := board["id"].(string)
	base := "/v1/studio/magic/boards/" + id
	runs := map[string]string{}
	for _, r := range e.call(userA, "POST", base+"/run", map[string]any{"revision": 1, "blocks": []string{"s1", "s2"}}, 202)["started"].([]any) {
		runs[r.(map[string]any)["block"].(string)] = r.(map[string]any)["run_id"].(string)
	}
	if len(runs) != 2 {
		t.Fatal(runs)
	}
	n := 0
	ask := func(owner, th string, args map[string]any) message.ToolsReply {
		n++
		raw, _ := json.Marshal(args)
		return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: owner, ThreadID: th,
			CallID: fmt.Sprintf("%s:1:%d", th, n), Tool: "cancel_runs", Args: raw})
	}
	stopped := func(r message.ToolsReply) string {
		t.Helper()
		if r.Code != "" || r.Preview != nil {
			t.Fatal(r)
		}
		raw, _ := json.Marshal(r.Result)
		var out struct {
			Stopped []struct{ RunID, Block, Status string } `json:"stopped"`
		}
		_ = json.Unmarshal(raw, &out)
		s := ""
		for _, x := range out.Stopped {
			s += fmt.Sprintf("%s:%s ", x.Block, x.Status)
		}
		return s
	}

	for _, bad := range []map[string]any{{"board_id": id}, {"board_id": id, "lane": "shots", "all": true}, {"board_id": "x", "all": true}} {
		if r := ask(userA, thread, bad); r.Code != "invalid" {
			t.Fatal(bad, r)
		}
	}
	if r := ask(userB, other, map[string]any{"board_id": id, "all": true}); r.Code != "not_found" {
		t.Fatal(r) // another user's board
	}
	// the runs named: only that one, dropped at once (never sent)
	if got := stopped(ask(userA, thread, map[string]any{"board_id": id, "run_ids": []string{runs["s1"]}})); got != "s1:cancelled " {
		t.Fatal(got)
	}
	// another lane: nothing of it is going
	if got := stopped(ask(userA, thread, map[string]any{"board_id": id, "lane": "music"})); got != "" {
		t.Fatal(got)
	}
	// the lane's: the one still going; then all: none (ended runs are left out)
	if got := stopped(ask(userA, thread, map[string]any{"board_id": id, "lane": "shots"})); got != "s2:cancelled " {
		t.Fatal(got)
	}
	if got := stopped(ask(userA, thread, map[string]any{"board_id": id, "all": true})); got != "" {
		t.Fatal(got)
	}
	for _, r := range e.call(userA, "GET", base, nil, 200)["runs"].([]any) {
		if r := r.(map[string]any); r["status"] != "cancelled" {
			t.Fatal(r)
		}
	}
}
