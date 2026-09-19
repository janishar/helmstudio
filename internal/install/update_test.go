package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// branchLine is a manifest pointing at a branch rather than a commit, which is
// the only shape an update makes sense for.
func (f *fixture) branchLine() string {
	return "repo: file://" + f.repo + "\nref: main"
}

// moveMain commits something new on the repository's main branch and returns
// the commit it made.
func (f *fixture) moveMain(name string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo, name), []byte("later\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.gitTest(f.repo, "add", ".")
	f.gitTest(f.repo, "commit", "-q", "-m", "moved")
	return f.gitTest(f.repo, "rev-parse", "HEAD")
}

// An update pulls what the branch points at now and builds it. This is the
// one path that takes code nobody approved when the studio was installed,
// which is why :update exists rather than :install doing it.
func TestUpdateBuildsTheBranchTip(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	f.gitTest(f.repo, "branch", "-M", "main")
	f.studio("toy-studio", f.branchLine(), f.defaultBuild())
	f.install("toy-studio")
	first := f.info("toy-studio")
	if first.State != StateReady || first.CommitSHA == "" {
		t.Fatalf("after install: %+v", first)
	}

	moved := f.moveMain("NEW")
	if moved == first.CommitSHA {
		t.Fatal("the repository did not move")
	}

	j, err := f.in.Update(ctx, "toy-studio")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := f.wait(j); got.State != JobSucceeded {
		t.Fatalf("update job = %+v", got)
	}
	after := f.info("toy-studio")
	if after.CommitSHA != moved {
		t.Errorf("checked out %s, want the tip %s", after.CommitSHA, moved)
	}
	if after.State != StateReady {
		t.Errorf("after update: %+v, want ready", after)
	}
	// The new commit's file is in the checkout, which is the whole point.
	if _, err := os.Stat(filepath.Join(first.Root, "NEW")); err != nil {
		t.Errorf("the tip's file is not in the checkout: %v", err)
	}
	// Build steps ran again: they had run against a different commit.
	if steps := f.steps(); !strings.Contains(steps, "one") {
		t.Errorf("no step ran for the new commit: %q", steps)
	}
}

// Nothing to pull is said rather than done: an update that would change
// nothing refuses instead of rebuilding.
func TestUpdateRefusesWhenAlreadyAtTheTip(t *testing.T) {
	f := newFixture(t, nil)
	f.gitTest(f.repo, "branch", "-M", "main")
	f.studio("toy-studio", f.branchLine(), f.defaultBuild())
	f.install("toy-studio")

	_, err := f.in.Update(context.Background(), "toy-studio")
	if err == nil {
		t.Fatal("updating to the commit already installed was accepted")
	}
	if !strings.Contains(err.Error(), "already at") {
		t.Errorf("refusal does not say it is up to date: %v", err)
	}
}

// A studio built from a directory on this Mac has no upstream, and the
// directory is the author's own. There is nothing to pull and nothing that
// may be overwritten.
func TestUpdateRefusesALocalPathStudio(t *testing.T) {
	f := newFixture(t, nil)
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "README"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.studio("toy-studio", "local_path: "+local, f.defaultBuild())

	_, err := f.in.Update(context.Background(), "toy-studio")
	if err == nil {
		t.Fatal("a local_path studio was offered an update")
	}
	if !strings.Contains(err.Error(), "directory on this Mac") {
		t.Errorf("refusal does not say why: %v", err)
	}
}

// CheckUpdate records where the ref points and says whether the studio is
// behind it (02 §5: update_available is remote_sha != commit_sha).
func TestCheckUpdateRecordsTheTipAndSaysWhenBehind(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	f.gitTest(f.repo, "branch", "-M", "main")
	f.studio("toy-studio", f.branchLine(), f.defaultBuild())
	f.install("toy-studio")

	// Up to date: a check says so and leaves it ready.
	if got, err := f.in.CheckUpdate(ctx, "toy-studio"); err != nil || got.State != StateReady {
		t.Fatalf("check on an up-to-date studio: %+v %v", got, err)
	}

	moved := f.moveMain("NEW")
	got, err := f.in.CheckUpdate(ctx, "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUpdateAvailable {
		t.Errorf("state = %q, want update_available", got.State)
	}
	var remote string
	if err := f.st.Reader().QueryRowContext(ctx,
		`SELECT COALESCE(remote_sha, '') FROM installations WHERE studio_id = 'toy-studio'`).Scan(&remote); err != nil {
		t.Fatal(err)
	}
	if remote != moved {
		t.Errorf("remote_sha = %q, want the tip %q", remote, moved)
	}
}
