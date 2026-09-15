package api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

const addr = "127.0.0.1:8700"

func newServer(t *testing.T) (*Server, *supervisor.Supervisor) {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: 2 * time.Second, PortMin: 40000, PortMax: 40999,
		HostMemory: func() (uint64, error) { return 64 << 30, nil }})
	var studios []supervisor.Studio
	for _, id := range []string{"first-heavy", "second-heavy"} {
		root := t.TempDir()
		f := filepath.Join(t.TempDir(), id+".yaml")
		os.WriteFile(f, []byte(`id: `+id+`
name: `+strings.ReplaceAll(id, "-", " ")+`
kinds: [video]
local_path: `+root+`
peak_ram_gb: 20
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
processes:
  - name: studio
    heavy: true
    cmd: "while :; do echo tick; sleep 0.05; done"
`), 0o600)
		m, res, err := manifest.Load(f)
		if err != nil || !res.OK() {
			t.Fatal(err, res.Errors)
		}
		// Since M3 a launch needs an installation; record one as install
		// leaves it (docs/decisions.md, "M3 install and weights").
		if err := st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
				VALUES (?, ?, ?, 'ready', 1, 1)`, id, m.Digest, root)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		studios = append(studios, supervisor.Studio{Manifest: m, File: f})
	}
	sup.SetStudios(studios)
	srv, err := New(sup, fstest.MapFS{"index.html": {Data: []byte("shelf")}}, addr, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		sup.Shutdown(ctx)
		st.Close()
	})
	return srv, sup
}

func do(t *testing.T, h http.Handler, method, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+addr+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A launch is a state change on the user's machine. A page on another origin,
// or a request addressed to another host name (DNS rebinding), must not be
// able to make one.
func TestHostAndOriginChecks(t *testing.T) {
	srv, _ := newServer(t)
	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"cross-site page", "", "https://evil.example", http.StatusForbidden},
		{"opaque origin", "", "null", http.StatusForbidden},
		{"same host, other port", "", "http://127.0.0.1:9999", http.StatusForbidden},
		{"rebound name", "evil.example:8700", "", http.StatusMisdirectedRequest},
		{"same origin", "", "http://127.0.0.1:8700", http.StatusNotFound},
		{"no origin (curl)", "", "", http.StatusNotFound},
		{"localhost", "localhost:8700", "http://localhost:8700", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A launch of an unknown studio: 404 means the request passed the
			// checks and reached the handler.
			req := httptest.NewRequest("POST", "http://"+addr+Base+"/studios/nope:launch", nil)
			if c.host != "" {
				req.Host = c.host
			}
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
		})
	}
}

func waitRunning(t *testing.T, h http.Handler, id string) supervisor.GroupStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rec := do(t, h, "GET", Base+"/studios/"+id+"/processes", nil)
		var gs supervisor.GroupStatus
		json.Unmarshal(rec.Body.Bytes(), &gs)
		if gs.State == "running" {
			return gs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not reach running", id)
	return supervisor.GroupStatus{}
}

// The heavy refusal reaches the client as 409 with the arithmetic in the
// body; ?preempt=true stops the first and launches the second.
func TestHeavyConflictOverHTTP(t *testing.T) {
	srv, _ := newServer(t)
	if rec := do(t, srv, "POST", Base+"/studios/first-heavy:launch", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("launch: %d %s", rec.Code, rec.Body)
	}
	waitRunning(t, srv, "first-heavy")

	rec := do(t, srv, "POST", Base+"/studios/second-heavy:launch", nil)
	var body errorBody
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body.Error != "heavy_conflict" || body.Heavy == nil ||
		body.Heavy.RunningPeakGB != 20 || body.Heavy.HostGB != 64 || !strings.Contains(body.Message, "40 GB") {
		t.Fatalf("second launch: %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, srv, "POST", Base+"/studios/second-heavy:launch?preempt=true", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("preempting launch: %d %s", rec.Code, rec.Body)
	}
	waitRunning(t, srv, "second-heavy")
	rec = do(t, srv, "GET", Base+"/studios/first-heavy/processes", nil)
	var gs supervisor.GroupStatus
	json.Unmarshal(rec.Body.Bytes(), &gs)
	if gs.Processes[0].ExitReason != "preempted" {
		t.Fatalf("first studio after preemption: %s", rec.Body)
	}
}

func TestErrorsAreShaped(t *testing.T) {
	srv, _ := newServer(t)
	for path, want := range map[string]int{
		"/studios/nope:launch":        http.StatusNotFound,
		"/studios/first-heavy:stop":   http.StatusConflict,
		"/studios/first-heavy:reboot": http.StatusNotFound,
	} {
		rec := do(t, srv, "POST", Base+path, nil)
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != want || body.Error == "" || body.Message == "" {
			t.Errorf("POST %s: %d %s; want %d with a kind and a message", path, rec.Code, rec.Body, want)
		}
	}
}

// The log stream replays buffered output and then follows live lines.
func TestLogStreamFollowsOutput(t *testing.T) {
	srv, _ := newServer(t)
	hs := httptest.NewUnstartedServer(srv)
	hs.Start()
	defer hs.Close()
	if rec := do(t, srv, "POST", Base+"/studios/first-heavy:launch", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("launch: %d %s", rec.Code, rec.Body)
	}
	waitRunning(t, srv, "first-heavy")

	req, _ := http.NewRequest("GET", hs.URL+Base+"/studios/first-heavy/processes/studio/logs", nil)
	req.Host = addr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	ticks := 0
	for sc.Scan() && ticks < 20 {
		if strings.HasPrefix(sc.Text(), "data: ") && strings.Contains(sc.Text(), `"text":"tick"`) {
			ticks++
		}
	}
	if ticks < 20 {
		t.Fatalf("saw %d tick lines, want a live stream: %v", ticks, sc.Err())
	}
}
