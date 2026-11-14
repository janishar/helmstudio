package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const echoWorkflow = `{"id":"echo","title":"Echo","family":"echo","env":"fake",
 "nodes":{"prompt":{"kind":"prompt"},"echo":{"kind":"echo/echo","in":{"text":"prompt.text"}},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text","default":""}]},
  "echo/echo":{"params":[{"name":"steps","type":"int","default":10},{"name":"delay_ms","type":"int","default":300},{"name":"fail","type":"bool","default":false}]},
  "output":{"params":[]}}}`

var png = []byte("\x89PNG\r\n\x1a\nfake")

// fakeAPI is the slice of the public API helm uses. outcome is the run's end: succeeded, failed or cancelled
// (cancelled waits for POST .../cancel).
type fakeAPI struct {
	t         *testing.T
	outcome   string
	mu        sync.Mutex
	access    string // the access token the server accepts
	refreshes int
	inputs    map[string]any
	cancelled chan struct{}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/files/u/o1/runs/r1/0.png" && r.Header.Get("X-API-Key") != "helm_test" {
		http.Error(w, `{"detail":"invalid api key"}`, 401)
		return
	}
	f.mu.Lock()
	authed := r.Header.Get("Authorization") == "Bearer "+f.access
	f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	switch route {
	case "POST /auth/signin":
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["password"] != "secret pass 1" {
			http.Error(w, `{"detail":"invalid email or password"}`, 401)
			return
		}
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt1","user":{"email":%q}}`, f.access, b["email"])
		return
	case "POST /auth/refresh":
		f.mu.Lock()
		f.refreshes++
		f.access = fmt.Sprintf("at%d", f.refreshes+1)
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt%d","user":{}}`, f.access, f.refreshes+1)
		f.mu.Unlock()
		return
	case "GET /files/u/o1/runs/r1/0.png":
		if r.URL.Query().Get("sig") != "ok" {
			http.Error(w, `{"detail":"invalid link"}`, 403)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	if !authed {
		http.Error(w, `{"detail":"invalid or expired token"}`, 401)
		return
	}
	switch route {
	case "GET /auth/me":
		fmt.Fprint(w, `{"email":"a@example.com","name":"a","roles":["user"]}`)
	case "DELETE /auth/signout":
		w.WriteHeader(204)
	case "GET /v1/studio/workflows/echo":
		fmt.Fprint(w, echoWorkflow)
	case "POST /v1/studio/sessions":
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"s1","title":"Echo"}`)
	case "POST /v1/studio/sessions/s1/runs":
		var b struct {
			Inputs map[string]any `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.inputs = b.Inputs
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"r1","status":"queued","outputs":[]}`)
	case "GET /v1/studio/sessions/s1/events":
		f.events(w, r)
	case "POST /v1/studio/runs/r1/cancel":
		close(f.cancelled)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"r1","status":"running","outputs":[]}`)
	case "GET /v1/studio/runs/r1":
		fmt.Fprint(w, `{"id":"r1","status":"succeeded","outputs":[{"index":0,"key":"u/o1/runs/r1/0.png","kind":"image","url":"/files/u/o1/runs/r1/0.png?exp=1&sig=ok"}]}`)
	default:
		f.t.Errorf("unexpected %s", route)
		http.Error(w, `{"detail":"not found"}`, 404)
	}
}

func (f *fakeAPI) events(w http.ResponseWriter, r *http.Request) {
	from, err := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if r.Header.Get("Accept") != "text/event-stream" || err != nil {
		f.t.Errorf("events: accept %q, last id %q", r.Header.Get("Accept"), r.Header.Get("Last-Event-ID"))
	}
	w.Header().Set("Content-Type", "text/event-stream")
	seq := 0
	send := func(run, typ, data string) {
		if seq++; seq <= from {
			return
		}
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: {\"seq\":%d,\"type\":%q,\"run_id\":%q,\"data\":%s}\n\n", seq, typ, seq, typ, run, data)
		w.(http.Flusher).Flush()
	}
	send("other", "status", `{"status":"queued"}`) // another run of the session: ignored
	send("r1", "status", `{"status":"queued","error":null,"host":"fake"}`)
	fmt.Fprint(w, ": keep-alive\n\n")
	send("r1", "status", `{"status":"running","error":null,"host":"fake"}`)
	send("r1", "progress", `{"node":"echo","step":1,"of":2}`)
	switch f.outcome {
	case "succeeded":
		send("r1", "progress", `{"node":"echo","step":2,"of":2}`)
		send("r1", "output", `{"index":0,"key":"u/o1/runs/r1/0.png","kind":"image","url":"/x"}`)
		send("r1", "status", `{"status":"succeeded","error":null,"host":"fake"}`)
	case "failed":
		send("r1", "status", `{"status":"failed","error":"echo failed on purpose","host":"fake"}`)
	case "cancelled":
		select {
		case <-f.cancelled:
		case <-time.After(5 * time.Second):
			f.t.Error("no cancel request")
		}
		send("r1", "status", `{"status":"cancelled","error":null,"host":"fake"}`)
	}
}

