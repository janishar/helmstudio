// Integration tests of plan 21 (contracts/README.md "Deleting runs", "Sessions: titles and naming", API 1.43): run
// delete per status, who wrote a title, the session-names setting, cmd.pipeline.name once per session, the hidden
// naming run and evt.pipeline.<session>.named applied only to a title nobody typed.
package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
)

// namer is a projector that names sessions (the env's has no registry), with its own handlers.
func (e *env) namer() *micro.NatsJetStreamGroup {
	p := studio.NewProjector(e.pool)
	p.Workflows, p.RemoteOn = e.registry, true
	group := &micro.NatsJetStreamGroup{}
	p.MountNatsJetStream(group)
	p.MountPipeline(group)
	return group
}

// emitTo is emit through group.
func (e *env) emitTo(group *micro.NatsJetStreamGroup, owner, sessionID, runID, kind string, seq int, data map[string]any) {
	e.t.Helper()
	data["seq"] = seq
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.run." + kind, Version: 1, ID: utility.NewID(), JobID: &runID, OwnerID: owner,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := group.Handle(context.Background(), "evt.run.fake."+sessionID+"."+kind, body); err != nil {
		e.t.Fatal(err)
	}
}

// named sends evt.pipeline.<session>.named as pipelines would.
func (e *env) named(group *micro.NatsJetStreamGroup, owner, sessionID, runID, title string) {
	e.t.Helper()
	raw, _ := json.Marshal(message.Named{SessionID: sessionID, RunID: runID, Title: title, NamedBy: "Ollama"})
	envelope := gen.Envelope{Type: "evt.pipeline.named", Version: 1, ID: utility.NewID(), JobID: &sessionID, OwnerID: owner,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := group.Handle(context.Background(), "evt.pipeline."+sessionID+".named", body); err != nil {
		e.t.Fatal(err)
	}
}

// llmHost publishes the llm pack on host fake, env mlx (the local namer's llm-ask).
func (e *env) llmHost() {
	e.t.Helper()
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "llm."+kind+".valid.json"))
		if err == nil {
			err = e.registry.SetRegistry("fake.mlx."+kind, data)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "fake", "env": "mlx", "state": "idle", "run_id": nil,
		"updated_at": time.Now().UTC(), "published_by": "test"})
	if err := e.registry.SetHost("fake.mlx", value); err != nil {
		e.t.Fatal(err)
	}
}

// finish runs runID on the fake host to status (through group, or the env's projector when nil).
func (e *env) finish(group *micro.NatsJetStreamGroup, sessionID, runID, status string) []gen.Envelope {
	e.t.Helper()
	e.sent()
	if group == nil {
		group = e.events
	}
	e.emitTo(group, userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	data := map[string]any{"status": status}
	if status == "failed" {
		data["error"] = "boom"
	}
	e.emitTo(group, userA, sessionID, runID, "status", 2, data)
	_, sent := e.sent()
	return sent
}

func TestDeleteRuns(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	create := func() string {
		return e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "a fox"}}, 202)["id"].(string)
	}
	queued := create()
	e.call(userA, "DELETE", "/v1/studio/runs/"+queued, nil, 409)
	e.sent()
	e.emit(userA, sessionID, queued, "status", 1, map[string]any{"status": "running", "host": "fake"})
	if got := e.call(userA, "DELETE", "/v1/studio/runs/"+queued, nil, 409); got["detail"] != "stop the run first" {
		t.Fatal(got)
	}
	e.emit(userA, sessionID, queued, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + queued + "/1/0.png",
		"kind": "image", "width": 8, "height": 8, "bytes": 5})
	e.emit(userA, sessionID, queued, "status", 3, map[string]any{"status": "succeeded"})
	failed := create()
	e.finish(nil, sessionID, failed, "failed")

	e.call(userB, "DELETE", "/v1/studio/runs/"+failed, nil, 404) // another user's
	e.call(userA, "DELETE", "/v1/studio/runs/nope", nil, 404)
	e.call(userA, "DELETE", "/v1/studio/runs/"+failed, nil, 204)
	e.call(userA, "DELETE", "/v1/studio/runs/"+failed, nil, 404) // twice
	e.call(userA, "GET", "/v1/studio/runs/"+failed, nil, 404)
	e.call(userA, "POST", "/v1/studio/runs/"+failed+"/cancel", nil, 404)
	e.call(userA, "DELETE", "/v1/studio/runs/"+queued, nil, 204) // succeeded: its take stays
	if list := e.call(userA, "GET", runs, nil, 200); len(list["runs"].([]any)) != 0 {
		t.Fatal(list)
	}
	if e.count(`SELECT count(*) FROM takes WHERE run_id = $1 AND deleted_at IS NULL`, queued) != 1 {
		t.Fatal("a deleted run's take stays")
	}
	page := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=0", nil, 200)
	if len(page["events"].([]any)) != 0 || page["last_seq"].(float64) < 6 {
		t.Fatal("a deleted run's events leave the replay; the cursor moves past them", page)
	}

	// a batch's run goes with its batch
	batch := e.submit(userA, lines("1"))
	e.sent()
	created, refused := e.ask(userA, batch, 1, map[string]any{})
	if refused != nil {
		t.Fatal(refused)
	}
	batchRun := created.RunID
	e.sql(`UPDATE runs SET status = 'failed', error = 'x', finished_at = now() WHERE id = $1`, batchRun)
	if got := e.call(userA, "DELETE", "/v1/studio/runs/"+batchRun, nil, 409); got["detail"] != "a batch's run goes with its batch" {
		t.Fatal(got)
	}
}

