package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/registry"
)

func h3(t *testing.T, id string) registry.Workflow {
	return canonical(t, "minimax-h3.", "mac.native", id)
}

// contracts/fixtures/video/media-rows.json (check.py media_rows): optional nodes dropped, not_with, each rows item's
// media field checked and snapped, max_total_s; resolve then mediaRules, as a run is created.
func TestMediaRowsFixtures(t *testing.T) {
	var fx struct {
		Takes map[string]gallery.MediaTake
		Cases []struct {
			Description, Workflow string
			Inputs                map[string]any
			Expect                struct {
				Error   *string
				Dropped []string
				Values  map[string][]map[string]any
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(graphFixtures, "..", "video", "media-rows.json"))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) == 0 {
		t.Fatal(err)
	}
	takes := map[string]gallery.MediaTake{}
	for id, take := range fx.Takes {
		take.ID = id
		takes[id] = take
	}
	for _, c := range fx.Cases {
		workflow := h3(t, c.Workflow)
		nodes, detail := resolve(workflow, c.Inputs)
		if detail == "" {
			detail = mediaRules(nodes, workflow.NodeSchemas, takes)
		}
		if c.Expect.Error != nil {
			if detail != *c.Expect.Error {
				t.Errorf("%q: got %q, want %q", c.Description, detail, *c.Expect.Error)
			}
			continue
		}
		if detail != "" {
			t.Errorf("%q: %s", c.Description, detail)
			continue
		}
		for id := range workflow.Nodes {
			_, kept := nodes[id]
			if dropped := slices.Contains(c.Expect.Dropped, id); kept == dropped {
				t.Errorf("%q: node %s kept %v", c.Description, id, kept)
			}
		}
		for _, node := range nodes {
			for port, ref := range node.In {
				if src, _, _ := strings.Cut(ref, "."); slices.Contains(c.Expect.Dropped, src) {
					t.Errorf("%q: %s still wired from %s", c.Description, port, ref)
				}
			}
		}
		for path, want := range c.Expect.Values { // the media fields as checked (other fields take their defaults)
			id, name, _ := strings.Cut(path, ".")
			got, _ := nodes[id].Params[name].([]any)
			field, _, _ := paramOfMust(workflow, path).MediaItem()
			if len(got) != len(want) {
				t.Errorf("%q: %s has %d items", c.Description, path, len(got))
				continue
			}
			for i := range want {
				g, w := asJSON(t, got[i].(map[string]any)[field]), asJSON(t, want[i][field])
				if !reflect.DeepEqual(g, w) {
					t.Errorf("%q: %s[%d]: got %v, want %v", c.Description, path, i+1, g, w)
				}
			}
		}
	}
}

func paramOfMust(workflow registry.Workflow, path string) registry.Param {
	p, _ := findParam(workflow, path)
	return p
}

// asJSON is v through JSON (numbers by value).
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

// A rows item without its media field, or with a malformed one, is refused while the inputs are checked.
func TestMediaRowsItemRequired(t *testing.T) {
	refs := h3(t, "minimax-h3-references")
	for body, want := range map[string]string{
		`{"generate.videos": [{"sound": "silent"}]}`:                                                             "generate.videos[1].video: a take is required",
		`{"generate.audios": [{"audio": null}]}`:                                                                 "generate.audios[1].audio: a take is required",
		`{"generate.videos": [{"video": {"take_id": "x"}}]}`:                                                     `generate.videos[1].video: must be {"take_id": <uuid>, "in"?: <seconds>, "out"?: <seconds>}`,
		`{"generate.videos": [{}, {}, {}, {}]}`:                                                                  "generate.videos: at most 3 items",
		`{"generate.videos": [{"video": {"take_id": "11111111-0000-4000-8000-000000000021"}, "sound": "loud"}]}`: "generate.videos[1].sound: must be one of keep, silent, replace",
	} {
		if _, detail := resolve(refs, inputs(t, body)); detail != want {
			t.Errorf("%s: got %q, want %q", body, detail, want)
		}
	}
	// a left-out sound takes its default
	nodes, detail := resolve(refs, inputs(t, `{"generate.videos": [{"video": {"take_id": "11111111-0000-4000-8000-000000000021"}}]}`))
	if detail != "" || nodes["generate"].Params["videos"].([]any)[0].(map[string]any)["sound"] != "keep" {
		t.Fatal(detail, nodes["generate"].Params["videos"])
	}
}

// Element mentions in image to video give the name and text only: refs stay empty, so the run is not refused.
func TestReferenceRowsNotWith(t *testing.T) {
	for id, want := range map[string]bool{"minimax-h3-image-to-video": false, "minimax-h3-references": true,
		"minimax-h3-text-to-video": true} {
		w := h3(t, id)
		nodes, detail := resolve(w, inputs(t, `{"first.image": {"take_id": "11111111-0000-4000-8000-000000000025"}}`))
		if id != "minimax-h3-image-to-video" {
			nodes, detail = resolve(w, nil)
		}
		if detail != "" {
			t.Fatal(id, detail)
		}
		if _, _, _, ok := referenceRows(nodes, w.NodeSchemas); ok != want {
			t.Errorf("%s: reference rows %v", id, ok)
		}
	}
}

// Lineage adds each rows item's media take (node, item, field order), and release resolves it to defs media.
func TestParentsOfMediaRows(t *testing.T) {
	graph := `{"generate": {"kind": "minimax-h3/generate", "params": {"refs": [{"image": {"take_id": "a"}}],
		"videos": [{"video": {"take_id": "v", "in": 0, "out": 2}, "sound": "keep"}], "audios": [{"audio": {"take_id": "s", "in": 1, "out": 3}}]}}}`
	if got := parents([]byte(graph), nil); !reflect.DeepEqual(got, []string{"s", "a", "v"}) {
		t.Fatal(got)
	}
}
