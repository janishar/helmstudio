// Integration tests of the llm pack and agent nodes (contracts/README.md "The llm pack and agent nodes", API 1.14):
// text workflows are internal, a judge's ask run (Media rows, thumbnails counted, its answer on the run and in
// run_finished), decisions stored from step events and shown on the row, the takes, the review grid and the execution
// view, a judge pick with agreed and note, Run again with the same decisions, the memory proxy, and isolation.
package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

// judgePipeline is the demonstration's shape on the echo family (echo has no seed: one take per visit), a Suggest judge keeping one (a fail
// rewrites the prompt and retries once, then gives up to the output), the output of its pick.
func judgePipeline(title string) map[string]any {
	return map[string]any{"title": title, "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
		"still": map[string]any{"kind": "step", "workflow": "echo", "inputs": map[string]any{"echo.steps": 2},
			"bind": map[string]any{"prompt.text": "$prompt"}},
		"judge":   map[string]any{"kind": "judge", "takes": "still", "criteria": map[string]any{"must": []string{"A fox"}, "nice": []string{"Sharp"}}},
		"rewrite": map[string]any{"kind": "write", "from": []string{"$prompt", "judge.reasons"}, "to": "$prompt"},
		"out":     map[string]any{"kind": "output", "from": "judge.picked", "collection": "Foxes"},
	}, "edges": []any{[]any{"inputs", "still"}, []any{"still", "judge"},
		[]any{"judge", map[string]any{"pass": "out", "fail": "rewrite", "max": 1, "then": "out"}}, []any{"rewrite", "still"}}}
}

