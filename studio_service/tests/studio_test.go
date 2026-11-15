// Integration test of studio_service: HTTP → outbox → (a hand-driven runner) → projector → read models and the event
// log, against a scratch Postgres (TEST_DATABASE_URL, skipped without it), with no NATS.
package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/middleware"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/migrations"
	"github.com/helmstudio/studio_service/registry"
)

const (
	userA = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
	userB = "11111111-2222-4333-8444-555555555555"
)

// env is one test's API: a migrated scratch database, the runners' registry, the projector's handlers, a test server
// and the run ids written to the KV cancels.
type env struct {
	t         *testing.T
	pool      *pgxpool.Pool
	registry  *registry.Registry
	events    *micro.NatsJetStreamGroup
	srv       *httptest.Server
	cancelled []string
	revoked   atomic.Bool // the token check of open event streams fails
	unreached bool        // the KV cancels write fails
	service   *studio.Service
	store     *storage.Memory // the objects reference images are checked against
}

// setup migrates a fresh schema, loads the fake runner's registry values and serves the API. It skips without
// TEST_DATABASE_URL.
func setup(t *testing.T) *env {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	if err := goose.Up(sqldb, "."); err != nil {
		t.Fatal(err)
	}
	sqldb.Close()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	e := &env{t: t, pool: pool, registry: &registry.Registry{}, events: &micro.NatsJetStreamGroup{}, store: storage.NewMemory()}
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", kind+".valid.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.registry.SetRegistry("fake.fake."+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	e.host(0)
	studio.NewProjector(pool).MountNatsJetStream(e.events)
	studio.NewProjector(pool).MountPipeline(e.events)
	signer := common.NewSigner("test-secret-0123456789", "http://kong", time.Hour)
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(middleware.NewErrorCatcher(), middleware.NewNotFound(),
		middleware.NewBodyLimit(func(string) int64 { return 1 << 20 }), middleware.NewPrefixGuard("/v1/", fakeAuthentication))
	shutdown := make(chan struct{})
	t.Cleanup(func() { close(shutdown) })
	cancel := func(_ context.Context, runID string) error {
		if e.unreached {
			return errors.New("no responders")
		}
		e.cancelled = append(e.cancelled, runID)
		return nil
	}
	recheck := func(context.Context, string) error {
		if e.revoked.Load() {
			return errors.New("revoked")
		}
		return nil
	}
	e.service = studio.NewService(pool, signer, e.registry, cancel, e.store)
	router.LoadControllers(studio.NewController(e.service, recheck, shutdown),
		gallery.NewController(gallery.NewService(pool, signer, e.store, e.registry, e.service.FindWorkflow)),
		lora.NewController(lora.NewService(pool, signer, e.store, e.registry)))
	e.srv = httptest.NewServer(router.Engine())
	t.Cleanup(e.srv.Close)
	return e
}

// host publishes the fake runner's hosts key, refreshed age ago.
func (e *env) host(age time.Duration) {
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "fake", "env": "fake", "state": "idle", "run_id": nil,
		"updated_at": time.Now().Add(-age).UTC(), "published_by": "test"})
	if err := e.registry.SetHost("fake.fake", value); err != nil {
		e.t.Fatal(err)
	}
}

