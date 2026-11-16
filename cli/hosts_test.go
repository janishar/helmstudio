package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHosts(t *testing.T) {
	out := hostsOut(t, "HostList.valid.json")
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

// A cuda host beside the Mac: its device and VRAM on the host line, host RAM, a CPU-offloaded component.
func TestHostsCUDA(t *testing.T) {
	out := hostsOut(t, "HostList.valid.3.json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var gcp []string
	for i, l := range lines {
		if strings.HasPrefix(l, "gcp") {
			for _, x := range lines[i:] {
				gcp = append(gcp, strings.Join(strings.Fields(x), " "))
			}
		}
	}
	want := []string{
		"gcp 8.1 / 93.6 GB cuda NVIDIA RTX PRO 6000 Blackwell Server Edition 95.6 GB host RAM 14.2 / 172.0 GB",
		"torch idle 8.1 / 93.6 GB",
		"transformer qwen-image-2.1 turbo int8 6.76 GB pinned used 2026-11-15T10:00:20Z",
		"text_encoder qwen-image-2.1 base bf16 15.40 GB host RAM used 2026-11-15T10:00:10Z",
	}
	if strings.Join(gcp, "\n") != strings.Join(want, "\n") {
		t.Fatalf("hosts:\n%s", out)
	}
}

func hostsOut(t *testing.T, fixture string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	body, err := os.ReadFile("../contracts/fixtures/http/" + fixture)
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
	return helmOK(t, "hosts")
}
