package install

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

// tree records every entry under dir with its type, mode, size, mtime,
// symlink target and content hash, so any change shows.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		line := fmt.Sprintf("%s %v %d %d", rel, fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		if fi.Mode()&fs.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			line += " -> " + target
		} else if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			line += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// R67: uninstall stops the studio, removes the checkout helmstudio cloned and
// the installation with its bindings, and leaves the studio's data, its
// downloaded weights and any linked directory exactly as they were.
func TestUninstallRemovesTheCheckoutAndNothingElse(t *testing.T) {
	f := newFixture(t, nil)
	f.hub.Add("org/linked", "FL2VA/dit.safetensors", hubtest.Content(10, 1), true)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("toy-studio")
	linkedWeight := st.Manifest.Weights[0]
	linkedWeight.Name, linkedWeight.Repo, linkedWeight.Dest, linkedWeight.Files = "linked", "org/linked", "Linked", []string{"FL2VA/**"}
	st.Manifest.Weights = append(st.Manifest.Weights, linkedWeight)

	user := filepath.Join(t.TempDir(), "user models", "Linked")
	os.MkdirAll(filepath.Join(user, "FL2VA"), 0o755)
	os.WriteFile(filepath.Join(user, "FL2VA", "dit.safetensors"), []byte("the user's"), 0o644)
	if _, err := f.in.LinkWeight(context.Background(), "toy-studio", "linked", user); err != nil {
		t.Fatal(err)
	}
	f.install("toy-studio")
	if _, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(f.dirs.Data(), "studios", "toy-studio", "data")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "take-001.mp4"), []byte("a take"), 0o644)
	userBefore, dataBefore, modelsBefore := tree(t, user), tree(t, data), tree(t, filepath.Join(f.dirs.Models(), "Toy"))

	j, err := f.in.Uninstall(context.Background(), "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(j); got.State != JobSucceeded {
		t.Fatalf("uninstall job = %+v", got)
	}
	if f.sup.Running("toy-studio") {
		t.Fatal("the studio is still running after uninstall")
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Data(), "studios", "toy-studio", "src")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the checkout survived uninstall: %v", err)
	}
	if got := f.info("toy-studio"); got.State != StateListed {
		t.Fatalf("after uninstall: %+v, want listed", got)
	}
	if n := f.count(`SELECT count(*) FROM studio_model_bindings`); n != 0 {
		t.Fatalf("%d bindings survived uninstall", n)
	}
	if n := f.count(`SELECT count(*) FROM model_artifacts`); n != 2 {
		t.Fatalf("artifacts after uninstall = %d; want both kept for reclaim", n)
	}
	if tree(t, user) != userBefore || tree(t, data) != dataBefore || tree(t, filepath.Join(f.dirs.Models(), "Toy")) != modelsBefore {
		t.Fatal("uninstall changed the linked directory, the studio's data or the downloaded weights")
	}
	if n := f.count(`SELECT count(*) FROM jobs WHERE studio_id = 'toy-studio'`); n < 2 {
		t.Fatalf("job history did not survive uninstall: %d jobs", n)
	}
}

// A local_path checkout is the user's: uninstall never removes it.
func TestUninstallNeverRemovesALocalPathCheckout(t *testing.T) {
	f := newFixture(t, nil)
	local := filepath.Join(t.TempDir(), "my-studio")
	os.MkdirAll(filepath.Join(local, "engine"), 0o755)
	os.WriteFile(filepath.Join(local, "main.go"), []byte("package main"), 0o644)
	f.studio("local-studio", "local_path: "+local, f.defaultBuild())
	f.install("local-studio")
	if got := f.info("local-studio"); got.State != StateReady || got.Root != local {
		t.Fatalf("local install: %+v", got)
	}
	before := tree(t, local)
	j, err := f.in.Uninstall(context.Background(), "local-studio")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(j)
	if tree(t, local) != before {
		t.Fatal("uninstall changed a local_path checkout")
	}
}

