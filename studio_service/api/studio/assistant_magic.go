package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"
	"github.com/jackc/pgx/v5"

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
	"list_boards":      {{"q"}, {}},
	"create_board":     {{"name", "target"}, {}},
	"show_board":       {{"board_id"}, {"board_id"}},
	"magic_kinds":      {{"kind"}, {}},
	"try_board_edit":   {{"board_id", "base_revision", "ops"}, {"board_id", "base_revision", "ops"}},
	"edit_board":       {{"board_id", "base_revision", "ops"}, {"board_id", "base_revision", "ops"}},
	"restore_board":    {{"board_id", "revision", "to"}, {"board_id", "revision", "to"}},
	"plan_board":       {{"board_id", "blocks", "lane", "stale"}, {"board_id"}},
	"run_blocks":       {{"board_id", "revision", "blocks", "lane", "stale"}, {"board_id", "revision"}},
	"make_views":       {{"board_id", "revision", "asset", "views", "model", "width", "height", "advanced"}, {"board_id", "revision", "asset", "views"}},
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
	// API 1.93
	Q      *string         `json:"q"`
	Target json.RawMessage `json:"target"`
	Kind   *string         `json:"kind"`
	Asset  string          `json:"asset"`
	Views  []string        `json:"views"`
	Model  *string         `json:"model"`
	// API 1.98: make_views' size and the model's own params
	Width    *int           `json:"width"`
	Height   *int           `json:"height"`
	Advanced map[string]any `json:"advanced"`
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
		path := "/v1/studio/magic/boards"
		if a.Q != nil {
			path += "?q=" + url.QueryEscape(*a.Q)
		}
		var out json.RawMessage
		return orRefused(call(http.MethodGet, path, nil, &out), out)
	case "create_board": // API 1.93: an empty board, no card (nothing is lost or run)
		body := map[string]any{}
		if a.Name != nil {
			body["name"] = *a.Name
		}
		if a.Target != nil {
			body["target"] = a.Target
		}
		var board struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Revision int    `json:"revision"`
		}
		return orRefused(call(http.MethodPost, "/v1/studio/magic/boards", body, &board),
			map[string]any{"board_id": board.ID, "name": board.Name, "revision": board.Revision})
	case "magic_kinds": // API 1.93: the models, their fields, greyed ones, env and Advanced
		var out dto.MagicKinds
		if refused := call(http.MethodGet, "/v1/studio/magic/kinds", nil, &out); refused != nil {
			return *refused
		}
		if a.Kind != nil {
			out.Kinds = slices.DeleteFunc(out.Kinds, func(k dto.MagicKindInfo) bool { return k.Kind != *a.Kind })
		}
		return message.ToolsReply{Result: out}
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
		return s.magicRun(call, base, a, pick, req.Confirmed, req.Ask, asked)
	case "make_views":
		return s.magicViews(ctx, caller, call, base, a, req.Confirmed, req.Ask)
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

