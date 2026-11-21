// Package timeline: the timeline rules (contracts/README.md "Timeline rules"), a port of contracts/check.py's
// reference (t_normalize, t_edit, t_apply, t_cuts, snap_point, drop_index, frame_at, export_plan, shot_keyframes,
// rerender_steps) tested on every case of contracts/fixtures/timelines. Pure functions over the document: no
// database, no clock.
package timeline

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strconv"
)

// Caps (contracts/README.md, Timeline).
const (
	MagnetPx       = 6   // an edge lands on a cut within this many screen pixels
	ImageHoldS     = 3.0 // an image take added to V1 holds this long
	MaxAudioTracks = 8
	MaxClips       = 1000
	MaxBytes       = 262144
	MaxAlts        = 32
	MaxRerender    = 22

	// an export: at most 2 hours, an estimated 16 GiB written and 32 GiB of distinct takes read
	MaxExportS      = 7200
	MaxExportBytes  = 16 << 30
	MaxExportInputs = 32 << 30
)

// rates is the frame-rate table: fps as JSON writes it → the exact rate num/den.
var rates = []struct {
	fps      float64
	num, den int
}{{23.976, 24000, 1001}, {24, 24, 1}, {25, 25, 1}, {29.97, 30000, 1001}, {30, 30, 1}, {48, 48, 1}, {50, 50, 1},
	{59.94, 60000, 1001}, {60, 60, 1}}

// Rate is fps's exact rate num/den; ok false off the table.
func Rate(fps float64) (num, den int, ok bool) {
	for _, r := range rates {
		if math.Abs(r.fps-fps) < 1e-6 {
			return r.num, r.den, true
		}
	}
	return 0, 0, false
}

// Rnd rounds half away from zero (Go's math.Round is exactly that).
func Rnd(x float64) int { return int(math.Round(x)) }

// Grid is the target's frame and sample grids: f(s) = rnd(s × num / den), s(f) = f × den / num, in that order.
type Grid struct{ num, den, sr int }

// NewGrid is the grid of target (an fps off the table is 24).
func NewGrid(t Target) Grid {
	num, den, ok := Rate(t.FPS)
	if !ok {
		num, den = 24, 1
	}
	return Grid{num, den, t.SampleRate}
}

// F is seconds as frames.
func (g Grid) F(s float64) int { return Rnd(s * float64(g.num) / float64(g.den)) }

// S is frames as seconds.
func (g Grid) S(f int) float64 { return float64(f*g.den) / float64(g.num) }

// Snap puts seconds on the frame grid.
func (g Grid) Snap(s float64) float64 { return g.S(g.F(s)) }

// Smp is seconds as samples.
func (g Grid) Smp(s float64) int { return Rnd(s * float64(g.sr)) }

// RateString is the exact rate as "num/den".
func (g Grid) RateString() string { return strconv.Itoa(g.num) + "/" + strconv.Itoa(g.den) }

// ------------------------------------------------------------------ the document

// Target is the sequence's geometry and rates.
type Target struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	SampleRate int     `json:"sample_rate"`
}

// Dissolve is a clip's transition_in.
type Dissolve struct {
	Type   string `json:"type"`
	Frames int    `json:"frames"`
}

// ShotRef is a generation filling a slot: a New shot's run, or a Re-render batch's step. Status and Error are for
// answers only.
type ShotRef struct {
	RunID   string  `json:"run_id,omitempty"`
	BatchID string  `json:"batch_id,omitempty"`
	Step    string  `json:"step,omitempty"`
	Status  string  `json:"status,omitempty"`
	Error   *string `json:"error,omitempty"`
}

// Ref is the shot without its answer fields.
func (s ShotRef) Ref() ShotRef { return ShotRef{RunID: s.RunID, BatchID: s.BatchID, Step: s.Step} }

