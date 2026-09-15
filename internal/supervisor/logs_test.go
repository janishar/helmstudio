package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startStream(t *testing.T, limits LogLimits) (*logStream, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.log")
	l := newLogStream(path, limits)
	w, err := l.openForProcess()
	if err != nil {
		t.Fatal(err)
	}
	go l.run(5 * time.Millisecond)
	t.Cleanup(func() { w.Close(); l.close() })
	return l, w
}

// The ring replayed to a new viewer is bounded by lines and by bytes.
func TestLogRingIsBounded(t *testing.T) {
	limits := DefaultLogLimits
	limits.RingLines, limits.RingBytes = 100, 1<<20
	l, w := startStream(t, limits)
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	waitFor(t, 5*time.Second, "the tailer to catch up", func() bool {
		lines := l.lastLines(1)
		return len(lines) == 1 && lines[0] == "line 4999"
	})
	history, sub := l.subscribe(0)
	l.unsubscribe(sub)
	if len(history) != 100 || history[0].Text != "line 4900" {
		t.Fatalf("history has %d lines starting %q; want the last 100", len(history), history[0].Text)
	}

	limits.RingLines, limits.RingBytes = 1000, 64
	l2, w2 := startStream(t, limits)
	for i := 0; i < 100; i++ {
		fmt.Fprintf(w2, "%030d\n", i)
	}
	waitFor(t, 5*time.Second, "the tailer to catch up", func() bool {
		lines := l2.lastLines(1)
		return len(lines) == 1 && strings.HasSuffix(lines[0], "99")
	})
	history, sub = l2.subscribe(0)
	l2.unsubscribe(sub)
	if len(history) > 3 {
		t.Fatalf("history holds %d 30-byte lines under a 64-byte cap", len(history))
	}
}

// A viewer that never reads does not slow the stream: other viewers and the
// ring keep up, and the stuck viewer is told how many lines it lost.
func TestSlowViewerLosesLinesAndIsTold(t *testing.T) {
	limits := DefaultLogLimits
	limits.SubscriberBuffer = 10
	l, w := startStream(t, limits)
	_, stuck := l.subscribe(0)
	defer l.unsubscribe(stuck)
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	waitFor(t, 5*time.Second, "the stream to reach the last line despite a stuck viewer", func() bool {
		lines := l.lastLines(1)
		return len(lines) == 1 && lines[0] == "line 999"
	})
	history, late := l.subscribe(0)
	l.unsubscribe(late)
	if len(history) != 1000 {
		t.Fatalf("a viewer arriving now replays %d lines, want all 1000", len(history))
	}
	if len(stuck.ch) != 10 || stuck.dropped.Load() != 990 {
		t.Fatalf("stuck viewer: %d buffered, %d dropped; want 10 and 990", len(stuck.ch), stuck.dropped.Load())
	}
}

// A studio that logs faster than the tailer reads is skipped ahead rather
// than buffered without bound, and the skip is reported.
func TestTailerThatFallsBehindSkipsAhead(t *testing.T) {
	limits := DefaultLogLimits
	limits.LagLimit, limits.ReadChunk = 64<<10, 8<<10
	path := filepath.Join(t.TempDir(), "run.log")
	l := newLogStream(path, limits)
	w, _ := l.openForProcess()
	defer w.Close()
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < 10000; i++ { // ~1 MiB before the tailer ever runs
		w.WriteString(line)
	}
	w.WriteString("the end\n")
	go l.run(5 * time.Millisecond)
	defer l.close()
	waitFor(t, 5*time.Second, "the last line", func() bool {
		lines := l.lastLines(1)
		return len(lines) == 1 && lines[0] == "the end"
	})
	history, sub := l.subscribe(0)
	l.unsubscribe(sub)
	var gap int64
	for _, h := range history {
		if h.GapUnit == "bytes" {
			gap += h.Gap
		}
	}
	if gap < 900<<10 {
		t.Fatalf("reported %d skipped bytes; want most of the megabyte", gap)
	}
	if len(history) > int(limits.ReadChunk)/100+5 {
		t.Fatalf("buffered %d lines after skipping; want about one chunk's worth", len(history))
	}
}

// R30: a run log over its cap is compacted to head, marker and tail while the
// process keeps appending, and the log_files row is marked truncated.
func TestLogFileIsCappedHeadAndTail(t *testing.T) {
	limits := DefaultLogLimits
	limits.FileCap, limits.HeadBytes = 64<<10, 8<<10
	truncated := make(chan struct{}, 1)
	path := filepath.Join(t.TempDir(), "run.log")
	l := newLogStream(path, limits)
	l.onTruncate = func() { truncated <- struct{}{} }
	w, _ := l.openForProcess()
	go l.run(5 * time.Millisecond)
	defer l.close()

	for i := 0; i < 20000; i++ {
		fmt.Fprintf(w, "line %05d %s\n", i, strings.Repeat("y", 40))
	}
	w.Close()
	select {
	case <-truncated:
	case <-time.After(5 * time.Second):
		t.Fatal("never compacted")
	}
	waitFor(t, 5*time.Second, "the file to settle under its cap", func() bool {
		fi, _ := os.Stat(path)
		return fi.Size() <= limits.FileCap
	})
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.HasPrefix(s, "line 00000 ") || !strings.Contains(s, "[helmstudio: ") || !strings.HasSuffix(s, "line 19999 "+strings.Repeat("y", 40)+"\n") {
		t.Fatalf("compacted log does not keep head, marker and tail:\n%.200s … %s", s, s[max(0, len(s)-200):])
	}
	for _, ln := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if !strings.HasPrefix(ln, "line ") && !strings.HasPrefix(ln, "[helmstudio: ") {
			t.Fatalf("compaction left a broken line: %q", ln)
		}
	}
}

// R30: logs are kept for the last RunsKept group runs per studio. Older run
// logs are deleted with their rows; a log path that has been replaced by a
// symlink is never followed or removed.
func TestLogRetentionKeepsLastRunsAndNeverFollowsSymlinks(t *testing.T) {
	e := newEnv(t, Config{RunsKept: 2})
	e.manifest("chatty", "", `
  - name: studio
    role: main
    cmd: '"$SUPERVISOR_TEST_HELPER" helper exit --after-ms 10 --code 0'
`)
	target := filepath.Join(t.TempDir(), "precious.txt")
	os.WriteFile(target, []byte("not a log"), 0o600)

	var paths []string
	for i := 0; i < 4; i++ {
		if _, err := e.sup.Launch(context.Background(), "chatty", LaunchOptions{}); err != nil {
			t.Fatal(err)
		}
		e.waitDone("chatty")
		p := e.logPath("chatty", "studio")
		paths = append(paths, p)
		if i == 0 {
			os.Remove(p)
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	files, err := e.sup.LogFiles(context.Background(), "chatty")
	if err != nil {
		t.Fatal(err)
	}
	// Run 0's row survives because its path is a symlink and was refused.
	if len(files) != 3 {
		t.Fatalf("%d log rows remain, want 3 (the two newest runs and the refused symlink): %+v", len(files), files)
	}
	for i, p := range paths {
		_, err := os.Lstat(p)
		switch i {
		case 0:
			if err != nil {
				t.Errorf("the symlink at %s was removed", p)
			}
		case 1:
			if err == nil {
				t.Errorf("run %d's log %s was kept beyond the limit", i, p)
			}
		default:
			if err != nil {
				t.Errorf("run %d's log %s was deleted: %v", i, p, err)
			}
		}
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "not a log" {
		t.Fatalf("the symlink's target was touched: %q %v", b, err)
	}
}