// fakeAuthentication stands in for the NATS token check: X-Test-User is the caller's id.
func fakeAuthentication(ctx *gin.Context) {
	id := ctx.GetHeader("X-Test-User")
	if id == "" {
		network.SendError(ctx, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}
	common.SetCaller(ctx, common.Caller{ID: id, Roles: []string{"user"}})
}

// call sends JSON (nil: no body) as user, wants the status and returns the decoded body.
func (e *env) call(user, method, path string, body any, want int) map[string]any {
	e.t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request, _ := http.NewRequest(method, e.srv.URL+path, reader)
	request.Header.Set("X-Test-User", user)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	defer response.Body.Close()
	out, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		e.t.Fatalf("%s %s: %d %s, want %d", method, path, response.StatusCode, out, want)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	return decoded
}

// relay runs one dispatcher pass, then drains the outbox like the relay does and returns the subjects and commands.
func (e *env) relay() ([]string, []gen.Envelope) {
	e.t.Helper()
	if err := studio.NewDispatcher(e.pool, e.registry, nil, nil).Pass(context.Background(), time.Now()); err != nil {
		e.t.Fatal(err)
	}
	var subjects []string
	var sent []gen.Envelope
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, subject string, envelope *gen.Envelope, msgID string) error {
		if !strings.HasPrefix(msgID, *envelope.JobID+":") {
			e.t.Fatalf("Nats-Msg-Id %s", msgID)
		}
		subjects = append(subjects, subject)
		sent = append(sent, *envelope)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return subjects, sent
}

// emit projects one evt.run.<session>.<kind> of runID as the runner would publish it.
func (e *env) emit(owner, sessionID, runID, kind string, seq int, data map[string]any) {
	e.t.Helper()
	data["seq"] = seq
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.run." + kind, Version: 1, ID: utility.NewID(), JobID: &runID, OwnerID: owner,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := e.events.Handle(context.Background(), "evt.run."+sessionID+"."+kind, body); err != nil {
		e.t.Fatal(err)
	}
}

