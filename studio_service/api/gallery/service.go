// Package gallery: the caller's takes (/v1/studio/takes, /sessions/{id}/takes), uploads (/v1/studio/uploads), the
// take the projector stores for each run output (Record) and the sweep that removes deleted and abandoned objects.
package gallery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// errNotFound answers an unknown, malformed, deleted or another user's id alike.
var errNotFound = network.NewNotFoundError("not found", nil)

// UploadLink is how long an upload's PUT link lasts; UploadTTL how long a pending upload may wait for its complete.
const (
	UploadLink = 15 * time.Minute
	UploadTTL  = time.Hour
)

// visible is the condition of a take the caller can see: not deleted and, for an upload, completed.
const visible = `t.deleted_at IS NULL AND t.ready`

// Find returns a template or one of the caller's workflows with the schemas of its kinds and its stored version (nil
// for a template); an error that is a 404 when there is none (studio's FindWorkflow).
type Find func(ctx context.Context, caller common.Caller, id string) (registry.Workflow, *int, error)

// Service owns the takes table. signer makes the links (GET for takes, PUT for uploads), store is read to check an
// upload, registry tells whether the watches have loaded, find has the workflows reuse and "Use in…" read.
type Service struct {
	db       *pgxpool.Pool
	signer   *common.Signer
	store    storage.Store
	registry *registry.Registry
	find     Find
	offers   Offerable
}

// Offerable lists the workflows "Use in…" may offer, in offer order (studio's Offerable; API 1.19).
type Offerable func(ctx context.Context, caller common.Caller) ([]registry.Workflow, error)

// SetOffers sets where GET /takes/{id}/uses reads its workflows.
func (s *Service) SetOffers(offers Offerable) { s.offers = offers }

// NewService returns the service.
func NewService(db *pgxpool.Pool, signer *common.Signer, store storage.Store, registry *registry.Registry, find Find) *Service {
	return &Service{db: db, signer: signer, store: store, registry: registry, find: find}
}

// tx runs fn scoped to the caller as a plain user: an admin sees only their own takes here too.
func (s *Service) tx(ctx context.Context, caller common.Caller, fn func(tx pgx.Tx) error) error {
	return common.WithUser(ctx, s.db, common.Caller{ID: caller.ID}, fn)
}

// takes runs query and scans every row.
func takes(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]model.Take, error) {
	rows, _ := tx.Query(ctx, query, args...)
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Take])
}

// take runs query and scans its row; no row is errNotFound.
func take(ctx context.Context, tx pgx.Tx, query string, args ...any) (model.Take, error) {
	rows, _ := tx.Query(ctx, query, args...)
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[model.Take])
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	return row, err
}

// ------------------------------------------------------------------ list

// Filter is GET /takes's query, parsed.
type Filter struct {
	Kind, Source, SessionID string
	Starred                 *bool
	Words                   []string // q split on whitespace
	After                   *cursor
	Limit                   int
}

// cursor is the last take of a page: the next page starts after it.
type cursor struct {
	CreatedAt time.Time
	ID        string
}

// encode makes the opaque next_cursor.
func (c cursor) encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

// decodeCursor reads a next_cursor; false when it is malformed.
func decodeCursor(text string) (*cursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	stamp, id, found := strings.Cut(string(raw), "|")
	at, timeErr := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !found || timeErr != nil || !utility.IsUUID(id) {
		return nil, false
	}
	return &cursor{CreatedAt: at, ID: id}, true
}

// takeSources are TakeSource's values (contracts/openapi.yaml): import from 1.16, export and frame from 1.17.
var takeSources = []string{"create", "upload", "pipeline", "import", "export", "frame"}

