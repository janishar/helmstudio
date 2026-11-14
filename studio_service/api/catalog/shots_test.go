package catalog

import (
	"testing"

	"github.com/helmstudio/studio_service/api/catalog/model"
)

// The shots brief comes from the item that gives a product its poses: links removed, one line; a later library-only
// item clears it.
func TestShotsBrief(t *testing.T) {
	brief := model.Item{Poses: map[string][]string{"rings": {"ring-packshot"}},
		Briefs: map[string]string{"rings": "one swing shot,\n like this https://example.com/a photo"}}
	inputs := model.Inputs{"poses": {Items: []model.Item{brief}}}
	if got := shotsBrief(inputs, "rings"); got == nil || *got != "one swing shot, like this photo" {
		t.Fatalf("shots brief: %v", got)
	}
	if got := shotsBrief(inputs, "necklaces"); got != nil {
		t.Fatalf("another product: %v", *got)
	}
	inputs["poses"] = model.StepInput{Items: []model.Item{brief, {Poses: map[string][]string{"rings": {"ring-packshot"}}}}}
	if got := shotsBrief(inputs, "rings"); got != nil {
		t.Fatalf("library-only choice: %v", *got)
	}
}

// A shot gets at most maxPoseRefs reference photos, in a stable order (the run command's references maxItems).
func TestPoseRefsAreCapped(t *testing.T) {
	st := &state{session: &model.Session{Inputs: model.Inputs{"poses": {Items: []model.Item{{PoseRefs: map[string]string{}}}}}}}
	for _, id := range []string{"f", "e", "d", "c", "b", "a"} {
		st.uploads = append(st.uploads, model.Upload{ID: id, Key: "k/" + id})
		st.session.Inputs["poses"].Items[0].PoseRefs[id] = "custom-ring-swing"
	}
	if got := poseRefs(st)["custom-ring-swing"]; len(got) != maxPoseRefs || got[0] != "k/a" || got[3] != "k/d" {
		t.Fatalf("pose refs: %v", got)
	}
}
