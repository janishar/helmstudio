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

// The ltx, qwen-image, minimax-h3-image and minimax-h3 templates with their schemas, as check.py check_media reads them,
// or (API 1.79) the registries a case names ("" is echo's).
func mediaWorkflows(t *testing.T, families ...string) []registry.Workflow {
	dir := filepath.Join("..", "..", "..", "contracts", "fixtures", "registry")
	var out []registry.Workflow
	if families == nil {
		families = []string{"ltx.", "qwen-image.", "minimax-h3-image.", "minimax-h3."}
	}
	for _, family := range families {
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
			Registries  []string
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
		wfs := workflows
		if c.Registries != nil {
			wfs = mediaWorkflows(t, c.Registries...)
		}
		var got any = map[string]any{"uses": Offers(take, wfs, c.Range)}
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
		{"\x00\x00\x00\x18ftypM4A \x00\x00\x02\x00isomiso2", "audio/mp4", ""},
		{"\x00\x00\x00\x18ftypisom\x00\x00\x02\x00M4A mp42", "audio/mp4", ""},
		{"\x00\x00\x00\x18ftypisom\x00\x00\x02\x00avc1mp41", "audio/mp4", "not a valid m4a file"},
		{"\x00\x00\x00\x0cftypM4A ", "audio/mp4", "not a valid m4a file"},
		{"RIFF\x24\x00\x00\x00WAVEfmt ", "audio/mp4", "not a valid m4a file"},
	} {
		if got := checkAudio([]byte(c.head), c.contentType); got != c.want {
			t.Errorf("%q: %q", c.head, got)
		}
	}
}

// GET /uploads/limits (API 1.38): the largest Canvas width or height over the canonical registries (check.py
// upload_limits): 8192 with MiniMax H3 Image, 4096 with Qwen alone, the upload cap with none.
func TestUploadLimits(t *testing.T) {
	limits := func(prefixes ...string) int {
		var r registry.Registry
		_ = r.SetHost("mac.mlx", []byte(`{"host": "mac", "env": "mlx", "state": "idle", "updated_at": "2026-11-15T10:00:00Z"}`))
		var all []json.RawMessage // one host's nodes key holds every pack's
		for _, prefix := range prefixes {
			var nodes struct{ Nodes []json.RawMessage }
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry", prefix+"nodes.valid.json"))
			if err == nil {
				err = json.Unmarshal(data, &nodes)
			}
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, nodes.Nodes...)
		}
		data, _ := json.Marshal(map[string]any{"nodes": all})
		if err := r.SetRegistry("mac.mlx.nodes", data); err != nil {
			t.Fatal(err)
		}
		return (&Service{registry: &r}).UploadLimits().ImageMaxSide
	}
	if got := limits("qwen-image.", "ltx.", "minimax-h3.", "minimax-h3-image."); got != 8192 {
		t.Fatal(got)
	}
	if got := limits("qwen-image.", "ltx.", "minimax-h3."); got != 4096 {
		t.Fatal(got)
	}
	if got := limits(); got != MaxSide {
		t.Fatal(got)
	}
}

// The class rule offers and Use share (API 1.79): a classed workflow takes an audio take of its class only into a media
// param, never a video's sound; a param that accepts video takes the video; an unclassed workflow takes any.
func TestByClass(t *testing.T) {
	audio := registry.Param{Type: "media", Accepts: []string{"audio"}}
	video := registry.Param{Type: "media", Accepts: []string{"video", "audio"}}
	voice, music, clip := MediaTake{Kind: "audio", Sound: "voice"}, MediaTake{Kind: "audio", Sound: "music"}, MediaTake{Kind: "video"}
	for _, c := range []struct {
		class string
		take  MediaTake
		p     registry.Param
		want  bool
	}{
		{"music", music, audio, true}, {"music", voice, audio, false}, {"music", clip, audio, false},
		{"sound", clip, video, true}, {"", voice, audio, true}, {"", clip, audio, true},
	} {
		if got := ByClass(c.class, c.take, c.p); got != c.want {
			t.Errorf("ByClass(%q, %+v, %v) = %v", c.class, c.take, c.p.Accepts, got)
		}
	}
}
