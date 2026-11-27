package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
)

// helm mcp's Magic tools (plan 16 §20.8; contracts/README.md, Magic: the Assistant): the board routes as the signed-in
// user. There is no card over MCP: a write applies at once (the client's own ask is the Yes, destructiveHint on the
// edits as on a sequence's), and a costly one answers the plan it ran with, as export_timeline does.

func (s *mcpServer) magicHandlers() map[string]toolFunc {
	board := func(in args) string { return "/magic/boards/" + in.esc("board_id") }
	pick := func(in args) map[string]any {
		p := in.pick("blocks", "lane", "stale")
		if len(p) == 0 {
			p["stale"] = true // plan_board's default: every block that is not done
		}
		return p
	}
	return map[string]toolFunc{
		"list_boards": plain(func(ctx context.Context, in args) (any, error) {
			if q := in.str("q"); q != "" {
				return s.get(ctx, "/magic/boards?q="+url.QueryEscape(q))
			}
			return s.get(ctx, "/magic/boards")
		}),
		"magic_kinds": plain(func(ctx context.Context, in args) (any, error) {
			var k struct {
				Kinds []map[string]any `json:"kinds"`
			}
			if err := s.c.callCtx(ctx, http.MethodGet, "/v1/studio/magic/kinds", nil, http.StatusOK, &k); err != nil {
				return nil, err
			}
			if kind := in.str("kind"); kind != "" {
				k.Kinds = slices.DeleteFunc(k.Kinds, func(x map[string]any) bool { return x["kind"] != kind })
			}
			return k, nil
		}),
		"create_board": plain(func(ctx context.Context, in args) (any, error) {
			var b struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Revision int    `json:"revision"`
			}
			if err := s.c.callCtx(ctx, http.MethodPost, "/v1/studio/magic/boards", in.pick("name", "target"), http.StatusCreated, &b); err != nil {
				return nil, err
			}
			return map[string]any{"board_id": b.ID, "name": b.Name, "revision": b.Revision}, nil
		}),
		"show_board": plain(func(ctx context.Context, in args) (any, error) {
			view, err := s.get(ctx, board(in)+"?view=agent")
			if err != nil {
				return nil, err
			}
			plan, err := s.send(ctx, http.MethodPost, board(in)+"/plan", map[string]any{"stale": true}, http.StatusOK)
			if err != nil {
				return nil, err
			}
			return map[string]any{"board": view, "plan": plan}, nil
		}),
		"try_board_edit": plain(func(ctx context.Context, in args) (any, error) { return s.editBoard(ctx, in, true) }),
		"edit_board":     plain(func(ctx context.Context, in args) (any, error) { return s.editBoard(ctx, in, false) }),
		"restore_board": plain(func(ctx context.Context, in args) (any, error) {
			raw, err := s.send(ctx, http.MethodPost, board(in)+"/restore", in.pick("revision", "to"), http.StatusOK)
			if err != nil {
				return nil, err
			}
			var b struct {
				Revision int `json:"revision"`
			}
			_ = json.Unmarshal(raw, &b)
			return map[string]any{"revision": b.Revision}, nil
		}),
		"plan_board": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, board(in)+"/plan", pick(in), http.StatusOK)
		}),
		"run_blocks": plain(func(ctx context.Context, in args) (any, error) {
			// the plan first, so the answer carries the estimate (the in-app card's ask is the client's here)
			p := in.pick("blocks", "lane", "stale")
			plan, err := s.send(ctx, http.MethodPost, board(in)+"/plan", p, http.StatusOK)
			if err != nil {
				return nil, err
			}
			var est struct {
				Seconds *float64 `json:"seconds"`
			}
			_ = json.Unmarshal(plan, &est)
			p["revision"] = in["revision"]
			raw, err := s.send(ctx, http.MethodPost, board(in)+"/run", p, http.StatusAccepted)
			if err != nil {
				return nil, err
			}
			var r struct {
				Board struct {
					Revision int `json:"revision"`
				} `json:"board"`
				Started json.RawMessage `json:"started"`
				Skipped json.RawMessage `json:"skipped"`
			}
			_ = json.Unmarshal(raw, &r)
			return map[string]any{"revision": r.Board.Revision, "started": r.Started, "skipped": r.Skipped, "seconds": est.Seconds,
				"plan": plan}, nil
		}),
		// alpha.6 (owner, 2026-11-27: "yes build it"): the board's runs still going stop, each by its own cancel, as the
		// Assistant's cancel_runs does in studio: a queued one drops at once, a running one at its next safe point
		"cancel_runs": plain(func(ctx context.Context, in args) (any, error) { return s.cancelRuns(ctx, in) }),
		"make_views": plain(func(ctx context.Context, in args) (any, error) {
			var r struct {
				Board struct {
					Revision int `json:"revision"`
				} `json:"board"`
				Started json.RawMessage `json:"started"`
			}
			if err := s.c.callCtx(ctx, http.MethodPost, "/v1/studio"+board(in)+"/assets/"+in.esc("asset")+"/views",
				in.pick("revision", "views", "model"), http.StatusAccepted, &r); err != nil {
				return nil, err
			}
			// seconds null (unknown): studio plans views in process, no route answers that estimate
			return map[string]any{"revision": r.Board.Revision, "started": r.Started, "seconds": nil}, nil
		}),
		"board_frame": plain(func(ctx context.Context, in args) (any, error) {
			var v agentView
			if err := s.c.callCtx(ctx, http.MethodGet, "/v1/studio"+board(in)+"?view=agent", nil, http.StatusOK, &v); err != nil {
				return nil, err
			}
			at, _ := in["at"].(float64)
			return v.frame(at), nil
		}),
		// the answer carries the plan (what went, the bakes); baking ones are 202, the sequence made when they finish
		"send_to_timeline": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, board(in)+"/send", in.pick("revision", "name"), anyOK)
		}),
	}
}

