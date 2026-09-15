// Package conformance is one suite, run twice: against the daemon over HTTP
// and against the embedded provider in process (docs/design/05 §5, R59,
// docs/decisions.md M4 Q25).
//
// Every case is written from api/openapi.yaml and the decisions it cites, and
// talks to a provider only through the generated Go client. A case that needs
// to look underneath — an inode, a file's mode — uses the directories the
// provider reports through /me, never the implementation's internals.
package conformance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
	"github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded"
)

// The studios every provider serves.
const (
	// A: the normal set plus handoff.send.
	A = "studio-a"
	// B: can read every studio's gallery and the shared kv namespace.
	B = "studio-b"
	// C: kv only, for capability refusals.
	C = "studio-c"
	// Q: tiny quotas.
	Q = "studio-q"
	// D: assets and gallery, without gallery.read_all: a handoff recipient.
	D = "studio-d"
	// S: kv, records, assets and gallery, not jobs: the Python and Node smoke.
	S = "studio-s"
)

// QuotaRecords and QuotaKVBytes are studio-q's declared quotas.
const (
	QuotaRecords = 3
	QuotaKVBytes = 2048
)

func manifests() []*manifest.Manifest {
	return []*manifest.Manifest{
		{ID: A, Name: "studio a", Capabilities: []string{"kv", "records", "assets", "gallery", "jobs", "handoff.send"}},
		{ID: B, Name: "studio b", Capabilities: []string{"kv", "records", "assets", "gallery", "jobs", "gallery.read_all", "kv.shared"}},
		{ID: C, Name: "studio c", Capabilities: []string{"kv"}},
		{ID: D, Name: "studio d", Capabilities: []string{"assets", "gallery"}},
		{ID: S, Name: "studio s", Capabilities: []string{"kv", "records", "assets", "gallery"}},
		{ID: Q, Name: "studio q", Capabilities: []string{"kv", "records"},
			Storage: &manifest.Storage{Quota: manifest.Quota{Records: QuotaRecords, KVBytes: QuotaKVBytes}}},
	}
}

// Env is one provider serving every studio above.
type Env struct {
	Name    string
	clients map[string]*helm.Client
	// Reclaim runs the launcher's asset reclaim: preview, then confirm.
	Reclaim func(t *testing.T) studioapi.ReclaimResult
	// Restart stops the provider and starts it again over the same data, as a
	// daemon restart or a studio reopening ./.helm would. Clients keep working.
	Restart func(t *testing.T)
	// Store is for cases that must set up rows the API cannot create, such
	// as an install job.
	Store *store.Store
	// Daemon-only.
	URL    string
	Tokens map[string]string
	Revoke func(t *testing.T, studio string)
}

func (e *Env) C(studio string) *helm.Client { return e.clients[studio] }

type provider struct {
	name string
	open func(t *testing.T) *Env
}

var providers = []provider{{"daemon", openDaemon}, {"embedded", openEmbedded}}

// each runs fn once per provider.
func each(t *testing.T, fn func(t *testing.T, e *Env)) {
	t.Helper()
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) { fn(t, p.open(t)) })
	}
}

// openDaemon assembles the daemon's studio API with studioapi.NewPlatform and
// Serve, the constructor cmd/helmstudio uses, behind the real internal/api
// server with its Host and Origin checks (second review #11). Tokens are
// minted for groups the suite pretends are running.
func openDaemon(t *testing.T) *Env {
	t.Helper()
	dirs := platformtest.Dirs(t)
	e := &Env{Name: "daemon", Tokens: map[string]string{}}
	groups := map[string]string{}
	var stop func()

	boot := func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		st, err := store.Open(ctx, dirs)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewUnstartedServer(nil)
		plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: dirs, API: "http://" + ts.Listener.Addr().String() + studioapi.Base,
			Provider: "daemon", Logf: t.Logf})
		sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Platform: plat.Launches})
		var studios []supervisor.Studio
		for _, m := range manifests() {
			studios = append(studios, supervisor.Studio{Manifest: m})
		}
		sup.SetStudios(studios)
		svc, h := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
		go svc.WatchJobs(ctx, 100*time.Millisecond)
		srv, err := api.New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf, api.WithStudioAPI(h, svc))
		if err != nil {
			t.Fatal(err)
		}
		ts.Config.Handler = srv
		ts.Start()

		if len(e.Tokens) == 0 {
			for _, m := range manifests() {
				group := store.NewID(time.Now())
				tok, err := plat.Tokens.Mint(ctx, m.ID, group, m.Capabilities)
				if err != nil {
					t.Fatal(err)
				}
				groups[m.ID], e.Tokens[m.ID] = group, tok
				for _, d := range []string{studioapi.StageDir(dirs.Stage(), m.ID, group), plat.Paths.Data(m.ID)} {
					if err := os.MkdirAll(d, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		e.clients = map[string]*helm.Client{}
		for id, tok := range e.Tokens {
			e.clients[id] = helm.NewRemote(ts.URL+studioapi.Base, tok)
		}
		e.Store, e.URL = st, ts.URL
		e.Revoke = func(t *testing.T, studio string) {
			if err := plat.Tokens.RevokeGroup(ctx, groups[studio]); err != nil {
				t.Fatal(err)
			}
		}
		e.Reclaim = func(t *testing.T) studioapi.ReclaimResult {
			t.Helper()
			var preview studioapi.ReclaimPreview
			getJSON(t, ts.URL+studioapi.Base+"/assets:reclaim", "", &preview)
			var res studioapi.ReclaimResult
			postJSON(t, ts.URL+studioapi.Base+"/assets:reclaim", map[string]any{"confirm": preview.Confirm}, http.StatusOK, &res)
			return res
		}
		stop = func() {
			svc.Events().Shutdown("the suite is restarting the daemon")
			ts.CloseClientConnections()
			ts.Close()
			cancel()
			st.Close()
		}
	}
	boot(t)
	e.Restart = func(t *testing.T) { stop(); boot(t) }
	t.Cleanup(func() { stop() })
	return e
}

func openEmbedded(t *testing.T) *Env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".helm")
	e := &Env{Name: "embedded"}
	var sh *embedded.Shared
	boot := func(t *testing.T) {
		var err error
		if sh, err = embedded.OpenShared(dir, manifests(), nil); err != nil {
			t.Fatal(err)
		}
		e.clients = map[string]*helm.Client{}
		for _, m := range manifests() {
			c, err := sh.Client(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			e.clients[m.ID] = c
		}
		e.Store = sh.Store()
		svc := sh.Service
		e.Reclaim = func(t *testing.T) studioapi.ReclaimResult {
			t.Helper()
			ctx := context.Background()
			p, err := svc.ReclaimPreview(ctx)
			if err != nil {
				t.Fatal(err)
			}
			res, _, err := svc.Reclaim(ctx, p.Confirm)
			if err != nil {
				t.Fatal(err)
			}
			return *res
		}
	}
	boot(t)
	e.Restart = func(t *testing.T) { sh.Close(); boot(t) }
	t.Cleanup(func() { sh.Close() })
	return e
}
