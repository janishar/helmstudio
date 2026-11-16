package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	pngenc "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The relevant tables of the old helm.db, as the real database declares them.
const oldSchema = `
CREATE TABLE assets (id TEXT PRIMARY KEY, sha256 TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, mime TEXT NOT NULL,
 bytes INTEGER NOT NULL, width INTEGER, height INTEGER, duration_s REAL, fps REAL, blob_path TEXT NOT NULL,
 library_path TEXT, origin_studio TEXT, pinned INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'ready',
 created_at INTEGER, last_used_at INTEGER, first_referenced_at INTEGER);
CREATE TABLE items (id TEXT PRIMARY KEY, studio_id TEXT NOT NULL, session_id TEXT, kind TEXT NOT NULL,
 asset_id TEXT NOT NULL REFERENCES assets(id), timeline_id TEXT, title TEXT, params TEXT NOT NULL DEFAULT '{}',
 starred INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, deleted_at INTEGER);
CREATE TABLE item_inputs (item_id TEXT NOT NULL, asset_id TEXT NOT NULL, role TEXT NOT NULL,
 PRIMARY KEY (item_id, asset_id, role)) WITHOUT ROWID;
CREATE TABLE derived (asset_id TEXT NOT NULL, variant TEXT NOT NULL, path TEXT NOT NULL, bytes INTEGER NOT NULL,
 PRIMARY KEY (asset_id, variant)) WITHOUT ROWID;
CREATE TABLE tags (item_id TEXT NOT NULL, tag TEXT NOT NULL, PRIMARY KEY (item_id, tag)) WITHOUT ROWID;
`

