// Package gallery: the caller's takes (/v1/studio/takes, /sessions/{id}/takes), uploads (/v1/studio/uploads), the
// take the projector stores for each run output (Record) and the sweep that removes deleted and abandoned objects.
package gallery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
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

// Service owns the takes table. signer makes the links (GET for takes, PUT for uploads), store is read to check an
// upload.
type Service struct {
	db     *pgxpool.Pool
	signer *common.Signer
	store  storage.Store
}

// NewService returns the service.
func NewService(db *pgxpool.Pool, signer *common.Signer, store storage.Store) *Service {
	return &Service{db: db, signer: signer, store: store}
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

// ParseFilter reads kind, source, starred, session_id, q, cursor and limit; the second result is the 422 detail.
// sessionParam false ignores a session_id (GET /sessions/{id}/takes sets it from the path).
func ParseFilter(query url.Values, sessionParam bool) (Filter, string) {
	filter := Filter{Limit: 60, Kind: query.Get("kind"), Source: query.Get("source")}
	if filter.Kind != "" && filter.Kind != "image" && filter.Kind != "video" && filter.Kind != "audio" {
		return filter, "kind must be image, video or audio"
	}
	if filter.Source != "" && filter.Source != "create" && filter.Source != "upload" {
		return filter, "source must be create or upload"
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

// Get returns one visible take.
func (s *Service) Get(ctx context.Context, caller common.Caller, id string) (dto.Take, error) {
	row, err := s.get(ctx, caller, id)
	return s.takeDTO(row), err
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

// Reuse returns the settings that made a take: 409 for an upload.
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
	return dto.TakeReuse{WorkflowID: madeWith.WorkflowID, Inputs: madeWith.Inputs}, nil
}

// takeDTO maps a row, signing its key: the original is its own thumbnail in API 1.2.
func (s *Service) takeDTO(row model.Take) dto.Take {
	if row.ID == "" {
		return dto.Take{}
	}
	link := s.signer.Sign(row.Key)
	out := dto.Take{ID: row.ID, Kind: row.Kind, Source: row.Source, Title: row.Title, URL: link, ThumbURL: link,
		ContentType: row.ContentType, Width: row.Width, Height: row.Height, Bytes: row.Bytes, CreatedAt: row.CreatedAt,
		Starred: row.Starred, SessionID: row.SessionID, RunID: row.RunID, Parents: row.Parents}
	_ = json.Unmarshal(row.MadeWith, &out.MadeWith)
	if out.Parents == nil {
		out.Parents = []string{}
	}
	return out
}
