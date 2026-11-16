package main

// helm import: an old helmstudio gallery (~/.helmstudio) into one user's gallery (contracts/README.md, Import).
//
// The old layout, read from the real helm.db (schema of internal/store in the reference, migrations 0001-0007):
//   - items(id ULID, studio_id, kind image|video|audio|text, asset_id → assets, title, params JSON text, starred 0|1,
//     created_at unix ms, deleted_at unix ms|null): one gallery item → one take (source import), key
//     "helmstudio:<id>"; deleted items, audio and text are skipped and counted.
//   - assets(id, sha256, kind, mime, bytes, width, height, duration_s, fps, blob_path relative to <root>/assets,
//     library_path the hardlink under <root>/library): the take's bytes are <root>/assets/<blob_path>.
//   - derived(asset_id, variant thumb-320|poster|…, path relative to <root>/cache/derived): a video's poster is its
//     "poster" row, else frame 0 by ffmpeg into a temp dir.
//   - item_inputs(item_id, asset_id, role input|image|…): lineage; a parent is the earliest imported item whose asset
//     is that input (inputs that are bare uploads, not items, are dropped).
//   - tags(item_id, tag): kept in the legacy snapshot.
//   - legacy = {studio, old_id, params (as stored), tags}; title = title, else the prompt's first line (params.prompt
//     or params.params.prompt, 80 characters), else the file stem.
//
// helm.db is read with the system sqlite3 shell (no Go SQLite driver is pinned in docs/plan/14-dependencies.md;
// the shell is on every Mac) through the URI file:<abs>?mode=ro&immutable=1, which never creates -wal or -shm files.
// Nothing under <path> is ever written.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig of jpeg files and posters
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxImageBytes, maxVideoBytes = 50 << 20, 2 << 30

type oldItem struct {
	ID          string `json:"id"`
	Studio      string `json:"studio_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Params      string `json:"params"`
	Starred     int    `json:"starred"`
	CreatedAt   int64  `json:"created_at"`
	Deleted     int    `json:"deleted"`
	AssetID     string `json:"asset_id"`
	Mime        string `json:"mime"`
	Bytes       int64  `json:"bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	BlobPath    string `json:"blob_path"`
	LibraryPath string `json:"library_path"`
	Poster      string `json:"poster"`
	Tags        string `json:"tags"`
}

type oldInput struct {
	ItemID  string `json:"item_id"`
	AssetID string `json:"asset_id"`
}

const itemsQuery = `SELECT i.id, i.studio_id, i.kind, coalesce(i.title,'') AS title, i.params, i.starred, i.created_at,
 (i.deleted_at IS NOT NULL) AS deleted, i.asset_id, a.mime, a.bytes, coalesce(a.width,0) AS width,
 coalesce(a.height,0) AS height, a.blob_path, coalesce(a.library_path,'') AS library_path,
 coalesce((SELECT path FROM derived d WHERE d.asset_id=a.id AND d.variant='poster'),'') AS poster,
 (SELECT json_group_array(tag) FROM tags t WHERE t.item_id=i.id) AS tags
 FROM items i JOIN assets a ON a.id=i.asset_id ORDER BY i.created_at, i.id`

const inputsQuery = `SELECT item_id, asset_id FROM item_inputs ORDER BY item_id, role, asset_id`

// sqliteQuery runs one query on the database read-only and decodes its rows (sqlite3 -json) into out.
func sqliteQuery(db, query string, out any) error {
	abs, err := filepath.Abs(db)
	if err != nil {
		return err
	}
	uri := "file:" + (&url.URL{Path: abs}).EscapedPath() + "?mode=ro&immutable=1"
	cmd := exec.Command("sqlite3", "-readonly", "-batch", "-bail", "-init", os.DevNull, "-json", uri, query)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sqlite3 %s: %v %s", db, err, strings.TrimSpace(stderr.String()))
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("[]")
	}
	return json.Unmarshal(b, out)
}

// skipReason is why an item is not imported ("" = it is).
func (it oldItem) skipReason() string {
	switch {
	case it.Deleted != 0:
		return "deleted"
	case it.Kind == "audio", it.Kind == "text":
		return it.Kind
	case it.Kind == "image" && (it.Mime == "image/png" || it.Mime == "image/jpeg" || it.Mime == "image/webp") && it.Bytes <= maxImageBytes:
		return ""
	case it.Kind == "video" && it.Mime == "video/mp4" && it.Bytes <= maxVideoBytes:
		return ""
	}
	return "unsupported"
}

