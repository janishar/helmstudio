package studioapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// PlatformConfig is what a provider serving the studio API over a set of
// directories needs. cmd/helmstudio, helm dev and the conformance suite all
// build their studio API from it, so a wiring mistake in one is a wiring
// mistake in the others (second review #11).
type PlatformConfig struct {
	Store *store.Store
	Dirs  *platform.Dirs
	// API is HELM_API: the base URL studios reach this provider at.
	API string
	// StudioData is {data}. Nil is <data>/studios/<id>/data, as the
	// supervisor's default.
	StudioData func(studioID string) string
	// Provider and Version are reported by /me.
	Provider, Version string
	// FreeBytes reports free space for the upload check. Nil is
	// platform.FreeDiskBytes.
	FreeBytes func(path string) (uint64, error)
	Logf      func(string, ...any)
}

// Platform is the studio API's half of a provider: tokens and launch hooks
// before the supervisor exists, the service and router after.
type Platform struct {
	Tokens   *Tokens
	Launches *Launches
	Paths    DirPaths
	cfg      PlatformConfig
}

// NewPlatform returns the tokens and launch hooks. Hand Launches to
// supervisor.Config.Platform, then call Readopted and Serve.
func NewPlatform(cfg PlatformConfig) *Platform {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.FreeBytes == nil {
		cfg.FreeBytes = platform.FreeDiskBytes
	}
	tokens := &Tokens{Store: cfg.Store}
	return &Platform{
		Tokens:   tokens,
		Launches: &Launches{Tokens: tokens, API: cfg.API, StageRoot: cfg.Dirs.Stage(), Logf: cfg.Logf},
		Paths:    DirPaths{StageRoot: cfg.Dirs.Stage(), DataRoot: cfg.Dirs.Data(), LogsRoot: cfg.Dirs.Logs(), StudioData: cfg.StudioData},
		cfg:      cfg,
	}
}

// Media is the asset tree under the provider's roots.
func (p *Platform) Media() *media.Engine {
	return &media.Engine{
		Assets:  filepath.Join(p.cfg.Dirs.Data(), "assets"),
		Derived: filepath.Join(p.cfg.Dirs.Cache(), "derived"),
		Library: p.cfg.Dirs.Library(),
	}
}

// Serve returns the service and the router, with token authentication.
func (p *Platform) Serve(studios Studios) (*Service, *Handler) {
	svc := NewService(Config{
		Store:     p.cfg.Store,
		Media:     p.Media(),
		Paths:     p.Paths,
		Studios:   studios,
		Provider:  p.cfg.Provider,
		Version:   p.cfg.Version,
		FreeBytes: p.cfg.FreeBytes,
		Logf:      p.cfg.Logf,
	})
	return svc, NewHandler(svc, p.Tokens.Authenticate, p.cfg.Logf)
}

// Readopted is what follows re-adoption: every token and stage directory of a
// group that is no longer live is revoked and removed. A group that died with
// the last provider never had its GroupEnded run.
func (p *Platform) Readopted(ctx context.Context, sup *supervisor.Supervisor) (revoked int64, cleared []string, err error) {
	live := LiveGroups(ctx, sup)
	if revoked, err = p.Tokens.RevokeAllExcept(ctx, live); err != nil {
		return 0, nil, fmt.Errorf("revoking the tokens of groups that did not survive: %w", err)
	}
	cleared, err = p.Launches.ClearStages(live)
	return revoked, cleared, err
}

// LiveGroups lists the run ids of every group starting, running or stopping.
func LiveGroups(ctx context.Context, sup *supervisor.Supervisor) []string {
	ids := sup.Unmanaged()
	for _, st := range sup.Studios() {
		ids = append(ids, st.Manifest.ID)
	}
	var out []string
	for _, id := range ids {
		gs, err := sup.Status(ctx, id)
		if err != nil || gs.GroupRunID == "" {
			continue
		}
		switch gs.State {
		case "starting", "running", "stopping":
			out = append(out, gs.GroupRunID)
		}
	}
	return out
}

// ClearStages removes every stage/<studio>/<group_run> whose group is not in
// live, through os.Root so nothing a studio linked there is followed
// (second review #6). Anything not adopted is discarded, as at a stop (07 §4).
func (l *Launches) ClearStages(live []string) ([]string, error) {
	root, err := os.OpenRoot(l.StageRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening the stage root: %w", err)
	}
	defer root.Close()
	studios, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("listing the stage root: %w", err)
	}
	var cleared []string
	for _, s := range studios {
		if !s.IsDir() {
			continue
		}
		runs, err := fs.ReadDir(root.FS(), s.Name())
		if err != nil {
			continue
		}
		for _, r := range runs {
			if slices.Contains(live, r.Name()) {
				continue
			}
			rel := filepath.Join(s.Name(), r.Name())
			if err := root.RemoveAll(rel); err != nil {
				return cleared, fmt.Errorf("clearing the stage directory %s: %w", rel, err)
			}
			cleared = append(cleared, rel)
		}
	}
	return cleared, nil
}
