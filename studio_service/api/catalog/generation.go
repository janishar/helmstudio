package catalog

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ora/orago/network"
	"github.com/ora/orago/utility"

	"github.com/ora/catalog_service/api/catalog/dto"
	"github.com/ora/catalog_service/api/catalog/message"
	"github.com/ora/catalog_service/api/catalog/model"
	"github.com/ora/catalog_service/common"
	"github.com/ora/catalog_service/messaging"
	"github.com/ora/catalog_service/registry"
)

// GetNextStep returns the Next step card of the session.
func (s *Service) GetNextStep(ctx context.Context, caller common.Caller, sessionID string) (dto.NextStep, error) {
	lib, prices, err := library()
	if err != nil {
		return dto.NextStep{}, err
	}
	var out dto.NextStep
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, false)
		if err == nil {
			out = NextStep(st, lib, prices)
		}
		return err
	})
	return out, err
}

// CreateRun runs a SKU set (see runSkus) and returns the run, its new images and SKUs.
func (s *Service) CreateRun(ctx context.Context, caller common.Caller, sessionID string, body *dto.RunRequest) (dto.RunAccepted, error) {
	lib, prices, err := library()
	if err != nil {
		return dto.RunAccepted{}, err
	}
	var out dto.RunAccepted
	err = s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		out, err = s.run(ctx, tx, st, lib, prices, body)
		return err
	})
	return out, err
}

// run checks the request against the session, then creates the images and queues cmd.catalog.run (inside tx).
func (s *Service) run(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, prices *registry.Prices,
	body *dto.RunRequest) (dto.RunAccepted, error) {
	if st.session.Stage != "ready" {
		return dto.RunAccepted{}, network.NewConflictError("start processing first", nil)
	}
	skus, err := runSkus(st, lib, body)
	if err != nil {
		return dto.RunAccepted{}, err
	}
	live := liveImages(st.images)
	shots := map[string][]string{} // sku id -> poses to render
	for _, sku := range skus {
		for _, pose := range sku.Brief.Poses {
			if _, ok := live[[2]string{sku.ID, pose}]; !body.MissingOnly || !ok {
				shots[sku.ID] = append(shots[sku.ID], pose)
			}
		}
	}
	skus = filter(skus, func(sku model.Sku) bool { return len(shots[sku.ID]) > 0 })
	if len(skus) == 0 {
		return dto.RunAccepted{}, network.NewConflictError("Nothing left to run there.", nil)
	}
	var category, groupKey *string
	if body.Category != "" {
		category = &body.Category
	}
	if body.GroupKey != "" {
		groupKey = &body.GroupKey
	}
	return s.queueRun(ctx, tx, st, lib, prices, body.Scope, category, groupKey, nil, skus, func(sku model.Sku) []shot {
		var out []shot
		for _, pose := range shots[sku.ID] {
			previous, _ := live[[2]string{sku.ID, pose}]
			out = append(out, shot{pose: pose, revision: previous.Revision + 1})
		}
		return out
	})
}

// stoppedError is a stopped run's error; the projector never overwrites it with the job's own final status.
const stoppedError = "Stopped"

// StopRun stops a queued or running run of the session (404 unknown, 409 ended). In one transaction the run fails
// with "Stopped", its generating images are deleted (they have no picture yet, so each SKU falls back to what it
// showed before), a command still in the outbox is dropped and a status event reaches the session's stream; then
// agents is told to stop the job. ponytail: a render already sent to the image provider still finishes and is
// billed there; agents discards it.
func (s *Service) StopRun(ctx context.Context, caller common.Caller, sessionID, runID string) (dto.RunStopped, error) {
	var out dto.RunStopped
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		i := slices.IndexFunc(st.runs, func(r model.Run) bool { return r.ID == strings.ToLower(runID) })
		if i < 0 {
			return network.NewNotFoundError("run not found", nil)
		}
		run := st.runs[i]
		if final[run.Status] {
			return network.NewConflictError("This run has already ended.", nil)
		}
		removed, err := stopRun(ctx, tx, st, run.ID)
		if err != nil {
			return err
		}
		if st, err = reload(ctx, tx, st); err != nil {
			return err
		}
		run = *st.run(run.ID)
		out = dto.RunStopped{Run: runDTO(run), Skus: s.skusDTO(st, run.SkuIDs), RemovedImageIDs: nonNil(removed)}
		return nil
	})
	if err == nil {
		s.stop.Run(out.Run.ID)
	}
	return out, err
}

