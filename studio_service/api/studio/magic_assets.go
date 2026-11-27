package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// Magic's assets (API 1.83, plan 39 phase E; contracts/README.md "Magic: assets and the storyboard"): Make views
// runs an image model once per view of an asset (its first sheet take as the reference, when it has one); magic_runs
// keeps each run against the asset with the view's name, and the projector adds its take to the asset's sheet.

// maxSheet is the takes an asset's sheet keeps (MagicAsset.sheet).
const maxSheet = 50

// viewWords is how a view of each kind of asset is asked for.
var viewWords = map[string]string{
	"character": "A character sheet view of %s: %s. Full colour, plain light background, the same person in every view",
	"prop":      "A product sheet view of %s: %s. Plain light background, the same object in every view",
	"place":     "%s, %s. An establishing view of the place, no people",
}

// viewPrompt is the prompt of one view of asset a ("Front", "Side").
func viewPrompt(a *magic.Asset, view string) string {
	who := a.Name
	if note := strings.TrimSpace(deref(a.Note)); note != "" {
		who += " (" + note + ")"
	}
	return fmt.Sprintf(viewWords[a.Kind], who, strings.ToLower(view))
}

// addView puts take on asset id's sheet named view; false when the asset is gone, has it or its sheet is full.
func addView(d magic.Doc, id, take, view string) bool {
	a := findAsset(d, id)
	if a == nil || slices.Contains(a.Sheet, take) || len(a.Sheet) >= maxSheet {
		return false
	}
	a.Sheet = append(a.Sheet, take)
	if view != "" {
		if a.Views == nil {
			a.Views = map[string]string{}
		}
		a.Views[take] = view
	}
	return true
}

// assetRuns are the board's Make views runs, the last 20 of each asset, oldest first.
func assetRuns(ctx context.Context, tx pgx.Tx, boardID string) ([]dto.MagicAssetRun, error) {
	rows, err := collect[magicRunRow](ctx, tx, `SELECT m.asset, m.run_id::text, m.inputs_hash, m.seed, m.via, m.label,
		m.created_at, r.workflow_id, r.status, r.progress, r.error,
		coalesce((SELECT array_agg(o.take_id::text ORDER BY o.idx) FROM run_outputs o JOIN takes t ON t.id = o.take_id
			AND t.deleted_at IS NULL WHERE o.run_id = m.run_id), '{}'), '{}'::text[], NULL::float8, NULL::int
		FROM (SELECT *, row_number() OVER (PARTITION BY asset ORDER BY created_at DESC) AS n FROM magic_runs
			WHERE board_id = $1 AND asset IS NOT NULL) m
		JOIN runs r ON r.id = m.run_id AND r.deleted_at IS NULL
		WHERE m.n <= $2 ORDER BY m.created_at, m.run_id`, boardID, maxBlockRuns)
	out := []dto.MagicAssetRun{}
	for _, r := range rows {
		out = append(out, dto.MagicAssetRun{Asset: r.Block, View: deref(r.Label), RunID: r.RunID, Status: r.Status,
			Progress: runPercent(r.Progress), Error: r.Error, Takes: r.Takes, CreatedAt: r.CreatedAt})
	}
	return out, err
}

// viewBlocks are Make views' runs of asset a as image blocks (one per view, model "" the kind's first), as they run
// and as they are planned.
func viewBlocks(d magic.Doc, a *magic.Asset, views []string, model string) []*magic.Block {
	var out []*magic.Block
	for _, view := range views {
		b := &magic.Block{ID: a.ID, Kind: "image", Length: 1, Name: "@" + a.Handle,
			Request: map[string]any{"kind": "image", "prompt": viewPrompt(a, view)}}
		if model != "" {
			b.Request["model"] = model
		}
		if a.Kind == "character" || a.Kind == "prop" { // a sheet view is square
			side := float64(min(d.Target.Width, d.Target.Height, 1024))
			b.Request["width"], b.Request["height"] = side, side
		}
		out = append(out, b)
	}
	return out
}

// PlanMagicViews is the plan of Make views (API 1.93, the Assistant's make_views under the ask rule): each view an
// image run of model on board id's asset, with the caller's rates. An unknown asset plans nothing (the route says why).
func (s *Service) PlanMagicViews(ctx context.Context, caller common.Caller, id, assetID string, views []string, model string) (dto.MagicPlan, error) {
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
		a := findAsset(d, assetID)
		if a == nil {
			return nil
		}
		return s.planBlocks(ctx, tx, caller, row.OwnerID, d, viewBlocks(d, a, views, model), &out)
	})
	return out, err
}

// MakeMagicViews answers POST …/assets/{asset_id}/views: one run of the image model (the request's, else the kind's
// first) per view, in the board's session.
func (s *Service) MakeMagicViews(ctx context.Context, caller common.Caller, id, assetID string, body *dto.MagicViewsCreate) (dto.MagicViewsStarted, error) {
	if !s.registry.Ready() {
		return dto.MagicViewsStarted{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.MagicViewsStarted{Started: []dto.MagicViewStarted{}}
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
		a := findAsset(d, assetID)
		if a == nil {
			return network.NewUnprocessableError("unknown asset "+assetID, nil)
		}
		if len(a.Sheet)+len(body.Views) > maxSheet {
			return network.NewUnprocessableError("@"+a.Handle+"'s sheet keeps at most 50 views", nil)
		}
		var busy string // a view still being made is not made twice (a double click, Make the missing twice)
		err = tx.QueryRow(ctx, `SELECT coalesce(min(m.label), '') FROM magic_runs m JOIN runs r ON r.id = m.run_id
			AND r.deleted_at IS NULL WHERE m.board_id = $1 AND m.asset = $2 AND m.label = ANY($3)
			AND r.status IN ('queued', 'running')`, row.ID, a.ID, body.Views).Scan(&busy)
		if err != nil {
			return err
		}
		if busy != "" {
			return network.NewConflictError(busy+" is already being made", nil)
		}
		w, why := magicModel(s.registry.Workflows(), "image", map[string]any{"model": body.Model})
		if why != "" {
			return network.NewUnprocessableError(why, nil)
		}
		session, err := boardSession(ctx, tx, &row)
		if err != nil {
			return err
		}
		var pics magicPics
		if len(a.Sheet) > 0 { // the asset's first view keeps the others like it
			pics.Refs, pics.RefAssets = []string{a.Sheet[0]}, []string{""}
		}
		via := common.ViaOf(caller.Via)
		for i, b := range viewBlocks(d, a, body.Views, "") {
			view := body.Views[i]
			run, err := s.createRun(ctx, tx, caller, session, &dto.RunRequest{WorkflowID: w.ID,
				Inputs: standardInputs(w, d, b, pics, "", -1)})
			if err != nil {
				var refused *network.APIError
				if errors.As(err, &refused) && refused.Code < 500 {
					return network.NewUnprocessableError(view+": "+refused.Message, nil)
				}
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO magic_runs (run_id, owner_id, board_id, block, inputs_hash, seed, via,
				asset, label) VALUES ($1, $2, $3, '', '', -1, $4, $5, $6)`, run.ID, row.OwnerID, row.ID, via, a.ID, view); err != nil {
				return err
			}
			out.Started = append(out.Started, dto.MagicViewStarted{View: view, RunID: run.ID})
			placed = append(placed, run)
		}
		out.Board, err = s.boardDTO(ctx, tx, row)
		return err
	})
	if err != nil {
		return dto.MagicViewsStarted{}, err
	}
	for i := range placed {
		if err := s.placeQueued(ctx, &placed[i]); err != nil {
			return out, err
		}
	}
	return out, nil
}
