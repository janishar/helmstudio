package gallery

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/migrations"
	"github.com/helmstudio/studio_service/registry"
)

const (
	userA = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
	userB = "11111111-2222-4333-8444-555555555555"
)

var (
	callerA = common.Caller{ID: userA}
	callerB = common.Caller{ID: userB}
)

// ------------------------------------------------------------------ without a database

// pngHeader is a PNG signature and IHDR of a w × h image: enough for image.DecodeConfig.
func pngHeader(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32([]byte("IHDR"), w), h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	out := binary.BigEndian.AppendUint32([]byte("\x89PNG\r\n\x1a\n"), 13)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

// webpHeader is a lossless 1 × 1 WebP (VP8L) header.
var webpHeader = []byte("RIFF\x12\x00\x00\x00WEBPVP8L\x05\x00\x00\x00\x2f\x00\x00\x00\x00\x00")

// encoded is a real 3 × 2 image as format.
func encoded(t *testing.T, format string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.White)
	var out bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// oriented is encoded(t, "jpeg") with an APP1 Exif segment whose IFD0 holds Orientation.
func oriented(t *testing.T, order binary.AppendByteOrder, orientation uint16) []byte {
	tiff := []byte("MM\x00\x2a")
	if order == binary.AppendByteOrder(binary.LittleEndian) {
		tiff = []byte("II\x2a\x00")
	}
	tiff = order.AppendUint32(tiff, 8)
	tiff = order.AppendUint16(tiff, 1)
	tiff = order.AppendUint16(tiff, 0x0112)
	tiff = order.AppendUint16(tiff, 3) // SHORT
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // value padding, then no next IFD
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	segment := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE1}, uint16(len(app1)+2))
	jpg := encoded(t, "jpeg")
	return append(append(append([]byte{}, jpg[:2]...), append(segment, app1...)...), jpg[2:]...)
}

// checkImage takes only a real image of the declared type, at most MaxSide a side.
func TestCheckImage(t *testing.T) {
	for _, test := range []struct {
		name, contentType string
		head              []byte
		w, h              int
		detail            string
	}{
		{"png", "image/png", encoded(t, "png"), 3, 2, ""},
		{"jpeg", "image/jpeg", encoded(t, "jpeg"), 3, 2, ""},
		{"webp", "image/webp", webpHeader, 1, 1, ""},
		{"html as png", "image/png", []byte("<html><script>alert(1)</script></html>"), 0, 0, "not a valid png image"},
		{"png as jpeg", "image/jpeg", encoded(t, "png"), 0, 0, "not a valid jpeg image"},
		{"empty", "image/webp", nil, 0, 0, "not a valid webp image"},
		{"16384 wide", "image/png", pngHeader(16384, 1), 16384, 1, ""},
		{"too wide", "image/png", pngHeader(16385, 1), 0, 0, "image is larger than 16384 px"},
		{"too tall", "image/png", pngHeader(1, 20000), 0, 0, "image is larger than 16384 px"},
		{"50 MP", "image/png", pngHeader(10000, 5000), 10000, 5000, ""},
		{"over 50 MP", "image/png", pngHeader(13000, 13000), 0, 0, "image is larger than 50 MP"},
		{"jpeg turned a quarter", "image/jpeg", oriented(t, binary.BigEndian, 6), 2, 3, ""},
		{"jpeg turned a quarter (II)", "image/jpeg", oriented(t, binary.LittleEndian, 8), 2, 3, ""},
		{"jpeg upside down", "image/jpeg", oriented(t, binary.LittleEndian, 3), 3, 2, ""},
	} {
		w, h, detail := checkImage(test.head, test.contentType)
		if w != test.w || h != test.h || detail != test.detail {
			t.Errorf("%s: %d × %d %q", test.name, w, h, detail)
		}
	}
}