// ParseFilter reads kind, source, starred, session_id, q, cursor and limit; the second result is the 422 detail.
// sessionParam false ignores a session_id (GET /sessions/{id}/takes sets it from the path).
func ParseFilter(query url.Values, sessionParam bool) (Filter, string) {
	filter := Filter{Limit: 60, Kind: query.Get("kind"), Source: query.Get("source")}
	if filter.Kind != "" && filter.Kind != "image" && filter.Kind != "video" && filter.Kind != "audio" {
		return filter, "kind must be image, video or audio"
	}
	if filter.Source != "" && !slices.Contains(takeSources, filter.Source) {
		return filter, "source must be " + strings.Join(takeSources[:len(takeSources)-1], ", ") + " or " + takeSources[len(takeSources)-1]
	}
	if text := query.Get("starred"); text != "" {
		if text != "true" && text != "false" {
			return filter, "starred must be true or false"
		}
		starred := text == "true"
		filter.Starred = &starred
	}
	if sessionParam && query.Has("session_id") {
		if filter.SessionID = query.Get("session_id"); !utility.IsUUID(filter.SessionID) {
			return filter, "session_id must be a uuid"
		}
	}
	q := query.Get("q")
	if utf8.RuneCountInString(q) > 200 {
		return filter, "q must be at most 200 characters"
	}
	filter.Words = strings.Fields(q)
	if text := query.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 || limit > 200 {
			return filter, "limit must be a whole number from 1 to 200"
		}
		filter.Limit = limit
	}
	if text := query.Get("cursor"); text != "" {
		var ok bool
		if filter.After, ok = decodeCursor(text); !ok {
			return filter, "cursor is not a next_cursor"
		}
	}
	return filter, ""
}

