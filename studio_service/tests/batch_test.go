// Integration tests of batches (contracts/README.md "Batches and the pipeline engine"): Submit and its checks, the
// control state machine with its commands, studio.runs.create, evt.studio.run_finished on every terminal path, the
// batch projector, the batch lane and isolation.
package tests

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// lines is a Submit body of echo with one row per prompt.
func lines(prompts ...string) map[string]any {
	return map[string]any{"workflow_id": "echo", "inputs": map[string]any{"echo.steps": 2},
		"rows": map[string]any{"lines": map[string]any{"param": "prompt.text", "values": prompts}}}
}

// submit creates a batch as user and returns it.
func (e *env) submit(user string, body map[string]any) map[string]any {
	e.t.Helper()
	return e.call(user, "POST", "/v1/studio/batches", body, 202)
}

// sent drains the outbox (after a dispatcher pass) and returns "<subject> <Nats-Msg-Id>" per message, and the
// envelopes.
func (e *env) sent() ([]string, []gen.Envelope) {
	e.t.Helper()
	if err := studio.NewDispatcher(e.pool, e.registry, nil, nil).Pass(context.Background(), time.Now()); err != nil {
		e.t.Fatal(err)
	}
	var lines []string
	var envelopes []gen.Envelope
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, subject string, envelope *gen.Envelope, msgID string) error {
		lines = append(lines, subject+" "+msgID)
		envelopes = append(envelopes, *envelope)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return lines, envelopes
}

// ask sends studio.runs.create for row (attempt 1) of batch as pipelines would.
func (e *env) ask(owner string, batch map[string]any, row int, inputs map[string]any) (message.RunsCreated, *message.RunsCreateError) {
	key := fmt.Sprintf("%s:%d:1:step:0:0", batch["id"], row)
	return e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: owner, SessionID: batch["session_id"].(string),
		WorkflowID: "echo", Inputs: inputs, Lane: "batch", BatchID: batch["id"].(string), Row: row, IdempotencyKey: key})
}

// pipeline projects one evt.pipeline.<batch>.<kind> as pipelines would publish it.
func (e *env) pipeline(owner, batchID, kind string, data map[string]any) {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.pipeline." + kind, Version: 1, ID: utility.NewID(), JobID: &batchID, OwnerID: owner,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := e.events.Handle(context.Background(), "evt.pipeline."+batchID+"."+kind, body); err != nil {
		e.t.Fatal(err)
	}
}

// finished returns the run_finished data in envelopes.
func finished(t *testing.T, envelopes []gen.Envelope) []message.RunFinished {
	var out []message.RunFinished
	for _, envelope := range envelopes {
		if envelope.Type == "evt.studio.run_finished" {
			var data message.RunFinished
			if err := json.Unmarshal(envelope.Data, &data); err != nil {
				t.Fatal(err)
			}
			out = append(out, data)
		}
	}
	return out
}

