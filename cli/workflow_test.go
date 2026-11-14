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
	"strings"
	"testing"
)

const mineID = "6f1c2a4e-0b7d-4c8e-9a51-3d2e1f0a9b88"

const mineWorkflow = `{"id":"` + mineID + `","title":"My echo","family":"echo","env":"fake","source":"mine","version":3,
 "based_on":"echo","created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z","problems":[],
 "nodes":{"prompt":{"kind":"prompt"},"echo":{"kind":"echo/echo","in":{"text":"prompt.text"},"position":{"x":1,"y":2}},"output":{"kind":"output","in":{"media":"echo.image"}}},
 "studio":{"composer":["prompt.text"]},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text"}]},"echo/echo":{"params":[{"name":"steps","type":"int"}]},"output":{"params":[]}}}`

// workflowAPI serves the workflow routes and records each request as "METHOD path body".
type workflowAPI struct {
	t    *testing.T
	reqs []string
}

func (f *workflowAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") != "helm_test" || r.Header.Get("Authorization") != "Bearer at1" {
		http.Error(w, `{"detail":"invalid or expired token"}`, 401)
		return
	}
	b, _ := io.ReadAll(r.Body)
	route := r.Method + " " + r.URL.Path
	f.reqs = append(f.reqs, strings.TrimSpace(route+" "+string(b)))
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	switch route {
	case "GET /v1/studio/workflows":
		fmt.Fprint(w, `{"workflows":[`+echoWorkflow[:len(echoWorkflow)-1]+`,"source":"template"},`+mineWorkflow+`]}`)
	case "GET /v1/studio/workflows/echo":
		fmt.Fprint(w, echoWorkflow[:len(echoWorkflow)-1]+`,"source":"template"}`)
	case "GET /v1/studio/workflows/" + mineID:
		fmt.Fprint(w, mineWorkflow)
	case "PUT /v1/studio/workflows/" + mineID:
		if body["version"] != 3.0 {
			http.Error(w, `{"detail":"workflow changed since you opened it (now version 3)"}`, 409)
			return
		}
		fmt.Fprint(w, `{"id":"`+mineID+`","version":4,"problems":[{"code":"no_output","message":"no output node","blocks":"run"}]}`)
	case "POST /v1/studio/workflows":
		if body["nodes"] != nil && body["title"] == "" {
			w.WriteHeader(422)
			fmt.Fprint(w, `{"detail":"workflow has 1 problems: echo.steps: must be between 1 and 200",
			 "problems":[{"code":"param_invalid","message":"echo.steps: must be between 1 and 200","blocks":"save","node":"echo","param":"steps"}]}`)
			return
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"new1","version":1,"problems":[]}`)
	case "POST /v1/studio/workflows/validate":
		fmt.Fprint(w, `{"can_save":true,"can_run":false,"problems":[
		 {"code":"no_output","message":"the workflow has no output node","blocks":"run"},
		 {"code":"required_input","message":"echo.text: not wired","blocks":"run","node":"echo","port":"text"}]}`)
	case "DELETE /v1/studio/workflows/" + mineID:
		w.WriteHeader(204)
	case "DELETE /v1/studio/workflows/busy":
		http.Error(w, `{"detail":"a run of this workflow is unfinished; cancel it or wait"}`, 409)
	default:
		f.t.Errorf("unexpected %s", route)
		http.Error(w, `{"detail":"not found"}`, 404)
	}
}

func setupWorkflows(t *testing.T) *workflowAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	f := &workflowAPI{t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func helmOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("%v: %d %s\n%s", args, code, errb.String(), out.String())
	}
	return out.String()
}

func helmFails(t *testing.T, want string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := helm(args, nil, &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), want) {
		t.Fatalf("%v: %d %q, want %q", args, code, errb.String(), want)
	}
	return out.String()
}

func TestWorkflowsList(t *testing.T) {
	setupWorkflows(t)
	want := fmt.Sprintf("%-36s template      fake     Echo\n%-36s mine     v3   fake     My echo\n", "echo", mineID)
	if got := helmOK(t, "workflows"); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestWorkflowGetSaveRoundTrip(t *testing.T) {
	f := setupWorkflows(t)
	file := filepath.Join(t.TempDir(), "w.json")
	if err := os.WriteFile(file, []byte(helmOK(t, "workflow", "get", mineID)), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, id, version, basedOn, err := readWorkflowFile(file)
	if err != nil || id != mineID || version != 3 || basedOn != "" || len(doc) != 5 {
		t.Fatalf("file: %v %q %d %q %v", doc, id, version, basedOn, err)
	}
	if got := helmOK(t, "workflow", "save", file); got != "saved "+mineID+" version 4\nrun  -                    no_output: no output node\n" {
		t.Fatalf("save: %q", got)
	}
	var put map[string]any
	_ = json.Unmarshal([]byte(strings.SplitN(f.reqs[len(f.reqs)-1], " ", 3)[2]), &put)
	if _, ok := put["id"]; ok || put["version"] != 3.0 || put["studio"] == nil || put["title"] != "My echo" {
		t.Fatalf("PUT body: no id, the version, the whole doc: %v", put)
	}

	// a stale version is the server's 409
	stale := strings.Replace(string(must(os.ReadFile(file))), `"version": 3`, `"version": 2`, 1)
	_ = os.WriteFile(file, []byte(stale), 0o600)
	helmFails(t, "409: workflow changed since you opened it (now version 3)", "workflow", "save", file)
	_ = os.WriteFile(file, []byte(`{"id":"`+mineID+`","title":"t","family":"echo","env":"fake","nodes":{}}`), 0o600)
	helmFails(t, "an id needs its version", "workflow", "save", file)
}

func TestWorkflowSaveTemplateCreatesCopy(t *testing.T) {
	f := setupWorkflows(t)
	file := filepath.Join(t.TempDir(), "echo.json")
	_ = os.WriteFile(file, []byte(helmOK(t, "workflow", "get", "echo")), 0o600)
	if got := helmOK(t, "workflow", "save", file); !strings.HasPrefix(got, "saved new1 version 1\nto keep editing it: helm workflow get new1 > ") {
		t.Fatalf("save: %q", got)
	}
	last := f.reqs[len(f.reqs)-1]
	if !strings.HasPrefix(last, "POST /v1/studio/workflows {") || !strings.Contains(last, `"based_on":"echo"`) {
		t.Fatalf("create: %s", last)
	}
	// a save-blocking problem: 422 with every problem printed
	_ = os.WriteFile(file, []byte(`{"title":"","family":"echo","env":"fake","nodes":{"echo":{"kind":"echo/echo","params":{"steps":0}}}}`), 0o600)
	out := helmFails(t, "422: workflow has 1 problems", "workflow", "save", file)
	if out != "save echo.steps           param_invalid: echo.steps: must be between 1 and 200\n" {
		t.Fatalf("problems: %q", out)
	}
}

func TestWorkflowValidateCopyRm(t *testing.T) {
	f := setupWorkflows(t)
	file := filepath.Join(t.TempDir(), "w.json")
	_ = os.WriteFile(file, []byte(`{"id":"x","version":1,"title":"t","family":"echo","env":"fake","nodes":{"echo":{"kind":"echo/echo"}}}`), 0o600)
	out := helmFails(t, "2 problems: it can be saved, not run", "workflow", "validate", file)
	if out != "run  -                    no_output: the workflow has no output node\nrun  echo.text            required_input: echo.text: not wired\n" {
		t.Fatalf("validate: %q", out)
	}
	if last := f.reqs[len(f.reqs)-1]; strings.Contains(last, `"id"`) || strings.Contains(last, `"version"`) {
		t.Fatalf("validate sends the doc only: %s", last)
	}
	if got := helmOK(t, "workflow", "copy", "echo", "--title", "Mine"); got != "new1\n" ||
		f.reqs[len(f.reqs)-1] != `POST /v1/studio/workflows {"based_on":"echo","title":"Mine"}` {
		t.Fatalf("copy: %q %s", got, f.reqs[len(f.reqs)-1])
	}
	if got := helmOK(t, "workflow", "rm", mineID); got != "deleted "+mineID+"\n" {
		t.Fatalf("rm: %q", got)
	}
	helmFails(t, "409: a run of this workflow is unfinished", "workflow", "rm", "busy")
	var out2, errb bytes.Buffer
	if code := helm([]string{"workflow", "get"}, nil, &out2, &errb, nil); code != 2 {
		t.Fatalf("usage: %d", code)
	}
	if code := helm([]string{"workflow", "frob", "x"}, nil, &out2, &errb, nil); code != 2 {
		t.Fatalf("unknown sub: %d", code)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
