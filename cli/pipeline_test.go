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
	"testing"
	"time"
)

const pipeID = "7a2b3c4d-0b7d-4c8e-9a51-3d2e1f0a9b88"

const pipelineJSON = `{"id":"` + pipeID + `","title":"Fox at night","version":2,"based_on":null,
 "created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z","workflow_titles":{"echo":"Echo"},
 "last_batch":{"id":"b1","status":"waiting"},"problems":[],
 "nodes":{"inputs":{"kind":"inputs","inputs":[{"name":"steps","type":"Int"},{"name":"prompt","type":"Text"}]},
  "s1":{"kind":"step","workflow":"echo","bind":{"prompt.text":"$prompt","echo.steps":"$steps"},"inputs":{"echo.delay_ms":50}},
  "review":{"kind":"gate","takes":"s1"},"out":{"kind":"output","from":"review.picked","collection":"Fox"}},
 "edges":[["inputs","s1"],["s1","review"],["review",{"pass":"out","fail":"s1","max":2,"then":"out"}]],
 "remote":{"enabled":true,"providers":["deepseek"]}}`

const reviewJSON = `{"batch_id":"b1","items":[
 {"row":2,"attempt":1,"inputs":{},"gate":"review","gate_title":null,"visit":1,"keep":1,"on_reject":"retry","rejects_left":2,
  "candidates":[{"take_id":"t2","url":"/x","thumb_url":"/y","width":64,"height":64,"seed":null}]},
 {"row":3,"attempt":1,"inputs":{},"gate":"review","gate_title":null,"visit":3,"keep":2,"on_reject":"then","rejects_left":0,
  "candidates":[{"take_id":"t3","url":"/x","thumb_url":"/y","width":64,"height":64,"seed":41},{"take_id":"t4","url":"/x","thumb_url":"/y","width":64,"height":64,"seed":42}]}]}`

// pipelineAPI serves the pipeline and gate routes and records each request as "METHOD path body".
type pipelineAPI struct {
	t      *testing.T
	reqs   []string
	events []string
}

func (f *pipelineAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	route := r.Method + " " + r.URL.Path
	f.reqs = append(f.reqs, strings.TrimSpace(route+" "+string(b)))
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	switch route {
	case "GET /v1/studio/pipelines":
		fmt.Fprint(w, `{"pipelines":[`+pipelineJSON+`,{"id":"p2","title":"New","version":1,"last_batch":null}]}`)
	case "GET /v1/studio/pipelines/" + pipeID:
		fmt.Fprint(w, pipelineJSON)
	case "PUT /v1/studio/pipelines/" + pipeID:
		if body["version"] != 2.0 {
			http.Error(w, `{"detail":"pipeline changed since you opened it (now version 2)"}`, 409)
			return
		}
		fmt.Fprint(w, `{"id":"`+pipeID+`","version":3,"problems":[]}`)
	case "POST /v1/studio/pipelines":
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"p3","version":1,"problems":[]}`)
	case "POST /v1/studio/pipelines/validate":
		fmt.Fprint(w, `{"can_save":false,"can_run":false,"problems":[
		 {"code":"uncapped_cycle","message":"s1 reaches itself with no capped edge","blocks":"save","node":"s1"}]}`)
	case "DELETE /v1/studio/pipelines/" + pipeID:
		http.Error(w, `{"detail":"a batch of this pipeline is unfinished; stop it or wait"}`, 409)
	case "POST /v1/studio/batches":
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"b1","title":"Fox at night × 2","status":"queued","counts":{"total":2}}`)
	case "GET /v1/studio/batches/b1/events":
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		var page []string
		for i, e := range f.events {
			if i+1 > since {
				page = append(page, fmt.Sprintf(`{"seq":%d,%s`, i+1, e[1:]))
			}
		}
		fmt.Fprintf(w, `{"events":[%s],"last_seq":%d}`, strings.Join(page, ","), max(since, len(f.events)))
	case "GET /v1/studio/batches/b1/review":
		fmt.Fprint(w, reviewJSON)
	case "POST /v1/studio/batches/b1/rows/2/pick", "POST /v1/studio/batches/b1/rows/3/pick":
		w.WriteHeader(202)
		fmt.Fprint(w, `{"row":2,"attempt":1,"status":"running","take_ids":[]}`)
	default:
		f.t.Errorf("unexpected %s", route)
		http.Error(w, `{"detail":"not found"}`, 404)
	}
}

func setupPipelines(t *testing.T) *pipelineAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	pollEvery = 10 * time.Millisecond
	f := &pipelineAPI{t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *pipelineAPI) last() string { return f.reqs[len(f.reqs)-1] }

