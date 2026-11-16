package timeline

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/helmstudio/studio_service/registry"
)

// Cuts are what an edge snaps to: 0 and every clip's start and end on every track, but the dragged clip's own.
func Cuts(doc Doc, takes map[string]Take, exclude string) []float64 {
	g, cuts := NewGrid(doc.Target), []float64{0}
	for ti, t := range doc.Tracks {
		for _, c := range t.Clips {
			if c.ID == exclude {
				continue
			}
			e := val(c.At) + val(c.Out) - val(c.In)
			if ti == 0 {
				e = g.S(g.F(val(c.At)) + Len(c, Kind(c, takes), g, false))
			}
			cuts = append(cuts, val(c.At), e)
		}
	}
	slices.Sort(cuts)
	return slices.Compact(cuts)
}

// SnapPoint is where a dragged edge lands: the nearest cut when within MagnetPx on screen (a tie: the earlier),
// else its frame.
func SnapPoint(t float64, cuts []float64, pxPerS float64, g Grid) float64 {
	if len(cuts) > 0 {
		best := cuts[0]
		for _, c := range cuts[1:] {
			if math.Abs(c-t) < math.Abs(best-t) {
				best = c
			}
		}
		if math.Abs(best-t)*pxPerS <= MagnetPx {
			return best
		}
	}
	return g.Snap(t)
}

// DropIndex is a moved V1 clip's index among the others: how many have their midpoint before mid (sequence seconds).
func DropIndex(doc Doc, clip string, mid float64, takes map[string]Take) int {
	g, n := NewGrid(doc.Target), 0
	m2 := 2 * g.F(mid)
	for _, c := range doc.Tracks[0].Clips {
		if c.ID != clip && 2*g.F(val(c.At))+Len(c, Kind(c, takes), g, false) < m2 {
			n++
		}
	}
	return n
}

// Picture is what the preview shows of a clip at a frame (T nil for an image).
type Picture struct {
	Clip    string   `json:"clip"`
	T       *float64 `json:"t"`
	Opacity float64  `json:"opacity"`
	Offline bool     `json:"offline,omitempty"`
}

// Sound is what the preview plays of a clip at a frame.
type Sound struct {
	Clip   string  `json:"clip"`
	T      float64 `json:"t"`
	GainDB float64 `json:"gain_db"`
	Fade   float64 `json:"fade"`
}

// Frame is the preview at one sequence frame: pictures bottom first, and sounds.
type Frame struct {
	Pictures []Picture `json:"pictures"`
	Sounds   []Sound   `json:"sounds"`
}

// FrameAt is what the preview shows and plays at sequence frame f (frame_at; the export applies the same gains).
func FrameAt(doc Doc, f int, takes map[string]Take) Frame {
	g, out := NewGrid(doc.Target), Frame{Pictures: []Picture{}, Sounds: []Sound{}}
	v1t := doc.Tracks[0]
	dins := make([]int, len(v1t.Clips)+1)
	for k, c := range v1t.Clips {
		dins[k] = c.din()
	}
	for k, c := range v1t.Clips {
		kind, a := Kind(c, takes), g.F(val(c.At))
		ln := Len(c, kind, g, false)
		start, end := a-dins[k]/2, a+ln+dins[k+1]/2
		if kind == "slot" || f < start || f >= end {
			continue
		}
		mixIn, mixOut := 1.0, 0.0
		if dins[k] != 0 && f < a+dins[k]/2 {
			mixIn = float64(f-start) / float64(dins[k])
		}
		if dins[k+1] != 0 && f >= a+ln-dins[k+1]/2 {
			mixOut = float64(f-(a+ln-dins[k+1]/2)) / float64(dins[k+1])
		}
		var src *float64
		if kind == "video" {
			src = f64(g.S(g.F(val(c.In)) + f - a))
		}
		pic := Picture{Clip: c.ID, T: src, Opacity: mixIn}
		if c.Offline {
			pic.Offline = true
			out.Pictures = append(out.Pictures, pic)
			continue
		}
		out.Pictures = append(out.Pictures, pic)
		if kind == "video" && takes[c.Take()].HasAudio && !*c.Mute && !*v1t.Mute {
			fade := 1 - mixOut
			if mixIn < 1 {
				fade = mixIn
			}
			out.Sounds = append(out.Sounds, Sound{Clip: c.ID, T: *src, GainDB: *v1t.GainDB + *c.GainDB, Fade: fade})
		}
	}
	for _, t := range doc.Tracks[1:] {
		for _, c := range t.Clips {
			a := g.F(val(c.At))
			if a <= f && f < a+Len(c, "video", g, true) && !c.Offline && !*c.Mute && !*t.Mute {
				out.Sounds = append(out.Sounds, Sound{Clip: c.ID, T: val(c.In) + g.S(f-a), GainDB: *t.GainDB + *c.GainDB, Fade: 1})
			}
		}
	}
	return out
}

// Reason is a code and the clip it is about ("" for none): why the picture is re-encoded, or why it cannot export.
type Reason struct{ Code, Clip string }

