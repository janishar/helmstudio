package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCatalogCopyIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../contracts/tools/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(toolCatalogJSON, want) {
		t.Fatal("make gen: gen/catalog.json differs from contracts/tools/catalog.json")
	}
}

// mcpSession logs in to the fake API and connects an MCP client to helm mcp's server in memory. progress gets every
// progress message the server sends.
func mcpSession(t *testing.T, outcome string) (*fakeAPI, *mcp.ClientSession, *[]string) {
	f, _ := setup(t, outcome)
	f.extra = map[string]http.HandlerFunc{}
	c, err := loggedIn()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newMCPServer(c).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	progress := &[]string{}
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			*progress = append(*progress, r.Params.Message)
			mu.Unlock()
		},
	})
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return f, cs, progress
}

func call(t *testing.T, cs *mcp.ClientSession, name string, in map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	p := &mcp.CallToolParams{Name: name, Arguments: in}
	p.SetProgressToken("p1")
	res, err := cs.CallTool(context.Background(), p)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out map[string]any
	if !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res, out
}

func text(res *mcp.CallToolResult) string {
	var s []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			s = append(s, tc.Text)
		}
	}
	return strings.Join(s, "\n")
}

func TestMCPToolsAreTheCatalogs(t *testing.T) {
	_, cs, _ := mcpSession(t, "")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cat := mcpCatalog()
	got := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl
	}
	for name, ct := range cat {
		tl, ok := got[name]
		if ok != slices.Contains(ct.Surfaces, "mcp") {
			t.Errorf("%s: registered %v, surfaces %v", name, ok, ct.Surfaces)
			continue
		}
		if !ok {
			continue
		}
		var want, have any
		_ = json.Unmarshal(ct.Input, &want)
		b, _ := json.Marshal(tl.InputSchema)
		_ = json.Unmarshal(b, &have)
		if fmt.Sprint(want) != fmt.Sprint(have) {
			t.Errorf("%s: input schema %v, catalog %v", name, have, want)
		}
		a := tl.Annotations
		destructive := a.DestructiveHint != nil && *a.DestructiveHint
		if a.ReadOnlyHint != ct.ReadOnly || (!ct.ReadOnly && a.DestructiveHint == nil) || destructive != ct.Destructive {
			t.Errorf("%s: annotations %+v, catalog read_only %v destructive %v", name, a, ct.ReadOnly, ct.Destructive)
		}
	}
	for _, name := range []string{"delete_take", "delete_run", "delete_session", "edit_timeline", "restore_timeline"} {
		if a := got[name].Annotations; a.ReadOnlyHint || !*a.DestructiveHint {
			t.Errorf("%s: not destructive: %+v", name, a)
		}
	}
	for _, name := range []string{"try_timeline_edit", "timeline_frame", "timeline_revisions", "show_timeline"} { // API 1.49: never asks
		if a := got[name].Annotations; !a.ReadOnlyHint {
			t.Errorf("%s: not read-only: %+v", name, a)
		}
	}
	for _, name := range []string{"sessions", "get_run", "models"} { // the assistant's
		if got[name] != nil {
			t.Errorf("%s is not an mcp tool", name)
		}
	}
}

