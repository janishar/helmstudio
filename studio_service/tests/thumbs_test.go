package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/studio"
)

// Thumbnails (contracts/README.md "Thumbnails", API 1.48).

// thumbsHost is mediaHost with a runner that runs thumbs jobs (API 1.48).
func (e *env) thumbsHost() {
	e.t.Helper()
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "fake", "env": "fake", "state": "idle", "run_id": nil,
		"updated_at": time.Now().UTC(), "published_by": "test",
		"media": map[string]any{"ffmpeg": "7.1", "encoders": []string{"libx264"}, "thumbs": true}})
	if err := e.registry.SetHost("fake.fake", value); err != nil {
		e.t.Fatal(err)
	}
}

// putObject stores body at key in the test store.
func (e *env) putObject(key, body string) {
	e.t.Helper()
	if err := e.store.Put(context.Background(), key, bytes.NewReader([]byte(body)), int64(len(body)), "image/webp"); err != nil {
		e.t.Fatal(err)
	}
}

// thumbOf gives take id a stored thumbnail at key and returns key.
func (e *env) thumbOf(id, key string) string {
	e.t.Helper()
	e.putObject(key, "webp")
	e.exec(`UPDATE takes SET thumb = jsonb_build_object('key', $2::text, 'width', 512, 'height', 256, 'bytes', 4) WHERE id = $1`, id, key)
	return key
}

func signed(link any, key string) bool {
	s, _ := link.(string)
	return strings.Contains(s, "/files/"+key+"?")
}

func TestThumbsInListsAndRunOutputs(t *testing.T) {
	e := setup(t)
	runID, sessionID := e.start(userA, "echo", map[string]any{"prompt.text": "a fox"})
	e.relay()
	prefix := "u/" + userA + "/runs/" + runID + "/1/"
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := map[string]any{"index": 0, "key": prefix + "0.png", "kind": "image", "width": 1024, "height": 512, "bytes": 900,
		"thumb": map[string]any{"key": prefix + "0.thumb.webp", "width": 1024, "height": 512, "bytes": 90}}
	e.emit(userA, sessionID, runID, "output", 2, out)
	bad := map[string]any{"index": 1, "key": prefix + "1.png", "kind": "image", "width": 64, "height": 64, "bytes": 9,
		"thumb": map[string]any{"key": prefix + "0.thumb.webp", "width": 64, "height": 64, "bytes": 9}}
	e.emit(userA, sessionID, runID, "output", 3, bad) // not its own index: the take keeps no thumb
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	outputs := run["outputs"].([]any)
	first, second := outputs[0].(map[string]any), outputs[1].(map[string]any)
	if !signed(first["thumb_url"], prefix+"0.thumb.webp") || !signed(first["url"], prefix+"0.png") ||
		!signed(second["thumb_url"], prefix+"1.png") {
		t.Fatal("RunOutput.thumb_url is its take's", first, second)
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+first["take_id"].(string), nil, 200)
	if !signed(take["thumb_url"], prefix+"0.thumb.webp") || !signed(take["url"], prefix+"0.png") {
		t.Fatal(take)
	}

	// every list signs the thumbnail; the full file stays url
	img := e.image(userA)
	thumb := e.thumbOf(img, "u/"+userA+"/thumbs/j/0.webp")
	listed := e.call(userA, "GET", "/v1/studio/takes", nil, 200)["takes"].([]any)
	found := false
	for _, x := range listed {
		if x := x.(map[string]any); x["id"] == img {
			found = signed(x["thumb_url"], thumb) && strings.Contains(x["url"].(string), "/uploads/")
		}
	}
	if !found {
		t.Fatal("gallery list", listed)
	}
	el := e.element(userA, "Fox", "fox", "a fox", img)
	images := el["images"].([]any)
	if image := images[0].(map[string]any); !signed(image["thumb_url"], thumb) || !strings.Contains(image["url"].(string), "/uploads/") ||
		!signed(el["thumb_url"], thumb) {
		t.Fatal("library image", el)
	}
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{img}}, 201)
	tlTakes := e.call(userA, "GET", "/v1/studio/timelines/"+tl["id"].(string), nil, 200)["takes"].(map[string]any)
	if !signed(tlTakes[img].(map[string]any)["thumb_url"], thumb) {
		t.Fatal("timeline take", tlTakes)
	}

	// a deleted take's output has no thumb_url
	e.call(userA, "DELETE", "/v1/studio/takes/"+first["take_id"].(string), nil, 204)
	for _, o := range e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any) {
		if o := o.(map[string]any); o["take_id"] == first["take_id"] && o["thumb_url"] != nil {
			t.Fatal("deleted take", o)
		}
	}
}

