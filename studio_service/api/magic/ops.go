// Package magic is a Magic board's document and the named ops that edit it (contracts/README.md "Magic", API 1.80;
// plan 39 §45.6). It is web_service/lib/magic.ts `apply` in Go, op for op: contracts/fixtures/magic/edits.json is
// replayed through both (contracts/check.py), so the editor draws what studio stores. Pure: no database.
package magic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/helmstudio/studio_service/api/timeline"
)

// Target is a board's size and rates.
type Target struct {
	Width      int `json:"width"`
	Height     int `json:"height"`
	FPS        int `json:"fps"`
	SampleRate int `json:"sample_rate"`
}

// Lane is one lane; its place in Doc.Lanes is its order.
type Lane struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Role   string  `json:"role"`
	Hidden *bool   `json:"hidden,omitempty"`
	Kind   *string `json:"kind,omitempty"`
}

// Effect is an effect badge.
type Effect struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
	Off    *bool          `json:"off,omitempty"`
}

// Block is a node on a lane; Request is kept as the client gave it (MagicRequest).
type Block struct {
	ID        string         `json:"id"`
	Lane      string         `json:"lane"`
	At        int            `json:"at"`
	Length    int            `json:"length"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Request   map[string]any `json:"request"`
	Takes     []string       `json:"takes"`
	Pinned    *string        `json:"pinned,omitempty"`
	Note      *string        `json:"note,omitempty"`
	Effects   []Effect       `json:"effects"`
	FromFrame *string        `json:"from_frame,omitempty"`
	// GainDB is an audio block's level (API 1.101; -60..12 dB, absent = 0), carried onto its Timeline clips
	GainDB *float64 `json:"gain_db,omitempty"`
	// FadeIn and FadeOut are an audio block's fades in seconds (API 1.106; 0..10, absent = none), sent onto its clips
	FadeIn  *float64 `json:"fade_in,omitempty"`
	FadeOut *float64 `json:"fade_out,omitempty"`
}

func (b *Block) end() int { return b.At + b.Length }

// Link ties a block to a shot or a run of shots.
type Link struct {
	Block string   `json:"block"`
	To    []string `json:"to"`
}

// Transition is a join's transition: Between two blocks, or a lane's Start or End.
type Transition struct {
	ID      string     `json:"id"`
	Between *[2]string `json:"between,omitempty"`
	Start   *string    `json:"start,omitempty"`
	End     *string    `json:"end,omitempty"`
	Kind    string     `json:"kind"`
	Length  int        `json:"length"`
	// a morph's own (API 1.82): its video request, the takes its runs made and the one it shows
	Request map[string]any `json:"request,omitempty"`
	Takes   []string       `json:"takes,omitempty"`
	Pinned  *string        `json:"pinned,omitempty"`
}

// Asset is the board's own character, prop or place.
type Asset struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Handle       string   `json:"handle"`
	Name         string   `json:"name"`
	Sheet        []string `json:"sheet"`
	VoiceID      *string  `json:"voice_id,omitempty"`
	Note         *string  `json:"note,omitempty"`
	ImportedFrom *string  `json:"imported_from,omitempty"`
	// Views names sheet takes ("Front", "Side"), by take id (API 1.83)
	Views map[string]string `json:"views,omitempty"`
}

// Doc is one board (MagicDoc).
type Doc struct {
	Target      Target        `json:"target"`
	Lanes       []*Lane       `json:"lanes"`
	Blocks      []*Block      `json:"blocks"`
	Links       []*Link       `json:"links"`
	Transitions []*Transition `json:"transitions"`
	Assets      []*Asset      `json:"assets"`
	Notes       []string      `json:"notes"`
	// Ducking is the board's Duck music (API 1.106): absent is on at -12 dB (BoardDucking)
	Ducking *timeline.Ducking `json:"ducking,omitempty"`
}

// BoardDucking is a board's Duck music when its document has none (API 1.106), and a new board's.
func BoardDucking() timeline.Ducking {
	return timeline.Ducking{On: true, DB: new(float64(timeline.DuckDB))}
}

// Ducks is d's Duck music as it plays.
func (d Doc) Ducks() timeline.Ducking {
	if d.Ducking == nil {
		return BoardDucking()
	}
	return *d.Ducking
}

// DefaultTarget is a new board's: 1280 × 720, 24 fps, 48000 Hz.
var DefaultTarget = Target{Width: 1280, Height: 720, FPS: 24, SampleRate: 48000}

// Empty is a board with nothing on it.
func Empty(t Target) Doc { k := BoardDucking(); return Doc{Target: t, Ducking: &k}.filled() }

// filled is d with every list non-nil (they answer as []).
func (d Doc) filled() Doc {
	fill := func(s *[]string) {
		if *s == nil {
			*s = []string{}
		}
	}
	if d.Lanes == nil {
		d.Lanes = []*Lane{}
	}
	if d.Blocks == nil {
		d.Blocks = []*Block{}
	}
	if d.Links == nil {
		d.Links = []*Link{}
	}
	if d.Transitions == nil {
		d.Transitions = []*Transition{}
	}
	if d.Assets == nil {
		d.Assets = []*Asset{}
	}
	fill(&d.Notes)
	for _, b := range d.Blocks {
		fill(&b.Takes)
		if b.Effects == nil {
			b.Effects = []Effect{}
		}
		if b.Request == nil {
			b.Request = map[string]any{}
		}
	}
	for _, a := range d.Assets {
		fill(&a.Sheet)
	}
	return d
}

// Clone is a deep copy of d.
func (d Doc) Clone() Doc {
	data, _ := json.Marshal(d)
	var out Doc
	_ = json.Unmarshal(data, &out)
	return out.filled()
}

// Parse decodes a stored or given document strictly (unknown fields refused).
func Parse(data []byte) (Doc, error) {
	var d Doc
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Doc{}, err
	}
	return d.filled(), nil
}

// Duration is the board's length: its last block's end.
func (d Doc) Duration() int {
	n := 0
	for _, b := range d.Blocks {
		n = max(n, b.end())
	}
	return n
}

func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}

// ids is every block, lane and transition id (one namespace).
func (d *Doc) ids() []string {
	var out []string
	for _, b := range d.Blocks {
		out = append(out, b.ID)
	}
	for _, l := range d.Lanes {
		out = append(out, l.ID)
	}
	for _, t := range d.Transitions {
		out = append(out, t.ID)
	}
	return out
}

// FreshID is an id with this prefix not used in d nor in taken.
func (d *Doc) FreshID(prefix string, taken ...string) string {
	used := append(d.ids(), taken...)
	for n := 1; ; n++ {
		if id := fmt.Sprintf("%s%d", prefix, n); !slices.Contains(used, id) {
			return id
		}
	}
}

func (d *Doc) block(id string) *Block {
	for _, b := range d.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (d *Doc) lane(id string) *Lane {
	for _, l := range d.Lanes {
		if l.ID == id {
			return l
		}
	}
	return nil
}

func (d *Doc) without(gone func(*Block) bool) {
	d.Blocks = slices.DeleteFunc(slices.Clone(d.Blocks), gone)
}

// ------------------------------------------------------------------ placing blocks

// lift takes ids off the document; Insert closes each gap on its lane. Per lane, the map from an old time to its time
// after the gaps closed (none for Overwrite).
func lift(d *Doc, ids []string, mode string) map[string]func(int) int {
	var gone []*Block
	for _, b := range d.Blocks {
		if slices.Contains(ids, b.ID) {
			gone = append(gone, b)
		}
	}
	d.without(func(b *Block) bool { return slices.Contains(ids, b.ID) })
	maps := map[string]func(int) int{}
	if mode == "overwrite" {
		return maps
	}
	var lanes []string
	for _, b := range gone {
		if !slices.Contains(lanes, b.Lane) {
			lanes = append(lanes, b.Lane)
		}
	}
	for _, lane := range lanes {
		var holes []*Block
		for _, h := range gone {
			if h.Lane == lane {
				holes = append(holes, h)
			}
		}
		last := 0
		for _, b := range d.Blocks {
			if b.Lane == lane {
				last = max(last, b.end())
			}
		}
		close := func(t int) int {
			n := 0
			for _, h := range holes {
				switch {
				case h.end() <= t:
					n += h.Length
				case h.At < t:
					n += t - h.At
				}
			}
			return t - n
		}
		maps[lane] = func(t int) int {
			if t > last {
				return t
			}
			return close(t)
		}
		for _, b := range d.Blocks {
			if b.Lane == lane {
				b.At = close(b.At)
			}
		}
	}
	return maps
}

// room makes room for [at, at+n) on lane: Insert pushes what starts there or later along (a point inside a block
// goes to its end); Overwrite cuts the span out. Where the group starts.
func room(d *Doc, lane string, at, n int, mode string, taken []string) int {
	var here []*Block
	for _, b := range d.Blocks {
		if b.Lane == lane {
			here = append(here, b)
		}
	}
	if mode == "insert" {
		start := at
		for _, b := range here {
			if b.At < at && at < b.end() {
				start = b.end()
				break
			}
		}
		for _, b := range here {
			if b.At >= start {
				b.At += n
			}
		}
		return start
	}
	stop := at + n
	for _, b := range here {
		switch {
		case b.At >= stop || b.end() <= at:
		case b.At >= at && b.end() <= stop:
			d.without(func(x *Block) bool { return x == b })
		case b.At < at && b.end() > stop:
			rest := clone(*b)
			rest.ID, rest.At, rest.Length = d.FreshID("b", taken...), stop, b.end()-stop
			b.Length = at - b.At
			d.Blocks = append(d.Blocks, &rest)
		case b.At < at:
			b.Length = at - b.At
		default:
			b.Length, b.At = b.end()-stop, stop
		}
	}
	return at
}

// place puts group (off the document) on lane from at, keeping their spacing.
func place(d *Doc, group []*Block, lane string, at int, mode string) {
	lo, hi := math.MaxInt, 0
	var taken []string
	for _, b := range group {
		lo, hi = min(lo, b.At), max(hi, b.end())
		taken = append(taken, b.ID)
	}
	start := max(0, at)
	s := start
	if mode == "insert" {
		s = room(d, lane, start, hi-lo, mode, taken)
	} else {
		for _, b := range group {
			room(d, lane, start+b.At-lo, b.Length, mode, taken)
		}
	}
	for _, b := range group {
		c := *b
		c.Lane, c.At = lane, s+b.At-lo
		d.Blocks = append(d.Blocks, &c)
	}
}

// ------------------------------------------------------------------ ties and joins

// follow moves each tied block with its shot: by the shot's own move (a run: the first one's), or fitted to the run
// when it was fitted before. before: the blocks as they were; moved: blocks the op moved itself.
func follow(d *Doc, before map[string]Block, moved []string) {
	for _, l := range d.Links {
		b := d.block(l.Block)
		if b == nil || slices.Contains(moved, b.ID) {
			continue
		}
		var was []Block
		var is []*Block
		ok := true
		for _, id := range l.To {
			w, found := before[id]
			x := d.block(id)
			if !found || x == nil {
				ok = false
				break
			}
			was, is = append(was, w), append(is, x)
		}
		if !ok {
			continue
		}
		a0, e0, a1, e1 := math.MaxInt, 0, math.MaxInt, 0
		same := true
		for i := range was {
			a0, e0 = min(a0, was[i].At), max(e0, was[i].end())
			a1, e1 = min(a1, is[i].At), max(e1, is[i].end())
			same = same && was[i].At == is[i].At
		}
		if a0 == a1 && e0 == e1 && same {
			continue
		}
		old, found := before[b.ID]
		if !found {
			old = *b
		}
		if len(l.To) > 1 && old.At == a0 && old.end() == e0 {
			b.At, b.Length = a1, e1-a1
		} else {
			b.At = old.At + (is[0].At - was[0].At)
		}
	}
}

// tidy drops what points at blocks or lanes that are gone and joins that no longer touch; blocks in lane, time order.
func tidy(d *Doc) {
	d.Links = slices.DeleteFunc(slices.Clone(d.Links), func(l *Link) bool {
		if d.block(l.Block) == nil {
			return true
		}
		for _, t := range l.To {
			if d.block(t) == nil {
				return true
			}
		}
		return false
	})
	d.Transitions = slices.DeleteFunc(slices.Clone(d.Transitions), func(t *Transition) bool {
		if t.Between != nil {
			a, b := d.block(t.Between[0]), d.block(t.Between[1])
			return a == nil || b == nil || a.Lane != b.Lane || a.end() != b.At
		}
		lane := ""
		if t.Start != nil {
			lane = *t.Start
		} else if t.End != nil {
			lane = *t.End
		}
		return d.lane(lane) == nil
	})
	SortBlocks(d)
}

// SortBlocks puts d's blocks in lane, then time order.
func SortBlocks(d *Doc) {
	order := map[string]int{}
	for i, l := range d.Lanes {
		order[l.ID] = i
	}
	sort.SliceStable(d.Blocks, func(i, j int) bool {
		a, b := d.Blocks[i], d.Blocks[j]
		if order[a.Lane] != order[b.Lane] {
			return order[a.Lane] < order[b.Lane]
		}
		return a.At < b.At
	})
}

// ------------------------------------------------------------------ the ops

// Op is one named edit as given (MagicOp); Apply decodes its fields by its name.
type Op struct {
	Name string
	Raw  json.RawMessage
}

// UnmarshalJSON keeps the op whole and reads its name.
func (o *Op) UnmarshalJSON(data []byte) error {
	var head struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	o.Name, o.Raw = head.Op, slices.Clone(data)
	return nil
}

// MarshalJSON is the op as given.
func (o Op) MarshalJSON() ([]byte, error) { return o.Raw, nil }

// BlockNew is a block as add gives it (its id optional).
type BlockNew struct {
	ID        *string        `json:"id"`
	Lane      string         `json:"lane"`
	At        int            `json:"at"`
	Length    int            `json:"length"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Request   map[string]any `json:"request"`
	Takes     []string       `json:"takes"`
	Pinned    *string        `json:"pinned"`
	Note      *string        `json:"note"`
	Effects   []Effect       `json:"effects"`
	FromFrame *string        `json:"from_frame"`
	GainDB    *float64       `json:"gain_db"`
	FadeIn    *float64       `json:"fade_in"`  // API 1.106
	FadeOut   *float64       `json:"fade_out"` // API 1.106
}

