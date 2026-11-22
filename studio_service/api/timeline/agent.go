package timeline

import (
	"encoding/json"
	"reflect"
	"slices"
)

// The agents' reads of a document (contracts/README.md, Timeline: edits by ops; API 1.49): the diff an /edits call
// answers, the agent view and what V1 shows at a time. Each is check.py's function of the same name, held to it by
// fixtures/timelines/{diffs,agent_view,frame_time}.json.

// Diff is t_diff: what changed from one normalized document to another, by clip id.
type Diff struct {
	Added    []string   `json:"added"`
	Removed  []string   `json:"removed"`
	Changed  []Change   `json:"changed"`
	Tracks   TrackDiff  `json:"tracks"`
	Duration [2]float64 `json:"duration"`
	Markers  TrackDiff  `json:"markers"` // 1.50: markers by id (changed: at, name, color)
}

// Change is one clip's, track's or (1.50) marker's changed fields, each [before, after] (absent = null).
type Change struct {
	Clip   string            `json:"clip,omitempty"`
	Track  string            `json:"track,omitempty"`
	Marker string            `json:"marker,omitempty"`
	Fields map[string][2]any `json:"fields"`
}

// TrackDiff is the tracks part of a Diff, by name.
type TrackDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []Change `json:"changed"`
}

var diffFields = []string{"take_id", "in", "out", "hold", "at", "gain_db", "mute", "transition_in", "alternatives",
	"shot", "offline", "track", "link"} // link: 1.50

// fieldsOf is v as JSON fields (numbers as float64, as the reference compares them).
func fieldsOf(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if a, ok := m["alternatives"]; ok && a == nil {
		m["alternatives"] = []any{}
	}
	return m
}

// changes is the fields among keys that differ between a and b.
func changes(a, b map[string]any, keys []string) map[string][2]any {
	out := map[string][2]any{}
	for _, k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			out[k] = [2]any{a[k], b[k]}
		}
	}
	return out
}

// DiffDocs is t_diff(before, after).
func DiffDocs(before, after Doc) Diff {
	type entry struct {
		id     string
		fields map[string]any
	}
	clips := func(d Doc) []entry {
		var out []entry
		for _, t := range d.Tracks {
			for _, c := range t.Clips {
				f := fieldsOf(c)
				f["track"] = t.Name
				out = append(out, entry{c.ID, f})
			}
		}
		return out
	}
	a, b := clips(before), clips(after)
	find := func(list []entry, id string) int {
		return slices.IndexFunc(list, func(e entry) bool { return e.id == id })
	}
	out := Diff{Added: []string{}, Removed: []string{}, Changed: []Change{},
		Tracks:   TrackDiff{Added: []string{}, Removed: []string{}, Changed: []Change{}},
		Duration: [2]float64{before.DurationS, after.DurationS}}
	for _, e := range b {
		if i := find(a, e.id); i < 0 {
			out.Added = append(out.Added, e.id)
		} else if f := changes(a[i].fields, e.fields, diffFields); len(f) > 0 {
			out.Changed = append(out.Changed, Change{Clip: e.id, Fields: f})
		}
	}
	for _, e := range a {
		if find(b, e.id) < 0 {
			out.Removed = append(out.Removed, e.id)
		}
	}
	track := func(d Doc, name string) int {
		return slices.IndexFunc(d.Tracks, func(t Track) bool { return t.Name == name })
	}
	for _, t := range after.Tracks {
		if i := track(before, t.Name); i < 0 {
			out.Tracks.Added = append(out.Tracks.Added, t.Name)
		} else if f := changes(fieldsOf(before.Tracks[i]), fieldsOf(t), []string{"gain_db", "mute"}); len(f) > 0 {
			out.Tracks.Changed = append(out.Tracks.Changed, Change{Track: t.Name, Fields: f})
		}
	}
	for _, t := range before.Tracks {
		if track(after, t.Name) < 0 {
			out.Tracks.Removed = append(out.Tracks.Removed, t.Name)
		}
	}
	out.Markers = TrackDiff{Added: []string{}, Removed: []string{}, Changed: []Change{}}
	marker := func(d Doc, id string) int {
		return slices.IndexFunc(d.Markers, func(m Marker) bool { return m.ID == id })
	}
	for _, m := range after.Markers {
		if i := marker(before, m.ID); i < 0 {
			out.Markers.Added = append(out.Markers.Added, m.ID)
		} else if f := changes(fieldsOf(before.Markers[i]), fieldsOf(m), []string{"at", "name", "color"}); len(f) > 0 {
			out.Markers.Changed = append(out.Markers.Changed, Change{Marker: m.ID, Fields: f})
		}
	}
	for _, m := range before.Markers {
		if marker(after, m.ID) < 0 {
			out.Markers.Removed = append(out.Markers.Removed, m.ID)
		}
	}
	return out
}

// AgentClip is one clip of the agent view.
type AgentClip struct {
	ID           string   `json:"id"`
	Track        string   `json:"track"`
	Index        int      `json:"index"`
	TakeID       *string  `json:"take_id"`
	Kind         string   `json:"kind"`
	In           *float64 `json:"in"`
	Out          *float64 `json:"out"`
	Hold         *float64 `json:"hold"`
	At           float64  `json:"at"`
	End          float64  `json:"end"`
	Length       float64  `json:"length"`
	AtF          int      `json:"at_f"`
	EndF         int      `json:"end_f"`
	LengthF      int      `json:"length_f"`
	GainDB       float64  `json:"gain_db"`
	Mute         bool     `json:"mute"`
	TransitionIn *int     `json:"transition_in"`
	Alternatives []string `json:"alternatives"`
	Shot         *ShotRef `json:"shot"`
	Number       *int     `json:"number"`
	Offline      bool     `json:"offline"`
	Link         *string  `json:"link"` // 1.50
}