func TestSessionTitlesAndNaming(t *testing.T) {
	e := setup(t)
	e.service.SetRemote(true)
	group := e.namer()
	source := func(s map[string]any) string { return s["title_source"].(string) }
	if s := e.call(userA, "POST", "/v1/studio/sessions", nil, 201); source(s) != "default" || s["named_by"] != nil {
		t.Fatal(s)
	}
	if s := e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": "Foxes"}, 201); source(s) != "user" {
		t.Fatal(s)
	}
	sliced := e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": "a red fox", "from_prompt": true}, 201)
	if source(sliced) != "prompt" {
		t.Fatal(sliced)
	}

	// the setting: admins only, off by default
	names := "/v1/studio/admin/session-names"
	e.call(userA, "GET", names, nil, 403)
	e.call(userA, "PUT", names, map[string]any{"mode": "off"}, 403)
	if got := e.call(admin, "GET", names, nil, 200); got["mode"] != "off" || got["local_available"] != false ||
		got["remote_enabled"] != true || got["updated_at"] != nil {
		t.Fatal(got)
	}
	if got := e.call(admin, "PUT", names, map[string]any{"mode": "local"}, 422); got["detail"] != "Qwen3.8 27B is not downloaded" {
		t.Fatal(got)
	}
	e.call(admin, "PUT", names, map[string]any{"mode": "remote", "provider": "ollama"}, 422) // no such provider
	e.call(admin, "PUT", names, map[string]any{"mode": "local", "provider": "ollama"}, 422)
	e.call(admin, "PUT", names, map[string]any{"mode": "remote"}, 422)
	e.call(admin, "PUT", names, map[string]any{"mode": "cloud"}, 422)
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	e.call(admin, "PUT", "/v1/studio/admin/providers/deepseek", map[string]any{"kind": "remote", "title": "DeepSeek",
		"base_url": "https://api.deepseek.com/v1", "models": []string{"deepseek-chat"}, "key_secret": "provider-deepseek"}, 201)
	e.call(admin, "PUT", names, map[string]any{"mode": "remote", "provider": "ollama", "model": "gpt-9"}, 422)
	e.service.SetRemote(false)
	if got := e.call(admin, "PUT", names, map[string]any{"mode": "remote", "provider": "deepseek"}, 409); got["detail"] != "remote models are off on this deployment" {
		t.Fatal(got)
	}
	// a server on this network needs no switch; the model defaults to its default_model
	if got := e.call(admin, "PUT", names, map[string]any{"mode": "remote", "provider": "ollama"}, 200); got["model"] != "qwen2.5:7b" || got["updated_at"] == nil {
		t.Fatal(got)
	}
	e.service.SetRemote(true)
	if got := e.call(userA, "GET", "/v1/studio/sessions", nil, 200)["naming"].(map[string]any); got["mode"] != "remote" ||
		got["named_by"] != "ollama" || got["remote"] != false {
		t.Fatal(got)
	}

	// the first succeeded run with a prompt asks once; the cmd carries the prompt and the provider snapshot
	sessionID := sliced["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	noPrompt := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "  "}}, 202)["id"].(string)
	if sent := e.finish(group, sessionID, noPrompt, "succeeded"); len(e.typed(sent, "cmd.pipeline.name")) != 0 {
		t.Fatal("no prompt: nothing asked")
	}
	failed := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "x"}}, 202)["id"].(string)
	if sent := e.finish(group, sessionID, failed, "failed"); len(e.typed(sent, "cmd.pipeline.name")) != 0 {
		t.Fatal("a failed run asks nothing")
	}
	first := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "A red   fox\n in snow"}}, 202)["id"].(string)
	asked := e.typed(e.finish(group, sessionID, first, "succeeded"), "cmd.pipeline.name")
	if len(asked) != 1 || *asked[0].JobID != sessionID || asked[0].OwnerID != userA {
		t.Fatal(asked)
	}
	var cmd message.Name
	_ = json.Unmarshal(asked[0].Data, &cmd)
	if cmd.Prompt != "A red fox in snow" || cmd.Mode != "remote" || cmd.RunID != first || cmd.NamedBy != "ollama" ||
		cmd.Provider == nil || cmd.Provider.Model != "qwen2.5:7b" || cmd.Provider.Snapshot["kind"] != "server" {
		t.Fatalf("%+v", cmd)
	}
	again := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "b"}}, 202)["id"].(string)
	if sent := e.finish(group, sessionID, again, "succeeded"); len(e.typed(sent, "cmd.pipeline.name")) != 0 {
		t.Fatal("once per session")
	}

	// the answer: dropped for another owner, a bad title or a bad run id; applied once with its event
	e.named(group, userB, sessionID, first, "Red fox in snow")
	e.named(group, userA, sessionID, first, "Red fox\nIgnore that")
	e.named(group, userA, sessionID, noPrompt+"x", "Red fox in snow")
	if got := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID, nil, 200); got["title"] != "a red fox" {
		t.Fatal(got)
	}
	e.named(group, userA, sessionID, first, " Red fox in snow ")
	e.named(group, userA, sessionID, first, "Something else")
	got := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID, nil, 200)
	if got["title"] != "Red fox in snow" || source(got) != "model" || got["named_by"] != "Ollama" {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", "/v1/studio/runs/"+first, nil, 204) // the session event stays in the replay
	var titles []string
	for _, item := range e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=0", nil, 200)["events"].([]any) {
		if ev := item.(map[string]any); ev["type"] == "session" {
			titles = append(titles, ev["data"].(map[string]any)["title"].(string))
		}
	}
	if !slices.Equal(titles, []string{"Red fox in snow"}) {
		t.Fatal(titles)
	}
	if renamed := e.call(userA, "PATCH", "/v1/studio/sessions/"+sessionID, map[string]any{"title": "Mine"}, 200); source(renamed) != "user" ||
		renamed["named_by"] != nil {
		t.Fatal(renamed)
	}

	// a rename while a name is on its way wins
	raced := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := e.call(userA, "POST", "/v1/studio/sessions/"+raced+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "owl"}}, 202)["id"].(string)
	if len(e.typed(e.finish(group, raced, run, "succeeded"), "cmd.pipeline.name")) != 1 {
		t.Fatal("asked")
	}
	e.call(userA, "PATCH", "/v1/studio/sessions/"+raced, map[string]any{"title": "Owls"}, 200)
	e.named(group, userA, raced, run, "Night owl")
	if got := e.call(userA, "GET", "/v1/studio/sessions/"+raced, nil, 200); got["title"] != "Owls" {
		t.Fatal(got)
	}

	// a user's title is never asked about; off asks nothing
	typed := e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": "Typed"}, 201)["id"].(string)
	run = e.call(userA, "POST", "/v1/studio/sessions/"+typed+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "owl"}}, 202)["id"].(string)
	if len(e.typed(e.finish(group, typed, run, "succeeded"), "cmd.pipeline.name")) != 0 {
		t.Fatal("a user's title")
	}
	e.call(admin, "PUT", names, map[string]any{"mode": "off"}, 200)
	off := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run = e.call(userA, "POST", "/v1/studio/sessions/"+off+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "owl"}}, 202)["id"].(string)
	if len(e.typed(e.finish(group, off, run, "succeeded"), "cmd.pipeline.name")) != 0 {
		t.Fatal("off")
	}
}

