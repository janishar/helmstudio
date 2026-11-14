// Package catalog: Catalog Agent — the session and its Input steps, processing (cmd.catalog.process), SKUs and
// questions, runs and review (cmd.catalog.run), typed requests (catalog.interpret), the event projector and stream, download.
package catalog

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"mime"
	"mime/multipart"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/catalog/dto"
	"github.com/helmstudio/studio_service/api/catalog/message"
	"github.com/helmstudio/studio_service/api/catalog/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
	"github.com/helmstudio/studio_service/utils"
)

// maxFile caps one uploaded file (raw image, document, photo).
const maxFile = 25 << 20

// Service owns the catalog_* tables. store holds the session's files, signer turns keys into links, agents answers the
// catalog.interpret and catalog.poses.suggest requests (nil: those routes answer 503), stop tells agents to stop a run
// or images in it.
type Service struct {
	db     *pgxpool.Pool
	store  storage.Store
	signer *common.Signer
	agents micro.NatsClient
	stop   Stopper
}

// NewService returns the service.
func NewService(db *pgxpool.Pool, store storage.Store, signer *common.Signer, agents micro.NatsClient, stop Stopper) *Service {
	return &Service{db: db, store: store, signer: signer, agents: agents, stop: stop}
}

// Stopper tells agents, on the replica running a run's job, to stop the whole job (Run: agents.cancel) or to skip
// chosen images in it (Images: agents.cancel.image; no ids sends nothing). Two functions, so skipping images can never
// stop a run by accident, and tests can record the calls.
type Stopper struct {
	Run    func(runID string)
	Images func(runID string, imageIDs []string)
}

// NatsStopper publishes over client (core NATS, fire and forget). A failed publish is only logged: the rows are changed
// already, and the job's next running status broadcasts again.
func NatsStopper(client micro.NatsClient) Stopper {
	publish := func(subject, body string) {
		if err := client.Conn().Publish(subject, []byte(body)); err != nil {
			log.Printf("could not broadcast %s %q: %v", subject, body, err)
		}
	}
	return Stopper{
		Run: func(runID string) { publish(message.SubjectCancel, runID) },
		Images: func(runID string, imageIDs []string) {
			for _, imageID := range imageIDs {
				publish(message.SubjectCancelImage, runID+" "+imageID)
			}
		},
	}
}

// tx runs fn in a transaction scoped to the caller as a plain user: an admin sees only their own sessions here too.
func (s *Service) tx(ctx context.Context, caller common.Caller, fn func(tx pgx.Tx) error) error {
	return common.WithUser(ctx, s.db, common.Caller{ID: caller.ID}, fn)
}

// withState loads the session (locked) and runs fn on it in one caller transaction.
func (s *Service) withState(ctx context.Context, caller common.Caller, sessionID string, fn func(tx pgx.Tx, st *state) error) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, true)
		if err != nil {
			return err
		}
		return fn(tx, st)
	})
}

// reload re-reads the state inside tx after a change.
func reload(ctx context.Context, tx pgx.Tx, st *state) (*state, error) {
	return loadState(ctx, tx, st.session.ID, false)
}

// sessionKey is the object key of a session file: u/<owner>/catalog/<session>/<folder>/<id>.<ext>.
func sessionKey(st *state, folder, id, ext string) string {
	return objectKey(st.session.OwnerID, st.session.ID, folder, id, ext)
}

// objectKey is sessionKey before the session is loaded.
func objectKey(owner, sessionID, folder, id, ext string) string {
	return common.OwnerPrefix(owner) + "catalog/" + sessionID + "/" + folder + "/" + id + "." + ext
}

// library returns the registry's library and prices, or the 503 the routes that need them answer.
func library() (*registry.Library, *registry.Prices, error) {
	snapshot := registry.Current()
	if !snapshot.Ready() {
		return nil, nil, network.NewServiceUnavailableError("the catalog library is not published yet; try again shortly", nil)
	}
	return snapshot.Library, snapshot.Prices, nil
}

// ------------------------------------------------------------------ sessions

// Create makes an empty session (stage setup, flow at raw).
func (s *Service) Create(ctx context.Context, caller common.Caller, body *dto.SessionCreate) (dto.SessionDetail, error) {
	name := "Untitled project"
	if body.ProjectName != nil {
		name = *body.ProjectName
	}
	var out dto.SessionDetail
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		id := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO catalog_sessions (id, owner_id, project_name, inputs) VALUES ($1, $2, $3, $4)`,
			id, caller.ID, name, model.EmptyInputs()); err != nil {
			return err
		}
		st, err := loadState(ctx, tx, id, false)
		if err == nil {
			out = s.detail(st)
		}
		return err
	})
	return out, err
}

// List returns the caller's sessions, newest first (at most 100), each with its SKU and image counts.
// ponytail: one state read per session (statuses are derived in Go); one grouped query if the dashboard gets slow.
func (s *Service) List(ctx context.Context, caller common.Caller) (dto.SessionList, error) {
	out := dto.SessionList{Sessions: []dto.SessionSummary{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT id::text FROM catalog_sessions ORDER BY created_at DESC LIMIT 100`)
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			st, err := loadState(ctx, tx, id, false)
			if err != nil {
				return err
			}
			out.Sessions = append(out.Sessions, summaryOf(st))
		}
		return nil
	})
	return out, err
}

// summaryOf is one dashboard row: the session and its counts.
func summaryOf(st *state) dto.SessionSummary {
	counts := dto.Counts{Skus: len(st.skus)}
	for _, sku := range st.skus {
		switch st.status[sku.ID] {
		case "Not run":
			counts.NotRun++
		case "Queued", "Running":
			counts.Running++
		case "To review", "Redo":
			counts.ToReview++
		case "Approved":
			counts.Approved++
		}
	}
	for _, image := range st.images {
		if image.Status == "approved" {
			counts.ImagesApproved++
		}
	}
	session := st.session
	return dto.SessionSummary{ID: session.ID, ProjectName: session.ProjectName, Stage: session.Stage, SkuCount: len(st.skus),
		Counts: counts, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt}
}

// Stats returns the caller's totals: sessions, SKUs, approved images and the credits quoted for every run.
func (s *Service) Stats(ctx context.Context, caller common.Caller) (dto.Stats, error) {
	var out dto.Stats
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*)::int FROM catalog_sessions), (SELECT count(*)::int FROM catalog_skus),
			(SELECT count(*)::int FROM catalog_images WHERE status = 'approved'),
			(SELECT coalesce(sum(credits_quoted), 0) FROM catalog_runs)`).Scan(&out.Sessions, &out.Skus, &out.ImagesApproved, &out.CreditsQuoted)
	})
	return out, err
}

// Metrics returns the caller's render quality per product (the SKU's category when the image rendered), from every
// image that finished rendering, optionally of one session. Tries per image count only images that rendered (one
// that failed on a missing input made none). Cost per approved image is each image's credits × renders over the
// approved count; a run's credits per image is its quote over its images.
func (s *Service) Metrics(ctx context.Context, caller common.Caller, sessionID *string) (dto.Metrics, error) {
	out := dto.Metrics{Products: []dto.ProductMetrics{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT coalesce(i.category, k.category), count(*)::int, avg(i.first_pass::int)::float8,
			(avg(i.attempts) FILTER (WHERE i.attempts > 0))::float8,
			count(*) FILTER (WHERE i.status = 'approved')::int, count(*) FILTER (WHERE i.status = 'rejected')::int,
			count(i.feedback)::int,
			(sum(r.credits_quoted / greatest(cardinality(r.image_ids), 1) * i.attempts) /
				nullif(count(*) FILTER (WHERE i.status = 'approved'), 0))::float8
			FROM catalog_images i JOIN catalog_skus k ON k.id = i.sku_id JOIN catalog_runs r ON r.id = i.run_id
			WHERE i.status <> 'generating' AND ($1::uuid IS NULL OR i.session_id = $1)
			GROUP BY 1 ORDER BY 1`, sessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m dto.ProductMetrics
			if err := rows.Scan(&m.Product, &m.Images, &m.FirstPassRate, &m.AvgTries, &m.Approved, &m.Rejected,
				&m.FeedbackCount, &m.CreditsPerApproved); err != nil {
				return err
			}
			out.Products = append(out.Products, m)
		}
		return rows.Err()
	})
	return out, err
}

// Get returns the session with its SKUs, questions, runs and images.
func (s *Service) Get(ctx context.Context, caller common.Caller, sessionID string) (dto.SessionDetail, error) {
	var out dto.SessionDetail
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, false)
		if err == nil {
			out = s.detail(st)
		}
		return err
	})
	return out, err
}