// setup starts the fake API and logs in, with HOME in a temp dir.
func setup(t *testing.T, outcome string) (*fakeAPI, string) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	t.Setenv("HELM_PASSWORD", "secret pass 1")
	f := &fakeAPI{t: t, outcome: outcome, access: "at1", cancelled: make(chan struct{})}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	var out, errb bytes.Buffer
	if code := helm([]string{"login", "--url", srv.URL, "--email", "a@example.com"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("login: %d %s", code, errb.String())
	}
	return f, srv.URL
}

func TestLogin(t *testing.T) {
	_, url := setup(t, "")
	p, _ := configPath()
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config: %v %v", st, err)
	}
	c, _ := loadConfig()
	if c.URL != url || c.AccessToken != "at1" || c.RefreshToken != "rt1" || c.Email != "a@example.com" {
		t.Fatalf("config %+v", c)
	}
	t.Setenv("HELM_PASSWORD", "wrong")
	var out, errb bytes.Buffer
	if code := helm([]string{"login", "--url", url, "--email", "a@example.com"}, nil, &out, &errb, nil); code != 1 ||
		!strings.Contains(errb.String(), "invalid email or password") {
		t.Fatalf("bad password: %d %q", code, errb.String())
	}
}

func TestLoginPasswordPrompt(t *testing.T) {
	_, url := setup(t, "")
	t.Setenv("HELM_PASSWORD", "")
	var out, errb bytes.Buffer
	if code := helm([]string{"login", "--url", url, "--email", "a@example.com"}, strings.NewReader("secret pass 1\n"), &out, &errb, nil); code != 0 {
		t.Fatalf("prompt login: %d %s", code, errb.String())
	}
}

func TestRunSucceedsAndRefreshes(t *testing.T) {
	f, _ := setup(t, "succeeded")
	f.access = "at9" // the stored token is now stale: the first call gets a 401 and helm refreshes
	dir := t.TempDir()
	var out, errb bytes.Buffer
	args := []string{"run", "echo", "-p", "echo.steps=2", "-p", "prompt.text=a red fox", "-p", "echo.fail=false", "--out", dir}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s\n%s", code, errb.String(), out.String())
	}
	want := map[string]any{"echo.steps": 2.0, "prompt.text": "a red fox", "echo.fail": false}
	if fmt.Sprint(f.inputs) != fmt.Sprint(want) {
		t.Fatalf("inputs %v", f.inputs)
	}
	for _, line := range []string{"session s1", "run r1", "queued", "running on fake", "echo 1/2", "echo 2/2", "output 0 image", "succeeded"} {
		if !strings.Contains(out.String(), line+"\n") {
			t.Errorf("missing %q in\n%s", line, out.String())
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "r1_0.png")); err != nil || !bytes.Equal(b, png) {
		t.Fatalf("output file: %v", err)
	}
	if c, _ := loadConfig(); f.refreshes != 1 || c.AccessToken != "at2" || c.RefreshToken != "rt2" {
		t.Fatalf("refresh: %d %+v", f.refreshes, c)
	}
}

func TestRunFails(t *testing.T) {
	setup(t, "failed")
	var out, errb bytes.Buffer
	if code := helm([]string{"run", "echo", "-p", "echo.fail=true", "--out", t.TempDir()}, nil, &out, &errb, nil); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "failed: echo failed on purpose") || !strings.Contains(errb.String(), "run failed") {
		t.Fatalf("out %q err %q", out.String(), errb.String())
	}
}

func TestRunCancelOnInterrupt(t *testing.T) {
	setup(t, "cancelled")
	stop := make(chan os.Signal, 1)
	stop <- os.Interrupt
	var out, errb bytes.Buffer
	if code := helm([]string{"run", "echo", "--out", t.TempDir()}, nil, &out, &errb, stop); code != 1 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "cancelled\n") || !strings.Contains(errb.String(), "run cancelled") {
		t.Fatalf("out %q err %q", out.String(), errb.String())
	}
}

func TestInputsRejectsBadValues(t *testing.T) {
	var wf workflow
	if err := json.Unmarshal([]byte(echoWorkflow), &wf); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"echo.steps=ten", "echo.fail=maybe", "nodot=1", "echo.steps"} {
		if _, err := wf.inputs([]string{p}); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	got, err := wf.inputs([]string{"echo.unknown=1", "prompt.text=2"})
	if err != nil || got["echo.unknown"] != "1" || got["prompt.text"] != "2" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestWhoamiLogout(t *testing.T) {
	setup(t, "")
	var out, errb bytes.Buffer
	if code := helm([]string{"whoami"}, nil, &out, &errb, nil); code != 0 || out.String() != "a@example.com (a) roles: user\n" {
		t.Fatalf("whoami: %d %q %s", code, out.String(), errb.String())
	}
	if code := helm([]string{"logout"}, nil, &out, &errb, nil); code != 0 {
		t.Fatal(code, errb.String())
	}
	if c, _ := loadConfig(); c.AccessToken != "" || c.RefreshToken != "" {
		t.Fatalf("tokens kept: %+v", c)
	}
	if code := helm([]string{"whoami"}, nil, &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), "not logged in") {
		t.Fatalf("whoami after logout: %d %s", code, errb.String())
	}
}
