package visual

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/schema"
)

// A real daemon behind the editor's fixture (03 §13a, M7 Q17).
//
// The editor's whole design is that the page never parses YAML: the errors,
// the criteria and the document all come from the daemon. A fixture that
// answered `:validate` from a hand-written object would be a second copy of
// that response — and the first thing to go wrong would be a golden of an
// editor drawn from a shape the daemon no longer returns.
//
// So the fixture server mounts the real manifest operations over a temporary
// library. The studios list stays canned, because the goldens need one studio
// in each card state and no real daemon has that.

// editorManifest is what the editor fixture opens: a manifest with something
// wrong with it, because an editor drawn over a perfect document shows none of
// the work it exists to do.
const editorManifest = `# wan-studio — written by hand, from a repository that ships no manifest.
id: wan-studio
name: wan studio
description: Wan 2.6 text-to-video, through PyTorch on MPS.
kinds: [video]
repo: https://github.com/someone/wan
ref: v0.3.1
license: apache-2.0
requires:
  os: [darwin]
  arch: [arm64]
  tools: [git, uv]
  ram_gb: 32
  disk_gb: 90
peak_ram_gb: 18
runtime:
  framework: pytorch
  backends: [mps]
  precision: [bf16]
python:
  version: "3.12"
capabilities: [assets, gallery, gallery.read_all]
network: [huggingface.co, cdn-lfs.huggingface.co]
sdk:
  runtime: ^1.0.0
  ui: ^1.0.0
build:
  - { name: Install dependencies, run: "uv sync --all-extras" }
  - { name: Build the Metal kernels, run: "bash scripts/build_metal.sh" }
import:
  run: "uv run python -m wan.setup"
weights:
  - { name: wan_t2v_5b, repo: Wan-AI/Wan2.6-T2V-5B, dest: wan-2-6-5b, selectable: true }
  - { name: wan_t2v_14b, repo: Wan-AI/Wan2.6-T2V-14B, dest: wan-2-6-14b, selectable: true }
processes:
  - name: studio
    role: main
    heavy: true
    cmd: "uv run python -m wan.serve --model {models.selected} --port {port}"
    port: { prefer: 8740 }
    # No test.profile yet: the smoke test would run a full generation.
    health: { path: /health, timeout_s: 180 }
    ui: /
`

// mountDaemon adds /api/v1 and /schema/manifest.json to the fixture server, as
// the daemon serves them.
func mountDaemon(t *testing.T, mux *http.ServeMux, srv *httptest.Server) {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: time.Second, PortMin: 43000, PortMax: 43999})

	local := library.NewLocal(d.Data())
	if _, err := local.Save("wan-studio", []byte(editorManifest), ""); err != nil {
		t.Fatal(err)
	}
	res := library.New(library.Dir(library.SourceLocal, local.Dir))
	entries, err := res.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	var studios []supervisor.Studio
	for _, e := range entries {
		if e.Manifest == nil {
			t.Fatalf("%s did not resolve: %v", e.ID, e.Errors)
		}
		studios = append(studios, supervisor.Studio{Manifest: e.Manifest, File: e.File})
	}
	sup.SetStudios(studios)

	// The Host check is on the address the daemon was told it listens at, so
	// the server's own address is the one to hand it.
	handler, err := api.New(sup, fstest.MapFS{}, srv.Listener.Addr().String(), func(string, ...any) {},
		api.WithLibrary(res),
		api.WithApproval(st),
		api.WithManifests(local, library.NewReader(filepath.Join(d.Cache(), "manifests")), library.NewFetcher()))
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/api/v1/", handler)
	mux.HandleFunc("GET /schema/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(schema.Manifest)
	})
}