// Patch saves the fields sent. A step's items replace its list; raw items can only be removed; upload ids must be
// this session's; output, pose, model and background picks must be in the library. While Input is open, a change to
// another step re-runs the processing checks (recheck).
func (s *Service) Patch(ctx context.Context, caller common.Caller, sessionID string, body *dto.SessionPatch) (dto.Session, error) {
	var out dto.Session
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		session := st.session
		if body.ProjectName != nil {
			session.ProjectName = *body.ProjectName
		}
		if body.FlowPosition != nil {
			session.FlowPosition = *body.FlowPosition
		}
		if body.Messages != nil {
			raw, _ := json.Marshal(*body.Messages)
			session.Messages = raw
		}
		recheck := false
		for step, patch := range body.Inputs {
			input := session.Inputs[step]
			before, _ := json.Marshal(input)
			if patch.Items != nil {
				items := *patch.Items
				if step == "raw" {
					kept, err := keptRawItems(input.Items, items)
					if err != nil {
						return err
					}
					items = kept
				}
				items, err := s.adoptUploads(ctx, tx, st, items)
				if err != nil {
					return err
				}
				if err := checkChoices(st, step, items); err != nil {
					return err
				}
				input.Items = nonNil(items)
			}
			if patch.Skipped != nil {
				input.Skipped = *patch.Skipped
			}
			session.Inputs[step] = input
			if after, _ := json.Marshal(input); step != "raw" && !bytes.Equal(before, after) {
				recheck = true
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET project_name = $2, flow_position = $3, messages = $4, inputs = $5,
			updated_at = clock_timestamp() WHERE id = $1`, session.ID, session.ProjectName, session.FlowPosition,
			session.Messages, session.Inputs); err != nil {
			return err
		}
		if recheck && st.inSetup() {
			if err := recheckInputs(ctx, tx, st); err != nil {
				return err
			}
		}
		st, err := reload(ctx, tx, st)
		if err == nil {
			out = s.sessionDTO(st)
		}
		return err
	})
	return out, err
}

// checkChoices refuses (422) output, pose, model and background picks the library doesn't have: Start copies them
// into the briefs, where an unknown one would fail or misprice every run. Models and backgrounds may also be this
// session's uploads; their items' text (the brand's description) is trimmed in place and capped, and an item needs
// picks or a description.
func checkChoices(st *state, step string, items []model.Item) error {
	if step != "output" && step != "poses" && step != "models" && step != "backgrounds" {
		return nil
	}
	lib, _, err := library()
	if err != nil {
		return err
	}
	unknown := func(what, id string) error { return network.NewUnprocessableError("unknown "+what+" "+id, nil) }
	for i := range items {
		item := &items[i]
		if step == "models" || step == "backgrounds" { // the text is the brand's description of the model or background
			item.Text = strings.TrimSpace(item.Text)
			if utf8.RuneCountInString(item.Text) > 500 {
				return network.NewUnprocessableError(strings.TrimSuffix(step, "s")+" description: at most 500 characters", nil)
			}
			if len(item.IDs) == 0 && item.Text == "" {
				return network.NewUnprocessableError("pick a "+strings.TrimSuffix(step, "s")+" or describe one", nil)
			}
		}
		if item.Resolution != "" && !slices.ContainsFunc(lib.Resolutions, func(r registry.Resolution) bool { return r.ID == item.Resolution }) {
			return unknown("resolution", item.Resolution)
		}
		if item.Ratio != "" && !knownRatio(lib, item.Ratio) {
			return unknown("ratio", item.Ratio)
		}
		custom := map[string]string{} // this item's custom shots: id -> product
		for _, c := range item.CustomPoses {
			if lib.Category(c.Category) == nil {
				return unknown("category", c.Category)
			}
			if slices.ContainsFunc(lib.Poses, func(p registry.Pose) bool { return p.Category == c.Category && strings.EqualFold(p.Name, c.Name) }) {
				return network.NewUnprocessableError("custom shot "+c.Name+" is already a library pose", nil)
			}
			custom[c.ID] = c.Category
		}
		planned := map[string]bool{}
		for category, poses := range item.Poses {
			for _, pose := range poses {
				if lib.Pose(pose) == nil && custom[pose] != category {
					return unknown("pose", pose)
				}
				planned[pose] = true
			}
		}
		for id, pose := range item.PoseRefs { // a reference photo of this session for a shot of this plan
			if upload := st.upload(id); upload == nil || upload.Kind != "pose_ref" || !planned[pose] {
				return network.NewUnprocessableError("pose_refs: "+id+" is not a reference photo of a planned shot", nil)
			}
		}
		for _, id := range item.IDs {
			if st.upload(id) == nil && ((step == "models" && lib.Model(id) == nil) || (step == "backgrounds" && lib.Background(id) == nil)) {
				return unknown(strings.TrimSuffix(step, "s"), id)
			}
		}
	}
	return nil
}

// keptRawItems returns the stored raw items whose ids are in sent: raw items are only added by POST .../raw.
func keptRawItems(stored, sent []model.Item) ([]model.Item, error) {
	kept := []model.Item{}
	for _, item := range sent {
		i := slices.IndexFunc(stored, func(s model.Item) bool { return s.ID == item.ID })
		if i < 0 {
			return nil, network.NewUnprocessableError("raw items are added by POST .../raw; a PATCH can only remove them", nil)
		}
		kept = append(kept, stored[i])
	}
	return kept, nil
}

// adoptUploads checks the uploads the items name (upload_ids, and the UUIDs among a picker's ids): this session's
// stay; the caller's own from another session (an item reused from memory) are copied into this session under new ids,
// so the session owns every file it reads; any other id is a 422. ponytail: objects copied by a transaction that then
// fails stay until a Reset of the session.
func (s *Service) adoptUploads(ctx context.Context, tx pgx.Tx, st *state, items []model.Item) ([]model.Item, error) {
	out := slices.Clone(items)
	for i := range out {
		var err error
		old := out[i].UploadIDs
		if out[i].UploadIDs, err = s.adopt(ctx, tx, st, out[i].UploadIDs); err != nil {
			return nil, err
		}
		if len(out[i].PoseRefs) > 0 { // pose_refs name upload_ids: follow their copies
			refs := map[string]string{}
			for id, pose := range out[i].PoseRefs {
				if j := slices.Index(old, id); j >= 0 {
					id = out[i].UploadIDs[j]
				}
				refs[id] = pose
			}
			out[i].PoseRefs = refs
		}
		if out[i].IDs, err = s.adopt(ctx, tx, st, out[i].IDs); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// adopt returns ids with every upload of another session replaced by this session's copy; library ids pass.
func (s *Service) adopt(ctx context.Context, tx pgx.Tx, st *state, ids []string) ([]string, error) {
	ids = slices.Clone(ids)
	for j, id := range ids {
		if !utility.IsUUID(id) || st.upload(id) != nil {
			continue
		}
		found, err := collect[model.Upload](ctx, tx, `SELECT `+model.UploadCols+` FROM catalog_uploads WHERE id::text = $1`, id)
		if err != nil || len(found) == 0 {
			return nil, network.NewUnprocessableError("upload "+id+" is not one of your uploads", err)
		}
		upload := found[0]
		parts := strings.Split(upload.Key, "/") // u/<owner>/catalog/<session>/<folder>/<id>.<ext>
		upload.ID = utility.NewID()
		key := sessionKey(st, parts[len(parts)-2], upload.ID, strings.TrimPrefix(extOf(upload.Key), "."))
		if err := s.store.Copy(ctx, upload.Key, key); err != nil {
			return nil, err
		}
		upload.Key = key
		if err := insertUpload(ctx, tx, st, upload); err != nil {
			return nil, err
		}
		st.uploads = append(st.uploads, upload)
		ids[j] = upload.ID
	}
	return ids, nil
}

// Refresh clears Input for the next batch: SKUs, runs, images and memory stay; the flow goes back to raw.
func (s *Service) Refresh(ctx context.Context, caller common.Caller, sessionID string) (dto.SessionDetail, error) {
	var out dto.SessionDetail
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		if st.processing() || st.running() {
			return network.NewConflictError("wait for processing and runs to finish", nil)
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET inputs = $2, flow_position = 'raw', expected_skus = NULL,
			updated_at = clock_timestamp() WHERE id = $1`, st.session.ID, model.EmptyInputs()); err != nil {
			return err
		}
		st, err := reload(ctx, tx, st)
		if err == nil {
			out = s.detail(st)
		}
		return err
	})
	return out, err
}