// magicConfirmed answers the user's Yes on a card tool's preview (API 1.87) with the call's row locked, so a Yes asked
// again while the first still runs (a timeout's retry) waits for it and answers what it did: nothing runs twice. The
// answer replaces the preview; an internal error keeps it, to be asked again.
func (s *Service) magicConfirmed(ctx context.Context, caller common.Caller, req *message.ToolsRequest) message.ToolsReply {
	var out message.ToolsReply
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT reply FROM assistant_tool_calls WHERE call_id = $1 FOR UPDATE`,
			req.CallID).Scan(&raw); err != nil {
			return err
		}
		var asked message.ToolsReply
		if err := json.Unmarshal(raw, &asked); err != nil {
			return err
		}
		if asked.Preview == nil { // answered meanwhile
			out = asked
			return nil
		}
		out = s.magicTool(ctx, caller, req, &asked)
		if out.Code == "internal" {
			return nil
		}
		if out.Code == "" {
			out.Tool = req.Tool
		}
		_, err := tx.Exec(ctx, `UPDATE assistant_tool_calls SET reply = $2 WHERE call_id = $1`, req.CallID, toJSON(out))
		return err
	})
	if err != nil {
		log.Printf("studio.tools %s: %v", req.CallID, err)
		return toolErr("internal", "internal error")
	}
	return out
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
	if assetLane(is, tried.Diff) {
		r := toolErr("invalid", "Asset pictures go on the asset's sheet: use make_views {asset, views}, not a lane or blocks of their own.")
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

// assetWords name a lane for an asset's pictures ("Characters", "Props", "Character sheets").
var assetWords = map[string]bool{"character": true, "prop": true, "place": true, "setting": true, "location": true,
	"asset": true, "view": true, "sheet": true, "cast": true, "turnaround": true}

// assetLane is whether an Assistant edit adds a lane named for asset pictures, or an image or frame on one that is not
// a storyboard: those pictures belong on the asset's sheet (make_views). Storyboard frames that @ a character stay.
// ponytail: by lane name only; an image @-picking one asset on a free lane named otherwise still passes.
func assetLane(d magic.Doc, diff magic.Diff) bool {
	named := func(id string) bool {
		for _, l := range d.Lanes {
			if l.ID == id && l.Role != "storyboard" {
				for _, w := range strings.FieldsFunc(strings.ToLower(l.Name), func(r rune) bool { return r < 'a' || r > 'z' }) {
					if assetWords[strings.TrimSuffix(w, "s")] {
						return true
					}
				}
			}
		}
		return false
	}
	if slices.ContainsFunc(diff.Lanes.Added, named) {
		return true
	}
	return slices.ContainsFunc(d.Blocks, func(b *magic.Block) bool {
		return (b.Kind == "image" || b.Kind == "frame") && slices.Contains(diff.Blocks.Added, b.ID) && named(b.Lane)
	})
}

// magicRun is run_blocks: the plan first; within the limit everything runs at once (the final reply); above it, the
// board frames start and the preview is the plan with them; after the Yes the rest runs.
func (s *Service) magicRun(call magicCall, base string, a magicArgs, pick map[string]any, confirmed, ask bool, asked *message.ToolsReply) message.ToolsReply {
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
	final := func(p map[string]any, seconds *float64) message.ToolsReply {
		got, refused := run(p)
		if refused != nil {
			return *refused
		}
		return message.ToolsReply{Result: map[string]any{"revision": got.Board.Revision, "started": got.Started, "skipped": got.Skipped,
			"seconds": seconds}}
	}
	if confirmed {
		var before struct {
			Frames []magicFramePreview `json:"frames"`
			Rest   struct {
				Seconds *float64 `json:"seconds"`
			} `json:"rest"`
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
				return message.ToolsReply{Result: map[string]any{"revision": a.Revision, "started": []any{}, "skipped": []any{},
					"seconds": 0.0}}
			}
			return final(map[string]any{"blocks": rest}, before.Rest.Seconds)
		}
		return final(pick, before.Rest.Seconds) // a lane's or the stale pick: what runs already is left out
	}
	var plan dto.MagicPlan
	if refused := call(http.MethodPost, base+"/plan", pick, &plan); refused != nil {
		return *refused
	}
	if !plan.Ask && !ask { // API 1.93: ask, Auto past its hour's caps: the preview even within the limit
		return final(pick, plan.Seconds)
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
		frameSeconds := 0.0
		for _, f := range frames {
			if f.Seconds != nil {
				frameSeconds += *f.Seconds
			}
		}
		return message.ToolsReply{Result: map[string]any{"revision": a.Revision, "started": append([]dto.MagicStarted{}, started...),
			"skipped": []any{}, "seconds": frameSeconds}}
	}
	return message.ToolsReply{Preview: map[string]any{"revision": a.Revision, "plan": plan, "frames": frames,
		"rest": map[string]any{"blocks": rest, "names": names, "seconds": restSeconds, "host": host,
			"ask_text": askText(kinds, restSeconds)}}}
}

// magicViews is make_views (API 1.93): Make views planned as image runs; within the limit (and not ask) they start at
// once, else the preview is run_blocks' {revision, plan, frames: [], rest} and the Yes starts them.
func (s *Service) magicViews(ctx context.Context, caller common.Caller, call magicCall, base string, a magicArgs, confirmed, ask bool) message.ToolsReply {
	views := &dto.MagicViewsCreate{Revision: a.Revision, Views: a.Views, Model: deref(a.Model), Width: a.Width,
		Height: a.Height, Advanced: a.Advanced}
	start := func(seconds *float64) message.ToolsReply {
		body := views
		var out struct {
			dto.MagicViewsStarted
			Board struct {
				Revision int `json:"revision"`
			} `json:"board"`
		}
		if refused := call(http.MethodPost, base+"/assets/"+url.PathEscape(a.Asset)+"/views", body, &out); refused != nil {
			return *refused
		}
		return message.ToolsReply{Result: map[string]any{"revision": out.Board.Revision, "started": out.Started, "seconds": seconds}}
	}
	plan, err := s.PlanMagicViews(ctx, caller, a.BoardID, a.Asset, views)
	if err != nil {
		var refused *network.APIError
		if errors.As(err, &refused) && refused.Code == http.StatusNotFound {
			return toolErr("not_found", "%s", refused.Message)
		}
		return toolErr("internal", "internal error")
	}
	if confirmed || (!plan.Ask && !ask) || len(plan.Blocks) == 0 { // no asset: the route says why
		return start(plan.Seconds)
	}
	for _, b := range plan.Blocks {
		if b.Reason != nil { // a view that cannot run: the route refuses it (no card for nothing)
			return start(plan.Seconds)
		}
	}
	var host *string
	if len(plan.Blocks) > 0 {
		host = plan.Blocks[0].Host
	}
	kinds := make([]string, len(a.Views))
	for i := range kinds {
		kinds[i] = "view"
	}
	plan.AskText = askText(kinds, plan.Seconds)
	return message.ToolsReply{Preview: map[string]any{"revision": a.Revision, "plan": plan, "frames": []any{},
		"rest": map[string]any{"blocks": []string{}, "names": a.Views, "seconds": plan.Seconds, "host": host,
			"ask_text": plan.AskText}}}
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
