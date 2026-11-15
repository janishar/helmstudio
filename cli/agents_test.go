package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const judgeReviewJSON = `{"batch_id":"b1","items":[
 {"row":1,"attempt":1,"inputs":{},"gate":"judge","gate_title":null,"visit":1,"keep":1,"on_reject":"fail","rejects_left":0,
  "kind":"judge","suggested":["t2"],"verdict":"pass","model":"Ask (fake)","decision_id":"b1:1:1:judge:1",
  "candidates":[
   {"take_id":"t1","url":"/x","thumb_url":"/y","width":64,"height":64,"seed":null,"judge":{"passes":false,"rank":2,"checks":[],"reason":"no subject"}},
   {"take_id":"t2","url":"/x","thumb_url":"/y","width":64,"height":64,"seed":null,"judge":{"passes":true,"rank":1,"checks":[],"reason":"fine"}}]}]}`

const decisionsJSON = `{"decisions":[
 {"id":"b1:1:1:judge:1","row":1,"node":"judge","kind":"judge","visit":1,"attempt":1,"replayed":false,"replayed_from":null,
  "run_ids":["r1"],"workflow":"echo-ask","template":"judge/v1","memory_read":["m1"],"error":null,
  "judge":{"mode":"suggest","verdict":"pass","picked":["t2"],"first_failed":null,"agreed":false,"user_pick":["t1"],
   "takes":[{"take_id":"t1","passes":false,"rank":2,"reason":"no subject"},{"take_id":"t2","passes":true,"rank":1,"reason":"fine"}]}},
 {"id":"b1:1:1:rewrite:1","row":1,"node":"rewrite","kind":"write","visit":1,"attempt":1,"replayed":true,"replayed_from":"b0:1:1:rewrite:1",
  "run_ids":[],"workflow":"echo-ask","template":"write/v1","memory_read":[],"error":null,
  "write":{"to":"$prompt","before":"a fox","after":"a red fox","note":""}},
 {"id":"b1:1:1:check:2","row":1,"node":"check","kind":"decide","visit":2,"attempt":1,"replayed":false,"replayed_from":null,
  "run_ids":["r3"],"workflow":"echo-ask","template":"decide/v1","memory_read":[],"error":null,
  "decide":{"route":"accept","to":"out","reason":"ok","forced":true}}]}`

const memoryJSON = `{"pipeline_id":"p1","counts":{"judge_example":1,"note":1},"items":[
 {"id":"m1","kind":"judge_example","pipeline_id":"p1","key":"judge","text":"warmer light","kept":[],"rejected":[],"agreed":false,
  "source":{"kind":"pick","batch_id":"b1","row":1,"node":"judge","visit":1},"created_at":"x","updated_at":"x"},
 {"id":"m2","kind":"note","pipeline_id":"p1","key":null,"text":"likes dusk","kept":[],"rejected":[],"agreed":null,
  "source":{"kind":"user","batch_id":null,"row":null,"node":null,"visit":null},"created_at":"x","updated_at":"x"}]}`

// agentsAPI serves the review, pick, decisions and memory routes and records each request as "METHOD uri body".
type agentsAPI struct {
	t    *testing.T
	reqs []string
}

func (f *agentsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.reqs = append(f.reqs, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(b)))
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/studio/batches/b1/review":
		fmt.Fprint(w, judgeReviewJSON)
	case "POST /v1/studio/batches/b1/rows/1/pick":
		w.WriteHeader(202)
		fmt.Fprint(w, `{"row":1,"attempt":1,"status":"running","take_ids":[]}`)
	case "GET /v1/studio/batches/b1/rows":
		fmt.Fprint(w, `{"rows":[{"row":1,"attempt":1,"status":"succeeded"},{"row":2,"attempt":1,"status":"succeeded"}],"next_cursor":null}`)
	case "GET /v1/studio/batches/b1/rows/1/decisions":
		fmt.Fprint(w, decisionsJSON)
	case "GET /v1/studio/batches/b1/rows/2/decisions":
		fmt.Fprint(w, `{"decisions":[{"id":"b1:2:1:check:1","row":2,"node":"check","kind":"decide","visit":1,"attempt":1,
		 "replayed":false,"replayed_from":null,"run_ids":["r9"],"workflow":"echo-ask","template":"decide/v1","memory_read":[],
		 "error":"check: answer did not match the schema: 'x' is not one of ['retry', 'accept']"}]}`)
	case "GET /v1/studio/memory":
		fmt.Fprint(w, memoryJSON)
	case "POST /v1/studio/memory":
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"m3","kind":"note","text":"t","source":{"kind":"user"}}`)
	case "DELETE /v1/studio/memory/m2":
		w.WriteHeader(204)
	case "DELETE /v1/studio/memory/nope":
		http.Error(w, `{"detail":"not found"}`, 404)
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"detail":"not found"}`, 404)
	}
}