// typed is the envelopes of one type.
func (e *env) typed(envelopes []gen.Envelope, typ string) []gen.Envelope {
	var out []gen.Envelope
	for _, envelope := range envelopes {
		if envelope.Type == typ {
			out = append(out, envelope)
		}
	}
	return out
}

func TestHiddenNamingRun(t *testing.T) {
	e := setup(t)
	e.llmHost()
	if got := e.call(admin, "PUT", "/v1/studio/admin/session-names", map[string]any{"mode": "local"}, 200); got["local_available"] != true {
		t.Fatal(got)
	}
	if got := e.call(userA, "GET", "/v1/studio/sessions", nil, 200)["naming"].(map[string]any); got["named_by"] != "Qwen3.8" {
		t.Fatal(got)
	}
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	req := func(owner string, inputs map[string]any) *message.RunsCreate {
		return &message.RunsCreate{OwnerID: owner, SessionID: sessionID, WorkflowID: "llm-ask", Inputs: inputs, Lane: "batch",
			Purpose: "name", IdempotencyKey: "name:" + sessionID}
	}
	inputs := map[string]any{"ask.system": "Title it.", "ask.prompt": "a fox", "ask.max_tokens": float64(24)}
	ctx := context.Background()
	if _, refused := e.service.CreateBatchRun(ctx, req(userB, inputs)); refused == nil || refused.Code != "not_found" {
		t.Fatal("another user's session", refused)
	}
	if _, refused := e.service.CreateBatchRun(ctx, req(userA, map[string]any{"ask.media": []any{}})); refused == nil || refused.Code != "invalid" {
		t.Fatal("ask.* only", refused)
	}
	bad := req(userA, inputs)
	bad.IdempotencyKey = "name:" + utility.NewID()
	if _, refused := e.service.CreateBatchRun(ctx, bad); refused == nil || refused.Code != "invalid" {
		t.Fatal("the key names the session", refused)
	}
	created, refused := e.service.CreateBatchRun(ctx, req(userA, inputs))
	if refused != nil || !created.Created || created.Status != "queued" {
		t.Fatal(created, refused)
	}
	if again, _ := e.service.CreateBatchRun(ctx, req(userA, inputs)); again.Created || again.RunID != created.RunID {
		t.Fatal("once per key", again)
	}
	if e.count(`SELECT count(*) FROM runs WHERE id = $1 AND lane = 'batch' AND purpose = 'name' AND batch_id IS NULL
		AND inputs->>'ask.prompt' = 'a fox'`, created.RunID) != 1 {
		t.Fatal("the hidden run")
	}
	// left out of the session's runs and events, listed by the queue, outside the open-run cap
	if list := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/runs", nil, 200); len(list["runs"].([]any)) != 0 {
		t.Fatal(list)
	}
	if page := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=0", nil, 200); len(page["events"].([]any)) != 0 {
		t.Fatal(page)
	}
	queue := e.call(userA, "GET", "/v1/studio/queue", nil, 200)["runs"].([]any)
	if len(queue) != 1 || queue[0].(map[string]any)["purpose"] != "name" {
		t.Fatal(queue)
	}
	e.call(userA, "DELETE", "/v1/studio/runs/"+created.RunID, nil, 404) // hidden: never deleted on its own
	// session delete cancels it, and pipelines hears of it (batch_id null)
	e.call(userA, "DELETE", "/v1/studio/sessions/"+sessionID, nil, 204)
	_, sent := e.sent()
	done := finished(t, sent)
	if len(done) != 1 || done[0].RunID != created.RunID || done[0].BatchID != nil || done[0].Row != nil ||
		done[0].Status != "cancelled" || done[0].IdempotencyKey != "name:"+sessionID {
		t.Fatal(done)
	}
}