func (it oldItem) key() string { return "helmstudio:" + it.ID }

// title is the item's title, else its prompt's first line, else its file's stem (at most 80 characters).
func (it oldItem) title() string {
	t := strings.TrimSpace(it.Title)
	if t == "" {
		var p struct {
			Prompt any `json:"prompt"`
			Params struct {
				Prompt any `json:"prompt"`
			} `json:"params"`
		}
		_ = json.Unmarshal([]byte(it.Params), &p)
		for _, v := range []any{p.Prompt, p.Params.Prompt} {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				t, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
				t = strings.TrimSpace(t)
				break
			}
		}
	}
	if t == "" {
		t = strings.TrimSuffix(it.filename(), filepath.Ext(it.filename()))
	}
	if utf8.RuneCountInString(t) > 80 {
		t = string([]rune(t)[:80])
	}
	return t
}

func (it oldItem) filename() string {
	if it.LibraryPath != "" {
		return filepath.Base(it.LibraryPath)
	}
	return filepath.Base(it.BlobPath)
}

func (it oldItem) legacy() (map[string]any, error) {
	l := map[string]any{"studio": it.Studio, "old_id": it.ID, "params": it.Params}
	if json.Valid([]byte(it.Params)) {
		l["params"] = json.RawMessage(it.Params)
	}
	var tags []string
	if json.Unmarshal([]byte(it.Tags), &tags) == nil && len(tags) > 0 {
		l["tags"] = tags
	}
	if b, err := json.Marshal(l); err != nil || len(b) > 65536 {
		return nil, errors.New("legacy is larger than 64 KB")
	}
	return l, nil
}

type importStats struct {
	imported, images, videos, already, edges, starred int
	bytes                                             int64
	skipped                                           map[string]int
	failed                                            []string
}

func (s importStats) skipText() string {
	k := 0
	for _, n := range s.skipped {
		k += n
	}
	t := fmt.Sprintf("skipped %d (audio %d, text %d, deleted %d", k, s.skipped["audio"], s.skipped["text"], s.skipped["deleted"])
	if n := s.skipped["unsupported"]; n > 0 {
		t += fmt.Sprintf(", unsupported %d", n)
	}
	return t + ")"
}

func importCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("user", "", "the email of the user whose gallery receives the items")
	dry := fs.Bool("dry-run", false, "only read and count: no network call, nothing written")
	limit := fs.Int("limit", 0, "import at most n items (0 = all)")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: helm import <path> --user <email> [--dry-run] [--limit n]")
	}
	root := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" || fs.NArg() > 0 {
		return &exitErr{2, errors.New("usage: helm import <path> --user <email> [--dry-run] [--limit n]")}
	}
	var items []oldItem
	var inputs []oldInput
	db := filepath.Join(root, "helm.db")
	if _, err := os.Stat(db); err != nil {
		return err
	}
	if err := sqliteQuery(db, itemsQuery, &items); err != nil {
		return err
	}
	if err := sqliteQuery(db, inputsQuery, &inputs); err != nil {
		return err
	}

	st := importStats{skipped: map[string]int{}}
	var todo []oldItem
	byAsset := map[string]string{} // asset id → the earliest imported item's id
	for _, it := range items {
		if r := it.skipReason(); r != "" {
			st.skipped[r]++
			continue
		}
		todo = append(todo, it)
		if _, ok := byAsset[it.AssetID]; !ok {
			byAsset[it.AssetID] = it.ID
		}
	}
	parents := map[string][]string{}
	for _, in := range inputs {
		p, ok := byAsset[in.AssetID]
		if ok && p != in.ItemID && len(parents[in.ItemID]) < 64 && !contains(parents[in.ItemID], "helmstudio:"+p) {
			parents[in.ItemID] = append(parents[in.ItemID], "helmstudio:"+p)
		}
	}
	if *limit > 0 && len(todo) > *limit {
		todo = todo[:*limit]
	}

	if *dry {
		for _, it := range todo {
			fi, err := os.Stat(filepath.Join(root, "assets", it.BlobPath))
			if err != nil {
				st.failed = append(st.failed, it.ID+": file missing")
				continue
			}
			st.imported++
			if it.Kind == "video" {
				st.videos++
			} else {
				st.images++
			}
			st.bytes += fi.Size()
			st.edges += len(parents[it.ID])
			st.starred += it.Starred
		}
		fmt.Fprintf(stdout, "user %s\nwould import %d (%d images, %d videos), %.1f GB; %d lineage edges, %d starred; %s\n",
			*email, st.imported, st.images, st.videos, float64(st.bytes)/1e9, st.edges, st.starred, st.skipText())
		return printFailed(st.failed, stdout)
	}

	c, err := loggedIn()
	if err != nil {
		return err
	}
	owner, err := c.userID(*email)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "helm-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, it := range todo {
		ready, sent, err := c.importItem(root, tmp, owner, it, parents[it.ID])
		switch {
		case err != nil:
			st.failed = append(st.failed, it.ID+": "+err.Error())
		case ready:
			st.already++
		default:
			st.imported++
			if it.Kind == "video" {
				st.videos++
			} else {
				st.images++
			}
			st.bytes += sent
			st.edges += len(parents[it.ID])
			st.starred += it.Starred
		}
	}
	fmt.Fprintf(stdout, "imported %d new (%d images, %d videos), %d already imported, %d failed; %d lineage edges, %d starred, %.1f GB uploaded; %s\n",
		st.imported, st.images, st.videos, st.already, len(st.failed), st.edges, st.starred, float64(st.bytes)/1e9, st.skipText())
	return printFailed(st.failed, stdout)
}

