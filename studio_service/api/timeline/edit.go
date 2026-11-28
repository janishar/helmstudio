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
	GapID   string   `json:"gap_id"`
	From    *float64 `json:"from"`   // 1.50: a range's start
	To      *float64 `json:"to"`     // 1.50: a range's end
	Ripple  bool     `json:"ripple"` // 1.50: remove on an audio track ripples it
	Name    *string  `json:"name"`   // 1.50: a marker's
	Color   *string  `json:"color"`  // 1.50: a marker's
	On      *bool    `json:"on"`     // 1.106: ducking's
	DB      *float64 `json:"db"`     // 1.106: ducking's
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
// error code when the edit cannot apply. From 1.50 linked sound clips follow their V1 clip (follow).
func Edit(doc Doc, op Op, takes map[string]Take) (Doc, string) {
	d, code := edit(doc, op, takes)
	if code == "" {
		code = follow(doc, &d, op, takes)
	}
	return d, code
}

// allTracks are the ops that cut and move every track themselves (1.50): follow moves no linked clip after them.
var allTracks = []string{"split_all", "remove_range", "lift_range"}

// follow is t_follow (1.50, linked sound clips): an audio-track clip whose V1 clip the edit removed goes too; one
// whose V1 clip moved moves by as many frames (its content's place: start − in for a video that stays a video, else
// its start), except after allTracks. Moved before 0: link_start.
func follow(before Doc, raw *Doc, op Op, takes map[string]Take) string {
	linked := false
	for _, t := range raw.Tracks[1:] {
		for _, c := range t.Clips {
			linked = linked || c.Link != nil
		}
	}
	if !linked {
		return ""
	}
	g := NewGrid(raw.Target)
	type anchor struct {
		kind           string
		start, content int
	}
	anchors := func(d Doc) map[string]anchor { // a field an edit removed counts 0 (normalize refuses it anyway)
		out, pos := map[string]anchor{}, 0
		for _, c := range d.Tracks[0].Clips {
			kind, in := Kind(c, takes), g.F(val(c.In))
			a := anchor{kind, pos, pos}
			if kind == "video" {
				a.content = pos - in
				pos += g.F(val(c.Out)) - in
			} else {
				pos += g.F(val(c.Hold))
			}
			out[c.ID] = a
		}
		return out
	}
	b, a := anchors(before), anchors(*raw)
	for ti := 1; ti < len(raw.Tracks); ti++ {
		t := &raw.Tracks[ti]
		keep := make([]Clip, 0, len(t.Clips))
		for _, c := range t.Clips {
			if c.Link != nil {
				was, inB := b[*c.Link]
				now, inA := a[*c.Link]
				if inB && !inA {
					continue
				}
				if inB && !slices.Contains(allTracks, op.Op) {
					sh := now.content - was.content
					if now.kind != was.kind {
						sh = now.start - was.start
					}
					if sh != 0 {
						f := g.F(val(c.At)) + sh
						if f < 0 {
							return "link_start"
						}
						c.At = new(g.S(f))
					}
				}
			}
			keep = append(keep, c)
		}
		t.Clips = keep
	}
	return ""
}

// v1Starts is V1's clip starts in frames and its end (the clips' At may be stale while an edit runs).
func v1Starts(clips []Clip, takes map[string]Take, g Grid) ([]int, int) {
	out, pos := make([]int, len(clips)), 0
	for i, c := range clips {
		out[i] = pos
		pos += Len(c, Kind(c, takes), g, false)
	}
	return out, pos
}

// grow moves a V1 clip's end by df frames: a video's out, else its hold.
func grow(c *Clip, kind string, df int, g Grid) {
	if kind == "video" {
		c.Out = new(g.S(g.F(val(c.Out)) + df))
	} else {
		c.Hold = new(g.S(g.F(val(c.Hold)) + df))
	}
}

// front moves a V1 clip's start df frames into it: a video's in, else its hold shrinks.
func front(c *Clip, kind string, df int, g Grid) {
	if kind == "video" {
		c.In = new(g.S(g.F(val(c.In)) + df))
	} else {
		c.Hold = new(g.S(g.F(val(c.Hold)) - df))
	}
}

