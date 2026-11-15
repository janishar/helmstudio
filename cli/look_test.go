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

const lookID = "10000000-0000-4000-8000-000000000001"

const ownLook = `{"id":"` + lookID + `","kind":"look","group":"colour","name":"Warm","handle":null,"text":"warm tones",
 "images":[{"take_id":"t1","url":"u","thumb_url":"u","width":3,"height":2}],"thumb_url":"u","version":2,
 "created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z","source":"mine","family":null,
 "loras":[{"lora":{"lora_id":"l1"},"name":"ink","strength":0.7}],"needs":["text","images","lora"],"from_take_id":null}`

const starterLook = `{"id":"echo/grain","kind":"look","group":"film","name":"Grain","handle":null,"text":"fine film grain",
 "images":[],"thumb_url":null,"version":1,"created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z",
 "source":"starter","family":"echo","loras":[{"lora":null,"name":"echo-grain","strength":0.5}],"needs":["text","lora"],
 "from_take_id":null,"fits":false,"reason":"this workflow has no LoRA stack"}`

const unknownLook = "looks[2]: unknown look echo/nope"

func lookAPI(t *testing.T) *workflowAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	f := &workflowAPI{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.Path
		f.reqs = append(f.reqs, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(b)))
		switch route {
		case "GET /v1/studio/library":
			fmt.Fprint(w, `{"items":[`+ownLook+`,`+starterLook+`]}`)
		case "GET /v1/studio/library/" + lookID:
			fmt.Fprint(w, ownLook)
		case "GET /v1/studio/library/echo/grain":
			fmt.Fprint(w, starterLook)
		case "DELETE /v1/studio/library/echo/grain":
			http.Error(w, `{"detail":"starter looks are read-only; save your own copy"}`, 409)
		case "PUT /v1/studio/library/" + lookID:
			fmt.Fprint(w, strings.Replace(ownLook, `"version":2`, `"version":3`, 1))
		case "POST /v1/studio/library":
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new1","version":1}`)
		case "DELETE /v1/studio/library/" + lookID:
			w.WriteHeader(204)
		case "GET /v1/studio/workflows/echo":
			fmt.Fprint(w, echoWorkflow)
		case "POST /v1/studio/sessions":
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"s1","title":"Echo"}`)
		case "POST /v1/studio/sessions/s1/runs":
			http.Error(w, `{"detail":"`+unknownLook+`"}`, 422)
		case "POST /v1/studio/batches":
			http.Error(w, `{"detail":"row 1: `+unknownLook+`"}`, 422)
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

func lastBody(t *testing.T, f *workflowAPI) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(f.reqs[len(f.reqs)-1], " ", 3)[2]), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestLooksList(t *testing.T) {
	f := lookAPI(t)
	got := helmOK(t, "looks", "--group", "film", "--workflow", "echo")
	want := fmt.Sprintf("%-36s %-7s %-8s %-15s v%-3d %s\n", lookID, "mine", "colour", "text,images,lora", 2, "Warm") +
		fmt.Sprintf("%-36s %-7s %-8s %-15s v%-3d %s\n", "echo/grain", "starter", "film", "text,lora", 1,
			"Grain (does not fit: this workflow has no LoRA stack)")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if last := f.reqs[len(f.reqs)-1]; last != "GET /v1/studio/library?group=film&kind=look&workflow=echo" {
		t.Fatalf("list: %s", last)
	}
}

func TestLookGetSaveRm(t *testing.T) {
	f := lookAPI(t)
	file := filepath.Join(t.TempDir(), "look.json")
	_ = os.WriteFile(file, []byte(helmOK(t, "look", "get", lookID)), 0o600)
	if got := helmOK(t, "look", "save", file); got != "saved "+lookID+" version 3\n" {
		t.Fatalf("save: %q", got)
	}
	put := lastBody(t, f)
	if fmt.Sprint(put["images"]) != "[map[take_id:t1]]" || fmt.Sprint(put["loras"]) != "[map[lora:map[lora_id:l1] strength:0.7]]" ||
		put["version"] != 2.0 || put["kind"] != nil || len(put) != 6 {
		t.Fatalf("PUT body is a LibraryItemUpdate: %v", put)
	}

	// a starter look: the path keeps its slash, the file saves as one's own copy (no based_on, null LoRAs dropped)
	_ = os.WriteFile(file, []byte(helmOK(t, "look", "get", "echo/grain")), 0o600)
	if got := helmOK(t, "look", "save", file); !strings.HasPrefix(got, "saved new1 version 1\n") {
		t.Fatalf("save copy: %q", got)
	}
	post := lastBody(t, f)
	if post["kind"] != "look" || post["based_on"] != nil || fmt.Sprint(post["loras"]) != "[]" || post["text"] != "fine film grain" {
		t.Fatalf("POST body: %v", post)
	}

	// Save as look from a take: from_take passes through
	_ = os.WriteFile(file, []byte(`{"group":"film","name":"From a take","from_take":{"take_id":"t9","loras":false}}`), 0o600)
	helmOK(t, "look", "save", file)
	if post := lastBody(t, f); fmt.Sprint(post["from_take"]) != "map[loras:false take_id:t9]" || post["kind"] != "look" {
		t.Fatalf("from_take: %v", post)
	}

	helmFails(t, "409: starter looks are read-only; save your own copy", "look", "rm", "echo/grain")
	if got := helmOK(t, "look", "rm", lookID); got != "deleted "+lookID+"\n" {
		t.Fatalf("rm: %q", got)
	}
}

func TestRunAndBatchSendLooks(t *testing.T) {
	f := lookAPI(t)
	helmFails(t, "helm: 422: "+unknownLook+"\n", "run", "echo", "--look", lookID, "-p", "prompt.text=a fox", "--look", "echo/nope")
	if got := fmt.Sprint(lastBody(t, f)["looks"]); got != "["+lookID+" echo/nope]" {
		t.Fatalf("run looks: %s", got)
	}
	lines := filepath.Join(t.TempDir(), "lines.txt")
	_ = os.WriteFile(lines, []byte("a fox\n"), 0o600)
	helmFails(t, "helm: 422: row 1: "+unknownLook+"\n", "batch", "echo", "--lines", lines, "--look", "echo/nope")
	if got := fmt.Sprint(lastBody(t, f)["looks"]); got != "[echo/nope]" {
		t.Fatalf("batch looks: %s", got)
	}
	helmFails(t, "helm: 422", "run", "echo", "-p", "prompt.text=x")
	if _, ok := lastBody(t, f)["looks"]; ok {
		t.Fatal("no --look: no looks key")
	}
}