// Reset starts over: inputs, messages, SKUs, questions, runs, images, events and the session's files go; memory stays,
// and so do the uploads its items name, so a reused item still has its file.
func (s *Service) Reset(ctx context.Context, caller common.Caller, sessionID string) (dto.SessionDetail, error) {
	var out dto.SessionDetail
	var prefix string
	var kept []string
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		if st.processing() || st.running() {
			return network.NewConflictError("wait for processing and runs to finish", nil)
		}
		prefix = common.OwnerPrefix(st.session.OwnerID) + "catalog/" + st.session.ID + "/"
		for _, table := range []string{"catalog_images", "catalog_runs", "catalog_questions", "catalog_skus", "catalog_processes", "catalog_events"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE session_id = $1`, st.session.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM catalog_uploads WHERE session_id = $1 AND id::text NOT IN (
			SELECT jsonb_array_elements_text(coalesce(item->'upload_ids', '[]') || coalesce(item->'ids', '[]'))
			FROM catalog_memory, jsonb_array_elements(items) item WHERE owner_id = $2)`, st.session.ID, st.session.OwnerID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(array_agg(key), '{}') FROM catalog_uploads WHERE session_id = $1`,
			st.session.ID).Scan(&kept); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET inputs = $2, messages = '[]', rules = NULL, expected_skus = NULL,
			stage = 'setup', flow_position = 'raw', updated_at = clock_timestamp() WHERE id = $1`,
			st.session.ID, model.EmptyInputs()); err != nil {
			return err
		}
		st, err := reload(ctx, tx, st)
		if err == nil {
			out = s.detail(st)
		}
		return err
	})
	if err != nil {
		return out, err
	}
	return out, s.deletePrefix(ctx, prefix, kept)
}

// deletePrefix deletes every object under prefix (a reset session's files) except the kept keys.
func (s *Service) deletePrefix(ctx context.Context, prefix string, kept []string) error {
	var keys []string
	for info, err := range s.store.List(ctx, prefix) {
		if err != nil {
			return err
		}
		if !slices.Contains(kept, info.Key) {
			keys = append(keys, info.Key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return s.store.Delete(ctx, keys...)
}

// ------------------------------------------------------------------ memory, library, favorites, estimate

// Memory returns the caller's last-used items per step.
func (s *Service) Memory(ctx context.Context, caller common.Caller) (dto.Memory, error) {
	out := dto.Memory{Memory: []dto.MemoryEntry{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		entries, err := collect[dto.MemoryEntry](ctx, tx, `SELECT step, items, project_name, updated_at FROM catalog_memory
			ORDER BY array_position(ARRAY['guidelines', 'output', 'poses', 'models', 'backgrounds', 'details'], step)`)
		out.Memory = append(out.Memory, entries...)
		return err
	})
	return out, err
}

// favoriteKinds are the kinds a favorite can be.
var favoriteKinds = []string{"models", "backgrounds", "poses"}

// favorites reads the caller's favorites inside tx.
func favorites(ctx context.Context, tx pgx.Tx) (dto.Favorites, error) {
	out := dto.Favorites{Models: []string{}, Backgrounds: []string{}, Poses: []string{}}
	rows, _ := tx.Query(ctx, `SELECT kind, item_id FROM catalog_favorites ORDER BY created_at, item_id`)
	var kind, id string
	_, err := pgx.ForEachRow(rows, []any{&kind, &id}, func() error {
		switch kind {
		case "models":
			out.Models = append(out.Models, id)
		case "backgrounds":
			out.Backgrounds = append(out.Backgrounds, id)
		case "poses":
			out.Poses = append(out.Poses, id)
		}
		return nil
	})
	return out, err
}

// Library returns the registry library with credits per resolution and the caller's favorites.
func (s *Service) Library(ctx context.Context, caller common.Caller) (dto.Library, error) {
	lib, prices, err := library()
	if err != nil {
		return dto.Library{}, err
	}
	out := dto.Library{Families: nonNil(lib.Families), Categories: nonNil(lib.Categories), Poses: nonNil(lib.Poses), Models: nonNil(lib.Models),
		Backgrounds: nonNil(lib.Backgrounds), Resolutions: []dto.ResolutionOption{}, Ratios: nonNil(lib.Ratios),
		Defaults: lib.Defaults, MaxAttempts: max(1, prices.MaxAttempts)}
	for _, resolution := range lib.Resolutions {
		out.Resolutions = append(out.Resolutions, dto.ResolutionOption{ID: resolution.ID, Label: resolution.Label,
			CreditsPerImage: prices.CreditsPerImage[resolution.ID]})
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		out.Favorites, err = favorites(ctx, tx)
		return err
	})
	return out, err
}

// SetFavorite adds (on) or removes a favorite; adding needs the item in the library.
func (s *Service) SetFavorite(ctx context.Context, caller common.Caller, kind, id string, on bool) (dto.Favorites, error) {
	if !slices.Contains(favoriteKinds, kind) {
		return dto.Favorites{}, errNotFound
	}
	if on {
		lib, _, err := library()
		if err != nil {
			return dto.Favorites{}, err
		}
		if (kind == "models" && lib.Model(id) == nil) || (kind == "backgrounds" && lib.Background(id) == nil) ||
			(kind == "poses" && lib.Pose(id) == nil) {
			return dto.Favorites{}, network.NewNotFoundError("no such "+strings.TrimSuffix(kind, "s")+" in the library", nil)
		}
	}
	var out dto.Favorites
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		query := `DELETE FROM catalog_favorites WHERE kind = $2 AND item_id = $3 AND owner_id = $1`
		if on {
			query = `INSERT INTO catalog_favorites (owner_id, kind, item_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`
		}
		if _, err := tx.Exec(ctx, query, caller.ID, kind, id); err != nil {
			return err
		}
		var err error
		out, err = favorites(ctx, tx)
		return err
	})
	return out, err
}

// creditsPerImage is the price of one image at resolution; ok false for a resolution the prices don't list.
func creditsPerImage(prices *registry.Prices, resolution string) (float64, bool) {
	credits, ok := prices.CreditsPerImage[resolution]
	return credits, ok
}

// Estimate quotes n images at a resolution. ponytail: a quote only; the credit ledger would charge here.
func (s *Service) Estimate(images int, resolution string) (dto.Estimate, error) {
	_, prices, err := library()
	if err != nil {
		return dto.Estimate{}, err
	}
	credits, ok := creditsPerImage(prices, resolution)
	if !ok {
		return dto.Estimate{}, network.NewUnprocessableError("unknown resolution "+resolution, nil)
	}
	return dto.Estimate{Images: images, Resolution: resolution, CreditsPerImage: credits, Credits: credits * float64(images)}, nil
}

// ------------------------------------------------------------------ inputs

// RawForm is the multipart form of POST /raw.
type RawForm struct {
	Kind     string // folder | files | csv
	Label    string
	Files    []*multipart.FileHeader
	Families []string // "What are we shooting?": family ids, one part each
	Paths    []string // each file's path inside the picked folder, in the order of Files (optional)
}

// stored is a document waiting to be put in the store with its row.
type stored struct {
	upload model.Upload
	data   []byte
}

// AddRawFiles stores uploaded raw images (or one CSV) and starts processing them. Each image is read (one at a time)
// and put in the store before the session is locked; addRaw deletes them again when it refuses.
func (s *Service) AddRawFiles(ctx context.Context, caller common.Caller, sessionID string, form RawForm) (dto.RawAccepted, error) {
	if form.Kind == "" {
		form.Kind = "files"
	}
	if form.Kind != "folder" && form.Kind != "files" && form.Kind != "csv" {
		return dto.RawAccepted{}, network.NewUnprocessableError("kind must be folder, files or csv", nil)
	}
	if len(form.Files) < 1 || len(form.Files) > 500 || utf8.RuneCountInString(form.Label) > 200 {
		return dto.RawAccepted{}, network.NewUnprocessableError("send 1 to 500 files (and a label of at most 200 characters)", nil)
	}
	if msg := dto.CheckFamilies(form.Families); msg != "" {
		return dto.RawAccepted{}, network.NewUnprocessableError(msg, nil)
	}
	if msg := checkPaths(form.Paths, len(form.Files)); msg != "" {
		return dto.RawAccepted{}, network.NewUnprocessableError(msg, nil)
	}
	if form.Kind == "csv" {
		if len(form.Files) != 1 {
			return dto.RawAccepted{}, network.NewUnprocessableError("a CSV upload is exactly one file", nil)
		}
		text, err := readSmall(form.Files[0], 1<<20)
		if err != nil {
			return dto.RawAccepted{}, err
		}
		label := form.Label
		if label == "" {
			label = form.Files[0].Filename
		}
		return s.addCsv(ctx, caller, sessionID, string(text), label, form.Families)
	}
	if !utility.IsUUID(sessionID) {
		return dto.RawAccepted{}, errNotFound
	}
	var files []model.Upload
	var names []string
	for i, header := range form.Files {
		name := header.Filename // multipart keeps only the base name: the folder path travels in paths
		if form.Paths != nil {
			name = form.Paths[i]
		}
		upload, err := s.putRaw(ctx, caller.ID, sessionID, header, name)
		if err != nil {
			for _, file := range files {
				_ = s.store.Delete(context.WithoutCancel(ctx), file.Key)
			}
			return dto.RawAccepted{}, err
		}
		files = append(files, upload)
		names = append(names, truncate(name, 512)) // the contract's cap per name
	}
	label := form.Label
	if label == "" {
		label = fmt.Sprintf("%d images", len(files))
	}
	count := len(files)
	item := model.Item{ID: utility.NewID(), Option: form.Kind, Label: label, ImageCount: &count}
	return s.addRaw(ctx, caller, sessionID, item, files, nil, form.Families, func(keys []string) message.Ingest {
		return message.Ingest{Kind: "files", Keys: keys, Names: names}
	})
}