func setupAgents(t *testing.T) *agentsAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	f := &agentsAPI{t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *agentsAPI) last() string { return f.reqs[len(f.reqs)-1] }

func TestReviewAndPickAtAJudge(t *testing.T) {
	f := setupAgents(t)
	want := "row 1 at judge (visit 1): pick 1 to 1; reject = fail\n" +
		"  judge (Ask (fake)): pass, suggests t2\n" +
		"  t1 seed - rank 2 fail: no subject\n" +
		"  t2 seed - rank 1 pass: fine  <- suggested\n"
	if got := helmOK(t, "batch", "review", "b1"); got != want {
		t.Fatalf("review %q", got)
	}
	if got := helmOK(t, "batch", "pick", "b1", "1", "t2"); got != "row 1 at judge: picked t2 (as the judge suggested); row running\n" ||
		f.last() != `POST /v1/studio/batches/b1/rows/1/pick {"gate":"judge","take_ids":["t2"]}` {
		t.Fatalf("agree %q %s", got, f.last())
	}
	got := helmOK(t, "batch", "pick", "b1", "1", "t1", "--note", "warmer light")
	if got != "row 1 at judge: picked t1 (the judge suggested t2); row running\n" ||
		f.last() != `POST /v1/studio/batches/b1/rows/1/pick {"gate":"judge","note":"warmer light","take_ids":["t1"]}` {
		t.Fatalf("differ %q %s", got, f.last())
	}
}

func TestBatchDecisions(t *testing.T) {
	f := setupAgents(t)
	row1 := "row 1 attempt 1 judge visit 1 (judge/v1, echo-ask) run r1; read 1 from memory: suggest pass, kept t2; you picked t1 (agreed: false)\n" +
		"  t1 rank 2 fail: no subject\n  t2 rank 1 pass: fine\n" +
		"row 1 attempt 1 rewrite visit 1 (write/v1, echo-ask) replayed from b0:1:1:rewrite:1: $prompt \"a fox\" -> \"a red fox\"\n" +
		"row 1 attempt 1 check visit 2 (decide/v1, echo-ask) run r3: accept -> out (forced: the loop's cap): ok\n"
	if got := helmOK(t, "batch", "decisions", "b1", "1"); got != row1 {
		t.Fatalf("row 1:\n%s", got)
	}
	all := helmOK(t, "batch", "decisions", "b1")
	if all != row1+"row 2 attempt 1 check visit 1 (decide/v1, echo-ask) run r9: error: check: answer did not match the schema: 'x' is not one of ['retry', 'accept']\n" {
		t.Fatalf("all:\n%s", all)
	}
	if f.reqs[len(f.reqs)-3] != "GET /v1/studio/batches/b1/rows" {
		t.Fatalf("rows: %v", f.reqs)
	}
	helmFails(t, "usage: helm batch decisions", "batch", "decisions")
	helmFails(t, `row "x": not a number`, "batch", "decisions", "b1", "x")
}

func TestMemory(t *testing.T) {
	f := setupAgents(t)
	want := "m1 judge_example judge      pick batch b1 row 1 agreed false: warmer light\n" +
		"m2 note          -          user: likes dusk\n1 judge examples, 1 notes\n"
	if got := helmOK(t, "memory", "list", "--pipeline", "p1"); got != want {
		t.Fatalf("list %q", got)
	}
	helmOK(t, "memory", "list", "--pipeline", "p1", "--kind", "note")
	if f.last() != "GET /v1/studio/memory?kind=note&pipeline_id=p1" {
		t.Fatalf("kind: %s", f.last())
	}
	if got := helmOK(t, "memory", "add", "--pipeline", "p1", "--key", "style", "likes", "dusk"); got != "m3\n" ||
		f.last() != `POST /v1/studio/memory {"key":"style","pipeline_id":"p1","text":"likes dusk"}` {
		t.Fatalf("add %q %s", got, f.last())
	}
	if got := helmOK(t, "memory", "rm", "m2"); got != "deleted m2\n" {
		t.Fatalf("rm %q", got)
	}
	helmFails(t, "404: not found", "memory", "rm", "nope")
	for _, args := range [][]string{{"memory"}, {"memory", "list"}, {"memory", "add", "--pipeline", "p1"}, {"memory", "rm"}} {
		helmFails(t, "usage: helm memory", args...)
	}
}