// Uninstalling mid-install cancels the install first.
func TestUninstallCancelsARunningInstall(t *testing.T) {
	f := newFixture(t, nil)
	started := filepath.Join(f.work, "started")
	f.studio("toy-studio", f.repoLine(), `
  - { name: slow, run: "touch '`+started+`'; sleep 60" }
`)
	j, err := f.in.Install(context.Background(), "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, started)
	u, err := f.in.Uninstall(context.Background(), "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(u); got.State != JobSucceeded {
		t.Fatalf("uninstall = %+v", got)
	}
	if got, _ := f.in.Job(context.Background(), j.ID); got.State != JobCancelled {
		t.Fatalf("install job = %+v, want cancelled", got)
	}
	if got := f.info("toy-studio"); got.State != StateListed {
		t.Fatalf("after uninstall: %+v", got)
	}
}

// DoD: no delete path follows a symlink. A symlink to a directory holding a
// sentinel is planted wherever a delete path walks — inside a cloned
// checkout, as a checkout itself, inside a downloaded weight, as a linked
// weight, and as an old build log — then every delete path runs: uninstall,
// reclaim, unlink and build-log retention. The sentinel directory must be
// exactly as it was.
func TestNoDeletePathFollowsASymlink(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.BuildLogsKept = 1 })
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "precious")
	os.MkdirAll(filepath.Join(outside, "deeper"), 0o755)
	os.WriteFile(filepath.Join(outside, "deeper", "sentinel"), []byte("keep me"), 0o644)
	os.WriteFile(filepath.Join(outside, "old-build.log"), []byte("not a log of ours"), 0o644)
	before := tree(t, outside)

	// A cloned checkout with a symlink inside, and a downloaded weight with
	// a symlink inside.
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.install("toy-studio")
	src := filepath.Join(f.dirs.Data(), "studios", "toy-studio", "src")
	mustSymlink(t, outside, filepath.Join(src, "engine", "escape"))
	mustSymlink(t, outside, filepath.Join(f.dirs.Models(), "Toy", "escape"))

	// A second studio whose checkout directory has been replaced by a
	// symlink.
	f.studio("second-studio", f.repoLine(), f.defaultBuild())
	f.install("second-studio")
	second := filepath.Join(f.dirs.Data(), "studios", "second-studio", "src")
	if err := os.RemoveAll(second); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, second)

	// A linked weight.
	user := filepath.Join(t.TempDir(), "user", "Linked")
	os.MkdirAll(filepath.Join(user, "FL2VA"), 0o755)
	os.WriteFile(filepath.Join(user, "FL2VA", "dit.safetensors"), []byte("x"), 0o644)
	mustSymlink(t, outside, filepath.Join(user, "FL2VA", "escape"))
	st, _ := f.sup.Studio("toy-studio")
	lw := st.Manifest.Weights[0]
	lw.Name, lw.Repo, lw.Dest, lw.Files = "linked", "org/linked", "Linked", []string{"FL2VA/**"}
	art, err := f.w.Link(ctx, "nobody", lw, user)
	if err != nil {
		t.Fatal(err)
	}

	// An old build log that is a symlink to a file outside the logs root.
	oldLog := filepath.Join("studios", "toy-studio", "build-00000000000000000000000000-00.log")
	mustSymlink(t, filepath.Join(outside, "old-build.log"), filepath.Join(f.dirs.Logs(), oldLog))
	if err := f.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES ('old', ?, 'build', 'step_run', 'gone', 'toy-studio', 0)`, oldLog)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Every delete path.
	for _, id := range []string{"toy-studio", "second-studio"} {
		j, err := f.in.Uninstall(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.wait(j); got.State != JobSucceeded {
			t.Fatalf("uninstall %s: %+v", id, got)
		}
	}
	if _, err := os.Lstat(second); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the symlinked checkout was not removed as a link: %v", err)
	}
	p, err := f.w.PreviewReclaim(ctx)
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("preview = %+v, %v; want the Toy download", p, err)
	}
	if _, err := f.w.Reclaim(ctx, p.Confirm); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Unlink(ctx, art.ID); err != nil {
		t.Fatal(err)
	}
	f.in.retainBuildLogs(ctx, "toy-studio")
	if _, err := os.Lstat(filepath.Join(f.dirs.Logs(), oldLog)); err != nil {
		t.Fatalf("retention removed a log that is a symlink: %v", err)
	}

	if after := tree(t, outside); after != before {
		t.Fatalf("a delete path followed a symlink.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(user, "FL2VA", "dit.safetensors")); err != nil {
		t.Fatalf("the linked directory lost a file: %v", err)
	}

	// A studio directory that is itself a symlink to somewhere holding a
	// src: uninstall refuses rather than removing inside the target.
	os.MkdirAll(filepath.Join(outside, "src"), 0o755)
	os.WriteFile(filepath.Join(outside, "src", "theirs"), []byte("theirs"), 0o644)
	before = tree(t, outside)
	f.studio("third-studio", f.repoLine(), f.defaultBuild())
	f.install("third-studio")
	third := filepath.Join(f.dirs.Data(), "studios", "third-studio")
	if err := os.RemoveAll(third); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, third)
	j, err := f.in.Uninstall(ctx, "third-studio")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(j); got.State != JobFailed || got.LastError == nil || !strings.Contains(got.LastError.Message, "not a directory helmstudio made") {
		t.Fatalf("uninstall through a symlinked studio directory: %+v", got)
	}
	if after := tree(t, outside); after != before {
		t.Fatalf("uninstall removed inside a symlinked studio directory's target.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// Build logs keep the newest install jobs' files and remove the rest.
func TestBuildLogRetentionKeepsTheNewestJobs(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.BuildLogsKept = 1 })
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.failStepTwo(true)
	first := f.install("toy-studio")
	time.Sleep(5 * time.Millisecond)
	f.failStepTwo(false)
	second := f.install("toy-studio")
	if n := f.count(`SELECT count(*) FROM log_files WHERE kind = 'build' AND path LIKE ?`, "%"+first.ID+"%"); n != 0 {
		t.Fatalf("%d build logs of the older job survived retention", n)
	}
	if n := f.count(`SELECT count(*) FROM log_files WHERE kind = 'build' AND path LIKE ?`, "%"+second.ID+"%"); n != 2 {
		t.Fatalf("the newest job has %d build logs, want 2", n)
	}
	for _, s := range first.Steps {
		if s.LogPath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(f.dirs.Logs(), s.LogPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("an old build log file survived: %v", err)
		}
	}
}

// Review M3 #4, R19b: installing again while a linked drive is unplugged
// refuses the job with the path and leaves the install state as it was, so
// the studio launches again as soon as the drive is back.
func TestMissingLinkedDriveLeavesInstallStateUntouched(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("toy-studio")
	st.Manifest.Weights[0].Files = nil
	user := filepath.Join(t.TempDir(), "drive", "Toy")
	os.MkdirAll(user, 0o755)
	os.WriteFile(filepath.Join(user, "model.bin"), []byte("mine"), 0o644)
	if _, err := f.in.LinkWeight(context.Background(), "toy-studio", "base", user); err != nil {
		t.Fatal(err)
	}
	f.install("toy-studio")
	unplugged := user + "-unplugged"
	os.Rename(user, unplugged)

	j := f.install("toy-studio")
	info := f.info("toy-studio")
	if j.State != JobFailed || j.LastError == nil || j.LastError.Code != "linked_missing" || !strings.Contains(j.LastError.Message, user) {
		t.Fatalf("job = %+v; want a refusal naming %s", j, user)
	}
	if info.State != StateReady || info.LastFailure != nil {
		t.Fatalf("install state = %+v; want ready and untouched", info)
	}
	os.Rename(unplugged, user)
	if _, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatalf("launch once the drive is back: %v", err)
	}
}

// Review M3 #12: a symlinked <logs>/studios/<id> directory puts nothing of
// its target within reach of build-log retention.
func TestBuildLogRetentionNeverFollowsASymlinkedDirectory(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.BuildLogsKept = 1 })
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.failStepTwo(true)
	first := f.install("toy-studio")
	dir := filepath.Join(f.dirs.Logs(), "studios", "toy-studio")
	elsewhere := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, elsewhere); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, elsewhere, dir)
	olds, _ := filepath.Glob(filepath.Join(elsewhere, "build-"+first.ID+"-*.log"))
	if len(olds) != 2 {
		t.Fatalf("the first job's logs in the moved directory: %v", olds)
	}
	time.Sleep(5 * time.Millisecond)
	f.failStepTwo(false)
	f.install("toy-studio") // retention now wants the first job's logs gone
	for _, p := range olds {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("retention removed %s through a symlinked log directory: %v", p, err)
		}
	}
}
