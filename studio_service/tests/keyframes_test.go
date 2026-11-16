// Integration tests of M4 stage B (contracts/README.md "Video"): image to video and keyframes (own image takes only,
// isolation through runs, saved workflows, batch rows and pipeline binds; descriptors at release; lineage), steps per
// role, auto length with the length the runner reports, and a LoRA already fused into the distilled weights.
package tests

import (
	"encoding/json"
	"strings"
	"testing"
)

// frame is a keyframe row of the take at seconds.
func frame(takeID string, seconds float64) map[string]any {
	return map[string]any{"image": map[string]any{"take_id": takeID}, "seconds": seconds}
}

func TestImageToVideo(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	first, last, theirs, clip := e.image(userA), e.image(userA), e.image(userB), e.image(userA, "video")
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	run := func(user string, inputs map[string]any, want int) map[string]any {
		return e.call(user, "POST", runs, map[string]any{"workflow_id": "ltx-image-to-video", "inputs": inputs}, want)
	}
	for detail, inputs := range map[string]map[string]any{
		"keyframes.images: at least 1 items":                                     {"prompt.text": "a fox"},
		"keyframes.images[2].image: take " + theirs + " not found":               {"keyframes.images": []any{frame(first, 0), frame(theirs, 2)}},
		"keyframes.images[1].image: take " + clip + " is not an image":           {"keyframes.images": []any{frame(clip, 0)}},
		"keyframes.images[2].seconds: frame 50 is past the clip's last frame 48": {"keyframes.images": []any{frame(first, 0), frame(last, 2.1)}},
		"keyframes.images[2].seconds: frame 0 is already used by row 1":          {"keyframes.images": []any{frame(first, 0), frame(last, 0.02)}},
		"sample2.steps: must be one of 3":                                        {"keyframes.images": []any{frame(first, 0)}, "sample2.steps": 8},
		"encode.beats[1].text: must be between 1 and 500 characters long":        {"keyframes.images": []any{frame(first, 0)}, "encode.beats": []any{map[string]any{"seconds": 1}}},
		"lora.rows[1].mode: must be one of bake":                                 {"keyframes.images": []any{frame(first, 0)}, "lora.rows": []any{map[string]any{"lora": map[string]any{"lora_id": strings.Repeat("c", 64)}, "mode": "adapter"}}},
	} {
		if got := run(userA, inputs, 422); got["detail"] != detail {
			t.Errorf("%v: %v, want %s", inputs, got["detail"], detail)
		}
	}
	// user B cannot pin A's take, even in B's own session
	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	if bad := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": "ltx-image-to-video",
		"inputs": map[string]any{"keyframes.images": []any{frame(first, 0)}}}, 422); bad["detail"] != "keyframes.images[1].image: take "+first+" not found" {
		t.Fatal(bad)
	}

	// first and last frame, auto length off; the command carries the descriptors, the beats and audio off
	made := run(userA, map[string]any{"prompt.text": "a fox runs", "keyframes.images": []any{frame(last, 49.0/24), frame(first, 0)},
		"encode.beats": []any{map[string]any{"text": "it stops"}}, "decode.audio": false}, 202)
	runID := made["id"].(string)
	_, sent := e.relay()
	if len(sent) != 1 {
		t.Fatal(sent)
	}
	var command struct {
		Workflow struct {
			Nodes map[string]struct{ Params map[string]any }
		}
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	rows := command.Workflow.Nodes["keyframes"].Params["images"].([]any)
	image := rows[1].(map[string]any)["image"].(map[string]any)
	if len(rows) != 2 || image["take_id"] != first || image["key"] != "u/"+userA+"/uploads/"+first+".png" ||
		rows[1].(map[string]any)["strength"] != float64(1) || command.Workflow.Nodes["decode"].Params["audio"] != false {
		t.Fatal(rows, command.Workflow.Nodes["decode"])
	}
	// the clip's parents are its keyframe images in row order
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	e.emit(userA, sessionID, runID, "output", 2, videoOutput(userA, runID))
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	if got, _ := json.Marshal(e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)["parents"]); string(got) != `["`+last+`","`+first+`"]` {
		t.Fatalf("parents %s", got)
	}

	// "Use in…" fills keyframe row 1; a saved workflow holds no take
	use := e.call(userA, "POST", "/v1/studio/takes/"+first+"/use", map[string]any{"workflow_id": "ltx-image-to-video"}, 200)
	if got, _ := json.Marshal(use["inputs"]); string(got) != `{"keyframes.images":[{"image":{"take_id":"`+first+`"}}]}` {
		t.Fatalf("%s", got)
	}
	e.call(userA, "POST", "/v1/studio/takes/"+first+"/use", map[string]any{"workflow_id": "ltx-text-to-video"}, 422)
	template := e.call(userA, "GET", "/v1/studio/workflows/ltx-image-to-video", nil, 200)
	doc := map[string]any{"title": "Pinned", "family": "ltx", "env": "mlx", "nodes": template["nodes"]}
	doc["nodes"].(map[string]any)["keyframes"].(map[string]any)["params"] = map[string]any{"images": []any{frame(theirs, 0)}}
	if bad := e.call(userA, "POST", "/v1/studio/workflows", doc, 422); !strings.Contains(bad["detail"].(string), "keyframes.images") {
		t.Fatal(bad)
	}
	doc["nodes"].(map[string]any)["keyframes"].(map[string]any)["params"] = map[string]any{"images": []any{}}
	saved := e.call(userA, "POST", "/v1/studio/workflows", doc, 201)
	if bad := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": saved["id"],
		"inputs": map[string]any{"keyframes.images": []any{frame(first, 0)}}}, 422); bad["detail"] != "unknown workflow "+saved["id"].(string) {
		t.Fatal(bad)
	}

	// batch rows over an image column, each with its own prompt (Gallery multi-select): B cannot use A's take
	batch := func(ids ...string) map[string]any {
		var cells []any
		for _, id := range ids {
			cells = append(cells, []any{[]any{frame(id, 0)}, "the prompt of " + id})
		}
		return map[string]any{"workflow_id": "ltx-image-to-video", "rows": map[string]any{"table": map[string]any{
			"columns": []string{"keyframes.images", "prompt.text"}, "rows": cells}}}
	}
	if bad := e.call(userB, "POST", "/v1/studio/batches", batch(theirs, first), 422); bad["detail"] != "row 2: keyframes.images[1].image: take "+first+" not found" {
		t.Fatal(bad)
	}
	e.submit(userA, batch(first, last))

	// a pipeline binding an Image input into the keyframes: B's rows cannot name A's take
	pipeline := e.call(userB, "POST", "/v1/studio/pipelines", fixturePipeline(t, "valid.ltx-first-frame.json"), 201)
	body := map[string]any{"pipeline_id": pipeline["id"], "inputs": map[string]any{"inputs.prompt": "a fox"},
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.first", "values": []string{theirs, first}}}}
	if bad := e.call(userB, "POST", "/v1/studio/batches", body, 422); !strings.Contains(bad["detail"].(string), first+" not found") {
		t.Fatal(bad)
	}
}

