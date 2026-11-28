package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
)

// Take facts (contracts/README.md "Take facts", API 1.104).

// factsHost is mediaHost with a runner that runs thumbs and facts jobs.
func (e *env) factsHost() {
	e.t.Helper()
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "fake", "env": "fake", "state": "idle", "run_id": nil,
		"updated_at": time.Now().UTC(), "published_by": "test",
		"media": map[string]any{"ffmpeg": "7.1", "encoders": []string{"libx264"}, "thumbs": true, "facts": true}})
	if err := e.registry.SetHost("fake.fake", value); err != nil {
		e.t.Fatal(err)
	}
}

func TestFactsJob(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	p := studio.NewProjector(e.pool)
	p.Store = e.store
	p.MountMedia(group)
	ctx := context.Background()
	e.thumbsHost()
	video, sound, gone := e.clipTake(userA, 2, true, nil), e.audioTake(userA, 3), e.audioTake(userA, 4)
	long, img := e.audioTake(userA, 1300), e.image(userA)
	if id, err := studio.QueueFacts(ctx, e.pool, e.registry); err != nil || id != "" {
		t.Fatal("an older runner (no media.facts) is sent none", id, err)
	}
	e.factsHost()
	for i, id := range []string{gone, sound, video} { // newest first: video, sound, gone
		e.exec(`UPDATE takes SET created_at = clock_timestamp() + make_interval(hours => $2) WHERE id = $1`, id, i)
	}
	jobID, err := studio.QueueFacts(ctx, e.pool, e.registry)
	if err != nil || jobID == "" {
		t.Fatal(jobID, err)
	}
	if again, err := studio.QueueFacts(ctx, e.pool, e.registry); err != nil || again != "" {
		t.Fatal("never two facts jobs", again, err)
	}
	_, sent := e.relay()
	var command struct {
		Kind         string           `json:"kind"`
		Inputs       []map[string]any `json:"inputs"`
		OutputPrefix string           `json:"output_prefix"`
	}
	_ = json.Unmarshal(sent[len(sent)-1].Data, &command)
	prefix := "u/" + userA + "/facts/" + jobID + "/"
	if command.Kind != "facts" || len(command.Inputs) != 3 || command.OutputPrefix != prefix ||
		command.Inputs[0]["kind"] != "video" || command.Inputs[1]["kind"] != "audio" {
		t.Fatal("video and audio takes, newest first; never an image or a take past 20 minutes", command, img, long)
	}
	e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 404)

	vf := map[string]any{"seconds": 2.0, "sound_seconds": 2.0, "lufs": -14.0, "peak_db": -1.0, "silence_start": 0.0,
		"silence_end": 0.0, "end_lufs": -14.5, "abrupt": true, "cuts": []float64{1.0}}
	af := map[string]any{"seconds": 3.0, "sound_seconds": 3.0, "lufs": -16.0, "peak_db": -2.0, "silence_start": 0.5,
		"silence_end": 1.0, "end_lufs": -70.0, "abrupt": false}
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil)
	e.putObject(prefix+"0.jpg", jpg.String())
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "succeeded", "result": map[string]any{"facts": []any{
		map[string]any{"index": 0, "facts": vf, "key": prefix + "0.jpg", "width": 1024, "height": 384, "bytes": 3,
			"at": []float64{0.1, 0.2}},
		map[string]any{"index": 1, "facts": af},
		map[string]any{"index": 2, "error": "input 2 is missing"}}}})
	got := e.call(userA, "GET", "/v1/studio/takes/"+video, nil, 200)["facts"].(map[string]any)
	if !signed(got["grid_url"], prefix+"0.jpg") || got["abrupt"] != true || len(got["at"].([]any)) != 2 || len(got["cuts"].([]any)) != 1 {
		t.Fatal(got)
	}
	got = e.call(userA, "GET", "/v1/studio/takes/"+sound, nil, 200)["facts"].(map[string]any)
	if got["grid_url"] != nil || got["silence_end"] != 1.0 || len(got["at"].([]any)) != 0 {
		t.Fatal(got)
	}
	if f, ok := e.call(userA, "GET", "/v1/studio/takes/"+gone, nil, 200)["facts"]; !ok || f != nil {
		t.Fatal("not made yet: null", f)
	}
	if _, ok := e.call(userA, "GET", "/v1/studio/takes/"+img, nil, 200)["facts"]; ok {
		t.Fatal("an image has no facts")
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND facts IS NULL AND facts_tries = 1`, gone); n != 1 {
		t.Fatal("an error adds a try")
	}

	// studio.see: the video's grid and facts; the audio's facts and no picture
	thread := utility.NewID()
	e.exec(`INSERT INTO assistant_threads (id, owner_id, title) VALUES ($1, $2, 'look')`, thread, userA)
	seen := e.service.See(ctx, &studio.SeeRequest{OwnerID: userA, ThreadID: thread, TakeID: video})
	if seen.Code != "" || len(*seen.Images) != 1 || len(seen.At) != 2 || len(seen.Facts) == 0 {
		t.Fatal(seen.Code, seen.At, string(seen.Facts))
	}
	heard := e.service.See(ctx, &studio.SeeRequest{OwnerID: userA, ThreadID: thread, TakeID: sound})
	if len(*heard.Images) != 0 || len(heard.Facts) == 0 {
		t.Fatal(heard)
	}

	// a failed job adds a try to each take and deletes its grids no take points at
	next, err := studio.QueueFacts(ctx, e.pool, e.registry)
	if err != nil || next == "" {
		t.Fatal(next, err)
	}
	e.relay()
	np := "u/" + userA + "/facts/" + next + "/"
	e.putObject(np+"0.jpg", "jpg")
	e.media(group, userA, next, "status", 1, map[string]any{"status": "failed",
		"failure": map[string]any{"class": "internal", "detail": "x"}})
	if _, err := e.store.Stat(ctx, np+"0.jpg"); err == nil {
		t.Fatal("an unused grid is deleted")
	}
	if n := e.count(`SELECT count(*) FROM takes WHERE id = $1 AND facts_tries = 2`, gone); n != 1 {
		t.Fatal("a failed job adds a try")
	}
}

// The backfill measures only an owner's newest FactsNewest video and audio takes (owner, 2026-11-28).
func TestFactsNewestOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.factsHost()
	old := e.audioTake(userA, 2)
	e.exec(`UPDATE takes SET created_at = clock_timestamp() - interval '1 day' WHERE id = $1`, old)
	for range studio.FactsNewest {
		e.audioTake(userA, 2)
	}
	e.exec(`UPDATE takes SET facts = '{}'::jsonb WHERE id <> $1`, old) // the newest are measured
	if id, err := studio.QueueFacts(ctx, e.pool, e.registry); err != nil || id != "" {
		t.Fatal("a take older than the newest 200 is not backfilled", id, err)
	}
}