func TestMCPRunWaitAndTake(t *testing.T) {
	f, cs, progress := mcpSession(t, "succeeded")
	f.positions = []int{1} // the first GET of r1 is still queued, so wait_run follows the stream
	_, out := call(t, cs, "run_workflow", map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "a fox", "echo.steps": 2}})
	if out["id"] != "r1" || f.workflowID != "echo" || fmt.Sprint(f.inputs) != "map[echo.steps:2 prompt.text:a fox]" {
		t.Fatalf("run %v inputs %v", out, f.inputs)
	}
	dir := t.TempDir()
	res, out := call(t, cs, "wait_run", map[string]any{"run_id": "r1", "out_dir": dir})
	run, _ := out["run"].(map[string]any)
	if res.IsError || out["still_running"] != false || run["status"] != "succeeded" {
		t.Fatalf("wait_run %v %s", out, text(res))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "r1_0.png")); err != nil || !bytes.Equal(b, png) || fmt.Sprint(out["saved"]) != fmt.Sprintf("[%s]", filepath.Join(dir, "r1_0.png")) {
		t.Fatalf("saved %v: %v", out["saved"], err)
	}
	if want := []string{"running on fake", "echo 1/2", "succeeded (seed 7)"}; !slices.ContainsFunc(*progress, func(s string) bool { return s == want[1] }) ||
		!slices.Contains(*progress, want[0]) || !slices.Contains(*progress, want[2]) {
		t.Fatalf("progress %q", *progress)
	}

	take := `{"id":"t9","kind":"image","thumb_url":"/files/u/o1/runs/r1/0.png?exp=1&sig=ok"}`
	f.extra["GET /v1/studio/takes/t9"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, take) }
	f.extra["GET /v1/studio/takes/t9/uses"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"uses":[]}`) }
	f.extra["GET /v1/studio/takes/t9/decisions"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"decisions":[]}`) }
	res, out = call(t, cs, "get_take", map[string]any{"take_id": "t9"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("get_take %s", text(res))
	}
	if img, ok := res.Content[1].(*mcp.ImageContent); !ok || img.MIMEType != "image/png" || !bytes.Equal(img.Data, png) {
		t.Fatalf("get_take image %#v", res.Content[1])
	}
	if tk, _ := out["take"].(map[string]any); tk["id"] != "t9" {
		t.Fatalf("get_take %v", out)
	}

	r, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "helm://take/t9"})
	if err != nil || len(r.Contents) != 2 || r.Contents[0].Text != take || r.Contents[1].MIMEType != "image/png" {
		t.Fatalf("resource %v %v", r, err)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "helm://workflow/echo"}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPErrorsAreTheDetail(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	f.extra["GET /v1/studio/takes/aaaaaaaa-0000-4000-8000-000000000000"] = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"take not found"}`, 404)
	}
	res, _ := call(t, cs, "get_take", map[string]any{"take_id": "aaaaaaaa-0000-4000-8000-000000000000"})
	if !res.IsError || !strings.Contains(text(res), "take not found") {
		t.Fatalf("got %v %q", res.IsError, text(res))
	}
	res, _ = call(t, cs, "get_take", map[string]any{"nope": 1}) // checked against the catalog schema first
	if !res.IsError || !strings.Contains(text(res), `"nope"`) {
		t.Fatalf("schema: %v %q", res.IsError, text(res))
	}
	f.extra["GET /v1/studio/takes/gone"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"detail":"not found"}`, 404) }
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "helm://take/gone"}); err == nil {
		t.Fatal("a missing take's resource read succeeded")
	}
}

