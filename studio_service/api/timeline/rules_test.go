package timeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/helmstudio/studio_service/registry"
)

// fixtures is contracts/fixtures/timelines, or TIMELINE_FIXTURES (generated variants in the same shapes).
var fixtures = func() string {
	if d := os.Getenv("TIMELINE_FIXTURES"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "contracts", "fixtures", "timelines")
}()

// same compares two values as JSON (numbers by value, as the fixtures do).
func same(t *testing.T, got, want any) bool {
	t.Helper()
	norm := func(v any) any {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	return reflect.DeepEqual(norm(got), norm(want))
}

type fixtureCase struct {
	Description string          `json:"description"`
	Document    Doc             `json:"document"`
	Previous    *Doc            `json:"previous"`
	Takes       map[string]Take `json:"takes"`
	Op          Op              `json:"op"`
	Exclude     string          `json:"exclude"`
	T           *float64        `json:"t"`
	PxPerS      float64         `json:"px_per_s"`
	Clip        string          `json:"clip"`
	Mid         float64         `json:"mid"`
	Frame       int             `json:"frame"`
	Clips       []string        `json:"clips"`
	Workflow    struct {
		Nodes map[string]registry.Node `json:"nodes"`
	} `json:"workflow"`
	Expect json.RawMessage `json:"expect"`
}

func load(t *testing.T, name string) (map[string]Take, map[string]*Keyframes, []fixtureCase) {
	t.Helper()
	var fx struct {
		Takes     map[string]Take       `json:"takes"`
		Keyframes map[string]*Keyframes `json:"keyframes"`
		Cases     []fixtureCase         `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(fixtures, name))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil || len(fx.Cases) == 0 {
		t.Fatal(name, err)
	}
	return fx.Takes, fx.Keyframes, fx.Cases
}

func takesOf(common, own map[string]Take) map[string]Take {
	out := map[string]Take{}
	for k, v := range common {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

func check(t *testing.T, name string, c fixtureCase, got any) {
	t.Helper()
	var want any
	_ = json.Unmarshal(c.Expect, &want)
	if !same(t, got, want) {
		raw, _ := json.Marshal(got)
		t.Errorf("%s %q:\n got  %s\n want %s", name, c.Description, raw, c.Expect)
	}
}

func TestRates(t *testing.T) {
	var fx struct {
		Cases []struct {
			FPS, Seconds, Snapped float64
			Frames                int
		} `json:"cases"`
	}
	data, _ := os.ReadFile(filepath.Join(fixtures, "rates.json"))
	if err := json.Unmarshal(data, &fx); err != nil || len(fx.Cases) == 0 {
		t.Fatal(err)
	}
	for _, c := range fx.Cases {
		g := NewGrid(Target{FPS: c.FPS, SampleRate: 48000})
		if g.F(c.Seconds) != c.Frames || g.Snap(c.Seconds) != c.Snapped {
			t.Errorf("%v: got %d %v", c, g.F(c.Seconds), g.Snap(c.Seconds))
		}
	}
}

func TestNormalizeFixtures(t *testing.T) {
	common, _, cases := load(t, "normalize.json")
	for _, c := range cases {
		n, probs := Normalize(c.Document, takesOf(common, c.Takes), c.Previous)
		var got any = map[string]any{"problems": probs}
		if n != nil {
			got = map[string]any{"document": n}
			// writing the result back changes nothing
			if again, p := Normalize(*n, takesOf(common, c.Takes), n); p != nil || !same(t, again, n) {
				t.Errorf("%q: normalize is not idempotent: %v", c.Description, p)
			}
		}
		check(t, "normalize", c, got)
	}
}

func TestEditFixtures(t *testing.T) {
	common, _, cases := load(t, "edits.json")
	for _, c := range cases {
		r := Apply(c.Document, c.Op, takesOf(common, c.Takes))
		var got any
		switch {
		case r.Error != "":
			got = map[string]any{"error": r.Error}
		case r.Document != nil:
			got = map[string]any{"document": r.Document}
		default:
			got = map[string]any{"problems": r.Problems}
		}
		check(t, "edits", c, got)
	}
}

func TestSnapFixtures(t *testing.T) {
	common, _, cases := load(t, "snap.json")
	for _, c := range cases {
		takes := takesOf(common, c.Takes)
		var got any
		if c.T != nil {
			got = map[string]any{"to": SnapPoint(*c.T, Cuts(c.Document, takes, c.Exclude), c.PxPerS, NewGrid(c.Document.Target))}
		} else {
			got = map[string]any{"index": DropIndex(c.Document, c.Clip, c.Mid, takes)}
		}
		check(t, "snap", c, got)
	}
}

func TestFrameAtFixtures(t *testing.T) {
	common, _, cases := load(t, "frame-at.json")
	for _, c := range cases {
		check(t, "frame-at", c, FrameAt(c.Document, c.Frame, takesOf(common, c.Takes)))
	}
}

func TestPlanFixtures(t *testing.T) {
	common, _, cases := load(t, "plan.json")
	for _, c := range cases {
		check(t, "plan", c, ExportPlan(c.Document, takesOf(common, c.Takes)))
	}
}

func TestShotKeyframeFixtures(t *testing.T) {
	var palette struct {
		Groups []struct {
			Nodes []registry.PaletteNode `json:"nodes"`
		} `json:"groups"`
	}
	data, _ := os.ReadFile(filepath.Join(fixtures, "..", "graphs", "palette.json"))
	if err := json.Unmarshal(data, &palette); err != nil {
		t.Fatal(err)
	}
	schemas := map[string]registry.NodeSchema{}
	for _, g := range palette.Groups {
		for _, n := range g.Nodes {
			schemas[n.Kind] = n.NodeSchema
		}
	}
	_, _, cases := load(t, "shot-keyframes.json")
	for _, c := range cases {
		check(t, "shot-keyframes", c, map[string]any{"keyframes": ShotKeyframes(c.Workflow.Nodes, schemas)})
	}
}

func TestRerenderFixtures(t *testing.T) {
	common, keyframes, cases := load(t, "rerender.json")
	for _, c := range cases {
		steps, err := RerenderSteps(c.Document, takesOf(common, c.Takes), c.Clips, keyframes)
		var got any = map[string]any{"steps": steps}
		if err != nil {
			clip := any(err.Clip)
			if err.Clip == "" {
				clip = nil
			}
			got = map[string]any{"error": err.Code, "clip": clip}
		}
		check(t, "rerender", c, got)
	}
}