// editBoard sends the ops to /edits (dry run: try_board_edit) and answers the revision, diff and warnings; an applied
// edit adds the summary its revision keeps. An op that cannot apply is the API's 422 detail, as a tool error.
func (s *mcpServer) editBoard(ctx context.Context, in args, dry bool) (any, error) {
	base := "/magic/boards/" + in.esc("board_id")
	body := in.pick("base_revision", "ops")
	body["dry_run"] = dry
	var r struct {
		Revision int             `json:"revision"`
		Diff     json.RawMessage `json:"diff"`
		Warnings []string        `json:"warnings"`
	}
	if err := s.c.callCtx(ctx, http.MethodPost, "/v1/studio"+base+"/edits", body, http.StatusOK, &r); err != nil {
		return nil, err
	}
	out := map[string]any{"revision": r.Revision, "diff": r.Diff, "warnings": r.Warnings}
	if dry {
		return out, nil // ponytail: no summary on a try; studio writes it only on the stored revision
	}
	var revs struct {
		Revisions []struct {
			Revision   int    `json:"revision"`
			OpsSummary string `json:"ops_summary"`
		} `json:"revisions"`
	}
	// the newest kept revision, if it is still this edit's (another edit may have landed since)
	if s.c.callCtx(ctx, http.MethodGet, "/v1/studio"+base+"/revisions?limit=1", nil, http.StatusOK, &revs) == nil &&
		len(revs.Revisions) > 0 && revs.Revisions[0].Revision == r.Revision {
		out["summary"] = revs.Revisions[0].OpsSummary
	}
	return out, nil
}

// agentView is the part of a board's agent view (MagicAgentView) board_frame reads.
type agentView struct {
	Target struct {
		FPS int `json:"fps"`
	} `json:"target"`
	Lanes []struct {
		ID     string `json:"id"`
		Hidden bool   `json:"hidden"`
	} `json:"lanes"`
	Blocks []struct {
		ID     string  `json:"id"`
		Lane   string  `json:"lane"`
		Name   string  `json:"name"`
		Kind   string  `json:"kind"`
		At     int     `json:"at"`
		Length int     `json:"length"`
		TakeID *string `json:"take_id"`
		Status string  `json:"status"`
	} `json:"blocks"`
}

