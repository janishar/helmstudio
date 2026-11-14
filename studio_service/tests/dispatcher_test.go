// Integration tests of the dispatcher (contracts/README.md "The dispatcher") on the fake host: one run at a time per
// host, the fair order and positions, the redacted queue, no eligible host, the lease with its one retry, cancel of a
// waiting run, and two replicas passing at once.
package tests

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

const userC = "22222222-3333-4444-8555-666666666666"

// sql runs a statement as System.
func (e *env) sql(statement string, args ...any) {
	e.t.Helper()
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), statement, args...)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

// count runs a count query as System.
func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), query, args...).Scan(&n)
	}); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// busyWith publishes the fake runner's key busy with run and attempt, refreshed now.
func (e *env) busyWith(runID string, attempt int) {
	value, _ := json.Marshal(map[string]any{"host": "fake", "env": "fake", "state": "busy", "run_id": runID,
		"attempt": attempt, "updated_at": time.Now().UTC()})
	if err := e.registry.SetHost("fake.fake", value); err != nil {
		e.t.Fatal(err)
	}
}

// sentIDs runs a dispatcher pass and the relay and returns the Nats-Msg-Ids sent.
func (e *env) sentIDs() []string {
	e.t.Helper()
	if err := studio.NewDispatcher(e.pool, e.registry, nil, nil).Pass(context.Background(), time.Now()); err != nil {
		e.t.Fatal(err)
	}
	var ids []string
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, _ string, _ *gen.Envelope, msgID string) error {
		ids = append(ids, msgID)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

// newRun creates a run of echo in a new session of user and returns its id and session id.
func (e *env) newRun(user string) (string, string) {
	sessionID := e.call(user, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := e.call(user, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)
	return run["id"].(string), sessionID
}

// One run at a time on the host, released round-robin between users; position counts the line on the host and the
// queue shows the caller's runs and only a count of others'.
func TestDispatchOrderPositionAndQueue(t *testing.T) {
	e := setup(t)
	a1, sessionA := e.newRun(userA)
	created := e.call(userA, "POST", "/v1/studio/sessions/"+sessionA+"/runs", map[string]any{"workflow_id": "echo"}, 202)
	a2 := created["id"].(string)
	b1, sessionB := e.newRun(userB)
	c1, sessionC := e.newRun(userC)
	if created["status"] != "queued" || created["host"] != "fake" || created["position"] != float64(2) ||
		created["lane"] != "interactive" || created["attempt"] != float64(1) {
		t.Fatal(created) // lined up behind a1 until b1 and c1 came
	}
	position := func(user, runID string) any {
		return e.call(user, "GET", "/v1/studio/runs/"+runID, nil, 200)["position"]
	}
	if position(userA, a1) != float64(1) || position(userB, b1) != float64(2) || position(userC, c1) != float64(3) ||
		position(userA, a2) != float64(4) {
		t.Fatal("line a1 b1 c1 a2", position(userA, a1), position(userB, b1), position(userC, c1), position(userA, a2))
	}

	released := func() []string {
		_, sent := e.relay()
		var ids []string
		for _, envelope := range sent {
			ids = append(ids, *envelope.JobID)
		}
		return ids
	}
	if got := released(); !slices.Equal(got, []string{a1}) {
		t.Fatal("one run on the free host:", got)
	}
	if got := released(); len(got) != 0 {
		t.Fatal("the host is busy:", got)
	}
	if position(userA, a1) != float64(1) || position(userA, a2) != float64(4) {
		t.Fatal("sent but not started is first in line")
	}
	queue := e.call(userA, "GET", "/v1/studio/queue", nil, 200)
	runs, hosts := queue["runs"].([]any), queue["hosts"].([]any)
	if len(runs) != 2 || runs[0].(map[string]any)["id"] != a1 || runs[1].(map[string]any)["id"] != a2 || len(hosts) != 1 {
		t.Fatal(queue)
	}
	if host := hosts[0].(map[string]any); host["host"] != "fake" || host["state"] != "busy" || host["others_queued"] != float64(2) {
		t.Fatal(host)
	}
	if host := e.call(userB, "GET", "/v1/studio/queue", nil, 200)["hosts"].([]any)[0].(map[string]any); host["others_queued"] != float64(3) {
		t.Fatal("b sees a1, a2, c1 as a count", host)
	}

	finish := func(owner, sessionID, runID string) {
		e.emit(owner, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
		e.emit(owner, sessionID, runID, "status", 2, map[string]any{"status": "succeeded"})
	}
	finish(userA, sessionA, a1)
	if got := released(); !slices.Equal(got, []string{b1}) {
		t.Fatal("b, never released, goes before a's second run:", got)
	}
	finish(userB, sessionB, b1)
	if got := released(); !slices.Equal(got, []string{c1}) {
		t.Fatal(got)
	}
	finish(userC, sessionC, c1)
	if got := released(); !slices.Equal(got, []string{a2}) {
		t.Fatal(got)
	}
	if queue := e.call(userB, "GET", "/v1/studio/queue", nil, 200); len(queue["runs"].([]any)) != 0 {
		t.Fatal("b has nothing open", queue)
	}
}

// No eligible host: a run fails at create, and a waiting run fails at the next pass once its host is gone; neither
// is sent.
func TestNoEligibleHost(t *testing.T) {
	e := setup(t)
	e.host(time.Minute)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	failed := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)
	if failed["status"] != "failed" || failed["error"] != "no live host for env fake" || failed["host"] != nil || failed["position"] != nil {
		t.Fatal(failed)
	}
	e.host(0)
	waiting := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)["id"].(string)
	e.host(time.Minute)
	if _, sent := e.relay(); len(sent) != 0 {
		t.Fatal(sent)
	}
	if run := e.call(userA, "GET", "/v1/studio/runs/"+waiting, nil, 200); run["status"] != "failed" || run["error"] != "no live host for env fake" {
		t.Fatal(run)
	}
	if got := e.eventTypes(userA, sessionID); !slices.Equal(got, []string{"status:failed", "status:queued", "status:failed"}) {
		t.Fatal(got)
	}
}

// A lost lease (the key not on the run Lease after release, for Confirm) purges the command and queues attempt 2;
// the lost attempt's events are dropped, the new one's seq starts at 1; a second loss fails the run. A key on the run
// and attempt holds it, and nothing is lost while the projector lags.
func TestLeaseRetriesOnce(t *testing.T) {
	e := setup(t)
	runID, sessionID := e.newRun(userA)
	e.relay()
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.png",
		"kind": "image", "bytes": 5})
	var purged []string
	purge := func(_ context.Context, subject string) error {
		purged = append(purged, subject)
		return nil
	}
	dispatcher := studio.NewDispatcher(e.pool, e.registry, purge, nil)
	pass := func(at time.Time) {
		t.Helper()
		if err := dispatcher.Pass(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	attempt := func() (string, float64) {
		run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
		return run["status"].(string), run["attempt"].(float64)
	}
	now := time.Now()
	pass(now)
	if status, n := attempt(); status != "running" || n != 1 {
		t.Fatal("inside Lease the run holds", status, n)
	}
	e.sql(`UPDATE runs SET released_at = released_at - interval '1 minute'`)
	e.busyWith(runID, 1)
	pass(now)
	pass(now.Add(studio.Confirm))
	lagging := studio.NewDispatcher(e.pool, e.registry, purge, func(context.Context) (int, error) { return 1, nil })
	e.host(0) // idle: the key no longer names it
	for _, at := range []time.Time{now, now.Add(2 * studio.Confirm)} {
		if err := lagging.Pass(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	pass(now) // first look
	if status, n := attempt(); status != "running" || n != 1 || len(purged) != 0 {
		t.Fatal("held so far", status, n, purged)
	}
	pass(now.Add(studio.Confirm))
	if status, n := attempt(); status != "queued" || n != 2 || !slices.Equal(purged, []string{"cmd.run.fake.fake"}) {
		t.Fatal("retried", status, n, purged)
	}
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded"}) // attempt 1 still talking: dropped
	if n := e.count(`SELECT count(*) FROM takes WHERE run_id IS NULL AND deleted_at IS NOT NULL`) +
		10*e.count(`SELECT count(*) FROM run_outputs`); n != 1 {
		t.Fatal("attempt 1's output goes, its take deleted and let go of the run:", n)
	}

	if _, sent := e.relay(); len(sent) != 1 || *sent[0].JobID != runID || !strings.Contains(string(sent[0].Data),
		`"output_prefix": "u/`+userA+`/runs/`+runID+`/2/"`) {
		t.Fatal("attempt 2 goes out with its own output prefix", sent)
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "attempt": 2})
	if status, n := attempt(); status != "running" || n != 2 {
		t.Fatal(status, n)
	}
	e.sql(`UPDATE runs SET released_at = released_at - interval '1 minute'`)
	pass(now)
	pass(now.Add(studio.Confirm))
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	if run["status"] != "failed" || run["error"] != "runner on host fake stopped responding; gave up after 2 attempts" {
		t.Fatal(run)
	}
	want := []string{"status:queued", "status:running", "status:queued", "status:running", "status:failed"}
	if got := e.eventTypes(userA, sessionID); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	page := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=3", nil, 200)
	if retry := page["events"].([]any)[0].(map[string]any)["data"].(map[string]any); retry["attempt"] != float64(2) {
		t.Fatal(retry)
	}
}

// A run the dispatcher has not released is cancelled at once and never sent, nor written to the KV cancels.
func TestCancelWaitingRun(t *testing.T) {
	e := setup(t)
	first, sessionID := e.newRun(userA)
	e.relay()
	second := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 202)["id"].(string)
	if run := e.call(userA, "POST", "/v1/studio/runs/"+second+"/cancel", nil, 202); run["status"] != "cancelled" || run["position"] != nil {
		t.Fatal(run)
	}
	e.emit(userA, sessionID, first, "status", 1, map[string]any{"status": "succeeded"})
	if _, sent := e.relay(); len(sent) != 0 || len(e.cancelled) != 0 {
		t.Fatal("a cancelled waiting run is never sent", sent, e.cancelled)
	}
}

// Two replicas passing at once release one run to the one host, never two.
func TestTwoReplicasReleaseOnce(t *testing.T) {
	e := setup(t)
	for _, user := range []string{userA, userB, userC, userA} {
		e.newRun(user)
	}
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		dispatcher := studio.NewDispatcher(e.pool, e.registry, nil, nil)
		wait.Go(func() { errs <- dispatcher.Pass(context.Background(), time.Now()) })
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(`SELECT count(*) FROM outbox`); n != 1 {
		t.Fatal("commands:", n)
	}
	if n := e.count(`SELECT count(*) FROM runs WHERE released_at IS NOT NULL`); n != 1 {
		t.Fatal("released:", n)
	}
}

// Deleting a session (its runs cascade) does not reset its owner's turn: C, released before A, still goes first.
func TestDeletedSessionKeepsTurn(t *testing.T) {
	e := setup(t)
	runOnce := func(user string) (string, string) {
		runID, sessionID := e.newRun(user)
		if _, sent := e.relay(); len(sent) != 1 || *sent[0].JobID != runID {
			t.Fatal(sent)
		}
		return runID, sessionID
	}
	c0, sessionC := runOnce(userC)
	e.emit(userC, sessionC, c0, "status", 1, map[string]any{"status": "succeeded"})
	a1, sessionA := runOnce(userA)
	e.emit(userA, sessionA, a1, "status", 1, map[string]any{"status": "succeeded"})
	b1, sessionB := runOnce(userB) // on the host
	c1 := e.call(userC, "POST", "/v1/studio/sessions/"+sessionC+"/runs", map[string]any{"workflow_id": "echo"}, 202)["id"]
	e.call(userA, "DELETE", "/v1/studio/sessions/"+sessionA, nil, 204)
	e.newRun(userA)
	e.emit(userB, sessionB, b1, "status", 1, map[string]any{"status": "succeeded"})
	if _, sent := e.relay(); len(sent) != 1 || *sent[0].JobID != c1 {
		t.Fatal("c, released longest ago, goes before a:", sent)
	}
}

// At most MaxOpenRuns queued or running runs per user: the next create is a 409; another user is not held back.
func TestOpenRunCap(t *testing.T) {
	e := setup(t)
	_, sessionID := e.newRun(userA)
	e.sql(`INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status)
		SELECT gen_random_uuid(), $1, $2, 'echo', 'fake', '{}', 'queued' FROM generate_series(2, $3)`, userA, sessionID, studio.MaxOpenRuns)
	full := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 409)
	if full["detail"] != "You have 100 runs queued or running; wait for some to finish." {
		t.Fatal(full)
	}
	e.newRun(userB)
}

// Until the registry has loaded the KV's current values, a pass decides nothing (a waiting run is neither failed nor
// sent) and a new run is a 503.
func TestNothingDecidedBeforeTheRegistryLoaded(t *testing.T) {
	e := setup(t)
	runID, sessionID := e.newRun(userA)
	e.registry.Await("registry", "hosts")
	e.registry.SetHost("fake.fake", nil) // as an empty view at start would show it
	if _, sent := e.relay(); len(sent) != 0 {
		t.Fatal(sent)
	}
	if run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200); run["status"] != "queued" {
		t.Fatal(run)
	}
	e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo"}, 503)
	e.registry.Loaded("registry")
	e.host(0)
	e.registry.Loaded("hosts")
	if _, sent := e.relay(); len(sent) != 1 || *sent[0].JobID != runID {
		t.Fatal(sent)
	}
}