// Submit checks everything (a bad row named), stores the batch, its rows and cmd.pipeline.submit, and the reads
// page; another user sees none of it.
func TestBatchSubmit(t *testing.T) {
	e := setup(t)
	path := "/v1/studio/batches"
	table := map[string]any{"workflow_id": "echo", "rows": map[string]any{"table": map[string]any{
		"columns": []string{"prompt.text", "echo.steps"}, "rows": [][]any{{"a", 3}, {"b", 0}}}}}
	if bad := e.call(userA, "POST", path, table, 422); bad["detail"] != "row 2: echo.steps: must be between 1 and 200" {
		t.Fatal(bad)
	}
	e.call(userA, "POST", path, map[string]any{"workflow_id": "nope", "rows": lines()["rows"]}, 422)
	foreign := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"]
	withSession := lines("a")
	withSession["session_id"] = foreign
	e.call(userA, "POST", path, withSession, 404)
	e.host(time.Minute)
	if bad := e.call(userA, "POST", path, lines("a"), 422); bad["detail"] != "no live host for env fake" {
		t.Fatal(bad)
	}
	e.host(0)

	batch := e.submit(userA, lines("a fox", "a heron"))
	id := batch["id"].(string)
	counts := batch["counts"].(map[string]any)
	if batch["status"] != "queued" || batch["control"] != "run" || batch["title"] != "Echo × 2" || batch["workflow_title"] != "Echo" ||
		counts["total"] != float64(2) || counts["queued"] != float64(2) {
		t.Fatal(batch)
	}
	if session := e.call(userA, "GET", "/v1/studio/sessions/"+batch["session_id"].(string), nil, 200); session["title"] != "Echo × 2" {
		t.Fatal("a new session titled like the batch", session)
	}
	subjects, envelopes := e.sent()
	if !slices.Equal(subjects, []string{"cmd.pipeline.submit " + id + ":1"}) || *envelopes[0].JobID != id {
		t.Fatal(subjects)
	}
	var submit message.Submit
	_ = json.Unmarshal(envelopes[0].Data, &submit)
	if submit.BatchID != id || submit.Snapshot.WorkflowID != "echo" || submit.Snapshot.Workflow.Env != "fake" || len(submit.Rows) != 2 ||
		submit.Rows[1].Inputs["prompt.text"] != "a heron" || submit.Rows[1].Inputs["echo.steps"] != float64(2) {
		t.Fatalf("%+v", submit)
	}
	rows := e.call(userA, "GET", path+"/"+id+"/rows?limit=1", nil, 200)
	first := rows["rows"].([]any)[0].(map[string]any)
	if first["row"] != float64(1) || first["status"] != "queued" || first["attempt"] != float64(1) || rows["next_cursor"] == nil {
		t.Fatal(rows)
	}
	next := e.call(userA, "GET", path+"/"+id+"/rows?cursor="+rows["next_cursor"].(string), nil, 200)
	if got := next["rows"].([]any); len(got) != 1 || got[0].(map[string]any)["row"] != float64(2) || next["next_cursor"] != nil {
		t.Fatal(next)
	}
	e.call(userA, "GET", path+"/"+id+"/rows?cursor=%21", nil, 422)

	for i := range 4 {
		e.submit(userA, lines(fmt.Sprint(i)))
	}
	if full := e.call(userA, "POST", path, lines("x"), 409); full["detail"] != "You have 5 unfinished batches; wait for one to finish." {
		t.Fatal(full)
	}
	page := e.call(userA, "GET", path+"?limit=3", nil, 200)
	more := e.call(userA, "GET", path+"?limit=3&cursor="+page["next_cursor"].(string), nil, 200)
	if len(page["batches"].([]any)) != 3 || len(more["batches"].([]any)) != 2 || more["next_cursor"] != nil ||
		more["batches"].([]any)[1].(map[string]any)["id"] != id {
		t.Fatal("newest first, in pages", page, more)
	}

	if list := e.call(userB, "GET", path, nil, 200); len(list["batches"].([]any)) != 0 {
		t.Fatal("another user's batches", list)
	}
	for _, call := range [][2]string{{"GET", ""}, {"GET", "/rows"}, {"GET", "/events"}, {"POST", "/pause"}, {"POST", "/resume"},
		{"POST", "/stop"}, {"POST", "/rows/1/retry"}} {
		e.call(userB, call[0], path+"/"+id+call[1], nil, 404)
	}
	e.call(userA, "GET", path+"/nope", nil, 404)
	e.call(userA, "POST", path+"/"+id+"/rows/0/retry", nil, 404)
	if e.count(`SELECT count(*) FROM batches`) != 5 {
		t.Fatal("a refused Submit stores nothing")
	}
	e.call(userA, "DELETE", "/v1/studio/sessions/"+batch["session_id"].(string), nil, 409)
}

// Concurrent Submits of one user (each in a new session) still stop at MaxOpenBatches.
func TestBatchCapUnderConcurrentSubmits(t *testing.T) {
	e := setup(t)
	body, _ := json.Marshal(lines("a"))
	codes := make(chan int, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			request, _ := http.NewRequest("POST", e.srv.URL+"/v1/studio/batches", bytes.NewReader(body))
			request.Header.Set("X-Test-User", userA)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				codes <- 0
				return
			}
			response.Body.Close()
			codes <- response.StatusCode
		})
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for code := range codes {
		if code == 202 {
			accepted++
		} else if code != 409 {
			t.Fatal("want 202 or 409, got", code)
		}
	}
	if accepted != 5 || e.count(`SELECT count(*) FROM batches`) != 5 {
		t.Fatal("accepted", accepted)
	}
}