// inside is the clip of t strictly around frame f: its index (-1: none), start and length.
func inside(t *Track, f int, onV1 bool, takes map[string]Take, g Grid) (k, a, ln int) {
	k = -1
	var starts []int
	if onV1 {
		starts, _ = v1Starts(t.Clips, takes, g)
	}
	for i, c := range t.Clips {
		s := g.F(val(c.At))
		if onV1 {
			s = starts[i]
		}
		if l := Len(c, Kind(c, takes), g, !onV1); s < f && f < s+l {
			k, a, ln = i, s, l
		}
	}
	return k, a, ln
}

// cut cuts clip k of t (start a, length ln frames) at frame f: the right part, inserted after it, gets id, keeps the
// take, alternatives and link, never the dissolve or the shot.
func cut(t *Track, k, f, a, ln int, id string, onV1 bool, takes map[string]Take, g Grid) {
	c := &t.Clips[k]
	c2 := Doc{Tracks: []Track{{Clips: []Clip{*c}}}}.Clone().Tracks[0].Clips[0]
	c2.TransitionIn, c2.Shot, c2.ID = nil, nil, id
	switch {
	case !onV1:
		// the left part ends on the right part's start sample
		s := float64(g.Smp(g.S(f))-g.Smp(val(c.At))+g.Smp(val(c.In))) / float64(g.sr)
		c.Out, c2.In, c2.At = new(s), new(s), new(g.S(f))
	case Kind(*c, takes) == "video":
		s := g.S(g.F(val(c.In)) + f - a)
		c.Out, c2.In = new(s), new(s)
	default:
		c.Hold, c2.Hold = new(g.S(f-a)), new(g.S(ln-f+a))
	}
	t.Clips = slices.Insert(t.Clips, k+1, c2)
}

// relink is t_relink (1.50): after V1 clips were cut (root: each new part's id → the clip it was cut from), a sound
// clip linked to one of the parts links to the part it starts in (the last starting at or before it; else the first).
func relink(d *Doc, root map[string]string, takes map[string]Take, g Grid) {
	if len(root) == 0 {
		return
	}
	rootOf := func(id string) string {
		if r, ok := root[id]; ok {
			return r
		}
		return id
	}
	type part struct {
		a  int
		id string
	}
	parts := map[string][]part{}
	starts, _ := v1Starts(d.Tracks[0].Clips, takes, g)
	for i, c := range d.Tracks[0].Clips {
		r := rootOf(c.ID)
		parts[r] = append(parts[r], part{starts[i], c.ID})
	}
	for ti := 1; ti < len(d.Tracks); ti++ {
		for ci := range d.Tracks[ti].Clips {
			c := &d.Tracks[ti].Clips[ci]
			if c.Link == nil || len(parts[rootOf(*c.Link)]) == 0 {
				continue
			}
			ps := parts[rootOf(*c.Link)]
			best := ps[0].id
			for _, p := range ps {
				if p.a <= g.F(val(c.At)) {
					best = p.id
				}
			}
			c.Link = &best
		}
	}
}