// LaneNew is a lane as lane_add gives it.
type LaneNew struct {
	ID     *string `json:"id"`
	Name   string  `json:"name"`
	Role   string  `json:"role"`
	Hidden *bool   `json:"hidden"`
	Kind   *string `json:"kind"`
}

// fields is every field an op may carry; each op reads its own (opFields).
type fields struct {
	Op      string          `json:"op"`
	Block   json.RawMessage `json:"block"`
	Lane    json.RawMessage `json:"lane"`
	Mode    string          `json:"mode"`
	IDs     []string        `json:"ids"`
	ID      string          `json:"id"`
	Dt      *int            `json:"dt"`
	At      *int            `json:"at"`
	Length  *int            `json:"length"`
	To      []string        `json:"to"`
	Between *[2]string      `json:"between"`
	Start   *string         `json:"start"`
	End     *string         `json:"end"`
	Kind    string          `json:"kind"`
	Request json.RawMessage `json:"request"` // transition, a morph's (API 1.82)
	Pinned  json.RawMessage `json:"pinned"`
	Effect  json.RawMessage `json:"effect"`
	Index   *int            `json:"index"`
	Set     json.RawMessage `json:"set"`
	After   *string         `json:"after"`
	By      *int            `json:"by"`
	Asset   json.RawMessage `json:"asset"`
	Take    string          `json:"take"`
	On      *bool           `json:"on"` // API 1.106: ducking's
	DB      *float64        `json:"db"`
}

