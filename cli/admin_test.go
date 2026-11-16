package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	b, err := os.ReadFile("../contracts/fixtures/http/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAdmin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	var last string // the last write: method, path?query and body
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		route := r.Method + " " + r.URL.Path
		if r.Method != http.MethodGet {
			b, _ := json.Marshal(body)
			last = route + "?" + r.URL.RawQuery + " " + string(b)
		}
		switch route {
		case "GET /v1/studio/models":
			_, _ = w.Write(fixture(t, "ModelList.valid.json"))
		case "GET /v1/studio/hosts":
			_, _ = w.Write(fixture(t, "HostList.valid.2.json"))
		case "POST /v1/studio/admin/models/qwen-image-2.1/download", "DELETE /v1/studio/admin/models/qwen-image-2.1":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write(fixture(t, "ModelJob.valid.json"))
		case "PUT /v1/studio/admin/hosts/mac/pins", "PUT /v1/studio/admin/hosts/mac/settings":
			_, _ = w.Write(fixture(t, "HostSettings.valid.json"))
		case "POST /v1/studio/admin/hosts/mac/unload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write(fixture(t, "HostUnloadAccepted.valid.json"))
		case "GET /auth/admin/apikeys":
			fmt.Fprint(w, `{"api_keys":[{"id":"k1","client_id":"c1","client":"helm","prefix":"helm_abcdefg","scopes":[],"created_at":"2026-11-01T00:00:00Z","revoked_at":null,"last_used_at":null}]}`)
		case "POST /auth/admin/apikeys":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"k2","client_id":"c2","client":"ci","prefix":"helm_xyz1234","scopes":[],"created_at":"2026-11-16T00:00:00Z","revoked_at":null,"last_used_at":null,"key":"helm_new"}`)
		case "DELETE /auth/admin/apikeys/k1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, `{"detail":"not found"}`, 404)
		}
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}

	out := helmOK(t, "models")
	for _, w := range []string{"base bf16|int8 57.8 GB gpu1: mismatch (1 files) mac: present",
		"turbo bf16|int8 40.9 GB gpu1: missing mac: downloading 62% gated", "ltx-2.5 distilled bf16|int8|int4 - mac: partial"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), w) {
			t.Errorf("models: no %q in\n%s", w, out)
		}
	}

	// A licence that must be accepted is printed and refused until --accept-licence.
	helmFails(t, "licence qwen-research must be accepted", "model", "download", "qwen-image-2.1", "--host", "mac", "--variant", "turbo")
	last = ""
	helmOK(t, "model", "download", "qwen-image-2.1", "--host", "mac", "--variant", "turbo", "--accept-licence")
	if last != `POST /v1/studio/admin/models/qwen-image-2.1/download? {"accept_licence":true,"host":"mac","variant":"turbo"}` {
		t.Errorf("download: %s", last)
	}
	helmOK(t, "model", "download", "qwen-image-2.1", "--host", "mac") // base: no acceptance needed
	helmOK(t, "model", "rm", "qwen-image-2.1", "--host", "mac", "--variant", "turbo")
	if last != `DELETE /v1/studio/admin/models/qwen-image-2.1?host=mac&variant=turbo null` {
		t.Errorf("rm: %s", last)
	}

	helmOK(t, "hosts", "pin", "mac", "torch", "vae", "qwen-image-2.1", "base", "bf16")
	if last != `PUT /v1/studio/admin/hosts/mac/pins? {"pins":[{"component":"transformer","env":"mlx","model":"ltx-2.5","precision":"int8","variant":"distilled"},{"component":"vae","env":"torch","model":"qwen-image-2.1","precision":"bf16","variant":"base"}]}` {
		t.Errorf("pin: %s", last)
	}
	helmOK(t, "hosts", "unpin", "mac", "mlx", "transformer", "ltx-2.5", "distilled", "int8")
	if last != `PUT /v1/studio/admin/hosts/mac/pins? {"pins":[]}` {
		t.Errorf("unpin: %s", last)
	}
	helmFails(t, "is not pinned", "hosts", "unpin", "mac", "torch", "vae", "qwen-image-2.1", "base", "bf16")
	helmOK(t, "hosts", "unload", "mac", "--all")
	if last != `POST /v1/studio/admin/hosts/mac/unload? {"all":true}` {
		t.Errorf("unload all: %s", last)
	}
	helmOK(t, "hosts", "unload", "mac", "--cached", "mlx")
	if last != `POST /v1/studio/admin/hosts/mac/unload? {"cached_env":"mlx"}` {
		t.Errorf("unload cached: %s", last)
	}
	helmOK(t, "hosts", "unload", "mac", "torch", "vae", "qwen-image-2.1", "base", "bf16")
	if last != `POST /v1/studio/admin/hosts/mac/unload? {"entries":[{"component":"vae","env":"torch","model":"qwen-image-2.1","precision":"bf16","variant":"base"}]}` {
		t.Errorf("unload entry: %s", last)
	}
	helmOK(t, "hosts", "after-run", "mac", "keep_pinned", "--reserve-gb", "8.5")
	if last != `PUT /v1/studio/admin/hosts/mac/settings? {"after_run":"keep_pinned","idle_unload_min":10,"reserve_gb":8.5}` {
		t.Errorf("after-run: %s", last)
	}

	out = helmOK(t, "keys", "list")
	if f := strings.Fields(out); len(f) < 6 || f[1] != "helm_abcdefg" || f[2] != "helm" || !strings.HasSuffix(strings.TrimSpace(out), "active") {
		t.Errorf("keys list: %q", out)
	}
	out = helmOK(t, "keys", "new", "ci")
	if !strings.Contains(out, "\nhelm_new\n") || last != `POST /auth/admin/apikeys? {"client":"ci"}` {
		t.Errorf("keys new: %q %s", out, last)
	}
	helmOK(t, "keys", "revoke", "k1")
	if last != `DELETE /auth/admin/apikeys/k1? null` {
		t.Errorf("keys revoke: %s", last)
	}
}