// edit is Edit's op itself (t_edit_op).
func edit(doc Doc, op Op, takes map[string]Take) (Doc, string) {
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
					e = max(e, g.Smp(val(c.At))+aLen(c, g))
				}
				f := g.F(float64(e) / float64(g.sr))
				if g.Smp(g.S(f)) < e {
					f++
				}
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, In: new(0.0), Out: new(tk.DurationS), At: new(g.S(f))})
			case tk.Kind == "image":
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, Hold: new(ImageHoldS)})
			default:
				t.Clips = append(t.Clips, Clip{ID: cid, TakeID: &tid, In: new(0.0), Out: new(tk.DurationS)})
			}
		}
		return d, ""
	case "insert", "place":
		cuts := []int{0}
		for _, c := range *v1 {
			cuts = append(cuts, cuts[len(cuts)-1]+Len(c, Kind(c, takes), g, false))
		}
		if op.Op == "place" { // 1.32, Make shot: an empty slot at exactly frames(at)
			f, n := g.F(val(op.At)), g.F(op.Hold)
			last := cuts[len(cuts)-1]
			slot := Clip{ID: op.ID, Hold: new(g.S(n))}
			if f >= last { // past V1's end: a gap up to it first
				if f > last {
					*v1 = append(*v1, Clip{ID: op.GapID, Hold: new(g.S(f - last))})
				}
				*v1 = append(*v1, slot)
				return d, ""
			}
			k := 0
			for i := range *v1 {
				if cuts[i] <= f {
					k = i
				}
			}
			gap := (*v1)[k]
			if gap.Take() == "" && gap.Shot == nil { // inside an empty slot: split it around the new slot
				left, right := f-cuts[k], cuts[k+1]-f-n
				var parts []Clip
				if left > 0 {
					gap.Hold = new(g.S(left))
					parts = append(parts, gap)
				}
				parts = append(parts, slot)
				if right > 0 {
					id := gap.ID
					if left > 0 {
						id = op.GapID
					}
					parts = append(parts, Clip{ID: id, Hold: new(g.S(right))})
				}
				*v1 = slices.Replace(*v1, k, k+1, parts...)
				cutAt(k + len(parts))
				if right < 0 { // longer than the gap: an insert of what overflows, every track moving on together
					for ti := 1; ti < len(d.Tracks); ti++ {
						for i, c := range d.Tracks[ti].Clips {
							if g.F(val(c.At)) >= cuts[k+1] {
								d.Tracks[ti].Clips[i].At = new(g.S(g.F(val(c.At)) - right))
							}
						}
					}
				}
				return d, ""
			}
			op.Hold = g.S(n) // inside a take's clip or a running shot: the insert edit
		}
		// insert: an empty slot at the V1 cut nearest the playhead (a tie: the later cut)
		f, best := g.F(val(op.At)), 0
		for k := range cuts {
			if abs(cuts[k]-f) <= abs(cuts[best]-f) {
				best = k
			}
		}
		insert(v1, best, Clip{ID: op.ID, Hold: new(op.Hold)})
		cutAt(best + 1)
		return d, ""
	case "add_track", "remove_track", "track":
		switch op.Op {
		case "add_track":
			if len(d.Tracks) > MaxAudioTracks {
				return d, "too_many_tracks"
			}
			d.Tracks = append(d.Tracks, Track{Kind: "audio", GainDB: new(0.0), Mute: new(false), Clips: []Clip{}})
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
		return d, split(&d, t, t == &d.Tracks[0], op, takes, g)
	case "split_all": // 1.50: blade on every track at frame At
		f, ids, root, made := g.F(val(op.At)), op.IDs, map[string]string{}, false
		for ti := range d.Tracks {
			t := &d.Tracks[ti]
			k, a, ln := inside(t, f, ti == 0, takes, g)
			if k < 0 || t.Clips[k].Shot != nil {
				continue
			}
			if len(ids) == 0 {
				return d, "ids_short"
			}
			cut(t, k, f, a, ln, ids[0], ti == 0, takes, g)
			if ti == 0 {
				root[ids[0]] = t.Clips[k].ID
			}
			ids, made = ids[1:], true
		}
		if !made {
			return d, "split_none"
		}
		relink(&d, root, takes, g)
		return d, ""
	case "remove_range", "lift_range": // 1.50: a range on every track, cut at both ends
		lo, hi := g.F(val(op.From)), g.F(val(op.To))
		if hi <= lo {
			return d, "range_empty"
		}
		for ti, t := range d.Tracks {
			starts, _ := v1Starts(t.Clips, takes, g)
			for k, c := range t.Clips {
				a := starts[k]
				if ti > 0 {
					a = g.F(val(c.At))
				}
				if c.Shot != nil && a < hi && a+Len(c, Kind(c, takes), g, ti > 0) > lo {
					return d, "range_shot"
				}
			}
		}
		ids, root := op.IDs, map[string]string{}
		for ti := range d.Tracks {
			t := &d.Tracks[ti]
			for _, f := range []int{lo, hi} {
				k, a, ln := inside(t, f, ti == 0, takes, g)
				if k < 0 {
					continue
				}
				if len(ids) == 0 {
					return d, "ids_short"
				}
				cut(t, k, f, a, ln, ids[0], ti == 0, takes, g)
				if ti == 0 {
					r := t.Clips[k].ID
					if rr, ok := root[r]; ok {
						r = rr
					}
					root[ids[0]] = r
				}
				ids = ids[1:]
			}
		}
		relink(&d, root, takes, g)
		starts, end := v1Starts(*v1, takes, g)
		first, last := -1, -1
		for k, a := range starts {
			if lo <= a && a < hi {
				if first < 0 {
					first = k
				}
				last = k
			}
		}
		if first >= 0 {
			*v1 = slices.Delete(*v1, first, last+1)
			next := first
			if op.Op == "lift_range" {
				if len(ids) == 0 {
					return d, "ids_short"
				}
				insert(v1, first, Clip{ID: ids[0], Hold: new(g.S(min(hi, end) - lo))})
				next++
			}
			cutAt(next)
		}
		for ti := 1; ti < len(d.Tracks); ti++ {
			t := &d.Tracks[ti]
			keep := make([]Clip, 0, len(t.Clips))
			for _, c := range t.Clips {
				a := g.F(val(c.At))
				if lo <= a && a < hi {
					continue
				}
				if a >= hi && op.Op == "remove_range" { // open point 4: audio tracks ripple too
					c.At = new(g.S(a - (hi - lo)))
				}
				keep = append(keep, c)
			}
			t.Clips = keep
		}
		return d, ""
	case "marker": // 1.50: a marker put (a new id adds one; a known id is replaced whole)
		d.Markers = append(slices.DeleteFunc(slices.Clone(d.Markers), func(m Marker) bool { return m.ID == op.ID }),
			Marker{ID: op.ID, At: val(op.At), Name: op.Name, Color: op.Color})
		return d, ""
	case "remove_marker":
		i := slices.IndexFunc(d.Markers, func(m Marker) bool { return m.ID == op.ID })
		if i < 0 {
			return d, "unknown_marker"
		}
		d.Markers = slices.Delete(d.Markers, i, i+1)
		return d, ""
	case "ducking": // 1.106: the sequence's Duck music (absent fields kept; a sequence without it is off)
		was := Ducking{DB: new(float64(DuckDB))}
		if d.Ducking != nil {
			was = *d.Ducking
		}
		now := Ducking{On: was.On, DB: new(was.Amount())}
		if op.On != nil {
			now.On = *op.On
		}
		if op.DB != nil {
			now.DB = new(*op.DB)
		}
		d.Ducking = &now
		return d, ""
	}
	var t *Track
	i, at := -1, 0
	for ti := range d.Tracks {
		if k := slices.IndexFunc(d.Tracks[ti].Clips, func(c Clip) bool { return c.ID == op.Clip }); k >= 0 {
			t, i, at = &d.Tracks[ti], k, ti
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
			moved.At = new(val(op.At))
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
		// a video's frames; (1.32) an audio take's end clamps an audio-track clip's out too (on the sample grid)
		if tk, ok := takes[c.Take()]; ok && (kind == "video" || kind == "audio") {
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
				c.In, c.Out = new(g.S(in)), new(g.S(out))
			} else {
				if op.Edge != "out" {
					df = -df
				}
				c.Hold = new(g.S(max(g.F(val(c.Hold))+df, least)))
			}
		} else if kind == "slot" { // 1.32: a line placeholder's hold, its start kept
			if op.Edge != "out" {
				df = -df
			}
			c.Hold = new(g.S(max(g.F(val(c.Hold))+df, 1)))
		} else {
			in, out, frame := g.Smp(val(c.In)), g.Smp(val(c.Out)), g.Smp(g.S(1))
			sr := float64(g.sr)
			if op.Edge == "in" {
				df = max(df, -g.F(val(c.At)))
				ni := min(max(in+g.Smp(g.S(df)), 0), out-frame)
				c.At, c.In = new(g.Snap(val(c.At)+float64(ni-in)/sr)), new(float64(ni)/sr)
			} else {
				no := max(out+g.Smp(g.S(df)), in+frame)
				if hasSrc {
					no = min(no, g.Smp(takes[c.Take()].DurationS))
				}
				c.Out = new(float64(no) / sr)
			}
		}
	case "remove", "lift":
		if op.Op == "lift" && !onV1 {
			return d, "lift_track"
		}
		if op.Op == "lift" {
			*c = Clip{ID: c.ID, Hold: new(g.S(Len(*c, kind, g, false)))}
			cutAt(i + 1)
		} else {
			removed := *c
			t.Clips = slices.Delete(t.Clips, i, i+1)
			if onV1 {
				cutAt(i)
			} else if op.Ripple { // 1.50: the later clips of its audio track move left by its length
				a, ln := g.F(val(removed.At)), Len(removed, kind, g, true)
				for k := range t.Clips {
					if x := g.F(val(t.Clips[k].At)); x >= a {
						t.Clips[k].At = new(g.S(x - ln))
					}
				}
			}
		}
	case "roll": // 1.50: the cut before the clip moves by delta; the length is kept
		if !onV1 {
			return d, "roll_track"
		}
		if i == 0 {
			return d, "roll_first"
		}
		df, p := g.F(op.Delta), &(*v1)[i-1]
		pk := Kind(*p, takes)
		if Len(*p, pk, g, false)+df < max(1, din(i-1)/2+din(i)/2) || Len(*c, kind, g, false)-df < max(1, din(i)/2+din(i+1)/2) {
			return d, "least"
		}
		grow(p, pk, df, g)
		front(c, kind, df, g)
	case "slip": // 1.50: in and out move by delta; place and length kept
		if kind == "image" || kind == "slot" {
			return d, "slip_still"
		}
		df := g.F(op.Delta)
		if onV1 {
			c.In, c.Out = new(g.S(g.F(val(c.In))+df)), new(g.S(g.F(val(c.Out))+df))
		} else { // on the sample grid
			ds, sr := g.Smp(g.S(df)), float64(g.sr)
			c.In, c.Out = new(float64(g.Smp(val(c.In))+ds)/sr), new(float64(g.Smp(val(c.Out))+ds)/sr)
		}
	case "slide": // 1.50: the clip moves along V1 by delta; its neighbours give and take the frames
		if !onV1 {
			return d, "slide_track"
		}
		if i == 0 || i == len(*v1)-1 {
			return d, "slide_end"
		}
		df, p, nx := g.F(op.Delta), &(*v1)[i-1], &(*v1)[i+1]
		pk, nk := Kind(*p, takes), Kind(*nx, takes)
		if Len(*p, pk, g, false)+df < max(1, din(i-1)/2+din(i)/2) || Len(*nx, nk, g, false)-df < max(1, din(i+1)/2+din(i+2)/2) {
			return d, "least"
		}
		grow(p, pk, df, g)
		front(nx, nk, df, g)
	case "overwrite":
		return d, overwrite(&d, at, i, op, takes, g)
	case "replace":
		if !slices.Contains(c.Alternatives, op.TakeID) {
			return d, "not_alternative"
		}
		tk, ok := takes[op.TakeID]
		if !ok {
			return d, "take_not_found"
		}
		ln, oldIn := Len(*c, kind, g, !onV1), 0
		if kind == "video" || kind == "audio" {
			oldIn = g.F(val(c.In))
		}
		c.In, c.Out, c.Hold = nil, nil, nil
		if tk.Kind == "image" && onV1 {
			c.Hold = new(g.S(ln))
		} else {
			src := g.F(tk.DurationS)
			in := min(oldIn, src-1)
			c.In, c.Out = new(g.S(in)), new(g.S(min(in+ln, src)))
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
			case "link": // 1.50
				c.Link = nil
				if !null {
					_ = json.Unmarshal(raw, &c.Link)
				}
			case "fade_in", "fade_out", "crossfade": // 1.106: 0 or null removes
				p := map[string]**float64{"fade_in": &c.FadeIn, "fade_out": &c.FadeOut, "crossfade": &c.Crossfade}[key]
				*p = nil
				if !null {
					_ = json.Unmarshal(raw, p)
					if *p != nil && **p == 0 {
						*p = nil
					}
				}
			}
		}
	case "fill": // studio: a shot's take fills its slot (README, Timeline: New shot; take actions)
		tk, ln, in0, out0 := takes[op.TakeID], Len(*c, kind, g, false), c.In, c.Out
		c.In, c.Out, c.Hold, c.Shot = nil, nil, nil, nil
		if sm := tk.SourceMap; !onV1 { // 1.32, an audio track (New line, a line's re-render): the whole take at its start
			c.In, c.Out = new(0.0), new(tk.DurationS)
		} else if tk.Kind == "image" {
			c.Hold = new(g.S(ln))
		} else if sm != nil {
			// the generated part; replacing its source (or a take made from it): the clip's window, grown on a side
			// only where the generated part lies outside the source (extend), never by the 8-frame snap (retake)
			a, b, mine, own := sm.NewInS, sm.NewOutS, 0.0, c.Take() == sm.TakeID
			if cur := takes[c.Take()].SourceMap; !own && cur != nil && cur.TakeID == sm.TakeID {
				mine, own = cur.OffsetS, true
			}
			if own && out0 != nil {
				end := sm.OffsetS + takes[sm.TakeID].DurationS
				if g.F(a) >= g.F(sm.OffsetS) {
					a = val(in0) - mine + sm.OffsetS
				}
				if g.F(b) <= g.F(end) {
					b = *out0 - mine + sm.OffsetS
				}
			}
			c.In, c.Out = new(g.S(max(g.F(a), 0))), new(g.S(min(g.F(b), g.F(tk.DurationS))))
		} else {
			c.In, c.Out = new(0.0), new(g.S(min(g.F(tk.DurationS), ln)))
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
// and alternatives, never the dissolve or the shot. From 1.50 a V1 split cuts the clip's linked sound clips at that
// frame too (op.IDs, in track order) and relinks them.
func split(d *Doc, t *Track, onV1 bool, op Op, takes map[string]Take, g Grid) string {
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
	id := t.Clips[k].ID
	cut(t, k, f, f-p, ln, op.ID, onV1, takes, g)
	if !onV1 {
		return ""
	}
	ids := op.IDs
	for ti := 1; ti < len(d.Tracks); ti++ {
		at := &d.Tracks[ti]
		k, a, ln := inside(at, f, false, takes, g)
		if k < 0 || at.Clips[k].Link == nil || *at.Clips[k].Link != id || at.Clips[k].Shot != nil {
			continue
		}
		if len(ids) == 0 {
			return "ids_short"
		}
		cut(at, k, f, a, ln, ids[0], false, takes, g)
		ids = ids[1:]
	}
	relink(d, map[string]string{op.ID: id}, takes, g)
	return ""
}

// overwrite is t_overwrite (1.50, Overwrite): clip i of track ti goes in at op.At over what is there. V1: it leaves an
// empty slot of its length, the frames [at, at + length) are cut out of V1 and it goes in there, an empty slot first
// past V1's end; no ripple. Audio (on op.Track, default its own): the clips under it are cut around it, a right part
// starting on the first frame at or after its end. New ids from op.IDs: a right part, then (V1) the slot it leaves,
// then the gap past the end.
func overwrite(d *Doc, ti, i int, op Op, takes map[string]Take, g Grid) string {
	t := &d.Tracks[ti]
	c, ids := t.Clips[i], op.IDs
	next := func() (string, bool) {
		if len(ids) == 0 {
			return "", false
		}
		id := ids[0]
		ids = ids[1:]
		return id, true
	}
	clone := func(c Clip) Clip { return Doc{Tracks: []Track{{Clips: []Clip{c}}}}.Clone().Tracks[0].Clips[0] }
	if ti == 0 {
		if op.Track != "" && op.Track != "V1" {
			return "overwrite_track"
		}
		v1 := &d.Tracks[0].Clips
		kind := Kind(c, takes)
		n, lo := Len(c, kind, g, false), g.F(val(op.At))
		hi := lo + n
		moved := clone(c)
		moved.TransitionIn = nil
		starts, end := v1Starts(*v1, takes, g)
		(*v1)[i] = Clip{Hold: new(g.S(n))}
		if i+1 < len(*v1) {
			(*v1)[i+1].TransitionIn = nil
		}
		type part struct {
			a, b int
			kind string
		}
		parts := make([]part, len(*v1))
		for k, x := range *v1 {
			xk := Kind(x, takes)
			parts[k] = part{starts[k], starts[k] + Len(x, xk, g, false), xk}
		}
		for k, p := range parts {
			if (*v1)[k].Shot != nil && p.a < hi && p.b > lo {
				return "overwrite_shot"
			}
		}
		var right []Clip
		root := map[string]string{}
		for k, p := range parts {
			if p.a < lo && p.b > hi {
				id, ok := next()
				if !ok {
					return "ids_short"
				}
				r := clone((*v1)[k])
				r.TransitionIn, r.Shot, r.ID = nil, nil, id
				front(&r, p.kind, hi-p.a, g)
				right, root[id] = []Clip{r}, (*v1)[k].ID
				break
			}
		}
		hole, ok := next()
		if !ok {
			return "ids_short"
		}
		(*v1)[i].ID = hole
		var gap []Clip
		if lo > end {
			id, ok := next()
			if !ok {
				return "ids_short"
			}
			gap = []Clip{{ID: id, Hold: new(g.S(lo - end))}}
		}
		before, after := []Clip{}, right
		for k, p := range parts {
			x := (*v1)[k]
			switch {
			case p.b <= lo:
				before = append(before, x)
			case p.a >= hi:
				after = append(after, x)
			case p.a < lo:
				grow(&x, p.kind, lo-p.b, g)
				before = append(before, x)
			case p.b > hi:
				front(&x, p.kind, hi-p.a, g)
				after = append(after, x)
			}
		}
		if len(after) > 0 {
			after[0].TransitionIn = nil
		}
		*v1 = slices.Concat(before, gap, []Clip{moved}, after)
		relink(d, root, takes, g)
		return ""
	}
	name := op.Track
	if name == "" {
		name = t.Name
	}
	to := slices.IndexFunc(d.Tracks, func(x Track) bool { return x.Name == name })
	switch {
	case to < 0:
		return "unknown_track"
	case to == 0:
		return "overwrite_track"
	}
	t.Clips = slices.Delete(t.Clips, i, i+1)
	tt, at, sr := &d.Tracks[to], g.Snap(val(op.At)), float64(g.sr)
	lo := g.Smp(at)
	hi := lo + aLen(c, g)
	fe := g.F(float64(hi) / sr)
	if g.Smp(g.S(fe)) < hi { // the first frame at or after its end
		fe++
	}
	re := g.Smp(g.S(fe))
	under := func(x Clip) bool { s := g.Smp(val(x.At)); return s < hi && s+aLen(x, g) > lo }
	for _, x := range tt.Clips {
		if under(x) && x.Shot != nil {
			return "overwrite_shot"
		}
	}
	order := slices.Clone(tt.Clips)
	slices.SortStableFunc(order, func(a, b Clip) int { return g.Smp(val(a.At)) - g.Smp(val(b.At)) })
	keep := []Clip{}
	for _, x := range order {
		if !under(x) {
			keep = append(keep, x)
			continue
		}
		xs, is := g.Smp(val(x.At)), g.Smp(val(x.In))
		xe := xs + aLen(x, g)
		switch {
		case xs < lo:
			x2 := clone(x)
			x2.In, x2.At = new(float64(is+re-xs)/sr), new(g.S(fe))
			x.Out = new(float64(is+lo-xs) / sr)
			keep = append(keep, x)
			if xe > re {
				id, ok := next()
				if !ok {
					return "ids_short"
				}
				x2.ID = id
				keep = append(keep, x2)
			}
		case xe > re:
			x.In, x.At = new(float64(is+re-xs)/sr), new(g.S(fe))
			keep = append(keep, x)
		}
	}
	c.At = new(at)
	tt.Clips = append(keep, c)
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
		overlap := map[string]bool{}
		for _, p := range probs {
			if p.Code == "overlap" {
				overlap[p.Clip] = true
			}
		}
		for ti := 1; ti < len(raw.Tracks); ti++ { // 1.32: a filled line running into the next clip is cut at its start
			order := make([]*Clip, 0, len(raw.Tracks[ti].Clips))
			for i := range raw.Tracks[ti].Clips {
				order = append(order, &raw.Tracks[ti].Clips[i])
			}
			slices.SortStableFunc(order, func(a, b *Clip) int { return g.Smp(val(a.At)) - g.Smp(val(b.At)) })
			for k := 1; k < len(order); k++ {
				if prev, c := order[k-1], order[k]; overlap[c.ID] && prev.Out != nil {
					prev.Out = new(float64(g.Smp(val(prev.In))+g.Smp(val(c.At))-g.Smp(val(prev.At))) / float64(g.sr))
				}
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
				if past[c.ID] && c.Hold != nil { // 1.32: a line placeholder, never longer than it is
					t.Clips[i].Hold = new(float64(min(g.Smp(val(c.Hold)), end-g.Smp(val(c.At)))) / float64(g.sr))
				} else if past[c.ID] { // never past its out (a line already cut at the next clip stays cut)
					t.Clips[i].Out = new(float64(min(g.Smp(val(c.Out)), g.Smp(val(c.In))+end-g.Smp(val(c.At)))) / float64(g.sr))
				}
			}
		}
		n, probs = Normalize(raw, takes, &doc)
	}
	return Result{Document: n, Problems: probs}
}

func abs(x int) int { return max(x, -x) }