// MarshalJSON writes the reference's [code, clip] with null for "".
func (r Reason) MarshalJSON() ([]byte, error) {
	if r.Clip == "" {
		return json.Marshal([]any{r.Code, nil})
	}
	return json.Marshal([]any{r.Code, r.Clip})
}

// Plan is an export plan's rule part (export_plan).
type Plan struct {
	StreamCopy     bool     `json:"stream_copy"`
	Reasons        []Reason `json:"reasons"`
	Blocked        []Reason `json:"blocked"`
	VideoBitrate   *int     `json:"video_bitrate"`
	EstimatedBytes int      `json:"estimated_bytes"`
	Bitrate        int      `json:"-"` // the conform bitrate, also for a copy plan's fallback
}

func ratio(s string) (int, int) {
	a, b, _ := strings.Cut(s, "/")
	n, _ := strconv.Atoi(a)
	d, err := strconv.Atoi(b)
	if err != nil || d == 0 {
		d = 1
	}
	return n, d
}

// ExportPlan is the export plan of a normalized document: stream copy only when every V1 clip is a whole, probed
// H.264 take in MP4/MOV matching the target and the first clip; sound is always re-encoded.
func ExportPlan(doc Doc, takes map[string]Take) Plan {
	g, tg := NewGrid(doc.Target), doc.Target
	p := Plan{Reasons: []Reason{}, Blocked: []Reason{}}
	for _, t := range doc.Tracks {
		for _, c := range t.Clips {
			if c.Offline {
				p.Blocked = append(p.Blocked, Reason{"offline", c.ID})
			}
		}
	}
	if len(doc.Tracks[0].Clips) == 0 { // sound alone has no picture to export
		p.Blocked = append(p.Blocked, Reason{Code: "empty"})
	}
	var first *Stream
	why := func(code, cid string) { p.Reasons = append(p.Reasons, Reason{code, cid}) }
	for _, c := range doc.Tracks[0].Clips {
		cid := c.ID
		if c.Take() == "" {
			why("gap", cid)
			continue
		}
		tk, ok := takes[c.Take()]
		if !ok {
			continue
		}
		if tk.Kind == "image" {
			why("image", cid)
			continue
		}
		if c.TransitionIn != nil {
			why("transition", cid)
		}
		s := tk.Stream
		if s == nil {
			why("not_probed", cid)
			continue
		}
		fn, fd := ratio(s.FrameRate)
		sar, field := "", ""
		if s.SAR != nil {
			sar = *s.SAR
		}
		if s.FieldOrder != nil {
			field = *s.FieldOrder
		}
		for _, check := range []struct {
			code string
			bad  bool
		}{{"container", s.Container != "mp4" && s.Container != "mov"}, {"codec", s.Codec != "h264"},
			{"pixel_format", s.PixFmt != "yuv420p"}, {"size", s.Width != tg.Width || s.Height != tg.Height},
			{"aspect", s.SAR != nil && sar != "1:1"},
			{"field_order", s.FieldOrder != nil && field != "progressive" && field != "unknown"},
			{"frame_rate", fn*g.den != fd*g.num},
			{"trimmed", g.F(val(c.In)) != 0 || g.F(val(c.Out)) != g.F(tk.DurationS)}} {
			if check.bad {
				why(check.code, cid)
			}
		}
		if first == nil {
			first = s
			continue
		}
		for _, check := range []struct {
			code string
			bad  bool
		}{{"profile", s.Profile != first.Profile}, {"level", s.Level != first.Level},
			{"time_base", s.TimeBase != first.TimeBase}, {"colour", !sameColour(s.Color, first.Color)},
			{"parameter_sets", s.ExtradataSHA256 != first.ExtradataSHA256}} {
			if check.bad {
				why(check.code, cid)
			}
		}
	}
	p.StreamCopy = len(p.Reasons) == 0 && len(p.Blocked) == 0
	p.Bitrate = min(max(Rnd(float64(tg.Width*tg.Height)*float64(g.num)/float64(g.den)*0.15), 1_000_000), 40_000_000)
	dur := g.S(doc.Frames)
	if p.StreamCopy {
		sum := 0
		for _, c := range doc.Tracks[0].Clips {
			sum += int(takes[c.Take()].Bytes)
		}
		p.EstimatedBytes = sum + Rnd(192000*dur/8)
	} else {
		bitrate := p.Bitrate
		p.VideoBitrate = &bitrate
		p.EstimatedBytes = Rnd(float64(bitrate+192000) * dur / 8)
	}
	inputs, seen := int64(0), map[string]bool{}
	for _, t := range doc.Tracks {
		for _, c := range t.Clips {
			if tk, ok := takes[c.Take()]; ok && !seen[c.Take()] {
				seen[c.Take()], inputs = true, inputs+tk.Bytes
			}
		}
	}
	if dur > MaxExportS || p.EstimatedBytes > MaxExportBytes || inputs > MaxExportInputs {
		p.Blocked = append(p.Blocked, Reason{Code: "too_large"})
	}
	return p
}

