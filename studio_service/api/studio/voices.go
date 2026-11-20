package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

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

// voiceParams are the voice params of the known, not bypassed nodes, node id then schema order.
func voiceParams(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []voiceParam {
	var out []voiceParam
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		schema, known := schemas[nodes[id].Kind]
		if !known || nodes[id].Bypass {
			continue
		}
		for _, p := range schema.Params {
			if p.Type == "voice" {
				out = append(out, voiceParam{id, p})
			}
		}
	}
	return out
}

// voiceReason is why one voice param of a node of kind cannot take the voice, "" when it can.
func voiceReason(kind string, param registry.Param, item voiceItem) string {
	src := voiceSource[item.Group]
	if !slices.Contains(param.Accepts, src) {
		return voiceRefused[src]
	}
	if src == "preset" && item.Voice.Family != registry.Family(kind) {
		return "voice " + item.Name + " is for " + item.Voice.Family
	}
	return ""
}

// voiceFit is GET /library?workflow= for a voice: "" when some voice param takes it, else the first param's reason.
func voiceFit(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, item voiceItem) string {
	params := voiceParams(nodes, schemas)
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
// <reason>"). It returns each path's defs voice and made_with.voices (node id then param order, each voice once), or
// the 422 details.
func runVoices(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID string, nodes map[string]registry.Node,
	schemas map[string]registry.NodeSchema) (map[string]voiceValue, []gallerydto.MadeWithVoice, []string, error) {
	params := voiceParams(nodes, schemas)
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
		v := voiceValue{ItemID: item.ID, Version: item.Version, Source: voiceSource[item.Group],
			Language: item.Voice.Language, Style: item.Voice.Style}
		if v.Source == "preset" {
			v.Family, v.Speaker = item.Voice.Family, item.Voice.Speaker
		} else {
			v.Text = item.Voice.Design
		}
		values[path] = v
		if !slices.ContainsFunc(made, func(m gallerydto.MadeWithVoice) bool { return m.ID == item.ID }) {
			made = append(made, gallerydto.MadeWithVoice{ID: item.ID, Name: item.Name, Version: item.Version, Source: item.Group})
		}
	}
	return values, made, errs, nil
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
