package catalog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ora/catalog_service/api/catalog/model"
	"github.com/ora/catalog_service/registry"
)

// The Next step card walks the spec's rules: sample first, then the next category's sample, the next batch, run all,
// in progress, review, download.
func TestNextStep(t *testing.T) {
	var lib registry.Library
	var prices registry.Prices
	for name, into := range map[string]any{"catalog_library": &lib, "catalog_prices": &prices} {
		data, err := os.ReadFile("../../../contracts/fixtures/registry/" + name + ".valid.json")
		if err == nil {
			err = json.Unmarshal(data, into)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	resolution := "2K"
	sku := func(id, category, group string) model.Sku {
		return model.Sku{ID: id, Code: strings.ToUpper(id), Category: category, GroupKey: &group, GroupLabel: &group,
			Brief: model.Brief{Resolution: &resolution}}
	}
	skus := []model.Sku{sku("r1", "rings", "halo"), sku("r2", "rings", "halo"), sku("r3", "rings", "band"),
		sku("n1", "necklaces", "")}
	card := func(status ...string) (string, string, []string) {
		st := &state{session: &model.Session{Stage: "ready"}, skus: skus, status: map[string]string{}}
		for i, s := range status {
			st.status[skus[i].ID] = s
		}
		next := NextStep(st, &lib, &prices)
		var labels []string
		for _, option := range next.Options {
			labels = append(labels, option.Label+"|"+option.Action.Type+":"+option.Action.Scope+":"+option.Action.GroupKey)
		}
		return next.State, next.Line, labels
	}
	for _, test := range []struct {
		name   string
		status []string
		state  string
		want   string // the options, joined
		line   string // a part of the line
	}{
		{"nothing run", []string{"Not run", "Not run", "Not run", "Not run"}, "next",
			"Run sample · Rings|run:sample:,Run sample · necklaces|run:sample:", "sample"},
		{"sample done, groups left", []string{"To review", "Not run", "Not run", "Not run"}, "next",
			"Run next batch · halo|run:batch:halo,Run all Rings|run:all:", "in groups"},
		{"one group left", []string{"Approved", "Approved", "Not run", "Not run"}, "next",
			"Run all Rings|run:all:,Run sample · necklaces|run:sample:", "left to run"},
		{"generating", []string{"Running", "Not run", "Not run", "Not run"}, "in_progress", "", "Generating R1"},
		{"review", []string{"Approved", "Approved", "To review", "Redo"}, "review", "", "R3 is ready"},
		{"all approved", []string{"Approved", "Approved", "Approved", "Approved"}, "done",
			"Download approved images|download::", "ready"},
	} {
		state, line, labels := card(test.status...)
		if state != test.state || strings.Join(labels, ",") != test.want || !strings.Contains(line, test.line) {
			t.Errorf("%s: %s %q %v", test.name, state, line, labels)
		}
	}
	// A shot the main-image gate skipped points the brand to the main image, until the main image passes or is approved.
	gated := "Main image didn't pass review — fix or regenerate it, then run again"
	t0 := time.Now()
	main := model.Image{SkuID: "r1", Shot: "ring-packshot", Revision: 1, Status: "to_review", UpdatedAt: t0}
	st := &state{session: &model.Session{Stage: "ready"}, skus: skus,
		status: map[string]string{"r1": "Redo", "r2": "Approved", "r3": "Approved", "n1": "Approved"},
		images: []model.Image{main, {SkuID: "r1", Shot: "ring-mood", Revision: 1, Status: "failed", Error: &gated,
			UpdatedAt: t0.Add(time.Second)}}}
	if line := NextStep(st, &lib, &prices).Line; !strings.HasPrefix(line, "R1: main image hasn't passed review") {
		t.Errorf("gated: %q", line)
	}
	st.images[0] = model.Image{SkuID: "r1", Shot: "ring-packshot", Revision: 1, Status: "approved",
		UpdatedAt: t0.Add(2 * time.Second)}
	if line := NextStep(st, &lib, &prices).Line; strings.Contains(line, "main image") {
		t.Errorf("gated after the main image was approved: %q", line)
	}
	// After a Refresh: the new batch's ring (no brief yet) is not offered; the started ones still are, then Start.
	skus = append(skus, model.Sku{ID: "r4", Code: "R4", Category: "rings"})
	if _, _, labels := card("Approved", "Approved", "Not run", "Approved", "Not run"); strings.Join(labels, ",") != "Run all Rings|run:all:" {
		t.Errorf("refresh: %v", labels)
	}
	if state, line, _ := card("Approved", "Approved", "Approved", "Approved", "Not run"); state != "next" || !strings.Contains(line, "new batch") {
		t.Errorf("refresh, rest done: %s %q", state, line)
	}
}

// Products run in library order (family order, then product order) whatever order processing found them in, and a
// furniture SKU is briefed without a model.
func TestProductOrderAndBrief(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/fixtures/registry/catalog_library.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Set("catalog_library", data); err != nil {
		t.Fatal(err)
	}
	lib := registry.Current().Library
	skus := []model.Sku{{ID: "s1", Code: "SO-001", Category: "sofas"}, {ID: "r1", Code: "R-001", Category: "rings"}}
	sortSkus(skus)
	if skus[0].Code != "R-001" {
		t.Fatalf("order: %v", skus)
	}
	st := &state{session: &model.Session{Inputs: model.EmptyInputs()}, skus: skus,
		status: map[string]string{"s1": "Not run", "r1": "Not run"}}
	briefs := resolveBriefs(st, lib)
	if ring, sofa := briefs[0].Brief, briefs[1].Brief; ring.Model == nil || *ring.Model != "m001" || sofa.Model != nil ||
		*sofa.Background != "living-room" || strings.Join(sofa.Poses, ",") != "sofa-room" {
		t.Fatalf("briefs: %+v %+v", ring, sofa)
	}
	if lower("TV units") != "TV units" || singular("TV units") != "TV unit" || countNoun("Rings", 2) != "rings" {
		t.Fatal("nouns")
	}
}
