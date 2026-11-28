// Save with the video (API 1.79, plan 38 phase M, contracts/README.md "Music and sound"): the mux endpoint, its job
// and command, idempotent per pair, and the projector's new video take with both parents.
package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/helmstudio/helmgo/micro"

	"github.com/helmstudio/studio_service/api/studio"
)

func TestTakeMux(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	p := studio.NewProjector(e.pool)
	p.Store = e.store
	p.MountMedia(group)
	made := map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{}}
	video, audio := e.clipTake(userA, 2, true, made), e.audioTake(userA, 3)
	mux := func(user, take, other string, want int) map[string]any {
		return e.call(user, "POST", "/v1/studio/takes/"+take+"/mux", map[string]any{"video_take_id": other}, want)
	}

	mux(userA, audio, video, 409) // no media env
	e.mediaHost(0)
	mux(userA, video, video, 422)
	if out := mux(userA, audio, audio, 422); out["detail"] != "take "+audio+" is not a video" {
		t.Fatal(out)
	}
	mux(userA, audio, "nope", 422)
	mux(userB, audio, video, 404)
	mux(userA, audio, e.clipTake(userB, 2, true, nil), 404)
	mux(userA, e.audioTake(userB, 3), video, 404)

	job := mux(userA, audio, video, 202)["job"].(map[string]any)
	if same := mux(userA, audio, video, 202)["job"].(map[string]any); same["id"] != job["id"] {
		t.Fatal("not idempotent", job, same)
	}
	if job["kind"] != "mux" || job["source_take_id"] != audio || job["video_take_id"] != video || job["status"] != "queued" {
		t.Fatal(job)
	}
	jobID := job["id"].(string)
	_, sent := e.relay()
	var command struct {
		Kind         string              `json:"kind"`
		Inputs       []map[string]string `json:"inputs"`
		OutputPrefix string              `json:"output_prefix"`
	}
	_ = json.Unmarshal(sent[len(sent)-1].Data, &command)
	prefix := "u/" + userA + "/muxes/" + jobID + "/"
	if command.Kind != "mux" || len(command.Inputs) != 2 || command.Inputs[0]["kind"] != "video" ||
		command.Inputs[1]["kind"] != "audio" || command.OutputPrefix != prefix {
		t.Fatal(command)
	}

	pic := func(name string) map[string]any {
		return map[string]any{"key": prefix + "mux." + name + ".png", "width": 256, "height": 128, "bytes": 9}
	}
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "running", "encoder": "copy"})
	e.media(group, userA, jobID, "status", 2, map[string]any{"status": "succeeded", "encoder": "copy", "result": map[string]any{
		"key": prefix + "mux.mp4", "width": 256, "height": 128, "bytes": 1000, "etag": "8b9caefd0c1e4a7b9d2f3e4a5b6c7d8e",
		"video": map[string]any{"duration_s": 2, "fps": 24, "frames": 48, "has_audio": true, "poster": pic("poster"), "last": pic("last")}}})
	done := e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 200)
	takeID, _ := done["take_id"].(string)
	if done["status"] != "succeeded" || takeID == "" {
		t.Fatal(done)
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)
	if take["kind"] != "video" || take["source"] != "create" || take["title"] != "a fox" ||
		fmt.Sprint(take["parents"]) != fmt.Sprint([]any{video, audio}) {
		t.Fatal(take)
	}
	if again := mux(userA, audio, video, 202)["job"].(map[string]any); again["id"] != jobID {
		t.Fatal("a kept take answers its job", again)
	}
	e.call(userB, "GET", "/v1/studio/media-jobs/"+jobID, nil, 404)

	// the take deleted: a new job
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, takeID)
	if fresh := mux(userA, audio, video, 202)["job"].(map[string]any); fresh["id"] == jobID {
		t.Fatal("a deleted take's job answered again")
	}
}
