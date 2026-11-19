package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

const echoWorkflow = `{"id":"echo","title":"Echo","family":"echo","env":"fake","media":"image",
 "nodes":{"prompt":{"kind":"prompt"},"echo":{"kind":"echo/echo","in":{"text":"prompt.text"}},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text","default":""}]},
  "echo/echo":{"params":[{"name":"steps","type":"int","default":10},{"name":"delay_ms","type":"int","default":300},{"name":"fail","type":"bool","default":false},
   {"name":"refs","type":"rows","default":[],"max":10,"item":{"image":"Image"},"mention":"<image{n}>"}]},
  "output":{"params":[]}}}`

var png = []byte("\x89PNG\r\n\x1a\nfake")

const takeJSON = `{"id":"t1","kind":"image","source":"upload","title":"pic","width":3,"height":2,"created_at":"2026-11-14T10:00:00Z","starred":true}`

// fakeAPI is the slice of the public API helm uses. outcome is the run's end: succeeded, failed or cancelled
// (cancelled waits for POST .../cancel).
type fakeAPI struct {
	t           *testing.T
	outcome     string
	mu          sync.Mutex
	access      string // the access token the server accepts
	refreshes   int
	inputs      map[string]any
	workflowID  string // the last run's workflow_id
	cancelled   chan struct{}
	positions   []int          // queue positions GET /runs/r1 answers before the run starts
	query       string         // the last GET /v1/studio/takes query
	uploaded    []byte         // the body PUT to the upload link
	batchBody   map[string]any // the last POST /v1/studio/batches body
	batchEvents []string       // the BatchEvents GET /v1/studio/batches/b1/events serves, seq = index + 1
	useBody     map[string]any // the last POST /v1/studio/takes/t1/use body
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/files/") && r.Header.Get("X-API-Key") != "helm_test" {
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
	case "GET /files/u/o1/runs/r1/0.mp4":
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(mp4)
		return
	case "GET /files/u/o1/runs/r1/0.png":
		if r.URL.Query().Get("sig") != "ok" {
			http.Error(w, `{"detail":"invalid link"}`, 403)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	case "PUT /files/u/o1/uploads/t1.png":
		q := r.URL.Query()
		if q.Get("sig") != "ok" || r.Header.Get("X-API-Key")+r.Header.Get("Authorization") != "" {
			http.Error(w, `{"detail":"invalid link"}`, 403)
			return
		}
		if r.Header.Get("Content-Type") != q.Get("ct") || strconv.FormatInt(r.ContentLength, 10) != q.Get("bytes") {
			http.Error(w, `{"detail":"Content-Type and Content-Length must be the signed ones"}`, 415)
			return
		}
		f.uploaded, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
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
	case "GET /v1/studio/workflows/echo-video":
		fmt.Fprint(w, videoWorkflow)
	case "GET /v1/studio/workflows/echo-image-to-video":
		fmt.Fprint(w, i2vWorkflow)
	case "GET /v1/studio/workflows/echo-retake":
		fmt.Fprint(w, retakeWorkflow)
	case "GET /v1/studio/takes/t2/uses":
		fmt.Fprint(w, t2Uses)
	case "GET /v1/studio/takes/t3/uses":
		fmt.Fprint(w, `{"uses":[]}`)
	case "GET /v1/studio/workflows/" + mineID:
		fmt.Fprint(w, mineWorkflow)
	case "GET /v1/studio/workflows/w1":
		fmt.Fprint(w, stackWorkflow)
	case "POST /v1/studio/sessions":
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"s1","title":"Echo"}`)
	case "POST /v1/studio/sessions/s1/runs":
		var b struct {
			WorkflowID string         `json:"workflow_id"`
			Inputs     map[string]any `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.inputs, f.workflowID = b.Inputs, b.WorkflowID
		w.WriteHeader(202)
		if len(f.positions) > 0 {
			fmt.Fprintf(w, `{"id":"r1","status":"queued","position":%d,"outputs":[]}`, f.positions[0])
			return
		}
		fmt.Fprint(w, `{"id":"r1","status":"queued","outputs":[]}`)
	case "GET /v1/studio/sessions/s1/events":
		f.events(w, r)
	case "POST /v1/studio/runs/r1/cancel":
		close(f.cancelled)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"r1","status":"running","outputs":[]}`)
	case "GET /v1/studio/runs/r1":
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.positions) > 0 {
			fmt.Fprintf(w, `{"id":"r1","status":"queued","position":%d,"outputs":[]}`, f.positions[0])
			f.positions = f.positions[1:]
			return
		}
		if f.workflowID == "echo-video" {
			fmt.Fprint(w, `{"id":"r1","status":"succeeded","outputs":[{"index":0,"key":"u/o1/runs/r1/0.mp4","kind":"video","url":"/files/u/o1/runs/r1/0.mp4?exp=1&sig=ok","take_id":"t9",
			 "width":256,"height":128,"video":{"duration_s":2.125,"fps":8,"frames":17,"has_audio":true,"poster_url":"/files/u/o1/runs/r1/0.poster.png?exp=1&sig=ok"}}]}`)
			return
		}
		fmt.Fprint(w, `{"id":"r1","status":"succeeded","outputs":[{"index":0,"key":"u/o1/runs/r1/0.png","kind":"image","url":"/files/u/o1/runs/r1/0.png?exp=1&sig=ok","take_id":"t9"}]}`)
	case "GET /v1/studio/takes":
		f.query = r.URL.RawQuery
		fmt.Fprint(w, `{"takes":[`+takeJSON+`,`+videoTakeJSON+`],"next_cursor":"c2"}`)
	case "POST /v1/studio/uploads":
		var b struct {
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			Bytes       int    `json:"bytes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Filename != "pic.png" || b.ContentType != "image/png" || b.Bytes != len(png) {
			f.t.Errorf("upload create %+v", b)
		}
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"take_id":"t1","upload_url":"/files/u/o1/uploads/t1.png?exp=1&bytes=%d&ct=image%%2Fpng&sig=ok","content_type":"image/png","bytes":%[1]d,"expires_at":"2026-01-01T00:00:00Z"}`, b.Bytes)
	case "POST /v1/studio/uploads/t1/complete":
		if f.uploaded == nil {
			http.Error(w, `{"detail":"upload not received"}`, 409)
			return
		}
		fmt.Fprint(w, takeJSON)
	case "POST /v1/studio/takes/t1/use":
		f.useBody = nil
		_ = json.NewDecoder(r.Body).Decode(&f.useBody)
		if f.useBody["workflow_id"] != "echo" {
			http.Error(w, fmt.Sprintf(`{"detail":"workflow %s takes no image input"}`, f.useBody["workflow_id"]), 422)
			return
		}
		fmt.Fprint(w, `{"workflow_id":"echo","inputs":{"echo.refs":[{"image":{"take_id":"t1"}}]}}`)
	case "POST /v1/studio/batches":
		f.batchBody = nil
		_ = json.NewDecoder(r.Body).Decode(&f.batchBody)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"b1","title":"Echo × 2","status":"queued","counts":{"total":2}}`)
	case "GET /v1/studio/batches/b1/events":
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		var page []string
		for i, e := range f.batchEvents {
			if i+1 > since {
				page = append(page, fmt.Sprintf(`{"seq":%d,%s`, i+1, e[1:]))
			}
		}
		fmt.Fprintf(w, `{"events":[%s],"last_seq":%d}`, strings.Join(page, ","), max(since, len(f.batchEvents)))
	case "GET /v1/studio/batches":
		f.query = r.URL.RawQuery
		fmt.Fprint(w, `{"batches":[{"id":"b1","title":"sweep","status":"done","counts":{"total":6,"succeeded":5,"failed":1},"created_at":"2026-11-14T10:00:00Z"}],"next_cursor":null}`)
	case "POST /v1/studio/batches/b1/stop":
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"b1","status":"running","control":"stop","counts":{"total":6,"succeeded":2}}`)
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
		send("r1", "progress", `{"node":"echo","step":2,"of":2,"phase":"sampling","eta_s":1.2}`)
		send("r1", "output", `{"index":0,"key":"u/o1/runs/r1/0.png","kind":"image","url":"/x"}`)
		send("r1", "status", `{"status":"succeeded","error":null,"host":"fake","resolved":{"seed":7}}`)
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
	pollEvery = 10 * time.Millisecond
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
	for _, line := range []string{"session s1", "run r1", "queued", "running on fake", "echo 1/2", "echo 2/2 sampling (~1s left)", "output 0 image", "succeeded (seed 7)", "take t9"} {
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
	for _, p := range []string{"echo.steps=ten", "echo.fail=maybe", "enhance.bypass=off", "nodot=1", "echo.steps"} {
		if _, err := wf.inputs([]string{p}); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	var qwen workflow
	_ = json.Unmarshal([]byte(`{"nodes": {"seed": {"kind": "seed"}, "model": {"kind": "qwen-image/model"}}, "node_schemas": {"seed": {"params": [{"name": "seed", "type": "seed"}]},
		"qwen-image/model": {"params": [{"name": "variant", "type": "enum"}]}}}`), &qwen)
	seeds, err := qwen.inputs([]string{"seed.seed=random", "model.variant=turbo"})
	if v, ok := seeds["seed.seed"]; err != nil || !ok || v != nil || seeds["model.variant"] != "turbo" {
		t.Fatalf("random is null, an enum is text: %v %v", seeds, err)
	}
	if seeds, _ := qwen.inputs([]string{"seed.seed=42"}); seeds["seed.seed"] != int64(42) {
		t.Fatalf("%v", seeds)
	}
	if _, err := qwen.inputs([]string{"seed.seed=4.5"}); err == nil {
		t.Error("seed 4.5 accepted")
	}
	got, err := wf.inputs([]string{"echo.unknown=1", "prompt.text=2", "enhance.bypass=false"})
	if err != nil || got["echo.unknown"] != "1" || got["prompt.text"] != "2" || got["enhance.bypass"] != false {
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

func TestRunPrintsQueuePosition(t *testing.T) {
	f, _ := setup(t, "succeeded")
	f.positions = []int{3, 3, 2, 1}
	var out, errb bytes.Buffer
	if code := helm([]string{"run", "echo", "--out", t.TempDir()}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	if got := out.String(); !strings.Contains(got, "queued, position 3\nqueued, position 2\nqueued, position 1\nqueued\nrunning") {
		t.Fatalf("positions once each, then the stream:\n%s", got)
	}
}

func TestTakesFilters(t *testing.T) {
	f, _ := setup(t, "")
	var out, errb bytes.Buffer
	args := []string{"takes", "--kind", "image", "--source", "upload", "--starred", "--session", "s1", "--q", "red fox", "--limit", "2"}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("takes: %d %s", code, errb.String())
	}
	if f.query != "kind=image&limit=2&q=red+fox&session_id=s1&source=upload&starred=true" {
		t.Fatalf("query %q", f.query)
	}
	want := "t1 * image upload 3x2       -      2026-11-14T10:00:00Z pic\n" +
		"t2   video create 256x128   2.1s   2026-11-14T11:00:00Z a fox\nmore: --cursor c2\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestUpload(t *testing.T) {
	f, _ := setup(t, "")
	dir := t.TempDir()
	file := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(file, png, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := helm([]string{"upload", file}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("upload: %d %s", code, errb.String())
	}
	if !bytes.Equal(f.uploaded, png) || !strings.HasPrefix(out.String(), "t1 ") {
		t.Fatalf("uploaded %q, out %q", f.uploaded, out.String())
	}
	text := filepath.Join(dir, "notes.png")
	if err := os.WriteFile(text, []byte("just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := helm([]string{"upload", text}, nil, &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), "only png, jpeg and webp") {
		t.Fatalf("text upload: %d %s", code, errb.String())
	}
}

func TestRunUploadsReferences(t *testing.T) {
	f, _ := setup(t, "succeeded")
	file := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(file, png, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	args := []string{"run", "echo", "-p", "echo.refs=@" + file + ", t7", "--out", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	want := `[map[image:map[take_id:t1]] map[image:map[take_id:t7]]]`
	if !bytes.Equal(f.uploaded, png) || fmt.Sprint(f.inputs["echo.refs"]) != want {
		t.Fatalf("uploaded %q, refs %v", f.uploaded, f.inputs["echo.refs"])
	}
	if !strings.Contains(out.String(), "uploaded "+file+" as take t1\n") {
		t.Fatalf("out %s", out.String())
	}
	if code := helm([]string{"run", "echo", "-p", "echo.refs=", "--out", t.TempDir()}, nil, &out, &errb, nil); code != 0 ||
		fmt.Sprint(f.inputs["echo.refs"]) != "[]" {
		t.Fatalf("empty refs: %d %v", code, f.inputs["echo.refs"])
	}
}

func TestImageValues(t *testing.T) {
	var wf workflow
	_ = json.Unmarshal([]byte(`{"nodes":{"src":{"kind":"image"},"x":{"kind":"two"}},"node_schemas":{
		"image":{"params":[{"name":"image","type":"image"}]},
		"two":{"params":[{"name":"pairs","type":"rows","item":{"a":"Image","b":"Image"}},{"name":"words","type":"rows","item":{"w":{"type":"text"}}}]}}}`), &wf)
	got, err := wf.inputs([]string{"src.image=t3"})
	if err != nil || fmt.Sprint(got["src.image"]) != "map[take_id:t3]" {
		t.Fatalf("%v %v", got, err)
	}
	for _, p := range []string{"src.image=t1,t2", "src.image=", "x.pairs=t1", "x.words=t1", "src.image=@pic.png"} {
		if _, err := wf.inputs([]string{p}); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}

func TestUse(t *testing.T) {
	f, _ := setup(t, "")
	var out, errb bytes.Buffer
	if code := helm([]string{"use", "--param", "echo.refs", "t1", "echo"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("use: %d %s", code, errb.String())
	}
	if out.String() != "helm run echo -p echo.refs=t1\n" || f.useBody["param"] != "echo.refs" {
		t.Fatalf("out %q body %v", out.String(), f.useBody)
	}
	out.Reset()
	if code := helm([]string{"use", "t1", "qwen-image"}, nil, &out, &errb, nil); code != 1 ||
		!strings.Contains(errb.String(), "takes no image input") || f.useBody["param"] != nil {
		t.Fatalf("no image input: %d %s %v", code, errb.String(), f.useBody)
	}
	if code := helm([]string{"use", "t1"}, nil, &out, &errb, nil); code != 1 {
		t.Fatalf("usage: %d", code)
	}
}

func rowEvent(row, attempt int, status, extra string) string {
	return fmt.Sprintf(`{"type":"row","data":{"row":%d,"attempt":%d,"status":%q%s}}`, row, attempt, status, extra)
}

func batchEvent(status, counts string) string {
	return fmt.Sprintf(`{"type":"batch","data":{"id":"b1","status":%q,"reason":null,"counts":%s}}`, status, counts)
}

func TestBatchLinesPrintsRowsAndFails(t *testing.T) {
	f, _ := setup(t, "")
	f.batchEvents = []string{
		batchEvent("running", `{"total":2}`),
		rowEvent(1, 1, "running", ""),
		rowEvent(1, 1, "succeeded", `,"take_ids":["t1"]`),
		rowEvent(1, 1, "succeeded", `,"take_ids":["t1"]`), // the whole row again: printed once
		rowEvent(2, 1, "failed", `,"error":"echo failed on purpose"`),
		batchEvent("done", `{"total":2,"succeeded":1,"failed":1,"cancelled":0}`),
	}
	var out, errb bytes.Buffer
	args := []string{"batch", "echo", "--lines", "-", "-p", "echo.steps=2", "--seeds", "2", "--title", "foxes"}
	if code := helm(args, strings.NewReader("a red fox\r\n\n  \na blue heron\n"), &out, &errb, nil); code != 1 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	want := map[string]any{"workflow_id": "echo", "title": "foxes", "inputs": map[string]any{"echo.steps": 2.0},
		"rows":  map[string]any{"lines": map[string]any{"param": "prompt.text", "values": []any{"a red fox", "a blue heron"}}},
		"seeds": map[string]any{"count": 2.0}}
	if fmt.Sprint(f.batchBody) != fmt.Sprint(want) {
		t.Fatalf("body %v", f.batchBody)
	}
	wantOut := "batch b1: Echo × 2, 2 rows\nrunning\nrow 1 succeeded take t1\nrow 2 failed: echo failed on purpose\n" +
		"done: 1 succeeded, 1 failed, 0 cancelled of 2\n"
	if out.String() != wantOut || !strings.Contains(errb.String(), "1 of 2 rows failed") {
		t.Fatalf("out %q err %q", out.String(), errb.String())
	}
}

func TestBatchCSVAndGrid(t *testing.T) {
	f, _ := setup(t, "")
	f.batchEvents = []string{batchEvent("done", `{"total":2,"succeeded":2}`)}
	file := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(file, []byte("prompt.text,echo.steps,echo.fail\n\"a fox, red\",2,false\nheron,,true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := helm([]string{"batch", "echo", "--csv", file, "--session", "s1"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("csv: %d %s", code, errb.String())
	}
	table := `map[table:map[columns:[prompt.text echo.steps echo.fail] rows:[[a fox, red 2 false] [heron <nil> true]]]]`
	if fmt.Sprint(f.batchBody["rows"]) != table || f.batchBody["session_id"] != "s1" {
		t.Fatalf("csv body %v", f.batchBody)
	}
	if code := helm([]string{"batch", "echo", "--grid", "prompt.text=a,b", "--grid", "echo.steps=1,3"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("grid: %d %s", code, errb.String())
	}
	grid := `map[grid:map[axes:[map[param:prompt.text values:[a b]] map[param:echo.steps values:[1 3]]]]]`
	if fmt.Sprint(f.batchBody["rows"]) != grid {
		t.Fatalf("grid body %v", f.batchBody["rows"])
	}
	if steps := f.batchBody["rows"].(map[string]any)["grid"].(map[string]any)["axes"].([]any)[1].(map[string]any)["values"].([]any); steps[0] != 1.0 {
		t.Fatalf("grid values are typed: %#v", steps)
	}
}

func TestBatchRejectsBadArgs(t *testing.T) {
	setup(t, "")
	bad := filepath.Join(t.TempDir(), "bad.csv")
	if err := os.WriteFile(bad, []byte("echo.steps\nten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for args, want := range map[string]string{
		"batch echo": "exactly one of",
		"batch echo --lines - --grid echo.steps=1": "exactly one of",
		"batch echo --csv " + bad:                  "row 1: echo.steps: not a valid int",
		"batch echo --grid echo.steps":             "want node.param=a,b,c",
		"batch echo --grid echo.fail=yes,no":       "echo.fail: not a valid bool",
		"batch echo --lines - -p echo.steps=x":     "echo.steps: not a valid int",
	} {
		var out, errb bytes.Buffer
		if code := helm(strings.Fields(args), strings.NewReader("x\n"), &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), want) {
			t.Errorf("%s: %d %q", args, code, errb.String())
		}
	}
}

func TestBatchesAndStop(t *testing.T) {
	f, _ := setup(t, "")
	var out, errb bytes.Buffer
	if code := helm([]string{"batches", "--limit", "5"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("batches: %d %s", code, errb.String())
	}
	if want := "b1 done      5/6   failed 1   2026-11-14T10:00:00Z sweep\n"; out.String() != want || f.query != "limit=5" {
		t.Fatalf("got %q (query %q) want %q", out.String(), f.query, want)
	}
	out.Reset()
	if code := helm([]string{"batch-stop", "b1"}, nil, &out, &errb, nil); code != 0 || out.String() != "batch b1 stopping: 2 succeeded, 0 failed of 6\n" {
		t.Fatalf("stop: %d %q %s", code, out.String(), errb.String())
	}
}

func TestRunUserWorkflow(t *testing.T) {
	f, _ := setup(t, "succeeded")
	var out, errb bytes.Buffer
	if code := helm([]string{"run", mineID, "-p", "echo.steps=2", "--out", t.TempDir()}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	if f.workflowID != mineID || f.inputs["echo.steps"] != 2.0 {
		t.Fatalf("workflow %q inputs %v", f.workflowID, f.inputs)
	}
}