// studio.runs.create makes one run per key in the batch lane from the row's stored inputs, and refuses a batch that is
// not the owner's, another session, another workflow, or a key that is not the row's attempt of its one step.
func TestRunsCreate(t *testing.T) {
	e := setup(t)
	batch := e.submit(userA, lines("a fox", "a heron"))
	e.sent()
	created, refused := e.ask(userA, batch, 1, map[string]any{"prompt.text": "not what was submitted"})
	if refused != nil || !created.Created || created.Status != "queued" || len(created.TakeIDs) != 0 {
		t.Fatal(created, refused)
	}
	again, refused := e.ask(userA, batch, 1, map[string]any{"prompt.text": "something else"})
	if refused != nil || again.Created || again.RunID != created.RunID {
		t.Fatal("one key, one run", again, refused)
	}
	if _, refused := e.ask(userB, batch, 2, nil); refused == nil || refused.Code != "not_found" {
		t.Fatal("another owner's batch", refused)
	}
	other := maps(batch)
	other["session_id"] = e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"]
	if _, refused := e.ask(userA, other, 2, nil); refused == nil || refused.Code != "not_found" {
		t.Fatal("another session", refused)
	}
	if _, refused := e.ask(userA, batch, 9, nil); refused == nil || refused.Code != "not_found" {
		t.Fatal("no such row", refused)
	}
	for _, key := range []string{":2:2:step:0:0", ":2:1:judge:0:0", ":2:1:step:1:0", ":2:1:step:0:1"} {
		other := &message.RunsCreate{OwnerID: userA, SessionID: batch["session_id"].(string), WorkflowID: "echo", Lane: "batch",
			BatchID: batch["id"].(string), Row: 2, IdempotencyKey: batch["id"].(string) + key}
		if _, refused := e.service.CreateBatchRun(context.Background(), other); refused == nil || refused.Code != "invalid" {
			t.Fatal("not the row's attempt of its one step", key, refused)
		}
	}
	bad := &message.RunsCreate{OwnerID: userA, SessionID: batch["session_id"].(string), WorkflowID: "echo", Lane: "batch",
		BatchID: batch["id"].(string), Row: 2, IdempotencyKey: batch["id"].(string) + ":3:1:step:0:0"}
	if _, refused := e.service.CreateBatchRun(context.Background(), bad); refused == nil || refused.Code != "invalid" {
		t.Fatal("a key of another row", refused)
	}
	run := e.call(userA, "GET", "/v1/studio/runs/"+created.RunID, nil, 200)
	if run["lane"] != "batch" || run["batch_id"] != batch["id"] || run["batch_row"] != float64(1) || run["inputs"].(map[string]any)["prompt.text"] != "a fox" {
		t.Fatal(run)
	}
	e.call(userB, "GET", "/v1/studio/runs/"+created.RunID, nil, 404)

	// the batch lane waits behind an interactive run created after it
	interactive := e.call(userB, "POST", "/v1/studio/sessions/"+e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)+"/runs",
		map[string]any{"workflow_id": "echo"}, 202)
	if _, envelopes := e.sent(); len(envelopes) != 1 || *envelopes[0].JobID != interactive["id"] {
		t.Fatal("interactive before batch", envelopes)
	}
	e.emit(userB, interactive["session_id"].(string), interactive["id"].(string), "status", 1, map[string]any{"status": "succeeded"})
	if _, envelopes := e.sent(); len(envelopes) != 1 || *envelopes[0].JobID != created.RunID {
		t.Fatal("then the batch run", envelopes)
	}

	// it succeeds with an output: a pipeline take, and run_finished once, through the outbox
	sessionID, runID := batch["session_id"].(string), created.RunID
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.png",
		"kind": "image", "width": 8, "height": 8, "bytes": 10})
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded"})
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded"}) // a redelivery
	subjects, envelopes := e.sent()
	done := finished(t, envelopes)
	if !slices.Equal(subjects, []string{"evt.studio." + sessionID + ".run_finished " + runID + ":finished"}) || len(done) != 1 ||
		done[0].Status != "succeeded" || done[0].Row != 1 || len(done[0].TakeIDs) != 1 ||
		done[0].IdempotencyKey != batch["id"].(string)+":1:1:step:0:0" {
		t.Fatal(subjects, done)
	}
	if e.count(`SELECT count(*) FROM takes WHERE id = $1 AND source = 'pipeline' AND batch_id = $2`, done[0].TakeIDs[0], batch["id"]) != 1 {
		t.Fatal("a batch run's take: source pipeline, with the batch")
	}
	if again, _ := e.ask(userA, batch, 1, nil); again.Status != "succeeded" || again.Created || len(again.TakeIDs) != 1 {
		t.Fatal("asking after the end gives the result", again)
	}
	row := e.call(userA, "GET", "/v1/studio/batches/"+batch["id"].(string)+"/rows?limit=1", nil, 200)["rows"].([]any)[0].(map[string]any)
	if row["status"] != "running" {
		t.Fatal("a run that left the queue shows its row running until pipelines reports the row", row)
	}
}

