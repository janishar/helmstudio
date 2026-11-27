// Integration tests of M7 pack 3 step 2 (API 1.22, contracts/README.md "Media inputs: rows of media"): video and
// audio takes in MiniMax H3's reference rows checked as the caller (another user's take is not found), snapped and
// resolved to defs media; not_with and the optional last frame; "Use in…" offers only placed inputs.
package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// native publishes host mac's native runner with the minimax-h3 fixtures, idle with nothing resident.
func (e *env) native() {
	e.t.Helper()
	for _, kind := range []string{"nodes", "workflows", "models"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "minimax-h3."+kind+".valid.json"))
		if err == nil {
			err = e.registry.SetRegistry("mac.native."+kind, data)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	e.mac("native", nil)
}

func TestMediaRows(t *testing.T) {
	e := setup(t)
	e.native()
	video, foreign, long := e.clipTake(userA, 3, true, nil), e.clipTake(userB, 3, true, nil), e.clipTake(userA, 10, true, nil)
	audio, short, picture := e.audioTake(userA, 3), e.audioTake(userA, 1.5), e.image(userA)
	e.exec(`UPDATE takes SET bytes = 5 WHERE kind = 'video'`) // clipTake's objects are 5 bytes ("0.mp4")
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := func(user, workflow string, inputs map[string]any, want int) map[string]any {
		s := session
		if user != userA {
			s = e.call(user, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
		}
		return e.call(user, "POST", "/v1/studio/sessions/"+s+"/runs", map[string]any{"workflow_id": workflow, "inputs": inputs}, want)
	}
	ref := func(id string, extra ...any) map[string]any {
		out := map[string]any{"take_id": id}
		for i := 0; i < len(extra); i += 2 {
			out[extra[i].(string)] = extra[i+1]
		}
		return out
	}
	rows := func(field string, refs ...map[string]any) []any {
		var out []any
		for _, r := range refs {
			out = append(out, map[string]any{field: r})
		}
		return out
	}
	images := rows("image", ref(picture))
	for want, inputs := range map[string]map[string]any{
		"generate.videos[1].video: take " + foreign + " not found":                                  {"generate.videos": rows("video", ref(foreign))},
		"generate.audios[1].audio: take " + short + " lasts 1.5 s; this input takes at least 2.0 s": {"generate.refs": images, "generate.audios": rows("audio", ref(short))},
		"generate.videos[1].video: take " + audio + " is not a video":                               {"generate.videos": rows("video", ref(audio))},
		"generate.videos: the takes last 16.0 s in all; at most 15.0 s":                             {"generate.videos": rows("video", ref(long), ref(long, "in", 4.0))},
		"generate.audios[1].audio: the part lasts 1.0 s; this input takes at least 2.0 s":           {"generate.refs": images, "generate.audios": rows("audio", ref(audio, "in", 1.0, "out", 2.0))},
		"generate.videos[1].video: a take is required":                                              {"generate.videos": []any{map[string]any{"sound": "silent"}}},
	} {
		if got := run(userA, "minimax-h3-references", inputs, 422); got["detail"] != want {
			t.Errorf("%s: %v", want, got["detail"])
		}
	}
	// another user names A's take: not found to them, as everywhere
	if got := run(userB, "minimax-h3-references", map[string]any{"generate.videos": rows("video", ref(video))}, 422); got["detail"] != "generate.videos[1].video: take "+video+" not found" {
		t.Fatal(got)
	}
	// image to video: references while the first frame is wired are refused at once
	if got := run(userA, "minimax-h3-image-to-video", map[string]any{"first.image": ref(picture), "generate.refs": images}, 422); got["detail"] != "generate.refs: must stay empty while generate.first is wired" {
		t.Fatal(got)
	}

	// a references run: the rows snapped and filled, stored as checked, sent as defs media
	made := run(userA, "minimax-h3-references", map[string]any{"prompt.text": "<Video 1> and <Audio 1>", "generate.refs": images,
		"generate.lora":   "none", // the Turbo LoRA (the default) needs Turbo Ref, an add-on this host has not
		"generate.videos": []any{map[string]any{"video": ref(video, "in", 0.51), "sound": "silent"}},
		"generate.audios": rows("audio", ref(audio))}, 202)
	stored := e.stored(made["id"].(string))["generate"].Params
	if fmt.Sprint(stored["videos"]) != "[map[sound:silent video:map[in:0.5 out:3 take_id:"+video+"]]]" ||
		fmt.Sprint(stored["audios"]) != "[map[audio:map[in:0 out:3 take_id:"+audio+"]]]" {
		t.Fatal(stored)
	}
	sent := e.command()["generate"].Params
	v := sent["videos"].([]any)[0].(map[string]any)["video"].(map[string]any)
	a := sent["audios"].([]any)[0].(map[string]any)["audio"].(map[string]any)
	if v["kind"] != "video" || v["in"] != 0.5 || v["fps"] != float64(24) || !strings.HasPrefix(v["key"].(string), "u/"+userA+"/") ||
		a["kind"] != "audio" || a["out"] != float64(3) || a["etag"] != "e" {
		t.Fatal(sent)
	}
	// its take's lineage: every row's take
	runID := made["id"].(string)
	e.emit(userA, session, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	e.emit(userA, session, runID, "output", 2, videoOutput(userA, runID))
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	parents := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)["parents"].([]any)
	if len(parents) != 3 || !slices.Contains(parents, any(video)) || !slices.Contains(parents, any(audio)) || !slices.Contains(parents, any(picture)) {
		t.Fatal(parents)
	}

	// image to video without a last frame: the optional node dropped with its wire, never sent
	e.exec(`UPDATE runs SET status = 'succeeded', finished_at = now() WHERE status <> 'succeeded'`) // frees the host
	run(userA, "minimax-h3-image-to-video", map[string]any{"first.image": ref(picture)}, 202)
	nodes := e.command()
	if _, kept := nodes["last"]; kept || nodes["first"].Params == nil {
		t.Fatal(nodes)
	}

	// "Use in…": an image goes to Image to video's first frame and References, not Text to video
	var offered []string
	for _, u := range e.call(userA, "GET", "/v1/studio/takes/"+picture+"/uses", nil, 200)["uses"].([]any) {
		offer := u.(map[string]any)
		if offer["family"] == "minimax-h3" {
			offered = append(offered, offer["workflow_id"].(string)+":"+offer["param"].(string))
		}
	}
	if strings.Join(offered, " ") != "minimax-h3-image-to-video:first.image minimax-h3-references:generate.refs" {
		t.Fatal(offered)
	}
	if got := e.call(userA, "POST", "/v1/studio/takes/"+picture+"/use", map[string]any{"workflow_id": "minimax-h3-text-to-video"}, 422); got["detail"] != "workflow minimax-h3-text-to-video takes no image input" {
		t.Fatal(got)
	}
	e.call(userB, "GET", "/v1/studio/takes/"+picture+"/uses", nil, 404)
	e.call(userB, "POST", "/v1/studio/takes/"+picture+"/use", map[string]any{"workflow_id": "minimax-h3-references"}, 404)
}
