package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// The Assistant's Magic tools (API 1.87, contracts/README.md "Magic: the Assistant"; plan 39 §45.8): each one or more
// of the board routes, asked in process as the thread's owner via assistant. A card tool (edit_board, restore_board,
// run_blocks, send_to_timeline) asked without `confirmed` answers its preview and does nothing else, except that
// run_blocks starts what needs no asking (everything within the limit, else the board frames); the user's Yes asks
// again with `confirmed` under the same call id.

// magicTools are each tool's args and which it needs.
var magicTools = map[string][2][]string{
	"list_boards":      {},
	"show_board":       {{"board_id"}, {"board_id"}},
	"try_board_edit":   {{"board_id", "base_revision", "ops"}, {"board_id", "base_revision", "ops"}},
	"edit_board":       {{"board_id", "base_revision", "ops"}, {"board_id", "base_revision", "ops"}},
	"restore_board":    {{"board_id", "revision", "to"}, {"board_id", "revision", "to"}},
	"plan_board":       {{"board_id", "blocks", "lane", "stale"}, {"board_id"}},
	"run_blocks":       {{"board_id", "revision", "blocks", "lane", "stale"}, {"board_id", "revision"}},
	"board_frame":      {{"board_id", "at"}, {"board_id", "at"}},
	"send_to_timeline": {{"board_id", "revision", "name"}, {"board_id", "revision"}},
}

func isMagicTool(name string) bool { _, ok := magicTools[name]; return ok }

// magicArgs are every Magic tool's args (tools/catalog.json input); each takes only its own.
type magicArgs struct {
	BoardID      string          `json:"board_id"`
	BaseRevision int             `json:"base_revision"`
	Revision     int             `json:"revision"`
	To           int             `json:"to"`
	Ops          json.RawMessage `json:"ops"`
	Blocks       []string        `json:"blocks"`
	Lane         *string         `json:"lane"`
	Stale        *bool           `json:"stale"`
	At           *float64        `json:"at"`
	Name         *string         `json:"name"`
}

// magicFramePreview is a board frame run_blocks started before asking.
type magicFramePreview struct {
	Block   string   `json:"block"`
	Name    string   `json:"name"`
	RunID   string   `json:"run_id"`
	Seconds *float64 `json:"seconds"`
}