// StopImages stops chosen images that are still generating (404 for an id not in the session, 409 when none of them is
// generating, e.g. stopped already); the others are left alone. In each active run they belong to they are deleted (each SKU and shot falls
// back to its previous revision); a run left with nothing generating is stopped whole (stopRun), any other records
// them in stopped_image_ids (the projector re-broadcasts the skip when the job reports running) and agents is told to
// skip them; a command not relayed yet loses their shots (dropShots).
func (s *Service) StopImages(ctx context.Context, caller common.Caller, sessionID string, body *dto.ImagesStop) (dto.ImagesStopped, error) {
	var out dto.ImagesStopped
	var whole []string
	partial := map[string][]string{} // run id -> its stopped image ids
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		byRun := map[string][]string{}
		var runIDs, skuIDs []string
		for _, id := range body.ImageIDs {
			image := st.image(strings.ToLower(id))
			if image == nil {
				var stopped bool // stopped before: its row is gone, the run remembers it
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalog_runs WHERE session_id = $1 AND $2::uuid = ANY(stopped_image_ids))`,
					st.session.ID, id).Scan(&stopped); err != nil {
					return err
				}
				if !stopped {
					return network.NewNotFoundError("image "+id+" not found", nil)
				}
				continue
			}
			if image.Status != "generating" || final[st.run(image.RunID).Status] || slices.Contains(byRun[image.RunID], image.ID) {
				continue
			}
			if byRun[image.RunID] == nil {
				runIDs = append(runIDs, image.RunID)
			}
			byRun[image.RunID] = append(byRun[image.RunID], image.ID)
			if !slices.Contains(skuIDs, image.SkuID) {
				skuIDs = append(skuIDs, image.SkuID)
			}
		}
		if len(runIDs) == 0 {
			return network.NewConflictError("Nothing of that is generating any more.", nil)
		}
		out.RemovedImageIDs = []string{}
		var touched []string
		for _, runID := range runIDs {
			removed, err := deleteGenerating(ctx, tx, runID, byRun[runID])
			if err != nil {
				return err
			}
			if len(removed) == 0 { // finished meanwhile: nothing of this run to stop
				continue
			}
			touched = append(touched, runID)
			out.RemovedImageIDs = append(out.RemovedImageIDs, removed...)
			var left bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalog_images WHERE run_id = $1 AND status = 'generating')`,
				runID).Scan(&left); err != nil {
				return err
			}
			if !left {
				if _, err := stopRun(ctx, tx, st, runID); err != nil {
					return err
				}
				whole = append(whole, runID)
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE catalog_runs SET stopped_image_ids = stopped_image_ids || $2::uuid[],
				image_ids = ARRAY(SELECT id FROM catalog_images WHERE run_id = $1 ORDER BY created_at, id) WHERE id = $1`,
				runID, removed); err != nil {
				return err
			}
			if err := dropShots(ctx, tx, runID, removed); err != nil {
				return err
			}
			partial[runID] = removed
		}
		if len(touched) == 0 {
			return network.NewConflictError("Nothing of that is generating any more.", nil)
		}
		st, err := reload(ctx, tx, st)
		if err != nil {
			return err
		}
		out.Skus = s.skusDTO(st, skuIDs)
		for _, runID := range touched {
			out.Runs = append(out.Runs, runDTO(*st.run(runID)))
		}
		return nil
	})
	if err == nil {
		for _, runID := range whole {
			s.stop.Run(runID)
		}
		for runID, imageIDs := range partial {
			s.stop.Images(runID, imageIDs)
		}
	}
	return out, err
}

// stopRun ends a run inside tx: failed "Stopped" with finished_at, its generating images deleted (returned and kept in
// stopped_image_ids, so stopping one again is a 409), image_ids kept to the finished ones, an unrelayed command dropped and a status event on the session's log. The caller
// broadcasts agents.cancel after the commit.
func stopRun(ctx context.Context, tx pgx.Tx, st *state, runID string) ([]string, error) {
	removed, err := deleteGenerating(ctx, tx, runID, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE catalog_runs SET status = 'failed', error = $2, finished_at = clock_timestamp(),
		image_ids = ARRAY(SELECT id FROM catalog_images WHERE run_id = $1 ORDER BY created_at, id),
		stopped_image_ids = stopped_image_ids || $3::uuid[] WHERE id = $1 AND status IN ('queued', 'running')`,
		runID, stoppedError, removed); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, runID); err != nil {
		return nil, err
	}
	status := map[string]any{"session_id": st.session.ID, "work": "run", "status": "failed", "error": stoppedError}
	return removed, appendEvent(ctx, tx, st.session.ID, st.session.OwnerID, "status", runID, time.Now().UTC(), status)
}

// dropShots removes the stopped images' shots from the run's command while it still waits in the outbox (a SKU left
// without shots goes too; the run keeps at least one generating image, so the command keeps a SKU), so agents never
// renders them. A command already relayed is untouched: the skip broadcast covers it.
func dropShots(ctx context.Context, tx pgx.Tx, runID string, imageIDs []string) error {
	_, err := tx.Exec(ctx, `UPDATE outbox SET data = jsonb_set(data, '{skus}', (
		SELECT coalesce(jsonb_agg(jsonb_set(sku, '{shots}', shots) ORDER BY n), '[]'::jsonb) FROM (
			SELECT sku, n, (SELECT coalesce(jsonb_agg(shot ORDER BY m), '[]'::jsonb)
				FROM jsonb_array_elements(sku->'shots') WITH ORDINALITY AS s(shot, m)
				WHERE NOT shot->>'image_id' = ANY($2::text[])) AS shots
			FROM jsonb_array_elements(data->'skus') WITH ORDINALITY AS k(sku, n)) kept
		WHERE jsonb_array_length(shots) > 0))
		WHERE job_id = $1`, runID, imageIDs)
	return err
}

