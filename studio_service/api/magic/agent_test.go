package magic

import (
	"fmt"
	"testing"
)

// The agent view (API 1.93): an asset is in use wherever lib/magic.ts usedIn says (refs, voice, first and last
// picks, a dialogue's speakers), takes newest first with pinned, and an asset's sheet with its views' names.
func TestAgentView(t *testing.T) {
	pin, voice := "t2", "echo/ada"
	d := Doc{Target: DefaultTarget, Lanes: []*Lane{{ID: "shots", Name: "Shots", Role: "shots"}},
		Assets: []*Asset{
			{ID: "mara", Kind: "character", Handle: "mara", Name: "Mara", Sheet: []string{"k1", "k2"},
				Views: map[string]string{"k1": "Front"}, VoiceID: &voice},
			{ID: "pier", Kind: "place", Handle: "pier", Name: "Pier", Sheet: []string{}},
			{ID: "boat", Kind: "prop", Handle: "boat", Name: "Boat", Sheet: []string{}},
			{ID: "gull", Kind: "prop", Handle: "gull", Name: "Gull", Sheet: []string{}},
		},
		Blocks: []*Block{
			{ID: "d1", Lane: "shots", Kind: "voice", Length: 24, Takes: []string{"t1", "t2", "t3"}, Pinned: &pin,
				Request: map[string]any{"kind": "voice", "turns": []any{map[string]any{"speaker": "mara", "text": "Hi."}}}},
			{ID: "s1", Lane: "shots", Kind: "video", At: 24, Length: 24, Takes: []string{},
				Request: map[string]any{"kind": "video", "first": map[string]any{"asset": "pier"}}},
			{ID: "s2", Lane: "shots", Kind: "video", At: 48, Length: 24, Takes: []string{"t4"},
				Request: map[string]any{"kind": "video", "last": map[string]any{"asset": "boat"},
					"refs": []any{map[string]any{"asset": "boat"}}}},
		}}
	v := AgentView(d.filled())
	used := map[string]string{}
	for _, a := range v.Assets {
		used[a.ID] = fmt.Sprint(a.UsedIn)
	}
	// mara only speaks a turn, pier is only a first frame, boat a last frame and a ref; gull is in nothing
	if fmt.Sprint(used) != "map[boat:[s2] gull:[] mara:[d1] pier:[s1]]" {
		t.Fatal(used)
	}
	if UsedIn(d, "gull") == nil {
		t.Fatal("used_in is [] when unused, never null")
	}
	b := v.Blocks[0]
	if fmt.Sprint(b.TakeIDs) != "[t3 t2 t1]" || !b.Pinned || *b.TakeID != "t2" || v.Blocks[1].Pinned ||
		fmt.Sprint(v.Blocks[1].TakeIDs) != "[]" || v.Blocks[2].Pinned || *v.Blocks[2].TakeID != "t4" {
		t.Fatal(v.Blocks)
	}
	if fmt.Sprint(d.Blocks[0].Takes) != "[t1 t2 t3]" {
		t.Fatal("the doc's takes are left as they were", d.Blocks[0].Takes)
	}
	m := v.Assets[0]
	if fmt.Sprintf("%+v", m.Sheet) != "[{TakeID:k1 View:Front} {TakeID:k2 View:}]" || m.VoiceID != voice || m.Note != "" {
		t.Fatal(m)
	}
}
