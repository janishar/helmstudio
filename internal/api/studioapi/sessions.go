package studioapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Sessions are a platform concept (R31a, 02 §5): the studio's own settings in
// an opaque state document, named uniquely among its live sessions.

const sessionCols = `id, studio_id, name, state, etag, created_at, opened_at`

func scanSession(sc interface{ Scan(...any) error }) (*helm.Session, error) {
	var out helm.Session
	var state string
	var created int64
	var opened sql.NullInt64
	if err := sc.Scan(&out.ID, &out.StudioID, &out.Name, &state, &out.ETag, &created, &opened); err != nil {
		return nil, err
	}
	doc, err := decodeDoc(state)
	if err != nil {
		return nil, err
	}
	out.State, out.CreatedAt, out.OpenedAt = doc, fromMS(created), fromNullMS(opened)
	return &out, nil
}

func (s *Service) readSession(ctx context.Context, q querier, studioID, id string) (*helm.Session, error) {
	out, err := scanSession(q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ? AND studio_id = ? AND deleted_at IS NULL`, id, studioID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no session %s", id)
	}
	return out, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Service) SessionsList(ctx context.Context, params helm.SessionsListParams) (*helm.SessionPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("sessions", p.StudioID)
	after, err := decodeCursor(params.Cursor, digest, 3)
	if err != nil {
		return nil, err
	}
	// Order: opened sessions newest first, then never-opened ones, each by
	// created_at then id, all descending. The sort key folds that into
	// (opened flag, time, id).
	const key = `(opened_at IS NOT NULL)`
	const t = `COALESCE(opened_at, created_at)`
	q := `SELECT ` + sessionCols + `, ` + key + `, ` + t + ` FROM sessions WHERE studio_id = ? AND deleted_at IS NULL`
	args := []any{p.StudioID}
	if after != nil {
		flag, ok1 := cursorInt(after[0])
		at, ok2 := cursorInt(after[1])
		id, ok3 := cursorString(after[2])
		if !ok1 || !ok2 || !ok3 {
			return nil, badCursor()
		}
		q += ` AND (` + key + `, ` + t + `, id) < (?, ?, ?)`
		args = append(args, flag, at, id)
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY ` + key + ` DESC, ` + t + ` DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &helm.SessionPage{Items: []helm.Session{}}
	var last []any
	for rows.Next() {
		var sess helm.Session
		var state string
		var created, flag, at int64
		var opened sql.NullInt64
		if err := rows.Scan(&sess.ID, &sess.StudioID, &sess.Name, &state, &sess.ETag, &created, &opened, &flag, &at); err != nil {
			return nil, err
		}
		if sess.State, err = decodeDoc(state); err != nil {
			return nil, err
		}
		sess.CreatedAt, sess.OpenedAt = fromMS(created), fromNullMS(opened)
		if len(page.Items) < limit {
			page.Items = append(page.Items, sess)
			last = []any{flag, at, sess.ID}
		} else {
			page.NextCursor = encodeCursor(digest, last...)
		}
	}
	return page, rows.Err()
}

func (s *Service) SessionsCreate(ctx context.Context, body *helm.SessionCreate) (*helm.Session, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	state, err := encodeDoc(body.State)
	if err != nil {
		return nil, err
	}
	var out *helm.Session
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.checkKVBytes(ctx, tx, p.StudioID, 0, int64(len(state))); err != nil {
			return err
		}
		id := s.newID()
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, studio_id, name, state, etag, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, p.StudioID, body.Name, state, newETag(), ms(s.now()))
		if isUniqueViolation(err) {
			return conflict("name_taken", "the studio already has a session named %q", body.Name)
		}
		if err != nil {
			return err
		}
		out, err = s.readSession(ctx, tx, p.StudioID, id)
		return err
	})
	return out, err
}

func (s *Service) SessionsGet(ctx context.Context, id string) (*helm.Session, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.readSession(ctx, s.st.Reader(), p.StudioID, id)
}

// SessionsUpdate applies a merge patch over {name, state} (first review #11):
// state members merge into the stored state.
func (s *Service) SessionsUpdate(ctx context.Context, id string, body map[string]any, params helm.SessionsUpdateParams) (*helm.Session, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	name, hasName := body["name"]
	if hasName {
		n, ok := name.(string)
		if !ok || n == "" || len([]rune(n)) > 200 {
			return nil, badRequest("name must be a string of 1 to 200 characters")
		}
	}
	patchState, hasState := body["state"]
	if hasState {
		if _, ok := patchState.(map[string]any); !ok {
			return nil, badRequest("state must be an object")
		}
	}
	var out *helm.Session
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readSession(ctx, tx, p.StudioID, id)
		if err != nil {
			return err
		}
		if params.IfMatch != nil && unquoteETag(*params.IfMatch) != cur.ETag {
			return etagMismatch("session " + id)
		}
		newName := cur.Name
		if hasName {
			newName = name.(string)
		}
		state := cur.State
		if hasState {
			state = mergePatch(state, patchState.(map[string]any))
		}
		oldDoc, _ := encodeDoc(cur.State)
		doc, err := encodeDoc(state)
		if err != nil {
			return err
		}
		if err := s.checkKVBytes(ctx, tx, p.StudioID, int64(len(oldDoc)), int64(len(doc))); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET name = ?, state = ?, etag = ? WHERE id = ?`, newName, doc, newETag(), id)
		if isUniqueViolation(err) {
			return conflict("name_taken", "the studio already has a session named %q", newName)
		}
		if err != nil {
			return err
		}
		out, err = s.readSession(ctx, tx, p.StudioID, id)
		return err
	})
	return out, err
}

func (s *Service) SessionsDelete(ctx context.Context, id string, params helm.SessionsDeleteParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readSession(ctx, tx, p.StudioID, id)
		if err != nil {
			return err
		}
		if params.IfMatch != nil && unquoteETag(*params.IfMatch) != cur.ETag {
			return etagMismatch("session " + id)
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET deleted_at = ? WHERE id = ?`, ms(s.now()), id)
		return err
	})
}

func (s *Service) SessionsDuplicate(ctx context.Context, id string, body *helm.SessionDuplicate) (*helm.Session, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Session
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.readSession(ctx, tx, p.StudioID, id)
		if err != nil {
			return err
		}
		state, err := encodeDoc(cur.State)
		if err != nil {
			return err
		}
		if err := s.checkKVBytes(ctx, tx, p.StudioID, 0, int64(len(state))); err != nil {
			return err
		}
		newID := s.newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO sessions (id, studio_id, name, state, etag, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			newID, p.StudioID, body.Name, state, newETag(), ms(s.now()))
		if isUniqueViolation(err) {
			return conflict("name_taken", "the studio already has a session named %q", body.Name)
		}
		if err != nil {
			return err
		}
		out, err = s.readSession(ctx, tx, p.StudioID, newID)
		return err
	})
	return out, err
}

func (s *Service) SessionsActivate(ctx context.Context, id string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET opened_at = ? WHERE id = ? AND studio_id = ? AND deleted_at IS NULL`, ms(s.now()), id, p.StudioID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("no session %s", id)
		}
		return nil
	})
}