// likeEscaper makes a word match itself in ILIKE (\ is ILIKE's default escape).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// List returns a page of the caller's visible takes matching filter, newest first. q: every word must occur in the
// title or the prompt, case-insensitively. ponytail: ILIKE scans the caller's takes; a pg_trgm index if galleries
// grow to tens of thousands.
func (s *Service) List(ctx context.Context, caller common.Caller, filter Filter) (dto.TakePage, error) {
	conds, args := []string{visible}, []any{}
	where := func(cond string, values ...any) {
		for _, value := range values {
			args = append(args, value)
			cond = strings.Replace(cond, "$?", "$"+strconv.Itoa(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if filter.Kind != "" {
		where(`t.kind = $?`, filter.Kind)
	}
	if filter.Source != "" {
		where(`t.source = $?`, filter.Source)
	}
	if filter.Starred != nil {
		where(`t.starred = $?`, *filter.Starred)
	}
	if filter.SessionID != "" {
		where(`t.session_id = $?`, filter.SessionID)
	}
	for _, word := range filter.Words {
		pattern := "%" + likeEscaper.Replace(word) + "%"
		where(`(t.title ILIKE $? OR t.made_with->>'prompt' ILIKE $?)`, pattern, pattern)
	}
	if filter.After != nil {
		where(`(t.created_at, t.id) < ($?, $?::uuid)`, filter.After.CreatedAt, filter.After.ID)
	}
	args = append(args, filter.Limit+1)
	query := `SELECT ` + model.TakeCols + ` FROM takes t WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY t.created_at DESC, t.id DESC LIMIT $` + strconv.Itoa(len(args))

	out := dto.TakePage{Takes: []dto.Take{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := takes(ctx, tx, query, args...)
		if len(rows) > filter.Limit {
			rows = rows[:filter.Limit]
			next := cursor{CreatedAt: rows[len(rows)-1].CreatedAt, ID: rows[len(rows)-1].ID}.encode()
			out.NextCursor = &next
		}
		for _, row := range rows {
			out.Takes = append(out.Takes, s.takeDTO(row))
		}
		return err
	})
	return out, err
}

// SessionTakes is List of one session: 404 for an unknown session.
func (s *Service) SessionTakes(ctx context.Context, caller common.Caller, sessionID string, filter Filter) (dto.TakePage, error) {
	if !utility.IsUUID(sessionID) {
		return dto.TakePage{}, errNotFound
	}
	var found bool
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)`, sessionID).Scan(&found)
	})
	if err != nil {
		return dto.TakePage{}, err
	}
	if !found {
		return dto.TakePage{}, errNotFound
	}
	filter.SessionID = sessionID
	return s.List(ctx, caller, filter)
}

// ------------------------------------------------------------------ one take

// Get returns one visible take with its children: the visible takes made from it, newest first, at most 50.
func (s *Service) Get(ctx context.Context, caller common.Caller, id string) (dto.Take, error) {
	if !utility.IsUUID(id) {
		return dto.Take{}, errNotFound
	}
	var out dto.Take
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		if err != nil {
			return err
		}
		out = s.takeDTO(row)
		type child struct{ ID, Kind, Title, Key string }
		rows, _ := tx.Query(ctx, `SELECT t.id::text, t.kind, t.title, `+model.ThumbKey+` FROM take_parents p JOIN takes t ON t.id = p.take_id
			WHERE p.parent_id = $1 AND `+visible+` ORDER BY t.created_at DESC, t.id DESC LIMIT 50`, id)
		children, err := pgx.CollectRows(rows, pgx.RowToStructByPos[child])
		refs := make([]dto.TakeRef, len(children))
		for i, c := range children {
			refs[i] = dto.TakeRef{ID: c.ID, Kind: c.Kind, Title: c.Title, ThumbURL: s.signer.Sign(c.Key)}
		}
		out.Children = &refs
		if err != nil {
			return err
		}
		decisions, err := takeDecisions(ctx, tx, id)
		out.Decisions = &decisions
		return err
	})
	return out, err
}

// takeDecisions are the judge decisions that weighed a take, newest first, at most 16: picked when it was kept (the
// user's pick at a Suggest judge once made, else the judge's), passed when every must held, else failed with its first
// false must.
func takeDecisions(ctx context.Context, tx pgx.Tx, id string) ([]dto.TakeDecision, error) {
	type row struct {
		ID, BatchID string
		Row         int
		Data        json.RawMessage
		UserPick    json.RawMessage
	}
	rows, _ := tx.Query(ctx, `SELECT id, batch_id::text, n, data, user_pick FROM decisions WHERE take_ids @> ARRAY[$1::uuid]
		ORDER BY created_at DESC, id LIMIT 16`, id)
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	out := []dto.TakeDecision{}
	for _, d := range found {
		var data struct {
			Node  string `json:"node"`
			Judge struct {
				Criteria struct {
					Must []string `json:"must"`
				} `json:"criteria"`
				Takes []struct {
					TakeID string `json:"take_id"`
					Checks []bool `json:"checks"`
					Passes bool   `json:"passes"`
				} `json:"takes"`
				Picked []string `json:"picked"`
			} `json:"judge"`
		}
		_ = json.Unmarshal(d.Data, &data)
		kept := data.Judge.Picked
		if len(d.UserPick) > 0 && string(d.UserPick) != "null" {
			kept = nil
			_ = json.Unmarshal(d.UserPick, &kept)
		}
		shown := dto.TakeDecision{DecisionID: d.ID, BatchID: d.BatchID, Row: d.Row, Node: data.Node, Outcome: "failed"}
		for _, t := range data.Judge.Takes {
			if t.TakeID != id {
				continue
			}
			switch {
			case slices.Contains(kept, id):
				shown.Outcome = "picked"
			case t.Passes:
				shown.Outcome = "passed"
			}
			for i, must := range data.Judge.Criteria.Must {
				if !t.Passes && i < len(t.Checks) && !t.Checks[i] {
					shown.FirstFailed = &must
					break
				}
			}
		}
		out = append(out, shown)
	}
	return out, err
}

// get reads one visible take.
func (s *Service) get(ctx context.Context, caller common.Caller, id string) (model.Take, error) {
	if !utility.IsUUID(id) {
		return model.Take{}, errNotFound
	}
	var row model.Take
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		return err
	})
	return row, err
}

// Update renames or stars a visible take.
func (s *Service) Update(ctx context.Context, caller common.Caller, id string, body *dto.TakePatch) (dto.Take, error) {
	if !utility.IsUUID(id) {
		return dto.Take{}, errNotFound
	}
	var title *string
	if body.Title != nil {
		trimmed := strings.TrimSpace(*body.Title)
		title = &trimmed
	}
	var row model.Take
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `UPDATE takes t SET title = coalesce($2, t.title), starred = coalesce($3, t.starred)
			WHERE t.id = $1 AND `+visible+` RETURNING `+model.TakeCols, id, title, body.Starred)
		return err
	})
	return s.takeDTO(row), err
}

// Delete soft-deletes a visible take: it leaves every answer at once (its run output row goes too) and the sweep
// removes the object after Grace.
func (s *Service) Delete(ctx context.Context, caller common.Caller, id string) error {
	if !utility.IsUUID(id) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE takes t SET deleted_at = clock_timestamp() WHERE t.id = $1 AND `+visible, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM run_outputs WHERE take_id = $1`, id)
		return err
	})
}

// Reuse returns the settings that made a take (409 for an upload) and how its workflow compares now: current, changed
// (a user workflow edited since: inputs no longer settable are dropped) or deleted (inputs as recorded).
func (s *Service) Reuse(ctx context.Context, caller common.Caller, id string) (dto.TakeReuse, error) {
	row, err := s.get(ctx, caller, id)
	if err != nil {
		return dto.TakeReuse{}, err
	}
	var madeWith *dto.MadeWith
	_ = json.Unmarshal(row.MadeWith, &madeWith)
	if madeWith == nil {
		return dto.TakeReuse{}, network.NewConflictError("an upload has no settings to reuse", nil)
	}
	if madeWith.Inputs == nil {
		madeWith.Inputs = map[string]any{}
	}
	if !s.registry.Ready() {
		return dto.TakeReuse{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	maps.Copy(madeWith.Inputs, madeWith.Typed) // the prompt as typed, the rows without the element images
	out := dto.TakeReuse{WorkflowID: madeWith.WorkflowID, Inputs: madeWith.Inputs, WorkflowStatus: "current"}
	if out.ElementsStatus, err = s.elementsStatus(ctx, caller, madeWith.Elements); err != nil {
		return dto.TakeReuse{}, err
	}
	if out.LorasStatus, err = s.lorasStatus(ctx, caller, madeWith.Loras); err != nil {
		return dto.TakeReuse{}, err
	}
	if out.LooksStatus, err = s.looksStatus(ctx, caller, madeWith.Looks); err != nil {
		return dto.TakeReuse{}, err
	}
	for _, l := range madeWith.Looks {
		out.Looks = append(out.Looks, l.ID)
	}
	workflow, version, err := s.find(ctx, caller, madeWith.WorkflowID)
	var notFound *network.APIError
	switch {
	case errors.As(err, &notFound) && notFound.Code == http.StatusNotFound:
		out.WorkflowStatus = "deleted"
	case err != nil:
		return dto.TakeReuse{}, err
	case version != nil && (madeWith.WorkflowVersion == nil || *version != *madeWith.WorkflowVersion):
		out.WorkflowStatus = "changed"
		for key := range out.Inputs {
			if !settable(workflow, key) {
				delete(out.Inputs, key)
			}
		}
	}
	return out, nil
}

// elementsStatus compares each element a take mentioned with the caller's library now: current (same version), changed
// or deleted.
func (s *Service) elementsStatus(ctx context.Context, caller common.Caller, used []dto.MadeWithElement) ([]dto.ElementStatus, error) {
	if len(used) == 0 {
		return nil, nil
	}
	ids := make([]string, len(used))
	for i, e := range used {
		ids[i] = e.ID
	}
	type now struct {
		ID      string
		Handle  *string
		Version int
	}
	var rows []now
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		found, _ := tx.Query(ctx, `SELECT id::text, handle, version FROM library_items WHERE id = ANY($1::uuid[])`, ids)
		var err error
		rows, err = pgx.CollectRows(found, pgx.RowToStructByPos[now])
		return err
	})
	out := make([]dto.ElementStatus, len(used))
	for i, e := range used {
		out[i] = dto.ElementStatus{ID: e.ID, Handle: e.Handle, Status: "deleted"}
		if at := slices.IndexFunc(rows, func(r now) bool { return r.ID == e.ID }); at >= 0 {
			out[i].Status, out[i].CurrentHandle = "changed", rows[at].Handle
			if rows[at].Version == e.Version {
				out[i].Status = "current"
			}
		}
	}
	return out, err
}

// looksStatus compares each look a take was made with against what the caller can use now: current (same version),
// changed (another version), deleted (own, deleted since) or missing (starter, no live host publishes it).
func (s *Service) looksStatus(ctx context.Context, caller common.Caller, used []dto.MadeWithLook) ([]dto.LookStatus, error) {
	if len(used) == 0 {
		return nil, nil
	}
	var own []string
	for _, l := range used {
		if utility.IsUUID(l.ID) {
			own = append(own, l.ID)
		}
	}
	versions := map[string]int{}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if len(own) == 0 {
			return nil
		}
		rows, _ := tx.Query(ctx, `SELECT id::text, version FROM library_items WHERE kind = 'look' AND id = ANY($1::uuid[])`, own)
		var id string
		var version int
		_, err := pgx.ForEachRow(rows, []any{&id, &version}, func() error { versions[id] = version; return nil })
		return err
	})
	starters := s.registry.Looks(time.Now())
	out := make([]dto.LookStatus, len(used))
	for i, l := range used {
		version, ok := versions[l.ID]
		gone := "deleted"
		if !utility.IsUUID(l.ID) {
			starter, live := starters[l.ID]
			version, ok, gone = starter.Version, live, "missing"
		}
		out[i] = dto.LookStatus{ID: l.ID, Status: gone}
		if ok {
			out[i].Status = map[bool]string{true: "current", false: "changed"}[version == l.Version]
		}
	}
	return out, err
}

// lorasStatus says, per LoRA a take was made with, whether the caller can use it now: current, missing (shared, no
// live host lists it) or deleted (own).
func (s *Service) lorasStatus(ctx context.Context, caller common.Caller, used []dto.MadeWithLora) ([]dto.LoraStatus, error) {
	if len(used) == 0 {
		return nil, nil
	}
	ids := make([]string, len(used))
	for i, u := range used {
		ids[i] = u.ID
	}
	var usable map[string]lora.Info
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		usable, err = lora.Usable(ctx, tx, s.registry, caller.ID, "", ids)
		return err
	})
	out := make([]dto.LoraStatus, len(used))
	for i, u := range used {
		out[i] = dto.LoraStatus{ID: u.ID, Status: "current"}
		if _, ok := usable[u.ID]; !ok {
			out[i].Status = map[bool]string{true: "missing", false: "deleted"}[u.Source == "shared"]
		}
	}
	return out, err
}

// settable reports whether a run of workflow may set the input key: "<node>.<param>" of an unwired param, or
// "<node>.bypass" of a node that can be bypassed.
func settable(workflow registry.Workflow, key string) bool {
	nodeID, name, _ := strings.Cut(key, ".")
	node, ok := workflow.Nodes[nodeID]
	schema := workflow.NodeSchemas[node.Kind]
	if !ok {
		return false
	}
	if name == "bypass" {
		return schema.Passthrough() != nil
	}
	_, wired := node.In[name]
	return !wired && slices.ContainsFunc(schema.Params, func(p registry.Param) bool { return p.Name == name })
}

// Uses lists "Use in…" (API 1.19, take_uses): every workflow the take fits, with its inputs pre-filled.
func (s *Service) Uses(ctx context.Context, caller common.Caller, id string) (dto.TakeUses, error) {
	row, err := s.get(ctx, caller, id)
	if err != nil {
		return dto.TakeUses{}, err
	}
	if !s.registry.Ready() {
		return dto.TakeUses{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.TakeUses{Uses: []dto.TakeOffer{}}
	if row.Kind == "image" && (row.Width == nil || row.Height == nil) {
		return out, nil // an image without a size goes into no workflow (Use: has no size)
	}
	workflows, err := s.offers(ctx, caller)
	if err == nil {
		out.Uses = Offers(FactsOf(row), workflows, nil)
	}
	return out, err
}

// Use answers "Use in…": the inputs that put the take into the workflow's image input (body.Param, else its only
// one): {take_id} for an image param, [{<field>: {take_id}}] for a rows param whose item has one Image field. From
// API 1.19 a video or audio take goes into a media param that accepts it, as Offers fills it.
func (s *Service) Use(ctx context.Context, caller common.Caller, id string, body *dto.TakeUse) (dto.TakeReuse, error) {
	row, err := s.get(ctx, caller, id)
	if err != nil {
		return dto.TakeReuse{}, err
	}
	if row.Kind != "image" {
		return s.useMedia(ctx, caller, row, body)
	}
	if row.Width == nil || row.Height == nil { // release needs them (studio vetTakes)
		return dto.TakeReuse{}, network.NewUnprocessableError("take "+id+" has no size", nil)
	}
	if !s.registry.Ready() {
		return dto.TakeReuse{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	workflow, _, err := s.find(ctx, caller, body.WorkflowID)
	var notFound *network.APIError
	if errors.As(err, &notFound) && notFound.Code == http.StatusNotFound {
		return dto.TakeReuse{}, network.NewUnprocessableError("unknown workflow "+body.WorkflowID, nil)
	} else if err != nil {
		return dto.TakeReuse{}, err
	}
	if registry.IsText(workflow.Nodes) {
		return dto.TakeReuse{}, network.NewUnprocessableError("workflow "+workflow.ID+" answers text; only a judge, write or decide node runs it", nil)
	}
	key, param, detail := imageInput(workflow, body.Param)
	if detail != "" {
		return dto.TakeReuse{}, network.NewUnprocessableError(detail, nil)
	}
	field, _ := param.ImageInput()
	var value any = map[string]any{"take_id": row.ID}
	if param.Type == "rows" {
		value = []any{map[string]any{field: value}}
	}
	return dto.TakeReuse{WorkflowID: workflow.ID, Inputs: map[string]any{key: value}}, nil
}

// useMedia is Use for a video or audio take: the workflow's media param that accepts it (body.Param, else its only
// one), the take fitting it (media_fit), with the inputs an offer has.
func (s *Service) useMedia(ctx context.Context, caller common.Caller, row model.Take, body *dto.TakeUse) (dto.TakeReuse, error) {
	if !s.registry.Ready() {
		return dto.TakeReuse{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	workflow, _, err := s.find(ctx, caller, body.WorkflowID)
	var notFound *network.APIError
	if errors.As(err, &notFound) && notFound.Code == http.StatusNotFound {
		return dto.TakeReuse{}, network.NewUnprocessableError("unknown workflow "+body.WorkflowID, nil)
	} else if err != nil {
		return dto.TakeReuse{}, err
	}
	take := FactsOf(row)
	var keys []string
	params := map[string]registry.Param{}
	for _, nodeID := range slices.Sorted(maps.Keys(workflow.Nodes)) {
		node := workflow.Nodes[nodeID]
		for _, p := range workflow.NodeSchemas[node.Kind].Params {
			_, wired := node.In[p.Name]
			if !wired && !node.Bypass && p.Type == "media" && (slices.Contains(p.Accepts, take.Kind) ||
				(take.Kind == "video" && slices.Contains(p.Accepts, "audio"))) {
				keys = append(keys, nodeID+"."+p.Name)
				params[nodeID+"."+p.Name] = p
			}
		}
	}
	unprocessable := func(detail string) (dto.TakeReuse, error) {
		return dto.TakeReuse{}, network.NewUnprocessableError(detail, nil)
	}
	switch _, ok := params[deref(body.Param)]; {
	case body.Param != nil && !ok:
		return unprocessable(*body.Param + ": not a " + take.Kind + " input of workflow " + workflow.ID)
	case body.Param != nil:
		keys = []string{*body.Param}
	case len(keys) == 0:
		return unprocessable("workflow " + workflow.ID + " takes no " + take.Kind + " input")
	case len(keys) > 1:
		return unprocessable("workflow " + workflow.ID + " has several " + take.Kind + " inputs; name one in param")
	}
	if detail := MediaFit(params[keys[0]], take); detail != "" {
		return unprocessable(detail)
	}
	inputs, ok := mediaInputs(take, workflow, params[keys[0]], keys[0], nil)
	if !ok {
		return unprocessable(fmt.Sprintf("take %s has %d frames; extending it would pass %d", take.ID, take.Frames, LtxMaxFrames))
	}
	return dto.TakeReuse{WorkflowID: workflow.ID, Inputs: inputs}, nil
}

// imageInput picks the workflow's image input: want ("<node>.<param>") when given, else its only one. The third
// result is the 422 detail.
func imageInput(workflow registry.Workflow, want *string) (string, registry.Param, string) {
	var keys []string
	params := map[string]registry.Param{}
	for _, nodeID := range slices.Sorted(maps.Keys(workflow.Nodes)) {
		node := workflow.Nodes[nodeID]
		for _, param := range workflow.NodeSchemas[node.Kind].Params {
			if _, wired := node.In[param.Name]; wired {
				continue // filled from upstream, not settable
			}
			if _, ok := param.ImageInput(); ok {
				keys = append(keys, nodeID+"."+param.Name)
				params[nodeID+"."+param.Name] = param
			}
		}
	}
	switch {
	case want != nil:
		if _, ok := params[*want]; !ok {
			return "", registry.Param{}, *want + ": not an image input of workflow " + workflow.ID
		}
		return *want, params[*want], ""
	case len(keys) == 0:
		return "", registry.Param{}, "workflow " + workflow.ID + " takes no image input"
	case len(keys) > 1:
		return "", registry.Param{}, "workflow " + workflow.ID + " has several image inputs; name one in param"
	}
	return keys[0], params[keys[0]], ""
}

// takeDTO maps a row, signing its keys.
func (s *Service) takeDTO(row model.Take) dto.Take { return TakeDTO(s.signer, row) }

// TakeDTO maps a row, signing its keys: an image is its own thumbnail (API 1.2), a video's is its poster.
func TakeDTO(signer *common.Signer, row model.Take) dto.Take {
	if row.ID == "" {
		return dto.Take{}
	}
	link := signer.Sign(row.Key)
	video := dto.Info(row.Video, signer.Sign)
	thumb := link
	if video != nil {
		thumb = video.PosterURL
	}
	out := dto.Take{ID: row.ID, Kind: row.Kind, Source: row.Source, Title: row.Title, URL: link, ThumbURL: thumb, Video: video,
		ContentType: row.ContentType, Width: row.Width, Height: row.Height, Bytes: row.Bytes, CreatedAt: row.CreatedAt,
		Starred: row.Starred, SessionID: row.SessionID, RunID: row.RunID, Parents: row.Parents, BatchID: row.BatchID}
	for _, raw := range []struct {
		to   *json.RawMessage
		from json.RawMessage
	}{{&out.Legacy, row.Legacy},
		{&out.FrameOf, row.FrameOf}, {&out.Export, row.Export}} {
		if len(raw.from) > 0 && string(raw.from) != "null" {
			*raw.to = raw.from
		}
	}
	_ = json.Unmarshal(row.MadeWith, &out.MadeWith)
	_ = json.Unmarshal(row.Audio, &out.Audio) // null leaves them nil
	_ = json.Unmarshal(row.SourceMap, &out.SourceMap)
	if out.Parents == nil {
		out.Parents = []string{}
	}
	return out
}
