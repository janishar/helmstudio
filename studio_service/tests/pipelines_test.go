// Integration tests of saved pipelines (contracts/README.md "Saved pipelines", "Batches of a pipeline", "Execution view",
// "Save as pipeline"): the documents, Submit and its snapshot, studio.runs.create for a step, parking, review and pick,
// cells, step events, the execution view and its state, Retry row from here, delete rules and isolation.
package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
)

// echoPipeline is the demonstration's shape on the echo family: a step, a gate (reject = again, at most twice, then
// give up to the output), a step that edits the pick, the output.
func echoPipeline(title string) map[string]any {
	return map[string]any{"title": title, "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
		"still":  map[string]any{"kind": "step", "workflow": "echo", "inputs": map[string]any{"echo.steps": 2}, "bind": map[string]any{"prompt.text": "$prompt"}},
		"review": map[string]any{"kind": "gate", "title": "Pick one", "takes": "still"},
		"edit":   map[string]any{"kind": "step", "workflow": "echo", "bind": map[string]any{"prompt.text": "$prompt", "echo.refs": "review.picked"}},
		"out":    map[string]any{"kind": "output", "from": "edit.takes", "collection": "Night"},
	}, "edges": []any{[]any{"inputs", "still"}, []any{"still", "review"},
		[]any{"review", map[string]any{"pass": "edit", "fail": "still", "max": 2, "then": "out"}}, []any{"edit", "out"}}}
}

// qwen publishes the torch runner's canonical registry on host mac, live.
func (e *env) qwen() {
	e.t.Helper()
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "qwen-image."+kind+".valid.json"))
		if err == nil {
			err = e.registry.SetRegistry("mac.torch."+kind, data)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "mac", "env": "torch", "state": "idle", "run_id": nil,
		"updated_at": time.Now().UTC(), "published_by": "test"})
	if err := e.registry.SetHost("mac.torch", value); err != nil {
		e.t.Fatal(err)
	}
}

// fixturePipeline reads contracts/fixtures/pipelines/<name>'s pipeline.
func fixturePipeline(t *testing.T, name string) map[string]any {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "pipelines", name))
	var fixture struct {
		Pipeline map[string]any `json:"pipeline"`
	}
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil {
		t.Fatal(err)
	}
	return fixture.Pipeline
}

// commands drains the outbox and returns the envelopes of type typ.
func (e *env) commands(typ string) []gen.Envelope {
	e.t.Helper()
	_, envelopes := e.sent()
	return slices.DeleteFunc(envelopes, func(envelope gen.Envelope) bool { return envelope.Type != typ })
}