// AgentMarker is one marker of the agent view (1.50).
type AgentMarker struct {
	ID    string  `json:"id"`
	At    float64 `json:"at"`
	AtF   int     `json:"at_f"`
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// AgentTrack is one track of the agent view.
type AgentTrack struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	GainDB float64 `json:"gain_db"`
	Mute   bool    `json:"mute"`
	Clips  int     `json:"clips"`
}

// AgentView is t_agent_view: a normalized document flat for an agent.
type AgentView struct {
	Target    Target              `json:"target"`
	DurationS float64             `json:"duration_s"`
	Frames    int                 `json:"frames"`
	Tracks    []AgentTrack        `json:"tracks"`
	Clips     []AgentClip         `json:"clips"`
	Links     map[string][]string `json:"links"`   // 1.50: each V1 clip's linked sound clips, in track order
	Markers   []AgentMarker       `json:"markers"` // 1.50
}

// Agent is t_agent_view(doc, takes).
func Agent(doc Doc, takes map[string]Take) AgentView {
	g := NewGrid(doc.Target)
	out := AgentView{Target: doc.Target, DurationS: doc.DurationS, Frames: doc.Frames, Tracks: []AgentTrack{}, Clips: []AgentClip{},
		Links: map[string][]string{}, Markers: []AgentMarker{}}
	for ti, t := range doc.Tracks {
		out.Tracks = append(out.Tracks, AgentTrack{Name: t.Name, Kind: t.Kind, GainDB: val(t.GainDB), Mute: t.Mute != nil && *t.Mute,
			Clips: len(t.Clips)})
		for i, c := range t.Clips {
			kind, a := Kind(c, takes), g.F(val(c.At))
			ln := Len(c, kind, g, ti > 0)
			ac := AgentClip{ID: c.ID, Track: t.Name, Index: i, TakeID: c.TakeID, Kind: kind, In: c.In, Out: c.Out, Hold: c.Hold,
				At: val(c.At), AtF: a, EndF: a + ln, LengthF: ln, GainDB: val(c.GainDB), Mute: c.Mute != nil && *c.Mute,
				Alternatives: append([]string{}, c.Alternatives...), Shot: c.Shot, Offline: c.Offline, Link: c.Link}
			if c.Link != nil {
				out.Links[*c.Link] = append(out.Links[*c.Link], c.ID)
			}
			switch {
			case ti == 0:
				ac.Length, ac.End = g.S(ln), g.S(a+ln)
			case c.Hold != nil:
				ac.Length, ac.End = *c.Hold, ac.At+*c.Hold
			default:
				ac.Length = val(c.Out) - val(c.In)
				ac.End = ac.At + ac.Length
			}
			if c.TransitionIn != nil {
				ac.TransitionIn = &c.TransitionIn.Frames
			}
			if c.Number > 0 {
				ac.Number = &c.Number
			}
			out.Clips = append(out.Clips, ac)
		}
	}
	for _, m := range doc.Markers {
		out.Markers = append(out.Markers, AgentMarker{ID: m.ID, At: m.At, AtF: g.F(m.At), Name: m.Name, Color: m.Color})
	}
	return out
}

// FrameTime is frame_at_time's answer: what V1 shows at a time.
type FrameTime struct {
	Empty   bool     `json:"empty"`
	Clip    *string  `json:"clip"`
	TakeID  *string  `json:"take_id"`
	Kind    *string  `json:"kind"`
	T       *float64 `json:"t"`
	Shown   *string  `json:"shown"`
	ShownT  *float64 `json:"shown_t"`
	Offline bool     `json:"offline,omitempty"`
}

// FrameAtTime is frame_at_time(doc, t, takes): the V1 clip under frames(t), no dissolve composited; a video's nearer
// stored frame, first (0) or last (seconds(frames(duration) − 1); a tie: first).
func FrameAtTime(doc Doc, t float64, takes map[string]Take) FrameTime {
	g := NewGrid(doc.Target)
	f, out := g.F(t), FrameTime{Empty: true}
	str := func(s string) *string { return &s }
	for _, c := range doc.Tracks[0].Clips {
		kind, a := Kind(c, takes), g.F(val(c.At))
		if a > f || f >= a+Len(c, kind, g, false) {
			continue
		}
		out.Clip = str(c.ID)
		if kind == "slot" {
			return out
		}
		out.Empty, out.TakeID, out.Kind = false, c.TakeID, str(kind)
		switch {
		case c.Offline:
			out.Offline = true
		case kind == "image":
			out.Shown = str("image")
		default:
			src, last := g.S(g.F(val(c.In))+f-a), g.S(g.F(takes[c.Take()].DurationS)-1)
			out.T, out.Shown, out.ShownT = &src, str("last"), &last
			if 2*g.F(src) <= g.F(last) {
				out.Shown, out.ShownT = str("first"), f64(0)
			}
		}
		return out
	}
	return out
}