// maps copies a decoded object.
func maps(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Every terminal status of a batch run reaches pipelines as run_finished: cancelled while queued, cancelled by the
// runner, no eligible host at a pass, lease lost twice, created failed (no host) and created cancelled (stopped).
func TestRunFinishedEveryPath(t *testing.T) {
	e := setup(t)
	batch := e.submit(userA, lines("1", "2", "3", "4", "5", "6"))
	sessionID := batch["session_id"].(string)
	e.sent()
	statusOf := func(envelopes []gen.Envelope) map[int]string {
		out := map[int]string{}
		for _, data := range finished(t, envelopes) {
			out[data.Row] = data.Status
		}
		return out
	}

	// row 1 is released; row 2 waits behind it and is cancelled while queued
	one, _ := e.ask(userA, batch, 1, nil)
	two, _ := e.ask(userA, batch, 2, nil)
	e.sent()
	e.call(userA, "POST", "/v1/studio/runs/"+two.RunID+"/cancel", nil, 202)
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{2: "cancelled"}) {
		t.Fatal("cancelled while queued", statusOf(envelopes))
	}
	// row 1 (released) is cancelled through the KV; the runner reports it
	e.call(userA, "POST", "/v1/studio/runs/"+one.RunID+"/cancel", nil, 202)
	e.emit(userA, sessionID, one.RunID, "status", 1, map[string]any{"status": "cancelled"})
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{1: "cancelled"}) {
		t.Fatal("cancelled by the runner", statusOf(envelopes))
	}

	// row 3: lost its lease on attempt 2
	three, _ := e.ask(userA, batch, 3, nil)
	e.sent()
	e.sql(`UPDATE runs SET attempt = 2, status = 'running', released_at = now() - interval '1 minute' WHERE id = $1`, three.RunID)
	dispatcher := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(studio.Confirm)} {
		if err := dispatcher.Pass(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{3: "failed"}) {
		t.Fatal("lease lost twice", statusOf(envelopes))
	}

	// row 4 waits while the host goes away: the pass fails it; row 5 is created failed
	e.call(userA, "POST", "/v1/studio/batches/"+batch["id"].(string)+"/pause", nil, 202) // held, still failed
	four, _ := e.ask(userA, batch, 4, nil)
	e.host(time.Minute)
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{4: "failed"}) {
		t.Fatal("no eligible host at a pass", four, statusOf(envelopes))
	}
	if five, _ := e.ask(userA, batch, 5, nil); five.Status != "failed" || five.Error == nil || *five.Error != "no live host for env fake" {
		t.Fatal(five)
	}
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{5: "failed"}) {
		t.Fatal("created failed", statusOf(envelopes))
	}
	e.host(0)
	e.call(userA, "POST", "/v1/studio/batches/"+batch["id"].(string)+"/stop", nil, 202)
	if six, _ := e.ask(userA, batch, 6, nil); six.Status != "cancelled" {
		t.Fatal("a stopped batch's runs are created cancelled", six)
	}
	if _, envelopes := e.sent(); !mapsEqual(statusOf(envelopes), map[int]string{6: "cancelled"}) {
		t.Fatal("created cancelled", statusOf(envelopes))
	}
}

