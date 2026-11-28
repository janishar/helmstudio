// Magic: Send to Timeline (API 1.82, plan 39 phase G, contracts/README.md "Magic: Send to Timeline"): a dry run, a
// send with no bakes (201, the sequence at once), a send that bakes (202, bake jobs, the sequence made when the last
// one is in), the sends list and Remove, another user's board 404, the 100 sequences 409.
package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/helmstudio/helmgo/micro"

	"github.com/helmstudio/studio_service/api/studio"
)

func TestMagicSend(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	p := studio.NewProjector(e.pool)
	p.Store = e.store
	p.MountMedia(group)
	s1, s2, line := e.clipTake(userA, 4, true, nil), e.clipTake(userA, 4, true, nil), e.audioTake(userA, 3)
	doc := map[string]any{"target": map[string]any{"width": 256, "height": 128, "fps": 24, "sample_rate": 48000},
		"lanes": []any{map[string]any{"id": "shots", "name": "Shots", "role": "shots"},
			map[string]any{"id": "lines", "name": "Lines", "role": "voice"}},
		"blocks": []any{
			map[string]any{"id": "a", "lane": "shots", "at": 0, "length": 48, "kind": "video", "name": "Shot 1",
				"request": map[string]any{"kind": "video"}, "takes": []any{s1}, "effects": []any{}},
			map[string]any{"id": "b", "lane": "shots", "at": 48, "length": 48, "kind": "video", "name": "Shot 2",
				"request": map[string]any{"kind": "video"}, "takes": []any{s2}, "effects": []any{}},
			map[string]any{"id": "v", "lane": "lines", "at": 12, "length": 48, "kind": "voice", "name": "Line 1",
				"request": map[string]any{"kind": "voice"}, "takes": []any{line}, "effects": []any{}}},
		"links": []any{map[string]any{"block": "v", "to": []any{"b"}}}, "transitions": []any{}, "assets": []any{}, "notes": []any{}}
	board := e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": "Tide", "doc": doc}, 201)
	id, rev := board["id"].(string), int(board["revision"].(float64))
	send := func(user string, body map[string]any, want int) map[string]any {
		return e.call(user, "POST", "/v1/studio/magic/boards/"+id+"/send", body, want)
	}

	// a dry run: the plan, nothing stored
	dry := send(userA, map[string]any{"revision": rev, "dry_run": true}, 200)
	plan := dry["plan"].(map[string]any)
	if plan["name"] != "Tide · send 1" || plan["bakes"].(float64) != 0 || dry["send"] != nil {
		t.Fatal(dry)
	}
	goes := plan["goes"].([]any)
	if len(goes) != 2 || goes[0].(map[string]any)["track"] != "V1" || goes[1].(map[string]any)["track"] != "A2" ||
		goes[1].(map[string]any)["linked"].(float64) != 1 {
		t.Fatal(goes)
	}
	send(userA, map[string]any{"revision": rev + 1}, 409)
	send(userB, map[string]any{"revision": rev}, 404)

	// no bakes: the sequence at once
	out := send(userA, map[string]any{"revision": rev}, 201)
	sent := out["send"].(map[string]any)
	tlID, _ := sent["timeline_id"].(string)
	if sent["status"] != "done" || tlID == "" || sent["name"] != "Tide · send 1" {
		t.Fatal(sent)
	}
	tl := e.call(userA, "GET", "/v1/studio/timelines/"+tlID, nil, 200)
	tracks := tl["tracks"].([]any)
	v1 := tracks[0].(map[string]any)["clips"].([]any)
	a2 := tracks[1].(map[string]any)["clips"].([]any)
	if len(v1) != 2 || len(a2) != 1 || a2[0].(map[string]any)["link"] != v1[1].(map[string]any)["id"] {
		t.Fatal(tracks)
	}

	// a wipe: baked; the sequence waits for its bake
	board = e.call(userA, "POST", "/v1/studio/magic/boards/"+id+"/edits", map[string]any{"base_revision": rev,
		"ops": []any{map[string]any{"op": "transition", "between": []any{"a", "b"}, "kind": "wipe"}}}, 200)["board"].(map[string]any)
	rev = int(board["revision"].(float64))
	send(userA, map[string]any{"revision": rev}, 409) // no media env
	e.mediaHost(0)
	out = send(userA, map[string]any{"revision": rev}, 202)
	sent = out["send"].(map[string]any)
	jobs := sent["jobs"].([]any)
	if sent["status"] != "baking" || len(jobs) != 1 || sent["name"] != "Tide · send 2" {
		t.Fatal(sent)
	}
	job := jobs[0].(map[string]any)
	jobID := job["id"].(string)
	if job["kind"] != "bake" || job["send_id"] != sent["id"] {
		t.Fatal(job)
	}
	if again := send(userA, map[string]any{"revision": rev}, 409); again["detail"] != "This board is still sending; wait for it." {
		t.Fatal(again)
	}
	_, msgs := e.relay()
	var command struct {
		Kind         string              `json:"kind"`
		Inputs       []map[string]string `json:"inputs"`
		OutputPrefix string              `json:"output_prefix"`
		Bake         struct {
			Frames int      `json:"frames"`
			Args   []string `json:"args"`
		} `json:"bake"`
	}
	_ = json.Unmarshal(msgs[len(msgs)-1].Data, &command)
	prefix := "u/" + userA + "/bakes/" + jobID + "/"
	if command.Kind != "bake" || len(command.Inputs) != 2 || command.OutputPrefix != prefix || command.Bake.Frames != 12 {
		t.Fatal(command)
	}
	pic := func(name string) map[string]any {
		return map[string]any{"key": prefix + "bake." + name + ".png", "width": 256, "height": 128, "bytes": 9}
	}
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "running", "encoder": "libx264"})
	e.media(group, userA, jobID, "status", 2, map[string]any{"status": "succeeded", "encoder": "libx264", "result": map[string]any{
		"key": prefix + "bake.mp4", "width": 256, "height": 128, "bytes": 1000, "etag": "8b9caefd0c1e4a7b9d2f3e4a5b6c7d8e",
		"video": map[string]any{"duration_s": 0.5, "fps": 24, "frames": 12, "has_audio": true, "poster": pic("poster"), "last": pic("last")}}})
	list := e.call(userA, "GET", "/v1/studio/magic/boards/"+id+"/sends", nil, 200)["sends"].([]any)
	newest := list[0].(map[string]any)
	if len(list) != 2 || newest["status"] != "done" || newest["timeline_id"] == nil || newest["baked"].(float64) != 1 {
		t.Fatal(list)
	}
	tl = e.call(userA, "GET", "/v1/studio/timelines/"+newest["timeline_id"].(string), nil, 200)
	v1 = tl["tracks"].([]any)[0].(map[string]any)["clips"].([]any)
	if len(v1) != 3 || fmt.Sprint(v1[1].(map[string]any)["out"]) != "0.5" {
		t.Fatal(v1)
	}
	bakeTake := v1[1].(map[string]any)["take_id"].(string)
	if take := e.call(userA, "GET", "/v1/studio/takes/"+bakeTake, nil, 200); take["source"] != "bake" ||
		take["title"] != "Tide · Shot 1 → Shot 2 · Wipe" {
		t.Fatal(take)
	}
	e.call(userB, "GET", "/v1/studio/magic/boards/"+id+"/sends", nil, 404)

	// Remove from list: the sequence stays
	e.call(userB, "DELETE", "/v1/studio/magic/boards/"+id+"/sends/"+newest["id"].(string), nil, 404)
	e.call(userA, "DELETE", "/v1/studio/magic/boards/"+id+"/sends/"+newest["id"].(string), nil, 204)
	if list := e.call(userA, "GET", "/v1/studio/magic/boards/"+id+"/sends", nil, 200)["sends"].([]any); len(list) != 1 {
		t.Fatal(list)
	}
	e.call(userA, "GET", "/v1/studio/timelines/"+newest["timeline_id"].(string), nil, 200)

	// a failed bake fails its send
	out = send(userA, map[string]any{"revision": rev}, 202)
	jobID = out["send"].(map[string]any)["jobs"].([]any)[0].(map[string]any)["id"].(string)
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "failed", "failure": map[string]any{"class": "ffmpeg", "detail": "boom"}})
	list = e.call(userA, "GET", "/v1/studio/magic/boards/"+id+"/sends", nil, 200)["sends"].([]any)
	if f := list[0].(map[string]any); f["status"] != "failed" || f["failure"] != "a bake failed: boom" {
		t.Fatal(f)
	}

	// 100 sequences: the Timeline's 409
	e.exec(`INSERT INTO timelines (id, owner_id, name, doc) SELECT gen_random_uuid(), $1, 'x', '{}'::jsonb FROM generate_series(1, 100)`, userA)
	if full := send(userA, map[string]any{"revision": rev, "dry_run": true}, 409); full["detail"] != "You have 100 sequences; delete some first." {
		t.Fatal(full)
	}
}