func TestPipelineDocuments(t *testing.T) {
	e := setup(t)
	e.qwen()
	path := "/v1/studio/pipelines"
	demo := e.call(userA, "POST", path, fixturePipeline(t, "valid.text-to-edit.json"), 201)
	id := demo["id"].(string)
	if demo["version"] != float64(1) || len(demo["problems"].([]any)) != 0 || demo["based_on"] != nil ||
		demo["workflow_titles"].(map[string]any)["qwen-image"] != "Qwen Image" || demo["last_batch"] != nil {
		t.Fatal(demo)
	}
	// a save-blocking problem is a 422 with every problem; a run-blocking one is saved
	bad := e.call(userA, "POST", path, fixturePipeline(t, "invalid.uncapped-cycle.json"), 422)
	if !strings.HasPrefix(bad["detail"].(string), "pipeline has 3 problems: ") || len(bad["problems"].([]any)) != 3 {
		t.Fatal(bad)
	}
	unreachable := e.call(userA, "POST", path, fixturePipeline(t, "invalid.reach.json"), 201)
	if codes(unreachable["problems"]) == nil {
		t.Fatal(unreachable)
	}
	if v := e.call(userA, "POST", path+"/validate", fixturePipeline(t, "invalid.gate.json"), 200); v["can_save"] != false || v["can_run"] != false {
		t.Fatal(v)
	}
	e.call(userA, "POST", path, map[string]any{"title": "x", "nodes": map[string]any{"a": map[string]any{"kind": "judge"}}, "edges": []any{}}, 422)

	// list, copy, replace (stale is 409), rename
	if list := e.call(userA, "GET", path, nil, 200)["pipelines"].([]any); len(list) != 2 || list[0].(map[string]any)["problems"] != nil {
		t.Fatal(list)
	}
	copied := e.call(userA, "POST", path, map[string]any{"based_on": id}, 201)
	if copied["title"] != "Fox at night copy" || copied["based_on"] != id {
		t.Fatal(copied)
	}
	edited := fixturePipeline(t, "valid.text-to-edit.json")
	edited["version"], edited["title"] = 1, "Fox at dawn"
	if got := e.call(userA, "PUT", path+"/"+id, edited, 200); got["version"] != float64(2) || got["title"] != "Fox at dawn" {
		t.Fatal(got)
	}
	if stale := e.call(userA, "PUT", path+"/"+id, edited, 409); stale["detail"] != "pipeline changed since you opened it (now version 2)" {
		t.Fatal(stale)
	}
	if renamed := e.call(userA, "PATCH", path+"/"+id, map[string]any{"title": "Fox"}, 200); renamed["version"] != float64(3) {
		t.Fatal(renamed)
	}
	// another user's pipeline is a 404 everywhere, and not a source to copy
	e.call(userB, "GET", path+"/"+id, nil, 404)
	edited["version"] = 3
	e.call(userB, "PUT", path+"/"+id, edited, 404)
	e.call(userB, "PATCH", path+"/"+id, map[string]any{"title": "mine"}, 404)
	e.call(userB, "DELETE", path+"/"+id, nil, 404)
	e.call(userB, "POST", path, map[string]any{"based_on": id}, 404)
	if list := e.call(userB, "GET", path, nil, 200)["pipelines"].([]any); len(list) != 0 {
		t.Fatal(list)
	}
	e.call(userA, "DELETE", path+"/"+copied["id"].(string), nil, 204)
	e.call(userA, "DELETE", path+"/"+copied["id"].(string), nil, 404)

	// the cap: 100 per user
	e.exec(`INSERT INTO user_pipelines (id, owner_id, title, nodes, edges)
		SELECT gen_random_uuid(), $1, 'p' || g, '{}', '[]' FROM generate_series(1, 98) g`, userA)
	if full := e.call(userA, "POST", path, map[string]any{"based_on": id}, 409); full["detail"] != "You have 100 pipelines; delete some first." {
		t.Fatal(full)
	}
}