// Clip is a slot. Pointer fields are absent when nil (the reference's "key in clip").
type Clip struct {
	ID           string    `json:"id"`
	TakeID       *string   `json:"take_id"`
	In           *float64  `json:"in,omitempty"`
	Out          *float64  `json:"out,omitempty"`
	Hold         *float64  `json:"hold,omitempty"`
	At           *float64  `json:"at,omitempty"`
	GainDB       *float64  `json:"gain_db,omitempty"`
	Mute         *bool     `json:"mute,omitempty"`
	TransitionIn *Dissolve `json:"transition_in,omitempty"`
	Alternatives []string  `json:"alternatives"`
	Shot         *ShotRef  `json:"shot,omitempty"`
	Number       int       `json:"number,omitempty"` // V1: the shot number (API 1.29; Number gives it)
	Offline      bool      `json:"offline,omitempty"`
}

// Take returns the clip's take id, "" for an empty slot.
func (c Clip) Take() string {
	if c.TakeID == nil {
		return ""
	}
	return *c.TakeID
}

func (c Clip) din() int {
	if c.TransitionIn == nil {
		return 0
	}
	return c.TransitionIn.Frames
}

// Track is V1 (video) or an audio track.
type Track struct {
	Name   string   `json:"name,omitempty"`
	Kind   string   `json:"kind"`
	GainDB *float64 `json:"gain_db,omitempty"`
	Mute   *bool    `json:"mute,omitempty"`
	Clips  []Clip   `json:"clips"`
}

// Doc is a document: as written (DurationS and Frames ignored) or normalized.
type Doc struct {
	Target    Target  `json:"target"`
	Tracks    []Track `json:"tracks"`
	DurationS float64 `json:"duration_s"`
	Frames    int     `json:"frames"`
}

// Clone is a deep copy (through JSON: every float survives the round trip).
func (d Doc) Clone() Doc {
	raw, _ := json.Marshal(d)
	var out Doc
	_ = json.Unmarshal(raw, &out)
	return out
}

// Take is what the rules know of a take the document names (the caller's: one that is missing is offline or not
// found).
type Take struct {
	Kind       string     `json:"kind"`
	DurationS  float64    `json:"duration_s"`
	HasAudio   bool       `json:"has_audio"`
	Bytes      int64      `json:"bytes"`
	SampleRate *int       `json:"sample_rate,omitempty"` // an audio take's own rate (API 1.32), nil unknown
	Stream     *Stream    `json:"stream,omitempty"`
	MadeWith   *MadeWith  `json:"made_with,omitempty"`
	FrameOf    *FrameOf   `json:"frame_of,omitempty"`
	SourceMap  *SourceMap `json:"source_map,omitempty"`
}

// SourceMap is how a take made from a media take lines up with it (API 1.19, Take.source_map): output time = source
// time + OffsetS; [NewInS, NewOutS) is the generated part.
type SourceMap struct {
	TakeID  string  `json:"take_id"`
	OffsetS float64 `json:"offset_s"`
	NewInS  float64 `json:"new_in_s"`
	NewOutS float64 `json:"new_out_s"`
}

// MadeWith is the part of a take's made_with re-render reads.
type MadeWith struct {
	WorkflowID string         `json:"workflow_id"`
	Inputs     map[string]any `json:"inputs"`
}

// FrameOf names the video take a frame take is the first or last frame of.
type FrameOf struct {
	TakeID string `json:"take_id"`
	At     string `json:"at"`
}

// Stream is a video's stream facts (defs.schema.json videoStream).
type Stream struct {
	Container       string         `json:"container"`
	Codec           string         `json:"codec"`
	Profile         string         `json:"profile"`
	Level           int            `json:"level"`
	PixFmt          string         `json:"pix_fmt"`
	Width           int            `json:"width"`
	Height          int            `json:"height"`
	SAR             *string        `json:"sar"`
	FieldOrder      *string        `json:"field_order"`
	FrameRate       string         `json:"frame_rate"`
	TimeBase        string         `json:"time_base"`
	Color           map[string]any `json:"color"`
	ExtradataSHA256 string         `json:"extradata_sha256"`
}

