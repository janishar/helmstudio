package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

func newInstallServer(t *testing.T) (*Server, string) {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	hub := hubtest.New(t)
	hub.Add("org/toy", "model.bin", hubtest.Content(2048, 1), true)
	w := weights.New(weights.Config{Store: st, Dirs: d, FreeDisk: func(string) (uint64, error) { return 1 << 50, nil },
		HF: &weights.HF{Endpoint: hub.URL(), Token: func(context.Context) (string, error) { return "", nil },
			Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Weights: w, Grace: 2 * time.Second, PortMin: 42000, PortMax: 42999})
	in := install.New(install.Config{Store: st, Dirs: d, Supervisor: sup, Weights: w,
		HostOS: func() string { return "darwin" }, HostArch: func() string { return "arm64" }, HostBackends: func() []string { return []string{"cpu"} },
		HostMemory: func() (uint64, error) { return 64 << 30, nil }, FreeDisk: func(string) (uint64, error) { return 1 << 50, nil }})

	local := t.TempDir()
	var studios []supervisor.Studio
	for id, backends := range map[string]string{"toy-studio": "cpu", "metal-only": "metal"} {
		f := filepath.Join(t.TempDir(), id+".yaml")
		os.WriteFile(f, []byte(`id: `+id+`
name: `+strings.ReplaceAll(id, "-", " ")+`
kinds: [video]
local_path: `+local+`
requires: { os: [darwin], arch: [arm64] }
runtime: { framework: other, backends: [`+backends+`] }
build:
  - { name: say hello, run: "echo hello from the build" }
weights:
  - { name: base, repo: org/toy, dest: Toy }
processes:
  - { name: studio, cmd: "exec sleep 60" }
`), 0o600)
		m, res, err := manifest.Load(f)
		if err != nil || !res.OK() {
			t.Fatal(err, res.Errors)
		}
		studios = append(studios, supervisor.Studio{Manifest: m, File: f})
	}
	sup.SetStudios(studios)
	srv, err := New(sup, fstest.MapFS{}, addr, t.Logf, WithInstall(in, w, d.Logs()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		in.Shutdown(ctx)
		sup.Shutdown(ctx)
		st.Close()
	})
	return srv, local
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return v
}

func doBody(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func waitJob(t *testing.T, h http.Handler, id string) install.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		j := decode[install.Job](t, do(t, h, "GET", Base+"/jobs/"+id, nil))
		if j.FinishedAt != nil {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still %s", id, j.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInstallJobsModelsAndReclaimOverHTTP(t *testing.T) {
	srv, local := newInstallServer(t)

	if rec := do(t, srv, "POST", Base+"/studios/metal-only:install", nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "blocked") {
		t.Fatalf("blocked install: %d %s", rec.Code, rec.Body)
	}

	rec := do(t, srv, "POST", Base+"/studios/toy-studio:install", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	job := waitJob(t, srv, decode[install.Job](t, rec).ID)
	if job.State != install.JobSucceeded {
		t.Fatalf("install job = %+v", job)
	}
	studio := decode[map[string]any](t, do(t, srv, "GET", Base+"/studios/toy-studio", nil))
	if studio["install_state"] != "ready" || studio["root"] != local {
		t.Fatalf("studio = %v", studio)
	}

	logs := do(t, srv, "GET", Base+"/jobs/"+job.ID+"/logs", nil)
	if body := logs.Body.String(); !strings.Contains(body, "event: step") || !strings.Contains(body, "hello from the build") || !strings.Contains(body, "event: end") {
		t.Fatalf("job log stream: %s", body)
	}
	if jobs := decode[[]install.Job](t, do(t, srv, "GET", Base+"/jobs?studio=toy-studio", nil)); len(jobs) != 2 {
		t.Fatalf("jobs = %+v; want the install and its download", jobs)
	}

	models := decode[[]weights.Artifact](t, do(t, srv, "GET", Base+"/models", nil))
	if len(models) != 1 || models[0].RefCount != 1 || models[0].BytesOnDisk != 2048 {
		t.Fatalf("models = %+v", models)
	}
	if rec := do(t, srv, "DELETE", Base+"/models/"+models[0].ID, nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "in_use") {
		t.Fatalf("deleting a download a studio uses: %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, srv, "POST", Base+"/studios/toy-studio:uninstall", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("uninstall: %d %s", rec.Code, rec.Body)
	} else if u := waitJob(t, srv, decode[install.Job](t, rec).ID); u.State != install.JobSucceeded {
		t.Fatalf("uninstall job = %+v", u)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("uninstall removed the local_path checkout: %v", err)
	}

	preview := decode[weights.ReclaimPreview](t, do(t, srv, "GET", Base+"/models:reclaim", nil))
	if len(preview.Items) != 1 || preview.TotalBytes != 2048 {
		t.Fatalf("preview = %+v", preview)
	}
	if rec := doBody(t, srv, "POST", Base+"/models:reclaim", `{"confirm":"stale"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "preview_changed") {
		t.Fatalf("stale reclaim: %d %s", rec.Code, rec.Body)
	}
	rec = doBody(t, srv, "POST", Base+"/models:reclaim", `{"confirm":"`+preview.Confirm+`"}`)
	if rec.Code != http.StatusOK || decode[weights.ReclaimPreview](t, rec).TotalBytes != 2048 {
		t.Fatalf("reclaim: %d %s", rec.Code, rec.Body)
	}
	if models := decode[[]weights.Artifact](t, do(t, srv, "GET", Base+"/models", nil)); len(models) != 0 {
		t.Fatalf("models after reclaim = %+v", models)
	}

	// Link a directory the user already has, then unlink it.
	user := filepath.Join(t.TempDir(), "Toy")
	os.MkdirAll(user, 0o755)
	if rec := doBody(t, srv, "POST", Base+"/studios/toy-studio/weights/base:link", `{"path":"`+user+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("linking an empty directory: %d %s", rec.Code, rec.Body)
	}
	os.WriteFile(filepath.Join(user, "model.bin"), []byte("mine"), 0o644)
	rec = doBody(t, srv, "POST", Base+"/studios/toy-studio/weights/base:link", `{"path":"`+user+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body)
	}
	linked := decode[weights.Artifact](t, rec)
	if rec := do(t, srv, "DELETE", Base+"/models/"+linked.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
	if b, err := os.ReadFile(filepath.Join(user, "model.bin")); err != nil || string(b) != "mine" {
		t.Fatalf("unlink touched the user's directory: %q %v", b, err)
	}
}

// Review M3 #3, the human's decision: DELETE /models/{id} reclaims an unused
// download, but only with the confirm of its one-item preview.
func TestDeleteModelNeedsTheConfirmOfItsPreview(t *testing.T) {
	srv, _ := newInstallServer(t)
	job := waitJob(t, srv, decode[install.Job](t, do(t, srv, "POST", Base+"/studios/toy-studio:install", nil)).ID)
	if job.State != install.JobSucceeded {
		t.Fatalf("install = %+v", job)
	}
	waitJob(t, srv, decode[install.Job](t, do(t, srv, "POST", Base+"/studios/toy-studio:uninstall", nil)).ID)
	id := decode[[]weights.Artifact](t, do(t, srv, "GET", Base+"/models", nil))[0].ID

	view := decode[modelView](t, do(t, srv, "GET", Base+"/models/"+id, nil))
	if view.Reclaim == nil || len(view.Reclaim.Items) != 1 || view.Reclaim.TotalBytes != 2048 {
		t.Fatalf("GET /models/{id} = %+v; want a one-item preview", view)
	}
	for _, q := range []string{"", "?confirm=stale"} {
		rec := do(t, srv, "DELETE", Base+"/models/"+id+q, nil)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"preview"`) {
			t.Fatalf("DELETE%s: %d %s; want 409 with the preview", q, rec.Code, rec.Body)
		}
		if models := decode[[]weights.Artifact](t, do(t, srv, "GET", Base+"/models", nil)); len(models) != 1 {
			t.Fatalf("DELETE%s deleted without a matching confirm", q)
		}
	}
	if rec := do(t, srv, "DELETE", Base+"/models/"+id+"?confirm="+view.Reclaim.Confirm, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE with the confirm: %d %s", rec.Code, rec.Body)
	}
	if models := decode[[]weights.Artifact](t, do(t, srv, "GET", Base+"/models", nil)); len(models) != 0 {
		t.Fatalf("models after DELETE = %+v", models)
	}
}
