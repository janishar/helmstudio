package timeline

import (
	"math"
	"slices"
	"strings"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/registry"
)

// ShotSlot is a Make shot offer's slot (API 1.32, TakeOffer.slot): send it with POST …/shots.
type ShotSlot struct {
	At     float64 `json:"at"`
	Length float64 `json:"length"`
	Exact  bool    `json:"exact"`
}

// Action is a clip's take action: an offer, with (Make shot only) its slot.
type Action struct {
	gallerydto.TakeOffer
	Slot *ShotSlot `json:"slot,omitempty"`
}

// MakeShot is an audio-track clip's actions (make_shot): its take's offers into one media param accepting audio of a
// workflow whose media is video, every family, over rng (the take's own seconds; nil = the clip's in..out), an audio
// take snapped to whole ms and a video's sound to its frames, each with slot {at: the clip's at + (rng's start − in), length: the part,
// exact}. None for a V1 clip, a placeholder or an offline clip. takes: the clip's take facts (missing = offline).
func MakeShot(doc Doc, clip string, takes map[string]gallery.MediaTake, workflows []registry.Workflow, rng *[2]float64) []Action {
	out := []Action{}
	for ti, t := range doc.Tracks {
		k := slices.IndexFunc(t.Clips, func(c Clip) bool { return c.ID == clip })
		if k < 0 {
			continue
		}
		c := t.Clips[k]
		tk, ok := takes[c.Take()]
		if ti == 0 || !ok || c.Offline {
			return out
		}
		snap := func(x float64) float64 { return math.Round(x*1000) / 1000 }
		if tk.Kind != "audio" {
			snap = func(x float64) float64 { return math.Round(x*tk.FPS) / tk.FPS }
		}
		r := [2]float64{val(c.In), val(c.Out)}
		if rng != nil {
			r = *rng
		}
		at := val(c.At) + r[0] - val(c.In) // a selection starts the slot where its sound sits on the track
		r = [2]float64{snap(r[0]), snap(r[1])}
		var video []registry.Workflow
		for _, w := range workflows {
			if w.Media == "video" {
				video = append(video, w)
			}
		}
		byID := map[string]registry.Workflow{}
		for _, w := range video {
			byID[w.ID] = w
		}
		for _, o := range gallery.Offers(tk, video, &r) {
			v, isRef := o.Inputs[o.Param].(map[string]any)
			node, name, _ := strings.Cut(o.Param, ".")
			w := byID[o.WorkflowID]
			i := slices.IndexFunc(w.NodeSchemas[w.Nodes[node].Kind].Params, func(p registry.Param) bool { return p.Name == name })
			if !isRef || i < 0 || !slices.Contains(w.NodeSchemas[w.Nodes[node].Kind].Params[i].Accepts, "audio") {
				continue // the clip's sound drives the shot: never rows, a Retake or an Extend
			}
			in, _ := v["in"].(float64)
			end, _ := v["out"].(float64)
			out = append(out, Action{TakeOffer: o, Slot: &ShotSlot{At: at, Length: end - in, Exact: true}})
		}
		return out
	}
	return out
}
