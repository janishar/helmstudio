package gallery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/helmstudio/studio_service/registry"
)

// asJSON is v through JSON (numbers by value, as the fixtures compare them).
func asJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// The ltx and qwen-image templates with their schemas, as check.py check_media reads them.
func mediaWorkflows(t *testing.T) []registry.Workflow {
	dir := filepath.Join("..", "..", "..", "contracts", "fixtures", "registry")
	var out []registry.Workflow
	for _, family := range []string{"ltx.", "qwen-image."} {
		var nodes struct{ Nodes []registry.NodeSchema }
		var workflows struct{ Workflows []registry.Workflow }
		for name, into := range map[string]any{"nodes": &nodes, "workflows": &workflows} {
			data, err := os.ReadFile(filepath.Join(dir, family+name+".valid.json"))
			if err == nil {
				err = json.Unmarshal(data, into)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		schemas := map[string]registry.NodeSchema{}
		for _, n := range nodes.Nodes {
			schemas[n.Kind] = n
		}
		for _, w := range workflows.Workflows {
			w.NodeSchemas = schemas
			out = append(out, w)
		}
	}
	return out
}

// contracts/fixtures/video/uses.json: "Use in…" offers and the media 422 texts (take_uses, media_fit).
func TestUsesFixtures(t *testing.T) {
	var fx struct {
		Takes map[string]MediaTake
		Cases []struct {
			Description string
			Take        string
			Range       *[2]float64
			Param       *registry.Param
			Expect      json.RawMessage
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "video", "uses.json"))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) == 0 {
		t.Fatal(err)
	}
	workflows := mediaWorkflows(t)
	for _, c := range fx.Cases {
		take := fx.Takes[c.Take]
		take.ID = c.Take
		var got any = map[string]any{"uses": Offers(take, workflows, c.Range)}
		if c.Param != nil {
			var fit any
			if text := MediaFit(*c.Param, take); text != "" {
				fit = text
			}
			got = map[string]any{"error": fit}
		}
		var want any
		_ = json.Unmarshal(c.Expect, &want)
		if g := asJSON(t, got); !reflect.DeepEqual(g, want) {
			raw, _ := json.Marshal(g)
			t.Errorf("%q:\n got  %s\n want %s", c.Description, raw, c.Expect)
		}
	}
}

// An audio upload's header: RIFF….WAVE, or an ID3v2 tag or an MPEG frame sync.
func TestCheckAudio(t *testing.T) {
	for _, c := range []struct {
		head, contentType, want string
	}{
		{"RIFF\x24\x00\x00\x00WAVEfmt ", "audio/wav", ""},
		{"RIFF\x24\x00\x00\x00AVI LIST", "audio/wav", "not a valid wav file"},
		{"ID3\x04\x00", "audio/mpeg", ""},
		{"\xff\xfb\x90\x64", "audio/mpeg", ""},
		{"\x89PNG", "audio/mpeg", "not a valid mp3 file"},
		{"", "audio/mpeg", "not a valid mp3 file"},
	} {
		if got := checkAudio([]byte(c.head), c.contentType); got != c.want {
			t.Errorf("%q: %q", c.head, got)
		}
	}
}