// sameColour compares two colour tag sets by value (null tags included).
func sameColour(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Keyframes is Workflow.keyframes: the rows a New shot binds a first (and with Last, a last) frame into.
type Keyframes struct {
	Param   string `json:"param"`
	Image   string `json:"image"`
	Seconds string `json:"seconds"`
	Last    bool   `json:"last"`
}

// ShotKeyframes is which frames a New shot can bind (shot_keyframes): the workflow's one rows param, unwired on a
// known, not bypassed node, with one Image field and a `position: seconds` field; nil when there is not exactly one.
func ShotKeyframes(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) *Keyframes {
	var found []Keyframes
	for _, id := range sortedKeys(nodes) {
		n := nodes[id]
		s, ok := schemas[n.Kind]
		if !ok || n.Bypass {
			continue
		}
		for _, p := range s.Params {
			image, isImage := p.ImageInput()
			if !isImage || image == "" || p.Type != "rows" {
				continue
			}
			var pos []string
			for _, name := range sortedKeys(p.Item) {
				var field struct {
					Position string `json:"position"`
				}
				if json.Unmarshal(p.Item[name], &field) == nil && field.Position == "seconds" {
					pos = append(pos, name)
				}
			}
			if _, wired := n.In[p.Name]; len(pos) == 0 || wired {
				continue
			}
			found = append(found, Keyframes{Param: id + "." + p.Name, Image: image, Seconds: pos[0],
				Last: p.Max != nil && *p.Max >= 2})
		}
	}
	if len(found) != 1 {
		return nil
	}
	return &found[0]
}

// sortedKeys is m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Step is one re-render step: clip_<id>, its clip, and its binds (`<param>[<row>]` ← `clip_<other>.<at>_frame`).
type Step struct {
	Step  string            `json:"step"`
	Clip  string            `json:"clip"`
	Binds map[string]string `json:"binds"`
}

// RerenderError is why a re-render cannot be built: the code and the clip ("" for none).
type RerenderError struct{ Code, Clip string }

// RerenderSteps is re-render's order and binds (rerender_steps). clips nil = every V1 clip with a take; keyframes:
// workflow id → its ShotKeyframes (absent: none).
func RerenderSteps(doc Doc, takes map[string]Take, clips []string, keyframes map[string]*Keyframes) ([]Step, *RerenderError) {
	var v1 []Clip
	for _, c := range doc.Tracks[0].Clips {
		if (clips != nil && slices.Contains(clips, c.ID)) || (clips == nil && c.Take() != "") {
			v1 = append(v1, c)
		}
	}
	if len(v1) > MaxRerender {
		return nil, &RerenderError{Code: "too_many"}
	}
	for _, c := range v1 {
		tk, ok := takes[c.Take()]
		switch {
		case c.Take() == "":
			return nil, &RerenderError{"empty", c.ID}
		case !ok:
			return nil, &RerenderError{"offline", c.ID}
		case tk.MadeWith == nil:
			return nil, &RerenderError{"not_made", c.ID}
		}
	}
	owner := map[string]string{}
	for _, c := range v1 {
		for _, a := range c.Alternatives {
			owner[a] = c.ID
		}
	}
	deps, binds := map[string]map[string]bool{}, map[string]map[string]string{}
	for _, c := range v1 {
		deps[c.ID], binds[c.ID] = map[string]bool{}, map[string]string{}
	}
	for _, c := range v1 {
		mw := takes[c.Take()].MadeWith
		kf := keyframes[mw.WorkflowID]
		if kf == nil {
			continue
		}
		rows, _ := mw.Inputs[kf.Param].([]any)
		for r, row := range rows {
			item, _ := row.(map[string]any)
			ref, _ := item[kf.Image].(map[string]any)
			tid, _ := ref["take_id"].(string)
			fo := takes[tid].FrameOf
			if fo == nil {
				continue
			}
			if src := owner[fo.TakeID]; src != "" && src != c.ID {
				binds[c.ID][kf.Param+"["+strconv.Itoa(r+1)+"]"] = "clip_" + src + "." + fo.At + "_frame"
				deps[c.ID][src] = true
			}
		}
	}
	var order []string
	left := make([]string, 0, len(v1))
	for _, c := range v1 {
		left = append(left, c.ID)
	}
	done := map[string]bool{}
	for len(left) > 0 {
		i := slices.IndexFunc(left, func(x string) bool {
			for dep := range deps[x] {
				if !done[dep] {
					return false
				}
			}
			return true
		})
		if i < 0 {
			return nil, &RerenderError{"rerender_cycle", left[0]}
		}
		order, done[left[i]] = append(order, left[i]), true
		left = slices.Delete(left, i, i+1)
	}
	steps := []Step{}
	for _, x := range order {
		steps = append(steps, Step{Step: "clip_" + x, Clip: x, Binds: binds[x]})
	}
	return steps, nil
}
