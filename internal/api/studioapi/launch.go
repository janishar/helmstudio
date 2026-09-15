package studioapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/supervisor"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Launches is the supervisor's PlatformHooks: what 07 §3 injects at spawn,
// and what a stopped group gives back.
type Launches struct {
	Tokens *Tokens
	// API is HELM_API, e.g. http://127.0.0.1:8700/api/v1.
	API string
	// StageRoot holds stage/<studio>/<group_run>/.
	StageRoot string
	Logf      func(string, ...any)
}

var _ supervisor.PlatformHooks = (*Launches)(nil)

// StageDir is a launch's stage directory.
func StageDir(root, studioID, groupRunID string) string {
	return filepath.Join(root, studioID, groupRunID)
}

func (l *Launches) LaunchEnv(ctx context.Context, st supervisor.Studio, groupRunID string) ([]string, error) {
	m := st.Manifest
	stage := StageDir(l.StageRoot, m.ID, groupRunID)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, fmt.Errorf("creating the stage directory %s: %w", stage, err)
	}
	env := []string{
		helm.EnvAPI + "=" + l.API,
		helm.EnvStudioID + "=" + m.ID,
		helm.EnvStageDir + "=" + stage,
	}
	tok, err := l.Tokens.Mint(ctx, m.ID, groupRunID, m.Capabilities)
	if err != nil {
		return nil, err
	}
	if tok != "" {
		env = append(env, helm.EnvToken+"="+tok)
	}
	return env, nil
}

func (l *Launches) GroupEnded(studioID, groupRunID string) {
	if err := l.Tokens.RevokeGroup(context.Background(), groupRunID); err != nil && l.Logf != nil {
		l.Logf("studioapi: revoking the tokens of %s's run %s: %v", studioID, groupRunID, err)
	}
	// Anything not adopted is discarded (07 §4). Through os.Root, so a
	// symlink a studio left in its stage is removed as a link, never followed.
	root, err := os.OpenRoot(l.StageRoot)
	if err != nil {
		return
	}
	defer root.Close()
	if err := root.RemoveAll(filepath.Join(studioID, groupRunID)); err != nil && !errors.Is(err, fs.ErrNotExist) && l.Logf != nil {
		l.Logf("studioapi: clearing the stage directory of %s's run %s: %v", studioID, groupRunID, err)
	}
}

// SupervisorStudios answers Studios from the supervisor's loaded manifests.
type SupervisorStudios struct{ Sup *supervisor.Supervisor }

func (s SupervisorStudios) Manifest(id string) (*manifest.Manifest, bool) {
	st, ok := s.Sup.Studio(id)
	if !ok {
		return nil, false
	}
	return st.Manifest, true
}

// DirPaths places stage and data directories under the resolved roots, as the
// supervisor does.
type DirPaths struct {
	StageRoot string
	DataRoot  string
	LogsRoot  string
	// StudioData overrides {data}; nil is <data>/studios/<id>/data.
	StudioData func(studioID string) string
}

func (d DirPaths) Stage(p Principal) string {
	if p.GroupRunID == "" {
		return ""
	}
	return StageDir(d.StageRoot, p.StudioID, p.GroupRunID)
}

func (d DirPaths) Data(studioID string) string {
	if d.StudioData != nil {
		return d.StudioData(studioID)
	}
	return filepath.Join(d.DataRoot, "studios", studioID, "data")
}

func (d DirPaths) Logs() string { return d.LogsRoot }