// magicTool answers one Magic tool call; asked is the preview it answered before the user's Yes (nil: none).
func (s *Service) magicTool(ctx context.Context, caller common.Caller, req *message.ToolsRequest, asked *message.ToolsReply) message.ToolsReply {
	raw := req.Args
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var top map[string]json.RawMessage
	var a magicArgs
	if err := json.Unmarshal(raw, &top); err != nil || top == nil || strictArgs(raw, &a) != nil {
		return toolErr("invalid", "args must be an object of the tool's input")
	}
	spec := magicTools[req.Tool]
	for key := range top {
		if !slices.Contains(spec[0], key) {
			return toolErr("invalid", "args: %s does not take %s", req.Tool, key)
		}
	}
	for _, key := range spec[1] {
		if _, ok := top[key]; !ok {
			return toolErr("invalid", "args: %s is required", key)
		}
	}
	if _, ok := top["board_id"]; ok && !utility.IsUUID(a.BoardID) {
		return toolErr("invalid", "args: board_id must be a uuid")
	}
	base := "/v1/studio/magic/boards/" + url.PathEscape(a.BoardID)
	call := func(method, path string, body any, out any) *message.ToolsReply {
		got, refused := s.localCall(ctx, caller, method, path, body)
		if refused == nil && out != nil {
			if err := json.Unmarshal(got, out); err != nil {
				r := toolErr("internal", "internal error")
				return &r
			}
		}
		return refused
	}
	pick := map[string]any{}
	for key, v := range map[string]any{"blocks": a.Blocks, "lane": a.Lane, "stale": a.Stale} {
		if _, ok := top[key]; ok {
			pick[key] = v
		}
	}
	switch req.Tool {
	case "list_boards":
		var out json.RawMessage
		return orRefused(call(http.MethodGet, "/v1/studio/magic/boards", nil, &out), out)
	case "show_board":
		var board, plan json.RawMessage
		if refused := call(http.MethodGet, base+"?view=agent", nil, &board); refused != nil {
			return *refused
		}
		if refused := call(http.MethodPost, base+"/plan", map[string]any{"stale": true}, &plan); refused != nil {
			return *refused
		}
		return message.ToolsReply{Result: map[string]any{"board": board, "plan": plan}}
	case "plan_board":
		if len(pick) == 0 {
			pick["stale"] = true // the tool's own default: what is not done
		}
		var out json.RawMessage
		return orRefused(call(http.MethodPost, base+"/plan", pick, &out), out)
	case "board_frame":
		var view dto.MagicAgentView
		if refused := call(http.MethodGet, base+"?view=agent", nil, &view); refused != nil {
			return *refused
		}
		return message.ToolsReply{Result: boardFrame(view, *a.At)}
	case "try_board_edit":
		out, _, refused := s.magicEditPreview(ctx, caller, call, base, a, false)
		return orRefused(refused, out)
	case "edit_board":
		if !req.Confirmed {
			out, estimate, refused := s.magicEditPreview(ctx, caller, call, base, a, true)
			if refused != nil {
				return *refused
			}
			out["estimate"] = estimate
			return message.ToolsReply{Preview: out}
		}
		var done struct {
			Revision int             `json:"revision"`
			Diff     json.RawMessage `json:"diff"`
			Warnings []string        `json:"warnings"`
		}
		if refused := call(http.MethodPost, base+"/edits", map[string]any{"base_revision": a.BaseRevision, "ops": a.Ops},
			&done); refused != nil {
			return *refused
		}
		var revs dto.MagicRevisionList // the summary the revision keeps (magic.Describe)
		summary := ""
		if call(http.MethodGet, base+"/revisions?limit=1", nil, &revs) == nil && len(revs.Revisions) > 0 {
			summary = revs.Revisions[0].OpsSummary
		}
		return message.ToolsReply{Result: map[string]any{"revision": done.Revision, "diff": done.Diff, "warnings": done.Warnings,
			"summary": summary}}
	case "restore_board":
		if !req.Confirmed {
			var revs dto.MagicRevisionList
			if refused := call(http.MethodGet, fmt.Sprintf("%s/revisions?before=%d&limit=1", base, a.To+1), nil, &revs); refused != nil {
				return *refused
			}
			if len(revs.Revisions) == 0 || revs.Revisions[0].Revision != a.To {
				return toolErr("invalid", "revision %d is not kept", a.To)
			}
			return message.ToolsReply{Preview: map[string]any{"revision": a.Revision, "to": revs.Revisions[0]}}
		}
		var board struct {
			Revision int `json:"revision"`
		}
		return orRefused(call(http.MethodPost, base+"/restore", map[string]any{"revision": a.Revision, "to": a.To}, &board),
			map[string]any{"revision": board.Revision})
	case "run_blocks":
		return s.magicRun(call, base, a, pick, req.Confirmed, asked)
	case "send_to_timeline":
		body := map[string]any{"revision": a.Revision, "dry_run": !req.Confirmed}
		if a.Name != nil {
			body["name"] = *a.Name
		}
		var out json.RawMessage
		if refused := call(http.MethodPost, base+"/send", body, &out); refused != nil {
			return *refused
		}
		if !req.Confirmed {
			return message.ToolsReply{Preview: out}
		}
		return message.ToolsReply{Result: out}
	}
	return toolErr("invalid", "studio does not serve %s", req.Tool)
}

// orRefused is the refusal, else result.
func orRefused(refused *message.ToolsReply, result any) message.ToolsReply {
	if refused != nil {
		return *refused
	}
	return message.ToolsReply{Result: result}
}

type magicCall = func(method, path string, body any, out any) *message.ToolsReply