// Problem is one broken rule: its code, track and clip ("" for none).
type Problem struct{ Code, Track, Clip string }

// MarshalJSON writes the reference's [code, track, clip] with null for "".
func (p Problem) MarshalJSON() ([]byte, error) {
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return json.Marshal([]any{p.Code, null(p.Track), null(p.Clip)})
}

func f64(v float64) *float64 { return &v }
func boolp(v bool) *bool     { return &v }
func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// Kind is a clip's kind: slot (an empty slot, or from 1.32 a New line's placeholder on an audio track), video, image
// or (1.32) audio; an offline clip by its fields (hold = image).
func Kind(c Clip, takes map[string]Take) string {
	tid := c.Take()
	if tid == "" {
		return "slot"
	}
	if tk, ok := takes[tid]; ok {
		return tk.Kind
	}
	if c.Hold != nil {
		return "image"
	}
	return "video"
}

// Len is a clip's length in frames on the sequence (an audio-track clip: its end's frame minus its start's).
func Len(c Clip, kind string, g Grid, audio bool) int {
	if audio {
		return g.F(val(c.At)+audioS(c)) - g.F(val(c.At))
	}
	if kind == "video" {
		return g.F(val(c.Out)) - g.F(val(c.In))
	}
	return g.F(val(c.Hold))
}

// audioS is an audio-track clip's length in seconds: out − in, or (1.32) a line placeholder's hold.
func audioS(c Clip) float64 {
	if c.Hold != nil {
		return *c.Hold
	}
	return val(c.Out) - val(c.In)
}

// aLen is an audio-track clip's length in target samples (a_len).
func aLen(c Clip, g Grid) int {
	if c.Hold != nil {
		return g.Smp(*c.Hold)
	}
	return g.Smp(val(c.Out)) - g.Smp(val(c.In))
}