// mapsEqual compares two small maps.
func mapsEqual(a, b map[int]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Pause holds the batch's queued runs, Resume lets them go, Stop cancels them; each sends its command with the next
// number; every call outside its states is 409; Retry queues a failed row under a new attempt.
func TestBatchControl(t *testing.T) {
	e := setup(t)
	batch := e.submit(userA, lines("a", "b"))
	id, path := batch["id"].(string), "/v1/studio/batches/"+batch["id"].(string)
	e.sent()
	e.call(userA, "POST", path+"/resume", nil, 409)
	if paused := e.call(userA, "POST", path+"/pause", nil, 202); paused["control"] != "pause" || paused["status"] != "queued" {
		t.Fatal(paused)
	}
	e.call(userA, "POST", path+"/pause", nil, 409)
	held, _ := e.ask(userA, batch, 1, nil)
	subjects, _ := e.sent()
	if !slices.Equal(subjects, []string{"cmd.pipeline.pause " + id + ":2"}) {
		t.Fatal("the held run is not released", subjects)
	}
	if run := e.call(userA, "GET", "/v1/studio/runs/"+held.RunID, nil, 200); run["status"] != "queued" || run["position"] != nil {
		t.Fatal("held: no position", run)
	}
	e.pipeline(userA, id, "batch", map[string]any{"seq": 2, "status": "paused", "reason": "paused by you"})
	if resumed := e.call(userA, "POST", path+"/resume", nil, 202); resumed["control"] != "run" || resumed["status"] != "paused" {
		t.Fatal(resumed)
	}
	if subjects, _ := e.sent(); !slices.Equal(subjects, []string{"cmd.pipeline.resume " + id + ":3", "cmd.run.fake.fake " + held.RunID + ":1"}) {
		t.Fatal("resumed: the run goes", subjects)
	}

	// row 2 failed; it is retried under attempt 2
	e.call(userA, "POST", path+"/rows/2/retry", nil, 409)
	e.pipeline(userA, id, "row", map[string]any{"seq": 5, "row": 2, "attempt": 1, "status": "failed", "error": "boom"})
	retried := e.call(userA, "POST", path+"/rows/2/retry", nil, 202)
	if retried["status"] != "queued" || retried["attempt"] != float64(2) || retried["error"] != nil {
		t.Fatal(retried)
	}
	e.call(userA, "POST", path+"/rows/2/retry", nil, 409)
	_, envelopes := e.sent()
	if len(envelopes) != 1 || envelopes[0].Type != "cmd.pipeline.retry" || !strings.Contains(string(envelopes[0].Data), `"attempt": 2`) {
		t.Fatal(envelopes)
	}
	e.pipeline(userA, id, "row", map[string]any{"seq": 6, "row": 2, "attempt": 1, "status": "failed", "error": "late"})
	if row := e.call(userA, "GET", path+"/rows?cursor="+rowCursor(1), nil, 200)["rows"].([]any)[0].(map[string]any); row["status"] != "queued" {
		t.Fatal("an event of an older attempt is ignored", row)
	}

	// Stop: the released run goes to the KV cancels, stop is sent; then everything is 409
	if stopped := e.call(userA, "POST", path+"/stop", nil, 202); stopped["control"] != "stop" {
		t.Fatal(stopped)
	}
	if !slices.Equal(e.cancelled, []string{held.RunID}) {
		t.Fatal("the released run is cancelled through the KV", e.cancelled)
	}
	if subjects, _ := e.sent(); !slices.Equal(subjects, []string{"cmd.pipeline.stop " + id + ":5"}) {
		t.Fatal(subjects)
	}
	for _, call := range []string{"/stop", "/pause", "/resume", "/rows/2/retry"} {
		e.call(userA, "POST", path+call, nil, 409)
	}
	// pipelines ended the batch done before it read the stop (the cancelled runs ended its rows first): stopped
	e.pipeline(userA, id, "batch", map[string]any{"seq": 3, "status": "done"})
	if done := e.call(userA, "GET", path, nil, 200); done["status"] != "stopped" || done["finished_at"] == nil {
		t.Fatal("done after Stop is stopped", done)
	}
}

// rowCursor is the rows cursor after row n.
func rowCursor(n int) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "row %d", n))
}