// eventTypes returns the session's events after since as "type:status-or-step" strings.
func (e *env) eventTypes(user, sessionID string) []string {
	page := e.call(user, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=0", nil, 200)
	var out []string
	for _, item := range page["events"].([]any) {
		event := item.(map[string]any)
		data := event["data"].(map[string]any)
		switch event["type"] {
		case "status":
			out = append(out, "status:"+data["status"].(string))
		case "progress":
			out = append(out, "progress")
		case "output":
			if !strings.HasPrefix(data["url"].(string), "http://kong/files/u/"+userA+"/runs/") {
				e.t.Fatalf("output url: %v", data["url"])
			}
			out = append(out, "output")
		}
	}
	return out
}

// A run from request to succeeded: the command, the projected events (each once), the outputs with signed links,
// nothing after the end, and another user always gets 404.
func TestRunFlow(t *testing.T) {
	e := setup(t)
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)
	sessionID := session["id"].(string)
	if session["title"] != "Untitled" {
		t.Fatal(session)
	}
	e.call(userB, "GET", "/v1/studio/sessions/"+sessionID, nil, 404)
	e.call(userA, "GET", "/v1/studio/sessions/nope", nil, 404)
	e.call(userA, "PATCH", "/v1/studio/sessions/"+sessionID, map[string]any{"title": "  "}, 422)
	if renamed := e.call(userA, "PATCH", "/v1/studio/sessions/"+sessionID, map[string]any{"title": " Fox "}, 200); renamed["title"] != "Fox" {
		t.Fatal(renamed)
	}
	if list := e.call(userB, "GET", "/v1/studio/sessions", nil, 200); len(list["sessions"].([]any)) != 0 {
		t.Fatal("another user's sessions")
	}

	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	e.call(userB, "POST", runs, map[string]any{"workflow_id": "echo"}, 404)
	e.call(userA, "POST", runs, map[string]any{"workflow_id": "nope"}, 422)
	if bad := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"echo.steps": 0}}, 422); bad["detail"] != "echo.steps: must be between 1 and 200" {
		t.Fatal(bad)
	}
	run := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "a red fox", "echo.steps": 2}}, 202)
	runID := run["id"].(string)
	if run["status"] != "queued" || run["host"] != "fake" || run["progress"] != nil || len(run["outputs"].([]any)) != 0 {
		t.Fatal(run)
	}
	subjects, sent := e.relay()
	if len(sent) != 1 || subjects[0] != "cmd.run.fake.fake" || sent[0].Type != "cmd.run.execute" || *sent[0].JobID != runID || sent[0].OwnerID != userA {
		t.Fatal(subjects, sent)
	}
	var command struct {
		Workflow struct {
			ID    string
			Nodes map[string]struct{ Params map[string]any }
		}
		OutputPrefix string `json:"output_prefix"`
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	if command.Workflow.ID != "echo" || command.Workflow.Nodes["echo"].Params["steps"] != float64(2) ||
		command.Workflow.Nodes["echo"].Params["delay_ms"] != float64(300) || command.Workflow.Nodes["output"].Params == nil ||
		command.OutputPrefix != "u/"+userA+"/runs/"+runID+"/1/" {
		t.Fatalf("%s", sent[0].Data)
	}

	e.emit(userB, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"}) // not the owner: dropped
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"}) // redelivery
	e.emit(userA, sessionID, runID, "progress", 2, map[string]any{"node": "echo", "step": 1, "of": 2})
	e.emit(userA, sessionID, runID, "progress", 3, map[string]any{"node": "echo", "step": 2, "of": 2, "phase": "sampling", "eta_s": 1.5})
	e.emit(userA, sessionID, runID, "output", 4, map[string]any{"index": 1, "key": "u/" + userB + "/runs/x/0.png", "kind": "image", "bytes": 5})
	e.emit(userA, sessionID, runID, "output", 5, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/0.png",
		"kind": "image", "width": 512, "height": 512, "bytes": 1234, "seed": 42})
	e.emit(userA, sessionID, runID, "status", 6, map[string]any{"status": "succeeded", "resolved": map[string]any{"seed": 42}})
	e.emit(userA, sessionID, runID, "status", 7, map[string]any{"status": "failed", "error": "late"}) // after the end
	e.emit(userA, sessionID, runID, "progress", 8, map[string]any{"node": "echo", "step": 2, "of": 2})

	want := []string{"status:queued", "status:running", "progress", "progress", "output", "status:succeeded"}
	if got := e.eventTypes(userA, sessionID); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	e.call(userB, "GET", "/v1/studio/sessions/"+sessionID+"/events", nil, 404)
	done := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	outputs := done["outputs"].([]any)
	if done["status"] != "succeeded" || done["error"] != nil || done["started_at"] == nil || done["finished_at"] == nil ||
		len(outputs) != 1 || done["progress"].(map[string]any)["step"] != float64(2) ||
		done["progress"].(map[string]any)["phase"] != "sampling" || done["resolved"].(map[string]any)["seed"] != float64(42) {
		t.Fatal(done)
	}
	if output := outputs[0].(map[string]any); output["width"] != float64(512) || output["seed"] != float64(42) ||
		!strings.Contains(output["url"].(string), "sig=") {
		t.Fatal(output)
	}
	e.call(userB, "GET", "/v1/studio/runs/"+runID, nil, 404)
	e.call(userA, "POST", "/v1/studio/runs/"+runID+"/cancel", nil, 409)
	if list := e.call(userA, "GET", runs, nil, 200); len(list["runs"].([]any)) != 1 {
		t.Fatal(list)
	}
	// an output stored before the gallery (no take) becomes a take on Backfill, once
	ctx := context.Background()
	if err := common.System(ctx, e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE run_outputs SET take_id = NULL; DELETE FROM takes`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	projector := studio.NewProjector(e.pool)
	projector.Workflows = e.registry
	if made, err := projector.Backfill(ctx); err != nil || made != 1 {
		t.Fatal(made, err)
	}
	if made, err := projector.Backfill(ctx); err != nil || made != 0 {
		t.Fatal("again:", made, err)
	}
	var title string
	var seed float64
	if err := common.System(ctx, e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT t.title, (t.made_with->>'seed')::float8 FROM takes t
			JOIN run_outputs o ON o.take_id = t.id WHERE t.run_id = $1 AND t.session_id = $2`, runID, sessionID).Scan(&title, &seed)
	}); err != nil || title != "a red fox" || seed != 42 {
		t.Fatal(title, seed, err)
	}
	if n := e.count(`SELECT count(*) FROM takes t JOIN runs r ON r.id = t.run_id WHERE t.created_at = r.finished_at`); n != 1 {
		t.Fatal("a backfilled take is dated when its run finished:", n)
	}
	// a deleted take's run output row goes (gallery.Delete): its output event is left out, the seq still moves on
	e.sql(`DELETE FROM run_outputs WHERE run_id = $1`, runID)
	want = []string{"status:queued", "status:running", "progress", "progress", "status:succeeded"}
	if got := e.eventTypes(userA, sessionID); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	if page := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=4", nil, 200); page["last_seq"] != float64(6) ||
		len(page["events"].([]any)) != 1 {
		t.Fatal(page)
	}
	e.call(userB, "DELETE", "/v1/studio/sessions/"+sessionID, nil, 404)
	e.call(userA, "DELETE", "/v1/studio/sessions/"+sessionID, nil, 204)
	e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 404)
}

