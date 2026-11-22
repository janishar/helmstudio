package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const tlID = "c3a1f2e4-5b6d-4e7f-8a9b-0c1d2e3f4a5b"

// fakeTimelines serves the timeline routes from the contract fixtures; an export runs for `polls` reads.
type fakeTimelines struct {
	mu        sync.Mutex
	got       map[string]map[string]any // "METHOD path" → its JSON body
	client    string                    // the last X-Helm-Client
	query     string                    // the last query
	polls     int
	cancelled bool
}

func timelineServer(t *testing.T, polls int) (*fakeTimelines, *httptest.Server) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	pollEvery = 5 * time.Millisecond
	f := &fakeTimelines{got: map[string]map[string]any{}, polls: polls}
	tl, list, plan := fixture(t, "Timeline.valid.json"), fixture(t, "TimelineList.valid.json"), fixture(t, "ExportPlan.valid.2.json")
	var job map[string]any
	if err := json.Unmarshal(fixture(t, "MediaJob.valid.json"), &job); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		key := r.Method + " " + r.URL.Path
		f.got[key], f.client, f.query = body, r.Header.Get("X-Helm-Client"), r.URL.RawQuery
		base := "/v1/studio/timelines/" + tlID
		jobPath := "/v1/studio/media-jobs/" + job["id"].(string)
		send := func(status int, v any) {
			w.WriteHeader(status)
			if b, ok := v.([]byte); ok {
				_, _ = w.Write(b)
				return
			}
			_ = json.NewEncoder(w).Encode(v)
		}
		switch key {
		case "GET /v1/studio/timelines":
			send(200, list)
		case "GET " + base:
			if r.URL.Query().Get("view") == "agent" {
				send(200, fixture(t, "TimelineAgentView.valid.json"))
				break
			}
			send(200, tl)
		case "POST " + base + "/clips", "POST " + base + "/restore":
			send(200, tl)
		case "POST " + base + "/edits": // API 1.49
			send(200, fixture(t, "TimelineEditResult.valid.json"))
		case "GET " + base + "/revisions":
			send(200, fixture(t, "TimelineRevisionList.valid.json"))
		case "POST " + base + "/export/plan":
			send(200, plan)
		case "POST " + base + "/export":
			send(202, job)
		case "GET " + jobPath:
			if f.polls--; f.polls <= 0 {
				if f.cancelled {
					job["status"], job["failure"] = "cancelled", nil
				} else {
					job["status"], job["take_id"] = "succeeded", "44444444-0000-4000-8000-000000000001"
				}
			}
			send(200, job)
		case "POST " + jobPath + "/cancel":
			f.cancelled, f.polls = true, 1
			send(200, job)
		case "GET /v1/studio/takes/44444444-0000-4000-8000-000000000001":
			send(200, map[string]any{"url": "/files/u/o1/exports/j1/export.mp4?exp=1&sig=ok"})
		case "GET /files/u/o1/exports/j1/export.mp4":
			_, _ = w.Write(mp4)
		default:
			send(404, map[string]string{"detail": "not found"})
		}
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return f, srv
}

