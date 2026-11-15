package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// contracts/fixtures/video/keyframes.json: keyframe positions on the clip, as check.py position_errors.
func TestKeyframeFixtures(t *testing.T) {
	palette := fixturePalette(t)
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range palette {
		schemas[kind] = node.NodeSchema
	}
	var fx struct {
		Cases []struct {
			Description string          `json:"description"`
			Workflow    dto.WorkflowDoc `json:"workflow"`
			Errors      []string        `json:"errors"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(graphFixtures, "..", "video", "keyframes.json"))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) < 5 {
		t.Fatal(err)
	}
	for _, c := range fx.Cases {
		if got := positionErrors(c.Workflow.Nodes, schemas); !slices.Equal(got, c.Errors) {
			t.Errorf("%s: got %q, want %q", c.Description, got, c.Errors)
		}
	}
}

// The ltx templates' run rules: steps per role, auto length where declared, keyframe rows needed and placed, merged
// item defaults checked (contracts/README.md, Video).
func TestResolveVideoRules(t *testing.T) {
	i2v := ltxWorkflow(t, "ltx-image-to-video")
	row := `{"image": {"take_id": "` + takeA + `"}}`
	for body, want := range map[string]string{
		`{}`:                                  "keyframes.images: at least 1 items",
		`{"keyframes.images": []}`:            "keyframes.images: at least 1 items",
		`{"keyframes.images": [` + row + `]}`: "",
		`{"keyframes.images": [` + row + `], "duration.frames": "auto"}`:                                                    "",
		`{"keyframes.images": [` + row + `], "sample2.steps": 8}`:                                                           "sample2.steps: must be one of 3",
		`{"keyframes.images": [` + row + `], "sample1.steps": 5}`:                                                           "sample1.steps: must be one of 3, 8",
		`{"keyframes.images": [` + row + `], "sample1.role": "stage2"}`:                                                     "sample1.steps: must be one of 3",
		`{"keyframes.images": [` + row + `, {"image": {"take_id": "` + takeB + `"}, "seconds": 2.1}]}`:                      "keyframes.images[2].seconds: frame 50 is past the clip's last frame 48",
		`{"keyframes.images": [` + row + `, ` + row + `]}`:                                                                  "keyframes.images[2].seconds: frame 0 is already used by row 1",
		`{"keyframes.images": [` + row + `], "encode.beats": [{"seconds": 1}]}`:                                             "encode.beats[1].text: must be between 1 and 500 characters long",
		`{"keyframes.images": [` + row + `], "encode.beats": [{"text": "a fox runs"}], "decode.audio": false}`:              "",
		`{"keyframes.images": [` + row + `], "lora.rows": [{"lora": {"lora_id": "` + sixtyFour + `"}, "mode": "adapter"}]}`: "lora.rows[1].mode: must be one of bake",
		`{"keyframes.bypass": true}`: "",
	} {
		if _, detail := resolve(i2v, inputs(t, body)); detail != want {
			t.Errorf("%s: %q, want %q", body, detail, want)
		}
	}
	t2v := ltxWorkflow(t, "ltx-text-to-video")
	nodes, detail := resolve(t2v, inputs(t, `{"duration.frames": "auto"}`))
	if detail != "" || nodes["duration"].Params["frames"] != "auto" {
		t.Fatal(detail, nodes["duration"].Params)
	}
	clip := canonical(t, "", "fake.fake", "echo-video")
	if _, detail := resolve(clip, inputs(t, `{"duration.frames": "auto"}`)); detail != "" {
		t.Fatal(detail)
	}
	d := clip.NodeSchemas["echo/duration"] // the same param without auto length
	d.Params = slices.Clone(d.Params)
	d.Params[0].Auto = nil
	clip.NodeSchemas["echo/duration"] = d
	if _, detail := resolve(clip, inputs(t, `{"duration.frames": "auto"}`)); detail != "duration.frames: auto length is not offered" {
		t.Fatal(detail)
	}
}

const sixtyFour = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

// ltxWorkflow is a canonical ltx template with its node schemas.
func ltxWorkflow(t *testing.T, id string) registry.Workflow {
	return canonical(t, "ltx.", "mac.mlx", id)
}
