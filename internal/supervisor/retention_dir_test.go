package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// M3 review #12: when <logs>/studios/<id> itself has become a symlink, run-log
// retention removes nothing in its target.
func TestLogRetentionNeverFollowsASymlinkedStudioLogDirectory(t *testing.T) {
	e := newEnv(t, Config{RunsKept: 1})
	e.manifest("chatty", "", `
  - name: studio
    role: main
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 10 --code 0'
`)
	if _, err := e.sup.Launch(context.Background(), "chatty", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("chatty")
	old := e.logPath("chatty", "studio")

	// Move the studio's log directory elsewhere and leave a symlink to it.
	dir := filepath.Dir(old)
	elsewhere := filepath.Join(t.TempDir(), "moved-logs")
	if err := os.Rename(dir, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(elsewhere, filepath.Base(old))

	if _, err := e.sup.Launch(context.Background(), "chatty", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("chatty")
	if _, err := e.sup.Launch(context.Background(), "chatty", LaunchOptions{}); err != nil {
		t.Fatal(err)
	}
	e.waitDone("chatty")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("retention removed a log through a symlinked directory: %v", err)
	}
}
