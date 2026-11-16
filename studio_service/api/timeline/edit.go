package timeline

import (
	"encoding/json"
	"slices"
)

// Op is one edit (contracts/README.md, Timeline rules: edits). Set holds the raw fields of a set or track op, so an
// absent field differs from null (null removes).
type Op struct {
	Op      string   `json:"op"`
	Clip    string   `json:"clip"`
	Index   *int     `json:"index"`
	At      *float64 `json:"at"`
	Track   string   `json:"track"`
	Edge    string   `json:"edge"`
	Delta   float64  `json:"delta"`
	ID      string   `json:"id"`
	TakeID  string   `json:"take_id"`
	TakeIDs []string `json:"take_ids"`
	IDs     []string `json:"ids"`
	Hold    float64  `json:"hold"`
	Set     map[string]json.RawMessage
}

// UnmarshalJSON decodes the op and keeps its raw fields.
func (o *Op) UnmarshalJSON(data []byte) error {
	type plain Op
	if err := json.Unmarshal(data, (*plain)(o)); err != nil {
		return err
	}
	return json.Unmarshal(data, &o.Set)
}

// Edit applies one edit to a normalized document (t_edit): the new document as written (normalize it next), or an
// error code when the edit cannot apply.
func Edit(doc Doc, op Op, takes map[string]Take) (Doc, string) {
	d, g := doc.Clone(), NewGrid(doc.Target)
	track := func(name string) *Track {
		for i := range d.Tracks {
			if d.Tracks[i].Name == name {
				return &d.Tracks[i]
			}
		}
		return nil
	}
	v1 := &d.Tracks[0].Clips
	din := func(i int) int {
		if 0 < i && i < len(*v1) {
			return (*v1)[i].din()
		}
		return 0
	}
	cutAt := func(i int) { // the cut before v1[i] changed: its dissolve goes
		if i < len(*v1) {
			(*v1)[i].TransitionIn = nil
		}
	}
	insert := func(clips *[]Clip, i int, c Clip) { *clips = slices.Insert(*clips, min(max(i, 0), len(*clips)), c) }
	switch op.Op {
	case "add":
		name := op.Track
		if name == "" {
			name = "V1"
		}
		t := track(name)
		if t == nil {
			return d, "unknown_track"
		}
		for i := range min(len(op.TakeIDs), len(op.IDs)) {
			tid, cid := op.TakeIDs[i], op.IDs[i]
			tk := takes[tid]
			switch {
			case t.Name != "V1": // at the first frame at or after the track's end (in samples): never an overlap
				e := 0
				for _, c := range t.Clips {
					e = max(e, g.Smp(val(c.At))+g.Smp(val(c.Out))-g.Smp(val(c.In)))
				}
				f := g.F(float64(e) / float64(g.sr))
				if g.Smp(g.S(f)) < e {
					f++
				}
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, In: f64(0), Out: f64(tk.DurationS), At: f64(g.S(f))})
			case tk.Kind == "image":
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, Hold: f64(ImageHoldS)})
			default:
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, In: f64(0), Out: f64(tk.DurationS)})
			}
		}
		return d, ""
	case "insert": // an empty slot at the V1 cut nearest the playhead (a tie: the later cut)
		cuts := []int{0}
		for _, c := range *v1 {
			cuts = append(cuts, cuts[len(cuts)-1]+Len(c, Kind(c, takes), g, false))
		}
		f, best := g.F(val(op.At)), 0
		for k := range cuts {
			if abs(cuts[k]-f) <= abs(cuts[best]-f) {
				best = k
			}
		}
		insert(v1, best, Clip{ID: op.ID, Hold: f64(op.Hold)})
		cutAt(best + 1)
		return d, ""
	case "add_track", "remove_track", "track":
		switch op.Op {
		case "add_track":
			if len(d.Tracks) > MaxAudioTracks {
				return d, "too_many_tracks"
			}
			d.Tracks = append(d.Tracks, Track{Kind: "audio", GainDB: f64(0), Mute: boolp(false), Clips: []Clip{}})
		case "remove_track":
			if op.Track == "V1" {
				return d, "video_track"
			}
			i := slices.IndexFunc(d.Tracks, func(t Track) bool { return t.Name == op.Track })
			if i < 0 {
				return d, "unknown_track"
			}
			d.Tracks = slices.Delete(d.Tracks, i, i+1)
		default:
			t := track(op.Track)
			if t == nil {
				return d, "unknown_track"
			}
			if raw, ok := op.Set["gain_db"]; ok {
				_ = json.Unmarshal(raw, &t.GainDB)
			}
			if raw, ok := op.Set["mute"]; ok {
				_ = json.Unmarshal(raw, &t.Mute)
			}
		}
		for i := range d.Tracks {
			d.Tracks[i].Name = "" // names follow the position
		}
		return d, ""
	case "split":
		t := track(op.Track)
		if t == nil {
			return d, "unknown_track"
		}
		return d, split(t, t == &d.Tracks[0], op, takes, g)
	}
	var t *Track
	i := -1
	for ti := range d.Tracks {
		if k := slices.IndexFunc(d.Tracks[ti].Clips, func(c Clip) bool { return c.ID == op.Clip }); k >= 0 {
			t, i = &d.Tracks[ti], k
			break
		}
	}
	if t == nil {
		return d, "unknown_clip"
	}
	c, onV1 := &t.Clips[i], t == &d.Tracks[0]
	kind := Kind(*c, takes)
	switch op.Op {
	case "move":
		if onV1 {
			moved := *c
			*v1 = slices.Delete(*v1, i, i+1)
			cutAt(i)
			moved.TransitionIn = nil
			index := 0
			if op.Index != nil {
				index = *op.Index
			}
			insert(v1, index, moved)
			cutAt(index + 1)
		} else {
			moved := *c
			moved.At = f64(val(op.At))
			t.Clips = slices.Delete(t.Clips, i, i+1)
			to := t
			if op.Track != "" {
				if to = track(op.Track); to == nil {
					return d, "unknown_track"
				}
			}
			to.Clips = append(to.Clips, moved)
		}
	case "trim":
		df := g.F(op.Delta)
		src, hasSrc := 0, false
		if tk, ok := takes[c.Take()]; ok && kind == "video" {
			src, hasSrc = g.F(tk.DurationS), true
		}
		if onV1 {
			dIn, dOut := din(i), din(i+1)
			least := max(1, dIn/2+dOut/2)
			if kind == "video" {
				in, out := g.F(val(c.In)), g.F(val(c.Out))
				if op.Edge == "in" {
					in = min(max(in+df, dIn/2), out-least)
				} else {
					out = max(out+df, in+least)
					if hasSrc {
						out = min(out, src-dOut/2)
					}
				}
				c.In, c.Out = f64(g.S(in)), f64(g.S(out))
			} else {
				if op.Edge != "out" {
					df = -df
				}
				c.Hold = f64(g.S(max(g.F(val(c.Hold))+df, least)))
			}
		} else {
			in, out, frame := g.Smp(val(c.In)), g.Smp(val(c.Out)), g.Smp(g.S(1))
			sr := float64(g.sr)
			if op.Edge == "in" {
				df = max(df, -g.F(val(c.At)))
				ni := min(max(in+g.Smp(g.S(df)), 0), out-frame)
				c.At, c.In = f64(g.Snap(val(c.At)+float64(ni-in)/sr)), f64(float64(ni)/sr)
			} else {
				no := max(out+g.Smp(g.S(df)), in+frame)
				if hasSrc {
					no = min(no, g.Smp(takes[c.Take()].DurationS))
				}
				c.Out = f64(float64(no) / sr)
			}
		}
	case "remove", "lift":
		if op.Op == "lift" && !onV1 {
			return d, "lift_track"
		}
		if op.Op == "lift" {
			*c = Clip{ID: c.ID, Hold: f64(g.S(Len(*c, kind, g, false)))}
			cutAt(i + 1)
		} else {
			t.Clips = slices.Delete(t.Clips, i, i+1)
			if onV1 {
				cutAt(i)
			}
		}
	case "replace":
		if !slices.Contains(c.Alternatives, op.TakeID) {
			return d, "not_alternative"
		}
		tk, ok := takes[op.TakeID]
		if !ok {
			return d, "take_not_found"
		}
		ln, oldIn := Len(*c, kind, g, !onV1), 0
		if kind == "video" {
			oldIn = g.F(val(c.In))
		}
		c.In, c.Out, c.Hold = nil, nil, nil
		if tk.Kind == "image" && onV1 {
			c.Hold = f64(g.S(ln))
		} else {
			src := g.F(tk.DurationS)
			in := min(oldIn, src-1)
			c.In, c.Out = f64(g.S(in)), f64(g.S(min(in+ln, src)))
		}
		tid := op.TakeID
		c.TakeID = &tid
	case "set":
		for key, raw := range op.Set {
			null := string(raw) == "null"
			switch key {
			case "gain_db":
				c.GainDB = nil
				if !null {
					_ = json.Unmarshal(raw, &c.GainDB)
				}
			case "mute":
				c.Mute = nil
				if !null {
					_ = json.Unmarshal(raw, &c.Mute)
				}
			case "hold":
				c.Hold = nil
				if !null {
					_ = json.Unmarshal(raw, &c.Hold)
				}
			case "transition_in":
				c.TransitionIn = nil
				if !null {
					_ = json.Unmarshal(raw, &c.TransitionIn)
				}
			}
		}
	case "fill": // studio: a shot's take fills its slot (README, Timeline: New shot)
		tk, ln := takes[op.TakeID], Len(*c, kind, g, false)
		c.In, c.Out, c.Hold, c.Shot = nil, nil, nil, nil
		if tk.Kind == "image" {
			c.Hold = f64(g.S(ln))
		} else {
			c.In, c.Out = f64(0), f64(g.S(min(g.F(tk.DurationS), ln)))
		}
		tid := op.TakeID
		c.TakeID = &tid
		c.Alternatives = append(slices.Clone(c.Alternatives), tid)
	default:
		return d, "unknown_op"
	}
	return d, ""
}

