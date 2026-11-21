// Integration tests of Timeline audio (API 1.32, contracts/README.md "Timeline: New line", "Make shot"): a New line's
// placeholder on an audio track, its refusals, the projector's fill, a redo and its 409, a cancelled line removed, then
// Make shot from the filled line (the offer's slot and the place edit splitting an empty slot).
package tests

import (
	"testing"
)

// track is a timeline answer's track n.
func track(timeline map[string]any, n int) []any {
	return timeline["tracks"].([]any)[n].(map[string]any)["clips"].([]any)
}

func TestNewLineAndMakeShot(t *testing.T) {
	e := setup(t)
	e.voices()
	v := e.clipTake(userA, 4, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{v}}, 201)
	base, sessionID := "/v1/studio/timelines/"+tl["id"].(string), tl["session_id"].(string)
	vid := v1(tl)[0].(map[string]any)["id"].(string)
	// V1: the video, then an empty slot 4-8 s; an empty A2
	tracks := tl["tracks"].([]any)
	video := tracks[0].(map[string]any)
	video["clips"] = append(video["clips"].([]any), map[string]any{"id": "g", "take_id": nil, "hold": 4})
	tl = e.call(userA, "PUT", base, map[string]any{"revision": 1, "target": tl["target"],
		"tracks": append(tracks, map[string]any{"kind": "audio", "clips": []any{}})}, 200)

	line := map[string]any{"workflow_id": "echo-voice-over", "inputs": map[string]any{"prompt.text": "Back by noon.",
		"speak.voice": map[string]any{"voice_id": "echo/ada"}}}
	slot := map[string]any{"track": "A2", "at": 4.51, "length": 1, "id": "line1"}
	for want, body := range map[string]map[string]any{
		"New line needs a workflow whose media is audio": {"slot": slot, "run": map[string]any{"workflow_id": "echo-video",
			"inputs": map[string]any{"prompt.text": "a fox"}}},
		"unknown track A4":                          {"slot": with(slot, map[string]any{"track": "A4"}), "run": line},
		"unknown clip nope":                         {"slot": map[string]any{"clip": "nope"}, "run": line},
		"clip " + vid + " is not on an audio track": {"slot": map[string]any{"clip": vid}, "run": line},
		"New line needs an audio track":             {"slot": with(slot, map[string]any{"track": "V1"}), "run": line},
	} {
		if got := e.call(userA, "POST", base+"/lines", body, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}
	e.call(userB, "POST", base+"/lines", map[string]any{"slot": slot, "run": line}, 404)

	// the placeholder at the playhead, on a frame
	ans := e.call(userA, "POST", base+"/lines", map[string]any{"slot": slot, "run": line}, 202)
	runID := ans["run"].(map[string]any)["id"].(string)
	ph := track(ans["timeline"].(map[string]any), 1)[0].(map[string]any)
	if ph["id"] != "line1" || ph["take_id"] != nil || ph["at"] != 4.5 || ph["hold"] != float64(1) ||
		ph["shot"].(map[string]any)["run_id"] != runID || ans["timeline"].(map[string]any)["revision"] != float64(3) {
		t.Fatal(ph)
	}
	// overlapping it is a rule, nothing stored
	if got := e.call(userA, "POST", base+"/lines", map[string]any{"slot": with(slot, map[string]any{"id": "ov"}), "run": line}, 422); got["problems"] == nil {
		t.Fatal(got)
	}

	// the run's take fills it whole at its at
	e.relay()
	take := e.voiceTake(userA, sessionID, runID, 1.4)
	tl = e.call(userA, "GET", base, nil, 200)
	c := track(tl, 1)[0].(map[string]any)
	if c["take_id"] != take || c["in"] != float64(0) || c["out"] != 1.4 || c["at"] != 4.5 || c["shot"] != nil ||
		len(c["alternatives"].([]any)) != 1 || tl["takes"].(map[string]any)[take].(map[string]any)["audio"] == nil {
		t.Fatal(c)
	}
	if got := e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{take}}, 422); got["detail"] != "take_ids[0]: an audio take goes on an audio track" {
		t.Fatal(got)
	}

	// Make shot: Audio to video at the line's at and length, exact
	var a2v map[string]any
	for _, a := range e.call(userA, "GET", base+"/clips/line1/actions", nil, 200)["actions"].([]any) {
		if a.(map[string]any)["workflow_id"] == "echo-a2v" {
			a2v = a.(map[string]any)
		}
	}
	if a2v == nil || a2v["slot"].(map[string]any)["at"] != 4.5 || a2v["slot"].(map[string]any)["length"] != 1.4 ||
		a2v["slot"].(map[string]any)["exact"] != true {
		t.Fatal(a2v)
	}
	if got := e.call(userA, "GET", base+"/clips/g/actions", nil, 200)["actions"].([]any); len(got) != 0 {
		t.Fatal("an empty slot has none", got)
	}
	inputs := with(a2v["inputs"].(map[string]any), map[string]any{"prompt.text": "a line to see"})
	shot := e.call(userA, "POST", base+"/shots", map[string]any{"slot": with(a2v["slot"].(map[string]any), map[string]any{"id": "ms"}),
		"run": map[string]any{"workflow_id": "echo-a2v", "inputs": inputs}}, 202)
	clips := v1(shot["timeline"].(map[string]any))
	g, ms := clips[1].(map[string]any), clips[2].(map[string]any)
	if len(clips) != 4 || g["id"] != "g" || g["hold"] != 0.5 || ms["id"] != "ms" || ms["at"] != 4.5 || ms["shot"] == nil {
		t.Fatal("the gap splits around the slot", clips)
	}

	// Generate from this clip: the shot on the clip, its take kept; a second is 409; cancelled, the clip stays
	redo := e.call(userA, "POST", base+"/lines", map[string]any{"slot": map[string]any{"clip": "line1"}, "run": line}, 202)
	if c := track(redo["timeline"].(map[string]any), 1)[0].(map[string]any); c["take_id"] != take || c["shot"] == nil {
		t.Fatal(c)
	}
	if got := e.call(userA, "POST", base+"/lines", map[string]any{"slot": map[string]any{"clip": "line1"}, "run": line}, 409); got["detail"] != "clip line1 already has a line running" {
		t.Fatal(got)
	}
	e.call(userA, "POST", "/v1/studio/runs/"+redo["run"].(map[string]any)["id"].(string)+"/cancel", nil, 202)
	if c := track(e.call(userA, "GET", base, nil, 200), 1)[0].(map[string]any); c["take_id"] != take || c["shot"] != nil {
		t.Fatal("a redone clip loses only the shot", c)
	}

	// a cancelled line removes its placeholder
	gone := e.call(userA, "POST", base+"/lines", map[string]any{"slot": map[string]any{"track": "A2", "at": 0, "length": 1, "id": "gone"}, "run": line}, 202)
	e.call(userA, "POST", "/v1/studio/runs/"+gone["run"].(map[string]any)["id"].(string)+"/cancel", nil, 202)
	if a2 := track(e.call(userA, "GET", base, nil, 200), 1); len(a2) != 1 || a2[0].(map[string]any)["id"] != "line1" {
		t.Fatal("placeholder kept", a2)
	}
}
