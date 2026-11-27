package studio

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Magic's standard request and runs (API 1.81, plan 39 phase D; contracts/README.md "Magic: the standard request",
// "Magic: runs"): a block's request is turned by the workflow's `standard` block into a normal run in the board's own
// session; magic_runs keeps run → block with the hash of the block's inputs, so the projector adds each take to its
// block and the status (draft, running, done, out of date, failed) is derived.

// magicKindFields are each kind's shared fields in form order (size = width and height).
var magicKindFields = map[string][]string{
	"image": {"prompt", "refs", "size", "seed"},
	"video": {"prompt", "first", "last", "length", "size", "seed"},
	"voice": {"line", "voice", "seed"},
	"frame": {"prompt", "refs", "size", "seed"}, // API 1.83: a board frame is a still of an image model
	// API 1.85 (plan 39 phase H): music's style is its prompt; a sound may be made from a shot's video
	"music": {"prompt", "lyrics", "length", "key", "bpm", "seed"},
	"sound": {"from", "prompt", "length", "seed"},
}

// magicServes is the standard kind that serves a block kind: a board frame is a still (API 1.83).
func magicServes(kind string) string {
	if kind == "frame" {
		return "image"
	}
	return kind
}

// magicKindOrder is the kinds as GET /magic/kinds lists them (Add's order).
var magicKindOrder = []string{"video", "image", "voice", "music", "sound", "frame"}

const maxBlockRuns = 20 // the runs of one block a board answers

var magicFieldWords = map[string]string{"prompt": "a prompt", "line": "a line", "refs": "references",
	"first": "a first frame", "last": "a last frame", "length": "a length", "size": "a size", "seed": "a seed",
	"voice": "a voice", "lyrics": "lyrics", "key": "a key", "bpm": "a BPM", "from": "a shot's video"}

// modelFields are the shared fields of kind a standard block fills, in form order.
func modelFields(kind string, st *registry.Standard) []string {
	out := []string{}
	for _, f := range magicKindFields[kind] {
		_, ok := st.Fields[f]
		if f == "size" {
			_, w := st.Fields["width"]
			_, h := st.Fields["height"]
			ok = w && h
		}
		if ok {
			out = append(out, f)
		}
	}
	return out
}

// standardModels are the workflows that serve kind as models: the default first (API 1.85), then the fastest declared
// (then by id); the first is the kind's default. A model's video workflow (`from`, API 1.85) is not one of them: it
// is the model's `video` (videoOf).
func standardModels(ws []registry.Workflow, kind string) []registry.Workflow {
	var out []registry.Workflow
	for _, w := range ws {
		if w.Standard != nil && w.Standard.Kind == magicServes(kind) && !isVideoOf(ws, w) {
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b registry.Workflow) int {
		sa, sb := math.Inf(1), math.Inf(1)
		if a.Standard.Seconds != nil {
			sa = *a.Standard.Seconds
		}
		if b.Standard.Seconds != nil {
			sb = *b.Standard.Seconds
		}
		return cmp.Or(cmp.Compare(boolInt(!a.Standard.Default), boolInt(!b.Standard.Default)), cmp.Compare(sa, sb),
			strings.Compare(a.ID, b.ID))
	})
	return out
}

// takesVideo is whether w's standard block fills `from` (a shot's video).
func takesVideo(w registry.Workflow) bool { _, ok := w.Standard.Fields["from"]; return ok }

// isVideoOf is whether w is another workflow's video one: it takes a video and a workflow of its kind and model
// name takes none (MMAudio Sound from a video beside MMAudio Sound).
func isVideoOf(ws []registry.Workflow, w registry.Workflow) bool {
	return takesVideo(w) && slices.ContainsFunc(ws, func(x registry.Workflow) bool {
		return x.Standard != nil && x.ID != w.ID && x.Standard.Kind == w.Standard.Kind && x.Standard.Model == w.Standard.Model &&
			!takesVideo(x)
	})
}

// videoOf is model m's video workflow (m itself when it takes a video), nil without.
func videoOf(ws []registry.Workflow, m registry.Workflow) *registry.Workflow {
	if takesVideo(m) {
		return &m
	}
	for _, w := range ws {
		if w.Standard != nil && w.ID != m.ID && w.Standard.Kind == m.Standard.Kind && w.Standard.Model == m.Standard.Model &&
			takesVideo(w) {
			return &w
		}
	}
	return nil
}

// advancedParams are a model's Advanced fields: every unwired param a form can set that its standard block does not
// fill, node id order then param order, with the workflow's own value as the default.
func advancedParams(w registry.Workflow) []dto.MagicParam {
	filled := map[string]bool{}
	for _, p := range w.Standard.Fields {
		filled[p] = true
	}
	for p := range w.Standard.Set {
		filled[p] = true
	}
	out := []dto.MagicParam{}
	for _, id := range slices.Sorted(maps.Keys(w.Nodes)) {
		node := w.Nodes[id]
		for _, p := range w.NodeSchemas[node.Kind].Params {
			path := id + "." + p.Name
			_, wired := node.In[p.Name]
			if wired || filled[path] || slices.Contains([]string{"rows", "image", "media", "voice", "span"}, p.Type) {
				continue
			}
			if v, ok := node.Params[p.Name]; ok {
				p.Default = v
			}
			out = append(out, dto.MagicParam{Path: path, Param: p})
		}
	}
	return out
}

// magicKinds is GET /magic/kinds from the registry's workflows.
func magicKinds(ws []registry.Workflow) dto.MagicKinds {
	out := dto.MagicKinds{Kinds: []dto.MagicKindInfo{}}
	for _, kind := range magicKindOrder {
		info := dto.MagicKindInfo{Kind: kind, Fields: magicKindFields[kind], Models: []dto.MagicModel{}}
		for _, w := range standardModels(ws, kind) {
			fields := modelFields(kind, w.Standard)
			var video *dto.MagicVideo
			if v := videoOf(ws, w); v != nil && v.ID != w.ID { // one model, two workflows: its fields are both's
				video = &dto.MagicVideo{WorkflowID: v.ID, Title: v.Title, Advanced: advancedParams(*v)}
				for _, f := range modelFields(kind, v.Standard) {
					if !slices.Contains(fields, f) {
						fields = append(fields, f)
					}
				}
				fields = slices.DeleteFunc(slices.Clone(magicKindFields[kind]), func(f string) bool { return !slices.Contains(fields, f) })
			}
			greyed := map[string]string{}
			for _, f := range magicKindFields[kind] {
				if !slices.Contains(fields, f) {
					greyed[f] = w.Standard.Model + " can't take " + magicFieldWords[f]
				}
			}
			info.Models = append(info.Models, dto.MagicModel{WorkflowID: w.ID, Name: w.Standard.Model, Title: w.Title, Env: w.Env,
				Fields: fields, Greyed: greyed, Seconds: w.Standard.Seconds, Advanced: advancedParams(w), Video: video})
		}
		out.Kinds = append(out.Kinds, info)
	}
	return out
}

