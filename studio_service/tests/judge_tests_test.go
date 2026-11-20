// Integration test of judge tests (contracts/README.md "Judge test", API 1.28): the saved judge's ask run on picked
// takes (the template from pipelines, the takes as media, thumbnails counted, the caller's newest session when the
// takes have none), the verdict asked once the run succeeded and kept, a failed answer check, and isolation.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestJudgeTests(t *testing.T) {
	e := setup(t)
	var asked []message.JudgeTestRequest
	e.service.SetJudgeTest(func(_ context.Context, req message.JudgeTestRequest) (message.JudgeTestReply, error) {
		asked = append(asked, req)
		if req.Op == "prompt" {
			return message.JudgeTestReply{System: "sys", Prompt: "ECHO-ANSWER: {}", Schema: `{"type":"object"}`, MaxTokens: 800}, nil
		}
		if *req.Text == "bad" {
			return message.JudgeTestReply{Error: "takes must be 1..2 in order", Code: "answer"}, nil
		}
		return message.JudgeTestReply{Verdict: "pass", Picked: req.TakeIDs[1:], Takes: []message.JudgeTestTake{
			{TakeID: req.TakeIDs[0], Checks: []bool{false, true}, Reason: "no fox", Rank: 2},
			{TakeID: req.TakeIDs[1], Checks: []bool{true, true}, Reason: "fine", Passes: true, Rank: 1}}}, nil
	})
	pipeline := judgePipeline("Foxes")
	pipeline["nodes"].(map[string]any)["mem"] = map[string]any{"kind": "memory", "op": "read", "key": "briefs", "limit": 4}
	pipeline["nodes"].(map[string]any)["judge"].(map[string]any)["memory"] = "mem"
	pipeline["edges"] = append([]any{[]any{"inputs", "mem"}, []any{"mem", "still"}}, pipeline["edges"].([]any)[1:]...)
	pipelineID := e.call(userA, "POST", "/v1/studio/pipelines", pipeline, 201)["id"].(string)
	tests := "/v1/studio/pipelines/" + pipelineID + "/judge-tests"
	still, clip := e.image(userA), e.image(userA, "video")
	body := map[string]any{"node": "judge", "take_ids": []string{still, clip}}

	e.call(userA, "POST", tests, body, 422) // no session yet: a test run needs one
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	e.call(userB, "POST", tests, body, 404)                                                                  // another user's pipeline
	e.call(userA, "POST", tests, map[string]any{"node": "judge", "take_ids": []string{e.image(userB)}}, 404) // another user's take
	e.call(userA, "POST", tests, map[string]any{"node": "still", "take_ids": []string{still}}, 422)          // not a judge
	e.call(userA, "POST", tests, map[string]any{"node": "judge", "take_ids": []string{still, still}}, 422)   // twice
	e.call(userA, "POST", tests, map[string]any{"node": "judge", "take_ids": []string{e.image(userA, "audio")}}, 422)
	asked = nil

	test := e.call(userA, "POST", tests, body, 202)
	runID, _ := test["run_id"].(string)
	if test["status"] != "queued" || test["verdict"] != nil || len(test["takes"].([]any)) != 0 || runID == "" {
		t.Fatal(test)
	}
	if len(asked) != 1 || asked[0].N != 2 || asked[0].Node != "judge" || asked[0].PipelineID != pipelineID ||
		asked[0].Memory == nil || asked[0].Memory.Limit != 4 || *asked[0].Memory.Key != "briefs" ||
		string(asked[0].Criteria) != `{"must":["A fox"],"nice":["Sharp"]}` {
		t.Fatalf("%+v", asked)
	}
	if e.count(`SELECT count(*) FROM runs WHERE id = $1 AND thumbs = 4 AND workflow_id = 'echo-ask' AND session_id = $2
		AND inputs->>'ask.prompt' = 'ECHO-ANSWER: {}' AND jsonb_array_length(inputs->'ask.media') = 2`, runID, session) != 1 {
		t.Fatal("the ask run: the template, the takes as media, an image and a video counted as 4 thumbnails")
	}
	path := "/v1/studio/judge-tests/" + test["id"].(string)
	e.call(userB, "GET", path, nil, 404)
	e.call(userA, "GET", "/v1/studio/judge-tests/nope", nil, 404)

	e.sent()
	e.emit(userA, session, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	if got := e.call(userA, "GET", path, nil, 200); got["status"] != "running" || len(asked) != 1 {
		t.Fatal(got)
	}
	answer := map[string]any{"tries": []any{map[string]any{"text": "good", "error": nil}}, "seen": []any{},
		"tokens_in": 1, "tokens_out": 1, "seconds": 0.1}
	e.emit(userA, session, runID, "status", 2, map[string]any{"status": "succeeded", "answer": answer})
	got := e.call(userA, "GET", path, nil, 200)
	takes := got["takes"].([]any)
	if got["status"] != "succeeded" || got["verdict"] != "pass" || len(takes) != 2 || len(asked) != 2 ||
		*asked[1].Text != "good" || asked[1].Keep != 1 || fmt.Sprint(asked[1].TakeIDs) != fmt.Sprint([]string{still, clip}) {
		t.Fatal(got, asked)
	}
	first, _ := json.Marshal(takes[0])
	if string(first) != `{"checks":[{"criterion":"A fox","level":"must","pass":false},{"criterion":"Sharp","level":"nice","pass":true}],`+
		`"passes":false,"picked":false,"rank":2,"reason":"no fox","take_id":"`+still+`"}` || takes[1].(map[string]any)["picked"] != true {
		t.Fatalf("%s", first)
	}
	if again := e.call(userA, "GET", path, nil, 200); fmt.Sprint(again) != fmt.Sprint(got) || len(asked) != 2 {
		t.Fatal("the verdict is kept: pipelines is asked once")
	}

	// an answer that fails the checks: the test failed with that error
	bad := e.call(userA, "POST", tests, map[string]any{"node": "judge", "take_ids": []string{clip}}, 202)
	e.sent()
	answer["tries"] = []any{map[string]any{"text": "bad", "error": nil}}
	e.emit(userA, session, bad["run_id"].(string), "status", 1, map[string]any{"status": "succeeded", "answer": answer})
	if got := e.call(userA, "GET", "/v1/studio/judge-tests/"+bad["id"].(string), nil, 200); got["status"] != "failed" ||
		got["error"] != "takes must be 1..2 in order" || got["verdict"] != nil {
		t.Fatal(got)
	}

	// no answer from pipelines: 503, nothing made
	e.service.SetJudgeTest(nil)
	e.call(userA, "POST", tests, body, 503)
	if e.count(`SELECT count(*) FROM judge_tests`) != 2 {
		t.Fatal("a test without its template")
	}
}
