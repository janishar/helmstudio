package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	body, err := os.ReadFile("../contracts/fixtures/http/HostList.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "GET /v1/studio/hosts" {
			http.Error(w, `{"detail":"not found"}`, 404)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	out := helmOK(t, "hosts")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	norm := func(i int) string { return strings.Join(strings.Fields(lines[i]), " ") }
	want := map[int]string{
		0:  "mac 58.4 / 48.0 GB",
		1:  "mlx idle 41.2 / 48.0 GB",
		2:  "transformer ltx-2.5 distilled int8 19.18 GB used 2026-11-15T10:02:00Z",
		3:  "transformer ltx-2.5 distilled int8 19.75 GB + (hidden) used 2026-11-15T10:10:40Z",
		7:  "torch idle 17.2 / 52.0 GB",
		8:  "text_encoder qwen-image-2.1 base int8 8.50 GB used 2026-11-15T10:00:10Z",
		10: "vae qwen-image-2.1 base bf16 1.30 GB used 2026-11-15T10:00:30Z",
	}
	if len(lines) != 11 {
		t.Fatalf("hosts:\n%s", out)
	}
	for i, w := range want {
		if norm(i) != w {
			t.Errorf("line %d: %q, want %q", i, norm(i), w)
		}
	}
}
