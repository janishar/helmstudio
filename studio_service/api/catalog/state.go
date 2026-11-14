package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/catalog/dto"
	"github.com/helmstudio/studio_service/api/catalog/model"
	"github.com/helmstudio/studio_service/registry"
)

// errNotFound is the 404 of an unknown id, a malformed one and another user's.
var errNotFound = network.NewNotFoundError("not found", nil)

// state is one session and everything hanging off it, read in one transaction. Most routes read it whole: a session
// holds at most a few thousand rows, and the derived SKU statuses need the images and runs anyway.
type state struct {
	session   *model.Session
	process   *model.Process // the queued or running processing job, else the newest; nil = idle
	uploads   []model.Upload // oldest first
	skus      []model.Sku    // category order, then code
	questions []model.Question
	runs      []model.Run   // newest first
	images    []model.Image // oldest first
	status    map[string]string
}

// loadState reads the session (locked FOR UPDATE when lock, so mutations of one session run one at a time) and its
// rows; errNotFound when the caller can't see it.
func loadState(ctx context.Context, tx pgx.Tx, sessionID string, lock bool) (*state, error) {
	if !utility.IsUUID(sessionID) {
		return nil, errNotFound
	}
	query := `SELECT ` + model.SessionCols + ` FROM catalog_sessions WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, _ := tx.Query(ctx, query, sessionID)
	session, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByPos[model.Session])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	} else if err != nil {
		return nil, err
	}
	st := &state{session: session}
	rows, _ = tx.Query(ctx, `SELECT id::text, status, error FROM catalog_processes WHERE session_id = $1
		ORDER BY status IN ('queued', 'running') DESC, created_at DESC LIMIT 1`, sessionID)
	if processes, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByPos[model.Process]); err != nil {
		return nil, err
	} else if len(processes) == 1 {
		st.process = processes[0]
	}
	if st.uploads, err = collect[model.Upload](ctx, tx, `SELECT `+model.UploadCols+` FROM catalog_uploads WHERE session_id = $1
		ORDER BY created_at, id`, sessionID); err != nil {
		return nil, err
	}
	if st.skus, err = collect[model.Sku](ctx, tx, `SELECT `+model.SkuCols+` FROM catalog_skus WHERE session_id = $1
		ORDER BY code`, sessionID); err != nil {
		return nil, err
	}
	sortSkus(st.skus)
	if st.questions, err = collect[model.Question](ctx, tx, `SELECT `+model.QuestionCols+` FROM catalog_questions
		WHERE session_id = $1 ORDER BY created_at, id`, sessionID); err != nil {
		return nil, err
	}
	if st.runs, err = collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM catalog_runs WHERE session_id = $1
		ORDER BY created_at DESC, id`, sessionID); err != nil {
		return nil, err
	}
	if st.images, err = collect[model.Image](ctx, tx, `SELECT `+model.ImageCols+` FROM catalog_images WHERE session_id = $1
		ORDER BY created_at, revision, id`, sessionID); err != nil {
		return nil, err
	}
	st.status = skuStatuses(st.skus, st.images, st.runs)
	return st, nil
}