// split cuts the clip of t under the playhead (op.At, sequence seconds); the right part gets op.ID, keeps the take
// and alternatives, never the dissolve or the shot.
func split(t *Track, onV1 bool, op Op, takes map[string]Take, g Grid) string {
	f, k, p, ln := g.F(val(op.At)), -1, 0, 0
	for i, c := range t.Clips {
		a, l := g.F(val(c.At)), Len(c, Kind(c, takes), g, !onV1)
		if a <= f && f < a+l {
			k, p, ln = i, f-a, l
		}
	}
	switch {
	case k < 0:
		return "split_none"
	case p == 0:
		return "split_edge"
	case t.Clips[k].Shot != nil:
		return "split_shot"
	}
	c := &t.Clips[k]
	c2 := Doc{Tracks: []Track{{Clips: []Clip{*c}}}}.Clone().Tracks[0].Clips[0]
	c2.TransitionIn, c2.Shot, c2.ID = nil, nil, op.ID
	switch {
	case !onV1:
		// the left part ends on the right part's start sample
		cut := float64(g.Smp(g.S(f))-g.Smp(val(c.At))+g.Smp(val(c.In))) / float64(g.sr)
		c.Out, c2.In, c2.At = f64(cut), f64(cut), f64(g.S(f))
	case Kind(*c, takes) == "video":
		s := g.S(g.F(val(c.In)) + p)
		c.Out, c2.In = f64(s), f64(s)
	default:
		c.Hold, c2.Hold = f64(g.S(p)), f64(g.S(ln-p))
	}
	t.Clips = slices.Insert(t.Clips, k+1, c2)
	return ""
}

