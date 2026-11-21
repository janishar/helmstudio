package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// Voices (contracts/README.md "Voices", API 1.30): saved voices are library items of kind voice (own designed ones,
// and the live hosts' starter voices, read-only); a voice param names one as {voice_id}, which a run resolves as the
// owner when it is made (runVoices) and the dispatcher sends as defs voice. contracts/check.py voice_reason and
// voice_fit are the reference, contracts/fixtures/voices the shared tests.

// errVoiceReadOnly is the answer to a write of a starter voice.
var errVoiceReadOnly = network.NewConflictError("starter voices are read-only", nil)

// voiceSource is what a param's accepts names for a voice's group; voiceRefused the reason it is not accepted.
var (
	voiceSource  = map[string]string{"preset": "preset", "cloned": "clip", "designed": "design"}
	voiceRefused = map[string]string{"preset": "this workflow cannot use a preset voice",
		"clip": "this workflow cannot clone a voice", "design": "this workflow cannot use a designed voice"}
)

// voiceItem is a saved voice as the fit rule and a run read it.
type voiceItem struct {
	ID, Name, Group string
	Version         int
	Voice           dto.VoiceInfo
}

// voiceParam is one voice param of a graph.
type voiceParam struct {
	Node  string
	Param registry.Param
}

// voiceParams are the voice params of the known, not bypassed nodes, node id then schema order, each param's accepts
// narrowed to the workflow node's only (pins: the workflow's own nodes, which hold only; README, Media inputs: only).
func voiceParams(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, pins map[string]registry.Node) []voiceParam {
	var out []voiceParam
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		schema, known := schemas[nodes[id].Kind]
		if !known || nodes[id].Bypass {
			continue
		}
		for _, p := range schema.Params {
			if p.Type == "voice" {
				if allowed, pinned := pins[id].Only[p.Name]; pinned {
					p.Accepts = slices.DeleteFunc(slices.Clone(p.Accepts), func(a string) bool { return !slices.Contains(allowed, a) })
				}
				out = append(out, voiceParam{id, p})
			}
		}
	}
	return out
}

// voiceSources are what a saved voice offers a voice param, its own source first, then (API 1.31) clip when it keeps
// one (check.py voice_sources).
func voiceSources(item voiceItem) []string {
	own := voiceSource[item.Group]
	if own != "clip" && item.Voice.Clip != nil {
		return []string{own, "clip"}
	}
	return []string{own}
}

// sentSource is the defs voice source studio sends for param: the first of the voice's sources it accepts, "" when it
// takes none (check.py voice_source).
func sentSource(param registry.Param, item voiceItem) string {
	for _, src := range voiceSources(item) {
		if slices.Contains(param.Accepts, src) {
			return src
		}
	}
	return ""
}

// voiceReason is why one voice param of a node of kind cannot take the voice, "" when it can.
func voiceReason(kind string, param registry.Param, item voiceItem) string {
	src := voiceSource[item.Group]
	if sentSource(param, item) == "" {
		return voiceRefused[src]
	}
	if src == "preset" && item.Voice.Family != registry.Family(kind) {
		return "voice " + item.Name + " is for " + item.Voice.Family
	}
	return ""
}

// voiceFit is GET /library?workflow= for a voice: "" when some voice param takes it, else the first param's reason.
func voiceFit(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, item voiceItem) string {
	params := voiceParams(nodes, schemas, nodes)
	if len(params) == 0 {
		return "this workflow takes no voice"
	}
	var reasons []string
	for _, p := range params {
		reason := voiceReason(nodes[p.Node].Kind, p.Param, item)
		if reason == "" {
			return ""
		}
		reasons = append(reasons, reason)
	}
	return reasons[0]
}

// voiceOf is a voice item's DTO as the fit rule reads it.
func voiceOf(item dto.LibraryItem) voiceItem {
	out := voiceItem{ID: item.ID, Name: item.Name, Group: item.Group, Version: item.Version}
	if item.Voice != nil {
		out.Voice = *item.Voice
	}
	return out
}

// starterVoiceDTO maps a starter voice: kind voice, group preset, its preview clip (signed) as the thumbnail.
func (s *Service) starterVoiceDTO(v registry.LiveVoice) dto.LibraryItem {
	family := v.Family
	out := dto.LibraryItem{ID: v.ID, Kind: "voice", Group: "preset", Name: v.Name, Images: []dto.LibraryImage{},
		Version: v.Version, CreatedAt: v.PublishedAt, UpdatedAt: v.PublishedAt, Source: "starter", Family: &family,
		Loras: []dto.LookLora{}, Needs: []string{}, Voice: &dto.VoiceInfo{Source: "preset", Family: v.Family,
			Speaker: v.Speaker, Language: v.Language, Style: v.Style}}
	if v.Preview != nil {
		out.ThumbURL = s.signer.SignPtr(&v.Preview.Key)
	}
	return out
}