// ParseFilter reads every query value; a malformed one is a 422 detail; cursors round-trip.
func TestParseFilter(t *testing.T) {
	filter, detail := ParseFilter(url.Values{"kind": {"image"}, "source": {"upload"}, "starred": {"false"},
		"session_id": {userA}, "q": {"  red   fox "}, "limit": {"2"}}, true)
	if detail != "" || filter.Kind != "image" || filter.Source != "upload" || filter.Starred == nil || *filter.Starred ||
		filter.SessionID != userA || strings.Join(filter.Words, ",") != "red,fox" || filter.Limit != 2 {
		t.Fatal(filter, detail)
	}
	if filter, _ := ParseFilter(url.Values{"session_id": {"x"}}, false); filter.SessionID != "" || filter.Limit != 60 {
		t.Fatal("session_id read where it is not a filter", filter)
	}
	for _, bad := range []url.Values{{"kind": {"gif"}}, {"source": {"timeline"}}, {"starred": {"1"}}, {"session_id": {"x"}},
		{"q": {strings.Repeat("a", 201)}}, {"limit": {"0"}}, {"limit": {"201"}}, {"limit": {"x"}}, {"cursor": {"nope"}},
		{"cursor": {cursor{time.Now(), "x"}.encode()}}} {
		if _, detail := ParseFilter(bad, true); detail == "" {
			t.Errorf("%v accepted", bad)
		}
	}
	at := time.Date(2026, 11, 14, 10, 0, 0, 123456000, time.UTC)
	back, ok := decodeCursor(cursor{at, userB}.encode())
	if !ok || !back.CreatedAt.Equal(at) || back.ID != userB {
		t.Fatal(back, ok)
	}
}

// Request bodies: unknown fields and bad values are 422 details; an upload's title is its base name.
func TestBodies(t *testing.T) {
	title, starred := " Fox ", true
	for body, want := range map[*dto.TakePatch]string{
		{Title: &title}: "", {Starred: &starred}: "", {}: "title or starred is required",
		{Title: new(string)}: "title must be 1-200 characters",
	} {
		if got := body.Validate(); got != want {
			t.Errorf("%+v: %q", body, got)
		}
	}
	var patch dto.TakePatch
	if err := patch.UnmarshalJSON([]byte(`{"starred": true, "key": "u/x"}`)); err != nil || patch.Validate() != "key: not a field of TakePatch" {
		t.Fatal(patch.Validate())
	}
	seconds := []float64{3, 600.5}
	for _, test := range []struct {
		body dto.UploadCreate
		want string
	}{
		{dto.UploadCreate{Filename: "C:\\pics\\fox reference 1.JPG", ContentType: "image/jpeg", Bytes: 1}, ""},
		{dto.UploadCreate{Filename: "a.svg", ContentType: "image/svg+xml", Bytes: 1}, "only png, jpeg and webp images or wav and mp3 audio can be uploaded"},
		{dto.UploadCreate{Filename: "a.wav", ContentType: "audio/wav", Bytes: 1, DurationS: &seconds[0]}, ""},
		{dto.UploadCreate{Filename: "a.mp3", ContentType: "audio/mpeg", Bytes: 1}, "duration_s is required for audio"},
		{dto.UploadCreate{Filename: "a.mp3", ContentType: "audio/mpeg", Bytes: 1, DurationS: &seconds[1]}, "audio is longer than 600 s"},
		{dto.UploadCreate{Filename: "a.png", ContentType: "image/png", Bytes: 1, DurationS: &seconds[0]}, "duration_s is for audio only"},
		{dto.UploadCreate{Filename: "a.png", ContentType: "image/png", Bytes: dto.MaxUploadBytes + 1}, "file is larger than 50 MB"},
		{dto.UploadCreate{Filename: "a.png", ContentType: "image/png"}, "bytes must be at least 1"},
		{dto.UploadCreate{ContentType: "image/png", Bytes: 1}, "filename must be 1-255 characters"},
	} {
		if got := test.body.Validate(); got != test.want {
			t.Errorf("%+v: %q", test.body, got)
		}
	}
	if got := (&dto.UploadCreate{Filename: "C:\\pics\\fox reference 1.JPG"}).Title(); got != "fox reference 1" {
		t.Fatal(got)
	}
	if got := (&dto.UploadCreate{Filename: ".png"}).Title(); got != ".png" {
		t.Fatal(got)
	}
	prompt := "  A fox at dusk\nsecond line"
	if got := Title(dto.MadeWith{Prompt: &prompt, WorkflowTitle: "Echo"}); got != "A fox at dusk" {
		t.Fatal(got)
	}
	if got := Title(dto.MadeWith{WorkflowTitle: "Echo"}); got != "Echo" {
		t.Fatal(got)
	}
	long := strings.Repeat("é", 100)
	if got := Title(dto.MadeWith{Prompt: &long}); got != strings.Repeat("é", 80) {
		t.Fatal(got)
	}
}