func TestMCPQueueCap(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	var mu sync.Mutex
	n, status := 0, map[string]string{}
	f.extra["POST /v1/studio/sessions/s1/runs"] = func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		n++
		id := fmt.Sprintf("q%d", n)
		status[id] = "queued"
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"id":%q,"status":"queued","outputs":[]}`, id)
	}
	for i := 1; i <= maxQueued+1; i++ {
		id := fmt.Sprintf("q%d", i)
		f.extra["GET /v1/studio/runs/"+id] = func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(w, `{"id":%q,"status":%q,"outputs":[]}`, id, status[id])
		}
	}
	for range maxQueued {
		if res, _ := call(t, cs, "run_workflow", map[string]any{"workflow_id": "echo"}); res.IsError {
			t.Fatal(text(res))
		}
	}
	res, _ := call(t, cs, "run_workflow", map[string]any{"workflow_id": "echo"})
	if !res.IsError || !strings.Contains(text(res), "at most 4") || n != maxQueued {
		t.Fatalf("5th run: %v %q (%d sent)", res.IsError, text(res), n)
	}
	mu.Lock()
	status["q2"] = "succeeded"
	mu.Unlock()
	if res, _ := call(t, cs, "run_workflow", map[string]any{"workflow_id": "echo"}); res.IsError {
		t.Fatalf("after one ended: %s", text(res))
	}
}

func TestMCPWrites(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	var got []string
	record := func(answer string, status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			got = append(got, fmt.Sprintf("%s %s %v", r.Method, r.URL.Path, b))
			w.WriteHeader(status)
			fmt.Fprint(w, answer)
		}
	}
	for _, p := range []string{"DELETE /v1/studio/takes/", "DELETE /v1/studio/runs/", "DELETE /v1/studio/sessions/"} {
		f.extra[p+id] = record("", 204)
	}
	f.extra["GET /v1/studio/batches/"+id+"/review"] = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"row":2,"gate":"keep","candidates":[]}]}`)
	}
	f.extra["POST /v1/studio/batches/"+id+"/rows/2/pick"] = record(`{"row":2,"status":"running"}`, 202)
	f.extra["POST /v1/studio/batches/"+id+"/chat"] = record(`{"id":"m1"}`, 202)
	f.extra["POST /v1/studio/timelines"] = record(`{"id":"tl1"}`, 201)
	f.extra["POST /v1/studio/timelines/"+id+"/clips"] = record(`{"id":"tl2"}`, 200)
	f.extra["POST /v1/studio/runs/"+id+"/cancel"] = record(`{"id":"r","status":"cancelled"}`, 202)
	f.extra["POST /v1/studio/timelines/"+id+"/export/plan"] = record(`{"revision":3}`, 200)
	f.extra["POST /v1/studio/timelines/"+id+"/export"] = record(`{"id":"j1","status":"queued"}`, 202)
	calls := []struct {
		tool string
		in   map[string]any
	}{
		{"delete_take", map[string]any{"take_id": id}},
		{"delete_run", map[string]any{"run_id": id}},
		{"delete_session", map[string]any{"session_id": id}},
		{"pick", map[string]any{"batch_id": id, "row": 2, "take_ids": []string{id}, "note": "sharper"}},
		{"batch_chat", map[string]any{"batch_id": id, "text": "two more rows"}},
		{"add_to_timeline", map[string]any{"take_ids": []string{id}, "name": "cut"}},
		{"add_to_timeline", map[string]any{"timeline_id": id, "take_ids": []string{id}, "track": "A2"}},
		{"cancel_run", map[string]any{"run_id": id}},
		{"export_timeline", map[string]any{"timeline_id": id}},
	}
	for _, c := range calls {
		if res, _ := call(t, cs, c.tool, c.in); res.IsError {
			t.Fatalf("%s: %s", c.tool, text(res))
		}
	}
	want := []string{
		"DELETE /v1/studio/takes/" + id + " map[]",
		"DELETE /v1/studio/runs/" + id + " map[]",
		"DELETE /v1/studio/sessions/" + id + " map[]",
		"POST /v1/studio/batches/" + id + "/rows/2/pick map[gate:keep note:sharper take_ids:[" + id + "]]",
		"POST /v1/studio/batches/" + id + "/chat map[text:two more rows]",
		"POST /v1/studio/timelines map[name:cut take_ids:[" + id + "]]",
		"POST /v1/studio/timelines/" + id + "/clips map[take_ids:[" + id + "] track:A2]",
		"POST /v1/studio/runs/" + id + "/cancel map[]",
		"POST /v1/studio/timelines/" + id + "/export/plan map[]",
		"POST /v1/studio/timelines/" + id + "/export map[revision:3]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	res, _ := call(t, cs, "pick", map[string]any{"batch_id": id, "row": 5, "reject": true})
	if !res.IsError || !strings.Contains(text(res), "row 5 is not waiting") {
		t.Fatalf("pick row 5: %q", text(res))
	}
}

// Tool calls run concurrently: parallel run_workflow calls still start at most maxQueued runs.
func TestMCPQueueCapUnderParallelCalls(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	var mu sync.Mutex
	n := 0
	f.extra["POST /v1/studio/sessions/s1/runs"] = func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond) // every call checks the cap before any run is counted, without the lock
		mu.Lock()
		n++
		id := fmt.Sprintf("q%d", n)
		mu.Unlock()
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"id":%q,"status":"queued","outputs":[]}`, id)
	}
	for i := 1; i <= maxQueued+2; i++ {
		id := fmt.Sprintf("q%d", i)
		f.extra["GET /v1/studio/runs/"+id] = func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"id":%q,"status":"queued","outputs":[]}`, id)
		}
	}
	var wg sync.WaitGroup
	for range maxQueued + 2 {
		wg.Go(func() {
			_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_workflow", Arguments: map[string]any{"workflow_id": "echo"}})
		})
	}
	wg.Wait()
	if n != maxQueued {
		t.Fatalf("%d runs started, at most %d", n, maxQueued)
	}
}

func TestMCPLocalPaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{"": "", "~": home, "~/Desktop/a.mp4": filepath.Join(home, "Desktop/a.mp4"), "/tmp/x/../y": "/tmp/y"} {
		if got, err := (args{"p": in}).localPath("p"); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"out", "./out", "~bob/x"} {
		if _, err := (args{"p": in}).localPath("p"); err == nil {
			t.Errorf("%q: a relative path was taken", in)
		}
	}
	_, cs, _ := mcpSession(t, "")
	res, _ := call(t, cs, "wait_run", map[string]any{"run_id": "aaaaaaaa-0000-4000-8000-000000000001", "out_dir": "takes"})
	if !res.IsError || !strings.Contains(text(res), "absolute") {
		t.Fatalf("wait_run relative out_dir: %q", text(res))
	}
}

func TestMCPPickNeverSendsRejectFalse(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	f.extra["GET /v1/studio/batches/"+id+"/review"] = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"row":2,"gate":"keep","candidates":[]}]}`)
	}
	var body map[string]any
	f.extra["POST /v1/studio/batches/"+id+"/rows/2/pick"] = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"row":2}`)
	}
	if res, _ := call(t, cs, "pick", map[string]any{"batch_id": id, "row": 2, "take_ids": []string{id}, "reject": false}); res.IsError {
		t.Fatal(text(res))
	}
	if _, ok := body["reject"]; ok {
		t.Fatalf("sent %v: the API's reject is const true", body)
	}
}

// batch_status reads the chat only for a batch with a director: the chat is 404 for a plain workflow batch.
func TestMCPBatchStatusChatOnlyWithDirector(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	director := "null"
	f.extra["GET /v1/studio/batches/"+id] = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"status":"done","director":%s}`, id, director)
	}
	f.extra["GET /v1/studio/batches/"+id+"/rows"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"rows":[]}`) }
	f.extra["GET /v1/studio/batches/"+id+"/review"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"items":[]}`) }
	res, out := call(t, cs, "batch_status", map[string]any{"batch_id": id})
	if res.IsError || out["batch"] == nil || out["chat"] != nil {
		t.Fatalf("no director: %v %q %v", res.IsError, text(res), out)
	}
	director = `{"state":"idle"}`
	f.extra["GET /v1/studio/batches/"+id+"/chat"] = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"messages":[]}`) }
	if res, out = call(t, cs, "batch_status", map[string]any{"batch_id": id}); res.IsError || out["chat"] == nil {
		t.Fatalf("with a director: %v %q %v", res.IsError, text(res), out)
	}
}