func TestAgentNodes(t *testing.T) {
	e := setup(t)
	var asked []message.MemoryRequest
	memoryReply, memoryErr := message.MemoryReply{}, error(nil)
	e.service.SetMemory(func(_ context.Context, req message.MemoryRequest) (message.MemoryReply, error) {
		asked = append(asked, req)
		return memoryReply, memoryErr
	})

	// text workflows are internal: not listed, not run from Create or a workflow batch
	for _, w := range e.call(userA, "GET", "/v1/studio/workflows", nil, 200)["workflows"].([]any) {
		if w.(map[string]any)["id"] == "echo-ask" {
			t.Fatal("echo-ask listed")
		}
	}
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	refused := e.call(userA, "POST", "/v1/studio/sessions/"+session+"/runs", map[string]any{"workflow_id": "echo-ask", "inputs": map[string]any{}}, 422)
	if refused["detail"] != "workflow echo-ask answers text; only a judge, write or decide node runs it" {
		t.Fatal(refused)
	}
	e.call(userA, "POST", "/v1/studio/batches", map[string]any{"workflow_id": "echo-ask",
		"rows": map[string]any{"lines": map[string]any{"param": "ask.prompt", "values": []string{"x"}}}}, 422)

	// validate: the worst case per row and auto resolved to the family's ask
	check := e.call(userA, "POST", "/v1/studio/pipelines/validate", judgePipeline("Foxes"), 200)
	if check["can_run"] != true || fmt.Sprint(check["bounds"]) != "map[llm_calls:4 remote_calls:0 remote_tokens:0 runs:2 tool_calls:0]" ||
		fmt.Sprint(check["models"]) != "map[judge:echo-ask rewrite:echo-ask]" {
		t.Fatal(check)
	}
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", judgePipeline("Foxes"), 201)
	pipelineID := pipeline["id"].(string)
	if fmt.Sprint(pipeline["bounds"]) != "map[llm_calls:4 remote_calls:0 remote_tokens:0 runs:2 tool_calls:0]" {
		t.Fatal(pipeline)
	}
	batch := e.submit(userA, map[string]any{"pipeline_id": pipelineID,
		"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}}}}})
	id, path, sessionID := batch["id"].(string), "/v1/studio/batches/"+batch["id"].(string), batch["session_id"].(string)
	if fmt.Sprint(batch["models"]) != "map[judge:echo-ask rewrite:echo-ask]" || batch["bounds"] == nil {
		t.Fatal(batch)
	}
	submits := e.commands("cmd.pipeline.submit")
	snapshot := data(submits[0])["snapshot"].(map[string]any)["pipeline"].(map[string]any)
	judge := snapshot["nodes"].(map[string]any)["judge"].(map[string]any)
	rewrite := snapshot["nodes"].(map[string]any)["rewrite"].(map[string]any)
	if judge["workflow"] != "echo-ask" || judge["mode"] != "suggest" || judge["keep"] != float64(1) || judge["model"] != "auto" ||
		rewrite["workflow"] != "echo-ask" || rewrite["instruction"] == nil ||
		snapshot["workflows"].(map[string]any)["echo-ask"] == nil {
		t.Fatal(snapshot)
	}

	// the judge's ask run: its workflow and branch checked, a video counted as 3 thumbnails, made once per key
	still, clip := e.image(userA), e.image(userA, "video")
	media := []any{map[string]any{"take": map[string]any{"take_id": still}}, map[string]any{"take": map[string]any{"take_id": clip}}}
	askInputs := map[string]any{"ask.prompt": "ECHO-ANSWER: {}", "ask.media": media}
	ask := func(owner, key, workflow string) (message.RunsCreated, *message.RunsCreateError) {
		return e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: owner, SessionID: sessionID,
			WorkflowID: workflow, Inputs: askInputs, Lane: "batch", BatchID: id, Row: 1, IdempotencyKey: id + ":1:" + key})
	}
	for key, workflow := range map[string]string{"1:judge:0:2": "echo-ask", "1:judge:32:0": "echo-ask", "2:judge:0:0": "echo-ask",
		"1:judge:0:0": "echo"} {
		if _, refused := ask(userA, key, workflow); refused == nil || refused.Code == "internal" {
			t.Fatal(key, refused)
		}
	}
	if _, refused := ask(userB, "1:judge:0:0", "echo-ask"); refused == nil || refused.Code != "not_found" {
		t.Fatal(refused)
	}
	run, refusal := ask(userA, "1:judge:0:0", "echo-ask")
	if refusal != nil || !run.Created {
		t.Fatal(refusal)
	}
	if e.count(`SELECT count(*) FROM runs WHERE id = $1 AND thumbs = 4`, run.RunID) != 1 {
		t.Fatal("an image and a video: 4 thumbnails")
	}
	_, sent := e.sent() // released with the video take resolved into the image form
	if len(sent) != 1 || sent[0].Type != "cmd.run.execute" || !jsonContains(sent[0].Data, `"key":"u/`+userA+`/uploads/`+clip+`.png"`) {
		t.Fatal(sent)
	}
	answer := map[string]any{"tries": []any{map[string]any{"text": `{"takes":[]}`, "error": nil}},
		"seen": []any{map[string]any{"take_id": still, "frame": nil, "key": "u/" + userA + "/llm/thumbs/" + sha('a') + ".jpg",
			"sha256": sha('a'), "width": 64, "height": 32}}, "tokens_in": 300, "tokens_out": 20, "seconds": 1.5}
	e.emit(userA, sessionID, run.RunID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, run.RunID, "status", 2, map[string]any{"status": "succeeded", "answer": answer})
	got := e.call(userA, "GET", "/v1/studio/runs/"+run.RunID, nil, 200)
	if fmt.Sprint(got["answer"]) != "map[seconds:1.5 text:{\"takes\":[]} tokens_in:300 tokens_out:20]" || len(got["outputs"].([]any)) != 0 {
		t.Fatal(got)
	}
	e.call(userB, "GET", "/v1/studio/runs/"+run.RunID, nil, 404)
	_, sent = e.sent()
	if done := finished(t, sent); len(done) != 1 || !jsonContains(done[0].Answer, `"tokens_in":300`) {
		t.Fatal(done)
	}
	if again, _ := ask(userA, "1:judge:0:0", "echo-ask"); again.Created || !jsonContains(again.Answer, `"tokens_out":20`) {
		t.Fatal(again)
	}

	// the judge's decision on its verdict step; the row parks in Suggest with the pick pre-selected
	decisionID := id + ":1:1:judge:1"
	decision := map[string]any{"id": decisionID, "replay_key": id + ":1:judge:1:" + sha('f'), "node": "judge", "kind": "judge",
		"visit": 1, "attempt": 1, "replayed": false, "replayed_from": nil, "run_ids": []string{run.RunID}, "workflow": "echo-ask",
		"model": nil, "template": "judge/v1", "system": "You are a careful reviewer.", "prompt": "The brief", "schema": "{}",
		"seen": answer["seen"], "answers": answer["tries"], "tokens_in": 300, "tokens_out": 20, "seconds": 1.5,
		"memory_read": []string{}, "error": nil,
		"judge": map[string]any{"criteria": map[string]any{"must": []string{"A fox"}, "nice": []string{"Sharp"}}, "keep": 1,
			"mode": "suggest", "verdict": "pass", "first_failed": nil, "picked": []string{clip},
			"takes": []any{map[string]any{"take_id": still, "checks": []bool{false, true}, "reason": "No fox.", "passes": false, "rank": 2},
				map[string]any{"take_id": clip, "checks": []bool{true, false}, "reason": "A fox.", "passes": true, "rank": 1}}}}
	memoryItem := "4e5f6a7b-8c9d-4e0f-9a1b-2c3d4e5f6a70"
	e.pipeline(userA, id, "step", map[string]any{"seq": 1, "row": 1, "attempt": 1, "step": 4, "node": "judge", "part": "verdict",
		"visit": 1, "status": "finished", "what": "Judged 2 takes: 1 passed", "changed": []string{}, "edge": nil, "loops": map[string]int{},
		"run_ids": []string{run.RunID}, "take_ids": []string{}, "error": nil, "checkpoint_id": "ck-4", "decision": decision,
		"memory": map[string]any{"read": []string{memoryItem}, "wrote": []string{}}})
	forged := map[string]any{}
	for k, v := range decision {
		forged[k] = v
	}
	forged["id"] = "00000000-0000-4000-8000-000000000000:1:1:judge:1" // another batch's: dropped
	e.pipeline(userA, id, "step", map[string]any{"seq": 2, "row": 1, "attempt": 1, "step": 5, "node": "judge", "part": "verdict",
		"visit": 1, "status": "finished", "what": "x", "changed": []string{}, "edge": nil, "loops": map[string]int{}, "run_ids": []string{},
		"take_ids": []string{}, "error": nil, "checkpoint_id": "ck-5", "decision": forged})
	judged := map[string]any{"verdict": "pass", "picked": 1, "of": 2, "keep": 1, "first_failed": nil, "suggested": []string{clip},
		"agreed": nil, "takes": []any{}}
	cells := []any{map[string]any{"node": "judge", "kind": "judge", "loop": map[string]any{"turns": 0, "max": 1, "then": "out", "gave_up": false},
		"attempts": []any{map[string]any{"visit": 1, "status": "waiting", "run_ids": []string{run.RunID}, "take_ids": []string{},
			"candidates": []string{still, clip}, "decision_id": decisionID, "judged": judged}}}}
	e.pipeline(userA, id, "row", map[string]any{"seq": 3, "row": 1, "attempt": 1, "status": "waiting", "node": "judge",
		"parked": map[string]any{"gate": "judge", "kind": "judge", "visit": 1, "keep": 1, "candidates": []string{still, clip},
			"suggested": []string{clip}, "decision_id": decisionID, "on_reject": "retry"}, "cells": cells})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 4, "status": "running",
		"memory": map[string]any{"examples": 1, "notes": 0, "taken_at": "2026-11-15T10:00:00Z"}})
	if got := e.call(userA, "GET", path, nil, 200); fmt.Sprint(got["memory"]) != "map[examples:1 notes:0 taken_at:2026-11-15T10:00:00Z]" {
		t.Fatal(got)
	}
	row := e.call(userA, "GET", path+"/rows", nil, 200)["rows"].([]any)[0].(map[string]any)
	cell := row["cells"].([]any)[0].(map[string]any)
	if row["parked"].(map[string]any)["kind"] != "judge" || cell["loop"] == nil ||
		cell["attempts"].([]any)[0].(map[string]any)["decision_id"] != decisionID {
		t.Fatal(row)
	}
	item := e.call(userA, "GET", path+"/review", nil, 200)["items"].([]any)[0].(map[string]any)
	candidates := item["candidates"].([]any)
	if item["kind"] != "judge" || fmt.Sprint(item["suggested"]) != "["+clip+"]" || item["verdict"] != "pass" || item["model"] != "Ask (fake)" ||
		item["decision_id"] != decisionID || candidates[1].(map[string]any)["judge"].(map[string]any)["rank"] != float64(1) ||
		fmt.Sprint(candidates[0].(map[string]any)["judge"].(map[string]any)["checks"]) != "[map[criterion:A fox level:must pass:false] map[criterion:Sharp level:nice pass:true]]" {
		t.Fatal(item)
	}

	// decisions: on the row (the forged one dropped) and on both candidates; another user's are 404
	decisions := e.call(userA, "GET", path+"/rows/1/decisions", nil, 200)["decisions"].([]any)
	shown := decisions[0].(map[string]any)
	seen := shown["seen"].([]any)[0].(map[string]any)
	if len(decisions) != 1 || shown["workflow_title"] != "Ask (fake)" || seen["thumb_url"] == nil || seen["key"] != nil ||
		shown["judge"].(map[string]any)["user_pick"] != nil {
		t.Fatal(decisions)
	}
	e.call(userA, "GET", path+"/rows/1/decisions?attempt=2", nil, 404)
	e.call(userB, "GET", path+"/rows/1/decisions", nil, 404)
	e.call(userB, "GET", "/v1/studio/takes/"+still+"/decisions", nil, 404)
	if list := e.call(userA, "GET", "/v1/studio/takes/"+still+"/decisions", nil, 200)["decisions"].([]any); len(list) != 1 {
		t.Fatal(list)
	}
	steps := e.call(userA, "GET", path+"/rows/1/execution", nil, 200)["steps"].([]any)
	if step := steps[0].(map[string]any); step["decision"].(map[string]any)["id"] != decisionID ||
		fmt.Sprint(step["memory"]) != "map[read:["+memoryItem+"] wrote:[]]" || steps[1].(map[string]any)["decision"] != nil {
		t.Fatal(steps)
	}

	// the user picks the other take with a note: agreed false, recorded on the cell and the decision, sent to pipelines
	picked := e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "judge", "take_ids": []string{still},
		"note": "The fox is there, just small."}, 202)
	attempt := picked["cells"].([]any)[0].(map[string]any)["attempts"].([]any)[0].(map[string]any)
	if attempt["status"] != "picked" || attempt["judged"].(map[string]any)["agreed"] != false {
		t.Fatal(attempt)
	}
	picks := e.commands("cmd.pipeline.pick")
	if pick := data(picks[0]); pick["agreed"] != false || pick["note"] != "The fox is there, just small." || pick["gate"] != "judge" {
		t.Fatal(pick)
	}
	judgeShown := e.call(userA, "GET", path+"/rows/1/decisions", nil, 200)["decisions"].([]any)[0].(map[string]any)["judge"].(map[string]any)
	if judgeShown["agreed"] != false || fmt.Sprint(judgeShown["user_pick"]) != "["+still+"]" {
		t.Fatal(judgeShown)
	}
	outcome := func(take string) string {
		list := e.call(userA, "GET", "/v1/studio/takes/"+take, nil, 200)["decisions"].([]any)
		d := list[0].(map[string]any)
		return fmt.Sprint(d["outcome"], " ", d["first_failed"])
	}
	if a, b := outcome(still), outcome(clip); a != "picked A fox" || b != "passed <nil>" {
		t.Fatal(a, b)
	}

	// Run again: refused until the batch ended, then a new batch with the same snapshot, rows and seeds
	e.call(userA, "POST", path+"/again", map[string]any{"decisions": "same"}, 409)
	e.pipeline(userA, id, "row", map[string]any{"seq": 5, "row": 1, "attempt": 1, "status": "succeeded", "take_ids": []string{still}})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 6, "status": "done"})
	e.call(userB, "POST", path+"/again", map[string]any{"decisions": "same"}, 404)
	e.call(userA, "POST", path+"/again", map[string]any{"decisions": "maybe"}, 422)
	again := e.call(userA, "POST", path+"/again", map[string]any{"decisions": "same"}, 201)
	if again["again_of"] != id || again["same_decisions"] != true || again["title"] != batch["title"].(string)+" again" ||
		again["session_id"] != sessionID || fmt.Sprint(again["models"]) != fmt.Sprint(batch["models"]) {
		t.Fatal(again)
	}
	submit := data(e.commands("cmd.pipeline.submit")[0])
	rows := submit["rows"].([]any)
	first := data(submits[0])["rows"].([]any)
	if submit["replay_from"] != id || submit["batch_id"] != again["id"] || fmt.Sprint(rows) != fmt.Sprint(first) ||
		submit["snapshot"].(map[string]any)["pipeline"].(map[string]any)["nodes"].(map[string]any)["judge"].(map[string]any)["workflow"] != "echo-ask" {
		t.Fatal(submit)
	}
	fresh := e.call(userA, "POST", path+"/again", map[string]any{"decisions": "again", "title": "Fresh eyes"}, 201)
	if fresh["same_decisions"] != false || fresh["title"] != "Fresh eyes" || data(e.commands("cmd.pipeline.submit")[0])["replay_from"] != nil {
		t.Fatal(fresh)
	}

	// memory: the caller's pipeline only, asked in the caller's namespace
	memoryReply = message.MemoryReply{Items: []message.MemoryItem{{ID: memoryItem, Kind: "judge_example", PipelineID: pipelineID,
		Text: "Small foxes are fine.", Kept: []string{still}, Rejected: []string{clip, "9b2f4c1a-6e3d-4a8b-b7c2-1d5e8f0a3b69"},
		Source:    json.RawMessage(`{"kind": "pick", "batch_id": "` + id + `", "row": 1, "node": "judge", "visit": 1}`),
		CreatedAt: "2026-11-15T10:21:00Z", UpdatedAt: "2026-11-15T10:21:00Z"}}}
	list := e.call(userA, "GET", "/v1/studio/memory?pipeline_id="+pipelineID, nil, 200)
	items := list["items"].([]any)
	if len(items) != 1 || len(items[0].(map[string]any)["rejected"].([]any)) != 1 || // a take that is not the caller's left out
		fmt.Sprint(list["counts"]) != "map[judge_example:1 note:0]" || asked[0].OwnerID != userA || asked[0].Op != "list" {
		t.Fatal(list, asked)
	}
	before := len(asked)
	e.call(userB, "GET", "/v1/studio/memory?pipeline_id="+pipelineID, nil, 404) // B cannot reach A's namespace
	e.call(userB, "POST", "/v1/studio/memory", map[string]any{"pipeline_id": pipelineID, "text": "mine"}, 404)
	e.call(userA, "GET", "/v1/studio/memory?pipeline_id="+pipelineID+"&kind=chat", nil, 422)
	if len(asked) != before {
		t.Fatal("asked pipelines for another user's pipeline", asked[before:])
	}
	memoryReply = message.MemoryReply{Item: &memoryReply.Items[0]}
	e.call(userA, "POST", "/v1/studio/memory", map[string]any{"pipeline_id": pipelineID, "text": "  Foxes are red.  ", "key": "briefs"}, 201)
	if last := asked[len(asked)-1]; last.Op != "create" || *last.Text != "Foxes are red." || *last.Key != "briefs" {
		t.Fatal(last)
	}
	e.call(userA, "POST", "/v1/studio/memory", map[string]any{"pipeline_id": pipelineID, "text": "   "}, 422)
	memoryReply = message.MemoryReply{Error: "memory is full (500 items); delete some first", Code: "full"}
	e.call(userA, "POST", "/v1/studio/memory", map[string]any{"pipeline_id": pipelineID, "text": "more"}, 409)
	memoryReply = message.MemoryReply{Error: "memory item not found", Code: "not_found"}
	e.call(userA, "DELETE", "/v1/studio/memory/"+memoryItem, nil, 404)
	before = len(asked)
	e.call(userB, "DELETE", "/v1/studio/memory/"+memoryItem, nil, 404)
	if len(asked) != before { // B has no pipelines: nothing asked
		t.Fatal(asked[before:])
	}
	deleted := 1
	memoryReply = message.MemoryReply{Deleted: &deleted}
	e.call(userA, "DELETE", "/v1/studio/memory/"+memoryItem, nil, 204)
	memoryErr = errors.New("no responders")
	e.call(userA, "GET", "/v1/studio/memory?pipeline_id="+pipelineID, nil, 503)
	memoryErr = nil
	e.exec(`UPDATE batches SET status = 'done' WHERE pipeline_id = $1`, pipelineID)
	e.call(userA, "DELETE", "/v1/studio/pipelines/"+pipelineID, nil, 204)
	if last := asked[len(asked)-1]; last.Op != "clear" || last.PipelineID != pipelineID || last.OwnerID != userA {
		t.Fatal(last)
	}
}

// A note is kept for judge picks only.
func TestGateNoteRefused(t *testing.T) {
	e := setup(t)
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", echoPipeline("Night"), 201)
	batch := e.submit(userA, map[string]any{"pipeline_id": pipeline["id"],
		"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}}}}})
	id := batch["id"].(string)
	take := e.image(userA)
	e.pipeline(userA, id, "row", map[string]any{"seq": 1, "row": 1, "attempt": 1, "status": "waiting", "node": "review",
		"parked": map[string]any{"gate": "review", "visit": 1, "keep": 1, "candidates": []string{take}}, "cells": []any{}})
	refused := e.call(userA, "POST", "/v1/studio/batches/"+id+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{take},
		"note": "why"}, 422)
	if refused["detail"] != "a note is kept for judge picks only" {
		t.Fatal(refused)
	}
}

// sha is a 64-character hex digest of one repeated digit.
func sha(c byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = c
	}
	return string(out)
}

// jsonContains reports whether raw JSON holds the fragment (compact).
func jsonContains(raw []byte, fragment string) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	compact, _ := json.Marshal(v)
	return len(fragment) > 0 && strings.Contains(string(compact), fragment)
}