// checkPaths is the 422 detail for raw upload paths, or "": none, or one relative path per file without "..".
func checkPaths(paths []string, files int) string {
	if paths == nil {
		return ""
	}
	if len(paths) != files {
		return "send one path per file, in the order of files"
	}
	for _, p := range paths {
		if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") ||
			slices.Contains(strings.Split(p, "/"), "..") {
			return "a path is relative, at most 512 characters and has no .. segment"
		}
	}
	return ""
}

// putRaw reads one raw image and puts it under the session's raw/ folder; the upload (named `name`, its path inside
// the picked folder when known) waits for its row.
func (s *Service) putRaw(ctx context.Context, owner, sessionID string, header *multipart.FileHeader, name string) (model.Upload, error) {
	data, ext, err := utils.ReadImage(header, header.Filename, maxFile)
	if err != nil {
		return model.Upload{}, network.NewUnprocessableError(err.Error(), nil)
	}
	id := utility.NewID()
	upload := model.Upload{ID: id, Kind: "raw", Name: truncate(name, 512), ContentType: utils.ContentType(ext),
		Size: int64(len(data)), Key: objectKey(owner, sessionID, "raw", id, ext)}
	return upload, s.store.Put(ctx, upload.Key, bytes.NewReader(data), upload.Size, upload.ContentType)
}

// AddRawLink adds a Drive folder, a website or CSV text and starts processing it.
func (s *Service) AddRawLink(ctx context.Context, caller common.Caller, sessionID string, body *dto.RawLink) (dto.RawAccepted, error) {
	if body.Kind == "csv" {
		return s.addCsv(ctx, caller, sessionID, body.CsvText, "CSV", body.Families)
	}
	label := "Google Drive folder"
	if body.Kind == "website" {
		parsed, _ := url.Parse(body.URL)
		label = parsed.Host + parsed.Path
	}
	item := model.Item{ID: utility.NewID(), Option: body.Kind, Label: truncate(label, 200), URL: body.URL}
	return s.addRaw(ctx, caller, sessionID, item, nil, nil, body.Families, func([]string) message.Ingest {
		return message.Ingest{Kind: body.Kind, URL: body.URL}
	})
}

// addCsv parses a CSV sheet and starts processing its rows; expected_skus is its distinct SKUs.
func (s *Service) addCsv(ctx context.Context, caller common.Caller, sessionID, text, label string, families []string) (dto.RawAccepted, error) {
	rows, expected, err := ParseCsv(text)
	if err != nil {
		return dto.RawAccepted{}, err
	}
	count := len(rows)
	item := model.Item{ID: utility.NewID(), Option: "csv", Label: truncate(label, 200), ImageCount: &count}
	return s.addRaw(ctx, caller, sessionID, item, nil, &expected, families, func([]string) message.Ingest {
		return message.Ingest{Kind: "csv", Rows: rows}
	})
}

