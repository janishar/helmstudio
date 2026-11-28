package gallery

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/registry"
)

// Media inputs (contracts/README.md "Media inputs", API 1.19): a video or audio take as a workflow's input. The pure
// rules here are check.py's media_fit, extend_room and take_uses, tested on contracts/fixtures/video/uses.json.

// LtxMaxFrames is the longest clip retake, extend and audio to video make (ltx/duration's max).
const LtxMaxFrames = 321

// MediaTake is what the media rules read of a take.
type MediaTake struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       float64 `json:"fps"`
	Frames    int     `json:"frames"`
	DurationS float64 `json:"duration_s"`
	HasAudio  bool    `json:"has_audio"`
	Sound     string  `json:"sound,omitempty"` // API 1.79, an audio take's class (FactsOf; "" for Facts)
}

// Facts reads a take row's media facts: a video's from takes.video, an audio take's length from takes.audio.
func Facts(id, kind string, width, height *int, video, audio json.RawMessage) MediaTake {
	out := MediaTake{ID: id, Kind: kind, Width: deref(width), Height: deref(height)}
	var v dto.Video
	var a dto.AudioInfo
	switch kind {
	case "video":
		_ = json.Unmarshal(video, &v)
		out.FPS, out.Frames, out.DurationS, out.HasAudio = v.FPS, v.Frames, v.DurationS, v.HasAudio
	case "audio":
		_ = json.Unmarshal(audio, &a)
		out.DurationS, out.HasAudio = a.DurationS, true
	}
	return out
}

// FactsOf is Facts of a gallery row, with (API 1.79) an audio take's class (null: voice).
func FactsOf(row model.Take) MediaTake {
	out := Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)
	if row.Kind == "audio" {
		out.Sound = cmp.Or(deref(row.Sound), "voice")
	}
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// MediaFit is why a take does not fit a media param (the 422 text after `<node>.<param>: `), "" when it does. A trim
// part longer than max_s is the value's own 422; an offer caps it instead.
func MediaFit(p registry.Param, t MediaTake) string {
	picture := t.Kind == "video" && slices.Contains(p.Accepts, "video")
	sound := slices.Contains(p.Accepts, "audio") && (t.Kind == "audio" || t.Kind == "video") && t.HasAudio
	switch {
	case !picture && !sound && slices.Equal(p.Accepts, []string{"audio"}):
		return fmt.Sprintf("take %s has no sound", t.ID)
	case !picture && !sound:
		return fmt.Sprintf("take %s is not a video", t.ID)
	case picture && p.Grid > 0 && (t.Width%p.Grid != 0 || t.Height%p.Grid != 0):
		return fmt.Sprintf("take %s is %d × %d; this input needs sides on a %d px grid", t.ID, t.Width, t.Height, p.Grid)
	case picture && p.MaxFrames > 0 && t.Frames > p.MaxFrames:
		return fmt.Sprintf("take %s has %d frames; this input takes at most %d", t.ID, t.Frames, p.MaxFrames)
	case p.MinS > 0 && t.DurationS < p.MinS: // API 1.22; a trim cannot make it longer
		return fmt.Sprintf("take %s lasts %.1f s; this input takes at least %.1f s", t.ID, t.DurationS, p.MinS)
	case p.MaxS > 0 && t.DurationS > p.MaxS && !p.Trim:
		return fmt.Sprintf("take %s lasts %.1f s; this input takes at most %.1f s", t.ID, t.DurationS, p.MaxS)
	}
	return ""
}

// ltxFloor is a source's frames floored to LTX's 8k + 1 (k ≥ 1), as the old studio reads it.
func ltxFloor(n int) int { return 8*max(1, (n-1)/8) + 1 }

// ExtendRoom is ltx/extend's pre-fill for a video take (ok false: not offered): nothing (the default 2 s), or the most
// whole 8-frame steps under 321 frames as seconds (a 0.1 s step, rounded down); a range (a Timeline clip or selection,
// take seconds) gives the direction: after when it ends at the take's end (within one frame), else before when it
// starts at 0, else none.
func ExtendRoom(t MediaTake, rng *[2]float64) (map[string]any, bool) {
	n := ltxFloor(t.Frames)
	k := (LtxMaxFrames - n) / 8
	if k < 1 {
		return nil, false
	}
	out := map[string]any{}
	if n+8*int(math.Ceil(2*t.FPS/8)) > LtxMaxFrames {
		out["seconds"] = math.Floor(8*float64(k)/t.FPS*10) / 10
	}
	if rng != nil {
		switch {
		case t.DurationS-rng[1] < 1/t.FPS:
			out["direction"] = "after"
		case rng[0] < 0.5/t.FPS:
			out["direction"] = "before"
		default:
			return nil, false
		}
	}
	return out, true
}