// magicEditPreview is an edit's dry run as the Assistant reads it: revision, diff, warnings and summary; with estimate,
// the plan of the blocks it adds or changes on the document it would make ({seconds, host, blocks}).
func (s *Service) magicEditPreview(ctx context.Context, caller common.Caller, call magicCall, base string, a magicArgs, estimate bool) (map[string]any, map[string]any, *message.ToolsReply) {
	var before struct {
		Doc json.RawMessage `json:"doc"`
	}
	if refused := call(http.MethodGet, base, nil, &before); refused != nil {
		return nil, nil, refused
	}
	var tried struct {
		Revision int             `json:"revision"`
		Diff     magic.Diff      `json:"diff"`
		Warnings []string        `json:"warnings"`
		Board    json.RawMessage `json:"board"`
	}
	if refused := call(http.MethodPost, base+"/edits", map[string]any{"base_revision": a.BaseRevision, "ops": a.Ops, "dry_run": true},
		&tried); refused != nil {
		return nil, nil, refused
	}
	var after struct {
		ID  string          `json:"id"`
		Doc json.RawMessage `json:"doc"`
	}
	_ = json.Unmarshal(tried.Board, &after)
	was, err1 := magic.Parse(before.Doc)
	is, err2 := magic.Parse(after.Doc)
	if err1 != nil || err2 != nil {
		r := toolErr("internal", "internal error")
		return nil, nil, &r
	}
	out := map[string]any{"revision": tried.Revision, "diff": tried.Diff, "warnings": tried.Warnings,
		"summary": magic.Describe(was, is)}
	if !estimate {
		return out, nil, nil
	}
	est := map[string]any{"seconds": nil, "host": nil, "blocks": 0}
	ids := append(slices.Clone(tried.Diff.Blocks.Added), tried.Diff.Blocks.Changed...)
	ids = append(ids, tried.Diff.Transitions.Added...) // a new morph runs as a block
	if len(ids) > 0 {
		plan, err := s.PlanMagicDoc(ctx, caller, after.ID, is, ids)
		if err != nil {
			r := toolErr("internal", "internal error")
			return nil, nil, &r
		}
		n := 0
		for _, b := range plan.Blocks {
			if b.Reason == nil {
				n++
				if est["host"] == nil && b.Host != nil {
					est["host"] = *b.Host
				}
			}
		}
		est["blocks"] = n
		if n > 0 && plan.Seconds != nil {
			est["seconds"] = *plan.Seconds
		}
	}
	return out, est, nil
}