// ------------------------------------------------------------------ with a database

// fixture is a migrated scratch schema (gallery_test, so it never meets the tests package's public schema), the
// service on a memory store, and one session with one run of user A.
type fixture struct {
	t                *testing.T
	pool             *pgxpool.Pool
	store            *storage.Memory
	service          *Service
	sessionID, runID string
}

// setup skips without TEST_DATABASE_URL.
func setup(t *testing.T) *fixture {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	sqldb, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.Exec(`DROP SCHEMA IF EXISTS gallery_test CASCADE; CREATE SCHEMA gallery_test`); err != nil {
		t.Fatal(err)
	}
	sqldb.Close()
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	url := base + separator + "search_path=gallery_test"
	if sqldb, err = sql.Open("pgx", url); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	if err := goose.Up(sqldb, "."); err != nil {
		t.Fatal(err)
	}
	sqldb.Close()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := &fixture{t: t, pool: pool, store: storage.NewMemory(), sessionID: utility.NewID(), runID: utility.NewID()}
	reg := &registry.Registry{}
	find := func(_ context.Context, _ common.Caller, id string) (registry.Workflow, *int, error) {
		workflow, ok := reg.Workflow(id)
		if !ok {
			return workflow, nil, errNotFound
		}
		return workflow, nil, nil
	}
	f.service = NewService(pool, common.NewSigner("test-secret-0123456789", "http://kong", time.Hour), f.store, reg, find)
	f.system(`INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, 'S')`, f.sessionID, userA)
	f.system(`INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status) VALUES ($1, $2, $3, 'echo', 'fake', '{}', 'running')`,
		f.runID, userA, f.sessionID)
	return f
}

// system runs one statement as System.
func (f *fixture) system(query string, args ...any) {
	f.t.Helper()
	if err := common.System(context.Background(), f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), query, args...)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