// The demonstration's batch on qwen-image: the snapshot carries every node with its merged inputs and seed param, and
// each row a base seed per step; editing the pipeline later changes nothing of the batch.
func TestPipelineSubmitSnapshot(t *testing.T) {
	e := setup(t)
	e.qwen()
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", fixturePipeline(t, "valid.text-to-edit.json"), 201)
	id := pipeline["id"].(string)
	body := map[string]any{"pipeline_id": id, "inputs": map[string]any{"inputs.edit": "make it night"},
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.prompt", "values": []string{"a fox", "a heron"}}}}
	if bad := e.call(userA, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": id, "rows": body["rows"]}, 422); bad["detail"] != "row 1: inputs.edit: a value is required" {
		t.Fatal(bad)
	}
	if bad := e.call(userB, "POST", "/v1/studio/batches", body, 422); bad["detail"] != "unknown pipeline "+id {
		t.Fatal(bad)
	}
	e.call(userA, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": id, "workflow_id": "echo", "rows": body["rows"]}, 422)
	batch := e.submit(userA, body)
	if batch["pipeline_id"] != id || batch["pipeline_title"] != "Fox at night" || batch["pipeline_version"] != float64(1) ||
		batch["collection"] != "Fox at night" || batch["workflow_id"] != "qwen-image" || batch["title"] != "Fox at night × 2" {
		t.Fatal(batch)
	}
	submits := e.commands("cmd.pipeline.submit")
	var submit struct {
		Snapshot struct {
			WorkflowID string `json:"workflow_id"`
			Pipeline   struct {
				Version   int                       `json:"version"`
				Nodes     map[string]map[string]any `json:"nodes"`
				Workflows map[string]any            `json:"workflows"`
			} `json:"pipeline"`
		} `json:"snapshot"`
		Rows []message.SubmitRow `json:"rows"`
	}
	if len(submits) != 1 || json.Unmarshal(submits[0].Data, &submit) != nil {
		t.Fatal(submits)
	}
	still, edit := submit.Snapshot.Pipeline.Nodes["still"], submit.Snapshot.Pipeline.Nodes["edit"]
	if still["seed_param"] != "seed.seed" || still["seeds"] != float64(4) || edit["seeds"] != float64(1) ||
		edit["inputs"] == nil || submit.Snapshot.Pipeline.Nodes["review"]["keep"] != float64(1) ||
		submit.Snapshot.Pipeline.Workflows["qwen-image"] == nil || submit.Snapshot.WorkflowID != "qwen-image" {
		t.Fatalf("%+v", submit.Snapshot)
	}
	if len(submit.Rows) != 2 || submit.Rows[1].Inputs["inputs.prompt"] != "a heron" || submit.Rows[1].Inputs["inputs.edit"] != "make it night" {
		t.Fatalf("%+v", submit.Rows)
	}
	if _, ok := submit.Rows[0].Seeds["still"]; !ok || len(submit.Rows[0].Seeds) != 2 {
		t.Fatal("a base seed per row and step with a seed param", submit.Rows[0].Seeds)
	}
	// the batch keeps its snapshot after an edit
	edited := fixturePipeline(t, "valid.text-to-edit.json")
	edited["version"], edited["title"] = 1, "Renamed"
	edited["nodes"].(map[string]any)["still"].(map[string]any)["seeds"] = 2
	e.call(userA, "PUT", "/v1/studio/pipelines/"+id, edited, 200)
	execution := e.call(userA, "GET", "/v1/studio/batches/"+batch["id"].(string)+"/rows/1/execution", nil, 200)
	if execution["pipeline"].(map[string]any)["title"] != "Fox at night" ||
		execution["pipeline"].(map[string]any)["nodes"].(map[string]any)["still"].(map[string]any)["seeds"] != float64(4) {
		t.Fatal(execution["pipeline"])
	}
	if got := e.call(userA, "GET", "/v1/studio/batches/"+batch["id"].(string), nil, 200); got["pipeline_title"] != "Fox at night" {
		t.Fatal(got)
	}
	// a pipeline with a run problem cannot run
	broken := e.call(userA, "POST", "/v1/studio/pipelines", fixturePipeline(t, "invalid.reach.json"), 201)
	if bad := e.call(userA, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": broken["id"], "rows": body["rows"]}, 422); !strings.HasPrefix(bad["detail"].(string), "pipeline cannot run: ") {
		t.Fatal(bad)
	}
	// while the batch is unfinished the pipeline stays; once it is done it can go, the batch keeping its snapshot
	e.call(userA, "DELETE", "/v1/studio/pipelines/"+id, nil, 409)
	e.pipeline(userA, batch["id"].(string), "batch", map[string]any{"seq": 1, "status": "done"})
	e.call(userA, "DELETE", "/v1/studio/pipelines/"+id, nil, 204)
	if got := e.call(userA, "GET", "/v1/studio/batches/"+batch["id"].(string), nil, 200); got["pipeline_id"] != id {
		t.Fatal(got)
	}
}

// One echo row through the pipeline: its step's run (made_with.pipeline), parking (waiting row, waiting batch, cells),
// the review grid, a pick, step events, the execution view and its state, Retry row from here; user B sees none of it.
func TestPipelineRowFlow(t *testing.T) {
	e := setup(t)
	var asked []message.StateGet
	reply, replyErr := message.StateReply{CheckpointID: "ck-2", State: map[string]any{"takes": map[string]any{"still": 1}}}, error(nil)
	e.service.SetStateGet(func(_ context.Context, req message.StateGet) (message.StateReply, error) {
		asked = append(asked, req)
		return reply, replyErr
	})
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", echoPipeline("Night"), 201)
	if len(pipeline["problems"].([]any)) != 0 {
		t.Fatal(pipeline)
	}
	batch := e.submit(userA, map[string]any{"pipeline_id": pipeline["id"],
		"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}}}}})
	id, path := batch["id"].(string), "/v1/studio/batches/"+batch["id"].(string)
	sessionID := batch["session_id"].(string)
	e.sent()

	// the step's run, made once per key; a wrong step, seed index or workflow is refused
	ask := func(key string, workflow string, inputs map[string]any) (message.RunsCreated, *message.RunsCreateError) {
		return e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA, SessionID: sessionID,
			WorkflowID: workflow, Inputs: inputs, Lane: "batch", BatchID: id, Row: 1, IdempotencyKey: id + ":1:" + key})
	}
	stepInputs := map[string]any{"prompt.text": "a fox", "echo.steps": float64(2)} // as JSON decodes it
	for key, code := range map[string]string{"1:still:0:1": "invalid", "1:nope:0:0": "invalid", "2:still:0:0": "invalid"} {
		if _, refused := ask(key, "echo", stepInputs); refused == nil || refused.Code != code {
			t.Fatal(key, refused)
		}
	}
	if _, refused := ask("1:still:0:0", "qwen-image", stepInputs); refused == nil || refused.Code != "not_found" {
		t.Fatal(refused)
	}
	run, refused := ask("1:still:0:0", "echo", stepInputs)
	if again, _ := ask("1:still:0:0", "echo", stepInputs); refused != nil || !run.Created || again.RunID != run.RunID || again.Created {
		t.Fatal(run, refused, again)
	}
	e.sent()
	e.emit(userA, sessionID, run.RunID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, run.RunID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + run.RunID + "/1/0.png",
		"kind": "image", "width": 8, "height": 8, "bytes": 10})
	e.emit(userA, sessionID, run.RunID, "status", 3, map[string]any{"status": "succeeded"})
	takes := e.call(userA, "GET", "/v1/studio/takes?source=pipeline", nil, 200)["takes"].([]any)
	if len(takes) != 1 {
		t.Fatal(takes)
	}
	take := takes[0].(map[string]any)
	takeID := take["id"].(string)
	made := take["made_with"].(map[string]any)["pipeline"].(map[string]any)
	if made["id"] != pipeline["id"] || made["node"] != "still" || made["attempt"] != float64(1) || made["row"] != float64(1) ||
		made["title"] != "Night" || made["version"] != float64(1) {
		t.Fatal(made)
	}

	// the row parks on the gate: waiting, its cells, a waiting batch
	cells := []any{
		map[string]any{"node": "still", "kind": "step", "attempts": []any{map[string]any{"visit": 1, "status": "succeeded",
			"run_ids": []string{run.RunID}, "take_ids": []string{takeID}, "seeds": []int{}}}},
		map[string]any{"node": "review", "kind": "gate", "attempts": []any{map[string]any{"visit": 1, "status": "waiting",
			"run_ids": []string{}, "take_ids": []string{}, "candidates": []string{takeID}}}},
	}
	e.pipeline(userA, id, "batch", map[string]any{"seq": 1, "status": "running"})
	e.pipeline(userA, id, "row", map[string]any{"seq": 2, "row": 1, "attempt": 1, "status": "waiting", "run_id": run.RunID,
		"node": "review", "parked": map[string]any{"gate": "review", "visit": 1, "keep": 1, "candidates": []string{takeID}}, "cells": cells})
	got := e.call(userA, "GET", path, nil, 200)
	if got["status"] != "waiting" || got["counts"].(map[string]any)["waiting"] != float64(1) {
		t.Fatal(got)
	}
	row := e.call(userA, "GET", path+"/rows", nil, 200)["rows"].([]any)[0].(map[string]any)
	shown := row["cells"].([]any)[0].(map[string]any)["attempts"].([]any)[0].(map[string]any)
	if row["status"] != "waiting" || row["node"] != "review" || row["parked"].(map[string]any)["gate"] != "review" ||
		shown["thumb_url"] == nil || shown["seconds"] == nil {
		t.Fatal(row)
	}
	review := e.call(userA, "GET", path+"/review", nil, 200)["items"].([]any)
	item := review[0].(map[string]any)
	if len(review) != 1 || item["on_reject"] != "retry" || item["rejects_left"] != float64(2) || item["gate_title"] != "Pick one" ||
		item["candidates"].([]any)[0].(map[string]any)["take_id"] != takeID {
		t.Fatal(review)
	}

	// steps: the execution view and a step's state
	for i, step := range []map[string]any{
		{"node": "inputs", "part": "inputs", "status": "finished", "edge": map[string]any{"to": "still", "via": "next"}, "checkpoint_id": "ck-1"},
		{"node": "still", "part": "submit", "status": "finished", "run_ids": []string{run.RunID, utility.NewID()}, "checkpoint_id": "ck-2"},
		{"node": "still", "part": "wait", "status": "resumed", "take_ids": []string{takeID}, "edge": map[string]any{"to": "review", "via": "next"}, "checkpoint_id": "ck-3"},
		{"node": "review", "part": "gate", "status": "current", "checkpoint_id": nil},
	} {
		step["seq"], step["row"], step["attempt"], step["step"], step["visit"] = 10+i, 1, 1, i+1, 1
		step["what"], step["changed"], step["loops"] = "did "+step["part"].(string), []string{}, map[string]any{"review": 0}
		e.pipeline(userA, id, "step", step)
	}
	e.pipeline(userA, id, "step", map[string]any{"seq": 9, "row": 1, "attempt": 1, "step": 1, "node": "inputs", "part": "inputs",
		"visit": 1, "status": "finished", "what": "older", "changed": []string{}, "loops": map[string]any{}, "checkpoint_id": nil}) // older: ignored
	execution := e.call(userA, "GET", path+"/rows/1/execution", nil, 200)
	steps := execution["steps"].([]any)
	if len(steps) != 4 || steps[0].(map[string]any)["has_state"] != true || steps[3].(map[string]any)["has_state"] != false ||
		len(steps[1].(map[string]any)["run_ids"].([]any)) != 1 || execution["current"] != "review" ||
		execution["nodes"].(map[string]any)["review"].(map[string]any)["status"] != "waiting" || len(execution["edges"].([]any)) != 2 {
		t.Fatal(execution)
	}
	state := e.call(userA, "GET", path+"/rows/1/execution/2/state", nil, 200)
	if state["node"] != "still" || state["state"].(map[string]any)["takes"] == nil || len(asked) != 1 ||
		asked[0].OwnerID != userA || asked[0].CheckpointID != "ck-2" || asked[0].Row != 1 {
		t.Fatal(state, asked)
	}
	e.call(userA, "GET", path+"/rows/1/execution/4/state", nil, 404) // current: no checkpoint
	e.call(userA, "GET", path+"/rows/1/execution/x/state", nil, 404)
	e.call(userA, "GET", path+"/rows/1/execution?attempt=2", nil, 404)
	reply = message.StateReply{Error: "gone", Code: "not_found"}
	e.call(userA, "GET", path+"/rows/1/execution/2/state", nil, 404)
	replyErr = errors.New("timeout")
	e.call(userA, "GET", path+"/rows/1/execution/2/state", nil, 503)

	// user B: 404 on all of it
	for _, call := range [][2]string{{"GET", "/review"}, {"GET", "/rows/1/execution"}, {"GET", "/rows/1/execution/2/state"}} {
		e.call(userB, call[0], path+call[1], nil, 404)
	}
	e.call(userB, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{takeID}}, 404)
	e.call(userB, "POST", path+"/rows/1/retry", map[string]any{"from_step": 2}, 404)

	// the pick: the wrong gate or row is 409, a take not offered 422; then recorded, sent once
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "still", "take_ids": []string{takeID}}, 409)
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{utility.NewID()}}, 422)
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{takeID, takeID}}, 422)
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review"}, 422)
	picked := e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{takeID}}, 202)
	gate := picked["cells"].([]any)[1].(map[string]any)["attempts"].([]any)[0].(map[string]any)
	if picked["status"] != "running" || picked["parked"] != nil || gate["status"] != "picked" {
		t.Fatal(picked)
	}
	picks := e.commands("cmd.pipeline.pick")
	if len(picks) != 1 || data(picks[0])["gate"] != "review" || data(picks[0])["visit"] != float64(1) ||
		fmt.Sprint(data(picks[0])["take_ids"]) != "["+takeID+"]" {
		t.Fatal(picks)
	}
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "take_ids": []string{takeID}}, 202)
	if again := e.commands("cmd.pipeline.pick"); len(again) != 0 {
		t.Fatal("the same pick again sends nothing", again)
	}
	e.call(userA, "POST", path+"/rows/1/pick", map[string]any{"gate": "review", "reject": true}, 409)
	// a waiting republished for the visit already picked stays running
	e.pipeline(userA, id, "row", map[string]any{"seq": 20, "row": 1, "attempt": 1, "status": "waiting", "run_id": run.RunID,
		"node": "review", "parked": map[string]any{"gate": "review", "visit": 1, "keep": 1, "candidates": []string{takeID}}})
	if row := e.call(userA, "GET", path+"/rows", nil, 200)["rows"].([]any)[0].(map[string]any); row["status"] != "running" {
		t.Fatal(row)
	}

	// Retry row from here: a step without state is 422, a running row 409; a failed row re-runs from step 3
	e.call(userA, "POST", path+"/rows/1/retry", map[string]any{"from_step": 3}, 409)
	e.pipeline(userA, id, "row", map[string]any{"seq": 21, "row": 1, "attempt": 1, "status": "failed", "error": "boom"})
	e.call(userA, "POST", path+"/rows/1/retry", map[string]any{"from_step": 9}, 422)
	e.call(userA, "POST", path+"/rows/1/retry", map[string]any{"from_step": 0}, 422)
	retried := e.call(userA, "POST", path+"/rows/1/retry", map[string]any{"from_step": 3}, 202)
	if retried["attempt"] != float64(2) || retried["status"] != "queued" || len(retried["cells"].([]any)) != 0 {
		t.Fatal(retried)
	}
	retries := e.commands("cmd.pipeline.retry")
	if len(retries) != 1 || data(retries[0])["from_step"] != float64(3) || data(retries[0])["checkpoint_id"] != "ck-2" ||
		data(retries[0])["attempt"] != float64(2) {
		t.Fatal(retries)
	}
	second := e.call(userA, "GET", path+"/rows/1/execution", nil, 200)
	if second["attempt"] != float64(2) || len(second["steps"].([]any)) != 2 ||
		fmt.Sprint(second["retried_from"]) != "map[attempt:1 step:3]" {
		t.Fatal(second)
	}
	if first := e.call(userA, "GET", path+"/rows/1/execution?attempt=1", nil, 200); len(first["steps"].([]any)) != 4 || first["status"] != "cancelled" {
		t.Fatal(first)
	}
	// once the batch has finished, its checkpoints are gone
	e.pipeline(userA, id, "row", map[string]any{"seq": 22, "row": 1, "attempt": 2, "status": "failed", "error": "boom"})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 23, "status": "done"})
	if done := e.call(userA, "POST", path+"/rows/1/retry", map[string]any{"from_step": 2}, 409); done["detail"] != "this batch has finished; its checkpoints are gone" {
		t.Fatal(done)
	}
	e.call(userA, "GET", path+"/rows/1/execution/1/state", nil, 404)
}