func TestThumbsJob(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	p := studio.NewProjector(e.pool)
	p.Store = e.store
	p.MountMedia(group)
	ctx := context.Background()
	if id, err := studio.QueueThumbs(ctx, e.pool, e.registry); err != nil || id != "" {
		t.Fatal("no media env: nothing queued", id, err)
	}
	e.mediaHost(0)
	a1, a2 := e.image(userA), e.image(userA)
	if id, err := studio.QueueThumbs(ctx, e.pool, e.registry); err != nil || id != "" {
		t.Fatal("an older runner (no media.thumbs) is sent none", id, err)
	}
	e.thumbsHost()
	video := e.clipTake(userA, 2, true, nil) // poster u/A/runs/<id>/1/0.poster.png
	posterless := e.image(userA, "video")
	e.exec(`UPDATE takes SET video = NULL WHERE id = $1`, posterless)
	b := e.image(userB)
	pending, tried, done := e.image(userA), e.image(userA), e.image(userA)
	e.exec(`UPDATE takes SET ready = false WHERE id = $1`, pending)
	e.exec(`UPDATE takes SET thumb_tries = 3 WHERE id = $1`, tried)
	e.thumbOf(done, "u/"+userA+"/thumbs/old/0.webp")
	foreign := e.image(userA)
	e.exec(`UPDATE takes SET key = 'u/`+userB+`/uploads/x.png' WHERE id = $1`, foreign)
	// newest first (a new upload never waits behind a backfill): userA's a1, a2, video, then userB's b
	for i, id := range []string{b, video, a2, a1} {
		e.exec(`UPDATE takes SET created_at = clock_timestamp() + make_interval(hours => $2) WHERE id = $1`, id, i)
	}

	jobID, err := studio.QueueThumbs(ctx, e.pool, e.registry)
	if err != nil || jobID == "" {
		t.Fatal(jobID, err)
	}
	if again, err := studio.QueueThumbs(ctx, e.pool, e.registry); err != nil || again != "" {
		t.Fatal("never two thumbs jobs", again, err)
	}
	_, sent := e.relay()
	var command struct {
		Kind         string           `json:"kind"`
		Inputs       []map[string]any `json:"inputs"`
		OutputPrefix string           `json:"output_prefix"`
	}
	_ = json.Unmarshal(sent[len(sent)-1].Data, &command)
	prefix := "u/" + userA + "/thumbs/" + jobID + "/"
	if command.Kind != "thumbs" || len(command.Inputs) != 3 || command.OutputPrefix != prefix || sent[len(sent)-1].OwnerID != userA ||
		!strings.HasSuffix(command.Inputs[2]["key"].(string), "/0.poster.png") || command.Inputs[2]["kind"] != "image" {
		t.Fatal("one owner's ready live takes without a thumb, newest first; a video's poster", command)
	}
	// no endpoint: 404; not one of the user's open media jobs
	e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 404)
	e.call(userA, "POST", "/v1/studio/media-jobs/"+jobID+"/cancel", nil, 404)
	if n := e.count(`SELECT count(*) FROM media_jobs WHERE id = $1 AND take_ids = $2::uuid[]`, jobID, []string{a1, a2, video}); n != 1 {
		t.Fatal("take_ids in input order")
	}

	// a1 is deleted meanwhile: its late thumbnail is deleted at once; a wrong key is refused; video skips
	e.call(userA, "DELETE", "/v1/studio/takes/"+a1, nil, 204)
	for _, k := range []string{"0.webp", "1.webp"} {
		e.putObject(prefix+k, "webp")
	}
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "running"})
	e.media(group, userA, jobID, "status", 2, map[string]any{"status": "succeeded", "result": map[string]any{"thumbs": []any{
		map[string]any{"index": 0, "key": prefix + "0.webp", "width": 512, "height": 256, "bytes": 4},
		map[string]any{"index": 1, "key": prefix + "1.webp", "width": 512, "height": 256, "bytes": 4},
		map[string]any{"index": 2, "skip": true}}}})
	if _, err := e.store.Stat(ctx, prefix+"0.webp"); err == nil {
		t.Fatal("a swept take's late thumbnail is deleted")
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+a2, nil, 200)
	if !signed(take["thumb_url"], prefix+"1.webp") {
		t.Fatal(take)
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND thumb IS NULL AND thumb_tries = 3`, video); n != 1 {
		t.Fatal("a skip is never tried again")
	}

	// the next job: userA has none left (posterless, pending, tried, done, foreign are never picked): userB's
	next, err := studio.QueueThumbs(ctx, e.pool, e.registry)
	if err != nil || next == "" {
		t.Fatal(next, err)
	}
	_, sent = e.relay()
	_ = json.Unmarshal(sent[len(sent)-1].Data, &command)
	if sent[len(sent)-1].OwnerID != userB || len(command.Inputs) != 1 {
		t.Fatal(command)
	}
	// a failed job adds a try to its takes and deletes its keys no take points at
	bPrefix := "u/" + userB + "/thumbs/" + next + "/"
	e.putObject(bPrefix+"0.webp", "half written")
	e.media(group, userB, next, "status", 1, map[string]any{"status": "failed", "failure": map[string]any{"class": "internal", "detail": "x"}})
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND thumb_tries = 1`, b); n != 1 {
		t.Fatal("a failed job adds a try")
	}
	if _, err := e.store.Stat(ctx, bPrefix+"0.webp"); err == nil {
		t.Fatal("a failed job's keys are deleted")
	}
	// at ThumbTries a take is left alone
	e.exec(`UPDATE takes SET thumb_tries = 3 WHERE id = $1`, b)
	if id, err := studio.QueueThumbs(ctx, e.pool, e.registry); err != nil || id != "" {
		t.Fatal("nothing left", id, err)
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND thumb IS NULL AND thumb_tries = 0`, foreign); n != 1 {
		t.Fatal("a key outside its owner is never sent")
	}

	// a lost thumbs job: the sweep ends it, adds a try and deletes its keys
	c := e.image(userB)
	lost, _ := studio.QueueThumbs(ctx, e.pool, e.registry)
	cPrefix := "u/" + userB + "/thumbs/" + lost + "/"
	e.putObject(cPrefix+"0.webp", "half")
	e.exec(`UPDATE media_jobs SET heard_at = clock_timestamp() - interval '1 hour' WHERE id = $1`, lost)
	if err := studio.SweepMediaJobs(ctx, e.pool, e.registry, nil, nil, e.store, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND thumb_tries = 1`, c); n != 1 {
		t.Fatal("a lost job adds a try")
	}
	if _, err := e.store.Stat(ctx, cPrefix+"0.webp"); err == nil {
		t.Fatal("a lost job's keys are deleted")
	}

	// a thumbs job never counts toward MaxOpenMediaJobs
	e.thumbsHost()
	e.exec(`UPDATE takes SET thumb_tries = 0 WHERE id = $1`, c)
	if id, _ := studio.QueueThumbs(ctx, e.pool, e.registry); id == "" {
		t.Fatal("queued")
	}
	for range studio.MaxOpenMediaJobs - 1 {
		e.exec(`INSERT INTO media_jobs (id, owner_id, kind, host, env) VALUES ($1, $2, 'frame', 'fake', 'fake')`, utility.NewID(), userB)
	}
	tlb := e.call(userB, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{e.clipTake(userB, 1, true, nil)}}, 201)
	e.call(userB, "POST", "/v1/studio/timelines/"+tlb["id"].(string)+"/export", map[string]any{"revision": 1}, 202)
}