// magicRun is run_blocks: the plan first; within the limit everything runs at once (the final reply); above it, the
// board frames start and the preview is the plan with them; after the Yes the rest runs.
func (s *Service) magicRun(call magicCall, base string, a magicArgs, pick map[string]any, confirmed bool, asked *message.ToolsReply) message.ToolsReply {
	run := func(p map[string]any) (dto.MagicRunStarted, *message.ToolsReply) {
		body := map[string]any{"revision": a.Revision}
		for k, v := range p {
			body[k] = v
		}
		var out struct {
			dto.MagicRunStarted
			Board struct {
				Revision int `json:"revision"`
			} `json:"board"`
		}
		refused := call(http.MethodPost, base+"/run", body, &out)
		out.MagicRunStarted.Board.Revision = out.Board.Revision
		return out.MagicRunStarted, refused
	}
	final := func(p map[string]any) message.ToolsReply {
		got, refused := run(p)
		if refused != nil {
			return *refused
		}
		return message.ToolsReply{Result: map[string]any{"revision": got.Board.Revision, "started": got.Started, "skipped": got.Skipped}}
	}
	if confirmed {
		var before struct {
			Frames []magicFramePreview `json:"frames"`
		}
		if asked != nil {
			raw, _ := json.Marshal(asked.Preview)
			_ = json.Unmarshal(raw, &before)
		}
		if a.Blocks != nil { // named blocks: the frames already running are not named again
			var rest []string
			for _, id := range a.Blocks {
				if !slices.ContainsFunc(before.Frames, func(f magicFramePreview) bool { return f.Block == id }) {
					rest = append(rest, id)
				}
			}
			if len(rest) == 0 {
				return message.ToolsReply{Result: map[string]any{"revision": a.Revision, "started": []any{}, "skipped": []any{}}}
			}
			return final(map[string]any{"blocks": rest})
		}
		return final(pick) // a lane's or the stale pick: what runs already is left out
	}
	var plan dto.MagicPlan
	if refused := call(http.MethodPost, base+"/plan", pick, &plan); refused != nil {
		return *refused
	}
	if !plan.Ask {
		return final(pick)
	}
	var view dto.MagicAgentView
	if refused := call(http.MethodGet, base+"?view=agent", nil, &view); refused != nil {
		return *refused
	}
	if view.Revision != a.Revision {
		return toolErr("conflict", "board changed since you opened it (now revision %d)", view.Revision)
	}
	frames := []magicFramePreview{}
	var ids, rest, names, kinds []string
	var host *string
	restSeconds := new(float64)
	for _, b := range plan.Blocks {
		i := slices.IndexFunc(view.Blocks, func(x magic.AgentBlock) bool { return x.ID == b.Block })
		switch {
		case b.Reason != nil:
		case i >= 0 && view.Blocks[i].Kind == "frame":
			ids = append(ids, b.Block)
			frames = append(frames, magicFramePreview{Block: b.Block, Name: shortName(view.Blocks[i].Name), Seconds: b.Seconds})
		default:
			rest = append(rest, b.Block)
			name, kind := b.Block, "morph" // a morph is no block of the view
			if i >= 0 {
				name, kind = shortName(view.Blocks[i].Name), view.Blocks[i].Kind
			}
			names, kinds = append(names, name), append(kinds, kind)
			if host == nil {
				host = b.Host
			}
			if b.Seconds == nil || restSeconds == nil {
				restSeconds = nil
			} else {
				*restSeconds += *b.Seconds
			}
		}
	}
	var started []dto.MagicStarted
	if len(ids) > 0 { // board frames never ask (plan 39 §45.8)
		got, refused := run(map[string]any{"blocks": ids})
		if refused != nil {
			return *refused
		}
		started = got.Started
		for i := range frames {
			for _, st := range got.Started {
				if st.Block == frames[i].Block && frames[i].RunID == "" {
					frames[i].RunID = st.RunID
				}
			}
		}
	}
	if len(rest) == 0 { // only frames (or nothing that can run): nothing is left to ask
		return message.ToolsReply{Result: map[string]any{"revision": a.Revision, "started": append([]dto.MagicStarted{}, started...),
			"skipped": []any{}}}
	}
	return message.ToolsReply{Preview: map[string]any{"revision": a.Revision, "plan": plan, "frames": frames,
		"rest": map[string]any{"blocks": rest, "names": names, "seconds": restSeconds, "host": host,
			"ask_text": askText(kinds, restSeconds)}}}
}

// shortName is a block's name before its " · " words ("Shot 2 · rain": "Shot 2").
func shortName(name string) string {
	short, _, _ := strings.Cut(name, " · ")
	return short
}

// boardFrame is what each visible lane shows at board second at: the block there, its take and the second into it.
func boardFrame(v dto.MagicAgentView, at float64) map[string]any {
	fps := float64(max(v.Target.FPS, 1))
	frame := int(math.Round(at * fps))
	shown := []map[string]any{}
	for _, l := range v.Lanes {
		if l.Hidden {
			continue
		}
		for _, b := range v.Blocks {
			if b.Lane == l.ID && b.At <= frame && frame < b.At+b.Length {
				shown = append(shown, map[string]any{"lane": l.ID, "block": b.ID, "name": b.Name, "kind": b.Kind,
					"take_id": b.TakeID, "offset_s": math.Round(float64(frame-b.At)/fps*1000) / 1000, "status": b.Status})
			}
		}
	}
	return map[string]any{"at_s": at, "frame": frame, "shown": shown}
}