// deleteGenerating deletes a run's generating images (only those of imageIDs when not nil) and returns their ids: they
// have no picture yet, so each SKU and shot falls back to what it showed before.
func deleteGenerating(ctx context.Context, tx pgx.Tx, runID string, imageIDs []string) ([]string, error) {
	rows, err := tx.Query(ctx, `DELETE FROM catalog_images WHERE run_id = $1 AND status = 'generating'
		AND ($2::uuid[] IS NULL OR id = ANY($2::uuid[])) RETURNING id::text`, runID, imageIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// runSkus resolves a run request's SKUs: sample = the first Not-run SKU of the category, batch = its Not-run SKUs of
// the group, all = every Not-run SKU of the category (or of the session), skus = exactly those (reruns allowed; 404
// for an id not in the session). SKUs of a batch not started yet (after a Refresh, before Start) are never picked.
// 409 when a chosen SKU is Queued or Running or not started, or nothing is left to run.
func runSkus(st *state, lib *registry.Library, body *dto.RunRequest) ([]model.Sku, error) {
	notRun := func(sku model.Sku) bool {
		return st.status[sku.ID] == "Not run" && resolved(sku) && (body.Category == "" || sku.Category == body.Category)
	}
	var skus []model.Sku
	switch body.Scope {
	case "sample":
		skus = filter(st.skus, notRun)
		if len(skus) > 1 {
			skus = skus[:1]
		}
	case "batch":
		skus = filter(st.skus, func(sku model.Sku) bool { return notRun(sku) && groupKey(sku) == body.GroupKey })
	case "all":
		skus = filter(st.skus, notRun)
		if len(skus) == 0 && body.Category != "" && slices.ContainsFunc(st.skus, func(sku model.Sku) bool { return sku.Category == body.Category }) {
			name := body.Category
			if category := lib.Category(body.Category); category != nil {
				name = category.Name
			}
			return nil, network.NewConflictError("Every "+singular(name)+" has been run already.", nil)
		}
	case "skus":
		for _, id := range body.SkuIDs {
			sku := st.sku(strings.ToLower(id))
			if sku == nil {
				return nil, network.NewNotFoundError("SKU "+id+" not found", nil)
			}
			if !slices.ContainsFunc(skus, func(s model.Sku) bool { return s.ID == sku.ID }) {
				skus = append(skus, *sku)
			}
		}
	}
	for _, sku := range skus {
		if status := st.status[sku.ID]; status == "Queued" || status == "Running" {
			return nil, network.NewConflictError(sku.Code+" is already generating", nil)
		}
		if !resolved(sku) {
			return nil, network.NewConflictError(sku.Code+" is in the new batch: start processing first", nil)
		}
	}
	if len(skus) == 0 {
		return nil, network.NewConflictError("Nothing left to run there.", nil)
	}
	return skus, nil
}

// shot is one image to create: its pose, revision and, for a regeneration, the image it fixes and the ratio asked for
// (empty = the SKU brief's).
type shot struct {
	pose     string
	revision int
	previous *model.Image
	ratio    string
}

// queueRun creates the run and one generating image per shot, then queues cmd.catalog.run. Every SKU needs a resolved
// background and, for a product that uses one, model (Start fills them), or the brand's description of it; each SKU renders at its own resolution
// (resolutionOf) and each shot at its own ratio (ratioOf) with its pose note; the run's ratio is the first SKU's.
// ponytail: credits are a quote only; the credit ledger would charge (and refund failures) here.
func (s *Service) queueRun(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, prices *registry.Prices,
	scope string, category, groupKey, feedback *string, skus []model.Sku, shotsOf func(model.Sku) []shot) (dto.RunAccepted, error) {
	runID := utility.NewID()
	command := message.Run{SessionID: st.session.ID, Scope: scope, Resolution: resolutionOf(lib, skus[0]), Ratio: ratioOf(lib, skus[0], ""),
		Rules: st.session.Rules, Details: detailsText(st.session.Inputs), Feedback: feedback}
	var skuIDs, imageIDs []string
	type row struct {
		id, sku, category, pose string
		revision                int
	}
	var rows []row
	quoted := 0.0
	customs, refs := customPoses(st.session.Inputs), poseRefs(st)
	for _, sku := range skus {
		resolution := resolutionOf(lib, sku)
		credits, ok := creditsPerImage(prices, resolution)
		if !ok {
			return dto.RunAccepted{}, network.NewUnprocessableError("no price for resolution "+resolution, nil)
		}
		var modelRef, backgroundRef *message.ChoiceRef
		if usesModel(lib, sku.Category) && (sku.Brief.Model != nil || sku.Brief.ModelDescription == nil) {
			ref, err := s.choiceRef(st, lib, sku.Brief.Model, true)
			if err != nil {
				return dto.RunAccepted{}, err
			}
			modelRef = &ref
		}
		if sku.Brief.Background != nil || sku.Brief.BackgroundDescription == nil { // else described only
			ref, err := s.choiceRef(st, lib, sku.Brief.Background, false)
			if err != nil {
				return dto.RunAccepted{}, err
			}
			backgroundRef = &ref
		}
		passed, notPassed := passedImages(st, sku.ID)
		runSku := message.RunSku{SkuRef: skuRef(sku), Model: modelRef, Background: backgroundRef, Resolution: resolution,
			Passed: passed, NotPassed: notPassed, BackgroundDescription: sku.Brief.BackgroundDescription}
		if usesModel(lib, sku.Category) {
			runSku.ModelDescription = sku.Brief.ModelDescription
		}
		runSku.ShotsBrief = sku.Brief.ShotsBrief
		for _, shot := range shotsOf(sku) {
			id := utility.NewID()
			name := shot.pose
			var custom *model.CustomPose
			if pose := lib.Pose(shot.pose); pose != nil {
				name = pose.Name
			} else if c, ok := skuCustom(sku, customs, shot.pose); ok {
				name, custom = c.Name, &c
			} else {
				return dto.RunAccepted{}, network.NewUnprocessableError("the shot "+shot.pose+" of "+sku.Code+
					" is no longer in the Shots selection; choose its shots again", nil)
			}
			var of *message.ImageRef
			if shot.previous != nil && shot.previous.Key != nil {
				of = &message.ImageRef{ImageID: shot.previous.ID, Key: *shot.previous.Key}
			}
			shotRatio := shot.ratio
			if shotRatio == "" {
				shotRatio = ratioOf(lib, sku, shot.pose)
			}
			out := message.Shot{ImageID: id, PoseID: shot.pose, PoseName: name, Revision: shot.revision,
				RegenerateOf: of, Ratio: shotRatio, Notes: sku.Brief.PoseNotes[shot.pose], References: refs[shot.pose]}
			if custom != nil { // agents render a custom shot from its own prompt
				out.Prompt, out.OnModel = custom.Prompt, &custom.OnModel
			}
			runSku.Shots = append(runSku.Shots, out)
			rows = append(rows, row{id, sku.ID, sku.Category, shot.pose, shot.revision})
			imageIDs = append(imageIDs, id)
			quoted += credits
		}
		skuIDs = append(skuIDs, sku.ID)
		command.Skus = append(command.Skus, runSku)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_runs (id, owner_id, session_id, scope, category, group_key, sku_ids,
		image_ids, credits_quoted, feedback) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, runID, st.session.OwnerID,
		st.session.ID, scope, category, groupKey, skuIDs, imageIDs, quoted, feedback); err != nil {
		return dto.RunAccepted{}, err
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `INSERT INTO catalog_images (id, owner_id, session_id, sku_id, run_id, shot, revision, feedback,
			category) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, r.id, st.session.OwnerID, st.session.ID, r.sku, runID,
			r.pose, r.revision, feedback, r.category); err != nil {
			return dto.RunAccepted{}, err
		}
	}
	if err := messaging.Enqueue(ctx, tx, messaging.Command{JobID: runID, OwnerID: st.session.OwnerID, Data: command}); err != nil {
		return dto.RunAccepted{}, err
	}
	st, err := reload(ctx, tx, st)
	if err != nil {
		return dto.RunAccepted{}, err
	}
	run := st.runs[slices.IndexFunc(st.runs, func(r model.Run) bool { return r.ID == runID })]
	return dto.RunAccepted{Run: runDTO(run), Images: s.imagesDTO(st, imageIDs), Skus: s.skusDTO(st, skuIDs)}, nil
}

// ratioOf is the ratio a SKU's pose renders at: the brief's pose ratio, else the brief ratio, else the library default.
func ratioOf(lib *registry.Library, sku model.Sku, pose string) string {
	if ratio := sku.Brief.PoseRatios[pose]; ratio != "" {
		return ratio
	}
	if sku.Brief.Ratio != nil {
		return *sku.Brief.Ratio
	}
	return lib.Defaults.Ratio
}

// knownRatio reports whether id is a library ratio.
func knownRatio(lib *registry.Library, id string) bool {
	return slices.ContainsFunc(lib.Ratios, func(r registry.Ratio) bool { return r.ID == id })
}

// passedImages splits a SKU's live images (the newest revision per pose) into those that passed review (still to
// review) or were approved, and the poses of the others: agents use the passed one of the product's anchor pose as a
// reference when a run has no anchor shot, and a gated product's other shots wait while its anchor pose is not passed.
// A superseded revision never counts: the brand replaced it.
func passedImages(st *state, skuID string) ([]message.Passed, []string) {
	passed, notPassed := []message.Passed{}, []string{}
	for _, image := range liveImages(st.images) {
		if image.SkuID != skuID {
			continue
		}
		var review struct {
			Passed bool `json:"passed"`
		}
		_ = json.Unmarshal(image.Review, &review)
		if image.Key != nil && (image.Status == "approved" || image.Status == "to_review" && review.Passed) {
			passed = append(passed, message.Passed{PoseID: image.Shot, Key: *image.Key})
		} else {
			notPassed = append(notPassed, image.Shot)
		}
	}
	slices.SortFunc(passed, func(a, b message.Passed) int { return strings.Compare(a.PoseID, b.PoseID) })
	slices.Sort(notPassed)
	return passed, notPassed
}

// choiceRef resolves a brief's model or background: a library item (agents resolve its id) or an upload of the owner
// (its key). 409 when the brief has none (Start fills it), 422 for an id that is neither.
func (s *Service) choiceRef(st *state, lib *registry.Library, id *string, isModel bool) (message.ChoiceRef, error) {
	what := "background"
	if isModel {
		what = "model"
	}
	if id == nil {
		return message.ChoiceRef{}, network.NewConflictError("a SKU has no "+what+" yet; start processing first", nil)
	}
	if upload := st.upload(*id); upload != nil {
		return message.ChoiceRef{ID: *id, Name: upload.Name, Key: &upload.Key}, nil
	}
	if isModel {
		if item := lib.Model(*id); item != nil {
			return message.ChoiceRef{ID: *id, Name: item.Name}, nil
		}
	} else if item := lib.Background(*id); item != nil {
		return message.ChoiceRef{ID: *id, Name: item.Name}, nil
	}
	return message.ChoiceRef{}, network.NewUnprocessableError("unknown "+what+" "+*id, nil)
}

// Regenerate renders the chosen images again with the feedback (at body.Ratio when sent, 422 for one not in the
// library), as new revisions; old ones keep their status.
func (s *Service) Regenerate(ctx context.Context, caller common.Caller, sessionID string, body *dto.RegenerateRequest) (dto.RunAccepted, error) {
	lib, prices, err := library()
	if err != nil {
		return dto.RunAccepted{}, err
	}
	if body.Ratio != "" && !knownRatio(lib, body.Ratio) {
		return dto.RunAccepted{}, network.NewUnprocessableError("unknown ratio "+body.Ratio, nil)
	}
	var out dto.RunAccepted
	err = s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		out, err = s.regenerate(ctx, tx, st, lib, prices, body.ImageIDs, strings.TrimSpace(body.Feedback), body.Ratio)
		return err
	})
	return out, err
}

// regenerate queues a regenerate run of imageIDs at ratio (empty = each shot's own; 404 for an image not in the
// session, 409 while one generates).
func (s *Service) regenerate(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, prices *registry.Prices,
	imageIDs []string, feedback, ratio string) (dto.RunAccepted, error) {
	live := liveImages(st.images)
	bySku := map[string][]shot{}
	var skus []model.Sku
	for _, id := range imageIDs {
		image := st.image(strings.ToLower(id))
		if image == nil {
			return dto.RunAccepted{}, network.NewNotFoundError("image "+id+" not found", nil)
		}
		newest := live[[2]string{image.SkuID, image.Shot}]
		if newest.Status == "generating" {
			return dto.RunAccepted{}, network.NewConflictError("an image is still generating", nil)
		}
		if slices.ContainsFunc(bySku[image.SkuID], func(s shot) bool { return s.pose == image.Shot }) {
			continue
		}
		if _, ok := bySku[image.SkuID]; !ok {
			skus = append(skus, *st.sku(image.SkuID))
		}
		bySku[image.SkuID] = append(bySku[image.SkuID], shot{pose: image.Shot, revision: newest.Revision + 1, previous: image,
			ratio: ratio})
	}
	return s.queueRun(ctx, tx, st, lib, prices, "regenerate", nil, nil, &feedback, skus, func(sku model.Sku) []shot {
		return bySku[sku.ID]
	})
}

// ReviewImage approves or rejects one image; 409 unless it is to_review, approved or rejected.
func (s *Service) ReviewImage(ctx context.Context, caller common.Caller, imageID, status string) (dto.Reviewed, error) {
	var out dto.Reviewed
	err := s.imageSession(ctx, caller, imageID, func(tx pgx.Tx, st *state) error {
		image := st.image(imageID)
		if image.Status != "to_review" && image.Status != "approved" && image.Status != "rejected" {
			return network.NewConflictError("the image is "+image.Status, nil)
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_images SET status = $2, updated_at = clock_timestamp() WHERE id = $1`,
			imageID, status); err != nil {
			return err
		}
		st, err := reload(ctx, tx, st)
		if err != nil {
			return err
		}
		image = st.image(imageID)
		sku := st.sku(image.SkuID)
		out = dto.Reviewed{Image: s.imageDTO(*image), Sku: s.skuDTO(*sku, st.status[sku.ID])}
		return nil
	})
	return out, err
}