// frame is what each visible lane shows at board second at (studio's boardFrame for the assistant, the same answer).
func (v agentView) frame(at float64) map[string]any {
	fps := float64(max(v.Target.FPS, 1))
	f := int(math.Round(at * fps))
	shown := []map[string]any{}
	for _, l := range v.Lanes {
		if l.Hidden {
			continue
		}
		for _, b := range v.Blocks {
			if b.Lane == l.ID && b.At <= f && f < b.At+b.Length {
				shown = append(shown, map[string]any{"lane": l.ID, "block": b.ID, "name": b.Name, "kind": b.Kind,
					"take_id": b.TakeID, "offset_s": math.Round(float64(f-b.At)/fps*1000) / 1000, "status": b.Status})
			}
		}
	}
	return map[string]any{"at_s": at, "frame": f, "shown": shown}
}

// cancelRuns is cancel_runs: the board's runs still queued or running, of exactly one of blocks, lane, run_ids or all
// (all and run_ids take Make views' runs too), each asked to stop; one that ended meanwhile (409) is left out.
func (s *mcpServer) cancelRuns(ctx context.Context, in args) (any, error) {
	n := 0
	for _, k := range []string{"blocks", "lane", "run_ids", "all"} {
		if in[k] != nil {
			n++
		}
	}
	if n != 1 {
		return nil, errors.New("give exactly one of blocks, lane, run_ids or all")
	}
	var b struct {
		Doc struct {
			Blocks []struct{ ID, Lane string } `json:"blocks"`
		} `json:"doc"`
		Runs []struct {
			Block  string `json:"block"`
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		} `json:"runs"`
		AssetRuns []struct {
			Asset  string `json:"asset"`
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		} `json:"asset_runs"`
	}
	if err := s.c.callCtx(ctx, http.MethodGet, "/v1/studio/magic/boards/"+in.esc("board_id"), nil, http.StatusOK, &b); err != nil {
		return nil, err
	}
	lane := map[string]string{}
	for _, x := range b.Doc.Blocks {
		lane[x.ID] = x.Lane
	}
	strs := func(k string) []string {
		var out []string
		for _, v := range asList(in[k]) {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	blocks, runIDs, want := strs("blocks"), strs("run_ids"), in.str("lane")
	type open struct{ block, run string }
	var going []open
	for _, r := range b.Runs {
		if r.Status == "queued" || r.Status == "running" {
			going = append(going, open{r.Block, r.RunID})
		}
	}
	if in["all"] != nil || in["run_ids"] != nil {
		for _, r := range b.AssetRuns {
			if r.Status == "queued" || r.Status == "running" {
				going = append(going, open{r.Asset, r.RunID})
			}
		}
	}
	stopped := []map[string]any{}
	for _, g := range going {
		switch {
		case in["blocks"] != nil && !slices.Contains(blocks, g.block),
			in["lane"] != nil && lane[g.block] != want,
			in["run_ids"] != nil && !slices.Contains(runIDs, g.run):
			continue
		}
		var run struct {
			Status string `json:"status"`
		}
		err := s.c.callCtx(ctx, http.MethodPost, "/v1/studio/runs/"+url.PathEscape(g.run)+"/cancel", nil, http.StatusAccepted, &run)
		var api *apiError
		if errors.As(err, &api) && api.status == http.StatusConflict {
			continue // it ended meanwhile
		} else if err != nil {
			return nil, err
		}
		stopped = append(stopped, map[string]any{"run_id": g.run, "block": g.block, "status": run.Status})
	}
	return map[string]any{"stopped": stopped}, nil
}

func asList(v any) []any { l, _ := v.([]any); return l }