// voiceValue is a voice value as cmd.run.execute carries it (defs voice): the item's facts when the run was made.
type voiceValue struct {
	ItemID   string `json:"item_id"`
	Version  int    `json:"version"`
	Source   string `json:"source"` // preset | clip | design
	Family   string `json:"family,omitempty"`
	Speaker  string `json:"speaker,omitempty"`
	Text     string `json:"text,omitempty"`
	Language string `json:"language,omitempty"`
	Style    string `json:"style,omitempty"`
	// API 1.31, source clip: the clip as a defs media value (in / out always), resolved when the run was made
	Clip map[string]any `json:"clip,omitempty"`
}

// readVoices reads the voices of ids the owner can use now: own items of kind voice (an explicit owner check, so tx
// may be System) and the live starter voices; any other id is absent.
func readVoices(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID string, ids []string) (map[string]voiceItem, error) {
	out := map[string]voiceItem{}
	var own []string
	var starters map[string]registry.LiveVoice
	for _, id := range ids {
		switch {
		case utility.IsUUID(id):
			own = append(own, strings.ToLower(id))
		case starters == nil:
			starters = reg.Voices(time.Now())
		}
	}
	for id, v := range starters {
		if slices.Contains(ids, id) {
			out[id] = voiceItem{ID: id, Name: v.Name, Group: "preset", Version: v.Version, Voice: dto.VoiceInfo{
				Source: "preset", Family: v.Family, Speaker: v.Speaker, Language: v.Language, Style: v.Style}}
		}
	}
	if len(own) == 0 {
		return out, nil
	}
	rows, err := collect[voiceItem](ctx, tx, `SELECT l.id::text, l.name, l.item_group, l.version, l.voice FROM library_items l
		WHERE l.owner_id = $1 AND l.kind = 'voice' AND l.id = ANY($2::uuid[])`, ownerID, own)
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, err
}

// runVoices resolves the voice values of a run's merged, checked graph as the owner (README, Voices): each must be a
// voice the owner can use now ("<node>.<param>: voice <id> not found") that the param takes ("<node>.<param>:
// <reason>"); a voice sent by its clip (API 1.31) needs the clip's object: its take, deleted or not, the owner's with
// the object there ("<node>.<param>: the clip of voice <name> is gone"). It returns each path's defs voice and made_with.voices (node id then param order, each voice once), or
// the 422 details.
func runVoices(ctx context.Context, tx pgx.Tx, store storage.Store, reg *registry.Registry, ownerID string, nodes,
	pins map[string]registry.Node, schemas map[string]registry.NodeSchema) (map[string]voiceValue, []gallerydto.MadeWithVoice, []string, error) {
	params := voiceParams(nodes, schemas, pins)
	var ids []string
	for _, p := range params {
		if ref, ok := nodes[p.Node].Params[p.Param.Name].(map[string]any); ok {
			ids = append(ids, ref["voice_id"].(string))
		}
	}
	if len(ids) == 0 {
		return nil, nil, nil, nil
	}
	found, err := readVoices(ctx, tx, reg, ownerID, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	clips, err := voiceClips(ctx, tx, store, ownerID, params, nodes, found)
	if err != nil {
		return nil, nil, nil, err
	}
	values, made, errs := map[string]voiceValue{}, []gallerydto.MadeWithVoice{}, []string(nil)
	for _, p := range params {
		ref, ok := nodes[p.Node].Params[p.Param.Name].(map[string]any)
		if !ok {
			continue
		}
		id, path := ref["voice_id"].(string), p.Node+"."+p.Param.Name
		item, ok := found[strings.ToLower(id)]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: voice %s not found", path, id))
			continue
		}
		if reason := voiceReason(nodes[p.Node].Kind, p.Param, item); reason != "" {
			errs = append(errs, path+": "+reason)
			continue
		}
		v := voiceValue{ItemID: item.ID, Version: item.Version, Source: sentSource(p.Param, item),
			Language: item.Voice.Language, Style: item.Voice.Style}
		switch v.Source {
		case "preset":
			v.Family, v.Speaker = item.Voice.Family, item.Voice.Speaker
		case "design":
			v.Text = item.Voice.Design
		case "clip":
			row, ok := clips[item.Voice.Clip.TakeID]
			if !ok {
				errs = append(errs, path+": the clip of voice "+item.Name+" is gone")
				continue
			}
			v.Clip = mediaDescriptor(row, map[string]any{"in": item.Voice.Clip.In, "out": item.Voice.Clip.Out})
		}
		values[path] = v
		if !slices.ContainsFunc(made, func(m gallerydto.MadeWithVoice) bool { return m.ID == item.ID }) {
			made = append(made, gallerydto.MadeWithVoice{ID: item.ID, Name: item.Name, Version: item.Version, Source: item.Group})
		}
	}
	return values, made, errs, nil
}