// opFields is each op's fields (MagicOp): the ones it takes, then the required ones.
var opFields = map[string][2][]string{
	"add":          {{"block", "mode"}, {"block", "mode"}},
	"move":         {{"ids", "dt", "lane", "mode"}, {"ids", "dt", "mode"}},
	"trim":         {{"id", "at", "length"}, {"id"}},
	"delete":       {{"ids", "mode"}, {"ids", "mode"}},
	"duplicate":    {{"ids"}, {"ids"}},
	"split":        {{"id", "at"}, {"id", "at"}},
	"link":         {{"block", "to"}, {"block", "to"}},
	"unlink":       {{"block"}, {"block"}},
	"transition":   {{"between", "start", "end", "kind", "length", "request", "pinned"}, {"kind"}},
	"effect":       {{"ids", "effect"}, {"ids", "effect"}},
	"effect_set":   {{"id", "index", "effect"}, {"id", "index", "effect"}},
	"block":        {{"id", "set"}, {"id", "set"}},
	"lane_add":     {{"lane", "after"}, {"lane"}},
	"lane":         {{"id", "set"}, {"id", "set"}},
	"lane_move":    {{"id", "by"}, {"id", "by"}},
	"lane_delete":  {{"id"}, {"id"}},
	"asset_delete": {{"id"}, {"id"}},
	"asset_add":    {{"asset"}, {"asset"}}, // API 1.83: assets, a block's own picture
	"asset":        {{"id", "set"}, {"id", "set"}},
	"take_add":     {{"id", "take"}, {"id", "take"}},
	"ducking":      {{"on", "db"}, {}}, // API 1.106: the board's Duck music
}