// addRaw records the stored files, adds the raw item and enqueues cmd.catalog.process with the new source and the
// families chosen, in one transaction. 422 for a family the library doesn't have; 409 while processing runs or once
// Processing is open (Refresh first). Files of a refused or failed call are deleted again.
func (s *Service) addRaw(ctx context.Context, caller common.Caller, sessionID string, item model.Item, files []model.Upload,
	expected *int, families []string, ingest func(keys []string) message.Ingest) (dto.RawAccepted, error) {
	var out dto.RawAccepted
	var keys []string
	for _, file := range files {
		keys = append(keys, file.Key)
	}
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		if err := checkFamilies(families); err != nil {
			return err
		}
		if st.processing() {
			return network.NewConflictError("the previous raw images are still being processed", nil)
		}
		if !st.inSetup() {
			return network.NewConflictError("this batch has started; use Refresh for the next one", nil)
		}
		for _, upload := range files {
			item.UploadIDs = append(item.UploadIDs, upload.ID)
			if err := insertUpload(ctx, tx, st, upload); err != nil {
				return err
			}
		}
		input := st.session.Inputs["raw"]
		input.Items, input.Skipped = append(input.Items, item), false
		st.session.Inputs["raw"] = input
		if expected != nil {
			st.session.ExpectedSkus = expected
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET inputs = $2, expected_skus = $3, updated_at = clock_timestamp()
		WHERE id = $1`, st.session.ID, st.session.Inputs, st.session.ExpectedSkus); err != nil {
			return err
		}
		processID, err := enqueueProcess(ctx, tx, st, []message.Ingest{ingest(keys)}, families)
		if err != nil {
			return err
		}
		if st, err = reload(ctx, tx, st); err != nil {
			return err
		}
		out = dto.RawAccepted{Session: s.sessionDTO(st), Item: item, ImageCount: item.ImageCount, ExpectedSkus: expected,
			ProcessID: processID}
		return nil
	})
	if err != nil && len(keys) > 0 {
		_ = s.store.Delete(context.WithoutCancel(ctx), keys...)
	}
	return out, err
}

// checkFamilies refuses (422) a family id the library doesn't have.
func checkFamilies(families []string) error {
	if len(families) == 0 {
		return nil
	}
	lib, _, err := library()
	if err != nil {
		return err
	}
	for _, id := range families {
		if lib.Family(id) == nil {
			return network.NewUnprocessableError("unknown family "+id, nil)
		}
	}
	return nil
}

// insertUpload records a stored file.
func insertUpload(ctx context.Context, tx pgx.Tx, st *state, upload model.Upload) error {
	_, err := tx.Exec(ctx, `INSERT INTO catalog_uploads (id, owner_id, session_id, kind, name, content_type, size, category, key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, upload.ID, st.session.OwnerID, st.session.ID, upload.Kind,
		truncate(upload.Name, 200), upload.ContentType, upload.Size, upload.Category, upload.Key)
	return err
}

// readSmall reads one uploaded file of at most limit bytes.
func readSmall(header *multipart.FileHeader, limit int64) ([]byte, error) {
	if header.Size > limit {
		return nil, network.NewRequestTooLargeError(fmt.Sprintf("%s is larger than %d bytes", header.Filename, limit), nil)
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = network.NewRequestTooLargeError(fmt.Sprintf("%s is larger than %d bytes", header.Filename, limit), nil)
	}
	return data, err
}

// ParseCsv reads a SKU sheet: a header row with SKU, name, category and image_url in any order (case-insensitive),
// comma- or tab-separated. Rows with an http(s) image_url are returned; expected counts the distinct SKUs of every row.
func ParseCsv(text string) ([]message.CsvRow, int, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord, reader.LazyQuotes, reader.TrimLeadingSpace = -1, true, true
	if first, _, _ := strings.Cut(text, "\n"); strings.Count(first, "\t") > strings.Count(first, ",") {
		reader.Comma = '\t'
	}
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, 0, network.NewUnprocessableError("the CSV could not be read", nil)
	}
	column := map[string]int{}
	for i, name := range records[0] {
		column[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, name := range []string{"sku", "name", "category", "image_url"} {
		if _, ok := column[name]; !ok {
			return nil, 0, network.NewUnprocessableError("the CSV needs the columns SKU, name, category, image_url", nil)
		}
	}
	field := func(record []string, name string) string {
		if i := column[name]; i < len(record) {
			return strings.TrimSpace(record[i])
		}
		return ""
	}
	rows, seen := []message.CsvRow{}, map[string]bool{}
	for _, record := range records[1:] {
		sku := field(record, "sku")
		if sku == "" {
			continue
		}
		seen[sku] = true
		if image := field(record, "image_url"); dto.IsLink(image) {
			rows = append(rows, message.CsvRow{Sku: sku, Name: field(record, "name"), Category: field(record, "category"), ImageURL: image})
		}
	}
	if len(seen) == 0 {
		return nil, 0, network.NewUnprocessableError("the CSV has no SKU rows", nil)
	}
	return rows, len(seen), nil
}

// readableDocs are the document types processing uses: text it reads, and images it keeps as metal finish or
// placement references (rose-gold.jpg, how-its-worn.png). Others are stored and flagged.
var readableDocs = []string{"pdf", "txt", "csv", "tsv", "md", "json", "html", "htm", "docx", "jpg", "jpeg", "png", "webp"}

// AddGuidelineFile stores a guideline (or project brief, target details) document and adds its item.
func (s *Service) AddGuidelineFile(ctx context.Context, caller common.Caller, sessionID, target string,
	header *multipart.FileHeader) (dto.InputAdded, error) {
	if target == "" {
		target = "guidelines"
	}
	if target != "guidelines" && target != "details" {
		return dto.InputAdded{}, network.NewUnprocessableError("target must be guidelines or details", nil)
	}
	data, err := readSmall(header, maxFile)
	if err != nil {
		return dto.InputAdded{}, err
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(header.Filename), "."))
	if ext == "" || len(ext) > 10 {
		return dto.InputAdded{}, network.NewUnprocessableError("the document needs a file extension (pdf, docx, txt, …)", nil)
	}
	contentType := mime.TypeByExtension("." + ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	kind, option := "guideline", "doc"
	if target == "details" {
		kind, option = "brief", "brief"
	}
	upload := model.Upload{ID: utility.NewID(), Kind: kind, Name: header.Filename, ContentType: contentType, Size: int64(len(data))}
	item := model.Item{ID: utility.NewID(), Option: option, Label: truncate(header.Filename, 200), UploadIDs: []string{upload.ID}}
	if !slices.Contains(readableDocs, ext) {
		detail := "Stored — I can't read ." + ext + " files yet."
		item.Detail = &detail
	}
	return s.addDocument(ctx, caller, sessionID, target, item, &stored{upload, data})
}

// AddGuidelineLink adds a guideline (or project brief) link.
func (s *Service) AddGuidelineLink(ctx context.Context, caller common.Caller, sessionID string, body *dto.GuidelineLink) (dto.InputAdded, error) {
	option := "link"
	if body.Target == "details" {
		option = "brief-link"
	}
	item := model.Item{ID: utility.NewID(), Option: option, Label: truncate(body.URL, 200), URL: body.URL}
	return s.addDocument(ctx, caller, sessionID, body.Target, item, nil)
}

// addDocument stores the file (if any), appends the item to the step and, while Input is open, re-runs the processing
// checks so the new document is read (recheck).
func (s *Service) addDocument(ctx context.Context, caller common.Caller, sessionID, step string, item model.Item,
	doc *stored) (dto.InputAdded, error) {
	var out dto.InputAdded
	err := s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		if doc != nil {
			doc.upload.Key = sessionKey(st, "guidelines", doc.upload.ID, strings.TrimPrefix(path.Ext(doc.upload.Name), "."))
			if err := s.store.Put(ctx, doc.upload.Key, bytes.NewReader(doc.data), doc.upload.Size, doc.upload.ContentType); err != nil {
				return err
			}
			if err := insertUpload(ctx, tx, st, doc.upload); err != nil {
				return err
			}
		}
		input := st.session.Inputs[step]
		input.Items, input.Skipped = append(input.Items, item), false
		st.session.Inputs[step] = input
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET inputs = $2, updated_at = clock_timestamp() WHERE id = $1`,
			st.session.ID, st.session.Inputs); err != nil {
			return err
		}
		var err error
		if st, err = reload(ctx, tx, st); err != nil {
			return err
		}
		if st.inSetup() {
			if err := recheckInputs(ctx, tx, st); err != nil {
				return err
			}
			if st, err = reload(ctx, tx, st); err != nil {
				return err
			}
		}
		out = dto.InputAdded{Session: s.sessionDTO(st), Item: item}
		return nil
	})
	return out, err
}

// Upload stores the brand's own model, background or pose reference photo.
func (s *Service) Upload(ctx context.Context, caller common.Caller, sessionID, kind, name, category string,
	header *multipart.FileHeader) (dto.Upload, error) {
	if kind != "model" && kind != "background" && kind != "pose_ref" {
		return dto.Upload{}, network.NewUnprocessableError("kind must be model, background or pose_ref", nil)
	}
	if utf8.RuneCountInString(name) > 200 || (category != "" && !dto.LibraryID.MatchString(category)) {
		return dto.Upload{}, network.NewUnprocessableError("name is at most 200 characters, category a category id", nil)
	}
	data, ext, err := utils.ReadImage(header, header.Filename, maxFile)
	if err != nil {
		return dto.Upload{}, network.NewUnprocessableError(err.Error(), nil)
	}
	if name == "" {
		name = header.Filename
	}
	upload := model.Upload{ID: utility.NewID(), Kind: kind, Name: name, ContentType: utils.ContentType(ext), Size: int64(len(data))}
	if kind == "pose_ref" && category != "" {
		upload.Category = &category
	}
	err = s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		upload.Key = sessionKey(st, "uploads", upload.ID, ext)
		if err := s.store.Put(ctx, upload.Key, bytes.NewReader(data), upload.Size, upload.ContentType); err != nil {
			return err
		}
		return insertUpload(ctx, tx, st, upload)
	})
	return dto.Upload{ID: upload.ID, Kind: upload.Kind, Name: truncate(upload.Name, 200), ContentType: upload.ContentType,
		Size: upload.Size, Category: upload.Category, URL: s.signer.Sign(upload.Key)}, err
}

// SuggestPoses asks agents (catalog.poses.suggest) for poses from the briefs; nothing is saved.
func (s *Service) SuggestPoses(ctx context.Context, caller common.Caller, sessionID string, body *dto.PoseSuggestRequest) (dto.PoseSuggestion, error) {
	request := &message.PosesSuggestRequest{OwnerID: caller.ID, SessionID: sessionID}
	uploadOf := map[string]string{} // reference object key -> upload id
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, false)
		if err != nil {
			return err
		}
		for _, brief := range body.Briefs {
			keys := []string{}
			for _, id := range brief.UploadIDs {
				upload := st.upload(id)
				if upload == nil || upload.Kind != "pose_ref" {
					return network.NewUnprocessableError("upload "+id+" is not one of this session's pose reference photos", nil)
				}
				keys = append(keys, upload.Key)
				uploadOf[upload.Key] = id
			}
			request.Briefs = append(request.Briefs, message.PoseBrief{Category: brief.Category, Text: brief.Text,
				ReferenceKeys: keys, Exact: brief.Exact})
		}
		return nil
	})
	if err != nil {
		return dto.PoseSuggestion{}, err
	}
	reply, err := askAgents[message.PosesSuggestRequest, message.PosesSuggestReply](ctx, s.agents, message.SubjectPosesSuggest, request)
	if err != nil {
		return dto.PoseSuggestion{}, err
	}
	if reply.Error != "" {
		return dto.PoseSuggestion{}, network.NewServiceUnavailableError("agents could not suggest poses; try again", errors.New(reply.Error))
	}
	out := dto.PoseSuggestion{Poses: reply.Poses, CustomPoses: []model.CustomPose{}, Refs: map[string]string{}, Reply: reply.Reply}
	if out.Poses == nil {
		out.Poses = map[string][]string{}
	}
	for _, c := range reply.CustomPoses {
		out.CustomPoses = append(out.CustomPoses, model.CustomPose(c))
	}
	for key, pose := range reply.Refs {
		if id, ok := uploadOf[key]; ok {
			out.Refs[id] = pose
		}
	}
	return out, nil
}

// askAgents sends a request to agents_service; no client, a timeout or a failed request is a retryable 503.
func askAgents[S, R any](ctx context.Context, agents micro.NatsClient, subject string, request *S) (*R, error) {
	if agents == nil {
		return nil, network.NewServiceUnavailableError("agents unavailable; try again", nil)
	}
	reply, err := micro.RequestNats[S, R](ctx, agents, subject, request)
	if err != nil {
		return nil, network.NewServiceUnavailableError("agents did not answer in time; try again", err)
	}
	return reply, nil
}

// truncate cuts text to at most n bytes (on a rune boundary).
func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// utf8RuneStart reports whether b starts a UTF-8 sequence.
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ------------------------------------------------------------------ processing

// recheckInputs re-runs the processing checks on the session's SKUs after an input changed: at once, or, while a job
// runs, once it ends (the projector queues it then: afterProcess), so a session's jobs never overlap. Nothing to check
// before the first SKUs.
func recheckInputs(ctx context.Context, tx pgx.Tx, st *state) error {
	if st.processing() {
		_, err := tx.Exec(ctx, `UPDATE catalog_sessions SET recheck = true WHERE id = $1`, st.session.ID)
		return err
	}
	if len(st.skus) == 0 {
		return nil
	}
	_, err := enqueueProcess(ctx, tx, st, []message.Ingest{}, nil)
	return err
}

// enqueueProcess records a new processing job and queues cmd.catalog.process for it, built from the session's inputs and
// SKUs (those of started batches first, counted in started_skus); ingest empty re-reads the documents and re-runs the
// checks only. families limits the products new SKUs are sorted into (none: every product). Callers make sure no other
// job of the session is queued or running.
func enqueueProcess(ctx context.Context, tx pgx.Tx, st *state, ingest []message.Ingest, families []string) (string, error) {
	id := utility.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_processes (id, owner_id, session_id) VALUES ($1, $2, $3)`,
		id, st.session.OwnerID, st.session.ID); err != nil {
		return "", err
	}
	inputs := st.session.Inputs
	data := message.Process{SessionID: st.session.ID, Ingest: ingest, Guidelines: docRefs(st, inputs["guidelines"].Items),
		Details: message.Details{Text: detailsText(inputs), Docs: docRefs(st, inputs["details"].Items)},
		Choices: choices(inputs), Skus: []message.SkuRef{}, ExpectedSkus: st.session.ExpectedSkus, Families: families}
	for _, started := range []bool{true, false} {
		for _, sku := range st.skus {
			if resolved(sku) == started {
				data.Skus = append(data.Skus, skuRef(sku))
				if started {
					data.StartedSkus++
				}
			}
		}
	}
	return id, messaging.Enqueue(ctx, tx, messaging.Command{JobID: id, OwnerID: st.session.OwnerID, Data: data})
}

// docRefs turns guideline or brief items into documents: stored files by key, links by URL (notes have neither).
func docRefs(st *state, items []model.Item) []message.DocRef {
	docs := []message.DocRef{}
	for _, item := range items {
		for _, id := range item.UploadIDs {
			if upload := st.upload(id); upload != nil {
				docs = append(docs, message.DocRef{Name: upload.Name, Key: upload.Key})
			}
		}
		if item.URL != "" {
			docs = append(docs, message.DocRef{Name: item.Label, URL: item.URL})
		}
	}
	return docs
}

// detailsText joins the additional details notes.
func detailsText(inputs model.Inputs) string {
	var notes []string
	for _, item := range inputs["details"].Items {
		if text := strings.TrimSpace(item.Text); text != "" {
			notes = append(notes, text)
		}
	}
	return truncate(strings.Join(notes, "\n\n"), 20000)
}

// choices reads the output, pose, model and background inputs: the last output item, poses merged by category (a
// later item wins), the models and backgrounds of every item.
func choices(inputs model.Inputs) message.Choices {
	out := message.Choices{Poses: map[string][]string{}, Models: []string{}, Backgrounds: []string{}}
	for _, item := range inputs["output"].Items {
		if item.Resolution != "" {
			out.Resolution = &item.Resolution
		}
		if item.Ratio != "" {
			out.Ratio = &item.Ratio
		}
	}
	for _, item := range inputs["poses"].Items {
		for category, poses := range item.Poses {
			out.Poses[category] = poses
		}
	}
	for _, item := range inputs["models"].Items {
		out.Models = appendNew(out.Models, item.IDs...)
	}
	for _, item := range inputs["backgrounds"].Items {
		out.Backgrounds = appendNew(out.Backgrounds, item.IDs...)
	}
	return out
}

// randomized reports whether any item of a models or backgrounds step asks to spread its picks.
func randomized(input model.StepInput) bool {
	return slices.ContainsFunc(input.Items, func(item model.Item) bool { return item.Randomize })
}

// appendNew appends the values list doesn't hold yet.
func appendNew(list []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(list, value) {
			list = append(list, value)
		}
	}
	return list
}