// record stores output index of the fixture's run with prompt, as the projector does, and returns the take id.
func (f *fixture) record(index int, prompt string) string {
	f.t.Helper()
	var id string
	err := common.System(context.Background(), f.pool, func(tx pgx.Tx) (err error) {
		id, err = Record(context.Background(), tx, Output{OwnerID: userA, SessionID: f.sessionID, RunID: f.runID, Index: index,
			Key: "u/" + userA + "/runs/" + f.runID + "/" + string(rune('0'+index)) + ".png", Kind: "image", Bytes: 10,
			MadeWith: dto.MadeWith{WorkflowID: "echo", WorkflowTitle: "Echo", Inputs: map[string]any{"prompt.text": prompt}, Prompt: &prompt}})
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

// status is the HTTP status of err (200 for nil).
func status(err error) int {
	var apiError *network.APIError
	if err == nil {
		return 200
	}
	if errors.As(err, &apiError) {
		return apiError.Code
	}
	return 500
}

// ids lists a page's take ids.
func ids(page dto.TakePage) []string {
	out := []string{}
	for _, take := range page.Takes {
		out = append(out, take.ID)
	}
	return out
}

// A run output is stored once per (run, index); its take carries signed links (never the key), and another user's
// id is a 404 on every route.
func TestTakesAndIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := f.record(0, "a red fox\nat dusk")
	if again := f.record(0, "a red fox"); again != first {
		t.Fatalf("the same output made a second take: %s, %s", first, again)
	}
	take, err := f.service.Get(ctx, callerA, first)
	if err != nil || take.Title != "a red fox" || take.Source != "create" || take.ContentType != "image/png" ||
		take.MadeWith == nil || *take.MadeWith.Prompt != "a red fox\nat dusk" || take.RunID == nil || *take.RunID != f.runID ||
		len(take.Parents) != 0 || !strings.HasPrefix(take.URL, "http://kong/files/u/"+userA+"/runs/") || take.ThumbURL != take.URL {
		t.Fatal(take, err)
	}
	reuse, err := f.service.Reuse(ctx, callerA, first)
	if err != nil || reuse.WorkflowID != "echo" || reuse.Inputs["prompt.text"] != "a red fox\nat dusk" {
		t.Fatal(reuse, err)
	}

	// user B sees nothing of A's, whatever the route
	starred := true
	for name, err := range map[string]error{
		"get": func() error { _, err := f.service.Get(ctx, callerB, first); return err }(),
		"update": func() error {
			_, err := f.service.Update(ctx, callerB, first, &dto.TakePatch{Starred: &starred})
			return err
		}(),
		"reuse":    func() error { _, err := f.service.Reuse(ctx, callerB, first); return err }(),
		"complete": func() error { _, err := f.service.CompleteUpload(ctx, callerB, first); return err }(),
		"session": func() error {
			_, err := f.service.SessionTakes(ctx, callerB, f.sessionID, Filter{Limit: 60})
			return err
		}(),
		"delete":    f.service.Delete(ctx, callerB, first),
		"malformed": func() error { _, err := f.service.Get(ctx, callerA, "nope"); return err }(),
	} {
		if status(err) != 404 {
			t.Errorf("%s as another user: %v", name, err)
		}
	}
	if page, err := f.service.List(ctx, callerB, Filter{Limit: 60}); err != nil || len(page.Takes) != 0 {
		t.Fatal("another user's list", page, err)
	}
	if page, err := f.service.List(ctx, callerB, Filter{Limit: 60, SessionID: f.sessionID}); err != nil || len(page.Takes) != 0 {
		t.Fatal("another user's session filter", page, err)
	}
	if _, err := f.service.Get(ctx, callerA, first); err != nil {
		t.Fatal("B's attempts changed A's take", err)
	}

	// deleting the session keeps the take, without its session and run
	f.system(`DELETE FROM sessions WHERE id = $1`, f.sessionID)
	if take, err := f.service.Get(ctx, callerA, first); err != nil || take.SessionID != nil || take.RunID != nil {
		t.Fatal(take, err)
	}

	// a soft delete hides it at once; again is 404
	if err := f.service.Delete(ctx, callerA, first); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, callerA, first); status(err) != 404 || status(f.service.Delete(ctx, callerA, first)) != 404 {
		t.Fatal("a deleted take is still there", err)
	}
}

// Filters combine, q needs every word in the title or the prompt, pages follow next_cursor without gaps or repeats.
func TestListFiltersAndPages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	fox := f.record(0, "a red fox")
	f.record(1, "a blue whale")
	f.record(2, "100% red_fox")
	starred, renamed := true, "Night owl"
	if _, err := f.service.Update(ctx, callerA, fox, &dto.TakePatch{Title: &renamed, Starred: &starred}); err != nil {
		t.Fatal(err)
	}
	search := func(filter Filter) int {
		filter.Limit = 60
		page, err := f.service.List(ctx, callerA, filter)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Takes)
	}
	yes, no := true, false
	for name, test := range map[string]struct {
		filter Filter
		want   int
	}{
		"all":                {Filter{}, 3},
		"prompt word":        {Filter{Words: []string{"RED"}}, 2},
		"title and prompt":   {Filter{Words: []string{"owl", "fox"}}, 1},
		"every word":         {Filter{Words: []string{"red", "whale"}}, 0},
		"% is literal":       {Filter{Words: []string{"100%"}}, 1},
		"_ is literal":       {Filter{Words: []string{"d_f"}}, 1},
		"starred":            {Filter{Starred: &yes}, 1},
		"unstarred":          {Filter{Starred: &no}, 2},
		"kind":               {Filter{Kind: "video"}, 0},
		"source":             {Filter{Source: "upload"}, 0},
		"session":            {Filter{SessionID: f.sessionID}, 3},
		"unknown session":    {Filter{SessionID: utility.NewID()}, 0},
		"source and starred": {Filter{Source: "create", Starred: &yes}, 1},
	} {
		if got := search(test.filter); got != test.want {
			t.Errorf("%s: %d takes, want %d", name, got, test.want)
		}
	}

	all, _ := f.service.List(ctx, callerA, Filter{Limit: 60})
	var paged []string
	filter := Filter{Limit: 2}
	for {
		page, err := f.service.SessionTakes(ctx, callerA, f.sessionID, filter)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, ids(page)...)
		if page.NextCursor == nil {
			break
		}
		filter.After, _ = decodeCursor(*page.NextCursor)
	}
	if strings.Join(paged, ",") != strings.Join(ids(all), ",") || len(paged) != 3 {
		t.Fatalf("pages %v, list %v", paged, ids(all))
	}
	if _, err := f.service.SessionTakes(ctx, callerA, utility.NewID(), Filter{Limit: 2}); status(err) != 404 {
		t.Fatal("unknown session", err)
	}
}

