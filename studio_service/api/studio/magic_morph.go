package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/registry"
)

// Morphs (API 1.82, contracts/README.md "Magic: Send to Timeline"): a morph transition runs as a block does: a video
// of its length from the first block's last frame to the second's first, by a model that takes both; its runs are
// indexed under the transition's id and its takes join the transition.

// findMorph is d's morph transition id (nil: none).
func findMorph(d magic.Doc, id string) *magic.Transition {
	for _, t := range d.Transitions {
		if t.ID == id && t.Kind == "morph" && t.Between != nil {
			return t
		}
	}
	return nil
}

// morphBlock is morph t as the block its runs are made from (nil when its blocks are gone): kind video, its length,
// its request with the frames its blocks give, marked `morph` so magicModel picks a model that takes both frames.
func morphBlock(d magic.Doc, t *magic.Transition) *magic.Block {
	if t == nil || t.Kind != "morph" || t.Between == nil {
		return nil
	}
	a, b := findBlock(d, t.Between[0]), findBlock(d, t.Between[1])
	if a == nil || b == nil {
		return nil
	}
	req := map[string]any{}
	for k, v := range t.Request {
		req[k] = v
	}
	req["kind"], req["morph"] = "video", true
	req["first"] = map[string]any{"block": a.ID, "part": "last"}
	req["last"] = map[string]any{"block": b.ID, "part": "first"}
	name := func(x *magic.Block) string { return strings.Split(x.Name, " · ")[0] }
	takes := t.Takes
	if takes == nil {
		takes = []string{}
	}
	return &magic.Block{ID: t.ID, Lane: a.Lane, At: max(a.At+a.Length-t.Length/2, 0), Length: max(t.Length, 1), Kind: "video",
		Name: "Morph · " + name(a) + " → " + name(b), Request: req, Takes: takes, Pinned: t.Pinned, Effects: []magic.Effect{}}
}

// withMorphs is d's blocks and then its morphs as blocks: what runs, plans and statuses go over.
func withMorphs(d magic.Doc) []*magic.Block {
	out := slices.Clone(d.Blocks)
	for _, t := range d.Transitions {
		if b := morphBlock(d, t); b != nil {
			out = append(out, b)
		}
	}
	return out
}

// runBlockOf is the block a run of id is made from: a block, or a morph as one.
func runBlockOf(d magic.Doc, id string) *magic.Block {
	if b := findBlock(d, id); b != nil {
		return b
	}
	return morphBlock(d, findMorph(d, id))
}

// morphModels are the video models that take a first and a last frame (all of them when none does).
func morphModels(models []registry.Workflow) []registry.Workflow {
	both := slices.DeleteFunc(slices.Clone(models), func(w registry.Workflow) bool {
		_, first := w.Standard.Fields["first"]
		_, last := w.Standard.Fields["last"]
		return !first || !last
	})
	if len(both) == 0 {
		return models
	}
	return both
}

// morphFill adds a morph run's take to its transition as a studio revision (the projector, beside magicFill); a
// morph gone since leaves the take in the gallery.
func morphFill(ctx context.Context, tx pgx.Tx, run model.Run, takeID string) error {
	var boardID, id string
	err := tx.QueryRow(ctx, `SELECT board_id::text, block FROM magic_runs WHERE run_id = $1 AND owner_id = $2`,
		run.ID, run.OwnerID).Scan(&boardID, &id)
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
	t := findMorph(doc, id)
	if t == nil || slices.Contains(t.Takes, takeID) || len(t.Takes) >= 200 {
		return nil
	}
	t.Takes = append(t.Takes, takeID)
	if why := magic.Check(doc); why != "" {
		return fmt.Errorf("magic fill of morph %s: %s", id, why)
	}
	undo, redo, err := boardPointers(ctx, tx, row.ID, row.Revision)
	if err != nil {
		return err
	}
	return storeBoard(ctx, tx, &row, doc, write{by: "studio", ops: "take"}, undo, redo)
}

// withMorphTakes is doc with every take a morph's runs made put back on it (as withRunTakes does for blocks).
func withMorphTakes(ctx context.Context, tx pgx.Tx, boardID string, doc magic.Doc) (magic.Doc, error) {
	type made struct{ Block, TakeID string }
	rows, err := collect[made](ctx, tx, `SELECT m.block, o.take_id::text FROM magic_runs m
		JOIN run_outputs o ON o.run_id = m.run_id JOIN takes t ON t.id = o.take_id AND t.deleted_at IS NULL
		WHERE m.board_id = $1 ORDER BY m.created_at, o.idx`, boardID)
	if err != nil || len(rows) == 0 {
		return doc, err
	}
	doc = doc.Clone()
	for _, r := range rows {
		if t := findMorph(doc, r.Block); t != nil && !slices.Contains(t.Takes, r.TakeID) && len(t.Takes) < 200 {
			t.Takes = append(t.Takes, r.TakeID)
		}
	}
	return doc, nil
}