// skuRef is a SKU as agents receive it.
func skuRef(sku model.Sku) message.SkuRef {
	return message.SkuRef{SkuID: sku.ID, Code: sku.Code, Name: sku.Name, Category: sku.Category,
		Subcategory: sku.Subcategory, Attributes: sku.Attributes, RawKeys: nonNil(sku.RawKeys)}
}

// ------------------------------------------------------------------ questions

// Answer applies the chosen option's SKU changes and records the answer; 409 when already answered.
func (s *Service) Answer(ctx context.Context, caller common.Caller, questionID string, option int) (dto.Answered, error) {
	if !utility.IsUUID(questionID) {
		return dto.Answered{}, errNotFound
	}
	var out dto.Answered
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var sessionID string
		err := tx.QueryRow(ctx, `SELECT session_id::text FROM catalog_questions WHERE id = $1`, questionID).Scan(&sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		st, err := loadState(ctx, tx, sessionID, true)
		if err != nil {
			return err
		}
		question := st.questions[slices.IndexFunc(st.questions, func(q model.Question) bool { return q.ID == questionID })]
		if question.Answer != nil {
			return network.NewConflictError("this question is answered already", nil)
		}
		var options []message.QuestionOption
		if err := json.Unmarshal(question.Options, &options); err != nil || option >= len(options) {
			return network.NewUnprocessableError("no such option", err)
		}
		changed, err := applyOption(ctx, tx, st, options[option].Apply)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_questions SET answer = $2, answered_at = clock_timestamp() WHERE id = $1`,
			questionID, option); err != nil {
			return err
		}
		if st, err = reload(ctx, tx, st); err != nil {
			return err
		}
		i := slices.IndexFunc(st.questions, func(q model.Question) bool { return q.ID == questionID })
		out = dto.Answered{Question: questionDTO(st, st.questions[i]), Skus: s.skusDTO(st, nonNil(changed))}
		return nil
	})
	return out, err
}

// applyOption makes an option's SKU changes and returns the ids of the SKUs that still exist and changed. A code that
// is no longer there (merged, reset) is skipped, and so is (logged) a product or subcategory the library does not have.
func applyOption(ctx context.Context, tx pgx.Tx, st *state, changes []message.Apply) ([]string, error) {
	var changed []string
	for _, change := range changes {
		sku := st.sku(change.SkuCode)
		if sku == nil {
			continue
		}
		if change.MergeInto != "" {
			target := st.sku(change.MergeInto)
			if target == nil || target.ID == sku.ID {
				continue
			}
			target.RawKeys = appendNew(target.RawKeys, sku.RawKeys...)
			if _, err := tx.Exec(ctx, `UPDATE catalog_skus SET raw_keys = $2, updated_at = clock_timestamp() WHERE id = $1`,
				target.ID, target.RawKeys); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM catalog_skus WHERE id = $1`, sku.ID); err != nil {
				return nil, err
			}
			changed = appendNew(slices.DeleteFunc(changed, func(id string) bool { return id == sku.ID }), target.ID)
			sku.Code = "" // gone: later changes to its code are skipped
			continue
		}
		if change.Category != "" || len(change.Subcategory) > 0 {
			if err := moveByAnswer(st, sku, change); err != nil {
				return nil, err
			}
		}
		if change.Brief != nil {
			setOverrides(sku, *change.Brief)
		}
		if len(change.Attributes) > 0 && string(change.Attributes) != "null" {
			merged, err := mergeObjects(sku.Attributes, change.Attributes)
			if err != nil {
				return nil, err
			}
			sku.Attributes = merged
		}
		if change.Confirmed != nil {
			sku.Confirmed = *change.Confirmed
		}
		if err := saveSku(ctx, tx, sku); err != nil {
			return nil, err
		}
		changed = appendNew(changed, sku.ID)
	}
	return changed, nil
}

// moveByAnswer moves sku to change's product as PATCH /skus/{id} does, then sets its subcategory (null clears it);
// either is skipped and logged when the library does not know it (agents' message, not the brand's input).
func moveByAnswer(st *state, sku *model.Sku, change message.Apply) error {
	lib, _, err := library()
	if err != nil {
		return err
	}
	if change.Category != "" && change.Category != sku.Category { // the same product keeps poses and subcategory
		if to := lib.Category(change.Category); to != nil {
			moveSku(st, lib, sku, to)
		} else {
			log.Printf("answer: %s: unknown category %s skipped", sku.Code, change.Category)
		}
	}
	if len(change.Subcategory) > 0 {
		var sub *string
		if json.Unmarshal(change.Subcategory, &sub) != nil || (sub != nil && !hasSubcategory(lib.Category(sku.Category), *sub)) {
			log.Printf("answer: %s: subcategory %s of %s skipped", sku.Code, change.Subcategory, sku.Category)
		} else {
			sku.Subcategory = sub
		}
	}
	return nil
}