func TestThumbsSweepAndFrames(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	group := &micro.NatsJetStreamGroup{}
	p := studio.NewProjector(e.pool)
	p.Store = e.store
	p.MountMedia(group)

	// the sweep removes a deleted take's thumbnail with it
	img := e.image(userA)
	thumb := e.thumbOf(img, "u/"+userA+"/thumbs/j/0.webp")
	e.call(userA, "DELETE", "/v1/studio/takes/"+img, nil, 204)
	e.exec(`UPDATE takes SET deleted_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, img)
	if n, err := gallery.NewSweeper(e.pool, e.store).Sweep(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := e.store.Stat(ctx, thumb); err == nil {
		t.Fatal("the sweep removes the thumbnail")
	}

	// a run video's first frame copies its thumbnail; the frame take owns its copy
	video := e.clipTake(userA, 2, true, map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{}})
	vthumb := e.thumbOf(video, "u/"+userA+"/runs/"+video+"/1/0.thumb.webp")
	first := e.call(userA, "POST", "/v1/studio/takes/"+video+"/frame", map[string]any{"at": "first"}, 200)["take"].(map[string]any)
	if s, _ := first["thumb_url"].(string); !strings.Contains(s, "/frames/"+first["id"].(string)+".thumb.webp?") {
		t.Fatal("a frame copy copies the thumbnail", first)
	}
	if _, err := e.store.Stat(ctx, vthumb); err != nil {
		t.Fatal("the video keeps its own")
	}

	// a frame job at last: the video keeps the job's frame, the frame take its own copy (never the same object)
	e.mediaHost(0)
	old := e.clipTake(userA, 2, false, nil)
	job := e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 202)["job"].(map[string]any)
	jobID := job["id"].(string)
	prefix := "u/" + userA + "/frames/" + jobID + "/"
	e.putObject(prefix+"frame.png", "png")
	e.putObject(prefix+"frame.thumb.webp", "webp")
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "succeeded", "result": map[string]any{
		"key": prefix + "frame.png", "width": 1280, "height": 720, "bytes": 3,
		"thumb": map[string]any{"key": prefix + "frame.thumb.webp", "width": 910, "height": 512, "bytes": 4}}})
	frame := e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 200)["take"].(map[string]any)
	if !strings.Contains(frame["url"].(string), "/frames/"+frame["id"].(string)+".png?") || !signed(frame["thumb_url"], prefix+"frame.thumb.webp") {
		t.Fatal("the frame take's own copy and its thumbnail", frame)
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND video->'last'->>'key' = $2`, old, prefix+"frame.png"); n != 1 {
		t.Fatal("the video's last frame is the job's")
	}
	// sweeping the frame take leaves the video's last frame
	e.call(userA, "DELETE", "/v1/studio/takes/"+frame["id"].(string), nil, 204)
	e.exec(`UPDATE takes SET deleted_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, frame["id"])
	if _, err := gallery.NewSweeper(e.pool, e.store).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Stat(ctx, prefix+"frame.png"); err != nil {
		t.Fatal("the video's last frame survives its frame take's sweep", err)
	}
	// a take from before 1.48 sharing the video's last frame: neither sweep deletes it while the other is live
	legacy := e.clipTake(userA, 2, false, nil)
	e.exec(`UPDATE takes SET key = $2, deleted_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, legacy, prefix+"frame.png")
	if n, err := gallery.NewSweeper(e.pool, e.store).Sweep(ctx); err != nil || n != 0 {
		t.Fatal("a take sharing a live video's last frame is left", n, err)
	}
	e.exec(`UPDATE takes SET deleted_at = NULL WHERE id = $1`, legacy)
	e.exec(`UPDATE takes SET deleted_at = clock_timestamp() - interval '2 hours' WHERE id = $1`, old)
	if n, err := gallery.NewSweeper(e.pool, e.store).Sweep(ctx); err != nil || n != 0 {
		t.Fatal("a video whose last frame a live take shares is left", n, err)
	}
	if _, err := e.store.Stat(ctx, prefix+"frame.png"); err != nil {
		t.Fatal("the shared last frame survives", err)
	}
}

