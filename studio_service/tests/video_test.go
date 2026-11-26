// Integration tests of M4 stage A (contracts/README.md "Video", "Two envs on one host"): a video take from the
// runner's output event to the gallery, runs and events (isolation included), the image-only inputs refusing it, the
// sweep removing its poster, workflows by media and the frame grid (the hand-over between host mac's envs:
// handover_test.go).
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/gallery"
)

// twoEnvs publishes host mac's two native runners, torch (the qwen-image fixtures) and mlx (the ltx fixtures), idle
// with nothing resident.
func (e *env) twoEnvs() {
	e.t.Helper()
	for env, prefix := range map[string]string{"torch": "qwen-image.", "mlx": "ltx."} {
		for _, kind := range []string{"nodes", "workflows", "models"} {
			data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", prefix+kind+".valid.json"))
			if err == nil {
				err = e.registry.SetRegistry("mac."+env+"."+kind, data)
			}
			if err != nil {
				e.t.Fatal(err)
			}
		}
		e.mac(env, nil)
	}
}

// mac publishes host mac's env key idle with a 48 GB budget, refreshed now (or at), holding resident (nil: nothing).
func (e *env) mac(env string, resident []map[string]any, at ...time.Time) {
	e.t.Helper()
	value, _ := json.Marshal(map[string]any{"host": "mac", "env": env, "state": "idle", "run_id": nil,
		"updated_at": append(at, time.Now())[0].UTC(), "resident": resident, "memory": map[string]any{"budget_gb": 48, "used_gb": 0}})
	if err := e.registry.SetHost("mac."+env, value); err != nil {
		e.t.Fatal(err)
	}
}