// ByClass is the class rule of a media param (API 1.79, contracts/README.md, Music and sound: offers by class): a
// workflow with a sound class takes a take into it only as an audio take of that class, unless it accepts video.
func ByClass(class string, t MediaTake, p registry.Param) bool {
	return class == "" || slices.Contains(p.Accepts, "video") || t.Sound == class
}

// Offers is "Use in…" (take_uses): every workflow, in the order given, with exactly one input of the take's sort that
// counts (Counts) (an image: an image param or Image rows; a video or audio take: a media param
// whose accepts names its kind, or audio for a video with sound) that the take fits, with the inputs pre-filled. rng:
// (in, out) in the take's own seconds (a Timeline clip or selection), nil = the whole take. From API 1.79
// (contracts/README.md, Music and sound) a workflow with a sound class takes a take as sound into a media param only
// when it is an audio take of that class (a video's sound never; rows take any class). From API 1.107 a text
// workflow is never one.
func Offers(t MediaTake, workflows []registry.Workflow, rng *[2]float64) []dto.TakeOffer {
	out := []dto.TakeOffer{}
	for _, wf := range workflows {
		if wf.Media == "text" { // API 1.107: a text workflow (listen) answers, it makes no take: never a use
			continue
		}
		class := registry.WorkflowSound(wf.Nodes, wf.NodeSchemas)
		type at struct {
			node string
			p    registry.Param
		}
		var found []at
		for _, id := range slices.Sorted(maps.Keys(wf.Nodes)) {
			for _, p := range wf.NodeSchemas[wf.Nodes[id].Kind].Params {
				if !Counts(wf, id, p) {
					continue
				}
				_, image := p.ImageInput()
				_, field, rows := registry.MediaRowsField(p)
				if (t.Kind == "image" && image) || (t.Kind != "image" && p.Type == "media" && ByClass(class, t, p) &&
					(slices.Contains(p.Accepts, t.Kind) || (t.Kind == "video" && slices.Contains(p.Accepts, "audio")))) ||
					// API 1.32: rows of one media field, by the take's own kind only
					(t.Kind != "image" && rows && slices.Contains(field.Accepts, t.Kind)) {
					found = append(found, at{id, p})
				}
			}
		}
		if len(found) != 1 {
			continue
		}
		spec := found[0].p
		if _, field, rows := registry.MediaRowsField(spec); rows {
			spec = field
		}
		if t.Kind != "image" && MediaFit(spec, t) != "" {
			continue
		}
		p, path := found[0].p, found[0].node+"."+found[0].p.Name
		inputs, ok := mediaInputs(t, wf, p, path, rng)
		if !ok {
			continue
		}
		offerValuesBy(wf, inputs)
		out = append(out, dto.TakeOffer{WorkflowID: wf.ID, Title: wf.Title, Action: wf.Action, Family: wf.Family,
			Media: cmp.Or(wf.Media, "image"), Param: path, Inputs: inputs})
	}
	return out
}

// offerValuesBy is the offers rule (API 1.39, check.py offer_values_by): a pre-filled value that a cross-node
// values_by refuses under the workflow's value of the named param (Extend before on Distilled) also sets that param,
// to the first value of its enum (the node's only, else the schema's) that allows the pre-fill (model.variant dev).
func offerValuesBy(wf registry.Workflow, inputs map[string]any) {
	for _, id := range slices.Sorted(maps.Keys(wf.Nodes)) {
		for _, p := range wf.NodeSchemas[wf.Nodes[id].Kind].Params {
			pre, given := inputs[id+"."+p.Name]
			if p.ValuesBy == nil || !given {
				continue
			}
			on, name, cross := strings.Cut(p.ValuesBy.Param, ".")
			ref, exists := wf.Nodes[on]
			i := slices.IndexFunc(wf.NodeSchemas[ref.Kind].Params, func(q registry.Param) bool { return q.Name == name })
			if !cross || !exists || i < 0 {
				continue
			}
			spec := wf.NodeSchemas[ref.Kind].Params[i]
			allows := func(v string) bool {
				list, listed := p.ValuesBy.Values[v]
				return !listed || slices.Contains(list, pre)
			}
			current := cmp.Or[any](inputs[on+"."+name], ref.Params[name], spec.Default)
			if v, _ := current.(string); allows(v) {
				continue
			}
			pool := ref.Only[name]
			if pool == nil {
				pool = spec.Enum
			}
			if j := slices.IndexFunc(pool, allows); j >= 0 {
				inputs[on+"."+name] = pool[j]
			}
		}
	}
}

