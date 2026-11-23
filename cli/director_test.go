package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../contracts/fixtures/http/"

// directorAPI serves the chat and provider routes from the contract's fixtures and records "METHOD uri body".
func directorAPI(t *testing.T) *[]string {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	reqs := &[]string{}
	file := func(w http.ResponseWriter, status int, name string) {
		b, err := os.ReadFile(fixtures + name)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(status)
		_, _ = w.Write(b)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*reqs = append(*reqs, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(b)))
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/studio/batches/b1/chat":
			file(w, 200, "ChatPage.valid.json")
		case "POST /v1/studio/batches/b1/chat":
			file(w, 202, "ChatMessage.valid.1.json")
		case "POST /v1/studio/batches/b2/chat":
			http.Error(w, `{"detail":"the director is off: the batch has ended"}`, 409)
		case "GET /v1/studio/admin/providers":
			file(w, 200, "ProviderList.valid.json")
		case "PUT /v1/studio/admin/providers/ollama":
			file(w, 201, "Provider.valid.1.json")
		case "PUT /v1/studio/admin/providers/again":
			file(w, 200, "Provider.valid.1.json")
		case "DELETE /v1/studio/admin/providers/ollama":
			w.WriteHeader(204)
		case "POST /v1/studio/admin/providers/ollama/test":
			file(w, 200, "ProviderTest.valid.1.json")
		case "POST /v1/studio/admin/providers/fakeai/test":
			file(w, 200, "ProviderTest.valid.2.json")
		default:
			http.Error(w, `{"detail":"admin role required"}`, 403)
		}
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return reqs
}

func TestBatchChat(t *testing.T) {
	reqs := directorAPI(t)
	want := "director director: idle, 3 of 100 wakes\n" +
		"2026-11-16T10:19:58Z user: Make shot 3 a close-up of @fox, and add a shot of the den at dawn.\n" +
		"2026-11-16T10:20:04Z director: Row 3 is a close-up now, and row 15 adds the den at dawn.\n" +
		`  rows_edit {"row":3,"inputs":{"script":"Close-up of @fox."}} -> {"row":3,"status":"queued"}` + "\n" +
		`  rows_add {"inputs":{"script":"The den at dawn."}} -> {"row":15,"status":"queued"}` + "\n" +
		"  rows 3 15\n" +
		"2026-11-16T10:20:04Z notice: Row 16 was not added: row: inputs.script: a value is required\n"
	if got := helmOK(t, "batch", "chat", "b1"); got != want {
		t.Fatalf("chat:\n%s", got)
	}
	got := helmOK(t, "batch", "chat", "b1", "add", "a", "shot")
	if got != "sent 1e2d3c4b-5a69-4788-9a1b-2c3d4e5f6a70; the director answers in the chat (helm batch chat b1)\n" ||
		(*reqs)[len(*reqs)-1] != `POST /v1/studio/batches/b1/chat {"text":"add a shot"}` {
		t.Fatalf("post %q %v", got, *reqs)
	}
	helmFails(t, "409: the director is off", "batch", "chat", "b2", "hi")
	helmFails(t, "usage: helm batch chat", "batch", "chat")
}

func TestProviders(t *testing.T) {
	reqs := directorAPI(t)
	got := helmOK(t, "providers")
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 || lines[0] != "remote models on this deployment: false" ||
		strings.Join(strings.Fields(lines[1]), " ") != "ollama server on http://host.docker.internal:11434/v1 qwen2.5:7b,llama3.2:3b (default qwen2.5:7b) mac 6.5 GB" {
		t.Fatalf("list:\n%s", got)
	}
	file := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(file, []byte(`{"kind":"server","base_url":"http://10.0.0.5:11434/v1","models":["m"],"host":"mac","memory_gb":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := helmOK(t, "providers", "set", "ollama", file); got != "created ollama\n" ||
		(*reqs)[len(*reqs)-1] != `PUT /v1/studio/admin/providers/ollama {"base_url":"http://10.0.0.5:11434/v1","host":"mac","kind":"server","memory_gb":4,"models":["m"]}` {
		t.Fatalf("set %q %v", got, *reqs)
	}
	if got := helmOK(t, "providers", "set", "again", file); got != "replaced again\n" {
		t.Fatalf("replace %q", got)
	}
	if got := helmOK(t, "providers", "test", "ollama", "--model", "qwen2.5:7b"); got != "ollama ok in 0.8 s; models qwen2.5:7b\nanswer: OK\n" ||
		(*reqs)[len(*reqs)-1] != "POST /v1/studio/admin/providers/ollama/test?model=qwen2.5%3A7b" {
		t.Fatalf("test %q %v", got, *reqs)
	}
	helmFails(t, "fakeai failed (network, 2.1 s): fakeai: could not connect", "providers", "test", "fakeai")
	if got := helmOK(t, "providers", "rm", "ollama"); got != "deleted ollama\n" {
		t.Fatalf("rm %q", got)
	}
	helmFails(t, "403: admin role required", "providers", "rm", "nobody")
	helmFails(t, "usage: helm providers", "providers", "set", "x")
}