// An upload is invisible until complete, which checks the object; a failed check discards it; uploads have no
// settings to reuse.
func TestUploads(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	start := func(contentType string, size int) dto.Upload {
		upload, err := f.service.CreateUpload(ctx, callerA, &dto.UploadCreate{Filename: "fox.png", ContentType: contentType, Bytes: int64(size)})
		if err != nil {
			t.Fatal(err)
		}
		return upload
	}
	key := func(upload dto.Upload) string {
		parsed, _ := url.Parse(upload.UploadURL)
		return strings.TrimPrefix(parsed.Path, "/files/")
	}
	image := encoded(t, "png")
	upload := start("image/png", len(image))
	if key(upload) != "u/"+userA+"/uploads/"+upload.TakeID+".png" || !strings.Contains(upload.UploadURL, "ct=image%2Fpng") ||
		time.Until(upload.ExpiresAt) > UploadLink || time.Until(upload.ExpiresAt) < UploadLink-time.Minute {
		t.Fatal(upload)
	}
	if _, err := f.service.Get(ctx, callerA, upload.TakeID); status(err) != 404 {
		t.Fatal("a pending upload is visible", err)
	}
	if _, err := f.service.CompleteUpload(ctx, callerA, upload.TakeID); status(err) != 409 {
		t.Fatal("complete before the PUT", err)
	}
	_ = f.store.Put(ctx, key(upload), bytes.NewReader(image), int64(len(image)), "image/png")
	if _, err := f.service.CompleteUpload(ctx, callerB, upload.TakeID); status(err) != 404 {
		t.Fatal("another user's complete", err)
	}
	take, err := f.service.CompleteUpload(ctx, callerA, upload.TakeID)
	if err != nil || take.Source != "upload" || *take.Width != 3 || *take.Height != 2 || take.Title != "fox" ||
		take.MadeWith != nil || take.SessionID != nil {
		t.Fatal(take, err)
	}
	var etag string // the content hash, set at complete
	if err := common.System(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT etag FROM takes WHERE id = $1`, take.ID).Scan(&etag)
	}); err != nil || etag != fmt.Sprintf("%x", md5.Sum(image)) {
		t.Fatal("etag", etag, err)
	}
	if again, err := f.service.CompleteUpload(ctx, callerA, upload.TakeID); err != nil || again.ID != take.ID {
		t.Fatal("a repeated complete", again, err)
	}
	if _, err := f.service.Reuse(ctx, callerA, take.ID); status(err) != 409 {
		t.Fatal("reuse of an upload", err)
	}

	// HTML declared as a png, and a size that differs: 422, then 404
	for _, body := range []string{"<html><script>alert(1)</script></html>", "short"} {
		size := len(body)
		if body == "short" {
			size++
		}
		bad := start("image/png", size)
		_ = f.store.Put(ctx, key(bad), strings.NewReader(body), int64(len(body)), "image/png")
		if _, err := f.service.CompleteUpload(ctx, callerA, bad.TakeID); status(err) != 422 {
			t.Fatal(body, err)
		}
		if _, err := f.service.CompleteUpload(ctx, callerA, bad.TakeID); status(err) != 404 {
			t.Fatal("a discarded upload", body, err)
		}
	}
	if page, _ := f.service.List(ctx, callerA, Filter{Limit: 60, Source: "upload"}); len(page.Takes) != 1 {
		t.Fatal(page)
	}
}

// The sweep removes the objects and rows of takes deleted past Grace and of uploads pending past UploadTTL, never
// a live take, a fresh delete, a key outside the owner's prefix or a key a live take still uses.
func TestSweep(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	put := func(key string) { _ = f.store.Put(ctx, key, strings.NewReader("x"), 1, "image/png") }
	exists := func(key string) bool { _, err := f.store.Stat(ctx, key); return err == nil }
	live, fresh, old := f.record(0, "live"), f.record(1, "fresh"), f.record(2, "old")
	keyOf := func(id string) (key string) {
		_ = common.System(ctx, f.pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT key FROM takes WHERE id = $1`, id).Scan(&key)
		})
		return key
	}
	for _, id := range []string{live, fresh, old} {
		put(keyOf(id))
	}
	_ = f.service.Delete(ctx, callerA, fresh)
	_ = f.service.Delete(ctx, callerA, old)
	f.system(`UPDATE takes SET deleted_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, old)

	// a deleted take whose key is another user's, and one sharing the live take's key
	foreign, shared := utility.NewID(), utility.NewID()
	f.system(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, deleted_at)
		VALUES ($1, $2, 'image', 'upload', 'x', $3, 'image/png', 1, true, clock_timestamp() - interval '2 hours'),
		       ($4, $2, 'image', 'upload', 'x', $5, 'image/png', 1, true, clock_timestamp() - interval '2 hours')`,
		foreign, userA, "u/"+userB+"/uploads/victim.png", shared, keyOf(live))
	put("u/" + userB + "/uploads/victim.png")

	// an abandoned upload and a recent one
	abandoned, err := f.service.CreateUpload(ctx, callerA, &dto.UploadCreate{Filename: "a.png", ContentType: "image/png", Bytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	recent, _ := f.service.CreateUpload(ctx, callerA, &dto.UploadCreate{Filename: "b.png", ContentType: "image/png", Bytes: 1})
	f.system(`UPDATE takes SET created_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, abandoned.TakeID)
	put(keyOf(abandoned.TakeID))

	// a judge's thumbnails: one of the old take only, one a live take's decision shows too
	thumb := func(c string) string { return "u/" + userA + "/llm/thumbs/" + strings.Repeat(c, 64) + ".jpg" }
	seen := func(take, key string) string {
		return `{"take_id": "` + take + `", "frame": null, "key": "` + key + `", "sha256": "", "width": 64, "height": 64}`
	}
	batch := utility.NewID()
	f.system(`INSERT INTO batches (id, owner_id, title, workflow_id, workflow_title, snapshot, inputs)
		VALUES ($1, $2, 'b', 'w', 'w', '{}', '{}')`, batch, userA)
	f.system(`INSERT INTO decisions (id, batch_id, n, attempt, owner_id, seq, kind, data, take_ids)
		VALUES ($7, $1, 1, 1, $2, 1, 'judge', $3::jsonb, ARRAY[$4, $5]::uuid[]),
		       ($8, $1, 2, 1, $2, 2, 'judge', $6::jsonb, ARRAY[$5]::uuid[])`,
		batch, userA, `{"seen": [`+seen(old, thumb("a"))+`, `+seen(old, thumb("b"))+`]}`, old, live,
		`{"seen": [`+seen(live, thumb("b"))+`]}`, batch+":1:1:judge:1", batch+":2:1:judge:1")
	put(thumb("a"))
	put(thumb("b"))

	removed, err := NewSweeper(f.pool, f.store).Sweep(ctx)
	if err != nil || removed != 2 {
		t.Fatal(removed, err)
	}
	if exists(thumb("a")) || !exists(thumb("b")) {
		t.Fatal("the old take's own thumbnail must go, the one a live take's decision shows must stay")
	}
	if exists(keyOf(old)) || exists("u/"+userA+"/uploads/"+abandoned.TakeID+".png") {
		t.Fatal("swept objects remain")
	}
	if !exists(keyOf(live)) || !exists(keyOf(fresh)) || !exists("u/"+userB+"/uploads/victim.png") {
		t.Fatal("the sweep removed an object it must keep")
	}
	var left int
	_ = common.System(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM takes WHERE id = ANY($1::uuid[])`,
			[]string{live, fresh, foreign, shared, recent.TakeID}).Scan(&left)
	})
	if left != 5 {
		t.Fatalf("%d of the kept rows left", left)
	}
}

// "Use in…" on the ltx templates: image to video takes the take as keyframe row 1 (at 0 s, strength 1 by the item
// defaults); text to video takes no image.
func TestImageInputLtx(t *testing.T) {
	var r registry.Registry
	_ = r.SetHost("mac.mlx", []byte(`{"host": "mac", "env": "mlx", "state": "idle", "updated_at": "2026-11-15T10:00:00Z"}`)) // keys read while present
	for _, kind := range []string{"nodes", "workflows"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", "ltx."+kind+".valid.json"))
		if err == nil {
			err = r.SetRegistry("mac.mlx."+kind, data)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	i2v, _ := r.Workflow("ltx-image-to-video")
	if key, param, detail := imageInput(i2v, nil); key != "keyframes.images" || param.Type != "rows" || detail != "" {
		t.Fatal(key, detail)
	}
	t2v, _ := r.Workflow("ltx-text-to-video")
	if _, _, detail := imageInput(t2v, nil); detail != "workflow ltx-text-to-video takes no image input" {
		t.Fatal(detail)
	}
}