// Cancel: a run still in the outbox is cancelled at once and never sent; a sent one is marked and written to the KV
// cancels, and a session with an open run can't be deleted.
func TestCancel(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": "c"}, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"

	unsent := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo"}, 202)["id"].(string)
	e.call(userB, "POST", "/v1/studio/runs/"+unsent+"/cancel", nil, 404)
	if run := e.call(userA, "POST", "/v1/studio/runs/"+unsent+"/cancel", nil, 202); run["status"] != "cancelled" {
		t.Fatal(run)
	}
	if _, sent := e.relay(); len(sent) != 0 || len(e.cancelled) != 0 {
		t.Fatal("a cancelled run is never sent", sent, e.cancelled)
	}

	sentID := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo"}, 202)["id"].(string)
	e.relay()
	e.call(userA, "DELETE", "/v1/studio/sessions/"+sessionID, nil, 409)
	e.unreached = true // the KV write fails: 503, and no cancel is marked that the runner never saw
	e.call(userA, "POST", "/v1/studio/runs/"+sentID+"/cancel", nil, 503)
	var marked bool
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT cancel_requested_at IS NOT NULL FROM runs WHERE id = $1`, sentID).Scan(&marked)
	}); err != nil || marked {
		t.Fatal("cancel marked after a failed KV write", marked, err)
	}
	e.unreached = false
	if run := e.call(userA, "POST", "/v1/studio/runs/"+sentID+"/cancel", nil, 202); run["status"] != "queued" {
		t.Fatal(run)
	}
	e.call(userA, "POST", "/v1/studio/runs/"+sentID+"/cancel", nil, 202) // again: harmless
	if !slices.Equal(e.cancelled, []string{sentID, sentID}) {
		t.Fatal(e.cancelled)
	}
	e.emit(userA, sessionID, sentID, "status", 1, map[string]any{"status": "cancelled"})
	want := []string{"status:queued", "status:cancelled", "status:queued", "status:cancelled"}
	if got := e.eventTypes(userA, sessionID); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	e.call(userA, "DELETE", "/v1/studio/sessions/"+sessionID, nil, 204)
}

// The SSE stream replays after Last-Event-ID with id, event and data lines.
func TestEventStream(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)
	e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/studio/sessions/"+sessionID+"/events", nil)
	request.Header.Set("X-Test-User", userA)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buf := make([]byte, 4096)
	n, _ := response.Body.Read(buf)
	frame := string(buf[:n])
	if response.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(frame, "id: 2\nevent: status\ndata: {") {
		t.Fatalf("%q", frame)
	}
}

// An open event stream ends once its token is no longer valid (expired, signed out, disabled).
func TestEventStreamEndsWhenTheTokenIsRevoked(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/studio/sessions/"+sessionID+"/events", nil)
	request.Header.Set("X-Test-User", userA)
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	e.revoked.Store(true)
	if _, err := io.ReadAll(response.Body); err != nil { // EOF before the 5 s deadline: the server closed it
		t.Fatal("the stream outlived its token:", err)
	}
}