// Normalize checks, snaps and fills a written document (t_normalize). takes: the caller's takes it names; a missing
// one is offline when previous (the stored revision) named it, else take_not_found.
func Normalize(doc Doc, takes map[string]Take, previous *Doc) (*Doc, []Problem) {
	var probs []Problem
	add := func(code, track, clip string) { probs = append(probs, Problem{code, track, clip}) }
	if raw, _ := json.Marshal(Doc{Target: doc.Target, Tracks: doc.Tracks}); len(raw) > MaxBytes {
		return nil, []Problem{{Code: "too_large"}}
	}
	tracks := doc.Tracks
	bad := len(tracks) == 0 || tracks[0].Kind != "video" || len(tracks) > 1+MaxAudioTracks
	for _, t := range tracks[min(1, len(tracks)):] {
		bad = bad || t.Kind != "audio"
	}
	if bad {
		return nil, []Problem{{Code: "track_layout"}}
	}
	total := 0
	for _, t := range tracks {
		total += len(t.Clips)
	}
	if total > MaxClips {
		return nil, []Problem{{Code: "too_many_clips"}}
	}
	g := NewGrid(doc.Target)
	known, shots := map[string]bool{}, map[string]ShotRef{}
	if previous != nil {
		for _, t := range previous.Tracks {
			for _, c := range t.Clips {
				known[c.Take()] = true
				for _, a := range c.Alternatives {
					known[a] = true
				}
				if c.Shot != nil {
					shots[c.ID] = c.Shot.Ref()
				}
			}
		}
	}
	type kc struct {
		n    Clip
		kind string
	}
	ids := map[string]bool{}
	var out []Track
	type end struct {
		track, clip string
		e           int
	}
	var ends []end
	for ti, t := range tracks {
		name, video := "V1", ti == 0
		if !video {
			name = "A" + strconv.Itoa(ti+1)
		}
		var clips []kc
		for _, c := range t.Clips {
			cid, tid := c.ID, c.Take()
			tk, has := takes[tid]
			kind := Kind(c, takes)
			if ids[cid] {
				add("duplicate_clip", name, cid)
			}
			ids[cid] = true
			if kind == "audio" && video { // 1.32: an audio take goes on A2-A9 only
				add("wrong_track", name, cid)
				continue
			}
			refs := slices.Clone(c.Alternatives)
			if tid != "" {
				refs = append([]string{tid}, refs...)
			}
			if slices.ContainsFunc(refs, func(x string) bool { _, in := takes[x]; return !in && !known[x] }) {
				add("take_not_found", name, cid)
			}
			if c.Shot != nil {
				if prev, ok := shots[cid]; !ok || prev != c.Shot.Ref() {
					add("shot_invalid", name, cid)
				}
			}
			n := Clip{ID: cid, TakeID: c.TakeID}
			switch {
			case kind == "video" || kind == "audio":
				if c.Hold != nil {
					add("hold_on_video", name, cid)
				}
				endS := c.Out
				if endS == nil && has {
					endS = f64(tk.DurationS)
				}
				if endS == nil {
					add("out_required", name, cid)
					continue
				}
				if video {
					i, o := g.F(val(c.In)), g.F(*endS)
					if has && !(0 <= i && o-i >= 1 && o <= g.F(tk.DurationS)) {
						add("trim", name, cid)
					}
					n.In, n.Out = f64(g.S(i)), f64(g.S(o))
				} else {
					i, o := g.Smp(val(c.In)), g.Smp(*endS)
					if has && !(0 <= i && i < o && o <= g.Smp(tk.DurationS)) {
						add("trim", name, cid)
					}
					if has && kind == "video" && !tk.HasAudio {
						add("no_audio", name, cid)
					}
					if c.At == nil {
						add("at_required", name, cid)
						continue
					}
					n.In, n.Out, n.At = f64(float64(i)/float64(g.sr)), f64(float64(o)/float64(g.sr)), f64(g.Snap(*c.At))
				}
			case !video && !(kind == "slot" && c.Shot != nil): // 1.32: a New line placeholder is the exception
				add("wrong_track", name, cid)
				continue
			case !video:
				if c.In != nil || c.Out != nil {
					add("image_trim", name, cid)
				}
				if c.Hold == nil {
					add("hold_required", name, cid)
					continue
				}
				if c.At == nil {
					add("at_required", name, cid)
					continue
				}
				if g.F(*c.Hold) < 1 {
					add("hold", name, cid)
				}
				n.At, n.Hold = f64(g.Snap(*c.At)), f64(g.Snap(*c.Hold))
			default:
				if c.In != nil || c.Out != nil {
					add("image_trim", name, cid)
				}
				if c.Hold == nil {
					add("hold_required", name, cid)
					continue
				}
				if g.F(*c.Hold) < 1 {
					add("hold", name, cid)
				}
				n.Hold = f64(g.Snap(*c.Hold))
			}
			n.GainDB, n.Mute = f64(val(c.GainDB)), boolp(c.Mute != nil && *c.Mute)
			if c.TransitionIn != nil {
				if video {
					d := *c.TransitionIn
					n.TransitionIn = &d
				} else {
					add("dissolve_track", name, cid)
				}
			}
			alts, all := []string{}, slices.Clone(c.Alternatives)
			if tid != "" {
				all = append(all, tid)
			}
			for _, a := range all {
				if !slices.Contains(alts, a) {
					alts = append(alts, a)
				}
			}
			for len(alts) > MaxAlts { // the oldest that is not the current one goes
				i := slices.IndexFunc(alts, func(a string) bool { return a != tid })
				alts = slices.Delete(alts, i, i+1)
			}
			n.Alternatives = alts
			if c.Shot != nil {
				ref := c.Shot.Ref()
				n.Shot = &ref
			}
			if video {
				n.Number = c.Number
			}
			if tid != "" && !has {
				n.Offline = true
			}
			clips = append(clips, kc{n, kind})
		}
		if video {
			pos := 0
			for i := range clips {
				clips[i].n.At = f64(g.S(pos))
				pos += Len(clips[i].n, clips[i].kind, g, false)
			}
			din := make([]int, len(clips)+1)
			for k, c := range clips {
				din[k] = c.n.din()
			}
			for k, c := range clips {
				n, kind, d := c.n, c.kind, din[k]
				switch {
				case d != 0 && k == 0:
					add("dissolve_first", name, n.ID)
				case d != 0 && (kind == "slot" || clips[k-1].kind == "slot"):
					add("dissolve_gap", name, n.ID)
				case d != 0:
					p, pk := clips[k-1].n, clips[k-1].kind
					ptk, phas := takes[p.Take()]
					if (pk == "video" && phas && g.F(val(p.Out))+d/2 > g.F(ptk.DurationS)) || (kind == "video" && g.F(val(n.In))-d/2 < 0) {
						add("dissolve_handles", name, n.ID)
					}
				}
				half := din[k+1] / 2
				if k > 0 {
					half += din[k] / 2
				}
				if half != 0 && half > Len(n, kind, g, false) {
					add("dissolve_length", name, n.ID)
				}
			}
		} else {
			slices.SortStableFunc(clips, func(a, b kc) int { return cmp.Compare(*a.n.At, *b.n.At) })
			prevEnd, seen := 0, false
			for _, c := range clips {
				a := g.Smp(*c.n.At)
				e := a + aLen(c.n, g)
				if seen && a < prevEnd {
					add("overlap", name, c.n.ID)
				}
				prevEnd, seen = max(e, prevEnd), true
				ends = append(ends, end{name, c.n.ID, e})
			}
		}
		nt := Track{Name: name, Kind: t.Kind, GainDB: f64(val(t.GainDB)), Mute: boolp(t.Mute != nil && *t.Mute), Clips: []Clip{}}
		for _, c := range clips {
			nt.Clips = append(nt.Clips, c.n)
		}
		out = append(out, nt)
	}
	frames := 0
	if v1 := out[0].Clips; len(v1) > 0 {
		last := v1[len(v1)-1]
		frames = g.F(*last.At) + Len(last, Kind(last, takes), g, false)
		for _, e := range ends {
			if e.e > g.Smp(g.S(frames)) {
				add("past_end", e.track, e.clip)
			}
		}
	} else {
		for _, e := range ends {
			frames = max(frames, g.F(float64(e.e)/float64(g.sr)))
		}
	}
	if len(probs) > 0 {
		slices.SortStableFunc(probs, func(a, b Problem) int {
			return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.Track, b.Track), cmp.Compare(a.Clip, b.Clip))
		})
		return nil, probs
	}
	return &Doc{Target: doc.Target, Tracks: out, DurationS: g.S(frames), Frames: frames}, nil
}

// Number gives V1's shot numbers as studio stores doc over stored (nil: a new sequence; contracts/README.md,
// Timeline rules: shot numbers): a clip id stored V1 has keeps its number, a clip carrying a number no other has keeps
// it (undo bringing a removed clip back), any other gets the highest so far + 1, in order.
func Number(doc *Doc, stored *Doc) {
	if len(doc.Tracks) == 0 {
		return
	}
	was, top := map[string]int{}, 0
	if stored != nil && len(stored.Tracks) > 0 {
		for _, c := range stored.Tracks[0].Clips {
			was[c.ID], top = c.Number, max(top, c.Number)
		}
	}
	clips, used := doc.Tracks[0].Clips, map[int]bool{}
	for i := range clips {
		if n := was[clips[i].ID]; n > 0 {
			clips[i].Number, used[n] = n, true
		}
	}
	for i := range clips {
		c := &clips[i]
		if was[c.ID] > 0 {
			continue
		}
		if c.Number > 0 && !used[c.Number] {
			used[c.Number] = true
		} else {
			c.Number = 0
		}
	}
	for n := range used {
		top = max(top, n)
	}
	for i := range clips {
		if clips[i].Number == 0 {
			top++
			clips[i].Number = top
		}
	}
}