func printFailed(failed []string, stdout io.Writer) error {
	for i, f := range failed {
		if i == 20 {
			break
		}
		fmt.Fprintln(stdout, f)
	}
	if len(failed) > 0 {
		return &exitErr{1, nil}
	}
	return nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// userID resolves an email to its user id with the admin user list; an unknown email is a usage error (exit 2).
func (c *client) userID(email string) (string, error) {
	var list struct {
		Users []user `json:"users"`
	}
	if err := c.call(http.MethodGet, "/auth/admin/users", nil, http.StatusOK, &list); err != nil {
		return "", err
	}
	for _, u := range list.Users {
		if strings.EqualFold(u.Email, email) {
			return u.ID, nil
		}
	}
	return "", &exitErr{2, fmt.Errorf("no user with email %s", email)}
}

type fileRef struct {
	path, ct      string
	size          int64
	width, height int
}

// importItem creates (or finds) the item's take, uploads its file (and a video's poster) and completes it. ready: it
// was already imported (only missing parent edges were added); sent: the bytes uploaded.
func (c *client) importItem(root, tmp, owner string, it oldItem, parents []string) (ready bool, sent int64, err error) {
	f := fileRef{path: filepath.Join(root, "assets", it.BlobPath), ct: it.Mime, width: it.Width, height: it.Height}
	fi, err := os.Stat(f.path)
	if err != nil {
		return false, 0, errors.New("file missing")
	}
	f.size = fi.Size()
	legacy, err := it.legacy()
	if err != nil {
		return false, 0, err
	}
	body := map[string]any{
		"owner_id": owner, "key": it.key(), "kind": it.Kind, "filename": it.filename(), "content_type": f.ct,
		"bytes": f.size, "created_at": time.UnixMilli(it.CreatedAt).UTC().Format("2006-01-02T15:04:05.000Z"),
		"starred": it.Starred != 0, "title": it.title(), "legacy": legacy, "parents": nonNil(parents),
	}
	var poster fileRef
	if it.Kind == "video" {
		v, err := probeVideo(f.path)
		if err != nil {
			return false, 0, err
		}
		f.width, f.height = v.Width, v.Height
		if poster, err = posterFor(root, tmp, it, f.path); err != nil {
			return false, 0, err
		}
		body["video"] = map[string]any{"duration_s": v.DurationS, "fps": v.FPS, "frames": v.Frames, "has_audio": v.HasAudio,
			"poster": map[string]any{"content_type": poster.ct, "bytes": poster.size, "width": poster.width, "height": poster.height}}
	} else {
		f.readSize()
	}
	body["width"], body["height"] = f.width, f.height

	var t struct {
		TakeID          string  `json:"take_id"`
		State           string  `json:"state"`
		UploadURL       *string `json:"upload_url"`
		PosterUploadURL *string `json:"poster_upload_url"`
	}
	resp, err := c.do(context.Background(), http.MethodPost, "/v1/studio/admin/imports/takes", body, nil)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return false, 0, readError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return false, 0, err
	}
	if t.State == "ready" {
		return true, 0, nil
	}
	if t.UploadURL == nil {
		return false, 0, errors.New("no upload link")
	}
	if err := c.putFile(*t.UploadURL, f); err != nil {
		return false, 0, err
	}
	sent = f.size
	if it.Kind == "video" {
		if t.PosterUploadURL == nil {
			return false, 0, errors.New("no poster upload link")
		}
		if err := c.putFile(*t.PosterUploadURL, poster); err != nil {
			return false, 0, err
		}
		sent += poster.size
	}
	err = c.call(http.MethodPost, "/v1/studio/admin/imports/takes/"+url.PathEscape(t.TakeID)+"/complete", nil, http.StatusOK, nil)
	return false, sent, err
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func (c *client) putFile(link string, f fileRef) error {
	r, err := os.Open(f.path)
	if err != nil {
		return err
	}
	defer r.Close()
	return c.put(link, r, f.size, f.ct)
}

// readSize sets width and height from a png or jpeg header (the old db's values stay for webp or on error).
func (f *fileRef) readSize() {
	r, err := os.Open(f.path)
	if err != nil {
		return
	}
	defer r.Close()
	if cfg, _, err := image.DecodeConfig(r); err == nil {
		f.width, f.height = cfg.Width, cfg.Height
	}
}

// posterFor is the old derived poster when it is a png or jpeg, else frame 0 extracted by ffmpeg into tmp.
func posterFor(root, tmp string, it oldItem, video string) (fileRef, error) {
	if it.Poster != "" {
		p := fileRef{path: filepath.Join(root, "cache", "derived", it.Poster)}
		if b, err := os.ReadFile(p.path); err == nil {
			if cfg, format, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
				p.ct, p.size, p.width, p.height = "image/"+format, int64(len(b)), cfg.Width, cfg.Height
				return p, nil
			}
		}
	}
	out := filepath.Join(tmp, it.ID+".poster.png")
	if err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-y", "-i", video, "-frames:v", "1", out).Run(); err != nil {
		return fileRef{}, fmt.Errorf("no poster (ffmpeg: %v)", err)
	}
	p := fileRef{path: out, ct: "image/png"}
	fi, err := os.Stat(out)
	if err != nil {
		return p, errors.New("no poster")
	}
	p.size = fi.Size()
	p.readSize()
	return p, nil
}

