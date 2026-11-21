package studio

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
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
		Sends                 *string // 1.31: the defs voice source of the first param that takes it
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
		if c.Sends != nil {
			sent := ""
			for _, p := range voiceParams(nodes, schemas, nodes) {
				if voiceReason(nodes[p.Node].Kind, p.Param, voiceOf(item)) == "" {
					sent = sentSource(p.Param, voiceOf(item))
					break
				}
			}
			if sent != *c.Sends {
				t.Errorf("%s: sends %q, want %q", c.Description, sent, *c.Sends)
			}
		}
	}
}

// A voice's checks, in the contract's order (README, Voices: designed): the shape by Validate, the rest by saveVoice.
func TestVoiceChecks(t *testing.T) {
	text := func(s string) *string { return &s }
	body := func(edit func(*dto.LibraryItemCreate)) *dto.LibraryItemCreate {
		b := &dto.LibraryItemCreate{Kind: "voice", LibraryFields: dto.LibraryFields{Group: "designed", Name: " Teen ",
			Voice: &dto.VoiceDesign{Design: text(" male, 17 "), Language: text("en-US")}}}
		edit(b)
		return b
	}
	check := func(b *dto.LibraryItemCreate) (dto.VoiceInfo, string) {
		if detail := b.Validate(); detail != "" {
			return dto.VoiceInfo{}, detail
		}
		none := func(string) (*clipFacts, error) { return nil, nil }
		info, err := saveVoice(&b.LibraryFields, b.FromTake, none, func(string) (*voiceItem, error) { return nil, nil })
		if err != nil {
			return info, err.Error()
		}
		return info, ""
	}
	for want, b := range map[string]*dto.LibraryItemCreate{
		"group film is not a voice group":                  body(func(b *dto.LibraryItemCreate) { b.Group = "film" }),
		"group nonsense is not a voice group":              body(func(b *dto.LibraryItemCreate) { b.Group = "nonsense" }),
		"preset voices come from packs":                    body(func(b *dto.LibraryItemCreate) { b.Group = "preset" }),
		"voice.design: a cloned voice has no design":       body(func(b *dto.LibraryItemCreate) { b.Group = "cloned" }),
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
		"from_take makes looks and voices only": body(func(b *dto.LibraryItemCreate) {
			b.Kind, b.FromTake = "element", &dto.LibraryFromTake{TakeID: "2b3c4d5e-6f70-4a81-9b92-a3b4c5d6e7f8"}
		}),
		"from_take: a look takes the whole take (no in or out)": body(func(b *dto.LibraryItemCreate) {
			b.Kind, b.FromTake = "look", &dto.LibraryFromTake{TakeID: "2b3c4d5e-6f70-4a81-9b92-a3b4c5d6e7f8", In: new(float64)}
		}),
		"voice.design must not contain control characters other than newlines and tabs": body(func(b *dto.LibraryItemCreate) {
			b.Voice.Design = text("a\x00b")
		}),
	} {
		if _, got := check(b); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	b := body(func(*dto.LibraryItemCreate) {})
	if info, got := check(b); got != "" || b.Name != "Teen" || !reflect.DeepEqual(info, dto.VoiceInfo{Source: "designed",
		Design: "male, 17", Language: "en-US"}) {
		t.Fatal(got, info)
	}
}

// clone.json: cloned voices, Save as voice and the sweep's rule (check.py save_voice, sweep).
func TestVoiceCloneFixtures(t *testing.T) {
	var fixture struct {
		Takes map[string]struct {
			ID, Kind  string
			DurationS float64 `json:"duration_s"`
			HasAudio  bool    `json:"has_audio"`
			Voices    []gallerydto.MadeWithVoice
		}
		Voices map[string]dto.LibraryItem
		Cases  []struct {
			Description string
			Body        json.RawMessage
			Expect      struct {
				Status     int
				Detail     string
				Voice      json.RawMessage
				FromTakeID *string `json:"from_take_id"`
			}
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "voices", "clone.json"))
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil || len(fixture.Cases) < 20 {
		t.Fatal(err)
	}
	take := func(id string) (*clipFacts, error) {
		x, ok := fixture.Takes[id]
		if !ok {
			return nil, nil
		}
		return &clipFacts{Media: gallery.MediaTake{ID: x.ID, Kind: x.Kind, DurationS: x.DurationS,
			HasAudio: x.HasAudio || x.Kind == "audio"}, Voices: x.Voices}, nil
	}
	voice := func(id string) (*voiceItem, error) {
		if x, ok := fixture.Voices[id]; ok {
			item := voiceOf(x)
			return &item, nil
		}
		return nil, nil
	}
	for _, c := range fixture.Cases {
		var b dto.LibraryItemCreate
		if err := json.Unmarshal(c.Body, &b); err != nil {
			t.Fatal(c.Description, err)
		}
		status, detail, got := 0, b.Validate(), ""
		if detail != "" {
			status = 422
		} else if info, err := saveVoice(&b.LibraryFields, b.FromTake, take, voice); err != nil {
			var api *network.APIError
			errors.As(err, &api)
			status, detail = api.Code, api.Message
		} else {
			data, _ := json.Marshal(info)
			got = string(data)
			if (clipTake(&info) == nil) != (c.Expect.FromTakeID == nil) {
				t.Errorf("%s: from_take_id %v", c.Description, clipTake(&info))
			}
		}
		if c.Expect.Status != 0 {
			if status != c.Expect.Status || detail != c.Expect.Detail {
				t.Errorf("%s: got %d %q, want %d %q", c.Description, status, detail, c.Expect.Status, c.Expect.Detail)
			}
			continue
		}
		var want, have any
		_ = json.Unmarshal(c.Expect.Voice, &want)
		_ = json.Unmarshal([]byte(got), &have)
		if detail != "" || !reflect.DeepEqual(have, want) {
			t.Errorf("%s: got %s %q, want %s", c.Description, got, detail, c.Expect.Voice)
		}
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
