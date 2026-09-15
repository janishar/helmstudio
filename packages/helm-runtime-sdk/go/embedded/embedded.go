// Package embedded is helmstudio's embedded provider: the platform API with no
// daemon, against ./.helm/helm.db (docs/design/05-sdk-and-custom-studios.md §2,
// docs/design/07-platform-services.md §0).
//
// Import it for its side effect and helm.FromEnv uses it whenever HELM_API is
// not set:
//
//	import _ "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded"
//
// It enforces nothing of its own. It runs the daemon's own studio-api router
// and service in process, over an in-memory transport, acting for the studio
// its manifest describes with the capabilities that manifest declares — so a
// call refused under the daemon is refused here the same way (docs/decisions.md
// M4 Q25 and first review #7). Only one process may open a ./.helm at a time;
// a multi-process studio runs under helm dev instead.
package embedded

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/store"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Environment variables the embedded provider reads.
const (
	// EnvDir overrides ./.helm.
	EnvDir = "HELM_DIR"
	// EnvManifest overrides ./helmstudio.yaml.
	EnvManifest = "HELM_MANIFEST"
)

func init() {
	helm.RegisterEmbedded(func() (*helm.Client, error) {
		p, err := Open(Options{})
		if err != nil {
			return nil, err
		}
		return p.Client, nil
	})
}

// Options says where the provider keeps its data and which studio it is.
type Options struct {
	// Dir holds helm.db, assets, library, stage, data and logs. Default
	// $HELM_DIR, else ./.helm.
	Dir string
	// Manifest is the studio's helmstudio.yaml. Default $HELM_MANIFEST, else
	// ./helmstudio.yaml. It must exist: capabilities come from it.
	Manifest string
	// StudioID overrides the manifest's id; default $HELM_STUDIO_ID.
	StudioID string
	// Now is for tests.
	Now func() time.Time
}

// Provider is an open embedded provider acting for one studio.
type Provider struct {
	Client *helm.Client
	shared *Shared
}

// Service is the provider's service, for tools that reach past the SDK.
func (p *Provider) Service() *studioapi.Service { return p.shared.Service }

// Close stops the provider and releases its directory.
func (p *Provider) Close() error { return p.shared.Close() }

// Shared is one directory serving several studios at once: helm dev's sibling
// fixtures, and the conformance suite, which needs two studios in one store to
// test handoff and cross-studio reads. Each client acts for its own studio.
type Shared struct {
	Service   *studioapi.Service
	Dir       string
	store     *store.Store
	manifests map[string]*manifest.Manifest
	paths     paths
	cancel    context.CancelFunc
}

type paths struct {
	dir    string
	single bool
}

func (p paths) DB() string                  { return filepath.Join(p.dir, "helm.db") }
func (p paths) DBLock() string              { return p.DB() + ".lock" }
func (p paths) DBBackup(version int) string { return p.DB() + ".bak." + strconv.Itoa(version) }

// Stage and Data are ./.helm/stage and ./.helm/data for one studio, and a
// directory per studio below them when several share the store.
func (p paths) Stage(pr studioapi.Principal) string { return p.sub("stage", pr.StudioID) }
func (p paths) Data(studioID string) string         { return p.sub("data", studioID) }
func (p paths) Logs() string                        { return filepath.Join(p.dir, "logs") }

func (p paths) sub(name, studioID string) string {
	if p.single {
		return filepath.Join(p.dir, name)
	}
	return filepath.Join(p.dir, name, studioID)
}

type studioSet map[string]*manifest.Manifest

func (s studioSet) Manifest(id string) (*manifest.Manifest, bool) {
	m, ok := s[id]
	return m, ok
}

func orEnv(v, env, def string) string {
	if v != "" {
		return v
	}
	if e := os.Getenv(env); e != "" {
		return e
	}
	return def
}

// Open opens the provider for the studio its manifest describes.
func Open(opts Options) (*Provider, error) {
	dir := orEnv(opts.Dir, EnvDir, ".helm")
	manifestPath := orEnv(opts.Manifest, EnvManifest, "helmstudio.yaml")
	m, res, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("embedded provider: the studio's capabilities come from its manifest, and %s could not be read: %w", manifestPath, err)
	}
	if !res.OK() {
		return nil, fmt.Errorf("embedded provider: %s is not a valid manifest: %v", manifestPath, res.Errors)
	}
	if id := orEnv(opts.StudioID, helm.EnvStudioID, ""); id != "" && id != m.ID {
		copied := *m
		copied.ID = id
		m = &copied
	}
	sh, err := OpenShared(dir, []*manifest.Manifest{m}, opts.Now)
	if err != nil {
		return nil, err
	}
	c, err := sh.Client(m.ID)
	if err != nil {
		sh.Close()
		return nil, err
	}
	return &Provider{Client: c, shared: sh}, nil
}

// OpenShared opens dir for every studio in manifests.
func OpenShared(dir string, manifests []*manifest.Manifest, now func() time.Time) (*Shared, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	set := studioSet{}
	for _, m := range manifests {
		set[m.ID] = m
	}
	p := paths{dir: dir, single: len(manifests) == 1}
	dirs := []string{dir, p.Logs()}
	for id := range set {
		dirs = append(dirs, p.Stage(studioapi.Principal{StudioID: id}), p.Data(id))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("embedded provider: creating %s: %w", d, err)
		}
	}
	st, err := store.Open(context.Background(), p)
	if err != nil {
		return nil, fmt.Errorf("embedded provider: %w (only one process may open %s; run a multi-process studio under helm dev)", err, dir)
	}
	svc := studioapi.NewService(studioapi.Config{
		Store:    st,
		Media:    &media.Engine{Assets: filepath.Join(dir, "assets"), Derived: filepath.Join(dir, "cache", "derived"), Library: filepath.Join(dir, "library")},
		Paths:    p,
		Studios:  set,
		Provider: "embedded",
		Version:  "embedded",
		Now:      now,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go svc.WatchJobs(ctx, 500*time.Millisecond)
	return &Shared{Service: svc, Dir: dir, store: st, manifests: set, paths: p, cancel: cancel}, nil
}

// Client returns a client acting for one studio, with the capabilities its
// manifest declares and no others (first review #7).
func (s *Shared) Client(studioID string) (*helm.Client, error) {
	m, ok := s.manifests[studioID]
	if !ok {
		return nil, fmt.Errorf("embedded provider: no manifest for %q", studioID)
	}
	principal := studioapi.Principal{StudioID: m.ID, Capabilities: append([]string{}, m.Capabilities...)}
	h := studioapi.NewHandler(s.Service, studioapi.Fixed(principal), nil)
	return helm.NewRemote("http://embedded"+studioapi.Base, "", helm.WithHTTPClient(&http.Client{Transport: Transport(h)})), nil
}

// StageDir and DataDir are where a studio's :adopt looks.
func (s *Shared) StageDir(studioID string) string {
	return s.paths.Stage(studioapi.Principal{StudioID: studioID})
}
func (s *Shared) DataDir(studioID string) string { return s.paths.Data(studioID) }

// Close stops the provider and releases the directory.
func (s *Shared) Close() error {
	s.cancel()
	s.Service.Events().Shutdown("the embedded provider is closing")
	return s.store.Close()
}

// Store is the provider's database, for tools that prepare rows the API
// cannot create, such as the conformance suite's install job.
func (s *Shared) Store() *store.Store { return s.store }