// PickTry keeps another render try of an image as the image (the brand overrides the reviewer's best): the image's key
// and review become that try's, the try is best and picked; the status stays (an approved image stays approved: the
// brand chose this try). 409 while generating or failed; 422 for an attempt the image does not have.
func (s *Service) PickTry(ctx context.Context, caller common.Caller, imageID string, attempt int) (dto.Reviewed, error) {
	var out dto.Reviewed
	err := s.imageSession(ctx, caller, imageID, func(tx pgx.Tx, st *state) error {
		image := st.image(imageID)
		if image.Status == "generating" || image.Status == "failed" {
			return network.NewConflictError("the image is "+image.Status, nil)
		}
		at := slices.IndexFunc(image.Tries, func(t model.Try) bool { return t.N == attempt })
		if at < 0 {
			return network.NewUnprocessableError("the image has no attempt "+strconv.Itoa(attempt), nil)
		}
		tries := slices.Clone(image.Tries)
		for i := range tries {
			tries[i].Best, tries[i].Picked = i == at, i == at
		}
		review := tries[at].Review
		if len(review) == 0 || string(review) == "null" {
			review = nil
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_images SET key = $2, review = $3::jsonb, tries = $4,
			updated_at = clock_timestamp() WHERE id = $1`, imageID, tries[at].Key, nilText(review), tries); err != nil {
			return err
		}
		st, err := reload(ctx, tx, st)
		if err != nil {
			return err
		}
		image = st.image(imageID)
		sku := st.sku(image.SkuID)
		out = dto.Reviewed{Image: s.imageDTO(*image), Sku: s.skuDTO(*sku, st.status[sku.ID])}
		return nil
	})
	return out, err
}

// imageSession runs fn on the state of the session that holds imageID; 404 when the caller has no such image.
func (s *Service) imageSession(ctx context.Context, caller common.Caller, imageID string, fn func(tx pgx.Tx, st *state) error) error {
	if !utility.IsUUID(imageID) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		var sessionID string
		if err := tx.QueryRow(ctx, `SELECT session_id::text FROM catalog_images WHERE id = $1`, imageID).Scan(&sessionID); err != nil {
			if err == pgx.ErrNoRows {
				return errNotFound
			}
			return err
		}
		st, err := loadState(ctx, tx, sessionID, true)
		if err != nil {
			return err
		}
		return fn(tx, st)
	})
}

// Approve approves every to_review image among the chosen images, or the live ones of the chosen SKUs or category.
func (s *Service) Approve(ctx context.Context, caller common.Caller, sessionID string, body *dto.ApproveRequest) (dto.Approved, error) {
	var out dto.Approved
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		var err error
		out, err = s.approve(ctx, tx, st, body)
		return err
	})
	return out, err
}

// approve does Approve inside tx; 404 for an image or SKU id not in the session.
func (s *Service) approve(ctx context.Context, tx pgx.Tx, st *state, body *dto.ApproveRequest) (dto.Approved, error) {
	var chosen []model.Image
	for _, id := range body.ImageIDs {
		image := st.image(strings.ToLower(id))
		if image == nil {
			return dto.Approved{}, network.NewNotFoundError("image "+id+" not found", nil)
		}
		chosen = append(chosen, *image)
	}
	if body.SkuIDs != nil || body.Category != "" {
		var skus []string
		for _, id := range body.SkuIDs {
			sku := st.sku(strings.ToLower(id))
			if sku == nil {
				return dto.Approved{}, network.NewNotFoundError("SKU "+id+" not found", nil)
			}
			skus = append(skus, sku.ID)
		}
		for _, sku := range st.skus {
			if body.Category != "" && sku.Category == body.Category {
				skus = append(skus, sku.ID)
			}
		}
		for _, image := range liveImages(st.images) {
			if slices.Contains(skus, image.SkuID) {
				chosen = append(chosen, image)
			}
		}
	}
	var ids, skuIDs []string
	for _, image := range chosen {
		if image.Status == "to_review" && !slices.Contains(ids, image.ID) {
			ids, skuIDs = append(ids, image.ID), appendNew(skuIDs, image.SkuID)
		}
	}
	out := dto.Approved{Approved: len(ids), Images: []dto.Image{}, Skus: []dto.Sku{}}
	if len(ids) == 0 {
		return out, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE catalog_images SET status = 'approved', updated_at = clock_timestamp()
		WHERE id = ANY($1::uuid[])`, ids); err != nil {
		return out, err
	}
	st, err := reload(ctx, tx, st)
	if err != nil {
		return out, err
	}
	out.Images, out.Skus = s.imagesDTO(st, ids), s.skusDTO(st, skuIDs)
	return out, nil
}

// PatchSku moves a SKU to another product, sets or clears its subcategory or wearer, or overrides its model, background or poses
// (and, with apply_to_category, every SKU of its category not Approved), or confirms its attributes. 422 for an
// unknown category, a subcategory of another product, an id that is neither in the library nor an upload of the
// session, a pose of another product or a model for a product without one.
func (s *Service) PatchSku(ctx context.Context, caller common.Caller, skuID string, body *dto.SkuPatch) (dto.SkuList, error) {
	lib, _, err := library()
	if err != nil {
		return dto.SkuList{}, err
	}
	if !utility.IsUUID(skuID) {
		return dto.SkuList{}, errNotFound
	}
	var out dto.SkuList
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		var sessionID string
		if err := tx.QueryRow(ctx, `SELECT session_id::text FROM catalog_skus WHERE id = $1`, skuID).Scan(&sessionID); err != nil {
			if err == pgx.ErrNoRows {
				return errNotFound
			}
			return err
		}
		st, err := loadState(ctx, tx, sessionID, true)
		if err != nil {
			return err
		}
		patch := message.BriefPatch{Poses: body.Poses, ModelDescription: body.ModelDescription.Trimmed(),
			BackgroundDescription: body.BackgroundDescription.Trimmed()}
		product := st.sku(skuID).Category // the product the brief fields belong to
		var to *registry.Category
		if body.Category != nil {
			if to = lib.Category(*body.Category); to == nil {
				return network.NewUnprocessableError("unknown category "+*body.Category, nil)
			}
			product = to.ID
		}
		if sub := body.Subcategory.Value; sub != nil && !hasSubcategory(lib.Category(product), *sub) {
			return network.NewUnprocessableError("subcategory "+*sub+" is not a "+product+" subcategory", nil)
		}
		if d := patch.ModelDescription; d != nil && *d != "" && !usesModel(lib, product) {
			return network.NewUnprocessableError(lib.Category(product).Name+" are shot without a model", nil)
		}
		if body.Model != nil {
			if !usesModel(lib, product) {
				return network.NewUnprocessableError(lib.Category(product).Name+" are shot without a model", nil)
			}
			patch.Model = *body.Model
			if _, err := s.choiceRef(st, lib, body.Model, true); err != nil {
				return err
			}
		}
		if body.Background != nil {
			if cat := lib.Category(product); cat != nil && !suits(lib, *body.Background, cat.Family) {
				return network.NewUnprocessableError("background "+*body.Background+" does not suit "+cat.Name, nil)
			}
			patch.Background = *body.Background
			if _, err := s.choiceRef(st, lib, body.Background, false); err != nil {
				return err
			}
		}
		poses := slices.Clone(body.Poses)
		for pose := range body.PoseRatios {
			poses = append(poses, pose)
		}
		for pose := range body.PoseNotes {
			poses = append(poses, pose)
		}
		for _, pose := range poses {
			if _, category, ok := findPose(st, lib, pose); !ok {
				return network.NewUnprocessableError("unknown pose "+pose, nil)
			} else if category != product {
				return network.NewUnprocessableError("pose "+pose+" is not a "+product+" pose", nil)
			}
		}
		for _, ratio := range body.PoseRatios {
			if !knownRatio(lib, ratio) {
				return network.NewUnprocessableError("unknown ratio "+ratio, nil)
			}
		}
		if body.Wearer.Set { // this SKU's own, before its model is set: a model of the other wearer is dropped
			if err := setWearer(lib, st.sku(skuID), body.Wearer.Value, choices(st.session.Inputs).Models); err != nil {
				return err
			}
		}
		ids, err := s.setBriefs(ctx, tx, st, lib, *st.sku(skuID), patch, to, body.Subcategory, body.ApplyToCategory,
			body.Confirmed, body.PoseRatios, body.PoseNotes)
		if err != nil {
			return err
		}
		if st, err = reload(ctx, tx, st); err == nil {
			out = dto.SkuList{Skus: s.skusDTO(st, ids)}
		}
		return err
	})
	return out, err
}

