package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// voiceFixture reads contracts/fixtures/voices/<name>: workflows by name and the cases against them.
func voiceFixture[C any](t *testing.T, name string) (map[string]map[string]registry.Node, []C) {
	t.Helper()
	var fixture struct {
		Workflows map[string]struct {
			Nodes map[string]registry.Node `json:"nodes"`
		} `json:"workflows"`
		Cases []C `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "voices", name))
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil || len(fixture.Cases) < 5 {
		t.Fatal(err, len(fixture.Cases))
	}
	out := map[string]map[string]registry.Node{}
	for id, w := range fixture.Workflows {
		out[id] = w.Nodes
	}
	return out, fixture.Cases
}

func paletteSchemas(t *testing.T) map[string]registry.NodeSchema {
	schemas := map[string]registry.NodeSchema{}
	for kind, node := range fixturePalette(t) {
		schemas[kind] = node.NodeSchema
	}
	return schemas
}

// media.json: a graph's Workflow.media, audio included.
func TestVoiceMediaFixtures(t *testing.T) {
	schemas := paletteSchemas(t)
	workflows, cases := voiceFixture[struct {
		Description, Workflow, Expect string
	}](t, "media.json")
	for _, c := range cases {
		if got := workflowMedia(workflows[c.Workflow], schemas); got != c.Expect {
			t.Errorf("%s: got %s, want %s", c.Description, got, c.Expect)
		}
	}
}

// fit.json: a voice's fit (voice_fit), and looks and elements in an audio workflow (look_fit).
func TestVoiceFitFixtures(t *testing.T) {
	schemas := paletteSchemas(t)
	workflows, cases := voiceFixture[struct {
		Description, Workflow string
		Item                  json.RawMessage
		Expect                *string
	}](t, "fit.json")
	for _, c := range cases {
		var item dto.LibraryItem
		var look lookItem
		if json.Unmarshal(c.Item, &item) != nil || json.Unmarshal(c.Item, &look) != nil {
			t.Fatal(c.Description)
		}
		nodes := workflows[c.Workflow]
		got := lookFit(nodes, schemas, item.Kind, look, nil)
		if item.Kind == "voice" {
			got = voiceFit(nodes, schemas, voiceOf(item))
		}
		want := ""
		if c.Expect != nil {
			want = *c.Expect
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", c.Description, got, want)
		}
	}
}

// A designed voice's checks, in the contract's order (README, Voices: designed).
func TestVoiceChecks(t *testing.T) {
	text := func(s string) *string { return &s }
	body := func(edit func(*dto.LibraryItemCreate)) *dto.LibraryItemCreate {
		b := &dto.LibraryItemCreate{Kind: "voice", LibraryFields: dto.LibraryFields{Group: "designed", Name: " Teen ",
			Voice: &dto.VoiceDesign{Design: text(" male, 17 "), Language: text("en-US")}}}
		edit(b)
		return b
	}
	for want, b := range map[string]*dto.LibraryItemCreate{
		"group film is not a voice group":                  body(func(b *dto.LibraryItemCreate) { b.Group = "film" }),
		"group nonsense is not a voice group":              body(func(b *dto.LibraryItemCreate) { b.Group = "nonsense" }),
		"preset voices come from packs":                    body(func(b *dto.LibraryItemCreate) { b.Group = "preset" }),
		"cloned voices are not available yet":              body(func(b *dto.LibraryItemCreate) { b.Group = "cloned" }),
		"a voice has no handle":                            body(func(b *dto.LibraryItemCreate) { b.Handle = text("x") }),
		"a voice has no text; describe it in voice.design": body(func(b *dto.LibraryItemCreate) { b.Text = text("x") }),
		"images: a voice has no images":                    body(func(b *dto.LibraryItemCreate) { b.Images = []dto.LibraryImageRef{{}} }),
		"loras: a voice has no LoRAs":                      body(func(b *dto.LibraryItemCreate) { b.Loras = []dto.LookLoraRef{{}} }),
		"voice: a designed voice needs voice.design":       body(func(b *dto.LibraryItemCreate) { b.Voice.Design = nil }),
		"voice.design must be 1-2000 characters":           body(func(b *dto.LibraryItemCreate) { b.Voice.Design = text("  ") }),
		"voice.language: EN is not a BCP 47 language tag":  body(func(b *dto.LibraryItemCreate) { b.Voice.Language = text("EN") }),
		"voice.style must be at most 500 characters":       body(func(b *dto.LibraryItemCreate) { b.Voice.Style = text(string(make([]byte, 501))) }),
		"name must be 1-80 characters":                     body(func(b *dto.LibraryItemCreate) { b.Name = "" }),
		"voice: only a voice has voice fields":             body(func(b *dto.LibraryItemCreate) { b.Kind, b.Group = "look", "film" }),
		"voice.design must not contain control characters other than newlines and tabs": body(func(b *dto.LibraryItemCreate) {
			b.Voice.Design = text("a\x00b")
		}),
	} {
		if got := b.Validate(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	b := body(func(*dto.LibraryItemCreate) {})
	if got := b.Validate(); got != "" || b.Name != "Teen" || b.Voice.Designed() != (dto.VoiceInfo{Source: "designed",
		Design: "male, 17", Language: "en-US"}) {
		t.Fatal(got, b.Voice.Designed())
	}
}

// A voice value's own-voice UUID is taken in any case and lowercased; a starter id stays as it is.
func TestVoiceRef(t *testing.T) {
	for in, want := range map[string]any{
		"7D1E5C2A-9B3F-4E8D-A1C6-2F4B8E9D0A13": map[string]any{"voice_id": "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"},
		"echo/ada":                             map[string]any{"voice_id": "echo/ada"},
		"Echo/Ada":                             nil,
	} {
		got, _ := voiceRef(map[string]any{"voice_id": in})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
}
