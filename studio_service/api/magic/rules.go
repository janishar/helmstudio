package magic

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// The board rules (contracts/README.md "Magic: the board rules"): what a whole document given to studio must hold,
// what an edit changed, the warnings after it and the agent view.

// Limits of one board (MagicDoc).
const (
	MaxLanes  = 64
	MaxBlocks = 2000
	MaxAssets = 200
)

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// Kinds are a block's kinds; Roles a lane's.
	Kinds = []string{"frame", "image", "video", "voice", "music", "sound"}
	Roles = []string{"storyboard", "shots", "voice", "music", "sound", "free"}
	// requestFields are a MagicRequest's
	requestFields = []string{"kind", "model", "prompt", "line", "turns", "voice", "style", "from", "refs", "advanced", "first",
		"last", "width", "height", "seed", "count", "voice_id", // API 1.81: first … voice_id
		"framing", "shot_model", // API 1.83: a storyboard frame's framing and the model of the shot made from it
		"lyrics", "key", "bpm"} // API 1.85: a music block's
	// Framing is a frame's shot size, angle and move, each one's values.
	Framing = map[string][]string{"size": {"wide", "medium", "close", "over_shoulder"}, "angle": {"eye", "low", "high"},
		"move": {"still", "push_in", "pan", "follow"}}
)

// CheckTarget is "" or why t is not a board's target.
func CheckTarget(t Target) string {
	switch {
	case t.Width < 16 || t.Width > 8192 || t.Height < 16 || t.Height > 8192:
		return "target: width and height must be 16 to 8192"
	case t.FPS < 1 || t.FPS > 120:
		return "target: fps must be 1 to 120"
	case t.SampleRate < 8000 || t.SampleRate > 192000:
		return "target: sample_rate must be 8000 to 192000"
	}
	return ""
}