func TestPipelinesListAndFiles(t *testing.T) {
	f := setupPipelines(t)
	want := fmt.Sprintf("%-36s v2   waiting  Fox at night\n%-36s v1   -        New\n", pipeID, "p2")
	if got := helmOK(t, "pipelines"); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	file := filepath.Join(t.TempDir(), "p.json")
	_ = os.WriteFile(file, []byte(helmOK(t, "pipeline", "get", pipeID)), 0o600)
	doc, id, version, _, err := pipelineKind.readFile(file)
	if err != nil || id != pipeID || version != 2 || len(doc) != 4 || doc["edges"] == nil {
		t.Fatalf("file: %v %q %d %v", doc, id, version, err)
	}
	if got := helmOK(t, "pipeline", "save", file); got != "saved "+pipeID+" version 3\n" {
		t.Fatalf("save: %q", got)
	}
	if !strings.HasPrefix(f.last(), "PUT /v1/studio/pipelines/"+pipeID+" {") || strings.Contains(f.last(), `"id"`) ||
		strings.Contains(f.last(), "workflow_titles") || !strings.Contains(f.last(), `"remote":{"enabled":true`) {
		t.Fatalf("PUT: %s", f.last())
	}
	stale := strings.Replace(string(must(os.ReadFile(file))), `"version": 2`, `"version": 1`, 1)
	_ = os.WriteFile(file, []byte(stale), 0o600)
	helmFails(t, "409: pipeline changed since you opened it", "pipeline", "save", file)

	_ = os.WriteFile(file, []byte(`{"title":"t","nodes":{"s1":{"kind":"step","workflow":"echo"}},"edges":[["s1","s1"]]}`), 0o600)
	out := helmFails(t, "1 problems: it cannot be saved", "pipeline", "validate", file)
	if out != "save s1                   uncapped_cycle: s1 reaches itself with no capped edge\n" {
		t.Fatalf("validate: %q", out)
	}
	if got := helmOK(t, "pipeline", "save", file); !strings.HasPrefix(got, "saved p3 version 1\nto keep editing it: helm pipeline get p3 > ") {
		t.Fatalf("create: %q", got)
	}
	if got := helmOK(t, "pipeline", "copy", pipeID, "--title", "Again"); got != "p3\n" ||
		f.last() != `POST /v1/studio/pipelines {"based_on":"`+pipeID+`","title":"Again"}` {
		t.Fatalf("copy: %q %s", got, f.last())
	}
	helmFails(t, "409: a batch of this pipeline is unfinished", "pipeline", "rm", pipeID)
}

func TestBatchOfPipeline(t *testing.T) {
	f := setupPipelines(t)
	f.events = []string{
		batchEvent("running", `{"total":2}`),
		batchEvent("waiting", `{"total":2,"waiting":2}`),
		rowEvent(1, 1, "succeeded", `,"take_ids":["t9"]`),
		rowEvent(2, 1, "succeeded", `,"take_ids":["t8"]`),
		batchEvent("done", `{"total":2,"succeeded":2,"failed":0,"cancelled":0}`),
	}
	var out, errb bytes.Buffer
	args := []string{"batch", "--pipeline", pipeID, "--lines", "-", "-p", "inputs.steps=3"}
	if code := helm(args, strings.NewReader("a fox\na heron\n"), &out, &errb, nil); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	var sent map[string]any
	for _, r := range f.reqs {
		if b, ok := strings.CutPrefix(r, "POST /v1/studio/batches "); ok {
			_ = json.Unmarshal([]byte(b), &sent)
		}
	}
	want := `map[inputs:map[inputs.steps:3] pipeline_id:` + pipeID + ` rows:map[lines:map[param:inputs.prompt values:[a fox a heron]]]]`
	if fmt.Sprint(sent) != want {
		t.Fatalf("body %v", sent)
	}
	wantOut := "batch b1: Fox at night × 2, 2 rows\nrunning\nwaiting: 2 rows wait for a pick (helm batch review b1)\n" +
		"row 1 succeeded take t9\nrow 2 succeeded take t8\ndone: 2 succeeded, 0 failed, 0 cancelled of 2\n"
	if out.String() != wantOut {
		t.Fatalf("out %q", out.String())
	}
	// inputs are typed by the pipeline's input types; a workflow id and --pipeline together is a usage error
	helmFails(t, "inputs.steps: not a valid int", "batch", "--pipeline", pipeID, "--grid", "inputs.steps=a,b")
	if code := helm([]string{"batch", "echo", "--pipeline", pipeID, "--lines", "-"}, strings.NewReader("x\n"), &out, &errb, nil); code != 2 {
		t.Fatalf("both: %d", code)
	}
}

func TestBatchReviewAndPick(t *testing.T) {
	f := setupPipelines(t)
	want := "row 2 at review (visit 1): pick 1 to 1; reject = retry, 2 left\n  t2 seed -\n" +
		"row 3 at review (visit 3): pick 1 to 2; reject = then\n  t3 seed 41\n  t4 seed 42\n"
	if got := helmOK(t, "batch", "review", "b1"); got != want {
		t.Fatalf("review %q", got)
	}
	if got := helmOK(t, "batch", "pick", "b1", "3", "t4", "t3"); got != "row 3 at review: picked t4,t3; row running\n" ||
		f.last() != `POST /v1/studio/batches/b1/rows/3/pick {"gate":"review","take_ids":["t4","t3"]}` {
		t.Fatalf("pick %q %s", got, f.last())
	}
	if got := helmOK(t, "batch", "pick", "b1", "2", "--reject"); got != "row 2 at review: rejected all; row running\n" ||
		f.last() != `POST /v1/studio/batches/b1/rows/2/pick {"gate":"review","reject":true}` {
		t.Fatalf("reject %q %s", got, f.last())
	}
	helmFails(t, "row 1 is not waiting for a pick", "batch", "pick", "b1", "1", "t1")
	for _, args := range [][]string{{"batch", "pick", "b1", "2"}, {"batch", "pick", "b1", "2", "t2", "--reject"}, {"batch", "review"}} {
		helmFails(t, "usage: helm batch", args...)
	}
}
