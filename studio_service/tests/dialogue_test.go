// Integration tests of dialogue (contracts/README.md "Dialogue and Timeline audio", API 1.32): an element's voice_id,
// a script expanded into lines at create time (the command's defs voice per line, made_with), and cues on the take.
package tests

import (
	"encoding/json"
	"testing"
)

func TestDialogue(t *testing.T) {
	e := setup(t)
	e.voices()
	img := e.image(userA)
	char := func(name, handle string, voice any) map[string]any {
		return map[string]any{"kind": "element", "group": "character", "name": name, "handle": handle,
			"images": []any{map[string]any{"take_id": img}}, "voice_id": voice}
	}
	mara := e.call(userA, "POST", library, char("Mara", "mara", "echo/ada"), 201)
	if mara["voice_id"] != "echo/ada" {
		t.Fatal(mara)
	}
	if got := e.call(userA, "POST", library, char("Jon", "jon", "echo/zed"), 404); got["detail"] != "voice_id: voice echo/zed not found" {
		t.Fatal(got)
	}
	prop := char("Cup", "cup", "echo/ada")
	prop["group"] = "prop"
	if got := e.call(userA, "POST", library, prop, 422); got["detail"] != "voice_id: only a character has a voice" {
		t.Fatal(got)
	}
	low := e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Low",
		"voice": map[string]any{"design": "low and slow"}}, 201)["id"].(string)
	jon := e.call(userA, "POST", library, char("Jon", "jon", low), 201)
	if got := e.call(userA, "GET", library+"/"+jon["id"].(string), nil, 200); got["voice_id"] != low {
		t.Fatal(got)
	}
	if got := e.call(userA, "GET", library+"/"+low, nil, 200); got["voice_id"] != nil {
		if _, has := got["voice_id"]; has {
			t.Fatal("a voice has no voice_id", got)
		}
	}

	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	run := func(script string, want int) map[string]any {
		return e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo-dialogue",
			"inputs": map[string]any{"dialogue.script": script}}, want)
	}
	if got := run("@mara: hi\n@ned: hey", 422); got["detail"] != "dialogue.script line 2: unknown element @ned" {
		t.Fatal(got)
	}
	runID := run("@mara (quiet): Noon.\n\n@jon: Closed.", 202)["id"].(string)
	nodes := e.command()
	lines, _ := json.Marshal(nodes["dialogue"].Params["lines"])
	want := `[{"pause_s":0.7,"style":"quiet","text":"Noon.","voice":{"family":"echo","item_id":"echo/ada","language":"en","source":"preset","speaker":"ada","version":1}},` +
		`{"pause_s":0,"style":"","text":"Closed.","voice":{"item_id":"` + low + `","source":"design","text":"low and slow","version":1}}]`
	if string(lines) != want || nodes["dialogue"].Params["script"] != "" {
		t.Fatalf("%s\n%s", lines, want)
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	cues := []any{map[string]any{"line": 1, "start_s": 0, "end_s": 0.333, "voice_id": "echo/ada", "text": "Noon."},
		map[string]any{"line": 2, "start_s": 1.0, "end_s": 1.5, "voice_id": low, "text": "Closed."}}
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.wav",
		"kind": "audio", "bytes": 96044, "audio": map[string]any{"duration_s": 1.5, "sample_rate": 24000, "cues": cues}})
	output := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)
	take := e.call(userA, "GET", "/v1/studio/takes/"+output["take_id"].(string), nil, 200)
	made := take["made_with"].(map[string]any)
	got, _ := json.Marshal(map[string]any{"cues": take["audio"].(map[string]any)["cues"], "elements": made["elements"],
		"voices": made["voices"], "script": made["typed"].(map[string]any)["dialogue.script"]})
	if wantMade, _ := json.Marshal(map[string]any{"cues": cues, "elements": []any{
		map[string]any{"id": mara["id"], "handle": "mara", "name": "Mara", "version": 1},
		map[string]any{"id": jon["id"], "handle": "jon", "name": "Jon", "version": 1}},
		"voices": []any{map[string]any{"id": "echo/ada", "name": "Ada", "source": "preset", "version": 1},
			map[string]any{"id": low, "name": "Low", "source": "designed", "version": 1}},
		"script": "@mara (quiet): Noon.\n\n@jon: Closed."}); string(got) != string(wantMade) {
		t.Fatalf("%s\n%s", got, wantMade)
	}

	// deleting the voice leaves the element reading null; its lines then have no voice
	e.call(userA, "DELETE", library+"/"+low, nil, 204)
	if got := e.call(userA, "GET", library+"/"+jon["id"].(string), nil, 200); got["voice_id"] != nil {
		t.Fatal(got)
	}
	if got := run("@jon: hi", 422); got["detail"] != "dialogue.script line 1: @jon has no voice" {
		t.Fatal(got)
	}
}