// ltxResident is what the mlx runner keeps after an ltx run at the template's precision (ltx.models.valid's plan).
var ltxResident = []map[string]any{
	{"component": "vae_encoder", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16", "gb": 0.59, "pinned": false},
	{"component": "upscaler", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16", "gb": 0.93, "pinned": false},
	{"component": "transformer", "model": "ltx-2.5", "variant": "distilled", "precision": "int8", "gb": 19.18, "pinned": false},
	{"component": "vae", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16", "gb": 0.76, "pinned": false},
	{"component": "audio_vae", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16", "gb": 0.06, "pinned": false},
	{"component": "vocoder", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16", "gb": 0.48, "pinned": false}}

// qwenResident is what the torch runner keeps after a qwen-image turbo int8 run (qwen-image.models.valid's plan).
var qwenResident = []map[string]any{
	{"component": "text_encoder", "model": "qwen-image-2.1", "variant": "base", "precision": "int8", "gb": 8.5, "pinned": false},
	{"component": "transformer", "model": "qwen-image-2.1", "variant": "turbo", "precision": "int8", "gb": 7.4, "pinned": false},
	{"component": "vae", "model": "qwen-image-2.1", "variant": "base", "precision": "bf16", "gb": 1.3, "pinned": false}}

// turbo is a qwen-image run's inputs for the variant with a memory plan.
var turbo = map[string]any{"model.variant": "turbo"}

// start creates a run of workflow as user in a new session and returns its id and session id.
func (e *env) start(user, workflow string, inputs map[string]any) (string, string) {
	e.t.Helper()
	sessionID := e.call(user, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := e.call(user, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": workflow,
		"inputs": inputs}, 202)
	return run["id"].(string), sessionID
}

// videoOutput is the evt.run.output of an ltx clip at index 0 of runID's attempt 1.
func videoOutput(owner, runID string) map[string]any {
	prefix := "u/" + owner + "/runs/" + runID + "/1/"
	return map[string]any{"index": 0, "key": prefix + "0.mp4", "kind": "video", "width": 448, "height": 256,
		"bytes": 412345, "seed": 42, "video": map[string]any{"duration_s": 49.0 / 24, "fps": 24, "frames": 49,
			"has_audio": true, "poster": map[string]any{"key": prefix + "0.poster.png", "width": 448, "height": 256,
				"bytes": 90210}}}
}

func TestVideoTake(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	runID, sessionID := e.start(userA, "ltx-text-to-video", map[string]any{"prompt.text": "a fox in the snow\nat dusk"})
	subjects, _ := e.relay()
	if !slices.Equal(subjects, []string{"cmd.run.mac.mlx"}) {
		t.Fatal(subjects)
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	noBlock := videoOutput(userA, runID)
	delete(noBlock, "video")
	foreign := videoOutput(userA, runID)
	foreign["index"] = 1
	foreign["video"].(map[string]any)["poster"].(map[string]any)["key"] = "u/" + userB + "/runs/x/0.poster.png"
	e.emit(userA, sessionID, runID, "output", 2, noBlock) // dropped: a video needs its metadata
	e.emit(userA, sessionID, runID, "output", 3, foreign) // dropped: the poster is not under the run's prefix
	e.emit(userA, sessionID, runID, "output", 4, videoOutput(userA, runID))
	e.emit(userA, sessionID, runID, "status", 5, map[string]any{"status": "succeeded"})

	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	outputs := run["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatal(outputs)
	}
	output := outputs[0].(map[string]any)
	video := output["video"].(map[string]any)
	if output["kind"] != "video" || !strings.Contains(output["url"].(string), "/1/0.mp4?") || video["frames"] != float64(49) ||
		video["fps"] != float64(24) || video["has_audio"] != true || !strings.Contains(video["poster_url"].(string), "/1/0.poster.png?") {
		t.Fatal(output)
	}
	page := e.call(userA, "GET", "/v1/studio/sessions/"+sessionID+"/events?since=0", nil, 200)
	for _, item := range page["events"].([]any) {
		if event := item.(map[string]any); event["type"] == "output" {
			poster := event["data"].(map[string]any)["video"].(map[string]any)["poster_url"]
			if poster != video["poster_url"] {
				t.Fatal("the event's poster is signed like the run's", event)
			}
		}
	}

	takes := e.call(userA, "GET", "/v1/studio/takes?kind=video", nil, 200)["takes"].([]any)
	if len(takes) != 1 || len(e.call(userA, "GET", "/v1/studio/takes?kind=image", nil, 200)["takes"].([]any)) != 0 {
		t.Fatal("kind filters", takes)
	}
	take := takes[0].(map[string]any)
	takeID := take["id"].(string)
	if take["content_type"] != "video/mp4" || take["url"] != output["url"] || take["thumb_url"] != video["poster_url"] ||
		take["video"].(map[string]any)["duration_s"] != 49.0/24 || take["title"] != "a fox in the snow" || take["width"] != float64(448) {
		t.Fatal(take)
	}
	if got := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200); got["video"] == nil || got["thumb_url"] != video["poster_url"] {
		t.Fatal(got)
	}
	e.call(userB, "GET", "/v1/studio/takes/"+takeID, nil, 404)
	e.call(userB, "GET", "/v1/studio/runs/"+runID, nil, 404)
	if len(e.call(userB, "GET", "/v1/studio/takes?kind=video", nil, 200)["takes"].([]any)) != 0 {
		t.Fatal("another user's video is not listed")
	}

	// image-only inputs refuse it: "Use in…" and a reference row
	if got := e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/use", map[string]any{"workflow_id": "echo"}, 422); got["detail"] != "workflow echo takes no video input" {
		t.Fatal(got)
	}
	got := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"echo.refs": refs(takeID)}}, 422)
	if got["detail"] != "echo.refs[1].image: take "+takeID+" is not an image" {
		t.Fatal(got)
	}

	// the sweep removes the MP4 and its poster
	for _, key := range []string{"0.mp4", "0.poster.png"} {
		data := []byte(key)
		_ = e.store.Put(context.Background(), "u/"+userA+"/runs/"+runID+"/1/"+key, bytes.NewReader(data), int64(len(data)), "")
	}
	e.call(userA, "DELETE", "/v1/studio/takes/"+takeID, nil, 204)
	e.exec(`UPDATE takes SET deleted_at = now() - interval '2 hours' WHERE id = $1`, takeID)
	if n, err := gallery.NewSweeper(e.pool, e.store).Sweep(context.Background()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, key := range []string{"0.mp4", "0.poster.png"} {
		if _, err := e.store.Stat(context.Background(), "u/"+userA+"/runs/"+runID+"/1/"+key); err == nil {
			t.Fatal(key, "left behind")
		}
	}
}

func TestWorkflowMediaAndFrames(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	ids := func(query string) []string {
		var out []string
		for _, item := range e.call(userA, "GET", "/v1/studio/workflows"+query, nil, 200)["workflows"].([]any) {
			workflow := item.(map[string]any)
			out = append(out, workflow["id"].(string)+":"+workflow["media"].(string))
		}
		slices.Sort(out)
		return out
	}
	if got := ids("?media=video"); !slices.Equal(got, []string{"echo-a2v:video", "echo-extend:video", "echo-image-to-video:video", "echo-retake:video",
		"echo-video:video", "ltx-audio-to-video:video", "ltx-extend:video", "ltx-image-to-video:video", "ltx-retake:video",
		"ltx-text-to-video:video"}) {
		t.Fatal(got)
	}
	if got := ids("?media=image"); slices.Contains(got, "ltx-text-to-video:video") || !slices.Contains(got, "qwen-image:image") {
		t.Fatal(got)
	}
	if got := e.call(userA, "GET", "/v1/studio/workflows?media=song", nil, 422); got["detail"] != "media must be image, video, audio, music, sound or voice" {
		t.Fatal(got)
	}
	// API 1.79: a sound class keeps the audio workflows of that class (echo-sound-video's Video output does not count)
	if got := ids("?media=sound"); !slices.Equal(got, []string{"echo-sound-video:audio", "echo-sound:audio", "echo-vary:audio"}) {
		t.Fatal(got)
	}
	if got := ids("?media=music"); !slices.Equal(got, []string{"echo-music-extend:audio", "echo-music:audio", "echo-repaint:audio"}) {
		t.Fatal(got)
	}

	// a template's studio block is sent as published and copied with it; a user workflow's media is computed
	template := e.call(userA, "GET", "/v1/studio/workflows/ltx-text-to-video", nil, 200)
	if template["studio"].(map[string]any)["labels"].(map[string]any)["sample1.steps"] != "Stage 1 steps" {
		t.Fatal(template["studio"])
	}
	copied := e.call(userA, "POST", "/v1/studio/workflows", map[string]any{"based_on": "ltx-text-to-video"}, 201)
	if copied["media"] != "video" || copied["studio"] == nil || !slices.Contains(ids("?media=video&source=mine"), copied["id"].(string)+":video") {
		t.Fatal(copied)
	}

	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := func(frames any, want int) map[string]any {
		return e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "ltx-text-to-video",
			"inputs": map[string]any{"duration.frames": frames}}, want)
	}
	for frames, detail := range map[any]string{
		50:   "duration.frames: 50 is not on the frame grid 9 + 8k up to 321",
		329:  "duration.frames: 329 is not on the frame grid 9 + 8k up to 321",
		1:    "duration.frames: 1 is not on the frame grid 9 + 8k up to 321",
		57.5: "duration.frames: 57.5 is not on the frame grid 9 + 8k up to 321",
		"49": "duration.frames: 49 is not on the frame grid 9 + 8k up to 321",
	} {
		if got := run(frames, 422); got["detail"] != detail {
			t.Fatal(frames, got)
		}
	}
	if got := run(57, 202); got["inputs"].(map[string]any)["duration.frames"] != float64(57) {
		t.Fatal(got)
	}
}