// Save as pipeline: a chain of two runs (the second edits the first's take with an upload beside it) becomes inputs →
// step_1 → step_2 → output; an upload, several run parents and another user's take are refused.
func TestSaveTakeAsPipeline(t *testing.T) {
	e := setup(t)
	upload := e.image(userA)
	madeWith := func(prompt string, refs ...string) string {
		data, _ := json.Marshal(map[string]any{"workflow_id": "echo", "workflow_title": "Echo", "prompt": prompt, "seed": nil,
			"model": nil, "inputs": map[string]any{"prompt.text": prompt, "echo.steps": 3, "echo.delay_ms": 0, "echo.fail": false,
				"echo.refs": refsOf(refs...)}})
		return string(data)
	}
	take := func(title, made string, parents ...string) string {
		id := utility.NewID()
		e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, made_with)
			VALUES ($1, $2, 'image', 'create', $3, $4, 'image/png', 10, true, $5)`, id, userA, title, "u/"+userA+"/runs/x/"+id+".png", made)
		for i, parent := range parents {
			e.exec(`INSERT INTO take_parents (take_id, parent_id, owner_id, idx) VALUES ($1, $2, $3, $4)`, id, parent, userA, i)
		}
		return id
	}
	first := take("a fox", madeWith("a fox"))
	second := take("at night", madeWith("at night", first), first)
	saved := e.call(userA, "POST", "/v1/studio/takes/"+second+"/pipeline", nil, 201)
	nodes := saved["nodes"].(map[string]any)
	step1, step2 := nodes["step_1"].(map[string]any), nodes["step_2"].(map[string]any)
	if saved["title"] != "at night pipeline" || len(saved["problems"].([]any)) != 0 ||
		step1["bind"].(map[string]any)["prompt.text"] != "$prompt" || step2["bind"].(map[string]any)["echo.refs"] != "step_1.takes" ||
		step2["inputs"].(map[string]any)["prompt.text"] != "at night" || nodes["output"].(map[string]any)["from"] != "step_2.takes" ||
		len(saved["edges"].([]any)) != 3 {
		t.Fatal(saved)
	}
	withUpload := take("with a ref", madeWith("again", upload), upload)
	titled := e.call(userA, "POST", "/v1/studio/takes/"+withUpload+"/pipeline", map[string]any{"title": "Mine"}, 201)
	columns := titled["nodes"].(map[string]any)["inputs"].(map[string]any)["inputs"].([]any)
	if titled["title"] != "Mine" || len(columns) != 2 || columns[1].(map[string]any)["type"] != "Image" ||
		titled["nodes"].(map[string]any)["step_1"].(map[string]any)["bind"].(map[string]any)["echo.refs"] != "$image" {
		t.Fatal(titled)
	}
	if bad := e.call(userA, "POST", "/v1/studio/takes/"+upload+"/pipeline", nil, 422); bad["detail"] != "an upload has no lineage to save" {
		t.Fatal(bad)
	}
	twoParents := take("both", madeWith("both", first, second), first, second)
	if bad := e.call(userA, "POST", "/v1/studio/takes/"+twoParents+"/pipeline", nil, 422); !strings.Contains(bad["detail"].(string), "several runs' takes") {
		t.Fatal(bad)
	}
	e.call(userA, "DELETE", "/v1/studio/takes/"+first, nil, 204)
	if bad := e.call(userA, "POST", "/v1/studio/takes/"+second+"/pipeline", nil, 422); bad["detail"] != "take "+first+" in the lineage was deleted" {
		t.Fatal(bad)
	}
	e.call(userB, "POST", "/v1/studio/takes/"+second+"/pipeline", nil, 404)
}

// data is an envelope's data.
func data(envelope gen.Envelope) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(envelope.Data, &out)
	return out
}

// refsOf is an echo.refs value of the takes.
func refsOf(ids ...string) []any {
	if len(ids) == 0 {
		return []any{}
	}
	return refs(ids...)
}
