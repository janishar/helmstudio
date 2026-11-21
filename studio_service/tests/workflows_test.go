package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
)

// echoDoc is the echo template as a document of one's own, with positions and a Studio block.
func echoDoc(title string, steps int) map[string]any {
	return map[string]any{"title": title, "family": "echo", "env": "fake", "nodes": map[string]any{
		"prompt": map[string]any{"kind": "prompt", "params": map[string]any{"text": ""}, "position": map[string]any{"x": 0, "y": 40}},
		"echo": map[string]any{"kind": "echo/echo", "in": map[string]any{"text": "prompt.text"},
			"params": map[string]any{"steps": steps}, "position": map[string]any{"x": 280, "y": 40}},
		"output": map[string]any{"kind": "output", "in": map[string]any{"media": "echo.image"}},
	}, "studio": map[string]any{"composer": []any{"prompt.text"}, "quick": []any{"echo.steps"}}}
}

// with returns body with extra keys.
func with(body map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// codes lists a problems array's codes.
func codes(problems any) []string {
	var out []string
	for _, p := range problems.([]any) {
		out = append(out, p.(map[string]any)["code"].(string))
	}
	return out
}

// Create, copy, read, list, replace (with the version check), rename, validate and delete; another user gets 404.
func TestUserWorkflowCRUD(t *testing.T) {
	e := setup(t)
	path := "/v1/studio/workflows"
	mine := e.call(userA, "POST", path, echoDoc(" Mine ", 4), 201)
	id := mine["id"].(string)
	if !utility.IsUUID(id) || mine["title"] != "Mine" || mine["source"] != "mine" || mine["version"] != float64(1) ||
		mine["based_on"] != nil || len(mine["problems"].([]any)) != 0 || mine["node_schemas"].(map[string]any)["echo/echo"] == nil {
		t.Fatal(mine)
	}
	if _, has := mine["based_on"]; !has {
		t.Fatal("based_on is null, not absent, for one's own")
	}
	// copies: of a template (title "<title> copy") and of one's own
	copied := e.call(userA, "POST", path, map[string]any{"based_on": "echo"}, 201)
	if copied["title"] != "Echo copy" || copied["based_on"] != "echo" || copied["family"] != "echo" {
		t.Fatal(copied)
	}
	if again := e.call(userA, "POST", path, map[string]any{"based_on": id, "title": "Two"}, 201); again["title"] != "Two" ||
		again["studio"] == nil || again["nodes"].(map[string]any)["echo"].(map[string]any)["position"] == nil {
		t.Fatal(again)
	}
	e.call(userA, "POST", path, map[string]any{"based_on": "nope"}, 404)
	e.call(userA, "POST", path, with(echoDoc("x", 4), map[string]any{"extra": 1}), 422)
	e.call(userA, "POST", path, with(echoDoc("x", 4), map[string]any{"based_on": "nope"}), 404)

	// list: templates by title (Echo, Echo audio to video, Echo dialogue, Echo extend, Echo image to video, Echo retake,
	// Echo tiny, Echo video, Echo voice-over), then mine newest first; never problems
	list := e.call(userA, "GET", path, nil, 200)["workflows"].([]any)
	if len(list) != 12 || list[8].(map[string]any)["source"] != "template" || list[9].(map[string]any)["title"] != "Two" {
		t.Fatal(list)
	}
	for _, item := range list {
		if _, has := item.(map[string]any)["problems"]; has {
			t.Fatal("problems in a list")
		}
	}
	if only := e.call(userA, "GET", path+"?source=template", nil, 200)["workflows"].([]any); len(only) != 9 {
		t.Fatal(only)
	}
	e.call(userA, "GET", path+"?source=other", nil, 422)
	if got := e.call(userA, "GET", path+"/echo", nil, 200); got["source"] != "template" || len(got["problems"].([]any)) != 0 {
		t.Fatal(got)
	}

	// isolation: B cannot read, copy, change or delete A's
	e.call(userB, "GET", path+"/"+id, nil, 404)
	e.call(userB, "POST", path, map[string]any{"based_on": id}, 404)
	e.call(userB, "PUT", path+"/"+id, with(echoDoc("B", 4), map[string]any{"version": 1}), 404)
	e.call(userB, "PATCH", path+"/"+id, map[string]any{"title": "B"}, 404)
	e.call(userB, "DELETE", path+"/"+id, nil, 404)
	if list := e.call(userB, "GET", path+"?source=mine", nil, 200)["workflows"].([]any); len(list) != 0 {
		t.Fatal(list)
	}

	// replace: version + 1; an editor holding the old version gets 409
	replaced := e.call(userA, "PUT", path+"/"+id, with(echoDoc("Mine", 6), map[string]any{"version": 1}), 200)
	if replaced["version"] != float64(2) {
		t.Fatal(replaced)
	}
	if stale := e.call(userA, "PUT", path+"/"+id, with(echoDoc("Mine", 7), map[string]any{"version": 1}), 409); stale["detail"] != "workflow changed since you opened it (now version 2)" {
		t.Fatal(stale)
	}
	if renamed := e.call(userA, "PATCH", path+"/"+id, map[string]any{"title": " Renamed "}, 200); renamed["title"] != "Renamed" || renamed["version"] != float64(3) {
		t.Fatal(renamed)
	}
	e.call(userA, "PUT", path+"/"+id, with(echoDoc("Mine", 7), map[string]any{"version": 2}), 409)
	e.call(userA, "PATCH", path+"/"+id, map[string]any{"title": " "}, 422)
	e.call(userA, "PUT", path+"/echo", with(echoDoc("T", 4), map[string]any{"version": 1}), 409)
	e.call(userA, "PATCH", path+"/echo", map[string]any{"title": "T"}, 409)
	e.call(userA, "DELETE", path+"/echo", nil, 409)

	// save-blocking problems: 422 with every problem; run-only ones are saved and returned
	broken := echoDoc("Broken", 0) // steps 0 is param_invalid
	delete(broken["nodes"].(map[string]any), "output")
	bad := e.call(userA, "POST", path, broken, 422)
	if got := codes(bad["problems"]); strings.Join(got, ",") != "no_output,param_invalid" || !strings.HasPrefix(bad["detail"].(string), "workflow has 2 problems: echo.steps") {
		t.Fatal(bad)
	}
	noOutput := echoDoc("No output", 3)
	delete(noOutput["nodes"].(map[string]any), "output")
	saved := e.call(userA, "POST", path, noOutput, 201)
	if got := codes(saved["problems"]); len(got) != 1 || got[0] != "no_output" {
		t.Fatal(saved)
	}
	check := e.call(userA, "POST", path+"/validate", with(noOutput, map[string]any{"version": 9}), 200)
	if check["can_save"] != true || check["can_run"] != false {
		t.Fatal(check)
	}
	e.call(userA, "POST", path+"/validate", map[string]any{"title": "x"}, 422)

	// the palette: core first, then the echo family
	palette := e.call(userA, "GET", "/v1/studio/nodes?env=fake", nil, 200)["groups"].([]any)
	if len(palette) != 2 || palette[0].(map[string]any)["title"] != "Core" || palette[1].(map[string]any)["family"] != "echo" {
		t.Fatal(palette)
	}
	if none := e.call(userA, "GET", "/v1/studio/nodes?env=torch", nil, 200)["groups"].([]any); len(none) != 0 {
		t.Fatal(none)
	}
	e.call(userA, "GET", "/v1/studio/nodes?env=BAD", nil, 422)

	e.call(userA, "DELETE", path+"/"+id, nil, 204)
	e.call(userA, "DELETE", path+"/"+id, nil, 404)
}

// At most 200 workflows per user.
func TestUserWorkflowCap(t *testing.T) {
	e := setup(t)
	e.sql(`INSERT INTO user_workflows (id, owner_id, title, family, env, nodes)
		SELECT gen_random_uuid(), $1, 'w' || n, 'echo', 'fake', '{}' FROM generate_series(1, 200) n`, userA)
	if full := e.call(userA, "POST", "/v1/studio/workflows", echoDoc("one more", 4), 409); full["detail"] != "You have 200 workflows; delete some first." {
		t.Fatal(full)
	}
	e.call(userB, "POST", "/v1/studio/workflows", echoDoc("B's first", 4), 201)
}

// A run of a user workflow sends its snapshot: later edits do not change it, the command has no position or bypass,
// the workflow cannot be deleted while it is open, and reuse reports the workflow changed, then deleted. Another user
// cannot run or batch it.
func TestUserWorkflowRuns(t *testing.T) {
	e := setup(t)
	mine := e.call(userA, "POST", "/v1/studio/workflows", echoDoc("Mine", 4), 201)
	id := mine["id"].(string)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"

	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	if denied := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": id}, 422); denied["detail"] != "unknown workflow "+id {
		t.Fatal(denied)
	}
	e.call(userB, "POST", "/v1/studio/batches", with(lines("a"), map[string]any{"workflow_id": id}), 422)

	if wired := e.call(userA, "POST", runs, map[string]any{"workflow_id": id, "inputs": map[string]any{"echo.text": "x"}}, 422); wired["detail"] != "echo.text: not a param of this workflow" {
		t.Fatal(wired)
	}
	if bypass := e.call(userA, "POST", runs, map[string]any{"workflow_id": id, "inputs": map[string]any{"echo.bypass": true}}, 422); bypass["detail"] != "echo.bypass: echo cannot be bypassed" {
		t.Fatal(bypass)
	}
	run := e.call(userA, "POST", runs, map[string]any{"workflow_id": id, "inputs": map[string]any{"prompt.text": "a fox"}}, 202)
	if run["workflow_version"] != float64(1) || run["status"] != "queued" || run["host"] != "fake" {
		t.Fatal(run)
	}
	runID := run["id"].(string)

	// edited after the run was made: the run still sends version 1's graph
	e.call(userA, "PUT", "/v1/studio/workflows/"+id, with(echoDoc("Mine v2", 9), map[string]any{"version": 1}), 200)
	if busy := e.call(userA, "DELETE", "/v1/studio/workflows/"+id, nil, 409); busy["detail"] != "a run of this workflow is unfinished; cancel it or wait" {
		t.Fatal(busy)
	}
	_, sent := e.relay()
	if len(sent) != 1 || strings.Contains(string(sent[0].Data), "position") || strings.Contains(string(sent[0].Data), "bypass") {
		t.Fatalf("%v", sent)
	}
	var command struct {
		Workflow struct {
			ID    string
			Nodes map[string]struct{ Params map[string]any }
		}
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	if command.Workflow.ID != id || command.Workflow.Nodes["echo"].Params["steps"] != float64(4) ||
		command.Workflow.Nodes["echo"].Params["delay_ms"] != float64(300) {
		t.Fatalf("%s", sent[0].Data)
	}
	key := fmt.Sprintf("u/%s/runs/%s/1/0.png", userA, runID)
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": key, "kind": "image", "width": 8, "height": 8, "bytes": 5})
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded"})
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	take := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)
	made := take["made_with"].(map[string]any)
	if made["workflow_version"] != float64(1) || made["workflow_title"] != "Mine" || made["inputs"].(map[string]any)["echo.steps"] != float64(4) {
		t.Fatal(made)
	}
	reuse := e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/reuse", nil, 200)
	if reuse["workflow_status"] != "changed" || reuse["inputs"].(map[string]any)["prompt.text"] != "a fox" {
		t.Fatal(reuse)
	}
	e.call(userA, "DELETE", "/v1/studio/workflows/"+id, nil, 204)
	if reuse := e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/reuse", nil, 200); reuse["workflow_status"] != "deleted" {
		t.Fatal(reuse)
	}
	if gone := e.call(userA, "POST", runs, map[string]any{"workflow_id": id}, 422); gone["detail"] != "unknown workflow "+id {
		t.Fatal(gone)
	}
	if template := e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/reuse", nil, 200); template["workflow_id"] != id {
		t.Fatal(template)
	}
}