// voiceClips reads the clip takes of the voices params send by their clip (found: the voices by id), deleted or not,
// with an explicit owner check, and keeps those whose object is there (HEADed; a take recorded without its etag gets
// it now, as checkMedia).
func voiceClips(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, params []voiceParam,
	nodes map[string]registry.Node, found map[string]voiceItem) (map[string]inputTake, error) {
	var ids []string
	for _, p := range params {
		ref, _ := nodes[p.Node].Params[p.Param.Name].(map[string]any)
		id, _ := ref["voice_id"].(string)
		if item, ok := found[strings.ToLower(id)]; ok && sentSource(p.Param, item) == "clip" {
			ids = append(ids, item.Voice.Clip.TakeID)
		}
	}
	out := map[string]inputTake{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := clipTakes(ctx, tx, ownerID, ids, true)
	if err != nil {
		return nil, err
	}
	all := slices.Collect(maps.Values(rows))
	there, err := present(ctx, store, len(all), func(i int) (string, int64) { return all[i].Key, all[i].Bytes })
	if err != nil {
		return nil, err
	}
	for i, row := range all {
		if !there[i] {
			continue
		}
		if row.Etag == nil {
			etag, err := contentHash(ctx, store, row.Key)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			} else if err != nil {
				return nil, network.NewServiceUnavailableError("could not read a voice's clip; try again", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE takes SET etag = $2 WHERE id = $1 AND etag IS NULL`, row.ID, etag); err != nil {
				return nil, err
			}
			row.Etag = &etag
		}
		out[row.ID] = row
	}
	return out, nil
}

// clipTakes reads the owner's ready takes among ids (an explicit owner check), deleted ones too when deleted: a voice
// keeps its clip's take after it left the gallery (README, Voices: cloned voices).
func clipTakes(ctx context.Context, tx pgx.Tx, ownerID string, ids []string, deleted bool) (map[string]inputTake, error) {
	rows, err := collect[inputTake](ctx, tx, `SELECT id::text, kind, key, content_type, bytes, etag, width, height, video, audio
		FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND ready AND (deleted_at IS NULL OR $3)`, uuids(ids), ownerID, deleted)
	out := map[string]inputTake{}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, err
}

// runParents is a run's lineage: the takes of its stored graph's Image and media values (parents of the typed view),
// then (API 1.31) the clip take of each voice it sent by its clip, path order, each take once.
func runParents(graph, mentions []byte) []string {
	out := parents(typedView(graph, mentions))
	var exp expansion
	if len(mentions) == 0 || json.Unmarshal(mentions, &exp) != nil {
		return out
	}
	for _, path := range slices.Sorted(maps.Keys(exp.VoiceValues)) {
		if id, ok := exp.VoiceValues[path].Clip["take_id"].(string); ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// voiceClipParam is how a voice's clip is checked: as this media param (README, Voices: cloned voices).
var voiceClipParam = registry.Param{Type: "media", Accepts: []string{"audio"}, Trim: true, MinS: 3, MaxS: 30}

// clipFacts is a clip's take as saveVoice reads it: its media facts and its made_with.voices.
type clipFacts struct {
	Media  gallery.MediaTake
	Voices []gallerydto.MadeWithVoice
}

// saveVoice checks a voice's fields after its shape (dto checkVoice) in the contract's order (check.py save_voice:
// clip or from_take, the clip's take, a Save as voice copy of the design, the design, the clip's part, consent, then
// language, style and name) and returns its stored VoiceInfo. take reads one of the owner's takes (nil: not found);
// voice one of the owner's voices (nil: deleted).
func saveVoice(f *dto.LibraryFields, from *dto.LibraryFromTake, take func(id string) (*clipFacts, error),
	voice func(id string) (*voiceItem, error)) (dto.VoiceInfo, error) {
	bad := func(detail string) (dto.VoiceInfo, error) {
		return dto.VoiceInfo{}, network.NewUnprocessableError(detail, nil)
	}
	if f.Voice == nil {
		f.Voice = &dto.VoiceDesign{}
	}
	v := f.Voice
	path, ref := "", v.Clip
	switch {
	case v.Clip != nil && from != nil:
		return bad("voice.clip: give the clip or from_take, not both")
	case v.Clip != nil:
		path = "voice.clip"
	case from != nil:
		path, ref = "from_take", &dto.ClipRef{TakeID: from.TakeID, In: from.In, Out: from.Out}
	case f.Group == "cloned":
		return bad("voice: a cloned voice needs voice.clip")
	}
	var facts *clipFacts
	if ref != nil {
		var err error
		if facts, err = take(ref.TakeID); err != nil {
			return dto.VoiceInfo{}, err
		} else if facts == nil {
			return dto.VoiceInfo{}, network.NewNotFoundError(path+": take "+ref.TakeID+" not found", nil)
		}
	}
	if f.Group == "designed" && v.Design == nil {
		if from == nil {
			return bad("voice: a designed voice needs voice.design")
		}
		i := slices.IndexFunc(facts.Voices, func(m gallerydto.MadeWithVoice) bool { return m.Source == "designed" })
		if i < 0 {
			return bad("from_take: take " + ref.TakeID + " was not made with a designed voice")
		}
		src, err := voice(facts.Voices[i].ID)
		if err != nil {
			return dto.VoiceInfo{}, err
		} else if src == nil || src.Voice.Design == "" {
			return bad("from_take: voice " + facts.Voices[i].Name + " was deleted")
		}
		design := src.Voice.Design
		v.Design = &design
		if v.Language == nil && src.Voice.Language != "" {
			v.Language = &src.Voice.Language
		}
		if v.Style == nil && src.Voice.Style != "" {
			v.Style = &src.Voice.Style
		}
	}
	if detail := v.CheckDesign(); detail != "" {
		return bad(detail)
	}
	var clip *dto.VoiceClip
	if ref != nil {
		if detail := gallery.MediaFit(voiceClipParam, facts.Media); detail != "" {
			return bad(path + ": " + detail)
		}
		value := map[string]any{"take_id": ref.TakeID}
		if ref.In != nil {
			value["in"] = *ref.In
		}
		if ref.Out != nil {
			value["out"] = *ref.Out
		}
		snapped, detail := snapMedia(voiceClipParam, facts.Media, value)
		if detail != "" {
			return bad(path + ": " + detail)
		}
		if v.Consent == nil || !*v.Consent {
			return bad(`voice.consent: tick "I have the right to use this voice"`)
		}
		clip = &dto.VoiceClip{TakeID: ref.TakeID, In: snapped["in"].(float64), Out: snapped["out"].(float64)}
	}
	if detail := f.CheckTail(); detail != "" {
		return bad(detail)
	}
	info := v.Info(f.Group)
	info.Clip, info.Consent = clip, clip != nil
	return info, nil
}

// checkVoice is saveVoice against the owner's takes and voices in tx: a clip's take must be the owner's ready,
// undeleted take, except kept (a PUT keeping its stored clip, whose take may have been deleted since).
func (s *Service) checkVoice(ctx context.Context, tx pgx.Tx, ownerID string, f *dto.LibraryFields,
	from *dto.LibraryFromTake, kept string) (dto.VoiceInfo, error) {
	take := func(id string) (*clipFacts, error) {
		rows, err := clipTakes(ctx, tx, ownerID, []string{id}, id == kept)
		row, ok := rows[id]
		if err != nil || !ok {
			return nil, err
		}
		out := &clipFacts{Media: gallery.Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)}
		var made *gallerydto.MadeWith
		if err := tx.QueryRow(ctx, `SELECT made_with FROM takes WHERE id = $1`, id).Scan(&made); err != nil {
			return nil, err
		} else if made != nil {
			out.Voices = made.Voices
		}
		return out, nil
	}
	voice := func(id string) (*voiceItem, error) {
		found, err := readVoices(ctx, tx, s.registry, ownerID, []string{id})
		item, ok := found[id]
		if err != nil || !ok || item.Group != "designed" {
			return nil, err
		}
		return &item, nil
	}
	return saveVoice(f, from, take, voice)
}

// sendVoices puts a run's resolved voice values (runs.mentions voice_values) into the graph it is sent with.
func sendVoices(nodes map[string]registry.Node, mentions []byte) {
	var exp expansion
	if len(mentions) == 0 || json.Unmarshal(mentions, &exp) != nil {
		return
	}
	for path, v := range exp.VoiceValues {
		id, name, _ := strings.Cut(path, ".")
		if node, ok := nodes[id]; ok && node.Params != nil {
			node.Params[name] = v
		}
	}
}