// setBriefs moves sku to the product to (when not nil) and writes patch on it, or on every SKU of its category not
// Approved (a built-in model only on those of its wear), with the pose ratios and notes maps replaced when not nil; the subcategory (when sent) and confirmed are
// sku's own and never spread. Returns their ids.
func (s *Service) setBriefs(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, sku model.Sku,
	patch message.BriefPatch, to *registry.Category, subcategory dto.Nullable, toCategory bool, confirmed *bool,
	poseRatios, poseNotes map[string]string) ([]string, error) {
	var ids []string
	for i := range st.skus {
		target := &st.skus[i]
		if target.ID != sku.ID && (!toCategory || target.Category != sku.Category || st.status[target.ID] == "Approved") {
			continue
		}
		if to != nil {
			moveSku(st, lib, target, to)
		}
		if subcategory.Set && target.ID == sku.ID {
			target.Subcategory = subcategory.Value
		}
		own := patch
		if target.ID != sku.ID && own.Model != "" && !lib.SuitsWearer(own.Model, wearerOf(*target)) {
			own.Model = "" // Apply to all never puts a women's model on a men's SKU (nor the other way)
		}
		setOverrides(target, own)
		keepCustomPoses(st, target)
		fillDefaults(lib, target)
		if poseRatios != nil {
			target.Brief.PoseRatios = maps.Clone(poseRatios)
		}
		if poseNotes != nil {
			target.Brief.PoseNotes = maps.Clone(poseNotes)
		}
		if confirmed != nil && target.ID == sku.ID {
			target.Confirmed = *confirmed
		}
		if err := saveSku(ctx, tx, target); err != nil {
			return nil, err
		}
		ids = append(ids, target.ID)
	}
	return ids, nil
}

