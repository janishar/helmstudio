package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// gatedServer is the daemon with the approval gate on, and one studio.
func gatedServer(t *testing.T, m *manifest.Manifest) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st})
	sup.SetStudios([]supervisor.Studio{{Manifest: m}})
	srv, err := New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf, WithApproval(st))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv
	ts.Start()
	t.Cleanup(func() { ts.Close(); cancel(); st.Close() })
	return ts
}

const gatedManifest = `id: wan-studio
name: wan studio
kinds: [video]
repo: https://github.com/someone/wan-studio
ref: v0.3.1
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [metal]
processes:
  - name: studio
    role: main
    cmd: "./dist/wan --port {port}"
    port: { prefer: 8790 }
    health: { tcp: true, timeout_s: 60 }
`

func loadGated(t *testing.T, text string) *manifest.Manifest {
	t.Helper()
	m, res, err := manifest.LoadBytes("gated.yaml", []byte(text))
	if err != nil || !res.OK() {
		t.Fatalf("fixture: %v %v", res.Errors, err)
	}
	return m
}

func digestOf(t *testing.T, ts *httptest.Server, id string) string {
	t.Helper()
	res, body := request(t, "GET", ts.URL+Base+"/studios/"+id+"/approval", "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("approval: %d %s", res.StatusCode, body)
	}
	var p struct {
		Digest          string `json:"digest"`
		AlreadyApproved bool   `json:"already_approved"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	return p.Digest
}

// Launch is gated, not only install: cmd and health.exec run at launch and
// never pass through install at all.
func TestLaunchWithoutAnApprovalIsRefused(t *testing.T) {
	ts := gatedServer(t, loadGated(t, gatedManifest))

	res, body := request(t, "POST", ts.URL+Base+"/studios/wan-studio:launch", "", nil)
	if res.StatusCode != 409 || !strings.Contains(body, "approval_required") {
		t.Fatalf("got %d %s; want 409 approval_required", res.StatusCode, body)
	}
	// The refusal carries the preview, so the caller has something to show.
	var e struct {
		Details struct {
			Approval struct {
				Digest   string `json:"digest"`
				Commands []struct {
					Command string `json:"command"`
				} `json:"commands"`
			} `json:"approval"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatal(err)
	}
	if e.Details.Approval.Digest == "" {
		t.Error("a refusal with no preview leaves the caller nothing to show")
	}
	if len(e.Details.Approval.Commands) == 0 {
		t.Error("the preview in the refusal has no commands")
	}
}

// Reading the preview approves nothing. If it did, fetching the screen would
// be equivalent to consenting to it — and every gate here would be decorative.
func TestFetchingThePreviewDoesNotApproveAnything(t *testing.T) {
	ts := gatedServer(t, loadGated(t, gatedManifest))

	_ = digestOf(t, ts, "wan-studio")

	res, body := request(t, "POST", ts.URL+Base+"/studios/wan-studio:launch", "", nil)
	if res.StatusCode != 409 || !strings.Contains(body, "approval_required") {
		t.Fatalf("after merely reading the preview, launch got %d %s; want 409 approval_required", res.StatusCode, body)
	}
}

// A digest that does not describe what would run now is refused, and says so
// differently — the caller needs to know the picture moved, not that they
// forgot something.
func TestAStaleDigestIsPreviewChanged(t *testing.T) {
	ts := gatedServer(t, loadGated(t, gatedManifest))
	res, body := request(t, "POST", ts.URL+Base+"/studios/wan-studio:launch?approval=not-the-right-digest", "", nil)
	if res.StatusCode != 409 || !strings.Contains(body, "preview_changed") {
		t.Fatalf("got %d %s; want 409 preview_changed", res.StatusCode, body)
	}
}

// The matching digest is recorded, so the studio stops asking until something
// it covers changes.
func TestAMatchingDigestIsRecordedAndStopsTheAsking(t *testing.T) {
	m := loadGated(t, gatedManifest)
	ts := gatedServer(t, m)
	digest := digestOf(t, ts, "wan-studio")

	// The launch itself fails for its own reasons in this test daemon — no
	// installation, nothing to spawn. What matters is that it got past the
	// gate, which an approval_required would mean it had not.
	_, body := request(t, "POST", ts.URL+Base+"/studios/wan-studio:launch?approval="+digest, "", nil)
	if strings.Contains(body, "approval_required") || strings.Contains(body, "preview_changed") {
		t.Fatalf("a matching digest was refused: %s", body)
	}

	res, body := request(t, "GET", ts.URL+Base+"/studios/wan-studio/approval", "", nil)
	if res.StatusCode != 200 {
		t.Fatal(body)
	}
	var p struct {
		AlreadyApproved bool `json:"already_approved"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	if !p.AlreadyApproved {
		t.Error("after approving, the preview should say so rather than asking again")
	}
}
