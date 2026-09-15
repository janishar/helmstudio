package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// LogLimits bound what one process's output can cost the daemon.
type LogLimits struct {
	// FileCap is the size at which a run log is compacted to its head and
	// tail (docs/design/01-prd.md R30). Compaction keeps HeadBytes from the
	// start and the most recent FileCap/2 bytes.
	FileCap   int64
	HeadBytes int64
	// RingLines and RingBytes bound the in-memory buffer replayed to a new
	// log viewer, whichever is reached first.
	RingLines int
	RingBytes int
	// ReadChunk is the most the tailer reads per tick; LagLimit is how far
	// behind the file it may fall before it skips ahead and says so.
	ReadChunk int64
	LagLimit  int64
	// SubscriberBuffer is how many lines a slow viewer may fall behind before
	// it starts losing lines — it is told how many — instead of slowing
	// anything else down.
	SubscriberBuffer int
}

// DefaultLogLimits are the M2 defaults (docs/decisions.md, "M2 supervision").
var DefaultLogLimits = LogLimits{
	FileCap:          16 << 20,
	HeadBytes:        1 << 20,
	RingLines:        2000,
	RingBytes:        1 << 20,
	ReadChunk:        256 << 10,
	LagLimit:         4 << 20,
	SubscriberBuffer: 512,
}

// maxLine is the longest line kept whole; a longer run of bytes without a
// newline is split, so one enormous line cannot grow a buffer without bound.
const maxLine = 16 << 10

// LogLine is one line of a process's output. Seq increases by one per line
// for the life of the stream. A line with Gap > 0 is not output: it reports
// that Gap lines (for a slow viewer) or bytes (for a tailer that fell behind)
// were not delivered.
type LogLine struct {
	Seq     uint64 `json:"seq"`
	Text    string `json:"text,omitempty"`
	Gap     int64  `json:"gap,omitempty"`
	GapUnit string `json:"gap_unit,omitempty"` // "lines" or "bytes"
}

// logStream is one process's log for one group run.
//
// The process writes straight into the file — its stdout and stderr are the
// file, not a pipe to the daemon. That is what makes backpressure a non-issue
// for the studio (a kernel append never waits on the daemon) and what lets a
// process keep logging after a kill -9 of the daemon, instead of dying of
// SIGPIPE on its next line. The daemon tails the file at its own pace into a
// bounded ring buffer and fans lines out to viewers without ever blocking.
type logStream struct {
	path   string
	limits LogLimits
	// onTruncate is called once, the first time the file is compacted.
	onTruncate func()

	mu        sync.Mutex
	ring      []LogLine
	ringBytes int
	seq       uint64
	subs      map[*logSub]struct{}
	offset    int64
	partial   []byte
	truncated bool

	stop     chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

type logSub struct {
	ch      chan LogLine
	dropped atomic.Int64
}

func newLogStream(path string, limits LogLimits) *logStream {
	return &logStream{
		path:    path,
		limits:  limits,
		subs:    make(map[*logSub]struct{}),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// openForProcess opens (creating or appending to) the file the process
// writes to.
func (l *logStream) openForProcess() (*os.File, error) {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the log file %s: %w", l.path, err)
	}
	return f, nil
}

// seedFromFile loads the last bytes of an existing file into the ring and
// starts tailing from its end. Used when a process is re-adopted.
func (l *logStream) seedFromFile(tailBytes int64) {
	f, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	start := max(fi.Size()-tailBytes, 0)
	buf := make([]byte, fi.Size()-start)
	n, _ := f.ReadAt(buf, start)
	buf = buf[:n]
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	l.mu.Lock()
	l.offset = start + int64(n)
	l.mu.Unlock()
	l.ingest(buf)
}

// run tails the file until close is called, then drains what is left.
func (l *logStream) run(tick time.Duration) {
	defer close(l.stopped)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			for l.readOnce() {
			}
			l.flushPartial()
			return
		case <-t.C:
			for i := 0; i < 4 && l.readOnce(); i++ {
			}
		}
	}
}

// close stops the tailer after it has read everything written so far.
func (l *logStream) close() {
	l.stopOnce.Do(func() { close(l.stop) })
	<-l.stopped
}

