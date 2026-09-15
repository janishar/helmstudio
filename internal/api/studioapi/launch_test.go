package studioapi_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// launchable records a manifest and its installation as install leaves them.
func launchable(t *testing.T, st *store.Store, yaml string) supervisor.Studio {
	t.Helper()
	f := filepath.Join(t.TempDir(), "helmstudio.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, res, err := manifest.Load(f)
	if err != nil || !res.OK() {
		t.Fatal(err, res.Errors)
	}
	err = st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at) VALUES (?, ?, ?, 'ready', 1, 1)`,
			m.ID, m.Digest, m.LocalPath)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return supervisor.Studio{Manifest: m, File: f}
}

func manifestYAML(id, root, caps, cmd string) string {
	return `id: ` + id + `
name: ` + strings.ReplaceAll(id, "-", " ") + `
kinds: [video]
local_path: ` + root + `
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
capabilities: [` + caps + `]
processes:
  - name: studio
    cmd: ` + cmd + `
`
}

// A launched process gets HELM_API, HELM_STUDIO_ID, a stage directory and a
// token that works while its group runs; stopping the group revokes the token
// and clears the stage (07 §3, 07 §4, Q6).
func TestLaunchInjectsTheTokenAndStopRevokesIt(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tokens := &studioapi.Tokens{Store: st}
	launches := &studioapi.Launches{Tokens: tokens, API: "http://127.0.0.1:8700/api/v1", StageRoot: d.Stage(), Logf: t.Logf}
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: 2 * time.Second, PortMin: 43000, PortMax: 43999, Platform: launches})
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		sup.Shutdown(c)
	})

	root := t.TempDir()
	out := filepath.Join(root, "env.txt")
	withCaps := launchable(t, st, manifestYAML("token-studio", root, "kv, gallery",
		`"env | grep ^HELM_ > `+out+`.tmp && mv `+out+`.tmp `+out+` && touch \"$HELM_STAGE_DIR/staged\" && sleep 60"`))
	noCaps := launchable(t, st, manifestYAML("plain-studio", t.TempDir(), "", `"env | grep ^HELM_ > `+filepath.Join(root, "plain.txt")+`; sleep 60"`))
	badEnv := manifestYAML("sneaky-studio", t.TempDir(), "kv", `"sleep 60"`) + "    env: { HELM_API: http://evil.example }\n"
	sneaky := launchable(t, st, badEnv)
	sup.SetStudios([]supervisor.Studio{withCaps, noCaps, sneaky})

	gs, err := sup.Launch(ctx, "token-studio", supervisor.LaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := waitFile(t, out)
	vars := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(env), "\n") {
		k, v, _ := strings.Cut(line, "=")
		vars[k] = v
	}
	stage := studioapi.StageDir(d.Stage(), "token-studio", gs.GroupRunID)
	if vars["HELM_API"] != "http://127.0.0.1:8700/api/v1" || vars["HELM_STUDIO_ID"] != "token-studio" || vars["HELM_STAGE_DIR"] != stage || !strings.HasPrefix(vars["HELM_TOKEN"], studioapi.TokenPrefix) {
		t.Fatalf("injected environment = %v", vars)
	}
	waitFile(t, filepath.Join(stage, "staged"))
	p, err := tokens.Resolve(ctx, vars["HELM_TOKEN"])
	if err != nil || p.StudioID != "token-studio" || p.GroupRunID != gs.GroupRunID || strings.Join(p.Capabilities, ",") != "kv,gallery" {
		t.Fatalf("the injected token resolves to %+v, %v", p, err)
	}

	if err := sup.Stop("token-studio"); err != nil {
		t.Fatal(err)
	}
	if err := sup.Wait(ctx, "token-studio"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, resolveErr := tokens.Resolve(ctx, vars["HELM_TOKEN"])
		_, statErr := os.Stat(stage)
		if resolveErr != nil && os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after stop: token resolves (%v), stage exists (%v)", resolveErr, statErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A studio with no capabilities gets no token at all.
	if _, err := sup.Launch(ctx, "plain-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	if plain := waitFile(t, filepath.Join(root, "plain.txt")); strings.Contains(plain, "HELM_TOKEN") || !strings.Contains(plain, "HELM_API=") {
		t.Fatalf("a studio with no capabilities got: %s", plain)
	}

	// A manifest cannot set HELM_* itself.
	_, err = sup.Launch(ctx, "sneaky-studio", supervisor.LaunchOptions{})
	if err == nil || !strings.Contains(err.Error(), "HELM_API") {
		t.Fatalf("a manifest setting HELM_API launched: %v", err)
	}
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Second review #6: after re-adoption, the stage directories of groups that
// died with the last provider are cleared, the live group's is kept, and a
// symlink a studio left in a stage is removed as a link, never followed.
func TestClearStagesKeepsLiveGroupsAndFollowsNoSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "precious"), []byte("not the studio's"), 0o600)
	for _, dir := range []string{"h3-studio/dead-run", "h3-studio/live-run", "ltx-studio/other-dead"} {
		os.MkdirAll(filepath.Join(root, dir), 0o700)
		os.WriteFile(filepath.Join(root, dir, "unadopted.mp4"), []byte("render"), 0o600)
	}
	os.Symlink(outside, filepath.Join(root, "h3-studio/dead-run", "escape"))

	l := &studioapi.Launches{StageRoot: root}
	cleared, err := l.ClearStages([]string{"live-run"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cleared, ",") != filepath.Join("h3-studio", "dead-run")+","+filepath.Join("ltx-studio", "other-dead") {
		t.Fatalf("cleared = %v", cleared)
	}
	if _, err := os.Stat(filepath.Join(root, "h3-studio/live-run/unadopted.mp4")); err != nil {
		t.Fatalf("the live group's stage was cleared: %v", err)
	}
	for _, gone := range []string{"h3-studio/dead-run", "ltx-studio/other-dead"} {
		if _, err := os.Lstat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Fatalf("clearing a stage followed a symlink out of it: %v", err)
	}
}