// MagicKinds answers GET /magic/kinds.
func (s *Service) MagicKinds(ctx context.Context, caller common.Caller) (dto.MagicKinds, error) {
	if !s.registry.Ready() {
		return dto.MagicKinds{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	return magicKinds(s.registry.Workflows()), nil
}

// ------------------------------------------------------------------ the resolver

// reqString, reqNumber and reqRef read a MagicRequest's fields (it is kept as JSON).
func reqString(req map[string]any, k string) string { v, _ := req[k].(string); return v }

func reqNumber(req map[string]any, k string) (float64, bool) {
	v, ok := req[k].(float64)
	return v, ok
}

func reqRefs(req map[string]any) []map[string]any {
	var out []map[string]any
	list, _ := req["refs"].([]any)
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// magicModel is the workflow a block's request runs: its `model` when that serves the kind, else the kind's first; a
// request with `from` (a sound from a shot's video, API 1.85) runs the model's video workflow, and with no `model` the
// kind's first model that takes a video.
func magicModel(ws []registry.Workflow, kind string, req map[string]any) (registry.Workflow, string) {
	models := standardModels(ws, kind)
	if req["morph"] == true { // API 1.82: a morph's model takes a first and a last frame
		models = morphModels(models)
	}
	from := reqString(req, "from") != ""
	pick := func(w registry.Workflow) (registry.Workflow, string) {
		if !from {
			return w, ""
		}
		if v := videoOf(ws, w); v != nil {
			return *v, ""
		}
		return w, w.Standard.Model + " can't take " + magicFieldWords["from"]
	}
	if want := reqString(req, "model"); want != "" {
		for _, w := range models {
			if v := videoOf(ws, w); w.ID == want || (v != nil && v.ID == want) { // a model by its video workflow's id too
				return pick(w)
			}
		}
		return registry.Workflow{}, "its model " + want + " is on no host"
	}
	if len(models) == 0 {
		return registry.Workflow{}, "no model for " + kind + " is on any host"
	}
	if from {
		for _, w := range models {
			if videoOf(ws, w) != nil {
				return pick(w)
			}
		}
	}
	return pick(models[0])
}

// whyNot is why block b of d cannot run ("" when it can): its kind, a dialogue, no model, a pick its model needs.
func whyNot(ws []registry.Workflow, d magic.Doc, b *magic.Block) string {
	if isDialogue(b) { // API 1.89: each turn runs as a line
		turns := dialogueTurns(b)
		if len(turns) == 0 {
			return "A dialogue needs a line"
		}
		for i, t := range turns {
			line := "Line " + strconv.Itoa(i+1)
			text, _ := t["text"].(string)
			speaker, _ := t["speaker"].(string)
			a := findAsset(d, speaker)
			switch {
			case strings.TrimSpace(text) == "":
				return line + " has no words"
			case a == nil:
				return line + " has no speaker"
			case a.VoiceID == nil:
				return line + "'s @" + a.Handle + " has no voice"
			}
		}
		if w, why := magicModel(ws, b.Kind, b.Request); why != "" {
			return why
		} else if _, ok := w.Standard.Fields["line"]; !ok {
			return w.Standard.Model + " can't take " + magicFieldWords["line"]
		}
		return whyNot(ws, d, turnBlock(b, 0))
	}
	w, why := magicModel(ws, b.Kind, b.Request)
	if why != "" {
		return why
	}
	// a first frame on an image node that may not stay empty (MiniMax H3's), and a voice, are required
	if path, ok := w.Standard.Fields["first"]; ok {
		nodeID, _, _ := strings.Cut(path, ".")
		if n := w.Nodes[nodeID]; n.Kind == "image" && !n.Optional {
			if first, _, _ := pickRefs(d, b); !picksSomething(d, first) {
				return w.Standard.Model + " needs a first frame"
			}
		}
	}
	if _, ok := w.Standard.Fields["voice"]; ok && blockVoice(d, b) == "" {
		return w.Standard.Model + " needs a voice"
	}
	if from := reqString(b.Request, "from"); from != "" { // API 1.85: the shot whose video the sound is made from
		shot := findBlock(d, from)
		switch {
		case shot == nil || shot.Kind != "video":
			return "its shot is not on the board"
		case shownTake(shot) == "":
			return strings.Split(shot.Name, " · ")[0] + " has no take yet"
		}
	}
	return ""
}

// picksSomething is whether pick r names a block or an asset with a sheet that is on the board.
func picksSomething(d magic.Doc, r map[string]any) bool {
	if r == nil {
		return false
	}
	if id, ok := r["asset"].(string); ok {
		a := findAsset(d, id)
		return a != nil && len(a.Sheet) > 0
	}
	id, _ := r["block"].(string)
	return findBlock(d, id) != nil
}

// blockVoice is the voice block b speaks in: its voice asset's, else its voice_id ("" none).
func blockVoice(d magic.Doc, b *magic.Block) string {
	if a := findAsset(d, reqString(b.Request, "voice")); a != nil && a.VoiceID != nil {
		return *a.VoiceID
	}
	return reqString(b.Request, "voice_id")
}

// isDialogue is whether block b is a dialogue: a voice block with turns (API 1.89).
func isDialogue(b *magic.Block) bool {
	_, ok := b.Request["turns"].([]any)
	return ok && b.Kind == "voice"
}

// dialogueTurns are a dialogue's turns ({speaker, text}), nil for any other block.
func dialogueTurns(b *magic.Block) []map[string]any {
	if !isDialogue(b) {
		return nil
	}
	var out []map[string]any
	for _, t := range b.Request["turns"].([]any) {
		m, _ := t.(map[string]any)
		out = append(out, m) // a malformed turn keeps its place (the document check refuses one anyway)
	}
	return out
}

// turnBlock is turn i of dialogue b as the line block its run is made from: the turn's text its line, its speaker
// its voice, the rest of b's request (one run, no saved voice).
func turnBlock(b *magic.Block, i int) *magic.Block {
	req := maps.Clone(b.Request)
	delete(req, "turns")
	delete(req, "count")
	delete(req, "voice_id")
	if t := dialogueTurns(b)[i]; t != nil {
		req["line"], req["voice"] = t["text"], t["speaker"]
	}
	tb := *b
	tb.Request = req
	return &tb
}

// turnHashes are dialogue b's turns' inputs hashed now, each as its line block.
func turnHashes(d magic.Doc, b *magic.Block) []string {
	turns := dialogueTurns(b)
	out := make([]string, len(turns))
	for i := range turns {
		out[i] = inputsHash(d, turnBlock(b, i))
	}
	return out
}

// runTurn is the dialogue turn run r is for now (API 1.90): the turn it ran for while its inputs match that turn's,
// else the turn they match now (a line moved, or one before it removed), else the turn it ran for; -1 for no turn.
func runTurn(hashes []string, r magicRunRow) int {
	if r.Turn == nil {
		return -1
	}
	if i := *r.Turn; i < len(hashes) && hashes[i] == r.InputsHash {
		return i
	}
	if j := slices.Index(hashes, r.InputsHash); j >= 0 {
		return j
	}
	return *r.Turn
}

// hostOf is block b's Run on host (API 1.89), nil for Auto.
func hostOf(b *magic.Block) *string {
	if h := reqString(b.Request, "host"); h != "" {
		return &h
	}
	return nil
}

// magicPics are a block's pictures as take ids: its first and last frame and its refs ("" none); RefAssets is the
// asset each ref's picture is of ("" a block's), so the prompt can name its row.
type magicPics struct {
	First, Last string
	Refs        []string
	RefAssets   []string
}

// magicStyle and magicFraming are the words a frame's style and framing add to its prompt (and a shot made from it).
var magicStyle = map[string]string{
	"sketch": "A storyboard sketch: loose pencil lines, black and white",
	"rough":  "A rough storyboard drawing: simple shapes, grey shading",
	"full":   "A finished storyboard frame: full colour, cinematic light",
}

var magicFraming = map[string]map[string]string{
	"size":  {"wide": "wide shot", "medium": "medium shot", "close": "close-up", "over_shoulder": "over-the-shoulder shot"},
	"angle": {"eye": "at eye level", "low": "from a low angle", "high": "from a high angle"},
	"move":  {"still": "the camera holds still", "push_in": "the camera slowly pushes in", "pan": "the camera pans", "follow": "the camera follows the action"},
}

// framingWords are a request's framing parts as words ("wide shot, at eye level"), the move apart.
func framingWords(req map[string]any) (shot, move string) {
	fr, _ := req["framing"].(map[string]any)
	word := func(k string) string { v, _ := fr[k].(string); return magicFraming[k][v] }
	var parts []string
	for _, k := range []string{"size", "angle"} {
		if w := word(k); w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, ", "), word("move")
}

// sentences joins parts as sentences: each trimmed, empty ones left out, a capital first and one full stop each.
func sentences(parts ...string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimRight(strings.TrimSpace(p), ". ")
		if p != "" {
			out = append(out, strings.ToUpper(p[:1])+p[1:]+".")
		}
	}
	return strings.Join(out, " ")
}

// magicPrompt is the prompt block b's run sends: its own; a frame's after its style and framing, with who and where
// it names; a shot made from a frame with no prompt of its own, the frame's framing, words and move. The board's
// @handles become their names, with the row of the asset's picture when the model takes references (`mention`,
// <image{n}>), so Create's resolver never reads them as elements.
func magicPrompt(d magic.Doc, b *magic.Block, mention string, refAssets []string) string {
	text := reqString(b.Request, "prompt")
	switch {
	case b.Kind == "frame":
		shot, _ := framingWords(b.Request)
		var with []string           // who and where the frame picks that its words don't name
		picked := map[string]bool{} // every asset it picks, with a picture or not
		for _, r := range reqRefs(b.Request) {
			if id, ok := r["asset"].(string); ok {
				picked[id] = true
			}
		}
		for _, a := range d.Assets {
			if picked[a.ID] && magic.ReplaceMention(text, a.Handle, "") == text {
				with = append(with, "@"+a.Handle)
			}
		}
		feat := ""
		if len(with) > 0 {
			feat = "With " + strings.Join(with, ", ")
		}
		text = sentences(magicStyle[cmp.Or(reqString(b.Request, "style"), "sketch")], shot, text, feat)
	case strings.TrimSpace(text) == "" && b.FromFrame != nil:
		if f := findBlock(d, *b.FromFrame); f != nil {
			shot, move := framingWords(f.Request)
			text = sentences(shot, reqString(f.Request, "prompt"), move)
		}
	}
	for _, a := range d.Assets {
		name := a.Name
		if i := slices.Index(refAssets, a.ID); i >= 0 && mention != "" {
			name += " " + strings.ReplaceAll(mention, "{n}", strconv.Itoa(i+1))
		}
		text = magic.ReplaceMention(text, a.Handle, name)
	}
	return text
}

// fitSize is w × h scaled down to fit the int params' maxes, each side then on its step and in its bounds.
func fitSize(w, h float64, pw, ph registry.Param) (float64, float64) {
	scale := 1.0
	if pw.Max != nil {
		scale = min(scale, *pw.Max/w)
	}
	if ph.Max != nil {
		scale = min(scale, *ph.Max/h)
	}
	return onGrid(w*scale, pw), onGrid(h*scale, ph)
}

// onGrid is v on p's grid (min + k × step, else whole numbers) within its bounds.
func onGrid(v float64, p registry.Param) float64 {
	lo, step := 0.0, 1.0
	if p.Min != nil {
		lo = *p.Min
	}
	if p.Step != nil && *p.Step > 0 {
		step = *p.Step
	}
	v = lo + math.Round((v-lo)/step)*step
	if p.Max != nil && v > *p.Max {
		v = lo + math.Floor((*p.Max-lo)/step)*step
	}
	return math.Max(v, lo)
}

// itemField is the name of a rows param's item field of type typ (an Image port: "Image"), or of the item param with
// position seconds (typ "seconds").
func rowField(p registry.Param, typ string) string {
	for _, name := range slices.Sorted(maps.Keys(p.Item)) {
		raw := p.Item[name]
		var port string
		if json.Unmarshal(raw, &port) == nil && port == typ {
			return name
		}
		var item registry.Param
		if typ == "seconds" && json.Unmarshal(raw, &item) == nil && item.Position == "seconds" {
			return name
		}
	}
	return ""
}

// standardInputs is a run's inputs for block b's request on workflow w (its standard block): each shared field the
// model fills on its param, `set` and `advanced` under them; voiceID is the voice it speaks in ("" none).
func standardInputs(w registry.Workflow, d magic.Doc, b *magic.Block, pics magicPics, voiceID string, seed int64) map[string]any {
	st, req, target := w.Standard, b.Request, d.Target
	inputs := map[string]any{}
	if adv, ok := req["advanced"].(map[string]any); ok { // only this model's own: a key kept from another is dropped
		for _, p := range advancedParams(w) {
			if v, ok := adv[p.Path]; ok {
				inputs[p.Path] = v
			}
		}
	}
	maps.Copy(inputs, st.Set)
	param := func(field string) (string, registry.Param, bool) {
		path, ok := st.Fields[field]
		if !ok {
			return "", registry.Param{}, false
		}
		p, found := findParam(w, path)
		return path, p, found
	}
	if path, _, ok := param("prompt"); ok {
		mention := ""
		if _, refs, ok := param("refs"); ok {
			mention = refs.Mention
		}
		inputs[path] = magicPrompt(d, b, mention, pics.RefAssets)
	}
	if path, _, ok := param("line"); ok {
		inputs[path] = reqString(req, "line")
	}
	// API 1.85: music's lyrics (empty: an instrumental), key (only one of the param's values) and BPM
	if path, _, ok := param("lyrics"); ok {
		inputs[path] = reqString(req, "lyrics")
	}
	if path, _, ok := param("instrumental"); ok {
		inputs[path] = strings.TrimSpace(reqString(req, "lyrics")) == ""
	}
	if path, p, ok := param("key"); ok && slices.Contains(p.Enum, reqString(req, "key")) {
		inputs[path] = reqString(req, "key")
	}
	if path, p, ok := param("bpm"); ok {
		if v, ok := reqNumber(req, "bpm"); ok && v > 0 {
			inputs[path] = onGrid(v, p)
		}
	}
	if path, _, ok := param("from"); ok {
		if shot := findBlock(d, reqString(req, "from")); shot != nil && shownTake(shot) != "" {
			inputs[path] = map[string]any{"take_id": shownTake(shot)}
		}
	}
	if path, _, ok := param("seed"); ok {
		inputs[path] = float64(seed)
	}
	if pw, w1, ok := param("width"); ok {
		if ph, h1, ok := param("height"); ok {
			wd, hd := float64(target.Width), float64(target.Height)
			if v, ok := reqNumber(req, "width"); ok {
				wd = v
			}
			if v, ok := reqNumber(req, "height"); ok {
				hd = v
			}
			inputs[pw], inputs[ph] = fitSize(wd, hd, w1, h1)
		}
	}
	// the clip's length in the workflow's frames (its fps param's value), on the frames param's grid
	frames, fps := 0.0, float64(target.FPS)
	if path, p, ok := param("length"); ok && p.Type != "frames" { // API 1.85: a float param is the block's seconds
		inputs[path] = onGrid(float64(b.Length)/float64(target.FPS), p)
	} else if ok {
		nodeID, _, _ := strings.Cut(path, ".")
		node := w.Nodes[nodeID]
		if v, ok := number(merged(w.NodeSchemas[node.Kind], node.Params)[p.FPS]); ok && v > 0 {
			fps = v
		}
		frames = onGrid(math.Round(float64(b.Length)/float64(target.FPS)*fps), p)
		inputs[path] = frames
	}
	if path, p, ok := param("refs"); ok {
		image, rows := rowField(p, "Image"), []any{}
		for _, t := range pics.Refs {
			if p.Max != nil && float64(len(rows)) >= *p.Max {
				break
			}
			rows = append(rows, map[string]any{image: map[string]any{"take_id": t}})
		}
		inputs[path] = rows
	}
	rowsOf := map[string][]any{}
	for _, end := range []struct{ field, take string }{{"first", pics.First}, {"last", pics.Last}} {
		path, p, ok := param(end.field)
		switch {
		case !ok:
			continue
		case p.Type == "image":
			if end.take != "" {
				inputs[path] = map[string]any{"take_id": end.take}
			}
			continue
		}
		if _, seen := rowsOf[path]; !seen {
			rowsOf[path] = []any{}
		}
		if end.take == "" {
			continue
		}
		row := map[string]any{rowField(p, "Image"): map[string]any{"take_id": end.take}}
		if at := rowField(p, "seconds"); at != "" {
			s := 0.0
			if end.field == "last" && frames > 0 {
				s = math.Floor((frames-1)/fps*10) / 10 // the clip's last frame, on the 0.1 s grid
			}
			row[at] = s
		}
		rowsOf[path] = append(rowsOf[path], row)
	}
	for path, rows := range rowsOf {
		nodeID, _, _ := strings.Cut(path, ".")
		if len(rows) == 0 { // no frame: the node goes (text to video), when it can
			if w.NodeSchemas[w.Nodes[nodeID].Kind].Passthrough() != nil {
				inputs[nodeID+".bypass"] = true
			}
			continue
		}
		inputs[path] = rows
		if w.NodeSchemas[w.Nodes[nodeID].Kind].Passthrough() != nil {
			inputs[nodeID+".bypass"] = false
		}
	}
	if path, _, ok := param("voice"); ok && voiceID != "" {
		inputs[path] = map[string]any{"voice_id": voiceID}
	}
	return inputs
}

// shownTake is the take block b shows: its pinned one, else its latest ("" none).
func shownTake(b *magic.Block) string {
	if b.Pinned != nil {
		return *b.Pinned
	}
	if len(b.Takes) > 0 {
		return b.Takes[len(b.Takes)-1]
	}
	return ""
}

func findBlock(d magic.Doc, id string) *magic.Block {
	for _, b := range d.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func findAsset(d magic.Doc, id string) *magic.Asset {
	for _, a := range d.Assets {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// pickRefs are a block's picture picks: first, last (from_frame's take when it has no first) and refs; missing
// blocks and assets are left out.
func pickRefs(d magic.Doc, b *magic.Block) (first, last map[string]any, refs []map[string]any) {
	first, _ = b.Request["first"].(map[string]any)
	last, _ = b.Request["last"].(map[string]any)
	if first == nil && b.FromFrame != nil {
		first = map[string]any{"block": *b.FromFrame, "part": "take"}
	}
	return first, last, reqRefs(b.Request)
}

// inputsHash is what block b's take depends on, hashed: its request, its length, the takes its picks show (an asset:
// its sheet and voice) and the frame it is made from. A take whose run's hash differs is out of date.
func inputsHash(d magic.Doc, b *magic.Block) string {
	pick := func(r map[string]any) any {
		if r == nil {
			return nil
		}
		if id, ok := r["asset"].(string); ok {
			if a := findAsset(d, id); a != nil {
				return []any{a.Sheet, a.VoiceID}
			}
			return nil
		}
		id, _ := r["block"].(string)
		if x := findBlock(d, id); x != nil {
			return []any{shownTake(x), r["part"]}
		}
		return nil
	}
	first, last, refs := pickRefs(d, b)
	var picked []any
	for _, r := range refs {
		picked = append(picked, pick(r))
	}
	var voice any
	if id, ok := b.Request["voice"].(string); ok {
		voice = pick(map[string]any{"asset": id})
	}
	// API 1.89: Run on and Loop are not inputs (left out only when set, so a hash kept from before still matches)
	req := b.Request
	if _, h := req["host"]; h || req["loop"] != nil {
		req = maps.Clone(req)
		delete(req, "host")
		delete(req, "loop")
	}
	// a shot made from a frame follows the frame's request too (its words make the shot's prompt)
	// (appended only then, so a hash kept from before 1.83 still matches)
	parts := []any{req, b.Length, pick(first), pick(last), picked, voice}
	if b.FromFrame != nil {
		if x := findBlock(d, *b.FromFrame); x != nil {
			parts = append(parts, x.Request)
		}
	}
	// API 1.85: a sound made from a shot's video follows the shot's take (appended only then, as above)
	if from := reqString(b.Request, "from"); from != "" {
		parts = append(parts, pick(map[string]any{"block": from}))
	}
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// magicPicture is the picture take of a pick: an asset's first sheet take; a block's shown take, or (a video) its
// first or last frame take, made at once as POST /takes/{id}/frame does; "" for a pick of nothing.
func (s *Service) magicPicture(ctx context.Context, tx pgx.Tx, ownerID string, d magic.Doc, r map[string]any, end string) (string, error) {
	if r == nil {
		return "", nil
	}
	if id, ok := r["asset"].(string); ok {
		if a := findAsset(d, id); a != nil && len(a.Sheet) > 0 {
			return a.Sheet[0], nil
		}
		return "", nil
	}
	id, _ := r["block"].(string)
	x := findBlock(d, id)
	if x == nil {
		return "", nil
	}
	name := strings.Split(x.Name, " · ")[0]
	take := shownTake(x)
	if take == "" {
		return "", network.NewUnprocessableError(name+" has no take yet", nil)
	}
	_, rows, err := loadTakes(ctx, tx, ownerID, []string{take})
	if err != nil {
		return "", err
	}
	row, ok := rows[take]
	switch {
	case !ok:
		return "", network.NewUnprocessableError(name+"'s take is gone", nil)
	case row.Kind == "image":
		return take, nil
	case row.Kind != "video":
		return "", network.NewUnprocessableError(name+" has no picture", nil)
	}
	part, _ := r["part"].(string)
	if part != "first" && part != "last" {
		part = end
	}
	// in a transaction of its own, so a frame job it starts outlives this request's (a 409 or a skip rolls it back)
	frameID := ""
	err = s.tx(ctx, common.Caller{ID: ownerID}, func(tx pgx.Tx) error {
		frame, _, err := s.frameOf(ctx, tx, ownerID, row, part)
		if frame != nil {
			frameID = frame.ID
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if frameID == "" {
		return "", network.NewConflictError(name+"'s "+part+" frame is being made; try again in a moment", nil)
	}
	return frameID, nil
}

// ------------------------------------------------------------------ runs, takes and status

type magicRunRow struct {
	Block, RunID, InputsHash string
	Seed                     *int64
	Via, Label               *string
	CreatedAt                time.Time
	WorkflowID, Status       string
	Progress                 json.RawMessage
	Error                    *string
	Takes                    []string
	TakeKinds                []string // each take's kind, in Takes' order
	RenderS                  *float64
	Turn                     *int // API 1.89: a dialogue's turn
}

// takeFits is whether a take of kind takeKind joins a block of kind (API 1.85): a voice, music or sound block takes
// only audio, so MMAudio's video with the sound stays out of its sound block; the others take every take a run makes.
func takeFits(kind, takeKind string) bool {
	return takeKind == "audio" || !slices.Contains([]string{"voice", "music", "sound"}, kind)
}

// fitTakes are the takes of run r that fit block b (all of them with no block).
func fitTakes(b *magic.Block, r magicRunRow) []string {
	if b == nil || len(r.TakeKinds) != len(r.Takes) {
		return r.Takes
	}
	out := []string{}
	for i, t := range r.Takes {
		if takeFits(b.Kind, r.TakeKinds[i]) {
			out = append(out, t)
		}
	}
	return out
}

// boardRuns are the board's runs, the last 20 of each block (of each dialogue turn), oldest first; deleted runs and takes are left out.
func boardRuns(ctx context.Context, tx pgx.Tx, boardID string) ([]magicRunRow, error) {
	return collect[magicRunRow](ctx, tx, `SELECT m.block, m.run_id::text, m.inputs_hash, m.seed, m.via, m.label, m.created_at,
		r.workflow_id, r.status, r.progress, r.error,
		coalesce((SELECT array_agg(o.take_id::text ORDER BY o.idx) FROM run_outputs o JOIN takes t ON t.id = o.take_id
			AND t.deleted_at IS NULL WHERE o.run_id = m.run_id), '{}'),
		coalesce((SELECT array_agg(t.kind::text ORDER BY o.idx) FROM run_outputs o JOIN takes t ON t.id = o.take_id
			AND t.deleted_at IS NULL WHERE o.run_id = m.run_id), '{}'),
		(SELECT (t.made_with->>'render_s')::float8 FROM run_outputs o JOIN takes t ON t.id = o.take_id
			WHERE o.run_id = m.run_id ORDER BY o.idx LIMIT 1), m.turn
		FROM (SELECT *, row_number() OVER (PARTITION BY block, turn ORDER BY created_at DESC) AS n FROM magic_runs
			WHERE board_id = $1 AND asset IS NULL) m
		JOIN runs r ON r.id = m.run_id AND r.deleted_at IS NULL
		WHERE m.n <= $2 ORDER BY m.created_at, m.run_id`, boardID, maxBlockRuns)
}

// runPercent is a run's progress as a percentage (its last step of), nil without.
func runPercent(raw json.RawMessage) *int {
	var p struct{ Step, Of float64 }
	if json.Unmarshal(raw, &p) != nil || p.Of <= 0 {
		return nil
	}
	v := int(math.Round(min(max(p.Step/p.Of, 0), 1) * 100))
	return &v
}

var openRun = map[string]bool{"queued": true, "running": true}

// blockStatuses derives every block's status from the document and its runs (contracts/README.md, Magic: runs); a
// dialogue's from its turns' (API 1.89).
func blockStatuses(d magic.Doc, runs []magicRunRow) map[string]dto.MagicStatus {
	out := map[string]dto.MagicStatus{}
	for _, b := range withMorphs(d) { // API 1.82: morphs too
		var mine []magicRunRow
		for _, r := range runs {
			if r.Block == b.ID {
				mine = append(mine, r)
			}
		}
		if isDialogue(b) {
			out[b.ID] = dialogueStatus(d, b, mine)
		} else {
			out[b.ID] = runStatus(mine, shownTake(b), inputsHash(d, b))
		}
	}
	return out
}

// runStatus is the status of a block (or a dialogue's turn) with runs mine that shows take, its inputs hashed now.
func runStatus(mine []magicRunRow, take, hash string) dto.MagicStatus {
	open := -1
	for i, r := range mine {
		if openRun[r.Status] {
			open = i // the newest open run's progress
		}
	}
	if open >= 0 {
		return dto.MagicStatus{Status: "running", Progress: runPercent(mine[open].Progress)}
	}
	if take == "" {
		if n := len(mine); n > 0 && mine[n-1].Status == "failed" {
			return dto.MagicStatus{Status: "failed", Error: cmp.Or(deref(mine[n-1].Error), "failed")}
		}
		return dto.MagicStatus{Status: "draft"}
	}
	for _, r := range mine {
		if slices.Contains(r.Takes, take) && r.InputsHash != hash {
			return dto.MagicStatus{Status: "out_of_date"}
		}
	}
	return dto.MagicStatus{Status: "done"}
}

// dialogueStatus is dialogue b's status from each turn's (a turn plays its newest run's take still on the block):
// running when a turn is, failed when one failed with no take, draft when none has a take, out of date when one is
// out of date or has no take yet, else done.
func dialogueStatus(d magic.Doc, b *magic.Block, mine []magicRunRow) dto.MagicStatus {
	out := dto.MagicStatus{Status: "done", Turns: []dto.MagicTurnStatus{}}
	hashes := turnHashes(d, b)
	takes, drafts := 0, 0
	for i := range hashes {
		var runs []magicRunRow
		take, now := "", "" // its newest take, its newest made from its inputs now (API 1.90)
		for _, r := range mine {
			if runTurn(hashes, r) != i {
				continue
			}
			runs = append(runs, r)
			for _, t := range r.Takes {
				if slices.Contains(b.Takes, t) {
					take = t
					if r.InputsHash == hashes[i] {
						now = t
					}
				}
			}
		}
		take = cmp.Or(now, take)
		st := runStatus(runs, take, hashes[i])
		ts := dto.MagicTurnStatus{Status: st.Status, Progress: st.Progress, Error: st.Error}
		if take != "" {
			ts.Take = &take
			takes++
		}
		out.Turns = append(out.Turns, ts)
		switch {
		case out.Status == "running":
		case st.Status == "running":
			out.Status, out.Progress, out.Error = "running", st.Progress, ""
		case out.Status == "failed":
		case st.Status == "failed" && take == "":
			out.Status, out.Error = "failed", "Line "+strconv.Itoa(i+1)+": "+st.Error
		case st.Status == "draft":
			drafts++
		case st.Status == "out_of_date":
			out.Status = "out_of_date"
		}
	}
	switch {
	case out.Status == "running" || out.Status == "failed":
	case takes == 0:
		out.Status = "draft"
	case drafts > 0:
		out.Status = "out_of_date"
	}
	return out
}

// boardExtras fills a board's session, runs, takes and statuses.
func (s *Service) boardExtras(ctx context.Context, tx pgx.Tx, row boardRow, out *dto.MagicBoard) error {
	out.SessionID = row.SessionID
	runs, err := boardRuns(ctx, tx, row.ID)
	if err != nil {
		return err
	}
	d := out.Doc
	out.Runs, out.Takes = []dto.MagicBlockRun{}, map[string]dto.MagicTake{}
	byTake := map[string]string{}
	hashes := map[string][]string{} // a dialogue's turn hashes, once a block
	for _, r := range runs {
		by := "user"
		if deref(r.Via) == "assistant" {
			by = "assistant"
		}
		current := false
		b := runBlockOf(d, r.Block)
		switch {
		case b != nil && isDialogue(b) && r.Turn != nil: // API 1.89; 1.90: the turn it is for now
			if _, ok := hashes[b.ID]; !ok {
				hashes[b.ID] = turnHashes(d, b)
			}
			turn := runTurn(hashes[b.ID], r)
			current = turn < len(hashes[b.ID]) && r.InputsHash == hashes[b.ID][turn]
			r.Turn = &turn
		case b != nil && r.Turn == nil:
			current = r.InputsHash == inputsHash(d, b)
		}
		r.Takes = fitTakes(b, r) // API 1.85: a sound block's run lists its audio take
		out.Runs = append(out.Runs, dto.MagicBlockRun{Block: r.Block, RunID: r.RunID, WorkflowID: r.WorkflowID,
			Status: r.Status, Progress: runPercent(r.Progress), Error: r.Error, Takes: r.Takes, Seed: r.Seed,
			Current: current, By: by, CreatedAt: r.CreatedAt, RenderS: r.RenderS, Style: r.Label, Turn: r.Turn})
		for _, t := range r.Takes {
			byTake[t] = r.RunID
		}
	}
	out.Status = blockStatuses(d, runs)
	if out.AssetRuns, err = assetRuns(ctx, tx, row.ID); err != nil {
		return err
	}
	_, rows, err := loadTakes(ctx, tx, row.OwnerID, magic.TakeIDs(d))
	if err != nil {
		return err
	}
	for id, t := range rows {
		td := gallery.TakeDTO(s.signer, t)
		mt := dto.MagicTake{ID: id, Kind: t.Kind, URL: td.URL, ThumbURL: &td.ThumbURL}
		if td.Video != nil {
			mt.PosterURL = &td.Video.PosterURL
		}
		if td.MadeWith != nil {
			mt.Seed = td.MadeWith.Seed
		}
		if td.Sound != "" { // API 1.85: a music take's cover, a sound or voice take's orb
			mt.Sound = &td.Sound
		}
		switch { // API 1.89: its seconds, a song's sections
		case td.Video != nil:
			mt.DurationS = &td.Video.DurationS
		case td.Audio != nil:
			mt.DurationS, mt.Sections = &td.Audio.DurationS, td.Audio.Sections
		}
		if run, ok := byTake[id]; ok {
			mt.RunID = &run
		}
		out.Takes[id] = mt
	}
	return nil
}

// boardSession is the board's own session; one deleted since is made again. It is hidden from Create's list.
func boardSession(ctx context.Context, tx pgx.Tx, row *boardRow) (string, error) {
	if row.SessionID != nil {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)`, *row.SessionID).Scan(&found); err != nil || found {
			return *row.SessionID, err
		}
	}
	id := utility.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)`, id, row.OwnerID,
		"Magic · "+row.Name); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE magic_boards SET session_id = $2 WHERE id = $1`, row.ID, id); err != nil {
		return "", err
	}
	row.SessionID = &id
	return id, nil
}

// pickBlocks are the blocks a pick names, in run order (board frames first, then lane by lane in time); named
// blocks that do not exist and an unknown lane are 422s.
func pickBlocks(d magic.Doc, pick dto.MagicPick, status map[string]dto.MagicStatus) ([]*magic.Block, error) {
	var out []*magic.Block
	switch {
	case pick.Blocks != nil:
		for _, id := range pick.Blocks {
			b := runBlockOf(d, id) // API 1.82: or a morph
			if b == nil {
				return nil, network.NewUnprocessableError("unknown block "+id, nil)
			}
			out = append(out, b)
		}
	case pick.Lane != nil:
		if !slices.ContainsFunc(d.Lanes, func(l *magic.Lane) bool { return l.ID == *pick.Lane }) {
			return nil, network.NewUnprocessableError("unknown lane "+*pick.Lane, nil)
		}
		for _, b := range withMorphs(d) {
			if b.Lane == *pick.Lane {
				out = append(out, b)
			}
		}
	default:
		for _, b := range withMorphs(d) {
			if st := status[b.ID].Status; st != "done" && st != "running" {
				out = append(out, b)
			}
		}
	}
	lane := map[string]int{}
	for i, l := range d.Lanes {
		lane[l.ID] = i
	}
	slices.SortStableFunc(out, func(a, b *magic.Block) int {
		return cmp.Or(cmp.Compare(boolInt(a.Kind != "frame"), boolInt(b.Kind != "frame")), cmp.Compare(lane[a.Lane], lane[b.Lane]),
			cmp.Compare(a.At, b.At))
	})
	return out, nil
}

// RunMagicBoard answers POST …/run: each picked block's request as `count` runs in the board's session.
func (s *Service) RunMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicRunCreate) (dto.MagicRunStarted, error) {
	if !s.registry.Ready() {
		return dto.MagicRunStarted{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.MagicRunStarted{Started: []dto.MagicStarted{}, Skipped: []dto.MagicSkipped{}}
	var placed []dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if body.Revision != row.Revision {
			return staleBoard(row)
		}
		d := row.doc()
		runs, err := boardRuns(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		status := blockStatuses(d, runs)
		blocks, err := pickBlocks(d, body.MagicPick, status)
		if err != nil {
			return err
		}
		named := body.Blocks != nil
		ws := s.registry.Workflows()
		for _, b := range blocks {
			name := strings.Split(b.Name, " · ")[0]
			why := whyNot(ws, d, b)
			if why == "" && status[b.ID].Status == "running" {
				if named {
					return network.NewConflictError(name+" is already running", nil)
				}
				why = "already running"
			}
			if why != "" {
				if named {
					return network.NewUnprocessableError(name+": "+why, nil)
				}
				out.Skipped = append(out.Skipped, dto.MagicSkipped{Block: b.ID, Reason: why})
				continue
			}
			session, err := boardSession(ctx, tx, &row)
			if err != nil {
				return err
			}
			// each block in a savepoint: a lane's or stale's block refused on the way (a pick with no take yet, the
			// run's own 4xx) is skipped with why; a named one is the error, with its name
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			made, err := s.runBlock(ctx, sp, caller, &row, session, d, b)
			var refused *network.APIError
			if errors.As(err, &refused) && refused.Code < 500 {
				if err := sp.Rollback(ctx); err != nil {
					return err
				}
				switch {
				case !named:
					out.Skipped = append(out.Skipped, dto.MagicSkipped{Block: b.ID, Reason: refused.Message})
					continue
				case refused.Code == 409:
					return network.NewConflictError(name+": "+refused.Message, nil)
				}
				return network.NewUnprocessableError(name+": "+refused.Message, nil)
			} else if err != nil {
				return err
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			for _, r := range made {
				out.Started = append(out.Started, dto.MagicStarted{Block: b.ID, RunID: r.ID})
			}
			placed = append(placed, made...)
		}
		out.Board, err = s.boardDTO(ctx, tx, row)
		return err
	})
	if err != nil {
		return dto.MagicRunStarted{}, err
	}
	for i := range placed {
		if err := s.placeQueued(ctx, &placed[i]); err != nil {
			return out, err
		}
	}
	return out, nil
}

// runBlock makes block b's runs (its count, seeds from its seed) in the board's session and indexes them; a
// dialogue's are one run per turn (API 1.89).
func (s *Service) runBlock(ctx context.Context, tx pgx.Tx, caller common.Caller, row *boardRow, session string, d magic.Doc, b *magic.Block) ([]dto.Run, error) {
	if isDialogue(b) {
		return s.runTurns(ctx, tx, caller, row, session, d, b)
	}
	w, _ := magicModel(s.registry.Workflows(), b.Kind, b.Request)
	first, last, refs := pickRefs(d, b)
	var pics magicPics
	var err error
	if pics.First, err = s.magicPicture(ctx, tx, row.OwnerID, d, first, "last"); err != nil {
		return nil, err
	}
	if pics.Last, err = s.magicPicture(ctx, tx, row.OwnerID, d, last, "first"); err != nil {
		return nil, err
	}
	for _, r := range refs {
		t, err := s.magicPicture(ctx, tx, row.OwnerID, d, r, "last")
		if err != nil {
			return nil, err
		}
		if t != "" {
			asset, _ := r["asset"].(string)
			pics.Refs, pics.RefAssets = append(pics.Refs, t), append(pics.RefAssets, asset)
		}
	}
	voiceID := blockVoice(d, b)
	seed, seeded := reqNumber(b.Request, "seed")
	count, ok := reqNumber(b.Request, "count")
	if !ok {
		count = 1
	}
	hash, via := inputsHash(d, b), common.ViaOf(caller.Via)
	var label *string // a frame keeps the style it is drawn in
	if b.Kind == "frame" {
		style := cmp.Or(reqString(b.Request, "style"), "sketch")
		label = &style
	}
	var out []dto.Run
	for i := range int(min(max(count, 1), 4)) {
		runSeed := int64(-1)
		if seeded && seed >= 0 {
			runSeed = int64(seed) + int64(i)
		}
		run, err := s.createRun(ctx, tx, caller, session, &dto.RunRequest{WorkflowID: w.ID,
			Inputs: standardInputs(w, d, b, pics, voiceID, runSeed), Host: hostOf(b)})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO magic_runs (run_id, owner_id, board_id, block, inputs_hash, seed, via, label)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, run.ID, row.OwnerID, row.ID, b.ID, hash, runSeed, via, label); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// runTurns runs dialogue b's turns that are not done or running (every one that is not running when all are done),
// each as its line block (turnBlock), one run each, indexed with its turn.
func (s *Service) runTurns(ctx context.Context, tx pgx.Tx, caller common.Caller, row *boardRow, session string, d magic.Doc, b *magic.Block) ([]dto.Run, error) {
	runs, err := boardRuns(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	turns := blockStatuses(d, runs)[b.ID].Turns
	var todo, idle []int
	for i, t := range turns {
		if t.Status != "running" {
			idle = append(idle, i)
			if t.Status != "done" {
				todo = append(todo, i)
			}
		}
	}
	if len(todo) == 0 {
		todo = idle
	}
	seed, seeded := reqNumber(b.Request, "seed")
	runSeed := int64(-1)
	if seeded && seed >= 0 {
		runSeed = int64(seed)
	}
	via := common.ViaOf(caller.Via)
	var out []dto.Run
	for _, i := range todo {
		tb := turnBlock(b, i)
		w, _ := magicModel(s.registry.Workflows(), tb.Kind, tb.Request)
		run, err := s.createRun(ctx, tx, caller, session, &dto.RunRequest{WorkflowID: w.ID,
			Inputs: standardInputs(w, d, tb, magicPics{}, blockVoice(d, tb), runSeed), Host: hostOf(b)})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO magic_runs (run_id, owner_id, board_id, block, inputs_hash, seed, via, turn)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, run.ID, row.OwnerID, row.ID, b.ID, inputsHash(d, tb), runSeed, via, i); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// magicFill adds a board run's take to its block as a studio revision (the projector, after storing the take); a
// block or board gone since leaves the take in the gallery.
func magicFill(ctx context.Context, tx pgx.Tx, run model.Run, takeID string) error {
	var boardID, block string
	var asset, label *string
	err := tx.QueryRow(ctx, `SELECT board_id::text, block, asset, label FROM magic_runs WHERE run_id = $1 AND owner_id = $2`,
		run.ID, run.OwnerID).Scan(&boardID, &block, &asset, &label)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	row, err := one[boardRow](ctx, tx, `SELECT `+boardCols+` FROM magic_boards WHERE id = $1 AND owner_id = $2
		AND deleted_at IS NULL FOR UPDATE`, boardID, run.OwnerID)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	doc := row.doc()
	if asset != nil { // a view Make views made joins the asset's sheet, named
		if !addView(doc, *asset, takeID, deref(label)) {
			return nil
		}
	} else if b := findBlock(doc, block); b == nil || slices.Contains(b.Takes, takeID) || len(b.Takes) >= 200 {
		return nil
	} else if fits, err := takeFitsBlock(ctx, tx, b, takeID); err != nil || !fits {
		return err // API 1.85: MMAudio's video with the sound stays in the Gallery
	} else {
		b.Takes = append(b.Takes, takeID)
	}
	if why := magic.Check(doc); why != "" {
		return fmt.Errorf("magic fill of %s: %s", block, why)
	}
	undo, redo, err := boardPointers(ctx, tx, row.ID, row.Revision)
	if err != nil {
		return err
	}
	return storeBoard(ctx, tx, &row, doc, write{by: "studio", ops: "take"}, undo, redo)
}

// takeFitsBlock is whether take id fits block b (takeFits).
func takeFitsBlock(ctx context.Context, tx pgx.Tx, b *magic.Block, id string) (bool, error) {
	var kind string
	if err := tx.QueryRow(ctx, `SELECT kind::text FROM takes WHERE id = $1`, id).Scan(&kind); err != nil {
		return false, err
	}
	return takeFits(b.Kind, kind), nil
}

// withRunTakes is doc with every take a board run made put back on its block (still on the board, oldest first): an
// undo, redo or restore never takes a take back (plan 39 §45.12).
func withRunTakes(ctx context.Context, tx pgx.Tx, boardID string, doc magic.Doc) (magic.Doc, error) {
	type made struct {
		Block, TakeID string
		Asset, Label  *string
		Kind          string
	}
	rows, err := collect[made](ctx, tx, `SELECT m.block, o.take_id::text, m.asset, m.label, t.kind::text FROM magic_runs m
		JOIN run_outputs o ON o.run_id = m.run_id JOIN takes t ON t.id = o.take_id AND t.deleted_at IS NULL
		WHERE m.board_id = $1 ORDER BY m.created_at, o.idx`, boardID)
	if err != nil || len(rows) == 0 {
		return doc, err
	}
	doc = doc.Clone()
	for _, r := range rows {
		if r.Asset != nil {
			addView(doc, *r.Asset, r.TakeID, deref(r.Label))
		} else if b := findBlock(doc, r.Block); b != nil && !slices.Contains(b.Takes, r.TakeID) && len(b.Takes) < 200 &&
			takeFits(b.Kind, r.Kind) {
			b.Takes = append(b.Takes, r.TakeID)
		}
	}
	return doc, nil
}

// ------------------------------------------------------------------ the estimate (plan 39 §45.10)

// magicUnits are a take's or a block's units of GPU work: an image's megapixels, a video's seconds × its pixels /
// (1280 × 720), a voice's (any audio's) seconds.
func magicUnits(kind string, w, h, seconds float64) float64 {
	switch kind {
	case "image", "frame":
		return w * h / 1e6
	case "video":
		return seconds * w * h / (1280 * 720)
	}
	return seconds
}

// median is the middle of xs (the mean of the two middle ones); xs is not empty.
func median(xs []float64) float64 {
	xs = slices.Sorted(slices.Values(xs))
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// recentRate is the caller's GPU seconds per unit of workflow on host: the median over their last 5 takes of it there
// with a render time; nil with none.
func recentRate(ctx context.Context, tx pgx.Tx, ownerID, workflowID, host, kind string) (*float64, error) {
	type take struct{ RenderS, W, H, Seconds float64 }
	rows, err := collect[take](ctx, tx, `SELECT (made_with->>'render_s')::float8, coalesce(width, 0)::float8,
		coalesce(height, 0)::float8, coalesce((video->>'duration_s')::float8, (audio->>'duration_s')::float8, 0)
		FROM takes WHERE owner_id = $1 AND made_with->>'workflow_id' = $2 AND made_with->>'host' = $3
		AND made_with ? 'render_s' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 5`, ownerID, workflowID, host)
	if err != nil {
		return nil, err
	}
	var rates []float64
	for _, t := range rows {
		if u := magicUnits(kind, t.W, t.H, t.Seconds); u > 0 {
			rates = append(rates, t.RenderS/u)
		}
	}
	if len(rates) == 0 {
		return nil, nil
	}
	m := median(rates)
	return &m, nil
}

// blockUnits are block b's units on the board (its size, its length).
func blockUnits(d magic.Doc, b *magic.Block) float64 {
	w, h := float64(d.Target.Width), float64(d.Target.Height)
	if v, ok := reqNumber(b.Request, "width"); ok {
		w = v
	}
	if v, ok := reqNumber(b.Request, "height"); ok {
		h = v
	}
	return magicUnits(b.Kind, w, h, float64(b.Length)/float64(d.Target.FPS))
}

// askAbove is the caller's ask_above_gpu_minutes (5 without a row).
func askAbove(ctx context.Context, tx pgx.Tx) (float64, error) {
	v := 5.0
	err := tx.QueryRow(ctx, `SELECT ask_above_gpu_minutes FROM magic_settings`).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return v, err
}

var magicNoun = map[string][2]string{"video": {"shot", "shots"}, "image": {"still", "stills"}, "voice": {"line", "lines"},
	"frame": {"frame", "frames"}, "music": {"cue", "cues"}, "sound": {"sound", "sounds"}, "view": {"view", "views"}}

// askText is the ask card's line: "3 shots, about 6 min".
func askText(kinds []string, seconds *float64) string {
	noun := [2]string{"block", "blocks"}
	if len(kinds) > 0 && !slices.ContainsFunc(kinds, func(k string) bool { return k != kinds[0] }) {
		if n, ok := magicNoun[kinds[0]]; ok {
			noun = n
		}
	}
	what := fmt.Sprintf("%d %s", len(kinds), noun[min(len(kinds), 2)/2])
	switch {
	case seconds == nil:
		return what + ", time unknown"
	case *seconds < 60:
		return fmt.Sprintf("%s, about %d s", what, int(math.Ceil(*seconds)))
	}
	return fmt.Sprintf("%s, about %d min", what, int(math.Round(*seconds/60)))
}

// PlanMagicBoard answers POST …/plan: per block its estimate, host and peak, the totals, the run order and the ask rule.
func (s *Service) PlanMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicPick) (dto.MagicPlan, error) {
	if !s.registry.Ready() {
		return dto.MagicPlan{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.MagicPlan{Blocks: []dto.MagicPlanBlock{}, Lanes: []dto.MagicPlanLane{}, Order: []string{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, false)
		if err != nil {
			return err
		}
		d := row.doc()
		runs, err := boardRuns(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		blocks, err := pickBlocks(d, *body, blockStatuses(d, runs))
		if err != nil {
			return err
		}
		return s.planBlocks(ctx, tx, caller, row.OwnerID, d, blocks, &out)
	})
	return out, err
}

// planBlocks fills out with blocks' estimates on document d (owner's rates), the totals, the run order and the ask
// rule.
func (s *Service) planBlocks(ctx context.Context, tx pgx.Tx, caller common.Caller, ownerID string, d magic.Doc, blocks []*magic.Block, out *dto.MagicPlan) error {
	var err error
	if out.LimitMinutes, err = askAbove(ctx, tx); err != nil {
		return err
	}
	ws := s.registry.Workflows()
	total, runs, known, unknownRun := 0.0, 0.0, true, false // runs: the total without board frames (they never ask)
	anyRun := false                                         // a run that is not a board frame: a limit of 0 always asks for it
	lanes := map[string]*dto.MagicPlanLane{}
	var kinds []string
	for _, b := range blocks {
		pb := dto.MagicPlanBlock{Block: b.ID, Source: "unknown"}
		out.Order = append(out.Order, b.ID)
		lane := lanes[b.Lane]
		if lane == nil {
			lane = &dto.MagicPlanLane{Lane: b.Lane, Seconds: new(float64)}
			lanes[b.Lane] = lane
		}
		lane.Blocks++
		if why := whyNot(ws, d, b); why != "" {
			pb.Reason = &why
			out.Blocks = append(out.Blocks, pb)
			continue
		}
		kinds = append(kinds, b.Kind)
		anyRun = anyRun || b.Kind != "frame"
		w, _ := magicModel(ws, b.Kind, b.Request)
		// the host and peak as POST /runs/plan answers them, for the request without its pictures (a dialogue: its
		// first turn's), on its Run on host when it has one
		pb0 := b
		if isDialogue(b) {
			pb0 = turnBlock(b, 0)
		}
		_, probe, _, perr := s.probeRun(ctx, tx, caller, &dto.RunRequest{WorkflowID: w.ID,
			Inputs: standardInputs(w, d, pb0, magicPics{}, blockVoice(d, pb0), -1), Host: hostOf(b)})
		if perr == nil {
			if slots, _, reason := eligible(s.registry, s.registry.Hosts(), probe, time.Now()); reason == "" {
				host := slices.Sorted(maps.Keys(slots))[0]
				pb.Host = &host
				if p := planOf(s.registry, host, probe); p.peak >= 0 {
					gb := float64(p.peak) / 100
					pb.PeakGB = &gb
				}
			}
		}
		count, ok := reqNumber(b.Request, "count")
		if !ok {
			count = 1
		}
		units := blockUnits(d, b) * min(max(count, 1), 4)
		var rate *float64
		if pb.Host != nil {
			if rate, err = recentRate(ctx, tx, ownerID, w.ID, *pb.Host, b.Kind); err != nil {
				return err
			}
		}
		switch {
		case rate != nil:
			pb.Source = "yours"
		case w.Standard.Seconds != nil:
			rate, pb.Source = w.Standard.Seconds, "pack"
		}
		if rate != nil {
			sec := math.Round(*rate*units*10) / 10
			pb.Seconds = &sec
			total += sec
			if b.Kind != "frame" {
				runs += sec
			}
			if lane.Seconds != nil {
				*lane.Seconds += sec
			}
		} else {
			known, lane.Seconds = false, nil
			if b.Kind != "frame" {
				unknownRun = true
			}
		}
		out.Blocks = append(out.Blocks, pb)
	}
	for _, l := range d.Lanes {
		if lane := lanes[l.ID]; lane != nil {
			out.Lanes = append(out.Lanes, *lane)
		}
	}
	if known {
		out.Seconds = &total
	}
	out.Ask = unknownRun || runs > out.LimitMinutes*60 || (anyRun && out.LimitMinutes == 0)
	out.AskText = askText(kinds, out.Seconds)
	return nil
}

// PlanMagicDoc plans blocks ids of document d (an edit not stored yet, API 1.87: the Assistant's proposal) on board
// id with the caller's rates; ids that are not blocks of d are left out.
func (s *Service) PlanMagicDoc(ctx context.Context, caller common.Caller, id string, d magic.Doc, ids []string) (dto.MagicPlan, error) {
	if !s.registry.Ready() {
		return dto.MagicPlan{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.MagicPlan{Blocks: []dto.MagicPlanBlock{}, Lanes: []dto.MagicPlanLane{}, Order: []string{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, false)
		if err != nil {
			return err
		}
		var blocks []*magic.Block
		for _, b := range ids {
			if x := runBlockOf(d, b); x != nil {
				blocks = append(blocks, x)
			}
		}
		if blocks, err = pickBlocks(d, dto.MagicPick{Blocks: idsOf(blocks)}, nil); err != nil || len(blocks) == 0 {
			return err
		}
		return s.planBlocks(ctx, tx, caller, row.OwnerID, d, blocks, &out)
	})
	return out, err
}

// idsOf is blocks' ids.
func idsOf(blocks []*magic.Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.ID
	}
	return out
}

// ------------------------------------------------------------------ settings

// GetMagicSettings is the caller's Magic settings.
func (s *Service) GetMagicSettings(ctx context.Context, caller common.Caller) (dto.MagicSettings, error) {
	var out dto.MagicSettings
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		v, err := askAbove(ctx, tx)
		out.AskAboveGPUMinutes = &v
		return err
	})
	return out, err
}

// PutMagicSettings stores the caller's Magic settings.
func (s *Service) PutMagicSettings(ctx context.Context, caller common.Caller, body *dto.MagicSettings) (dto.MagicSettings, error) {
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO magic_settings (owner_id, ask_above_gpu_minutes) VALUES ($1, $2)
			ON CONFLICT (owner_id) DO UPDATE SET ask_above_gpu_minutes = $2`, caller.ID, *body.AskAboveGPUMinutes)
		return err
	})
	return *body, err
}