// Migration 00032 (owner, 2026-11-23: a thumbnail once the short side is over 360, was 512): the takes the old rule
// skipped get their tries back; bigger or smaller ones, ones with a thumbnail and deleted ones keep theirs.
func TestThumbsMigrationRetriesTheSkipped(t *testing.T) {
	e := setup(t)
	take := func(kind string, w, h int) string {
		id := e.image(userA, kind)
		e.exec(`UPDATE takes SET width = $2, height = $3, thumb_tries = 3 WHERE id = $1`, id, w, h)
		return id
	}
	still, portrait, tall := take("image", 864, 480), take("image", 480, 864), take("image", 600, 400)
	big, small, edge := take("image", 1024, 600), take("image", 640, 360), take("image", 1024, 512)
	poster, smallPoster := take("video", 64, 32), take("video", 1280, 720)
	e.exec(`UPDATE takes SET video = '{"poster": {"key": "k", "width": 864, "height": 480, "bytes": 9}}' WHERE id = $1`, poster)
	e.exec(`UPDATE takes SET video = '{"poster": {"key": "k", "width": 320, "height": 180, "bytes": 9}}' WHERE id = $1`, smallPoster)
	gone, made := take("image", 864, 480), take("image", 864, 480)
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, gone)
	e.thumbOf(made, "u/"+userA+"/thumbs/old/0.webp")

	sqldb, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err = goose.DownTo(sqldb, ".", 31); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(sqldb, "."); err != nil {
		t.Fatal(err)
	}
	retried := []string{still, portrait, tall, edge, poster}
	if n := e.count(`SELECT count(*) FROM takes WHERE thumb_tries = 0`); n != len(retried) ||
		e.count(`SELECT count(*) FROM takes WHERE thumb_tries = 0 AND id = ANY($1::uuid[])`, retried) != len(retried) {
		t.Fatal("only the live takes without a thumbnail whose short side is 361–512 are picked again", n, big, small, smallPoster)
	}
}