// moveSku puts a SKU in another product, keeping its code and images: its poses become the session's choice for that
// product, else its defaults; a product without a model clears the model, one with a model gets the session's first
// pick (else its default; none when the models step only describes one) and the step's description when the brief had
// neither, and a built-in background made for another family becomes the product's default. Models follow the SKU's
// wearer (only fitting picks; a men's SKU gets the product's men's default). The subcategory, group,
// variant link and old pose overrides belonged to the old product; across families the old family's attributes (a
// ring's stone count on a sofa) are dropped and the SKU is unconfirmed.
func moveSku(st *state, lib *registry.Library, sku *model.Sku, to *registry.Category) {
	picked := choices(st.session.Inputs)
	from := sku.Category
	if sku.Category != to.ID {
		key, label := to.ID, to.Name
		sku.GroupKey, sku.GroupLabel, sku.VariantOf = &key, &label, nil
	}
	if from := lib.Category(sku.Category); from == nil || from.Family != to.Family {
		sku.Attributes, sku.Confirmed = commonAttributes(sku.Attributes), false
	}
	if to.Wearer != "" { // a product of one wear (men's rings): the SKU takes it, and a model of that wear
		wearer := to.Wearer
		_ = setWearer(lib, sku, &wearer, picked.Models) // stored attributes are a JSON object (agents validated them)
	}
	poses := picked.Poses[to.ID]
	if len(poses) == 0 {
		poses = to.DefaultPoses
	}
	sku.Category, sku.Subcategory = to.ID, nil
	sku.Brief.ShotsBrief = shotsBrief(st.session.Inputs, to.ID)
	sku.Overrides.Poses, sku.Brief.Poses = nil, slices.Clone(nonNil(poses))
	if sku.Category != from {
		sku.Brief.PoseRatios, sku.Brief.PoseNotes = nil, nil // the old product's poses
	}
	// a built-in background of another family (fashion studio on a sofa) falls back to the product's default
	if sku.Brief.Background != nil && !suits(lib, *sku.Brief.Background, to.Family) {
		def := to.DefaultBackground
		sku.Overrides.Background, sku.Brief.Background = "", &def
	}
	switch {
	case !to.UsesModel:
		sku.Overrides.Model, sku.Brief.Model = "", nil
		sku.Overrides.ModelDescription, sku.Brief.ModelDescription = nil, nil
	case sku.Brief.Model == nil && sku.Brief.ModelDescription == nil && resolved(*sku):
		if sku.Overrides.ModelDescription == nil { // the models step's description goes with the model, as at Start
			sku.Brief.ModelDescription = nonBlank(stepText(st.session.Inputs["models"]))
		}
		wearer := wearerOf(*sku)
		modelID := to.DefaultModelFor(wearer)
		if picks := filter(picked.Models, func(id string) bool { return lib.SuitsWearer(id, wearer) }); len(picks) > 0 {
			modelID = picks[0]
		} else if sku.Brief.ModelDescription != nil {
			modelID = "" // described only
		}
		if modelID != "" {
			sku.Brief.Model = &modelID
		}
	}
}

