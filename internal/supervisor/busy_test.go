package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// conflict launches id without confirming and returns the heavy_conflict
// refusal.
func conflict(t *testing.T, e *env, id string) *Error {
	t.Helper()
	_, err := e.sup.Launch(context.Background(), id, LaunchOptions{})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindHeavyConflict || se.Heavy == nil {
		t.Fatalf("launching %s beside a running heavy studio: %v; want a heavy conflict", id, err)
	}
	return se
}

// busyEnv runs a heavy studio whose /busy answers from a file, beside a
// second heavy studio waiting to launch.
func busyEnv(t *testing.T, busyDecl string) (*env, string) {
	t.Helper()
	e := newEnv(t, Config{Grace: time.Second, HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	busyFile := filepath.Join(t.TempDir(), "busy")
	e.manifest("running", "peak_ram_gb: 21", `  - name: studio
    heavy: true
    cmd: `+serve+` --name running --port {port} --busy-file `+busyFile+`
    port: {}
    health: { tcp: true, timeout_s: 10, interval_s: 1 }
`+busyDecl)
	e.manifest("waiting", "peak_ram_gb: 25", `  - name: studio
    heavy: true
    cmd: `+serve+` --name waiting --port {port}
    port: {}
    health: { tcp: true, timeout_s: 10, interval_s: 1 }
`)
	if _, err := e.sup.Launch(context.Background(), "running", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("running", stateRunning, 10*time.Second)
	return e, busyFile
}

func setBusy(t *testing.T, file, body string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Q12: a conforming answer is busy or idle, with what the studio said.
// Anything else — another status, a body without a "busy" boolean, a progress
// outside 0..1, no answer in time, no probe declared — is unknown, and is
// never shown as idle.
func TestBusyProbeReadsTheContractAndNothingElse(t *testing.T) {
	e, busyFile := busyEnv(t, "    busy: { path: /busy }\n")
	cases := []struct {
		body, state, inSentence string
		loaded                  *bool
	}{
		{`{"busy": true, "loaded": true, "progress": 0.4, "message": "rendering take 3"}`, BusyBusy, "40% through its work (rendering take 3) and holds a loaded model; stopping it loses that work", ptr(true)},
		{`{"busy": false, "loaded": true}`, BusyIdle, "reports no work in flight and holds a loaded model", ptr(true)},
		{`{"busy": false}`, BusyIdle, "reports no work in flight.", nil},
		{`[{"id": "job"}]`, BusyUnknown, "cannot tell whether running has work in flight", nil}, // ltx and h3's /api/queue today
		{`{"status": "ok", "variants": {}}`, BusyUnknown, `without a "busy" boolean`, nil},      // AuK's /api/health today
		{`{"busy": "yes"}`, BusyUnknown, "cannot tell", nil},
		{`{"Busy": false}`, BusyUnknown, `without a "busy" boolean`, nil}, // a Go studio's untagged struct
		{`{"busy": false} trailing`, BusyUnknown, "followed by more data", nil},
		{`{"busy": false}{"busy": true}`, BusyUnknown, "followed by more data", nil},
		{`{"busy": null}`, BusyUnknown, `without a "busy" boolean`, nil},
		{`{"busy": false, "loaded": "yes"}`, BusyUnknown, `"loaded" that is not a boolean`, nil},
		{`null`, BusyUnknown, "not answer with a JSON object", nil},
		{`{"busy": true, "progress": 40}`, BusyUnknown, "outside 0..1", nil},
		{`status 503`, BusyUnknown, "answered 503", nil},
		{`not json`, BusyUnknown, "cannot tell", nil},
		{`hang`, BusyUnknown, "did not answer within", nil},
	}
	for _, c := range cases {
		setBusy(t, busyFile, c.body)
		se := conflict(t, e, "waiting")
		b := se.Heavy.Busy
		if b.State != c.state {
			t.Errorf("answer %s: state %q, want %q (%+v)", c.body, b.State, c.state, b)
		}
		if (c.loaded == nil) != (b.Loaded == nil) || (c.loaded != nil && *c.loaded != *b.Loaded) {
			t.Errorf("answer %s: loaded %v, want %v", c.body, b.Loaded, c.loaded)
		}
		if !strings.Contains(se.Message, c.inSentence) {
			t.Errorf("answer %s: message %q lacks %q", c.body, se.Message, c.inSentence)
		}
		if c.state == BusyUnknown && strings.Contains(se.Message, "no work in flight") {
			t.Errorf("answer %s: an unknown state was worded as idle: %q", c.body, se.Message)
		}
	}
	if gs, _ := e.sup.Status(context.Background(), "running"); gs.State != stateRunning || gs.Processes[0].HealthState != healthPassing {
		t.Fatalf("busy probes changed the running studio: %+v", gs)
	}
}

func ptr[T any](v T) *T { return &v }

// A studio that declares no busy probe is unknown, and says why.
func TestNoBusyProbeIsUnknown(t *testing.T) {
	e, _ := busyEnv(t, "")
	se := conflict(t, e, "waiting")
	if se.Heavy.Busy.State != BusyUnknown || !strings.Contains(se.Heavy.Busy.Message, "declares no busy probe") {
		t.Fatalf("busy = %+v; want unknown, naming the missing probe", se.Heavy.Busy)
	}
}

// Q13: a confirmation is refused, and nothing is stopped, when the running
// studio's busy state changed after the user read the dialog; progress moving
// does not make it stale; a current confirmation stops the running studio.
func TestAStaleConfirmationStopsNothing(t *testing.T) {
	e, busyFile := busyEnv(t, "    busy: { path: /busy }\n")
	setBusy(t, busyFile, `{"busy": false, "loaded": true}`)
	idle := conflict(t, e, "waiting")
	before, _ := e.sup.Status(context.Background(), "running")

	setBusy(t, busyFile, `{"busy": true, "loaded": true, "progress": 0.1}`)
	_, err := e.sup.Launch(context.Background(), "waiting", LaunchOptions{Confirm: idle.Heavy.Confirm})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindPreviewChanged || se.Heavy == nil || se.Heavy.Busy.State != BusyBusy {
		t.Fatalf("confirming an idle dialog after a render started: %v; want preview_changed carrying the busy state", err)
	}
	for _, bogus := range []string{"true", "0000"} {
		if _, err := e.sup.Launch(context.Background(), "waiting", LaunchOptions{Confirm: bogus}); !errors.As(err, &se) || se.Kind != KindPreviewChanged {
			t.Fatalf("confirm %q: %v; want preview_changed", bogus, err)
		}
	}
	if gs, _ := e.sup.Status(context.Background(), "running"); gs.State != stateRunning || gs.Processes[0].PID != before.Processes[0].PID {
		t.Fatalf("a stale confirmation disturbed the running studio: %+v", gs)
	}

	busy := se.Heavy.Confirm
	setBusy(t, busyFile, `{"busy": true, "loaded": true, "progress": 0.7, "message": "later"}`)
	if _, err := e.sup.Launch(context.Background(), "waiting", LaunchOptions{Confirm: busy}); err != nil {
		t.Fatalf("confirming while only progress moved: %v", err)
	}
	e.waitState("waiting", stateRunning, 10*time.Second)
	if r := e.rows("running")["studio"]; r.state != stateExited || r.reason != reasonPreempted {
		t.Fatalf("running studio row %+v; want exited/preempted", r)
	}
}

// Review #7: the digest names the running group run, not only the busy state.
// A studio restarted between the dialog and the confirmation, with the same
// answer, is another group, and the confirmation stops nothing.
func TestAConfirmationDoesNotCarryOverToARestartedStudio(t *testing.T) {
	e, busyFile := busyEnv(t, "    busy: { path: /busy }\n")
	setBusy(t, busyFile, `{"busy": false, "loaded": true}`)
	before := conflict(t, e, "waiting")

	if err := e.sup.Stop("running"); err != nil {
		t.Fatal(err)
	}
	e.waitDone("running")
	if _, err := e.sup.Launch(context.Background(), "running", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	restarted := e.waitState("running", stateRunning, 10*time.Second)

	_, err := e.sup.Launch(context.Background(), "waiting", LaunchOptions{Confirm: before.Heavy.Confirm})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindPreviewChanged {
		t.Fatalf("confirming a dialog about the studio's previous run: %v; want preview_changed", err)
	}
	if gs, _ := e.sup.Status(context.Background(), "running"); gs.State != stateRunning || gs.GroupRunID != restarted.GroupRunID {
		t.Fatalf("the restarted studio was disturbed: %+v", gs)
	}
}

// The group's answer: busy wins, then unknown, then idle; every live member
// that declares a probe is asked.
func TestGroupBusyFoldsItsMembers(t *testing.T) {
	e := newEnv(t, Config{HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	dir := t.TempDir()
	file := func(name string) string { return filepath.Join(dir, name) }
	e.manifest("pair", "", fmt.Sprintf(`  - name: studio
    heavy: true
    cmd: %[1]s --name studio --port {port} --busy-file %[2]s
    port: {}
    health: { tcp: true, timeout_s: 10, interval_s: 1 }
    busy: { path: /busy }
  - name: engine
    role: sidecar
    cmd: %[1]s --name engine --port {port} --busy-file %[3]s
    port: {}
    health: { tcp: true, timeout_s: 10, interval_s: 1 }
    busy: { path: /busy }
`, serve, file("studio"), file("engine")))
	e.manifest("next", "", `  - name: studio
    heavy: true
    cmd: `+serve+` --name next --port {port}
    port: {}
`)
	if _, err := e.sup.Launch(context.Background(), "pair", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitState("pair", stateRunning, 10*time.Second)
	for _, c := range []struct{ studio, engine, want string }{
		{`{"busy": false}`, `{"busy": false, "loaded": true}`, BusyIdle},
		{`{"busy": false}`, `{"busy": true}`, BusyBusy},
		{`nope`, `{"busy": true}`, BusyBusy},
		{`nope`, `{"busy": false}`, BusyUnknown},
	} {
		setBusy(t, file("studio"), c.studio)
		setBusy(t, file("engine"), c.engine)
		if got := conflict(t, e, "next").Heavy.Busy; got.State != c.want {
			t.Errorf("studio %s, engine %s: %+v, want %s", c.studio, c.engine, got, c.want)
		}
	}
	setBusy(t, file("studio"), `{"busy": false}`)
	setBusy(t, file("engine"), `{"busy": false, "loaded": true}`)
	if got := conflict(t, e, "next").Heavy.Busy; got.Loaded == nil || !*got.Loaded {
		t.Errorf("a member holding a model: loaded = %v, want true", got.Loaded)
	}
}

// Review #5: a refused connection is reported as what happened, not as a
// timeout; the answer is still unknown.
func TestBusyProbeWordsTransportErrorsFromTheError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	st := probeBusy(context.Background(), port, "/busy")
	if st.State != BusyUnknown || strings.Contains(st.Message, "did not answer within") || !strings.Contains(st.Message, "refused") {
		t.Fatalf("probe of a closed port = %+v; want unknown, saying the connection was refused", st)
	}
}

// Review #6: a member re-adopted without a plan — its manifest no longer
// resolves, say a Python environment that no longer checks — is still asked
// through the loaded manifest; with no manifest loaded, the reason says so
// rather than claiming no probe is declared.
func TestARunningGroupWithoutAPlanIsStillAsked(t *testing.T) {
	e := newEnv(t, Config{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/busy" {
			w.Write([]byte(`{"busy": true, "message": "rendering"}`))
		}
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	st := e.manifest("adopted", "", `
  - name: studio
    cmd: exec sleep 60
    busy: { path: /busy }
`)
	planless := func(studio Studio) *group {
		g := newGroup(e.sup, studio, "adopted", "run")
		g.monitorOnly = true
		g.procs = []*proc{{g: g, name: "studio", port: port, spawned: true, exited: make(chan struct{})}}
		return g
	}
	if got := e.sup.groupBusy(context.Background(), planless(st)); got.State != BusyBusy || got.Message != "rendering" {
		t.Fatalf("a planless group with a loaded manifest: %+v; want its probe asked", got)
	}
	got := e.sup.groupBusy(context.Background(), planless(Studio{}))
	if got.State != BusyUnknown || strings.Contains(got.Message, "declares no busy probe") || !strings.Contains(got.Message, "manifest is not loaded") {
		t.Fatalf("a group with no manifest loaded: %+v; want unknown, saying the manifest is not loaded", got)
	}
}
