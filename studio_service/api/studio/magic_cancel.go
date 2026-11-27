package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// cancelRunsArgs are cancel_runs' args (tools/catalog.json): the board and exactly one of blocks, lane, run_ids, all.
type cancelRunsArgs struct {
	BoardID string   `json:"board_id"`
	Blocks  []string `json:"blocks"`
	Lane    *string  `json:"lane"`
	RunIDs  []string `json:"run_ids"`
	All     *bool    `json:"all"`
}

// cancelRunsTool is cancel_runs (owner, 2026-11-27: "yes build it"): a board's runs still queued or running stop, of
// the blocks named, a lane's, the runs named (run_blocks' started) or all of them (all and run_ids take Make views'
// runs too). Each is its run's own cancel as the caller, so a queued run drops at once and a running one stops at its
// next safe point; one that ended meanwhile is left out. No card: stopping needs no asking.
func (s *Service) cancelRunsTool(ctx context.Context, caller common.Caller, raw json.RawMessage) message.ToolsReply {
	var a cancelRunsArgs
	if strictArgs(raw, &a) != nil || !utility.IsUUID(a.BoardID) {
		return toolErr("invalid", "args: board_id must be a uuid, with one of blocks, lane, run_ids or all")
	}
	n := 0
	for _, set := range []bool{a.Blocks != nil, a.Lane != nil, a.RunIDs != nil, a.All != nil && *a.All} {
		if set {
			n++
		}
	}
	if n != 1 {
		return toolErr("invalid", "args: exactly one of blocks, lane, run_ids or all")
	}
	var board dto.MagicBoard
	got, refused := s.localCall(ctx, caller, http.MethodGet, "/v1/studio/magic/boards/"+url.PathEscape(a.BoardID), nil)
	if refused != nil {
		return *refused
	}
	if json.Unmarshal(got, &board) != nil {
		return toolErr("internal", "internal error")
	}
	lane := map[string]string{}
	for _, b := range board.Doc.Blocks {
		lane[b.ID] = b.Lane
	}
	type open struct{ block, run string }
	var going []open
	for _, r := range board.Runs {
		if openRun[r.Status] {
			going = append(going, open{r.Block, r.RunID})
		}
	}
	if a.All != nil || a.RunIDs != nil {
		for _, r := range board.AssetRuns {
			if openRun[r.Status] {
				going = append(going, open{r.Asset, r.RunID})
			}
		}
	}
	stopped := []map[string]any{}
	for _, g := range going {
		switch {
		case a.Blocks != nil && !slices.Contains(a.Blocks, g.block),
			a.Lane != nil && lane[g.block] != *a.Lane,
			a.RunIDs != nil && !slices.Contains(a.RunIDs, g.run):
			continue
		}
		var run dto.Run
		got, refused := s.localCall(ctx, caller, http.MethodPost, "/v1/studio/runs/"+g.run+"/cancel", nil)
		switch {
		case refused != nil && refused.Code == "conflict": // it ended meanwhile
			continue
		case refused != nil:
			return *refused
		case json.Unmarshal(got, &run) != nil:
			return toolErr("internal", "internal error")
		}
		// cancelled: dropped at once; running: it stops at its next safe point
		stopped = append(stopped, map[string]any{"run_id": g.run, "block": g.block, "status": run.Status})
	}
	return message.ToolsReply{Result: map[string]any{"stopped": stopped}}
}