// Check is "" or the first rule d breaks: ids unique (lanes, blocks and transitions share one namespace) and well
// formed, every block on a lane at a time from 0 with a length and not over the block before it, links and
// transitions naming what is there (a transition between two blocks that meet), take ids as UUIDs, the limits.
func Check(d Doc) string {
	if why := CheckTarget(d.Target); why != "" {
		return why
	}
	switch {
	case len(d.Lanes) > MaxLanes:
		return fmt.Sprintf("a board has at most %d lanes", MaxLanes)
	case len(d.Blocks) > MaxBlocks:
		return fmt.Sprintf("a board has at most %d blocks", MaxBlocks)
	case len(d.Assets) > MaxAssets:
		return fmt.Sprintf("a board has at most %d assets", MaxAssets)
	}
	seen := map[string]bool{}
	id := func(where, v string) string {
		switch {
		case !idPattern.MatchString(v):
			return where + ": id must be 1-40 letters, digits, _ or -"
		case seen[v]:
			return where + ": id " + v + " is used twice"
		}
		seen[v] = true
		return ""
	}
	takes := func(where string, ids []string) string {
		for _, t := range ids {
			if !uuidPattern.MatchString(t) {
				return where + ": take ids are UUIDs"
			}
		}
		return ""
	}
	for i, l := range d.Lanes {
		where := fmt.Sprintf("lanes[%d]", i)
		if why := id(where, l.ID); why != "" {
			return why
		}
		if strings.TrimSpace(l.Name) == "" || len([]rune(l.Name)) > 80 {
			return where + ": name must be 1-80 characters"
		}
		if !slices.Contains(Roles, l.Role) {
			return where + ": role must be one of " + strings.Join(Roles, ", ")
		}
		if l.Kind != nil && !slices.Contains(Kinds, *l.Kind) {
			return where + ": kind must be one of " + strings.Join(Kinds, ", ")
		}
	}
	lastEnd := map[string]int{}
	order := map[string]int{}
	for i, l := range d.Lanes {
		order[l.ID] = i
	}
	sorted := d.Clone()
	SortBlocks(&sorted)
	for i, b := range d.Blocks {
		where := fmt.Sprintf("blocks[%d]", i)
		if why := id(where, b.ID); why != "" {
			return why
		}
		switch _, ok := order[b.Lane]; {
		case !ok:
			return where + ": lane " + b.Lane + " is not on the board"
		case b.At < 0 || b.Length < 1:
			return where + ": a block starts at 0 or later and is at least a frame long"
		case !slices.Contains(Kinds, b.Kind):
			return where + ": kind must be one of " + strings.Join(Kinds, ", ")
		}
		if why := takes(where, b.Takes); why != "" {
			return why
		}
		if b.Pinned != nil && !slices.Contains(b.Takes, *b.Pinned) {
			return where + ": pinned is one of its takes"
		}
		if k, _ := b.Request["kind"].(string); !slices.Contains(Kinds, k) {
			return where + ": request.kind must be one of " + strings.Join(Kinds, ", ")
		}
		for k := range b.Request {
			if !slices.Contains(requestFields, k) {
				return where + ": request: unknown field " + k
			}
		}
		if fr, ok := b.Request["framing"]; ok {
			m, ok := fr.(map[string]any)
			if !ok {
				return where + ": request.framing is an object"
			}
			for k, v := range m {
				if s, _ := v.(string); !slices.Contains(Framing[k], s) {
					return where + ": request.framing: " + k + " is not a framing"
				}
			}
		}
	}
	for _, b := range sorted.Blocks {
		if e, ok := lastEnd[b.Lane]; ok && b.At < e {
			return "blocks: " + b.ID + " overlaps the block before it on its lane"
		}
		lastEnd[b.Lane] = b.end()
	}
	for i, l := range d.Links {
		if d.block(l.Block) == nil || len(l.To) == 0 || slices.ContainsFunc(l.To, func(t string) bool { return d.block(t) == nil || t == l.Block }) {
			return fmt.Sprintf("links[%d]: it ties a block to blocks on the board", i)
		}
	}
	for i, t := range d.Transitions {
		where := fmt.Sprintf("transitions[%d]", i)
		if why := id(where, t.ID); why != "" {
			return why
		}
		if !slices.Contains(TransitionKinds, t.Kind) || t.Kind == "cut" {
			return where + ": kind must be dissolve, fade, wipe or morph (a cut is no transition)"
		}
		if why := checkMorph(where, t); why != "" {
			return why
		}
		n := 0
		for _, set := range []bool{t.Between != nil, t.Start != nil, t.End != nil} {
			if set {
				n++
			}
		}
		if n != 1 || t.Length < 0 {
			return where + ": exactly one of between, start, end; a length from 0"
		}
		if t.Between != nil {
			a, b := d.block(t.Between[0]), d.block(t.Between[1])
			if a == nil || b == nil || a.Lane != b.Lane || a.end() != b.At {
				return where + ": its blocks meet on a lane"
			}
		} else if l := cmpOr(t.Start, t.End); d.lane(l) == nil {
			return where + ": lane " + l + " is not on the board"
		}
	}
	assets := map[string]bool{}
	for i, a := range d.Assets {
		where := fmt.Sprintf("assets[%d]", i)
		if !idPattern.MatchString(a.ID) || assets[a.ID] {
			return where + ": asset ids are unique, 1-40 letters, digits, _ or -"
		}
		assets[a.ID] = true
		if !slices.Contains([]string{"character", "prop", "place"}, a.Kind) {
			return where + ": kind must be character, prop or place"
		}
		if why := takes(where, a.Sheet); why != "" {
			return why
		}
		// MagicAsset's bounds: asset_add and asset only refuse an empty name
		switch {
		case strings.TrimSpace(a.Name) == "" || len([]rune(a.Name)) > 80:
			return where + ": name must be 1-80 characters"
		case len(a.Sheet) > 50:
			return where + ": a sheet keeps at most 50 takes"
		case a.Note != nil && len([]rune(*a.Note)) > 4000:
			return where + ": note is at most 4000 characters"
		case a.VoiceID != nil && len(*a.VoiceID) > 80, a.ImportedFrom != nil && len(*a.ImportedFrom) > 80:
			return where + ": voice_id and imported_from are at most 80 characters"
		}
		for t, name := range a.Views {
			if !slices.Contains(a.Sheet, t) || strings.TrimSpace(name) == "" || len([]rune(name)) > 40 {
				return where + ": views name sheet takes, 1-40 characters each"
			}
		}
	}
	return ""
}

func cmpOr(a, b *string) string {
	if a != nil {
		return *a
	}
	if b != nil {
		return *b
	}
	return ""
}

// checkMorph is "" or why t's morph fields break the rules (API 1.82): only a morph has them, its request is a
// video's with known fields, its takes are UUIDs and its pin one of them.
func checkMorph(where string, t *Transition) string {
	if t.Kind != "morph" {
		if t.Request != nil || len(t.Takes) > 0 || t.Pinned != nil {
			return where + ": only a morph has a request and takes"
		}
		return ""
	}
	for _, id := range t.Takes {
		if !uuidPattern.MatchString(id) {
			return where + ": take ids are UUIDs"
		}
	}
	if t.Pinned != nil && !slices.Contains(t.Takes, *t.Pinned) {
		return where + ": pinned is one of its takes"
	}
	if t.Request != nil {
		if t.Request["kind"] != "video" {
			return where + ": a morph's request is a video's"
		}
		for k := range t.Request {
			if !slices.Contains(requestFields, k) {
				return where + ": request: unknown field " + k
			}
		}
	}
	return ""
}