// hasSubcategory reports whether the product (nil: unknown) has the subcategory id.
func hasSubcategory(cat *registry.Category, id string) bool {
	return cat != nil && slices.ContainsFunc(cat.Subcategories, func(sub registry.Subcategory) bool { return sub.ID == id })
}

// Download zips the newest approved revision of each SKU × shot as <code>/<shot>.<ext> plus catalog.csv, and returns
// a signed link; 409 when nothing is approved.
func (s *Service) Download(ctx context.Context, caller common.Caller, sessionID string) (dto.Download, error) {
	var entries []Entry
	var skus int
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, false)
		if err != nil {
			return err
		}
		entries, skus = approvedEntries(st)
		return nil
	})
	if err != nil {
		return dto.Download{}, err
	}
	if len(entries) == 0 {
		return dto.Download{}, network.NewConflictError("Nothing is approved yet.", nil)
	}
	return s.zip(ctx, caller.ID, entries, skus)
}

// zip writes the entries to the owner's archives/ prefix and returns the signed link.
func (s *Service) zip(ctx context.Context, owner string, entries []Entry, skus int) (dto.Download, error) {
	key := common.OwnerPrefix(owner) + "archives/" + utility.NewID() + ".zip"
	if err := writeZip(ctx, s.store, key, entries); err != nil {
		return dto.Download{}, err
	}
	return dto.Download{URL: s.signer.Sign(key), FileName: "catalog_approved.zip", Skus: skus, Images: len(entries) - 1}, nil
}

