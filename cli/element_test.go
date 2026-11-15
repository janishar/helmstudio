package main

import (
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

const elemID = "e0000000-0000-4000-8000-000000000001"

const elemJSON = `{"id":"` + elemID + `","kind":"element","group":"character","name":"Fox","handle":"fox","text":"a red fox",
 "images":[{"take_id":"t1","url":"/files/u/o1/a.png?sig=x","thumb_url":"/files/u/o1/a.png?sig=y","width":3,"height":2}],
 "thumb_url":"/files/u/o1/a.png?sig=y","version":2,"created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`

const unknownWolf = "prompt.text: unknown element @wolf"

// elementAPI serves the library routes, plus a run and a batch that refuse an unknown @wolf like studio does.
func elementAPI(t *testing.T) *workflowAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	f := &workflowAPI{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.Path
		f.reqs = append(f.reqs, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(b)))
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		switch route {
		case "GET /v1/studio/library":
			fmt.Fprint(w, `{"items":[`+elemJSON+`]}`)
		case "GET /v1/studio/library/" + elemID:
			fmt.Fprint(w, elemJSON)
		case "PUT /v1/studio/library/" + elemID:
			if body["version"] != 2.0 {
				http.Error(w, `{"detail":"element changed since you opened it (now version 2)"}`, 409)
				return
			}
			fmt.Fprint(w, strings.Replace(elemJSON, `"version":2`, `"version":3`, 1))
		case "POST /v1/studio/library":
			if body["handle"] == "fox" {
				http.Error(w, `{"detail":"handle @fox is already used"}`, 409)
				return
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new1","version":1}`)
		case "DELETE /v1/studio/library/" + elemID:
			w.WriteHeader(204)
		case "GET /v1/studio/workflows/echo":
			fmt.Fprint(w, echoWorkflow)
		case "POST /v1/studio/sessions":
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"s1","title":"Echo"}`)
		case "POST /v1/studio/sessions/s1/runs":
			http.Error(w, `{"detail":"`+unknownWolf+`"}`, 422)
		case "POST /v1/studio/batches":
			http.Error(w, `{"detail":"row 2: `+unknownWolf+`"}`, 422)
		default:
			t.Errorf("unexpected %s", route)
			http.Error(w, `{"detail":"not found"}`, 404)
		}
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestElementsListGetSaveRm(t *testing.T) {
	f := elementAPI(t)
	want := fmt.Sprintf("%-36s %-9s %-24s 1 img v2   Fox\n", elemID, "character", "@fox")
	if got := helmOK(t, "elements", "--q", "@Fo"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if last := f.reqs[len(f.reqs)-1]; last != "GET /v1/studio/library?kind=element&q=%40Fo" {
		t.Fatalf("list: %s", last)
	}

	file := filepath.Join(t.TempDir(), "fox.json")
	_ = os.WriteFile(file, []byte(helmOK(t, "element", "get", elemID)), 0o600)
	var doc map[string]any
	_ = json.Unmarshal(must(os.ReadFile(file)), &doc)
	if fmt.Sprint(doc["images"]) != "[map[take_id:t1]]" || doc["id"] != elemID || doc["version"] != 2.0 || doc["kind"] != nil {
		t.Fatalf("file: %v", doc)
	}
	if got := helmOK(t, "element", "save", file); got != "saved "+elemID+" version 3\n" {
		t.Fatalf("save: %q", got)
	}
	var put map[string]any
	_ = json.Unmarshal([]byte(strings.SplitN(f.reqs[len(f.reqs)-1], " ", 3)[2]), &put)
	if _, ok := put["id"]; ok || put["kind"] != nil || put["version"] != 2.0 || put["handle"] != "fox" || len(put) != 6 {
		t.Fatalf("PUT body is a LibraryItemUpdate: %v", put)
	}
	stale := strings.Replace(string(must(os.ReadFile(file))), `"version": 2`, `"version": 1`, 1)
	_ = os.WriteFile(file, []byte(stale), 0o600)
	helmFails(t, "409: element changed since you opened it (now version 2)", "element", "save", file)

	// no id: made, with kind element
	_ = os.WriteFile(file, []byte(`{"group":"prop","name":"Lamp","handle":"lamp","images":[{"take_id":"t1"}]}`), 0o600)
	if got := helmOK(t, "element", "save", file); !strings.HasPrefix(got, "saved new1 version 1\nto keep editing it: helm element get new1") {
		t.Fatalf("create: %q", got)
	}
	if last := f.reqs[len(f.reqs)-1]; !strings.HasPrefix(last, "POST /v1/studio/library {") || !strings.Contains(last, `"kind":"element"`) {
		t.Fatalf("create: %s", last)
	}
	_ = os.WriteFile(file, []byte(`{"group":"prop","name":"Fox","handle":"fox","images":[{"take_id":"t1"}]}`), 0o600)
	helmFails(t, "409: handle @fox is already used", "element", "save", file)

	if got := helmOK(t, "element", "rm", elemID); got != "deleted "+elemID+"\n" {
		t.Fatalf("rm: %q", got)
	}
	var out, errb strings.Builder
	if code := helm([]string{"element", "validate", file}, nil, &out, &errb, nil); code != 2 {
		t.Fatalf("validate is not an element command: %d", code)
	}
}

func TestMentionsPassThroughAndUnknownHandleIsPrinted(t *testing.T) {
	f := elementAPI(t)
	prompt := "@fox meets @wolf, mail a@b.c, @@literal"
	helmFails(t, "helm: 422: "+unknownWolf+"\n", "run", "echo", "-p", "prompt.text="+prompt)
	if last := f.reqs[len(f.reqs)-1]; !strings.Contains(last, `"prompt.text":"`+prompt+`"`) {
		t.Fatalf("run body: %s", last)
	}
	lines := filepath.Join(t.TempDir(), "lines.txt")
	_ = os.WriteFile(lines, []byte("@fox at dusk\n"+prompt+"\n"), 0o600)
	helmFails(t, "helm: 422: row 2: "+unknownWolf+"\n", "batch", "echo", "--lines", lines)
	if last := f.reqs[len(f.reqs)-1]; !strings.Contains(last, `"values":["@fox at dusk","`+prompt+`"]`) {
		t.Fatalf("batch body: %s", last)
	}
}