func TestAutoLength(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	runID, sessionID := e.start(userA, "ltx-text-to-video", map[string]any{"prompt.text": "a fox", "duration.frames": "auto"})
	_, sent := e.relay()
	if len(sent) != 1 || !strings.Contains(string(sent[0].Data), `"frames": "auto"`) {
		t.Fatal(sent)
	}
	output := videoOutput(userA, runID)
	output["video"].(map[string]any)["frames"] = 89
	output["resolved"] = map[string]any{"params": map[string]any{"duration.frames": 89}}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	e.emit(userA, sessionID, runID, "output", 2, output)
	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded",
		"resolved": map[string]any{"seed": 42, "params": map[string]any{"duration.frames": 89}}})
	run := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)
	if got, _ := json.Marshal(run["resolved"]); string(got) != `{"params":{"duration.frames":89},"seed":42}` {
		t.Fatalf("%s", got)
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+run["outputs"].([]any)[0].(map[string]any)["take_id"].(string), nil, 200)
	made := take["made_with"].(map[string]any)
	if made["inputs"].(map[string]any)["duration.frames"] != float64(89) || made["typed"].(map[string]any)["duration.frames"] != "auto" {
		t.Fatal(made)
	}
}

func TestPrefusedLora(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	distilled := "5d0c00000000000000000000000000000000000000000000000000000000a450" // ltx.models.valid.json prefused
	value, _ := json.Marshal(map[string]any{"loras": []any{map[string]any{"id": distilled, "name": "ltx-2.5-22b-distilled-lora-450",
		"family": "ltx", "rank": 450, "bytes": 8900000000, "key_style": "comfyui", "licence": nil, "modified_at": "2026-11-01T00:00:00Z"}}})
	if err := e.registry.SetRegistry("mac.mlx.loras", value); err != nil {
		t.Fatal(err)
	}
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	bad := e.call(userA, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "ltx-text-to-video",
		"inputs": map[string]any{"lora.rows": []any{map[string]any{"lora": map[string]any{"lora_id": distilled}}}}}, 422)
	if bad["detail"] != "lora.rows[1].lora: LoRA ltx-2.5-22b-distilled-lora-450 is already part of the distilled weights" {
		t.Fatal(bad)
	}
	if got := e.call(userA, "GET", "/v1/studio/loras/"+distilled, nil, 200); got["prefused"] != "LTX-2.5" { // the web offers no delete
		t.Fatal(got)
	}
}