// Counts reports whether param p of node id is an input "Use in…" may fill (API 1.22): on a known node that is not
// bypassed or optional, not wired, not excluded by a wired not_with input, and placed by the workflow's studio block
// (none: every param).
func Counts(wf registry.Workflow, id string, p registry.Param) bool {
	n := wf.Nodes[id]
	_, known := wf.NodeSchemas[n.Kind]
	_, wired := n.In[p.Name]
	_, excluded := p.Excluded(n)
	if !known || n.Bypass || n.Optional || wired || excluded {
		return false
	}
	var layout struct {
		Composer, Quick []string
		Inspector       []struct{ Items []string }
	}
	if len(wf.Studio) == 0 || string(wf.Studio) == "null" || json.Unmarshal(wf.Studio, &layout) != nil {
		return true
	}
	placed := append(layout.Composer, layout.Quick...)
	for _, section := range layout.Inspector {
		placed = append(placed, section.Items...)
	}
	return slices.Contains(placed, id+"."+p.Name)
}

// mediaInputs puts the take into the input p at path: an image as {take_id} or rows' reference 1; a media value
// with its part (trim: in..min(out, in + max_s)), every span param over the range, an extend's room. ok false: the
// workflow is not offered (no room to extend).
func mediaInputs(t MediaTake, wf registry.Workflow, p registry.Param, path string, rng *[2]float64) (map[string]any, bool) {
	ref := map[string]any{"take_id": t.ID}
	switch field, _ := p.ImageInput(); {
	case p.Type == "image":
		return map[string]any{path: ref}, true
	case p.Type == "rows" && field != "":
		return map[string]any{path: []any{map[string]any{field: ref}}}, true
	}
	a, b := 0.0, t.DurationS
	if rng != nil {
		a, b = rng[0], rng[1]
	}
	if name, spec, rows := registry.MediaRowsField(p); rows { // API 1.32: the take as item 1
		n := b - a // the part offered (a Timeline range) must hold the field's min_s and max_s
		if spec.Trim {
			longest := math.Inf(1)
			if spec.MaxS > 0 {
				longest = spec.MaxS
			}
			if p.MaxTotalS > 0 {
				longest = min(longest, p.MaxTotalS)
			}
			ref["in"], ref["out"] = a, min(b, a+longest)
			n = min(b, a+longest) - a
		}
		if n < spec.MinS-1e-9 || (spec.MaxS > 0 && n > spec.MaxS+1e-9) {
			return nil, false
		}
		return map[string]any{path: []any{map[string]any{name: ref}}}, true
	}
	if p.Trim {
		end := b
		if p.MaxS > 0 {
			end = min(b, a+p.MaxS)
		}
		ref["in"], ref["out"] = a, end
	}
	inputs := map[string]any{path: ref}
	picture := t.Kind == "video" && slices.Contains(p.Accepts, "video")
	if !picture && (t.Kind != "audio" || !slices.Contains(p.Accepts, "audio")) { // API 1.79: a span on audio too
		return inputs, true
	}
	for _, id := range slices.Sorted(maps.Keys(wf.Nodes)) {
		for _, q := range wf.NodeSchemas[wf.Nodes[id].Kind].Params {
			if q.Type == "span" {
				inputs[id+"."+q.Name] = map[string]any{"start": a, "end": b}
			}
		}
		// ltx/extend, the fake echo/extend: the room is a video source's rule (API 1.79: never an audio extend's)
		if !picture || !strings.HasSuffix(wf.Nodes[id].Kind, "/extend") {
			continue
		}
		room, ok := ExtendRoom(t, rng)
		if !ok {
			return nil, false
		}
		for k, v := range room {
			inputs[id+"."+k] = v
		}
	}
	return inputs, true
}
