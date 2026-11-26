// Integration tests of M7 pack 1 (API 1.19, contracts/README.md "Media inputs", "Timeline: take actions"): media and
// span values checked, snapped and resolved, another user's take refused everywhere, source_map stored, "Use in…"
// offers, audio uploads, a clip's actions, insert after and an audio take kept off the timeline.
package tests

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/helmstudio/helmgo/utility"
)

// audioTake stores a ready audio upload of owner, seconds long, with its object, and returns its id.
func (e *env) audioTake(owner string, seconds float64) string {
	e.t.Helper()
	id, data := utility.NewID(), []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	key := "u/" + owner + "/uploads/" + id + ".wav"
	if err := e.store.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), "audio/wav"); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, audio, etag)
		VALUES ($1, $2, 'audio', 'upload', 'tone', $3, 'audio/wav', $4, true, $5, 'e')`, id, owner, key, len(data),
		map[string]any{"duration_s": seconds})
	return id
}

func TestMediaInputs(t *testing.T) {
	e := setup(t)
	video, foreign := e.clipTake(userA, 2, true, nil), e.clipTake(userB, 2, true, nil)
	audio, long := e.audioTake(userA, 3), e.audioTake(userA, 20)
	offGrid := e.clipTake(userA, 2, true, nil)
	e.exec(`UPDATE takes SET width = 250 WHERE id = $1`, offGrid)
	e.exec(`UPDATE takes SET bytes = 5 WHERE kind = 'video'`) // clipTake's objects are 5 bytes ("0.mp4")
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + session + "/runs"
	run := func(workflow string, inputs map[string]any, want int) map[string]any {
		return e.call(userA, "POST", runs, map[string]any{"workflow_id": workflow, "inputs": inputs}, want)
	}
	source := func(id string, extra ...any) map[string]any {
		out := map[string]any{"take_id": id}
		for i := 0; i < len(extra); i += 2 {
			out[extra[i].(string)] = extra[i+1]
		}
		return out
	}
	span := map[string]any{"start": 0.51, "end": 1.25}
	for want, inputs := range map[string]map[string]any{
		"retake.span: a range is required":                                                         {"retake.source": source(video)},
		"retake.source: a take is required":                                                        {"retake.span": span},
		"retake.source: take " + foreign + " not found":                                            {"retake.source": source(foreign), "retake.span": span},
		"retake.source: take " + audio + " is not a video":                                         {"retake.source": source(audio), "retake.span": span},
		"retake.source: take " + offGrid + " is 250 × 128; this input needs sides on a 32 px grid": {"retake.source": source(offGrid), "retake.span": span},
		"retake.source: this input takes the whole take (no in or out)":                            {"retake.source": source(video, "in", 0.5), "retake.span": span},
		"retake.span: start must be before end":                                                    {"retake.source": source(video), "retake.span": map[string]any{"start": 1, "end": 0.5}},
		"retake.span: end 3.0 is past the clip's end 2.0":                                          {"retake.source": source(video), "retake.span": map[string]any{"start": 1, "end": 3}},
	} {
		if got := run("echo-retake", inputs, 422); got["detail"] != want {
			t.Errorf("%s: %v", want, got["detail"])
		}
	}
	for want, inputs := range map[string]map[string]any{
		"a2v.source: take " + video + " has no sound":                                  {"a2v.source": source(video)},
		"a2v.source: in must be before out":                                            {"a2v.source": source(audio, "in", 2.0, "out", 1.0)},
		"a2v.source: out 4.0 is past the take's end 3.0":                               {"a2v.source": source(audio, "out", 4.0)},
		"a2v.source: the part lasts 15.0 s; this input takes at most 13.4 s":           {"a2v.source": source(long, "in", 1.0, "out", 16.0)},
		"a2v.source: take " + foreign + " not found":                                   {"a2v.source": source(foreign)},
		`a2v.source: must be {"take_id": <uuid>, "in"?: <seconds>, "out"?: <seconds>}`: {"a2v.source": source(audio, "key", "u/x")},
	} {
		if want == "a2v.source: take "+video+" has no sound" {
			e.exec(`UPDATE takes SET video = jsonb_set(video, '{has_audio}', 'false') WHERE id = $1`, video)
		}
		if got := run("echo-a2v", inputs, 422); got["detail"] != want {
			t.Errorf("%s: %v", want, got["detail"])
		}
		e.exec(`UPDATE takes SET video = jsonb_set(video, '{has_audio}', 'true') WHERE id = $1`, video)
	}

	// a retake: the span snapped to the source's frames, the source resolved to defs media at release
	retake := run("echo-retake", map[string]any{"retake.source": source(video), "retake.span": span}, 202)
	if got := e.stored(retake["id"].(string))["retake"].Params["span"]; fmt.Sprint(got) != "map[end:1.25 start:0.5]" {
		t.Fatal(got)
	}
	sent := e.command()["retake"].Params
	media := sent["source"].(map[string]any)
	if media["take_id"] != video || media["kind"] != "video" || media["fps"] != float64(24) || media["frames"] != float64(48) ||
		media["etag"] == "" || !strings.HasPrefix(media["key"].(string), "u/"+userA+"/runs/") || media["in"] != nil ||
		fmt.Sprint(sent["span"]) != "map[end:1.25 start:0.5]" {
		t.Fatal(sent)
	}

	// its output: source_map kept (only from one of its own media takes), parents gain the media take
	retakeID := retake["id"].(string)
	e.emit(userA, session, retakeID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := videoOutput(userA, retakeID)
	out["source_map"] = map[string]any{"take_id": video, "offset_s": 0, "new_in_s": 0.375, "new_out_s": 1.375}
	e.emit(userA, session, retakeID, "output", 2, out)
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+retakeID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	made := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)
	if fmt.Sprint(made["parents"]) != "["+video+"]" || made["source_map"].(map[string]any)["new_out_s"] != 1.375 {
		t.Fatal(made)
	}

	// an audio part: in / out snapped to the millisecond, filled when absent
	a2v := run("echo-a2v", map[string]any{"a2v.source": source(audio, "in", 0.20004)}, 202)
	if got := e.stored(a2v["id"].(string))["a2v"].Params["source"]; fmt.Sprint(got) != "map[in:0.2 out:3 take_id:"+audio+"]" {
		t.Fatal(got)
	}
	// an off-grid end that rounds up: out kept at the last millisecond at or before it (the runner's bound)
	odd := e.audioTake(userA, 5.0416)
	a2v = run("echo-a2v", map[string]any{"a2v.source": source(odd)}, 202)
	if got := e.stored(a2v["id"].(string))["a2v"].Params["source"]; fmt.Sprint(got) != "map[in:0 out:5.041 take_id:"+odd+"]" {
		t.Fatal(got)
	}

	// "Use in…": offers and use; another user's take is 404
	uses := e.call(userA, "GET", "/v1/studio/takes/"+video+"/uses", nil, 200)["uses"].([]any)
	var offered []string
	for _, u := range uses {
		offer := u.(map[string]any)
		offered = append(offered, offer["workflow_id"].(string)+":"+offer["param"].(string))
	}
	if strings.Join(offered, " ") != "echo-sound-video:foley.source echo-retake:retake.source echo-a2v:a2v.source echo-extend:extend.source" {
		t.Fatal(uses)
	}
	e.call(userB, "GET", "/v1/studio/takes/"+video+"/uses", nil, 404)
	used := e.call(userA, "POST", "/v1/studio/takes/"+audio+"/use", map[string]any{"workflow_id": "echo-a2v"}, 200)
	if fmt.Sprint(used["inputs"]) != "map[a2v.source:map[in:0 out:3 take_id:"+audio+"]]" {
		t.Fatal(used)
	}
	if got := e.call(userA, "POST", "/v1/studio/takes/"+audio+"/use", map[string]any{"workflow_id": "echo-retake"}, 422); got["detail"] != "workflow echo-retake takes no audio input" {
		t.Fatal(got)
	}
	e.call(userB, "POST", "/v1/studio/takes/"+audio+"/use", map[string]any{"workflow_id": "echo-a2v"}, 404)

	// a media take deleted while its run waits fails it at release with the media-neutral text: never sent
	e.exec(`UPDATE runs SET status = 'succeeded', finished_at = now() WHERE status <> 'succeeded'`) // frees the host
	waiting := run("echo-a2v", map[string]any{"a2v.source": source(long, "out", 2.0)}, 202)
	e.call(userA, "DELETE", "/v1/studio/takes/"+long, nil, 204)
	e.host(0)
	if _, sent := e.relay(); len(sent) != 0 {
		t.Fatal("a deleted media take was sent", sent)
	}
	if gone := e.call(userA, "GET", "/v1/studio/runs/"+waiting["id"].(string), nil, 200); gone["status"] != "failed" ||
		gone["error"] != "input take "+long+" is gone" {
		t.Fatal(gone)
	}
}

func TestAudioUpload(t *testing.T) {
	e := setup(t)
	wav := []byte("RIFF\x24\x00\x00\x00WAVEfmt 0123")
	for body, want := range map[string]map[string]any{
		"duration_s is required for audio":                                         {"filename": "a.wav", "content_type": "audio/wav", "bytes": 10},
		"audio is longer than 600 s":                                               {"filename": "a.wav", "content_type": "audio/wav", "bytes": 10, "duration_s": 601},
		"only png, jpeg and webp images or wav, mp3 and m4a audio can be uploaded": {"filename": "a.ogg", "content_type": "audio/ogg", "bytes": 10},
	} {
		if got := e.call(userA, "POST", "/v1/studio/uploads", want, 422); got["detail"] != body {
			t.Errorf("%s: %v", body, got)
		}
	}
	upload := e.call(userA, "POST", "/v1/studio/uploads", map[string]any{"filename": "tone.wav", "content_type": "audio/wav",
		"bytes": len(wav), "duration_s": 3}, 201)
	id := upload["take_id"].(string)
	if !strings.Contains(upload["upload_url"].(string), "/uploads/"+id+".wav?") {
		t.Fatal(upload)
	}
	key := "u/" + userA + "/uploads/" + id + ".wav"
	if err := e.store.Put(context.Background(), key, bytes.NewReader(wav), int64(len(wav)), "audio/wav"); err != nil {
		t.Fatal(err)
	}
	take := e.call(userA, "POST", "/v1/studio/uploads/"+id+"/complete", nil, 200)
	if take["kind"] != "audio" || take["width"] != nil || take["audio"].(map[string]any)["duration_s"] != float64(3) ||
		take["thumb_url"] != take["url"] {
		t.Fatal(take)
	}
	bad := e.call(userA, "POST", "/v1/studio/uploads", map[string]any{"filename": "x.mp3", "content_type": "audio/mpeg",
		"bytes": len(wav), "duration_s": 3}, 201)["take_id"].(string)
	if err := e.store.Put(context.Background(), "u/"+userA+"/uploads/"+bad+".mp3", bytes.NewReader(wav), int64(len(wav)), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	if got := e.call(userA, "POST", "/v1/studio/uploads/"+bad+"/complete", nil, 422); got["detail"] != "not a valid mp3 file" {
		t.Fatal(got)
	}

	// API 1.32: an audio take goes on an audio track, not V1
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "S"}, 201)["id"].(string)
	if got := e.call(userA, "POST", "/v1/studio/timelines/"+tl+"/clips", map[string]any{"take_ids": []string{id}, "track": "V1"}, 422); got["detail"] != "take_ids[0]: an audio take goes on an audio track" {
		t.Fatal(got)
	}
}

func TestClipActions(t *testing.T) {
	e := setup(t)
	a, b := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 3, true, nil)
	e.exec(`UPDATE takes SET bytes = 5 WHERE kind = 'video'`) // clipTake's objects are 5 bytes ("0.mp4")
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "Fox", "take_ids": []string{a, b}}, 201)
	id, clips := tl["id"].(string), v1(tl)
	first := clips[0].(map[string]any)["id"].(string)
	actions := func(user, clip, query string, want int) map[string]any {
		return e.call(user, "GET", "/v1/studio/timelines/"+id+"/clips/"+clip+"/actions"+query, nil, want)
	}
	// the whole clip: retake over the whole take, the audio part as the clip's
	got := actions(userA, first, "", 200)["actions"].([]any)
	if len(got) != 3 || fmt.Sprint(got[2].(map[string]any)["inputs"]) != "map[extend.direction:after extend.source:map[take_id:"+a+"]]" ||
		fmt.Sprint(got[0].(map[string]any)["inputs"]) != "map[retake.source:map[take_id:"+a+"] retake.span:map[end:2 start:0]]" {
		t.Fatal(got)
	}
	// a selection, snapped to the take's frames
	got = actions(userA, first, "?start=0.51&end=1.26", 200)["actions"].([]any)
	if len(got) != 2 || fmt.Sprint(got[0].(map[string]any)["inputs"].(map[string]any)["retake.span"]) != "map[end:1.25 start:0.5]" { // no extend mid-take
		t.Fatal(got)
	}
	if bad := actions(userA, "nope", "", 422); bad["detail"] != "unknown clip nope" {
		t.Fatal(bad)
	}
	if bad := actions(userA, first, "?start=1&end=0.5", 422); bad["detail"] != "start must be before end" {
		t.Fatal(bad)
	}
	actions(userB, first, "", 404)

	// insert after: an empty slot at the clip's end cut, holding the shot
	shot := e.call(userA, "POST", "/v1/studio/timelines/"+id+"/shots", map[string]any{"slot": map[string]any{"after": first,
		"length": 1, "id": "ins"}, "run": map[string]any{"workflow_id": "echo-retake", "inputs": map[string]any{
		"retake.source": map[string]any{"take_id": a}, "retake.span": map[string]any{"start": 0, "end": 1}}}}, 202)
	if order := v1(shot["timeline"].(map[string]any)); order[1].(map[string]any)["id"] != "ins" || order[1].(map[string]any)["shot"] == nil {
		t.Fatal(order)
	}
	// its take fills the slot with the generated part only (source_map; t_edit fill)
	runID, session := shot["run"].(map[string]any)["id"].(string), shot["run"].(map[string]any)["session_id"].(string)
	e.relay()
	e.emit(userA, session, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := videoOutput(userA, runID)
	out["source_map"] = map[string]any{"take_id": a, "offset_s": 0, "new_in_s": 0.375, "new_out_s": 1.375}
	e.emit(userA, session, runID, "output", 2, out)
	filled := v1(e.call(userA, "GET", "/v1/studio/timelines/"+id, nil, 200))[1].(map[string]any)
	if filled["in"] != 0.375 || filled["out"] != 1.375 || filled["shot"] != nil {
		t.Fatal(filled)
	}
}