// AssetNew is an asset as asset_add gives it (its id optional).
type AssetNew struct {
	ID           *string           `json:"id"`
	Kind         string            `json:"kind"`
	Handle       string            `json:"handle"`
	Name         string            `json:"name"`
	Sheet        []string          `json:"sheet"`
	Views        map[string]string `json:"views"`
	VoiceID      *string           `json:"voice_id"`
	Note         *string           `json:"note"`
	ImportedFrom *string           `json:"imported_from"`
}

// handlePattern is an asset's @ handle (LibraryItem.handle's rule).
var handlePattern = regexp.MustCompile(`^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$`)

// AssetKinds are an asset's kinds.
var AssetKinds = []string{"character", "prop", "place"}

// unique is ids each once, in order (never nil).
func unique(ids []string) []string {
	out := []string{}
	for _, t := range ids {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// keepViews drops the names of takes no longer on a's sheet (none: no views).
func keepViews(a *Asset) {
	for id := range a.Views {
		if !slices.Contains(a.Sheet, id) {
			delete(a.Views, id)
		}
	}
	if len(a.Views) == 0 {
		a.Views = nil
	}
}

func (d *Doc) asset(id string) *Asset {
	for _, a := range d.Assets {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// FreshAssetID is the first unused a<n> among d's assets.
func (d *Doc) FreshAssetID() string {
	for n := 1; ; n++ {
		if id := fmt.Sprintf("a%d", n); d.asset(id) == nil {
			return id
		}
	}
}

// ReplaceMention is text with every mention of handle h (the Mentions grammar: `@@` is a literal @, an @ after a word
// character is text, a token is the run of letters, digits, _ and - minus trailing - and _) written as with.
func ReplaceMention(text, h, with string) string {
	word := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "@@") {
			out.WriteString("@@")
			i += 2
			continue
		}
		if text[i] == '@' && (i == 0 || !word(text[i-1])) {
			j := i + 1
			for j < len(text) && (word(text[j]) || text[j] == '-') {
				j++
			}
			tok := strings.TrimRight(text[i+1:j], "-_")
			if tok != "" && (tok[0]|0x20) >= 'a' && (tok[0]|0x20) <= 'z' && strings.EqualFold(tok, h) {
				out.WriteString(with)
			} else {
				out.WriteString(text[i : i+1+len(tok)])
			}
			i += 1 + len(tok)
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

// OpNames are the ops, sorted.
func OpNames() []string {
	var out []string
	for k := range opFields {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// strict decodes data into v, refusing unknown fields.
func strict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if f, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return fmt.Errorf("unknown field %s", strings.Trim(f, `"`))
		}
		return fmt.Errorf("invalid op: %v", strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

// Shape checks an op's name and fields (not whether it applies): "" or why not.
func Shape(op Op) string {
	spec, ok := opFields[op.Name]
	if !ok {
		return "unknown op " + strings.TrimSpace(fmt.Sprintf("%q", op.Name))
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(op.Raw, &keys); err != nil {
		return "an op is an object"
	}
	for k := range keys {
		if k != "op" && !slices.Contains(spec[0], k) {
			return "unknown field " + k
		}
	}
	for _, k := range spec[1] {
		if _, ok := keys[k]; !ok {
			return k + " is required"
		}
	}
	var f fields
	if err := strict(op.Raw, &f); err != nil {
		return err.Error()
	}
	if f.Mode != "" && f.Mode != "insert" && f.Mode != "overwrite" {
		return "mode must be insert or overwrite"
	}
	if _, ok := keys["ids"]; ok {
		if len(f.IDs) == 0 {
			return "ids must not be empty"
		}
		for i, id := range f.IDs {
			if slices.Contains(f.IDs[:i], id) {
				return "ids must be unique"
			}
		}
	}
	if _, ok := keys["to"]; ok && len(f.To) == 0 {
		return "to must not be empty"
	}
	if f.By != nil && *f.By != 1 && *f.By != -1 {
		return "by must be -1 or 1"
	}
	if f.Op == "transition" && !slices.Contains(TransitionKinds, f.Kind) {
		return "kind must be one of " + strings.Join(TransitionKinds, ", ")
	}
	if f.Length != nil && *f.Length < 0 || f.At != nil && *f.At < 0 || f.Index != nil && *f.Index < 0 {
		return "times and lengths are frames from 0"
	}
	return ""
}

// morphSet gives a morph t its request, takes and pin (API 1.82): kept from was while it stays a morph, then the op's
// request and pinned; another kind takes neither.
func morphSet(t, was *Transition, f fields) string {
	if t.Kind != "morph" {
		if len(f.Request) > 0 || len(f.Pinned) > 0 {
			return "Only a morph has a request."
		}
		return ""
	}
	if was != nil && was.Kind == "morph" {
		t.Request, t.Takes, t.Pinned = was.Request, was.Takes, was.Pinned
	}
	if len(f.Request) > 0 {
		var r map[string]any
		if json.Unmarshal(f.Request, &r) != nil || r["kind"] != "video" {
			return "A morph's request is a video's."
		}
		t.Request = r
	}
	if len(f.Pinned) > 0 {
		var p *string
		if json.Unmarshal(f.Pinned, &p) != nil || p != nil && !slices.Contains(t.Takes, *p) {
			return "That take isn't the morph's."
		}
		t.Pinned = p
	}
	return ""
}

// TransitionKinds are a transition's kinds.
var TransitionKinds = []string{"cut", "dissolve", "fade", "wipe", "morph", "crossfade"} // crossfade: API 1.106

// DefaultLength is the seconds a new transition of each kind lasts.
var DefaultLength = map[string]float64{"cut": 0, "dissolve": 0.5, "fade": 1, "wipe": 0.5, "morph": 1, "crossfade": 2}

// Applied is one op's result: the new document, or why it can't (the board's own words).
type Applied struct {
	Doc   Doc
	Error string
}

func refused(why string) Applied { return Applied{Error: why} }

// Apply applies one op to doc, all or nothing; doc is not changed. A malformed op is an Error too (Shape first for
// the 422 of a body that does not fit).
func Apply(doc Doc, op Op) Applied {
	r := apply(doc, op)
	if r.Error == "" {
		r.Doc = r.Doc.filled()
	}
	return r
}

func apply(doc Doc, op Op) Applied {
	if why := Shape(op); why != "" {
		return refused(why)
	}
	var f fields
	_ = strict(op.Raw, &f)
	d := doc.Clone()
	before := map[string]Block{}
	for _, b := range doc.Blocks {
		before[b.ID] = *b
	}
	if fn, ok := opApply[op.Name]; ok {
		return fn(&d, f, before)
	}
	return refused("unknown op " + op.Name)
}

// opApply is each op's apply: d is the clone it edits, before the blocks as they were.
var opApply = map[string]func(d *Doc, f fields, before map[string]Block) Applied{
	"add":          opAdd,
	"move":         opMove,
	"trim":         opTrim,
	"delete":       opDelete,
	"duplicate":    opDuplicate,
	"split":        opSplit,
	"link":         opLink,
	"unlink":       opUnlink,
	"transition":   opTransition,
	"effect":       opEffect,
	"effect_set":   opEffectSet,
	"block":        opBlock,
	"lane_add":     opLaneAdd,
	"lane":         opLane,
	"lane_move":    opLaneMove,
	"lane_delete":  opLaneDelete,
	"asset_delete": opAssetDelete,
	"asset_add":    opAssetAdd,
	"asset":        opAsset,
	"take_add":     opTakeAdd,
	"ducking":      opDucking, // API 1.106
}

// picked is the blocks of ids still on d.
func picked(d *Doc, ids []string) []*Block {
	var out []*Block
	for _, id := range ids {
		if b := d.block(id); b != nil {
			out = append(out, b)
		}
	}
	return out
}

// done tidies d and returns it.
func done(d *Doc) Applied { tidy(d); return Applied{Doc: *d} }

// opAdd applies the add op to d.
func opAdd(d *Doc, f fields, before map[string]Block) Applied {
	var nb BlockNew
	if err := strict(f.Block, &nb); err != nil {
		return refused("block: " + err.Error())
	}
	if d.lane(nb.Lane) == nil {
		return refused("That lane is gone.")
	}
	if nb.Length < 1 {
		return refused("A block needs a length.")
	}
	if nb.ID != nil && slices.Contains(d.ids(), *nb.ID) {
		return refused("That id is taken.")
	}
	b := Block{Lane: nb.Lane, At: nb.At, Length: nb.Length, Kind: nb.Kind, Name: nb.Name, Request: nb.Request,
		Takes: nb.Takes, Pinned: nb.Pinned, Note: nb.Note, Effects: nb.Effects, FromFrame: nb.FromFrame, GainDB: nb.GainDB,
		FadeIn: nonZero(nb.FadeIn), FadeOut: nonZero(nb.FadeOut)}
	if nb.ID != nil {
		b.ID = *nb.ID
	} else {
		b.ID = d.FreshID("b")
	}
	place(d, []*Block{&b}, b.Lane, b.At, f.Mode)
	follow(d, before, []string{b.ID})
	return done(d)
}

// opMove applies the move op to d.
func opMove(d *Doc, f fields, before map[string]Block) Applied {
	g := picked(d, f.IDs)
	if len(g) == 0 {
		return refused("Nothing to move.")
	}
	var from []string
	for _, b := range g {
		if !slices.Contains(from, b.Lane) {
			from = append(from, b.Lane)
		}
	}
	target := ""
	if f.Lane != nil {
		if err := json.Unmarshal(f.Lane, &target); err != nil {
			return refused("lane must be a lane id")
		}
		if d.lane(target) == nil {
			return refused("That lane is gone.")
		}
		if len(from) > 1 && slices.ContainsFunc(from, func(l string) bool { return l != target }) {
			return refused("Move blocks from one lane at a time.")
		}
	}
	copies := make([]*Block, len(g))
	for i, b := range g {
		c := clone(*b)
		copies[i] = &c
	}
	maps := lift(d, f.IDs, f.Mode)
	lanes := from
	if target != "" {
		lanes = []string{target}
	}
	for _, l := range lanes {
		var part []*Block
		lo := math.MaxInt
		for _, b := range copies {
			if target != "" || b.Lane == l {
				part = append(part, b)
				lo = min(lo, b.At)
			}
		}
		at := max(0, lo+*f.Dt)
		if m, ok := maps[l]; ok {
			at = m(at)
		}
		place(d, part, l, at, f.Mode)
	}
	follow(d, before, f.IDs)
	return done(d)
}

// opTrim applies the trim op to d.
func opTrim(d *Doc, f fields, before map[string]Block) Applied {
	b := d.block(f.ID)
	if b == nil {
		return refused("That block is gone.")
	}
	at := b.At
	if f.At != nil {
		at = *f.At
	}
	length := b.end() - at
	if f.Length != nil {
		length = *f.Length
	}
	if at < 0 || length < 1 {
		return refused("A block needs a length.")
	}
	for _, x := range d.Blocks {
		if x.Lane == b.Lane && x != b && x.At < at+length && x.end() > at {
			return refused("It would cover the block next to it.")
		}
	}
	b.At, b.Length = at, length
	follow(d, before, []string{b.ID})
	return done(d)
}

// opDelete applies the delete op to d.
func opDelete(d *Doc, f fields, before map[string]Block) Applied {
	if len(picked(d, f.IDs)) == 0 {
		return refused("Nothing to delete.")
	}
	lift(d, f.IDs, f.Mode)
	follow(d, before, nil)
	return done(d)
}

// opDuplicate applies the duplicate op to d.
func opDuplicate(d *Doc, f fields, before map[string]Block) Applied {
	g := picked(d, f.IDs)
	if len(g) == 0 {
		return refused("Nothing to duplicate.")
	}
	var lanes []string
	for _, b := range g {
		if !slices.Contains(lanes, b.Lane) {
			lanes = append(lanes, b.Lane)
		}
	}
	for _, l := range lanes {
		var part, copies []*Block
		last := 0
		for _, b := range g {
			if b.Lane == l {
				part = append(part, b)
				last = max(last, b.end())
			}
		}
		for _, b := range part {
			c := clone(*b)
			c.ID, c.Name, c.Pinned = d.FreshID("b"), b.Name+" copy", nil
			d.Blocks = append(d.Blocks, &c)
			copies = append(copies, &c)
		}
		d.without(func(b *Block) bool { return slices.Contains(copies, b) })
		place(d, copies, l, last, "insert")
	}
	follow(d, before, nil)
	return done(d)
}

// opSplit applies the split op to d.
func opSplit(d *Doc, f fields, before map[string]Block) Applied {
	b := d.block(f.ID)
	if b == nil || !(b.At < *f.At && *f.At < b.end()) {
		return refused("The playhead isn't on it.")
	}
	rest := clone(*b)
	rest.ID, rest.At, rest.Length = d.FreshID("b"), *f.At, b.end()-*f.At
	b.Length = *f.At - b.At
	d.Blocks = append(d.Blocks, &rest)
	for _, t := range d.Transitions {
		if t.Between != nil && t.Between[0] == b.ID {
			t.Between = &[2]string{rest.ID, t.Between[1]}
		}
	}
	return done(d)
}

// opLink applies the link op to d.
func opLink(d *Doc, f fields, before map[string]Block) Applied {
	var id string
	if json.Unmarshal(f.Block, &id) != nil {
		return refused("block must be a block id")
	}
	if d.block(id) == nil || slices.ContainsFunc(f.To, func(t string) bool { return d.block(t) == nil || t == id }) {
		return refused("Tie it to a shot.")
	}
	d.Links = append(slices.DeleteFunc(d.Links, func(l *Link) bool { return l.Block == id }),
		&Link{Block: id, To: f.To})
	return done(d)
}

// opUnlink applies the unlink op to d.
func opUnlink(d *Doc, f fields, before map[string]Block) Applied {
	var id string
	if json.Unmarshal(f.Block, &id) != nil {
		return refused("block must be a block id")
	}
	d.Links = slices.DeleteFunc(d.Links, func(l *Link) bool { return l.Block == id })
	return Applied{Doc: *d}
}

// opTransition applies the transition op to d.
func opTransition(d *Doc, f fields, before map[string]Block) Applied {
	n := 0
	for _, set := range []bool{f.Between != nil, f.Start != nil, f.End != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return refused("Pick a join.")
	}
	same := func(t *Transition) bool {
		switch {
		case f.Between != nil:
			return t.Between != nil && *t.Between == *f.Between
		case f.Start != nil:
			return t.Start != nil && *t.Start == *f.Start
		}
		return t.End != nil && *t.End == *f.End
	}
	var was *Transition
	for _, t := range d.Transitions {
		if same(t) {
			was = t
			break
		}
	}
	d.Transitions = slices.DeleteFunc(d.Transitions, same)
	if f.Between != nil {
		a, b := d.block(f.Between[0]), d.block(f.Between[1])
		if a == nil || b == nil || a.Lane != b.Lane || a.end() != b.At {
			return refused("Those blocks don't meet.")
		}
	}
	if f.Kind == "crossfade" && !crossfades(d, f.Between) { // API 1.106
		return refused("A crossfade joins two music or sound blocks.")
	}
	if f.Kind != "cut" {
		t := &Transition{Between: f.Between, Start: f.Start, End: f.End, Kind: f.Kind}
		switch {
		case was != nil:
			t.ID = was.ID
		default:
			t.ID = d.FreshID("t")
		}
		switch {
		case f.Length != nil:
			t.Length = *f.Length
		case was != nil:
			t.Length = was.Length
		default:
			t.Length = int(math.Round(DefaultLength[f.Kind] * float64(d.Target.FPS)))
		}
		if why := morphSet(t, was, f); why != "" {
			return refused(why)
		}
		d.Transitions = append(d.Transitions, t)
	}
	return done(d)
}

// opEffect applies the effect op to d.
func opEffect(d *Doc, f fields, before map[string]Block) Applied {
	var e Effect
	if err := strict(f.Effect, &e); err != nil {
		return refused("effect: " + err.Error())
	}
	for _, id := range f.IDs {
		if b := d.block(id); b != nil {
			b.Effects = append(b.Effects, clone(e))
		}
	}
	return Applied{Doc: *d}
}

// opEffectSet applies the effect_set op to d.
func opEffectSet(d *Doc, f fields, before map[string]Block) Applied {
	b := d.block(f.ID)
	if b == nil || *f.Index >= len(b.Effects) {
		return refused("That effect is gone.")
	}
	if string(f.Effect) == "null" {
		b.Effects = slices.Delete(b.Effects, *f.Index, *f.Index+1)
	} else {
		var e Effect
		if err := strict(f.Effect, &e); err != nil {
			return refused("effect: " + err.Error())
		}
		b.Effects[*f.Index] = e
	}
	return Applied{Doc: *d}
}

// opBlock applies the block op to d.
func opBlock(d *Doc, f fields, before map[string]Block) Applied {
	b := d.block(f.ID)
	if b == nil {
		return refused("That block is gone.")
	}
	var set struct {
		Name    *string         `json:"name"`
		Note    *string         `json:"note"`
		Request *map[string]any `json:"request"`
		Pinned  *string         `json:"pinned"`
		GainDB  *float64        `json:"gain_db"`
		FadeIn  *float64        `json:"fade_in"`  // API 1.106
		FadeOut *float64        `json:"fade_out"` // API 1.106
	}
	if err := strict(f.Set, &set); err != nil {
		return refused("set: " + err.Error())
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(f.Set, &keys)
	if set.Name != nil {
		b.Name = *set.Name
	}
	if set.Note != nil {
		b.Note = set.Note
	}
	if set.Request != nil {
		b.Request = *set.Request
	}
	if _, ok := keys["gain_db"]; ok { // API 1.101: 0 or null is no level; the board rules check kind and range
		b.GainDB = set.GainDB
		if b.GainDB != nil && *b.GainDB == 0 {
			b.GainDB = nil
		}
	}
	for _, k := range []struct { // API 1.106: 0 or null removes; another kind is refused
		key  string
		to   **float64
		from *float64
	}{{"fade_in", &b.FadeIn, set.FadeIn}, {"fade_out", &b.FadeOut, set.FadeOut}} {
		if _, ok := keys[k.key]; !ok {
			continue
		}
		if k.from != nil && *k.from != 0 && !slices.Contains(AudioKinds, b.Kind) {
			return refused(k.key + " is for voice, music and sound blocks")
		}
		*k.to = nonZero(k.from)
	}
	if _, ok := keys["pinned"]; ok { // null or "" unpins
		b.Pinned = set.Pinned
		if set.Pinned != nil && *set.Pinned == "" {
			b.Pinned = nil
		}
	}
	return Applied{Doc: *d}
}

// opDucking applies the ducking op to d (API 1.106): the board's Duck music, absent fields kept.
func opDucking(d *Doc, f fields, before map[string]Block) Applied {
	k := d.Ducks()
	k.DB = new(k.Amount())
	if f.On != nil {
		k.On = *f.On
	}
	if f.DB != nil {
		k.DB = new(*f.DB)
	}
	d.Ducking = &k
	return Applied{Doc: *d}
}

// nonZero is p, nil for 0.
func nonZero(p *float64) *float64 {
	if p == nil || *p == 0 {
		return nil
	}
	return new(*p)
}

// crossfades is whether between names two music or sound blocks (API 1.106: what a crossfade joins).
func crossfades(d *Doc, between *[2]string) bool {
	if between == nil {
		return false
	}
	for _, id := range between {
		if b := d.block(id); b == nil || (b.Kind != "music" && b.Kind != "sound") {
			return false
		}
	}
	return true
}

// opLaneAdd applies the lane_add op to d.
func opLaneAdd(d *Doc, f fields, before map[string]Block) Applied {
	var nl LaneNew
	if err := strict(f.Lane, &nl); err != nil {
		return refused("lane: " + err.Error())
	}
	if nl.ID != nil && slices.Contains(d.ids(), *nl.ID) {
		return refused("That id is taken.")
	}
	l := &Lane{Name: nl.Name, Role: nl.Role, Hidden: nl.Hidden, Kind: nl.Kind}
	if nl.ID != nil {
		l.ID = *nl.ID
	} else {
		l.ID = d.FreshID("l")
	}
	i := len(d.Lanes)
	if f.After != nil {
		if k := slices.IndexFunc(d.Lanes, func(x *Lane) bool { return x.ID == *f.After }); k >= 0 {
			i = k + 1
		}
	}
	d.Lanes = slices.Insert(d.Lanes, i, l)
	return Applied{Doc: *d}
}

// opLane applies the lane op to d.
func opLane(d *Doc, f fields, before map[string]Block) Applied {
	l := d.lane(f.ID)
	if l == nil {
		return refused("That lane is gone.")
	}
	var set struct {
		Name   *string `json:"name"`
		Role   *string `json:"role"`
		Hidden *bool   `json:"hidden"`
		Kind   *string `json:"kind"`
	}
	if err := strict(f.Set, &set); err != nil {
		return refused("set: " + err.Error())
	}
	if set.Name != nil && strings.TrimSpace(*set.Name) == "" {
		return refused("Give the lane a name.")
	}
	if set.Name != nil {
		l.Name = *set.Name
	}
	if set.Role != nil {
		l.Role = *set.Role
	}
	if set.Hidden != nil {
		l.Hidden = set.Hidden
	}
	if set.Kind != nil {
		l.Kind = set.Kind
	}
	return Applied{Doc: *d}
}

// opLaneMove applies the lane_move op to d.
func opLaneMove(d *Doc, f fields, before map[string]Block) Applied {
	i := slices.IndexFunc(d.Lanes, func(l *Lane) bool { return l.ID == f.ID })
	j := i + *f.By
	if i < 0 || j < 0 || j >= len(d.Lanes) {
		return refused("It can't go further.")
	}
	d.Lanes[i], d.Lanes[j] = d.Lanes[j], d.Lanes[i]
	return done(d)
}

// opLaneDelete applies the lane_delete op to d.
func opLaneDelete(d *Doc, f fields, before map[string]Block) Applied {
	if d.lane(f.ID) == nil {
		return refused("That lane is gone.")
	}
	d.Lanes = slices.DeleteFunc(d.Lanes, func(l *Lane) bool { return l.ID == f.ID })
	d.without(func(b *Block) bool { return b.Lane == f.ID })
	return done(d)
}

// opAssetDelete applies the asset_delete op to d.
func opAssetDelete(d *Doc, f fields, before map[string]Block) Applied {
	gone := d.asset(f.ID)
	d.Assets = slices.DeleteFunc(d.Assets, func(a *Asset) bool { return a.ID == f.ID })
	picks := func(r any) bool {
		m, _ := r.(map[string]any)
		a, has := m["asset"]
		return has && a == f.ID
	}
	for _, b := range d.Blocks {
		// its @ picks go (refs, the first and last frame; a voice stays as it is); its mentions in the prompt become its name
		if refs, ok := b.Request["refs"].([]any); ok {
			b.Request["refs"] = slices.DeleteFunc(slices.Clone(refs), picks)
		}
		for _, k := range []string{"first", "last"} {
			if picks(b.Request[k]) {
				delete(b.Request, k)
			}
		}
		if p, ok := b.Request["prompt"].(string); ok && gone != nil {
			b.Request["prompt"] = ReplaceMention(p, gone.Handle, gone.Name)
		}
	}
	return Applied{Doc: *d}
}

// opAssetAdd applies the asset_add op to d.
func opAssetAdd(d *Doc, f fields, before map[string]Block) Applied {
	var na AssetNew
	if err := strict(f.Asset, &na); err != nil {
		return refused("asset: " + err.Error())
	}
	switch {
	case na.ID != nil && d.asset(*na.ID) != nil:
		return refused("That id is taken.")
	case !slices.Contains(AssetKinds, na.Kind):
		return refused("kind must be character, prop or place")
	case strings.TrimSpace(na.Name) == "":
		return refused("Give it a name.")
	case !handlePattern.MatchString(na.Handle):
		return refused("A handle starts with a letter: letters, digits, - or _.")
	case slices.ContainsFunc(d.Assets, func(a *Asset) bool { return strings.EqualFold(a.Handle, na.Handle) }):
		return refused("That handle is taken.")
	}
	a := &Asset{Kind: na.Kind, Handle: na.Handle, Name: na.Name, Sheet: na.Sheet, Views: na.Views,
		VoiceID: na.VoiceID, Note: na.Note, ImportedFrom: na.ImportedFrom}
	if na.ID != nil {
		a.ID = *na.ID
	} else {
		a.ID = d.FreshAssetID()
	}
	a.Sheet = unique(a.Sheet)
	keepViews(a)
	d.Assets = append(d.Assets, a)
	return Applied{Doc: *d}
}

// opAsset applies the asset op to d.
func opAsset(d *Doc, f fields, before map[string]Block) Applied {
	a := d.asset(f.ID)
	if a == nil {
		return refused("That asset is gone.")
	}
	var set struct {
		Kind    *string            `json:"kind"`
		Name    *string            `json:"name"`
		Note    *string            `json:"note"`
		VoiceID *string            `json:"voice_id"`
		Sheet   *[]string          `json:"sheet"`
		Views   *map[string]string `json:"views"`
	}
	if err := strict(f.Set, &set); err != nil {
		return refused("set: " + err.Error())
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(f.Set, &keys)
	switch {
	case set.Kind != nil && !slices.Contains(AssetKinds, *set.Kind):
		return refused("kind must be character, prop or place")
	case set.Name != nil && strings.TrimSpace(*set.Name) == "":
		return refused("Give it a name.")
	}
	if set.Kind != nil {
		a.Kind = *set.Kind
	}
	if set.Name != nil {
		a.Name = *set.Name
	}
	if set.Note != nil {
		a.Note = set.Note
	}
	if _, ok := keys["voice_id"]; ok { // null or "" takes it off
		a.VoiceID = set.VoiceID
		if set.VoiceID != nil && *set.VoiceID == "" {
			a.VoiceID = nil
		}
	}
	if set.Sheet != nil {
		a.Sheet = unique(*set.Sheet)
	}
	if set.Views != nil {
		a.Views = *set.Views
	}
	keepViews(a)
	return Applied{Doc: *d}
}

// opTakeAdd applies the take_add op to d.
func opTakeAdd(d *Doc, f fields, before map[string]Block) Applied {
	b := d.block(f.ID)
	if b == nil {
		return refused("That block is gone.")
	}
	if !slices.Contains(b.Takes, f.Take) {
		if len(b.Takes) >= 200 {
			return refused("A block keeps at most 200 takes.")
		}
		b.Takes = append(b.Takes, f.Take)
	}
	b.Pinned = &f.Take
	return Applied{Doc: *d}
}
