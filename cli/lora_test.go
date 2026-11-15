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

const loraID = "c0000000-0000-4000-8000-000000000001"

const loraJSON = `{"id":"` + loraID + `","source":"mine","name":"ink wash","family":"qwen-image","rank":16,"size_bytes":2500000,
 "key_style":"comfyui","licence":null,"sha256":null,"hosts":[],"state":"ready","reason":null,"thumb_url":null,"created_at":"2026-11-14T10:00:00Z"}`

// stackWorkflow is echo with a LoRA stack: lora.rows holds {lora: Lora, strength, mode} items.
const stackWorkflow = `{"id":"w1","title":"Echo LoRA","family":"echo","env":"fake",
 "nodes":{"prompt":{"kind":"prompt"},"lora":{"kind":"echo/lora-stack"},"echo":{"kind":"echo/echo"},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text"}]},
  "echo/lora-stack":{"params":[{"name":"rows","type":"rows","default":[],"max":8,"item":{"lora":"Lora","strength":{"type":"float"},"mode":{"type":"enum"}}}]},
  "echo/echo":{"params":[{"name":"steps","type":"int"}]},"output":{"params":[]}}}`

// loraAPI serves the LoRA routes: an upload whose complete answers 422 for a file named bad.safetensors.
func loraAPI(t *testing.T) (*workflowAPI, *[]byte) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	f, put := &workflowAPI{t: t}, new([]byte)
	name := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.Path
		f.reqs = append(f.reqs, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()))
		switch route {
		case "GET /v1/studio/loras":
			fmt.Fprint(w, `{"loras":[`+loraJSON+`]}`)
		case "POST /v1/studio/loras":
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			name = body["filename"].(string)
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"lora":{"id":"%s","state":"pending"},"upload_url":"/files/u/o1/loras/%s.safetensors?sig=x",
			 "content_type":"application/octet-stream","bytes":%v,"expires_at":"2026-11-14T10:15:00Z"}`, loraID, loraID, body["bytes"])
		case "PUT /files/u/o1/loras/" + loraID + ".safetensors":
			if r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Authorization") != "" {
				http.Error(w, "bad put", 403)
				return
			}
			*put = b
			w.WriteHeader(204)
		case "POST /v1/studio/loras/" + loraID + "/complete":
			if name == "bad.safetensors" {
				http.Error(w, `{"detail":"pickle checkpoints are not accepted; convert it to safetensors"}`, 422)
				return
			}
			fmt.Fprint(w, loraJSON)
		case "DELETE /v1/studio/loras/" + loraID:
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s", route)
			http.Error(w, `{"detail":"not found"}`, 404)
		}
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f, put
}

func TestLorasUploadRm(t *testing.T) {
	f, put := loraAPI(t)
	line := helmOK(t, "loras", "--source", "mine", "--q", "ink")
	if fields := strings.Fields(line); strings.Join(fields, " ") != loraID+" mine ready qwen-image r16 2.5 MB ink wash" {
		t.Fatalf("loras: %q", line)
	}
	if f.reqs[0] != "GET /v1/studio/loras?q=ink&source=mine" {
		t.Fatalf("query: %v", f.reqs)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "ink wash.safetensors")
	if err := os.WriteFile(file, []byte("\x10\x00\x00\x00\x00\x00\x00\x00{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := helmOK(t, "lora", "upload", file); !strings.HasPrefix(out, loraID) || string(*put) != "\x10\x00\x00\x00\x00\x00\x00\x00{}" {
		t.Fatalf("upload: %q put %q", out, *put)
	}
	bad := filepath.Join(dir, "bad.safetensors")
	_ = os.WriteFile(bad, []byte("\x80\x04"), 0o600)
	helmFails(t, "422: pickle checkpoints are not accepted", "lora", "upload", bad)
	helmFails(t, "only .safetensors files", "lora", "upload", filepath.Join(dir, "x.ckpt"))
	if out := helmOK(t, "lora", "rm", loraID); out != "deleted "+loraID+"\n" {
		t.Fatalf("rm: %q", out)
	}
}

func TestLoraRows(t *testing.T) {
	var wf workflow
	if err := json.Unmarshal([]byte(stackWorkflow), &wf); err != nil {
		t.Fatal(err)
	}
	got, err := wf.loraRows([]string{"a1:0.8", "b2:-1.5", "c3"})
	want := `map[lora.rows:[map[lora:map[lora_id:a1] strength:0.8] map[lora:map[lora_id:b2] strength:-1.5] map[lora:map[lora_id:c3]]]]`
	if err != nil || fmt.Sprint(got) != want {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := wf.loraRows(nil); got != nil || err != nil {
		t.Fatalf("no flags: %v %v", got, err)
	}
	for _, bad := range []string{"a1:strong", ":1"} {
		if _, err := wf.loraRows([]string{bad}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	var echo workflow
	_ = json.Unmarshal([]byte(echoWorkflow), &echo)
	if _, err := echo.loraRows([]string{"a1:1"}); err == nil || !strings.Contains(err.Error(), "no LoRA stack") {
		t.Fatalf("echo: %v", err)
	}
}

func TestRunWithLoras(t *testing.T) {
	f, _ := setup(t, "succeeded")
	var out, errb strings.Builder
	args := []string{"run", "echo", "--lora", "a1:0.5", "--lora", "b2:1", "--out", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), "no LoRA stack") {
		t.Fatalf("echo has no stack: %d %s", code, errb.String())
	}
	if f.inputs != nil {
		t.Fatalf("a run was sent: %v", f.inputs)
	}
	args = []string{"run", "w1", "--lora", "a1:0.5", "-p", "echo.steps=2", "--lora", "b2:1", "--out", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	want := `[map[lora:map[lora_id:a1] strength:0.5] map[lora:map[lora_id:b2] strength:1]]`
	if f.workflowID != "w1" || fmt.Sprint(f.inputs["lora.rows"]) != want || f.inputs["echo.steps"] != 2.0 {
		t.Fatalf("workflow %q inputs %v", f.workflowID, f.inputs)
	}
}