// API 1.49 (plan 24): the timeline edit tools call the API with X-Helm-Client mcp; timeline_frame answers the image.
func TestMCPTimelineEdits(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	base := "/v1/studio/timelines/" + id
	var got []string
	record := func(answer string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			got = append(got, fmt.Sprintf("%s %s?%s %v %s", r.Method, r.URL.Path, r.URL.RawQuery, b, r.Header.Get("X-Helm-Client")))
			if strings.HasSuffix(r.URL.Path, "/edits") && b["ops"].([]any)[0].(map[string]any)["clip"] == "zz" {
				w.WriteHeader(422)
				fmt.Fprint(w, `{"detail":"ops[0]: unknown clip zz","op_index":0,"code":"unknown_clip","problems":[]}`)
				return
			}
			fmt.Fprint(w, answer)
		}
	}
	f.extra["GET "+base] = record(`{"id":"` + id + `","clips":[]}`)
	f.extra["GET "+base+"/exports"] = record(`{"jobs":[]}`)
	f.extra["POST "+base+"/edits"] = record(`{"revision":5,"diff":{"added":["k9"]}}`)
	f.extra["GET "+base+"/revisions"] = record(`{"revisions":[]}`)
	f.extra["GET "+base+"/revisions/3"] = record(`{"revision":3}`)
	f.extra["POST "+base+"/restore"] = record(`{"revision":6}`)
	f.extra["POST "+base+"/frame"] = record(`{"empty":false,"shown":"first","url":"/files/u/o1/runs/r1/0.png?exp=1&sig=ok"}`) // the fake API's png
	ops := []any{map[string]any{"op": "split", "track": "V1", "at": 1}}
	calls := []struct {
		tool string
		in   map[string]any
	}{
		{"show_timeline", map[string]any{"timeline_id": id}},
		{"try_timeline_edit", map[string]any{"timeline_id": id, "base_revision": 4, "ops": ops}},
		{"edit_timeline", map[string]any{"timeline_id": id, "base_revision": 4, "ops": ops}},
		{"timeline_revisions", map[string]any{"timeline_id": id, "before": 5}},
		{"timeline_revisions", map[string]any{"timeline_id": id, "revision": 3}},
		{"restore_timeline", map[string]any{"timeline_id": id, "revision": 5, "to": 3}},
	}
	for _, c := range calls {
		if res, _ := call(t, cs, c.tool, c.in); res.IsError {
			t.Fatalf("%s: %s", c.tool, text(res))
		}
	}
	res, out := call(t, cs, "timeline_frame", map[string]any{"timeline_id": id, "at": 0.5})
	images := 0
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok && img.MIMEType == "image/png" && bytes.Equal(img.Data, png) {
			images++
		}
	}
	if res.IsError || images != 1 || out["shown"] != "first" {
		t.Fatalf("frame: %v %s", out, text(res))
	}
	want := []string{
		"GET " + base + "?view=agent map[] mcp",
		"GET " + base + "/exports? map[] mcp",
		"POST " + base + "/edits? map[base_revision:4 dry_run:true ops:[map[at:1 op:split track:V1]]] mcp",
		"POST " + base + "/edits? map[base_revision:4 dry_run:false ops:[map[at:1 op:split track:V1]]] mcp",
		"GET " + base + "/revisions?before=5&limit=10 map[] mcp",
		"GET " + base + "/revisions/3? map[] mcp",
		"POST " + base + "/restore? map[revision:5 to:3] mcp",
		"POST " + base + "/frame? map[at:0.5] mcp",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// an op that cannot apply: the API's detail as a tool error
	res, _ = call(t, cs, "try_timeline_edit", map[string]any{"timeline_id": id, "base_revision": 4,
		"ops": []any{map[string]any{"op": "remove", "clip": "zz"}}})
	if !res.IsError || !strings.Contains(text(res), "422: ops[0]: unknown clip zz") {
		t.Fatalf("422: %s", text(res))
	}
	// the catalog refuses fill before any call (not one of the ops)
	res, _ = call(t, cs, "edit_timeline", map[string]any{"timeline_id": id, "base_revision": 4,
		"ops": []any{map[string]any{"op": "fill", "clip": "a", "take_id": id}}})
	if !res.IsError || len(got) != 9 {
		t.Fatalf("fill: %v %s", res.IsError, text(res))
	}
}

// A hung API fails a plain call after apiTimeout, and the MCP client's cancel stops the request at once.
func TestMCPHungAPI(t *testing.T) {
	f, cs, _ := mcpSession(t, "")
	gone := make(chan struct{}, 4) // one per request the server saw end
	f.extra["GET /v1/studio/timelines"] = func(_ http.ResponseWriter, r *http.Request) {
		select { // never answers
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		gone <- struct{}{}
	}
	defer func(d time.Duration) { apiTimeout = d }(apiTimeout)
	apiTimeout = 200 * time.Millisecond
	start := time.Now()
	res, _ := call(t, cs, "list_timelines", map[string]any{})
	if !res.IsError || time.Since(start) > 5*time.Second {
		t.Fatalf("hung call: error %v after %v", res.IsError, time.Since(start))
	}
	<-gone

	apiTimeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_timelines", Arguments: map[string]any{}}); err == nil {
		t.Fatal("a cancelled call succeeded")
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the client's cancel did not stop the API request")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
}