type videoInfo struct {
	Width, Height int
	DurationS     float64
	FPS           float64
	Frames        int
	HasAudio      bool
}

// probeVideo reads a video's size, length and audio with ffprobe (a variable so tests need no ffmpeg).
var probeVideo = func(path string) (videoInfo, error) {
	var v videoInfo
	b, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", "-show_format", path).Output()
	if err != nil {
		return v, fmt.Errorf("ffprobe: %v", err)
	}
	var p struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Rate     string `json:"avg_frame_rate"`
			Frames   string `json:"nb_frames"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return v, err
	}
	for _, s := range p.Streams {
		switch s.Type {
		case "audio":
			v.HasAudio = true
		case "video":
			if v.Width != 0 {
				continue
			}
			v.Width, v.Height = s.Width, s.Height
			if n, d, ok := strings.Cut(s.Rate, "/"); ok {
				num, _ := strconv.ParseFloat(n, 64)
				den, _ := strconv.ParseFloat(d, 64)
				if den > 0 {
					v.FPS = num / den
				}
			}
			v.Frames, _ = strconv.Atoi(s.Frames)
			v.DurationS, _ = strconv.ParseFloat(s.Duration, 64)
		}
	}
	if v.DurationS == 0 {
		v.DurationS, _ = strconv.ParseFloat(p.Format.Duration, 64)
	}
	if v.Frames == 0 {
		v.Frames = int(v.DurationS*v.FPS + 0.5)
	}
	if v.Width == 0 || v.FPS == 0 || v.DurationS == 0 || v.Frames == 0 {
		return v, errors.New("ffprobe: no video stream")
	}
	return v, nil
}