// setOverrides records brief fields on a SKU: they win over inputs and defaults when Start resolves the brief.
func setOverrides(sku *model.Sku, patch message.BriefPatch) {
	if patch.Model != "" {
		sku.Overrides.Model, sku.Brief.Model = patch.Model, &patch.Model
	}
	if patch.Background != "" {
		sku.Overrides.Background, sku.Brief.Background = patch.Background, &patch.Background
	}
	if len(patch.Poses) > 0 {
		sku.Overrides.Poses, sku.Brief.Poses = patch.Poses, patch.Poses
	}
	if patch.ModelDescription != nil {
		sku.Overrides.ModelDescription, sku.Brief.ModelDescription = patch.ModelDescription, nonBlank(*patch.ModelDescription)
	}
	if patch.BackgroundDescription != nil {
		sku.Overrides.BackgroundDescription, sku.Brief.BackgroundDescription = patch.BackgroundDescription,
			nonBlank(*patch.BackgroundDescription)
	}
}

// fillDefaults gives a started SKU left with neither a model (on a product that uses one) nor a background, picked or
// described, its product's default: a cleared description must not leave it with nothing to render (every run a 409).
func fillDefaults(lib *registry.Library, sku *model.Sku) {
	cat := lib.Category(sku.Category)
	if cat == nil || !resolved(*sku) {
		return
	}
	if cat.UsesModel && sku.Brief.Model == nil && sku.Brief.ModelDescription == nil {
		sku.Brief.Model = nonBlank(cat.DefaultModelFor(wearerOf(*sku)))
	}
	if sku.Brief.Background == nil && sku.Brief.BackgroundDescription == nil {
		sku.Brief.Background = nonBlank(cat.DefaultBackground)
	}
}

// wearerOf is whose wear a SKU is (attributes.wearer: "women", "men"; "" when unknown): it picks the SKU's models.
func wearerOf(sku model.Sku) string {
	var attributes struct {
		Wearer *string `json:"wearer"`
	}
	_ = json.Unmarshal(sku.Attributes, &attributes) // agents validated the attributes
	return deref(attributes.Wearer)
}

// setWearer sets a SKU's attributes.wearer (nil clears it); a built-in model of the other wearer on its brief is
// replaced by the first of the session's `picks` that suits the new wearer, else dropped, so fillDefaults (or Start)
// gives it its product's default for that wearer.
func setWearer(lib *registry.Library, sku *model.Sku, wearer *string, picks []string) error {
	patch, _ := json.Marshal(map[string]*string{"wearer": wearer})
	merged, err := mergeObjects(sku.Attributes, patch)
	if err != nil {
		return err
	}
	sku.Attributes = merged
	if m := sku.Brief.Model; m != nil && !lib.SuitsWearer(*m, wearerOf(*sku)) {
		sku.Brief.Model, sku.Overrides.Model = nil, ""
		if fit := filter(picks, func(id string) bool { return lib.SuitsWearer(id, wearerOf(*sku)) }); len(fit) > 0 {
			sku.Brief.Model = &fit[0]
		}
	}
	return nil
}

// womanWords and manWords tell whose look a model description describes ("a woman in her 20s", "a South Indian man").
var (
	womanWords = regexp.MustCompile(`(?i)\b(?:woman|women|female|lady|ladies|girl)\b`)
	manWords   = regexp.MustCompile(`(?i)\b(?:man|men|male|gentleman|boy)\b`)
)

// textWearer is whose look a model description describes: "women" or "men" when it names only one, else "".
func textWearer(text string) string {
	woman, man := womanWords.MatchString(text), manWords.MatchString(text)
	switch {
	case woman && !man:
		return "women"
	case man && !woman:
		return "men"
	}
	return ""
}

// nonBlank points at s, or is nil for "" (a description that says nothing is none).
func nonBlank(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// customPoses are the session's custom shots (the poses step items'), by id.
func customPoses(inputs model.Inputs) map[string]model.CustomPose {
	out := map[string]model.CustomPose{}
	for _, item := range inputs["poses"].Items {
		for _, c := range item.CustomPoses {
			out[c.ID] = c
		}
	}
	return out
}

// knownCustomPoses are every custom shot the session knows: the ones its SKUs keep, then the poses step's (newer).
func knownCustomPoses(st *state) map[string]model.CustomPose {
	out := map[string]model.CustomPose{}
	for _, sku := range st.skus {
		for _, c := range sku.Brief.CustomPoses {
			out[c.ID] = c
		}
	}
	maps.Copy(out, customPoses(st.session.Inputs))
	return out
}

// findPose is a pose's name and product: a library pose, or a custom shot the session knows; ok false when neither.
func findPose(st *state, lib *registry.Library, id string) (name, category string, ok bool) {
	if pose := lib.Pose(id); pose != nil {
		return pose.Name, pose.Category, true
	}
	c, ok := knownCustomPoses(st)[id]
	return c.Name, c.Category, ok
}

// keepCustomPoses copies onto a SKU's brief the definitions of the custom shots among its poses (the poses step's,
// newest), so a later poses change cannot leave its shots or their regenerations without a prompt.
// ponytail: a definition is never dropped (a few hundred bytes each); prune if briefs grow.
func keepCustomPoses(st *state, sku *model.Sku) {
	session := customPoses(st.session.Inputs)
	for _, pose := range sku.Brief.Poses {
		c, ok := session[pose]
		if !ok {
			continue
		}
		if i := slices.IndexFunc(sku.Brief.CustomPoses, func(x model.CustomPose) bool { return x.ID == pose }); i >= 0 {
			sku.Brief.CustomPoses[i] = c
		} else {
			sku.Brief.CustomPoses = append(sku.Brief.CustomPoses, c)
		}
	}
}

// maxPoseRefs is how many reference photos one shot gets (cmd.catalog.run references maxItems).
const maxPoseRefs = 4

// poseRefs maps a pose id to the object keys of the session's reference photos for it (the poses items' pose_refs),
// sorted, at most maxPoseRefs per pose.
func poseRefs(st *state) map[string][]string {
	out := map[string][]string{}
	for _, item := range st.session.Inputs["poses"].Items {
		for id, pose := range item.PoseRefs {
			if upload := st.upload(id); upload != nil {
				out[pose] = append(out[pose], upload.Key)
			}
		}
	}
	for pose, keys := range out {
		slices.Sort(keys)
		out[pose] = keys[:min(len(keys), maxPoseRefs)]
	}
	return out
}

// linkRE finds the links in a brief: they were read for picking poses, never sent to the image model.
var linkRE = regexp.MustCompile(`https?://\S+`)

// shotsBrief is the poses step's brief for a product (from the item that gives its poses, so a later library-only
// choice clears it): links removed, one line, at most 1000 bytes; nil for none.
func shotsBrief(inputs model.Inputs, category string) *string {
	brief := ""
	for _, item := range inputs["poses"].Items {
		if _, ok := item.Poses[category]; ok {
			brief = item.Briefs[category]
		}
	}
	return nonBlank(truncate(strings.Join(strings.Fields(linkRE.ReplaceAllString(brief, "")), " "), 1000))
}

// stepText is a models or backgrounds step's description: the last item's text that says something, "" for none.
func stepText(input model.StepInput) string {
	text := ""
	for _, item := range input.Items {
		if item.Text != "" {
			text = item.Text
		}
	}
	return text
}

// saveSku writes a SKU's editable fields.
func saveSku(ctx context.Context, tx pgx.Tx, sku *model.Sku) error {
	_, err := tx.Exec(ctx, `UPDATE catalog_skus SET attributes = $2, confirmed = $3, brief = $4, overrides = $5,
		category = $6, subcategory = $7, group_key = $8, group_label = $9, variant_of = $10::uuid,
		updated_at = clock_timestamp() WHERE id = $1`, sku.ID, sku.Attributes, sku.Confirmed, sku.Brief, sku.Overrides,
		sku.Category, sku.Subcategory, sku.GroupKey, sku.GroupLabel, sku.VariantOf)
	return err
}

// commonAttributes keeps only the attribute fields every family has (description, source, setting) and the wearer.
func commonAttributes(attributes json.RawMessage) json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(attributes, &all) != nil {
		return attributes
	}
	kept := map[string]json.RawMessage{}
	for _, key := range []string{"description", "source", "setting", "wearer"} {
		if value, ok := all[key]; ok {
			kept[key] = value
		}
	}
	out, _ := json.Marshal(kept)
	return out
}