// A workflow that cannot run is refused at run creation with its first problem; a batch snapshots one that can.
func TestUserWorkflowCannotRunAndBatch(t *testing.T) {
	e := setup(t)
	noOutput := echoDoc("No output", 3)
	delete(noOutput["nodes"].(map[string]any), "output")
	id := e.call(userA, "POST", "/v1/studio/workflows", noOutput, 201)["id"].(string)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	if refused := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": id}, 422); refused["detail"] != "workflow cannot run: the workflow has no output node" {
		t.Fatal(refused)
	}
	e.call(userA, "POST", "/v1/studio/batches", with(lines("a"), map[string]any{"workflow_id": id}), 422)

	ok := e.call(userA, "POST", "/v1/studio/workflows", echoDoc("Batchable", 5), 201)["id"].(string)
	batch := e.submit(userA, map[string]any{"workflow_id": ok, "rows": lines("a", "b")["rows"]})
	if batch["workflow_id"] != ok || batch["workflow_title"] != "Batchable" {
		t.Fatal(batch)
	}
	if busy := e.call(userA, "DELETE", "/v1/studio/workflows/"+ok, nil, 409); busy["detail"] != "a batch of this workflow is unfinished; stop it or wait" {
		t.Fatal(busy)
	}
	_, sent := e.sent()
	if len(sent) != 1 || strings.Contains(string(sent[0].Data), "position") || !strings.Contains(string(sent[0].Data), `"steps": 5`) {
		t.Fatalf("%v", sent)
	}
	// edited after Submit: the batch's row run still resolves the snapshot
	e.call(userA, "PUT", "/v1/studio/workflows/"+ok, with(echoDoc("Batchable", 8), map[string]any{"version": 1}), 200)
	created, refused := e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: batch["session_id"].(string), WorkflowID: ok, Lane: "batch", BatchID: batch["id"].(string), Row: 1,
		IdempotencyKey: batch["id"].(string) + ":1:1:step:0:0"})
	if refused != nil || !created.Created {
		t.Fatal(created, refused)
	}
	if n := e.count(`SELECT count(*) FROM runs WHERE id = $1 AND (nodes->'echo'->'params'->>'steps')::int = 5
		AND workflow_version = 1 AND workflow_title = 'Batchable'`, created.RunID); n != 1 {
		t.Fatal("the batch run does not run the snapshot")
	}
}