// TakeIDs is every take id d names (blocks' takes and pins, assets' sheets, from 1.82 morphs' takes), each once.
func TakeIDs(d Doc) []string {
	var out []string
	add := func(ids ...string) {
		for _, t := range ids {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	for _, b := range d.Blocks {
		add(b.Takes...)
		if b.Pinned != nil {
			add(*b.Pinned)
		}
	}
	for _, a := range d.Assets {
		add(a.Sheet...)
	}
	for _, t := range d.Transitions {
		add(t.Takes...)
		if t.Pinned != nil {
			add(*t.Pinned)
		}
	}
	return out
}

// ------------------------------------------------------------------ what an edit changed

// IDDiff is what changed in one list, by id, in document order.
type IDDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
}

// Diff is MagicDiff.
type Diff struct {
	Blocks      IDDiff `json:"blocks"`
	Lanes       IDDiff `json:"lanes"`
	Transitions IDDiff `json:"transitions"`
	Assets      IDDiff `json:"assets"`
	Links       IDDiff `json:"links"`
}

func diffBy[T any](a, b []T, key func(T) string) IDDiff {
	out := IDDiff{Added: []string{}, Removed: []string{}, Changed: []string{}}
	was := map[string]string{}
	for _, x := range a {
		data, _ := json.Marshal(x)
		was[key(x)] = string(data)
	}
	is := map[string]bool{}
	for _, x := range b {
		k := key(x)
		is[k] = true
		data, _ := json.Marshal(x)
		switch old, ok := was[k]; {
		case !ok:
			out.Added = append(out.Added, k)
		case old != string(data):
			out.Changed = append(out.Changed, k)
		}
	}
	for _, x := range a {
		if !is[key(x)] {
			out.Removed = append(out.Removed, key(x))
		}
	}
	return out
}

// DiffDocs is what changed from a to b.
func DiffDocs(a, b Doc) Diff {
	return Diff{
		Blocks:      diffBy(a.Blocks, b.Blocks, func(x *Block) string { return x.ID }),
		Lanes:       diffBy(a.Lanes, b.Lanes, func(x *Lane) string { return x.ID }),
		Transitions: diffBy(a.Transitions, b.Transitions, func(x *Transition) string { return x.ID }),
		Assets:      diffBy(a.Assets, b.Assets, func(x *Asset) string { return x.ID }),
		Links:       diffBy(a.Links, b.Links, func(x *Link) string { return x.Block }),
	}
}

// secs is frames as the board says them: "3 s", "1.5 s".
func secs(frames, fps int) string {
	return fmt.Sprintf("%s s", strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", float64(frames)/float64(fps)), "0"), "."))
}

// Warnings are what the board shows after an edit: a block tied to one shot that runs past it ("Line 1 runs 3 s
// past Shot 1", lib/magic.ts overruns).
func Warnings(d Doc) []string {
	out := []string{}
	for _, l := range d.Links {
		b, last := d.block(l.Block), d.block(l.To[len(l.To)-1])
		if b != nil && last != nil && len(l.To) == 1 && b.end() > last.end() {
			shot, _, _ := strings.Cut(last.Name, " · ")
			out = append(out, fmt.Sprintf("%s runs %s past %s", b.Name, secs(b.end()-last.end(), d.Target.FPS), shot))
		}
	}
	return out
}

// ------------------------------------------------------------------ the agent view

// AgentBlock is MagicAgentBlock.
type AgentBlock struct {
	ID        string   `json:"id"`
	Lane      string   `json:"lane"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	At        int      `json:"at"`
	Length    int      `json:"length"`
	AtS       float64  `json:"at_s"`
	EndS      float64  `json:"end_s"`
	Status    string   `json:"status"`
	TakeID    *string  `json:"take_id"`
	Takes     int      `json:"takes"`
	Prompt    string   `json:"prompt,omitempty"`
	Line      string   `json:"line,omitempty"`
	Note      string   `json:"note,omitempty"`
	TiedTo    []string `json:"tied_to"`
	Effects   []string `json:"effects"`
	FromFrame *string  `json:"from_frame,omitempty"`
	// Request is its standard request as stored (API 1.88): a block op's request replaces it whole, so an agent sends
	// it back with its changes
	Request map[string]any `json:"request"`
}

// AgentLane is one lane of the agent view.
type AgentLane struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Hidden bool   `json:"hidden"`
	Blocks int    `json:"blocks"`
}

// AgentTransition is a transition with where its join is.
type AgentTransition struct {
	Transition
	AtS float64 `json:"at_s"`
}

// AgentAsset is an asset with the blocks it is used in.
type AgentAsset struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Handle string   `json:"handle"`
	Name   string   `json:"name"`
	UsedIn []string `json:"used_in"`
}

// Agent is MagicAgentView's board part.
type Agent struct {
	Target      Target            `json:"target"`
	Frames      int               `json:"frames"`
	DurationS   float64           `json:"duration_s"`
	Lanes       []AgentLane       `json:"lanes"`
	Blocks      []AgentBlock      `json:"blocks"`
	Transitions []AgentTransition `json:"transitions"`
	Assets      []AgentAsset      `json:"assets"`
	Notes       []string          `json:"notes"`
}

// AgentView is what an agent reads of d: every block flat with its times in seconds and frames and its status
// (draft with no take, else done; running and out of date come with runs).
func AgentView(d Doc) Agent {
	fps := float64(d.Target.FPS)
	s := func(f int) float64 { return math.Round(float64(f)/fps*1000) / 1000 }
	out := Agent{Target: d.Target, Frames: d.Duration(), DurationS: s(d.Duration()), Lanes: []AgentLane{},
		Blocks: []AgentBlock{}, Transitions: []AgentTransition{}, Assets: []AgentAsset{}, Notes: d.Notes}
	for _, l := range d.Lanes {
		n := 0
		for _, b := range d.Blocks {
			if b.Lane == l.ID {
				n++
			}
		}
		out.Lanes = append(out.Lanes, AgentLane{ID: l.ID, Name: l.Name, Role: l.Role, Hidden: l.Hidden != nil && *l.Hidden, Blocks: n})
	}
	for _, b := range d.Blocks {
		ab := AgentBlock{ID: b.ID, Lane: b.Lane, Kind: b.Kind, Name: b.Name, At: b.At, Length: b.Length, AtS: s(b.At),
			EndS: s(b.end()), Status: "draft", Takes: len(b.Takes), TiedTo: []string{}, Effects: []string{},
			FromFrame: b.FromFrame, Request: b.Request}
		switch {
		case b.Pinned != nil:
			ab.TakeID = b.Pinned
		case len(b.Takes) > 0:
			ab.TakeID = &b.Takes[len(b.Takes)-1]
		}
		if ab.TakeID != nil {
			ab.Status = "done"
		}
		ab.Prompt, _ = b.Request["prompt"].(string)
		ab.Line, _ = b.Request["line"].(string)
		if b.Note != nil {
			ab.Note = *b.Note
		}
		for _, l := range d.Links {
			if l.Block == b.ID {
				ab.TiedTo = l.To
			}
		}
		for _, e := range b.Effects {
			if e.Off == nil || !*e.Off {
				ab.Effects = append(ab.Effects, e.Name)
			}
		}
		out.Blocks = append(out.Blocks, ab)
	}
	for _, t := range d.Transitions {
		at := 0
		switch {
		case t.Between != nil:
			if b := d.block(t.Between[1]); b != nil {
				at = b.At
			}
		default:
			lane := cmpOr(t.Start, t.End)
			first, last := math.MaxInt, 0
			for _, b := range d.Blocks {
				if b.Lane == lane {
					first, last = min(first, b.At), max(last, b.end())
				}
			}
			if t.Start != nil && first != math.MaxInt {
				at = first
			} else if t.End != nil {
				at = last
			}
		}
		out.Transitions = append(out.Transitions, AgentTransition{Transition: *t, AtS: s(at)})
	}
	for _, a := range d.Assets {
		used := []string{}
		for _, b := range d.Blocks {
			refs, _ := b.Request["refs"].([]any)
			picks := slices.ContainsFunc(refs, func(r any) bool { m, _ := r.(map[string]any); return m["asset"] == a.ID })
			if b.Request["voice"] == a.ID || picks {
				used = append(used, b.ID)
			}
		}
		out.Assets = append(out.Assets, AgentAsset{ID: a.ID, Kind: a.Kind, Handle: a.Handle, Name: a.Name, UsedIn: used})
	}
	return out
}

// ------------------------------------------------------------------ an edit in words (API 1.87)

// kindNouns name blocks by kind when an action counts them ("3 shots").
var kindNouns = map[string]string{"frame": "frame", "image": "image", "video": "shot", "voice": "line", "music": "cue",
	"sound": "sound"}

// andList is "A", "A and B", "A, B and C".
func andList(xs []string) string {
	if len(xs) < 2 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// blockWords names blocks: up to two by their short names; more, by kind: a kind with several is counted, one alone
// is named ("3 shots and Frame 4", the board's frame 8).
func blockWords(bs []*Block) string {
	short := func(b *Block) string { name, _, _ := strings.Cut(b.Name, " · "); return name }
	var kinds []string
	of := map[string][]*Block{}
	for _, b := range bs {
		if of[b.Kind] == nil {
			kinds = append(kinds, b.Kind)
		}
		of[b.Kind] = append(of[b.Kind], b)
	}
	parts := []string{}
	for _, k := range kinds {
		switch n := len(of[k]); {
		case len(bs) <= 2 || n == 1:
			for _, b := range of[k] {
				parts = append(parts, short(b))
			}
		default:
			parts = append(parts, fmt.Sprintf("%d %ss", n, kindNouns[k]))
		}
	}
	return andList(parts)
}

// Describe is an edit from a to b in the board's words (contracts/README.md "Magic: the Assistant"): "Added Shot 2
// and Shot 3; moved Line 2; removed Shot 5; changed Shot 1", then lanes, assets, transitions and ties; "No change".
func Describe(a, b Doc) string {
	diff := DiffDocs(a, b)
	pick := func(d Doc, ids []string, keep func(was, is *Block) bool) []*Block {
		var out []*Block
		for _, id := range ids {
			x := d.block(id)
			if x != nil && (keep == nil || keep(a.block(id), x)) {
				out = append(out, x)
			}
		}
		return out
	}
	moved := func(was, is *Block) bool { return was.At != is.At || was.Lane != is.Lane }
	var parts []string
	say := func(verb, what string) {
		if what != "" {
			parts = append(parts, verb+" "+what)
		}
	}
	say("added", blockWords(pick(b, diff.Blocks.Added, nil)))
	say("moved", blockWords(pick(b, diff.Blocks.Changed, moved)))
	say("removed", blockWords(pick(a, diff.Blocks.Removed, nil)))
	say("changed", blockWords(pick(b, diff.Blocks.Changed, func(was, is *Block) bool { return !moved(was, is) })))
	count := func(n int, one, many string) string {
		switch n {
		case 0:
			return ""
		case 1:
			return one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	names := func(d Doc, ids []string, name func(Doc, string) string) string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, name(d, id))
		}
		if len(out) > 3 {
			return ""
		}
		return andList(out)
	}
	lane := func(d Doc, id string) string {
		if l := d.lane(id); l != nil {
			return "lane " + l.Name
		}
		return "a lane"
	}
	asset := func(d Doc, id string) string {
		if x := d.asset(id); x != nil {
			return "@" + x.Handle
		}
		return "an asset"
	}
	for _, x := range []struct {
		verb string
		d    Doc
		ids  []string
	}{{"added", b, diff.Lanes.Added}, {"removed", a, diff.Lanes.Removed}, {"changed", b, diff.Lanes.Changed}} {
		say(x.verb, cmp.Or(names(x.d, x.ids, lane), count(len(x.ids), "a lane", "lanes")))
	}
	for _, x := range []struct {
		verb string
		d    Doc
		ids  []string
	}{{"added", b, diff.Assets.Added}, {"removed", a, diff.Assets.Removed}, {"changed", b, diff.Assets.Changed}} {
		say(x.verb, cmp.Or(names(x.d, x.ids, asset), count(len(x.ids), "an asset", "assets")))
	}
	transition := func(d Doc, id string) string {
		for _, t := range d.Transitions {
			if t.ID == id {
				return "a " + t.Kind
			}
		}
		return "a transition"
	}
	say("added", names(b, diff.Transitions.Added, transition))
	say("removed", names(a, diff.Transitions.Removed, transition))
	say("changed", count(len(diff.Transitions.Changed), "a transition", "transitions"))
	say("tied", count(len(diff.Links.Added)+len(diff.Links.Changed), "a block", "blocks"))
	say("untied", count(len(diff.Links.Removed), "a block", "blocks"))
	if len(parts) == 0 {
		return "No change"
	}
	out := strings.Join(parts, "; ")
	return strings.ToUpper(out[:1]) + out[1:]
}