func TestTimelinesAndShow(t *testing.T) {
	timelineServer(t, 1)
	out := helmOK(t, "timelines")
	if norm := strings.Join(strings.Fields(out), " "); norm != tlID+" r4 4 clips 8.54s 256x128 24fps 48000Hz 2026-11-16T10:00:00Z Fox at dusk" {
		t.Fatalf("timelines: %q", out)
	}
	out = helmOK(t, "timeline", "show", tlID)
	for _, want := range []string{
		"Fox at dusk  revision 4  256x128 24fps 48000Hz  8.54s (205 frames)",
		"V1\n",
		"a         0.00s  video aaaaaaaa-0000-4000-8000-000000000001 0.00-2.04s a fox trots in",
		"b         2.04s  image bbbbbbbb-0000-4000-8000-000000000001 hold 2.00s",
		"c         4.04s  video aaaaaaaa-0000-4000-8000-000000000002 0.25-1.75s a fox trots in  [2 alternatives]",
		"s         5.54s  empty slot 3.00s  [shot running]",
		"A2\n  m         1.00s  video aaaaaaaa-0000-4000-8000-000000000001 0.00-1.00s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
}

func TestTimelineAdd(t *testing.T) {
	f, _ := timelineServer(t, 1)
	helmOK(t, "timeline", "add", tlID, "t1", "--track", "A2", "t2")
	got := f.got["POST /v1/studio/timelines/"+tlID+"/clips"]
	if b, _ := json.Marshal(got); string(b) != `{"take_ids":["t1","t2"],"track":"A2"}` {
		t.Fatalf("body %s", b)
	}
	helmFails(t, "usage: helm timeline", "timeline", "add", tlID)
	helmFails(t, "usage: helm timeline", "timeline", "show")
}

func TestTimelineExportPlanBlocked(t *testing.T) {
	timelineServer(t, 1)
	var out, errb bytes.Buffer
	if code := helm([]string{"timeline", "export", tlID, "--plan"}, nil, &out, &errb, nil); code != 1 {
		t.Fatalf("plan: %d %s", code, errb.String())
	}
	want := "revision 4: re-encode, 98 frames 4.08s, about 608417 bytes, on no host\n  blocked no_host: no live host can export (ffmpeg)\n"
	if out.String() != want {
		t.Fatalf("plan:\n%q\nwant\n%q", out.String(), want)
	}
}

func TestTimelineExportSaves(t *testing.T) {
	f, _ := timelineServer(t, 3)
	dest := filepath.Join(t.TempDir(), "fox.mp4")
	out := helmOK(t, "timeline", "export", tlID, "-o", dest, "--host", "mac")
	if b, err := os.ReadFile(dest); err != nil || !bytes.Equal(b, mp4) {
		t.Fatalf("file: %v", err)
	}
	if b, _ := json.Marshal(f.got["POST /v1/studio/timelines/"+tlID+"/export"]); string(b) != `{"host":"mac","revision":4}` {
		t.Fatalf("export body %s", b)
	}
	want := "export 9e4d2c1b-3a5f-4e6d-8c7b-0a1b2c3d4e5f queued on mac.mlx\nrunning 40/133 frames\nsucceeded 40/133 frames\ntake 44444444-0000-4000-8000-000000000001\n" + dest + "\n"
	if out != want {
		t.Fatalf("out:\n%q\nwant\n%q", out, want)
	}
}

func TestTimelineExportCancelOnInterrupt(t *testing.T) {
	f, _ := timelineServer(t, 1000)
	stop := make(chan os.Signal, 1)
	stop <- os.Interrupt
	var out, errb bytes.Buffer
	if code := helm([]string{"timeline", "export", tlID}, nil, &out, &errb, stop); code != 1 ||
		!strings.Contains(errb.String(), "export cancelled") || !strings.Contains(out.String(), "cancelling\n") {
		t.Fatalf("cancel: %d %q %q", code, out.String(), errb.String())
	}
	if !f.cancelled {
		t.Fatal("no cancel sent")
	}
}

func TestTimelineEditByOps(t *testing.T) {
	f, _ := timelineServer(t, 1)
	ops := filepath.Join(t.TempDir(), "ops.json")
	if err := os.WriteFile(ops, []byte(`[{"op":"split","track":"V1","at":1.01},{"op":"remove","clip":"b"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := helmOK(t, "timeline", "edit", tlID, "-f", ops, "--revision", "4", "--dry-run")
	body, _ := json.Marshal(f.got["POST /v1/studio/timelines/"+tlID+"/edits"])
	if string(body) != `{"base_revision":4,"dry_run":true,"ops":[{"at":1.01,"op":"split","track":"V1"},{"clip":"b","op":"remove"}]}` ||
		f.client != "helm" {
		t.Fatalf("body %s client %q", body, f.client)
	}
	for _, want := range []string{"revision 5 (dry run: nothing stored)\n", "  added: k9\n", "  removed: b\n", "  changed: c (at)\n",
		"  duration 8.54s -> 6.54s\n", "  warning: ops[0]: at 1.01 snapped to 1\n", "Fox at dusk  revision 5"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit lacks %q:\n%s", want, out)
		}
	}
	// {base_revision, ops} in the file; without a revision it is refused before any call
	if err := os.WriteFile(ops, []byte(`{"base_revision":4,"ops":[{"op":"remove","clip":"b"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	helmOK(t, "timeline", "edit", tlID, "-f", ops)
	if b := f.got["POST /v1/studio/timelines/"+tlID+"/edits"]; b["base_revision"] != float64(4) || b["dry_run"] != false {
		t.Fatal(b)
	}
	if err := os.WriteFile(ops, []byte(`[{"op":"remove","clip":"b"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	helmFails(t, "--revision n", "timeline", "edit", tlID, "-f", ops)
	helmFails(t, "-f ops.json", "timeline", "edit", tlID)
}

func TestTimelineRevisionsRestoreAgent(t *testing.T) {
	f, _ := timelineServer(t, 1)
	out := helmOK(t, "timeline", "revisions", tlID, "--before", "6", "--limit", "3")
	if f.query != "before=6&limit=3" || !strings.Contains(out, "r5    user mcp    2026-11-23T10:00:00Z   4 clips    6.54s  split, remove\n") ||
		!strings.Contains(out, "r4    studio      ") {
		t.Fatalf("revisions %q:\n%s", f.query, out)
	}
	helmOK(t, "timeline", "restore", tlID, "--to", "3", "--revision", "5")
	if b, _ := json.Marshal(f.got["POST /v1/studio/timelines/"+tlID+"/restore"]); string(b) != `{"revision":5,"to":3}` {
		t.Fatalf("restore %s", b)
	}
	helmFails(t, "--to and --revision", "timeline", "restore", tlID, "--to", "3")
	out = helmOK(t, "timeline", "show", tlID, "--agent")
	if f.query != "view=agent" || !strings.Contains(out, `"at_f": 0`) || !strings.Contains(out, `"take_title": "a fox trots in"`) {
		t.Fatalf("agent %q:\n%s", f.query, out)
	}
}