// Result is what Apply makes: the normalized document, its problems, or the edit's error code.
type Result struct {
	Document *Doc      `json:"document,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Apply is an edit, then normalize against the document before it (t_apply). fill then drops a dissolve the new
// take cannot carry and cuts an audio-track clip a shorter V1 now ends inside (removed when it starts at or after
// the end), so a fill never fails.
func Apply(doc Doc, op Op, takes map[string]Take) Result {
	raw, code := Edit(doc, op, takes)
	if code != "" {
		return Result{Error: code}
	}
	n, probs := Normalize(raw, takes, &doc)
	if op.Op == "fill" && len(probs) > 0 {
		g := NewGrid(raw.Target)
		bad, past := map[string]bool{}, map[string]bool{}
		for _, p := range probs {
			switch p.Code {
			case "dissolve_handles", "dissolve_length":
				bad[p.Clip] = true
			case "past_end":
				past[p.Clip] = true
			}
		}
		frames := 0
		for i, c := range raw.Tracks[0].Clips {
			if bad[c.ID] {
				raw.Tracks[0].Clips[i].TransitionIn = nil
			}
			frames += Len(c, Kind(c, takes), g, false)
		}
		end := g.Smp(g.S(frames))
		for ti := 1; ti < len(raw.Tracks); ti++ {
			t := &raw.Tracks[ti]
			t.Clips = slices.DeleteFunc(t.Clips, func(c Clip) bool { return past[c.ID] && g.Smp(val(c.At)) >= end })
			for i, c := range t.Clips {
				if past[c.ID] {
					t.Clips[i].Out = f64(float64(g.Smp(val(c.In))+end-g.Smp(val(c.At))) / float64(g.sr))
				}
			}
		}
		n, probs = Normalize(raw, takes, &doc)
	}
	return Result{Document: n, Problems: probs}
}

func abs(x int) int { return max(x, -x) }