func pngBytes(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := pngenc.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// oldGallery writes a small helmstudio folder: two images (the second made from the first, a star), a video with a
// derived poster, a gif (unsupported), a deleted image and an audio item.
func oldGallery(t *testing.T) string {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not on PATH")
	}
	root := t.TempDir()
	files := map[string][]byte{
		"assets/blobs/aa/a1.png":      pngBytes(t, 4, 3),
		"assets/blobs/bb/b2.png":      pngBytes(t, 6, 5),
		"assets/blobs/cc/c3.mp4":      []byte("\x00\x00\x00\x18ftypisom fake mp4"),
		"assets/blobs/dd/d4.gif":      []byte("GIF89a"),
		"assets/blobs/ee/e5.png":      pngBytes(t, 2, 2),
		"assets/blobs/ff/f6.wav":      []byte("RIFF"),
		"cache/derived/c3/poster.png": pngBytes(t, 8, 6),
	}
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sql := oldSchema + `
INSERT INTO assets (id, sha256, kind, mime, bytes, width, height, blob_path, library_path) VALUES
 ('A1','a1','image','image/png',1,4,3,'blobs/aa/a1.png','qwen-image-studio/2026-09/fox.png'),
 ('A2','b2','image','image/png',1,6,5,'blobs/bb/b2.png',NULL),
 ('A3','c3','video','video/mp4',1,8,6,'blobs/cc/c3.mp4','ltx-studio/2026-09/harbour.mp4'),
 ('A4','d4','image','image/gif',1,1,1,'blobs/dd/d4.gif',NULL),
 ('A5','e5','image','image/png',1,2,2,'blobs/ee/e5.png',NULL),
 ('A6','f6','audio','audio/wav',1,NULL,NULL,'blobs/ff/f6.wav',NULL),
 ('UP','up','image','image/png',1,2,2,'blobs/up.png',NULL);
INSERT INTO items (id, studio_id, kind, asset_id, title, params, starred, created_at, deleted_at) VALUES
 ('I1','qwen-image-studio','image','A1',NULL,'{"prompt":"a red fox\nin snow","seed":7}',1,1790169404453,NULL),
 ('I2','qwen-image-studio','image','A2','Fox, edited','{"edit":true}',0,1790169500000,NULL),
 ('I3','ltx-studio','video','A3',NULL,'{"params":{"prompt":"harbour, slow push in"}}',0,1790169600000,NULL),
 ('I4','iris-studio','image','A4','gif','{}',0,1790169700000,NULL),
 ('I5','iris-studio','image','A5','gone','{}',0,1790169800000,1790169900000),
 ('I6','auk-studio','audio','A6','song','{}',0,1790169850000,NULL);
INSERT INTO item_inputs VALUES ('I2','A1','image'),('I2','UP','image'),('I3','A2','input');
INSERT INTO derived VALUES ('A3','poster','c3/poster.png',1),('A1','thumb-320','a1/thumb-320.jpg',1);
INSERT INTO tags VALUES ('I1','fox');
`
	cmd := exec.Command("sqlite3", filepath.Join(root, "helm.db"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	return root
}

// snapshot lists every file under root with its size and contents' length, to prove nothing was written.
func snapshot(t *testing.T, root string) string {
	var lines []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			lines = append(lines, fmt.Sprintf("%s %d %s", p, fi.Size(), fi.ModTime()))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// importAPI is the admin import endpoints: takes by key (pending, then ready once complete), signed PUTs.
type importAPI struct {
	mu      sync.Mutex
	calls   int
	takes   map[string]map[string]any // key → the create body
	ready   map[string]bool
	ids     map[string]string // take id → key
	puts    map[string]int    // object → bytes
	parents map[string][]any
}

func (a *importAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	switch route := r.Method + " " + r.URL.Path; {
	case route == "GET /auth/admin/users":
		fmt.Fprint(w, `{"users":[{"id":"7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13","email":"Ann@example.com","roles":["user"]}]}`)
	case route == "POST /v1/studio/admin/imports/takes":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		key := b["key"].(string)
		a.parents[key] = b["parents"].([]any)
		if a.ready[key] {
			fmt.Fprintf(w, `{"take_id":"id-%s","key":%q,"state":"ready","upload_url":null,"poster_upload_url":null,"expires_at":null}`, key, key)
			return
		}
		status := http.StatusCreated
		if a.takes[key] != nil {
			status = http.StatusOK
		}
		a.takes[key], a.ids["id-"+key] = b, key
		poster := "null"
		if b["kind"] == "video" {
			poster = fmt.Sprintf("%q", "/files/u/o/imports/id-"+key+".poster.png?sig=ok")
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"take_id":"id-%s","key":%q,"state":"pending","upload_url":%q,"poster_upload_url":%s,"expires_at":"2026-11-17T00:00:00Z"}`,
			key, key, "/files/u/o/imports/id-"+key+".bin?sig=ok", poster)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/files/"):
		b, _ := io.ReadAll(r.Body)
		a.puts[r.URL.Path] = len(b)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/complete"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/studio/admin/imports/takes/"), "/complete")
		a.ready[a.ids[id]] = true
		fmt.Fprint(w, `{"id":"x"}`)
	default:
		http.Error(w, `{"detail":"not found"}`, 404)
	}
}

func TestImport(t *testing.T) {
	root := oldGallery(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	api := &importAPI{takes: map[string]map[string]any{}, ready: map[string]bool{}, ids: map[string]string{}, puts: map[string]int{}, parents: map[string][]any{}}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	defer func(p func(string) (videoInfo, error)) { probeVideo = p }(probeVideo)
	probeVideo = func(string) (videoInfo, error) {
		return videoInfo{Width: 8, Height: 6, DurationS: 2, FPS: 24, Frames: 48, HasAudio: true}, nil
	}
	before := snapshot(t, root)

	out := helmOK(t, "import", root, "--user", "ann@example.com", "--dry-run")
	want := "user ann@example.com\nwould import 3 (2 images, 1 videos), 0.0 GB; 2 lineage edges, 1 starred; skipped 3 (audio 1, text 0, deleted 1, unsupported 1)\n"
	if out != want || api.calls != 0 {
		t.Fatalf("dry run (%d calls):\n%q\nwant\n%q", api.calls, out, want)
	}

	// A first run stopped after one item, then the rest: the first is already there.
	out = helmOK(t, "import", root, "--user", "ann@example.com", "--limit", "1")
	if !strings.HasPrefix(out, "imported 1 new (1 images, 0 videos), 0 already imported, 0 failed; 0 lineage edges, 1 starred") {
		t.Fatalf("first run: %s", out)
	}
	out = helmOK(t, "import", root, "--user", "ann@example.com")
	if !strings.HasPrefix(out, "imported 2 new (1 images, 1 videos), 1 already imported, 0 failed; 2 lineage edges, 0 starred, 0.0 GB uploaded; skipped 3 (audio 1, text 0, deleted 1, unsupported 1)") {
		t.Fatalf("second run: %s", out)
	}
	out = helmOK(t, "import", root, "--user", "ann@example.com")
	if !strings.HasPrefix(out, "imported 0 new (0 images, 0 videos), 3 already imported, 0 failed; 0 lineage edges") {
		t.Fatalf("third run: %s", out)
	}

	i1 := api.takes["helmstudio:I1"]
	if i1["title"] != "a red fox" || i1["starred"] != true || i1["width"] != 4.0 || i1["height"] != 3.0 ||
		i1["created_at"] != "2026-09-23T13:16:44.453Z" || i1["filename"] != "fox.png" || i1["content_type"] != "image/png" {
		t.Errorf("I1 body: %v", i1)
	}
	legacy, _ := json.Marshal(i1["legacy"])
	if string(legacy) != `{"old_id":"I1","params":{"prompt":"a red fox\nin snow","seed":7},"studio":"qwen-image-studio","tags":["fox"]}` {
		t.Errorf("I1 legacy: %s", legacy)
	}
	if p := fmt.Sprint(api.parents["helmstudio:I2"]); p != "[helmstudio:I1]" {
		t.Errorf("I2 parents: %s", p)
	}
	i3 := api.takes["helmstudio:I3"]
	video, _ := json.Marshal(i3["video"])
	if i3["title"] != "harbour, slow push in" || fmt.Sprint(api.parents["helmstudio:I3"]) != "[helmstudio:I2]" ||
		!strings.Contains(string(video), `"poster":{"bytes":`) || !strings.Contains(string(video), `"content_type":"image/png","height":6,"width":8}`) {
		t.Errorf("I3 body: %v %s", i3, video)
	}
	if api.puts["/files/u/o/imports/id-helmstudio:I3.poster.png"] == 0 || len(api.puts) != 4 {
		t.Errorf("puts: %v", api.puts)
	}
	if snapshot(t, root) != before {
		t.Error("the old folder changed")
	}

	if code := helm([]string{"import", root, "--user", "nobody@example.com"}, nil, io.Discard, io.Discard, nil); code != 2 {
		t.Errorf("unknown user: exit %d, want 2", code)
	}
}