// mergeObjects returns base with patch's top-level fields replacing its own.
func mergeObjects(base, patch json.RawMessage) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &merged); err != nil {
			return nil, err
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(patch, &fields); err != nil {
		return nil, err
	}
	for key, value := range fields {
		merged[key] = value
	}
	return json.Marshal(merged)
}

// ------------------------------------------------------------------ start

// Start resolves the brief of every SKU of this batch (Not run, brief not resolved yet: an earlier batch keeps its
// setup) from the inputs, else the defaults, saves the inputs as the caller's
// memory and opens Processing (stage ready, flow done). 409 with no SKUs, while processing runs or while a blocking
// question is open.
func (s *Service) Start(ctx context.Context, caller common.Caller, sessionID string) (dto.Started, error) {
	lib, _, err := library()
	if err != nil {
		return dto.Started{}, err
	}
	var out dto.Started
	err = s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		switch {
		case len(st.skus) == 0:
			return network.NewConflictError("no SKUs yet: add raw images and wait for processing", nil)
		case st.processing():
			return network.NewConflictError("processing is still running", nil)
		case slices.ContainsFunc(st.questions, func(q model.Question) bool { return q.Blocking && q.Answer == nil }):
			return network.NewConflictError("answer the open questions first", nil)
		}
		for i, sku := range resolveBriefs(st, lib) {
			if err := saveSku(ctx, tx, &sku); err != nil {
				return err
			}
			st.skus[i] = sku
		}
		for _, step := range model.Steps[1:] {
			input := st.session.Inputs[step]
			if input.Skipped || len(input.Items) == 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO catalog_memory (owner_id, step, items, project_name) VALUES ($1, $2, $3, $4)
				ON CONFLICT (owner_id, step) DO UPDATE SET items = $3, project_name = $4, updated_at = clock_timestamp()`,
				st.session.OwnerID, step, input.Items, st.session.ProjectName); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET stage = 'ready', flow_position = 'done',
			updated_at = clock_timestamp() WHERE id = $1`, st.session.ID); err != nil {
			return err
		}
		if st, err = reload(ctx, tx, st); err != nil {
			return err
		}
		out = dto.Started{Session: s.sessionDTO(st), Skus: s.skusDTO(st, nil), Summary: summary(st)}
		return nil
	})
	return out, err
}

// resolveBriefs returns this batch's SKUs (by index in st.skus) with their brief filled: resolution and ratio from the
// output step else the product's default resolution and the library defaults; poses from the poses step else the category's ORA defaults; models and
// backgrounds from their steps (spread over the SKUs when randomized, else the first; a built-in background of
// another family is skipped) else the category default, or none when the step only describes one (its text is every
// SKU's description); the
// SKU's overrides win. ponytail: "random" is a round robin over SKU order, so a rerun gets the same spread.
func resolveBriefs(st *state, lib *registry.Library) map[int]model.Sku {
	picked := choices(st.session.Inputs)
	resolution, ratio := lib.Defaults.Resolution, lib.Defaults.Ratio
	if picked.Resolution != nil {
		resolution = *picked.Resolution
	}
	if picked.Ratio != nil {
		ratio = *picked.Ratio
	}
	spread := func(ids []string, random bool, n int, fallback string) string {
		switch {
		case len(ids) == 0:
			return fallback
		case random:
			return ids[n%len(ids)]
		}
		return ids[0]
	}
	out := map[int]model.Sku{}
	n, perWearer := 0, map[string]int{} // randomized models rotate within each wearer's picks
	for i, sku := range st.skus {
		if st.status[sku.ID] != "Not run" || resolved(sku) {
			continue
		}
		category := lib.Category(sku.Category)
		wearer := wearerOf(sku)
		var defaultModel, defaultBackground string
		var defaultPoses []string
		if category != nil {
			defaultModel, defaultBackground, defaultPoses = category.DefaultModelFor(wearer), category.DefaultBackground, category.DefaultPoses
		}
		poses := picked.Poses[sku.Category]
		if len(poses) == 0 {
			poses = defaultPoses
		}
		var family string
		if category != nil {
			family = category.Family
		}
		backgrounds := filter(picked.Backgrounds, func(id string) bool { return suits(lib, id, family) })
		// a description alone is the model or background: no default photo for it
		modelText, backgroundText := stepText(st.session.Inputs["models"]), stepText(st.session.Inputs["backgrounds"])
		// (picks that all suit another family still leave the product default, with the description)
		fallbackModel, fallbackBackground := defaultModel, defaultBackground
		if modelText != "" {
			fallbackModel = ""
		}
		if backgroundText != "" && len(picked.Backgrounds) == 0 {
			fallbackBackground = ""
		}
		// only the picks that suit the SKU's wearer (a men's kurta never gets a women's model); none: the default
		models := filter(picked.Models, func(id string) bool { return lib.SuitsWearer(id, wearer) })
		// and a description of the other wear's look ("a woman in her 20s" for a men's kurta) gives way to the default
		if said := textWearer(modelText); len(models) == 0 && wearer != "" && said != "" && said != wearer {
			modelText, fallbackModel = "", defaultModel
		}
		modelID := spread(models, randomized(st.session.Inputs["models"]), perWearer[wearer], fallbackModel)
		perWearer[wearer]++
		backgroundID := spread(backgrounds, randomized(st.session.Inputs["backgrounds"]), n, fallbackBackground)
		skuResolution := resolution
		if picked.Resolution == nil && category != nil && category.DefaultResolution != nil {
			skuResolution = *category.DefaultResolution
		}
		poseRatios, poseNotes := sku.Brief.PoseRatios, sku.Brief.PoseNotes // set by SKU edits, kept like overrides
		customs := sku.Brief.CustomPoses
		sku.Brief = briefOf(modelID, backgroundID, poses, skuResolution, ratio)
		sku.Brief.PoseRatios, sku.Brief.PoseNotes, sku.Brief.CustomPoses = poseRatios, poseNotes, customs
		sku.Brief.ModelDescription, sku.Brief.BackgroundDescription = nonBlank(modelText), nonBlank(backgroundText)
		sku.Brief.ShotsBrief = shotsBrief(st.session.Inputs, sku.Category)
		setOverrides(&sku, message.BriefPatch(sku.Overrides))
		keepCustomPoses(st, &sku)
		fillDefaults(lib, &sku) // the SKU cleared the step's description
		if !usesModel(lib, sku.Category) {
			sku.Brief.Model, sku.Brief.ModelDescription = nil, nil // furniture: rendered without a model
		}
		if sku.Brief.Background != nil && category != nil && !suits(lib, *sku.Brief.Background, family) {
			sku.Brief.Background = &defaultBackground // e.g. a question's answer gave rings and sofas "marble"
		}
		out[i], n = sku, n+1
	}
	return out
}

// usesModel reports whether a category is rendered on a model; an unknown category is treated as one that does.
func usesModel(lib *registry.Library, category string) bool {
	found := lib.Category(category)
	return found == nil || found.UsesModel
}

// suits reports whether a background fits a product family: an upload (not in the library) or a built-in background
// with no families fits every family.
func suits(lib *registry.Library, backgroundID, family string) bool {
	bg := lib.Background(backgroundID)
	return bg == nil || len(bg.Families) == 0 || slices.Contains(bg.Families, family)
}

// deref is the string p points at, "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// briefOf builds a brief; an empty model or background stays null.
func briefOf(modelID, backgroundID string, poses []string, resolution, ratio string) model.Brief {
	brief := model.Brief{Poses: slices.Clone(nonNil(poses)), Resolution: &resolution, Ratio: &ratio}
	if modelID != "" {
		brief.Model = &modelID
	}
	if backgroundID != "" {
		brief.Background = &backgroundID
	}
	return brief
}

// summary is the "{n} SKUs found" card: counts and code ranges per category, and the sample SKU (the first Not run).
func summary(st *state) dto.Summary {
	out := dto.Summary{Total: len(st.skus), ByCategory: []dto.CategoryCount{}}
	for _, sku := range st.skus {
		last := len(out.ByCategory) - 1
		if last < 0 || out.ByCategory[last].Category != sku.Category {
			out.ByCategory = append(out.ByCategory, dto.CategoryCount{Category: sku.Category, FirstCode: sku.Code})
			last++
		}
		out.ByCategory[last].Count++
		out.ByCategory[last].LastCode = sku.Code
		if out.SampleSkuID == nil && st.status[sku.ID] == "Not run" {
			out.SampleSkuID = &sku.ID
		}
	}
	return out
}