// approvedEntries lists the zip's files: the newest approved revision per SKU × shot and a catalog.csv describing
// them; none when nothing is approved. Also returns how many SKUs have an approved image.
func approvedEntries(st *state) ([]Entry, int) {
	newest := map[[2]string]model.Image{}
	for _, image := range st.images {
		key := [2]string{image.SkuID, image.Shot}
		if current, ok := newest[key]; image.Status == "approved" && image.Key != nil && (!ok || image.Revision > current.Revision) {
			newest[key] = image
		}
	}
	if len(newest) == 0 {
		return nil, 0
	}
	var entries []Entry
	csv := [][]string{{"sku", "name", "category", "shot", "file"}}
	skus := 0
	for _, sku := range st.skus {
		found := false
		for _, image := range st.images {
			if chosen, ok := newest[[2]string{sku.ID, image.Shot}]; ok && chosen.ID == image.ID {
				name := sku.Code + "/" + image.Shot + extOf(*image.Key)
				entries = append(entries, Entry{Key: *image.Key, Name: name})
				csv = append(csv, []string{sku.Code, sku.Name, sku.Category, image.Shot, name})
				found = true
			}
		}
		if found {
			skus++
		}
	}
	return append(entries, Entry{Name: "catalog.csv", Data: csvBytes(csv)}), skus
}

// extOf returns a key's extension with its dot, ".png" when it has none.
func extOf(key string) string {
	if i := strings.LastIndex(key, "."); i > strings.LastIndex(key, "/") {
		return key[i:]
	}
	return ".png"
}

// skuCustom is a custom shot of the SKU's product: the definition its brief keeps, else the poses step's.
func skuCustom(sku model.Sku, session map[string]model.CustomPose, pose string) (model.CustomPose, bool) {
	if i := slices.IndexFunc(sku.Brief.CustomPoses, func(c model.CustomPose) bool { return c.ID == pose }); i >= 0 {
		return sku.Brief.CustomPoses[i], true
	}
	c, ok := session[pose]
	return c, ok && c.Category == sku.Category
}
