package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
)

// The library as the screens read it (docs/design/03-design-system.md §13a).
//
// A card states source, level and install state as three independent facts,
// and says where a Local entry came from. Every one of those is a field the
// API document declares; this is what makes them served rather than declared.

const localManifest = `id: wan-studio
name: wan studio
kinds: [video]
repo: https://github.com/someone/wan
ref: v0.3.1
requires: { os: [darwin], arch: [arm64] }
runtime: { framework: pytorch, backends: [mps] }
weights:
  - { name: small, repo: someone/wan-small, dest: wan-small, selectable: true }
  - { name: large, repo: someone/wan-large, dest: wan-large, selectable: true }
processes:
  - name: studio
    cmd: "uv run python -m wan.serve --model {models.selected} --port {port}"
    port: { prefer: 8730 }
    health: { path: /health, timeout_s: 180 }
`

// libraryServer is a daemon with a library and the manifest operations, which
// is what the launcher talks to and what `helm dev` deliberately is not.
func libraryServer(t *testing.T) (*Server, *library.Local) {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := weights.New(weights.Config{Store: st, Dirs: d, FreeDisk: func(string) (uint64, error) { return 1 << 50, nil }})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Weights: w, Grace: time.Second, PortMin: 41000, PortMax: 41999})
	in := install.New(install.Config{Store: st, Dirs: d, Supervisor: sup, Weights: w,
		HostOS: func() string { return "darwin" }, HostArch: func() string { return "arm64" },
		HostBackends: func() []string { return []string{"mps"} },
		HostMemory:   func() (uint64, error) { return 64 << 30, nil },
		FreeDisk:     func(string) (uint64, error) { return 1 << 50, nil }})

	local := library.NewLocal(d.Data())
	if _, err := local.Save("wan-studio", []byte(localManifest), ""); err != nil {
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

	srv, err := New(sup, fstest.MapFS{"index.html": {Data: []byte("shelf")}}, addr, t.Logf,
		WithInstall(in, w, d.Logs()),
		WithLibrary(res),
		WithManifests(local, library.NewReader(filepath.Join(d.Cache(), "manifests")), library.NewFetcher()))
	if err != nil {
		t.Fatal(err)
	}
	return srv, local
}

func TestLibraryServesWhereALocalEntryCameFrom(t *testing.T) {
	srv, local := libraryServer(t)
	if err := local.SetProvenance("wan-studio", library.Provenance{
		Kind: "imported", URL: "https://example.com/wan-studio.yaml",
	}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, "GET", "/api/v1/studios", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /studios: %d %s", rec.Code, rec.Body)
	}
	var page struct {
		Items []struct {
			ID         string `json:"id"`
			Source     string `json:"source"`
			Level      string `json:"level"`
			State      string `json:"install_state"`
			Selection  string `json:"selection"`
			Selectable []struct {
				Name       string `json:"name"`
				Selectable bool   `json:"selectable"`
			} `json:"selectable"`
			Provenance *struct {
				Kind string `json:"kind"`
				URL  string `json:"url"`
			} `json:"provenance"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d entries, want 1", len(page.Items))
	}
	e := page.Items[0]
	// The three facts a card states, and none of them derived from another.
	if e.Source != "local" || e.Level != "unverified" || e.State != "listed" {
		t.Errorf("source %q, level %q, install_state %q", e.Source, e.Level, e.State)
	}
	if e.Provenance == nil {
		t.Fatal("no provenance: the card cannot say where this file came from")
	}
	if e.Provenance.Kind != "imported" || e.Provenance.URL != "https://example.com/wan-studio.yaml" {
		t.Errorf("provenance = %+v", *e.Provenance)
	}
	// The checkpoints, so the screen that offers a choice knows what the
	// choices are. Nothing is chosen yet, and the screen says so rather than
	// showing the first one as if it were.
	if len(e.Selectable) != 2 {
		t.Fatalf("got %d selectable weights, want 2", len(e.Selectable))
	}
	if e.Selection != "" {
		t.Errorf("selection %q before anything was chosen", e.Selection)
	}
}

// A note about an entry from another source would be a note about a file this
// one is not.
func TestProvenanceIsOnlyEverALocalEntrysOwn(t *testing.T) {
	srv, local := libraryServer(t)
	if err := local.SetProvenance("wan-studio", library.Provenance{Kind: "written"}); err != nil {
		t.Fatal(err)
	}
	entries, err := srv.library.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		e.Source = library.SourceRegistry
		if p := srv.provenance(e); p != nil {
			t.Errorf("a %s entry carried a local note: %+v", e.Source, *p)
		}
	}
}
