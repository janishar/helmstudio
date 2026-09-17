package handshake_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/handshake"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
)

// current is the record a daemon in this process would publish.
func current(t *testing.T, dirs *platform.Dirs) handshake.Record {
	t.Helper()
	r, err := handshake.Current("127.0.0.1:8700", "test", dirs.Data())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	return r
}

// The shell reads one line and parses it. Both halves of that matter: a
// record spread over two lines would leave the shell waiting, and a record
// the shell cannot parse is the same as no daemon.
func TestAnnounceIsOneParseableLine(t *testing.T) {
	dirs := platformtest.Dirs(t)
	var out bytes.Buffer
	want := current(t, dirs)
	if err := handshake.Announce(&out, want); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	s := out.String()
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("Announce wrote %q, which does not end the line; a shell reading a line would block", s)
	}
	if n := strings.Count(s, "\n"); n != 1 {
		t.Errorf("Announce wrote %d lines, want 1:\n%s", n, s)
	}
	var got handshake.Record
	if err := json.Unmarshal([]byte(s), &got); err != nil {
		t.Fatalf("parsing the announced line %q: %v", s, err)
	}
	if got != want {
		t.Errorf("announced %+v, want %+v", got, want)
	}
}

// URL is the origin the launcher is served from. A shell that built its own
// would eventually build one the Origin check refuses.
func TestCurrentURLIsTheOriginOfAddr(t *testing.T) {
	dirs := platformtest.Dirs(t)
	r := current(t, dirs)
	if r.URL != "http://"+r.Addr {
		t.Errorf("URL is %q for addr %q, want %q", r.URL, r.Addr, "http://"+r.Addr)
	}
	if r.PID != os.Getpid() {
		t.Errorf("PID is %d, want this process %d", r.PID, os.Getpid())
	}
	if r.StartTime == 0 {
		t.Error("StartTime is 0, so Adopt could not tell this process from a later one with the same pid")
	}
	if r.Data != dirs.Data() {
		t.Errorf("Data is %q, want the data root %q", r.Data, dirs.Data())
	}
}

func TestPublishReadRoundTrip(t *testing.T) {
	dirs := platformtest.Dirs(t)
	want := current(t, dirs)
	if err := handshake.Publish(dirs, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := handshake.Read(dirs)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if want.Data != dirs.Data() {
		t.Errorf("the record names %q as its tree, want %q", want.Data, dirs.Data())
	}
}

// Publishing must replace, not append or fail: a daemon restarting in the
// same tree writes over whatever the last one left.
func TestPublishReplacesAndLeavesNoTemporaryFiles(t *testing.T) {
	dirs := platformtest.Dirs(t)
	first := current(t, dirs)
	first.Addr, first.URL = "127.0.0.1:9999", "http://127.0.0.1:9999"
	if err := handshake.Publish(dirs, first); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	second := current(t, dirs)
	if err := handshake.Publish(dirs, second); err != nil {
		t.Fatalf("Publish again: %v", err)
	}
	got, err := handshake.Read(dirs)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != second {
		t.Errorf("read back %+v, want the second record %+v", got, second)
	}
	entries, err := os.ReadDir(dirs.Data())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), handshake.Name) && e.Name() != handshake.Name {
			t.Errorf("Publish left %s behind in the data root", e.Name())
		}
	}
	info, err := os.Stat(handshake.Path(dirs))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s is mode %o, want 600", handshake.Name, perm)
	}
}

func TestAdoptFindsALiveDaemon(t *testing.T) {
	dirs := platformtest.Dirs(t)
	want := current(t, dirs)
	if err := handshake.Publish(dirs, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := handshake.Adopt(dirs)
	if err != nil {
		t.Fatalf("Adopt: %v, want the record of this process", err)
	}
	if got != want {
		t.Errorf("adopted %+v, want %+v", got, want)
	}
}

func TestAdoptWithNoRecord(t *testing.T) {
	dirs := platformtest.Dirs(t)
	if _, err := handshake.Adopt(dirs); !errors.Is(err, handshake.ErrNoDaemon) {
		t.Errorf("Adopt with no record: %v, want ErrNoDaemon", err)
	}
	if _, err := handshake.Read(dirs); !errors.Is(err, handshake.ErrNoDaemon) {
		t.Errorf("Read with no record: %v, want ErrNoDaemon", err)
	}
}

// The case the start time exists for. The pid is live and is this very
// process, so a check that compared pids alone would adopt a daemon that is
// not running and point the shell at a port nothing is listening on.
func TestAdoptRefusesALivePIDWithADifferentStartTime(t *testing.T) {
	dirs := platformtest.Dirs(t)
	r := current(t, dirs)
	r.StartTime++
	if err := handshake.Publish(dirs, r); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := handshake.Adopt(dirs); !errors.Is(err, handshake.ErrNoDaemon) {
		t.Errorf("Adopt on a reused pid: %v, want ErrNoDaemon", err)
	}
}

func TestAdoptRefusesAProcessThatHasGone(t *testing.T) {
	dirs := platformtest.Dirs(t)
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running a process to get a dead pid: %v", err)
	}
	r := current(t, dirs)
	r.PID = cmd.Process.Pid
	if err := handshake.Publish(dirs, r); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := handshake.Adopt(dirs); !errors.Is(err, handshake.ErrNoDaemon) {
		t.Errorf("Adopt on a dead pid: %v, want ErrNoDaemon", err)
	}
}

// A reader never deletes what it finds: the next daemon replaces the record
// when it listens, and a stale one is still evidence for a person debugging.
func TestAdoptLeavesAStaleRecordAlone(t *testing.T) {
	dirs := platformtest.Dirs(t)
	r := current(t, dirs)
	r.StartTime++
	if err := handshake.Publish(dirs, r); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := handshake.Adopt(dirs); !errors.Is(err, handshake.ErrNoDaemon) {
		t.Fatalf("Adopt: %v, want ErrNoDaemon", err)
	}
	if _, err := os.Stat(handshake.Path(dirs)); err != nil {
		t.Errorf("Adopt removed the stale record: %v", err)
	}
}

func TestWithdrawRemovesAndIsIdempotent(t *testing.T) {
	dirs := platformtest.Dirs(t)
	if err := handshake.Publish(dirs, current(t, dirs)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := handshake.Withdraw(dirs); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if _, err := os.Stat(handshake.Path(dirs)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after Withdraw, %s: %v, want it gone", handshake.Name, err)
	}
	if err := handshake.Withdraw(dirs); err != nil {
		t.Errorf("Withdraw on a record already gone: %v, want nil", err)
	}
}

func TestReadRejectsARecordThatIsNotJSON(t *testing.T) {
	dirs := platformtest.Dirs(t)
	if err := os.WriteFile(handshake.Path(dirs), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := handshake.Read(dirs)
	if err == nil {
		t.Fatal("Read on a corrupt record: nil, want an error naming the file")
	}
	if errors.Is(err, handshake.ErrNoDaemon) {
		t.Errorf("Read on a corrupt record: %v, want an error a person can act on, not ErrNoDaemon", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(handshake.Path(dirs))) {
		t.Errorf("Read error %q does not name %s", err, handshake.Name)
	}
}
