package magic

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/helmstudio/studio_service/api/timeline"
)

// Send to Timeline (API 1.82, contracts/README.md "Magic: Send to Timeline"): the sequence a board becomes, built
// from its document, the takes it shows and their statuses. Pure: studio stores the sequence and runs the bakes.

// SendGoes is MagicSendGoes.
type SendGoes struct {
	Lane   string `json:"lane"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Blocks int    `json:"blocks"`
	Track  string `json:"track"`
	Linked int    `json:"linked"`
}

// SendJoin is MagicSendJoin.
type SendJoin struct {
	Transition string `json:"transition"`
	Kind       string `json:"kind"`
	How        string `json:"how"`
}

// SendEffect is one effect badge on a shot that goes.
type SendEffect struct {
	Block string `json:"block"`
	Name  string `json:"name"`
	How   string `json:"how"`
}

// SendBakeSide is one side of a bake: a take, the seconds of it read, held frames before and after, its effects.
type SendBakeSide struct {
	TakeID    string
	Start     float64
	End       float64
	Rate      float64
	Pre, Post int
	Filters   []string
}

// SendBake is one bake the sequence waits for: the V1 clip it fills, its kind and frames, its sides.
type SendBake struct {
	Clip   string
	Kind   string // fade, wipe, dissolve, fade_in, fade_out, effects
	Title  string
	Frames int
	Sides  []SendBakeSide
}

// Send is the sequence a board becomes: its document as written (a baked clip is an empty slot of its length, named
// by a bake), the bakes, and what the dialog says.
type Send struct {
	Doc      timeline.Doc
	Bakes    []SendBake
	Goes     []SendGoes
	Joins    []SendJoin
	Effects  []SendEffect
	Warnings []string
}

// short is a block's name before " · " ("Shot 2 · umbrella" → "Shot 2").
func short(b *Block) string {
	n, _, _ := strings.Cut(b.Name, " · ")
	return cmp.Or(n, b.ID)
}

var joinNames = map[string]string{"dissolve": "Dissolve", "fade": "Fade", "wipe": "Wipe", "morph": "Morph", "fade_in": "Fade in",
	"fade_out": "Fade out"}

// sendShot is a V1 block with what it shows: its take, how many board frames of it there are, its effects.
type sendShot struct {
	b          *Block
	take       string
	kind       string  // video, image, or "" (no take)
	avail      int     // board frames its take covers from its start (an image: all of them)
	srcOff     int     // board frames into its take its clip starts (a kept dissolve's handle)
	rate       float64 // Slow
	filters    []string
	head, tail int      // board frames a bake or a morph takes from its start and its end
	headClip   *v1Piece // what fills its head (a fade in's bake)
	after      *v1Piece // what follows it (its join's bake or morph)
	dissolve   int      // a kept dissolve into it: its frames
}

type v1Piece struct {
	id     string
	take   string // a morph's take; "" for a bake
	frames int
	avail  int // a morph take's board frames
}

// sendBuild is BuildSend's work in progress: the board, its takes, the Send so far and its V1 shots.
type sendBuild struct {
	d         Doc
	takes     map[string]timeline.Take
	fps       float64
	out       Send
	shotsLane *Lane
	shots     []*sendShot
	byID      map[string]*sendShot
	// API 1.106: each sound join's outgoing block → its length; each sound lane's fade in and out (board frames)
	xfades    map[string]int
	laneFades map[string][2]int
}

func (sb *sendBuild) warn(f string, a ...any) {
	sb.out.Warnings = append(sb.out.Warnings, fmt.Sprintf(f, a...))
}

// s is board frames f in seconds.
func (sb *sendBuild) s(f int) float64 { return float64(f) / sb.fps }

// shown is the take a block or morph shows: its pinned one, else its latest ("" none).
func shown(takes []string, pinned *string) string {
	switch {
	case pinned != nil:
		return *pinned
	case len(takes) > 0:
		return takes[len(takes)-1]
	}
	return ""
}

// BuildSend is board d as a new sequence. takes are the takes it shows (a missing one is offline: a gap); status is
// each block's derived status. A block's shown take is its pinned one, else its latest; turns are a dialogue's takes,
// one per turn ("" none), played one after another (API 1.89).
// keep: a dissolve whose takes have the media past each cut goes as the Timeline's own (false: every dissolve is baked,
// for when the Timeline's rules refuse one).
func BuildSend(d Doc, takes map[string]timeline.Take, status map[string]string, turns map[string][]string, keep bool) Send {
	sb := &sendBuild{d: d, takes: takes, fps: float64(d.Target.FPS), out: Send{Goes: []SendGoes{}, Joins: []SendJoin{}, Effects: []SendEffect{}, Warnings: []string{}},
		xfades: map[string]int{}, laneFades: map[string][2]int{}}
	out, warn, fps := &sb.out, sb.warn, sb.fps
	target := timeline.Target{Width: d.Target.Width, Height: d.Target.Height, FPS: fps, SampleRate: d.Target.SampleRate}
	if _, _, ok := timeline.Rate(fps); !ok {
		warn("The Timeline has no %d fps: the sequence is 24 fps", d.Target.FPS)
		target.FPS = 24
	}
	if target.SampleRate != 44100 && target.SampleRate != 48000 {
		warn("The Timeline's sound is 44100 or 48000 Hz: the sequence is 48000 Hz")
		target.SampleRate = 48000
	}
	var shotsLane *Lane
	for _, l := range d.Lanes {
		if l.Role == "shots" {
			shotsLane = l
			break
		}
	}
	sb.shotsLane = shotsLane
	sb.collectShots(status)
	sb.joins(sb.v1Transitions(), keep)
	sb.effectBakes()
	v1, cursor := sb.layV1()
	shots := sb.shots
	tracks := []timeline.Track{{Kind: "video", Clips: v1}}
	if shotsLane != nil {
		n := 0
		for _, sh := range shots {
			if sh.take != "" {
				n++
			}
		}
		out.Goes = append(out.Goes, SendGoes{Lane: shotsLane.ID, Name: shotsLane.Name, Kind: "video", Blocks: n, Track: "V1"})
	}
	onV1 := map[string]bool{}
	for _, c := range v1 {
		onV1[c.ID] = true
	}
	tracks = sb.sound(tracks, turns, cursor, onV1)
	// clip ids on the Timeline's pattern (c1, c2, … in track order); links and bakes follow
	ids := map[string]string{}
	for ti := range tracks {
		for ci := range tracks[ti].Clips {
			c := &tracks[ti].Clips[ci]
			ids[c.ID] = "c" + strconv.Itoa(len(ids)+1)
			c.ID = ids[c.ID]
		}
	}
	for ti := range tracks[1:] {
		for ci := range tracks[ti+1].Clips {
			if c := &tracks[ti+1].Clips[ci]; c.Link != nil {
				to := ids[*c.Link]
				c.Link = &to
			}
		}
	}
	for i := range out.Bakes {
		out.Bakes[i].Clip = ids[out.Bakes[i].Clip]
	}
	ducks := d.Ducks() // API 1.106: written out (on at -12 when the board has none)
	ducks.DB = new(ducks.Amount())
	out.Doc = timeline.Doc{Target: target, Tracks: tracks, Ducking: &ducks}
	return *out
}

// val is p, 0 for none.
func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// collectShots is V1: the first Shots lane's blocks in time, with what each shows.
func (sb *sendBuild) collectShots(status map[string]string) {
	d, takes, out, warn, fps, shotsLane := sb.d, sb.takes, &sb.out, sb.warn, sb.fps, sb.shotsLane
	var shots []*sendShot
	byID := map[string]*sendShot{}
	if shotsLane != nil {
		for _, b := range d.Blocks {
			if b.Lane != shotsLane.ID {
				continue
			}
			sh := &sendShot{b: b, rate: 1}
			if id := shown(b.Takes, b.Pinned); id != "" {
				tk, ok := takes[id]
				switch {
				case !ok:
					warn("%s's take is gone: a gap", short(b))
				case tk.Kind == "audio":
					warn("%s is sound: it stays on the board", short(b))
				default:
					sh.take, sh.kind = id, tk.Kind
					sh.avail = b.Length
					if tk.Kind == "video" {
						sh.avail = int(math.Floor(tk.DurationS*fps + 1e-6))
					}
				}
			}
			for _, e := range b.Effects {
				if e.Off != nil && *e.Off {
					continue
				}
				f, r, ok := timeline.EffectFilters(e.Name, e.Params)
				how := "baked"
				if !ok || sh.take == "" {
					how = "skipped"
					warn("%s's %s isn't baked", short(b), e.Name)
				} else {
					sh.filters, sh.rate = append(sh.filters, f...), sh.rate*r
				}
				out.Effects = append(out.Effects, SendEffect{Block: b.ID, Name: e.Name, How: how})
			}
			if sh.kind == "video" && sh.rate != 1 {
				sh.avail = int(math.Floor(float64(sh.avail) / sh.rate))
			}
			if sh.kind == "image" {
				sh.avail = math.MaxInt32
			}
			switch st := status[b.ID]; {
			case sh.take == "" && len(b.Takes) == 0:
				warn("%s has no take: a gap", short(b))
			case sh.take != "" && st == "out_of_date":
				warn("%s is out of date: sends take %d as it is", short(b), slices.Index(b.Takes, sh.take)+1)
			case sh.take != "" && st == "running":
				warn("%s is still running: sends take %d", short(b), slices.Index(b.Takes, sh.take)+1)
			}
			if sh.take != "" && sh.kind == "video" && sh.avail < b.Length {
				warn("%s's take is %s shorter than its block: the rest is a gap", short(b), secs(b.Length-sh.avail, d.Target.FPS))
			}
			shots = append(shots, sh)
			byID[b.ID] = sh
		}
	}
	sb.shots, sb.byID = shots, byID
}

// fits: the frames of sh's own clip after its head and tail stay at least one.
func fits(sh *sendShot, head, tail int) bool { return sh.b.Length-head-tail >= 1 }

// side is board frames [w0, w1) from sh's start: what its take has of them, its first or last frame held for the rest.
func (sb *sendBuild) side(sh *sendShot, w0, w1 int) SendBakeSide {
	s := sb.s
	lo, hi := max(w0, 0), min(w1, sh.avail-sh.srcOff)
	if sh.kind == "image" {
		return SendBakeSide{TakeID: sh.take, Filters: sh.filters}
	}
	held := hi <= lo
	if held { // nothing of it there: its nearest frame held for the whole span
		lo = max(min(lo, sh.avail-sh.srcOff-1), 0)
		hi = lo + 1
	}
	pre, post := lo-w0, w1-hi
	if held {
		pre, post = 0, w1-w0-1
	}
	return SendBakeSide{TakeID: sh.take, Start: s(sh.srcOff+lo) * sh.rate, End: s(sh.srcOff+hi) * sh.rate, Rate: sh.rate,
		Pre: pre, Post: post, Filters: sh.filters}
}

func (sb *sendBuild) addBake(clip, kind, title string, frames int, sides ...SendBakeSide) {
	sb.out.Bakes = append(sb.out.Bakes, SendBake{Clip: clip, Kind: kind, Title: title, Frames: frames, Sides: sides})
}

// v1Transitions are the transitions on V1. From API 1.106 a join of two sound blocks goes as the outgoing clip's
// crossfade and a sound lane's fade in and out as its first and last block's clip fades (sound picks them up).
func (sb *sendBuild) v1Transitions() []*Transition {
	d, out, warn, byID, shotsLane := sb.d, &sb.out, sb.warn, sb.byID, sb.shotsLane
	var v1Transitions []*Transition
	for _, t := range d.Transitions {
		switch {
		case t.Between != nil && byID[t.Between[0]] != nil && byID[t.Between[1]] != nil,
			shotsLane != nil && (t.Start != nil && *t.Start == shotsLane.ID || t.End != nil && *t.End == shotsLane.ID):
			v1Transitions = append(v1Transitions, t)
		case t.Between != nil && func() bool { a, _ := SoundJoin(d, t); return a != nil }():
			a, _ := SoundJoin(d, t)
			sb.xfades[a.ID] = t.Length
			out.Joins = append(out.Joins, SendJoin{Transition: t.ID, Kind: t.Kind, How: "crossfade"})
		case t.Between != nil || t.Start != nil || t.End != nil:
			lane := ""
			if t.Between != nil {
				if b := d.block(t.Between[0]); b != nil {
					lane = b.Lane
				}
			} else {
				lane = cmpOr(t.Start, t.End)
			}
			if l := d.lane(lane); l != nil && slices.Contains([]string{"voice", "music", "sound"}, l.Role) {
				kind, f := t.Kind, sb.laneFades[l.ID]
				switch {
				case t.Start != nil:
					kind, f[0] = "fade_in", t.Length
				case t.End != nil:
					kind, f[1] = "fade_out", t.Length
				}
				if kind == "fade_in" || kind == "fade_out" {
					sb.laneFades[l.ID] = f
					out.Joins = append(out.Joins, SendJoin{Transition: t.ID, Kind: kind, How: "fades"})
					continue
				}
				out.Joins = append(out.Joins, SendJoin{Transition: t.ID, Kind: kind, How: "skipped"})
				warn("%s on %s isn't sent: it joins blocks that aren't both sound", joinNames[kind], l.Name)
			}
		}
	}
	return v1Transitions
}

// joins bakes, keeps or cuts each V1 transition.
func (sb *sendBuild) joins(v1Transitions []*Transition, keep bool) {
	takes, out, warn, fps, shots, byID, side, addBake := sb.takes, &sb.out, sb.warn, sb.fps, sb.shots, sb.byID, sb.side, sb.addBake
	// the lane's fade in and out first (they take the first block's head and the last one's tail), then the joins
	// in time, so a kept dissolve's handle (the second block's clip starting into its take) is known before that
	// block's next join reads it
	at := func(t *Transition) int {
		if t.Between == nil {
			return -1
		}
		return byID[t.Between[1]].b.At
	}
	slices.SortStableFunc(v1Transitions, func(a, b *Transition) int { return cmp.Compare(at(a), at(b)) })
	for _, t := range v1Transitions {
		L := t.Length
		if t.Between == nil {
			kind, sh := "fade_in", (*sendShot)(nil)
			if len(shots) > 0 {
				sh = shots[0]
			}
			if t.End != nil {
				kind = "fade_out"
				if len(shots) > 0 {
					sh = shots[len(shots)-1]
				}
			}
			join := SendJoin{Transition: t.ID, Kind: kind, How: "baked"}
			switch {
			case L < 1 || sh == nil:
				join.How = "cut"
			case sh.take == "":
				join.How = "cut"
				warn("%s needs %s's take: not sent", joinNames[kind], short(sh.b))
			case kind == "fade_in" && !fits(sh, L, sh.tail), kind == "fade_out" && !fits(sh, sh.head, L):
				join.How = "cut"
				warn("%s is too short for its %s: not sent", short(sh.b), strings.ToLower(joinNames[kind]))
			case kind == "fade_in":
				sh.head = L
				sh.headClip = &v1Piece{id: "bake-" + t.ID, frames: L}
				addBake(sh.headClip.id, kind, joinNames[kind]+" · "+short(sh.b), L, side(sh, 0, L))
			default:
				sh.tail = L
				addBake("bake-"+t.ID, kind, joinNames[kind]+" · "+short(sh.b), L, side(sh, sh.b.Length-L, sh.b.Length))
				sh.after = &v1Piece{id: "bake-" + t.ID, frames: L}
			}
			out.Joins = append(out.Joins, join)
			continue
		}
		a, b := byID[t.Between[0]], byID[t.Between[1]]
		title := short(a.b) + " → " + short(b.b) + " · " + joinNames[t.Kind]
		h1 := L / 2
		h2 := L - h1
		join := SendJoin{Transition: t.ID, Kind: t.Kind, How: "baked"}
		switch {
		case L < 1:
			join.How = "cut"
		case a.take == "" || b.take == "":
			join.How = "cut"
			warn("%s needs both takes: sent as a cut", title)
		case !fits(a, a.head, h1+a.tail) || !fits(b, h2+b.head, b.tail):
			join.How = "cut"
			warn("%s is too long for its shots: sent as a cut", title)
		case t.Kind == "morph":
			m := shown(t.Takes, t.Pinned)
			tk, ok := takes[m]
			if m == "" || !ok || tk.Kind != "video" {
				join.How = "cut"
				warn("%s has no take: sent as a cut", title)
				break
			}
			join.How = "clip"
			a.tail += h1
			b.head += h2
			a.after = &v1Piece{id: "morph-" + t.ID, take: m, frames: L, avail: int(math.Floor(tk.DurationS*fps + 1e-6))}
			if a.after.avail < L {
				warn("%s's take is shorter than the morph: the rest is a gap", title)
			}
		case keep && t.Kind == "dissolve" && a.kind == "video" && b.kind == "video" && len(a.filters) == 0 && len(b.filters) == 0 &&
			a.rate == 1 && b.rate == 1 && a.tail == 0 && b.head == 0 && a.srcOff+a.b.Length+h1 <= a.avail &&
			h1+b.b.Length <= b.avail && h1 <= a.b.Length-a.head && h1+h2 <= b.b.Length:
			join.How = "kept" // the Timeline's own: the takes have the media past each cut
			b.srcOff, b.dissolve = h1, L
		default:
			a.tail += h1
			b.head += h2
			addBake("bake-"+t.ID, t.Kind, title, L, side(a, a.b.Length-h1, a.b.Length+h2), side(b, -h1, h2))
			a.after = &v1Piece{id: "bake-" + t.ID, frames: L}
		}
		out.Joins = append(out.Joins, join)
	}
}

// effectBakes: each shot's own clip between its head and tail is baked with its effects.
func (sb *sendBuild) effectBakes() {
	shots, side, addBake := sb.shots, sb.side, sb.addBake
	for _, sh := range shots {
		if len(sh.filters) == 0 && sh.rate == 1 || sh.take == "" {
			continue
		}
		lo, hi := sh.head, sh.b.Length-sh.tail
		if sh.kind == "video" {
			hi = min(hi, sh.avail-sh.srcOff)
		}
		if hi > lo {
			addBake(sh.b.ID, "effects", short(sh.b)+" · effects", hi-lo, side(sh, lo, hi))
		}
	}
}

// layV1 lays V1 out in time; cursor is its end in board frames.
func (sb *sendBuild) layV1() (v1 []timeline.Clip, cursor int) {
	s, out, shots := sb.s, &sb.out, sb.shots
	baked := map[string]bool{}
	for _, bk := range out.Bakes {
		baked[bk.Clip] = true
	}
	slot := func(id string, frames int) {
		if frames > 0 {
			v1 = append(v1, timeline.Clip{ID: id, Hold: new(s(frames)), Alternatives: []string{}})
		}
	}
	piece := func(p *v1Piece) {
		if p == nil {
			return
		}
		if p.take == "" { // a bake: an empty slot until its take is made
			slot(p.id, p.frames)
			return
		}
		n := min(p.frames, p.avail)
		take := p.take
		v1 = append(v1, timeline.Clip{ID: p.id, TakeID: &take, In: new(0.0), Out: new(s(n)), Alternatives: []string{}})
		slot(p.id+"-rest", p.frames-n)
	}
	cursor = 0
	for i, sh := range shots {
		slot("gap-"+strconv.Itoa(i), sh.b.At-cursor)
		piece(sh.headClip)
		own := sh.b.Length - sh.head - sh.tail
		switch {
		case baked[sh.b.ID]: // its effects' bake
			n := own
			if sh.kind == "video" {
				n = min(own, max(sh.avail-sh.srcOff-sh.head, 0))
			}
			slot(sh.b.ID, n)
			slot(sh.b.ID+"-rest", own-n)
		case sh.kind == "video":
			n := min(own, max(sh.avail-sh.srcOff-sh.head, 0))
			if n > 0 {
				take := sh.take
				c := timeline.Clip{ID: sh.b.ID, TakeID: &take, In: new(s(sh.srcOff + sh.head)), Out: new(s(sh.srcOff + sh.head + n)),
					Alternatives: []string{}}
				if sh.dissolve > 0 {
					c.TransitionIn = &timeline.Dissolve{Type: "dissolve", Frames: sh.dissolve}
				}
				v1 = append(v1, c)
			}
			slot(sh.b.ID+map[bool]string{true: "-rest", false: ""}[n > 0], own-n)
		case sh.kind == "image":
			take := sh.take
			v1 = append(v1, timeline.Clip{ID: sh.b.ID, TakeID: &take, Hold: new(s(own)), Alternatives: []string{}})
		default:
			slot(sh.b.ID, own)
		}
		piece(sh.after)
		cursor = sh.b.At + sh.b.Length
	}
	return v1, cursor
}

// sound puts the voice, music and sound lanes (and free lanes' sound) on audio tracks after tracks, linked to their
// shots; end is V1's end in board frames.
func (sb *sendBuild) sound(tracks []timeline.Track, turns map[string][]string, end int, onV1 map[string]bool) []timeline.Track {
	d, takes, out, warn, fps, s, shots, shotsLane := sb.d, sb.takes, &sb.out, sb.warn, sb.fps, sb.s, sb.shots, sb.shotsLane
	links := map[string]string{}
	for _, l := range d.Links {
		if len(l.To) > 0 && onV1[l.To[0]] {
			links[l.Block] = l.To[0]
		}
	}
	for _, l := range d.Lanes {
		if l == shotsLane || l.Role == "storyboard" {
			continue
		}
		var clips []timeline.Clip
		pictures, linked, blocks := 0, 0, 0
		var mine []*Block
		for _, b := range d.Blocks {
			if b.Lane == l.ID {
				mine = append(mine, b)
			}
		}
		for bi, b := range mine {
			first := len(clips)
			// API 1.89: a dialogue's turns one after another, a Loop's take again and again, cut at the block's end
			pieces, dialogue := turns[b.ID]
			if !dialogue {
				pieces = []string{shown(b.Takes, b.Pinned)}
			}
			loop, _ := b.Request["loop"].(bool)
			from, _ := b.Request["from"].(string)
			loop = loop && from == "" // API 1.90: a sound made from a shot's video follows the shot
			at, stop, made := b.At, b.At+b.Length, 0
		piece:
			for i := 0; i < len(pieces) && at < stop; i++ {
				id := pieces[i]
				tk, ok := takes[id]
				switch {
				case (id == "" || !ok) && dialogue:
					warn("%s: line %d has no take", short(b), i+1)
					continue
				case id == "" || !ok:
					if slices.Contains([]string{"voice", "music", "sound"}, b.Kind) && l.Role != "free" {
						warn("%s has no take: not sent", short(b))
					}
					continue
				case tk.Kind != "audio":
					pictures++
					continue
				}
				n := min(stop-at, int(math.Floor(tk.DurationS*fps+1e-6)))
				if shotsLane != nil && len(shots) > 0 {
					switch {
					case at >= end:
						if made == 0 {
							warn("%s starts after the last shot: not sent", short(b))
						}
						break piece
					case at+n > end:
						warn("%s is cut at the last shot's end", short(b))
						n = end - at
					}
				}
				if n < 1 {
					break
				}
				take := id
				cid := b.ID
				if made > 0 {
					cid = b.ID + "." + strconv.Itoa(made)
				}
				c := timeline.Clip{ID: cid, TakeID: &take, In: new(0.0), Out: new(s(n)), At: new(s(at)), Alternatives: []string{}}
				if b.GainDB != nil { // API 1.101: the block's level is its clips' gain
					c.GainDB = new(*b.GainDB)
				}
				if to, ok := links[b.ID]; ok && made == 0 {
					c.Link = &to
					linked++
				}
				clips = append(clips, c)
				at += n
				made++
				// ponytail: a Loop stops at 100 clips a block; a longer block of a very short sound is cut there
				if loop && !dialogue && i == len(pieces)-1 && made < 100 {
					i = -1
				}
			}
			if made > 0 {
				blocks++
				// API 1.106: the block's fades on its first and last clip (a lane's fade in and out on its first and last
				// block, the longer of the two), a sound join after it as its last clip's crossfade
				in, out, f := val(b.FadeIn), val(b.FadeOut), sb.laneFades[l.ID]
				if bi == 0 {
					in = max(in, sb.s(f[0]))
				}
				if bi == len(mine)-1 {
					out = max(out, sb.s(f[1]))
				}
				lastClip := &clips[len(clips)-1]
				if in > 0 {
					clips[first].FadeIn = new(in)
				}
				if out > 0 {
					lastClip.FadeOut = new(out)
				}
				if n := sb.xfades[b.ID]; n > 0 {
					lastClip.Crossfade = new(sb.s(n))
				}
			}
		}
		if pictures > 0 && slices.Contains([]string{"shots", "free"}, l.Role) {
			warn("%s stays here: the Timeline has one picture track", l.Name)
		}
		if len(clips) == 0 {
			continue
		}
		if len(tracks) > timeline.MaxAudioTracks {
			warn("%s stays here: the Timeline has %d sound tracks", l.Name, timeline.MaxAudioTracks)
			continue
		}
		tracks = append(tracks, timeline.Track{Kind: "audio", Clips: clips})
		kind := l.Role
		if kind == "free" {
			kind = "audio"
		}
		out.Goes = append(out.Goes, SendGoes{Lane: l.ID, Name: l.Name, Kind: kind, Blocks: blocks,
			Track: "A" + strconv.Itoa(len(tracks)), Linked: linked})
	}
	for _, l := range d.Lanes {
		if l != shotsLane && l.Role == "shots" && slices.ContainsFunc(d.Blocks, func(b *Block) bool { return b.Lane == l.ID && len(b.Takes) > 0 }) {
			if !slices.ContainsFunc(out.Warnings, func(w string) bool { return strings.HasPrefix(w, l.Name+" stays here") }) {
				warn("%s stays here: the Timeline has one picture track", l.Name)
			}
		}
	}
	return tracks
}
