package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// The Assistant's character voices (alpha.6; contracts/README.md "Magic: the Assistant", design_voice and save_voice):
// Create's Design a voice for a board's asset, then Save as voice of the take the user picked, set on the asset.

// designWorkflow is the voice design template: an audio template whose voice param takes design, one that takes only
// design (Qwen3-TTS Voice design) and a real one first; its prompt and voice keys. ok false: none is published.
func designWorkflow(ws []registry.Workflow) (w registry.Workflow, prompt, voice string, ok bool) {
	best := math.MaxInt
	for _, x := range ws {
		if x.Media != "audio" {
			continue
		}
		p, v, rank := "", "", math.MaxInt
		for _, id := range slices.Sorted(maps.Keys(x.Nodes)) {
			n := x.Nodes[id]
			if n.Kind == "prompt" && p == "" {
				p = id + ".text"
			}
			for _, par := range x.NodeSchemas[n.Kind].Params {
				accepts := par.Accepts
				if only, has := n.Only[par.Name]; has {
					accepts = slices.DeleteFunc(slices.Clone(accepts), func(a string) bool { return !slices.Contains(only, a) })
				}
				if par.Type == "voice" && v == "" && slices.Contains(accepts, "design") {
					v, rank = id+"."+par.Name, map[bool]int{true: 0, false: 1}[len(accepts) == 1]
				}
			}
		}
		if x.Env == "fake" {
			rank += 2
		}
		if p != "" && v != "" && (rank < best || rank == best && x.ID < w.ID) {
			w, prompt, voice, ok, best = x, p, v, true, rank
		}
	}
	return w, prompt, voice, ok
}

// voiceEstimate is a design run's GPU seconds for line: the owner's recent rate of workflow w (any host; render
// seconds per second of speech), else the pack's; nil unknown. limit is the ask rule's (ask_above_gpu_minutes).
func (s *Service) voiceEstimate(ctx context.Context, caller common.Caller, w registry.Workflow, line string) (*float64, float64, error) {
	var rate *float64
	limit := 5.0
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var err error
		if limit, err = askAbove(ctx, tx); err != nil {
			return err
		}
		type take struct{ RenderS, Seconds float64 }
		rows, err := collect[take](ctx, tx, `SELECT (made_with->>'render_s')::float8, coalesce((audio->>'duration_s')::float8, 0)
			FROM takes WHERE owner_id = $1 AND made_with->>'workflow_id' = $2 AND made_with ? 'render_s'
			AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 5`, caller.ID, w.ID)
		if err != nil {
			return err
		}
		var rates []float64
		for _, t := range rows {
			if t.Seconds > 0 {
				rates = append(rates, t.RenderS/t.Seconds)
			}
		}
		if len(rates) > 0 {
			m := median(rates)
			rate = &m
		}
		return nil
	})
	if rate == nil && w.Standard != nil {
		rate = w.Standard.Seconds
	}
	if err != nil || rate == nil {
		return nil, limit, err
	}
	sec := math.Round(*rate*max(float64(len([]rune(line)))/15, 1)*10) / 10 // speech_seconds, at least 1 s
	return &sec, limit, nil
}

// designVoice is design_voice: the asset's designed voice (made, or voice_id's words changed) and one design run of
// the line in the board's session; within the limit (and not ask) at once, else the magic_run preview first.
func (s *Service) designVoice(ctx context.Context, caller common.Caller, call magicCall, base string, a magicArgs, confirmed, ask bool) message.ToolsReply {
	design, line := strings.TrimSpace(deref(a.Design)), strings.TrimSpace(deref(a.Line))
	switch {
	case design == "" || len([]rune(design)) > 2000:
		return toolErr("invalid", "args: design must be 1-2000 characters")
	case line == "" || len([]rune(line)) > 500:
		return toolErr("invalid", "args: line must be 1-500 characters")
	case a.VoiceID != nil && !utility.IsUUID(*a.VoiceID):
		return toolErr("invalid", "args: voice_id must be a uuid")
	}
	var view dto.MagicAgentView
	if refused := call(http.MethodGet, base+"?view=agent", nil, &view); refused != nil {
		return *refused
	}
	if view.Revision != a.Revision {
		return toolErr("conflict", "board changed since you opened it (now revision %d)", view.Revision)
	}
	ai := slices.IndexFunc(view.Assets, func(x magic.AgentAsset) bool { return x.ID == a.Asset })
	if ai < 0 {
		return toolErr("invalid", "unknown asset %s", a.Asset)
	}
	asset := view.Assets[ai]
	w, promptKey, voiceKey, ok := designWorkflow(s.registry.Workflows())
	if !ok {
		return toolErr("conflict", "no voice design model is installed: get Qwen3-TTS in Models")
	}
	seconds, limit, err := s.voiceEstimate(ctx, caller, w, line)
	if err != nil {
		return apiRefused(err, "design_voice")
	}
	if !confirmed && (ask || seconds == nil || *seconds > limit*60 || limit == 0) {
		text := askText([]string{"voice"}, seconds)
		return message.ToolsReply{Preview: map[string]any{"revision": a.Revision,
			"plan":   map[string]any{"limit_minutes": limit, "seconds": seconds, "ask": true, "ask_text": text},
			"frames": []any{}, "rest": map[string]any{"blocks": []string{}, "names": []string{asset.Name + "'s voice"},
				"seconds": seconds, "host": nil, "ask_text": text}}}
	}
	voice := map[string]any{"design": design}
	if a.Language != nil {
		voice["language"] = *a.Language
	}
	var item designedVoice
	if a.VoiceID == nil { // Create's Hear it: the description saved as a designed voice, named after the character
		name := string([]rune(asset.Name)[:min(len([]rune(asset.Name)), 80)])
		if refused := call(http.MethodPost, "/v1/studio/library", map[string]any{"kind": "voice", "group": "designed",
			"name": name, "voice": voice}, &item); refused != nil {
			return *refused
		}
	} else { // tried again with other words: the same voice
		lib := "/v1/studio/library/" + url.PathEscape(*a.VoiceID)
		if refused := call(http.MethodGet, lib, nil, &item); refused != nil {
			return *refused
		}
		if refused := call(http.MethodPut, lib, map[string]any{"group": "designed", "name": item.Name, "text": "",
			"images": []any{}, "voice": voice, "version": item.Version}, &item); refused != nil {
			return *refused
		}
	}
	run, err := s.voiceRun(ctx, caller, a.BoardID, a.Revision, w, map[string]any{promptKey: line,
		voiceKey: map[string]any{"voice_id": item.ID}})
	if err != nil {
		return apiRefused(err, "design_voice")
	}
	return message.ToolsReply{Result: map[string]any{"revision": a.Revision, "voice_id": item.ID,
		"started": []map[string]any{{"run_id": run.ID}}, "seconds": seconds}}
}