// The batch projector keeps the newest state (by seq, per row and for the batch), drops repeats, older attempts and
// another owner's events, checks the run and takes it is given, and logs every change for the events endpoint.
func TestBatchProjector(t *testing.T) {
	e := setup(t)
	batch := e.submit(userA, lines("a", "b"))
	id, path := batch["id"].(string), "/v1/studio/batches/"+batch["id"].(string)
	e.sent()
	run, _ := e.ask(userA, batch, 1, nil)
	other := e.submit(userA, lines("x"))
	foreignRun, _ := e.ask(userA, other, 1, nil)

	e.pipeline(userA, id, "batch", map[string]any{"seq": 1, "status": "running"})
	e.pipeline(userB, id, "batch", map[string]any{"seq": 9, "status": "failed", "reason": "not yours"})
	e.pipeline(userA, id, "row", map[string]any{"seq": 3, "row": 1, "attempt": 1, "status": "running", "run_id": run.RunID})
	e.pipeline(userA, id, "row", map[string]any{"seq": 2, "row": 1, "attempt": 1, "status": "queued"}) // older: ignored
	e.pipeline(userA, id, "row", map[string]any{"seq": 4, "row": 2, "attempt": 1, "status": "running", "run_id": foreignRun.RunID})
	got := e.call(userA, "GET", path, nil, 200)
	if got["status"] != "running" || got["started_at"] == nil || got["reason"] != nil {
		t.Fatal(got)
	}
	rows := e.call(userA, "GET", path+"/rows", nil, 200)["rows"].([]any)
	first, second := rows[0].(map[string]any), rows[1].(map[string]any)
	if first["run_id"] != run.RunID || first["status"] != "queued" || second["run_id"] != nil {
		t.Fatal("the run is kept only when it is this row's; queued until it leaves the queue", rows)
	}
	e.pipeline(userA, id, "row", map[string]any{"seq": 5, "row": 1, "attempt": 1, "status": "succeeded", "run_id": run.RunID,
		"take_ids": []string{utility.NewID()}})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 6, "status": "paused", "reason": "3 rows in a row failed: boom"})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 6, "status": "done"}) // a repeat of seq 6: dropped
	got = e.call(userA, "GET", path, nil, 200)
	counts := got["counts"].(map[string]any)
	if got["status"] != "paused" || got["reason"] != "3 rows in a row failed: boom" || counts["succeeded"] != float64(1) || counts["queued"] != float64(1) {
		t.Fatal(got)
	}
	row := e.call(userA, "GET", path+"/rows?limit=1", nil, 200)["rows"].([]any)[0].(map[string]any)
	if row["status"] != "succeeded" || len(row["take_ids"].([]any)) != 0 || row["thumb_url"] != nil || row["finished_at"] == nil {
		t.Fatal("a take that is not the owner's is dropped", row)
	}

	page := e.call(userA, "GET", path+"/events?since=0", nil, 200)
	events := page["events"].([]any)
	var kinds []string
	for _, item := range events {
		event := item.(map[string]any)
		kinds = append(kinds, event["type"].(string))
		if event["type"] == "batch" && event["data"].(map[string]any)["status"] != "paused" {
			t.Fatal("a batch event carries the batch as it is now", event)
		}
	}
	if !slices.Equal(kinds, []string{"row", "row", "batch"}) || page["last_seq"].(float64) < 3 {
		t.Fatal("the last event of each row and of the batch", kinds, page["last_seq"])
	}
	if later := e.call(userA, "GET", fmt.Sprintf("%s/events?since=%v", path, page["last_seq"]), nil, 200); len(later["events"].([]any)) != 0 {
		t.Fatal(later)
	}
	e.call(userA, "GET", path+"/events?since=x", nil, 422)
}