// collect runs a query and scans every row into T by position.
func collect[T any](ctx context.Context, tx pgx.Tx, query string, args ...any) ([]T, error) {
	rows, _ := tx.Query(ctx, query, args...)
	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

// sortSkus orders SKUs by the library's category order (unknown categories last), then by code.
func sortSkus(skus []model.Sku) {
	order := categoryOrder()
	slices.SortStableFunc(skus, func(a, b model.Sku) int {
		if d := rank(order, a.Category) - rank(order, b.Category); d != 0 {
			return d
		}
		return strings.Compare(a.Code, b.Code)
	})
}

// categoryOrder is the library's category ids in run order; none before the registry is loaded.
func categoryOrder() []string {
	var order []string
	if library := registry.Current().Library; library != nil {
		for _, category := range library.Categories {
			order = append(order, category.ID)
		}
	}
	return order
}

// rank is a category's place in order, len(order) when it isn't there.
func rank(order []string, category string) int {
	if i := slices.Index(order, category); i >= 0 {
		return i
	}
	return len(order)
}

// liveImages returns the newest revision of each SKU × shot, keyed by [sku_id, shot].
func liveImages(images []model.Image) map[[2]string]model.Image {
	live := map[[2]string]model.Image{}
	for _, image := range images {
		key := [2]string{image.SkuID, image.Shot}
		if current, ok := live[key]; !ok || image.Revision > current.Revision {
			live[key] = image
		}
	}
	return live
}

// skuStatuses derives each SKU's status from its live images: none = Not run; one generating = Queued while its run
// is queued, else Running; one rejected or failed = Redo; all approved = Approved; else To review.
func skuStatuses(skus []model.Sku, images []model.Image, runs []model.Run) map[string]string {
	runStatus := map[string]string{}
	for _, run := range runs {
		runStatus[run.ID] = run.Status
	}
	bySku := map[string][]model.Image{}
	for _, image := range liveImages(images) {
		bySku[image.SkuID] = append(bySku[image.SkuID], image)
	}
	status := map[string]string{}
	for _, sku := range skus {
		live := bySku[sku.ID]
		status[sku.ID] = "Not run"
		if len(live) == 0 {
			continue
		}
		queued, running, redo, approved := false, false, false, 0
		for _, image := range live {
			switch image.Status {
			case "generating":
				if runStatus[image.RunID] == "queued" {
					queued = true
				} else {
					running = true
				}
			case "rejected", "failed":
				redo = true
			case "approved":
				approved++
			}
		}
		switch {
		case running:
			status[sku.ID] = "Running"
		case queued:
			status[sku.ID] = "Queued"
		case redo:
			status[sku.ID] = "Redo"
		case approved == len(live):
			status[sku.ID] = "Approved"
		default:
			status[sku.ID] = "To review"
		}
	}
	return status
}

// processing reports whether a processing job is queued or running (loadState picks an unfinished one first).
func (st *state) processing() bool {
	return st.process != nil && (st.process.Status == "queued" || st.process.Status == "running")
}

// running reports whether a run of the session is queued or running.
func (st *state) running() bool {
	for _, run := range st.runs {
		if run.Status == "queued" || run.Status == "running" {
			return true
		}
	}
	return false
}

// inSetup reports whether Input is open: before Start, or after a Refresh until Processing opens again.
func (st *state) inSetup() bool {
	return st.session.Stage == "setup" || st.session.FlowPosition != "done"
}

// resolved reports whether Start has filled the SKU's brief: SKUs of a batch not started yet can't run, and Start
// resolves only those, so an earlier batch keeps the setup it was processed with.
func resolved(sku model.Sku) bool { return sku.Brief.Resolution != nil }

// sku finds a SKU by id or code; nil when absent.
func (st *state) sku(idOrCode string) *model.Sku {
	for i := range st.skus {
		if st.skus[i].ID == idOrCode || st.skus[i].Code == idOrCode {
			return &st.skus[i]
		}
	}
	return nil
}

// run finds a run by id; nil when absent.
func (st *state) run(id string) *model.Run {
	for i := range st.runs {
		if st.runs[i].ID == id {
			return &st.runs[i]
		}
	}
	return nil
}

// image finds an image by id; nil when absent.
func (st *state) image(id string) *model.Image {
	for i := range st.images {
		if st.images[i].ID == id {
			return &st.images[i]
		}
	}
	return nil
}

// upload finds an upload by id; nil when absent.
func (st *state) upload(id string) *model.Upload {
	for i := range st.uploads {
		if st.uploads[i].ID == id {
			return &st.uploads[i]
		}
	}
	return nil
}

// ------------------------------------------------------------------ mapping to the contract's shapes

// sessionDTO maps the session with its processing, uploads and rules (keys become signed links).
func (s *Service) sessionDTO(st *state) dto.Session {
	session := st.session
	out := dto.Session{ID: session.ID, ProjectName: session.ProjectName, Stage: session.Stage,
		FlowPosition: session.FlowPosition, Inputs: session.Inputs, Messages: session.Messages,
		Processing: dto.Processing{Status: "idle"}, Uploads: []dto.Upload{}, CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt}
	if st.process != nil {
		out.Processing = dto.Processing{Status: st.process.Status, ProcessID: &st.process.ID, Error: st.process.Error}
	}
	for _, upload := range st.uploads {
		out.Uploads = append(out.Uploads, dto.Upload{ID: upload.ID, Kind: upload.Kind, Name: upload.Name,
			ContentType: upload.ContentType, Size: upload.Size, Category: upload.Category, URL: s.signer.Sign(upload.Key),
			CreatedAt: upload.CreatedAt})
	}
	out.Rules = s.rulesDTO(session.Rules)
	return out
}

// storedRules is evt.catalog.skus data.rules as stored: reference images are object keys.
type storedRules struct {
	Background      *string `json:"background"`
	Styling         *string `json:"styling"`
	Mood            *string `json:"mood"`
	MetalReferences []struct {
		Finish string `json:"finish"`
		Key    string `json:"key"`
	} `json:"metal_references"`
	WornReferences []string `json:"worn_references"`
	Dimensions     *string  `json:"dimensions"`
	Notes          []string `json:"notes"`
}

// rulesDTO maps stored rules to the contract's Rules with signed links; nil when none were read.
func (s *Service) rulesDTO(raw json.RawMessage) *dto.Rules {
	var rules *storedRules
	if len(raw) == 0 || json.Unmarshal(raw, &rules) != nil || rules == nil {
		return nil
	}
	out := &dto.Rules{Background: rules.Background, Styling: rules.Styling, Mood: rules.Mood, Dimensions: rules.Dimensions,
		MetalReferences: []dto.MetalRefLink{}, WornReferences: []string{}, Notes: rules.Notes}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	for _, reference := range rules.MetalReferences {
		out.MetalReferences = append(out.MetalReferences, dto.MetalRefLink{Finish: reference.Finish, URL: s.signer.Sign(reference.Key)})
	}
	for _, key := range rules.WornReferences {
		out.WornReferences = append(out.WornReferences, s.signer.Sign(key))
	}
	return out
}

// skuDTO maps a SKU with its derived status.
func (s *Service) skuDTO(sku model.Sku, status string) dto.Sku {
	brief := sku.Brief
	if brief.Poses == nil {
		brief.Poses = []string{}
	}
	if brief.PoseRatios == nil {
		brief.PoseRatios = map[string]string{}
	}
	if brief.PoseNotes == nil {
		brief.PoseNotes = map[string]string{}
	}
	out := dto.Sku{ID: sku.ID, Code: sku.Code, Name: sku.Name, Category: sku.Category, Subcategory: sku.Subcategory,
		GroupKey: sku.GroupKey, GroupLabel: sku.GroupLabel, VariantOf: sku.VariantOf, Attributes: sku.Attributes,
		Confirmed: sku.Confirmed, Brief: brief, Status: status, RawURLs: []string{}, RawViews: []*string{}, UpdatedAt: sku.UpdatedAt}
	var tags struct {
		Sides map[string]*string `json:"sides"`
	}
	_ = json.Unmarshal(sku.Attributes, &tags) // agents validated the attributes; no sides = all untagged
	for _, key := range sku.RawKeys {
		out.RawURLs = append(out.RawURLs, s.signer.Sign(key))
		out.RawViews = append(out.RawViews, tags.Sides[key])
	}
	return out
}

// skusDTO maps the SKUs whose ids are in ids (all when ids is nil), in the state's order.
func (s *Service) skusDTO(st *state, ids []string) []dto.Sku {
	out := []dto.Sku{}
	for _, sku := range st.skus {
		if ids == nil || slices.Contains(ids, sku.ID) {
			out = append(out, s.skuDTO(sku, st.status[sku.ID]))
		}
	}
	return out
}

// imageDTO maps an image; its link is signed once it has a key, and every render try is signed too.
func (s *Service) imageDTO(image model.Image) dto.Image {
	out := dto.Image{ID: image.ID, SkuID: image.SkuID, RunID: image.RunID, Shot: image.Shot, Revision: image.Revision,
		Status: image.Status, Feedback: image.Feedback, Review: image.Review, Attempts: []dto.ImageAttempt{},
		Error: image.Error, CreatedAt: image.CreatedAt, UpdatedAt: image.UpdatedAt}
	for _, try := range image.Tries {
		review := try.Review
		if len(review) == 0 {
			review = json.RawMessage("null")
		}
		out.Attempts = append(out.Attempts, dto.ImageAttempt{Index: try.N, URL: s.signer.Sign(try.Key), Review: review,
			Best: try.Best, Picked: try.Picked})
	}
	if image.Key != nil && image.Status != "generating" && image.Status != "failed" {
		url := s.signer.Sign(*image.Key)
		out.URL = &url
	}
	return out
}

// imagesDTO maps the images whose ids are in ids (all when ids is nil).
func (s *Service) imagesDTO(st *state, ids []string) []dto.Image {
	out := []dto.Image{}
	for _, image := range st.images {
		if ids == nil || slices.Contains(ids, image.ID) {
			out = append(out, s.imageDTO(image))
		}
	}
	return out
}

// runDTO maps a run.
func runDTO(run model.Run) dto.Run {
	return dto.Run{ID: run.ID, Scope: run.Scope, Category: run.Category, GroupKey: run.GroupKey, SkuIDs: nonNil(run.SkuIDs),
		ImageIDs: nonNil(run.ImageIDs), CreditsQuoted: run.CreditsQuoted, Feedback: run.Feedback, Status: run.Status,
		Error: run.Error, CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt}
}

// questionDTO maps a question: the two option labels and the SKU ids of its codes.
func questionDTO(st *state, question model.Question) dto.Question {
	out := dto.Question{ID: question.ID, Check: question.Check, Text: question.Text, Options: []string{},
		Blocking: question.Blocking, Answer: question.Answer, AnsweredAt: question.AnsweredAt, SkuIDs: []string{},
		CreatedAt: question.CreatedAt}
	var options []struct {
		Label string `json:"label"`
	}
	_ = json.Unmarshal(question.Options, &options)
	for _, option := range options {
		out.Options = append(out.Options, option.Label)
	}
	for _, code := range question.SkuCodes {
		if sku := st.sku(code); sku != nil {
			out.SkuIDs = append(out.SkuIDs, sku.ID)
		}
	}
	return out
}

// detail maps the whole state (SessionDetail).
func (s *Service) detail(st *state) dto.SessionDetail {
	out := dto.SessionDetail{Session: s.sessionDTO(st), Skus: s.skusDTO(st, nil), Questions: []dto.Question{},
		Runs: []dto.Run{}, Images: s.imagesDTO(st, nil)}
	for _, question := range st.questions {
		out.Questions = append(out.Questions, questionDTO(st, question))
	}
	for _, run := range st.runs {
		out.Runs = append(out.Runs, runDTO(run))
	}
	return out
}

// nonNil turns a nil slice into an empty one, so it encodes as [].
func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