// readOnce reads at most one chunk, compacting the file first when it is over
// its cap. It reports whether more may be waiting.
func (l *logStream) readOnce() bool {
	f, err := os.OpenFile(l.path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	size := fi.Size()
	if l.limits.FileCap > 0 && size > l.limits.FileCap {
		if newSize, err := l.compact(f, size); err == nil {
			size = newSize
		}
	}

	l.mu.Lock()
	offset := l.offset
	if size < offset { // replaced or truncated by someone else
		offset = 0
	}
	if lag := size - offset; l.limits.LagLimit > 0 && lag > l.limits.LagLimit {
		skipTo := size - l.limits.ReadChunk
		l.partial = nil
		l.appendLocked(LogLine{Gap: skipTo - offset, GapUnit: "bytes"})
		offset = skipTo
	}
	l.mu.Unlock()

	n := min(size-offset, l.limits.ReadChunk)
	if n <= 0 {
		return false
	}
	buf := make([]byte, n)
	got, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	l.mu.Lock()
	l.offset = offset + int64(got)
	l.mu.Unlock()
	l.ingest(buf[:got])
	return offset+int64(got) < size
}

// compact rewrites the file as its head, a marker naming how much was elided,
// and its most recent FileCap/2 bytes.
//
// The process still appends to the file while this runs. It opens the file
// with O_APPEND, so after the truncate its writes land at the new end rather
// than in a hole — but a line written between reading the tail and truncating
// is lost, and one written between truncating and appending the tail lands
// just before the marker. Both windows are microseconds wide and sit next to
// the marker that already says output was elided.
func (l *logStream) compact(f *os.File, size int64) (int64, error) {
	head := min(l.limits.HeadBytes, size)
	headBuf := make([]byte, head)
	if _, err := f.ReadAt(headBuf, 0); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if i := bytes.LastIndexByte(headBuf, '\n'); i >= 0 {
		head = int64(i + 1)
	}
	keep := l.limits.FileCap / 2
	tailStart := max(size-keep, head)
	tail := make([]byte, size-tailStart)
	if _, err := f.ReadAt(tail, tailStart); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if tailStart > head {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tailStart += int64(i + 1)
			tail = tail[i+1:]
		}
	}
	elided := tailStart - head
	if err := f.Truncate(head); err != nil {
		return 0, err
	}
	a, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, err
	}
	marker := fmt.Sprintf("[helmstudio: %d bytes of output elided here; this log keeps its first %d bytes and last %d bytes]\n", elided, head, keep)
	_, err = a.Write(append([]byte(marker), tail...))
	a.Close()
	if err != nil {
		return 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	// Map the tailer's position into the rewritten file. Unread bytes that
	// were elided are not replayed; the marker line reports them.
	markerEnd := head + int64(len(marker))
	switch {
	case l.offset <= head:
	case l.offset <= tailStart:
		l.offset = head
	default:
		l.offset = markerEnd + (l.offset - tailStart)
	}
	first := !l.truncated
	l.truncated = true
	l.mu.Unlock()
	if first && l.onTruncate != nil {
		l.onTruncate()
	}
	return fi.Size(), nil
}

// ingest splits bytes into lines.
func (l *logStream) ingest(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data := append(l.partial, b...)
	l.partial = nil
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if len(data) > maxLine {
				l.appendLocked(LogLine{Text: string(data[:maxLine])})
				data = data[maxLine:]
				continue
			}
			l.partial = append([]byte(nil), data...)
			return
		}
		line := data[:i]
		data = data[i+1:]
		for len(line) > maxLine {
			l.appendLocked(LogLine{Text: string(line[:maxLine])})
			line = line[maxLine:]
		}
		l.appendLocked(LogLine{Text: string(bytes.TrimSuffix(line, []byte{'\r'}))})
	}
}

func (l *logStream) flushPartial() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.partial) > 0 {
		l.appendLocked(LogLine{Text: string(l.partial)})
		l.partial = nil
	}
}

// appendLocked adds a line to the ring and offers it to every viewer. A
// viewer whose buffer is full loses the line and is told how many it lost;
// nothing here ever waits on a viewer.
func (l *logStream) appendLocked(line LogLine) {
	l.seq++
	line.Seq = l.seq
	l.ring = append(l.ring, line)
	l.ringBytes += len(line.Text)
	for len(l.ring) > l.limits.RingLines || (l.ringBytes > l.limits.RingBytes && len(l.ring) > 1) {
		l.ringBytes -= len(l.ring[0].Text)
		l.ring[0] = LogLine{}
		l.ring = l.ring[1:]
	}
	for s := range l.subs {
		select {
		case s.ch <- line:
		default:
			s.dropped.Add(1)
		}
	}
}

// subscribe returns the buffered lines after afterSeq and a subscription for
// what follows. If lines after afterSeq have already left the ring, the
// history starts with a gap line saying how many.
func (l *logStream) subscribe(afterSeq uint64) ([]LogLine, *logSub) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var history []LogLine
	if len(l.ring) > 0 && afterSeq+1 < l.ring[0].Seq && afterSeq > 0 {
		history = append(history, LogLine{Gap: int64(l.ring[0].Seq - afterSeq - 1), GapUnit: "lines"})
	}
	for _, line := range l.ring {
		if line.Seq > afterSeq {
			history = append(history, line)
		}
	}
	s := &logSub{ch: make(chan LogLine, l.limits.SubscriberBuffer)}
	l.subs[s] = struct{}{}
	return history, s
}

func (l *logStream) unsubscribe(s *logSub) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.subs, s)
}

// lastLines returns up to n of the most recent output lines.
func (l *logStream) lastLines(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for i := len(l.ring) - 1; i >= 0 && len(out) < n; i-- {
		if l.ring[i].Gap == 0 {
			out = append(out, l.ring[i].Text)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