// designedVoice is the part of a voice (LibraryItem) the voice tools read.
type designedVoice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Group   string `json:"group"`
	Voice   *struct {
		Design   *string `json:"design"`
		Language *string `json:"language"`
		Style    *string `json:"style"`
	} `json:"voice"`
}

// saveVoice is save_voice: the picked take becomes its designed voice's clip and that voice goes on the asset (one
// edit); the preview is the edit's dry run (card board_edit), and the Yes on it is the voice rights tick.
func (s *Service) saveVoice(ctx context.Context, caller common.Caller, call magicCall, base string, a magicArgs, confirmed bool) message.ToolsReply {
	if a.TakeID == nil || !utility.IsUUID(*a.TakeID) {
		return toolErr("invalid", "args: take_id must be a uuid")
	}
	var take struct {
		MadeWith struct {
			Voices []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			} `json:"voices"`
		} `json:"made_with"`
	}
	if refused := call(http.MethodGet, "/v1/studio/takes/"+*a.TakeID, nil, &take); refused != nil {
		return *refused
	}
	vi := slices.IndexFunc(take.MadeWith.Voices, func(v struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}) bool {
		return v.Source == "designed"
	})
	if vi < 0 {
		return toolErr("invalid", "take %s was not made with a designed voice: pick a take design_voice made", *a.TakeID)
	}
	lib := "/v1/studio/library/" + url.PathEscape(take.MadeWith.Voices[vi].ID)
	var item designedVoice
	if refused := call(http.MethodGet, lib, nil, &item); refused != nil {
		return *refused
	}
	ea := a
	ea.BaseRevision = a.Revision
	ea.Ops = json.RawMessage(toJSON([]map[string]any{{"op": "asset", "id": a.Asset, "set": map[string]any{"voice_id": item.ID}}}))
	if !confirmed {
		out, _, refused := s.magicEditPreview(ctx, caller, call, base, ea, false)
		if refused != nil {
			return *refused
		}
		out["ops"], out["voice_id"], out["name"] = ea.Ops, item.ID, item.Name
		return message.ToolsReply{Preview: out}
	}
	voice := map[string]any{"clip": map[string]any{"take_id": *a.TakeID}, "consent": true}
	if v := item.Voice; v != nil {
		for key, x := range map[string]*string{"design": v.Design, "language": v.Language, "style": v.Style} {
			if x != nil {
				voice[key] = *x
			}
		}
	}
	if refused := call(http.MethodPut, lib, map[string]any{"group": item.Group, "name": item.Name, "text": "",
		"images": []any{}, "voice": voice, "version": item.Version}, nil); refused != nil {
		return *refused
	}
	var done struct {
		Revision int `json:"revision"`
	}
	if refused := call(http.MethodPost, base+"/edits", map[string]any{"base_revision": a.Revision, "ops": ea.Ops},
		&done); refused != nil {
		return *refused
	}
	var revs dto.MagicRevisionList
	summary := ""
	if call(http.MethodGet, base+"/revisions?limit=1", nil, &revs) == nil && len(revs.Revisions) > 0 {
		summary = revs.Revisions[0].OpsSummary
	}
	return message.ToolsReply{Result: map[string]any{"voice_id": item.ID, "revision": done.Revision, "summary": summary}}
}

// voiceRun makes one run of w with inputs in board id's session (revision current, asset on it), not on its sheet.
func (s *Service) voiceRun(ctx context.Context, caller common.Caller, id string, revision int, w registry.Workflow, inputs map[string]any) (dto.Run, error) {
	var run dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if revision != row.Revision {
			return staleBoard(row)
		}
		session, err := boardSession(ctx, tx, &row)
		if err != nil {
			return err
		}
		run, err = s.createRun(ctx, tx, caller, session, &dto.RunRequest{WorkflowID: w.ID, Inputs: inputs})
		return err
	})
	if err != nil {
		return run, err
	}
	return run, s.placeQueued(ctx, &run)
}

// apiRefused is err as a tool's refusal (a 4xx its text, else internal).
func apiRefused(err error, where string) message.ToolsReply {
	var refused *network.APIError
	if errors.As(err, &refused) && refused.Code < 500 {
		code := map[int]string{http.StatusNotFound: "not_found", http.StatusConflict: "conflict"}[refused.Code]
		return toolErr(cmp.Or(code, "invalid"), "%s", refused.Message)
	}
	log.Printf("studio.tools %s: %v", where, err)
	return toolErr("internal", "internal error")
}
