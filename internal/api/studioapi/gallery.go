package studioapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Gallery (R38, 07 §5, Q15-Q17, first review #1, #3, #11).

type itemRow struct {
	id, studio, kind, assetID string
	session, timeline, title  sql.NullString
	params                    string
	starred                   bool
	created                   int64
	deleted                   sql.NullInt64
}

const itemCols = `i.id, i.studio_id, i.kind, i.asset_id, i.session_id, i.timeline_id, i.title, i.params, i.starred, i.created_at, i.deleted_at`

func scanItem(sc interface{ Scan(...any) error }, extra ...any) (*itemRow, error) {
	var r itemRow
	var starred int64
	if err := sc.Scan(append([]any{&r.id, &r.studio, &r.kind, &r.assetID, &r.session, &r.timeline, &r.title, &r.params, &starred, &r.created, &r.deleted}, extra...)...); err != nil {
		return nil, err
	}
	r.starred = starred == 1
	return &r, nil
}

// visible reports whether p may see an item: its own, one with
// gallery.read_all, or one delivered to its inbox.
const visibleItem = `(i.studio_id = ?S OR ?R = 1 OR EXISTS (SELECT 1 FROM inbox n WHERE n.item_id = i.id AND n.to_studio = ?S))`

func bindVisible(q string, p Principal) (string, []any) {
	q = strings.ReplaceAll(q, "?S", "?")
	var args []any
	// Each ?S and ?R in visibleItem, in order.
	return strings.ReplaceAll(q, "?R", "?"), append(args, p.StudioID, readAll(p), p.StudioID)
}

// items loads the API view of rows, with their inputs, tags and assets.
func (s *Service) items(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, p Principal, rows []*itemRow) ([]helm.Item, error) {
	out := make([]helm.Item, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]any, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	in := "(?" + strings.Repeat(",?", len(ids)-1) + ")"
	inputs := map[string][]helm.ItemInput{}
	tags := map[string][]string{}
	rs, err := q.QueryContext(ctx, `SELECT item_id, asset_id, role FROM item_inputs WHERE item_id IN `+in+` ORDER BY role, asset_id`, ids...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var id string
		var x helm.ItemInput
		if err := rs.Scan(&id, &x.AssetID, &x.Role); err != nil {
			rs.Close()
			return nil, err
		}
		inputs[id] = append(inputs[id], x)
	}
	rs.Close()
	rs, err = q.QueryContext(ctx, `SELECT item_id, tag FROM tags WHERE item_id IN `+in+` ORDER BY tag`, ids...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var id, tag string
		if err := rs.Scan(&id, &tag); err != nil {
			rs.Close()
			return nil, err
		}
		tags[id] = append(tags[id], tag)
	}
	rs.Close()
	for _, r := range rows {
		a, err := scanAsset(q.QueryRowContext(ctx, `SELECT `+assetCols+` FROM assets WHERE id = ?`, r.assetID))
		if err != nil {
			return nil, err
		}
		params, err := decodeDoc(r.params)
		if err != nil {
			return nil, err
		}
		it := helm.Item{ID: r.id, StudioID: r.studio, Kind: helm.AssetKind(r.kind), AssetID: r.assetID, Asset: a.view(p),
			Params: params, Starred: r.starred, Tags: tags[r.id], Inputs: inputs[r.id], CreatedAt: fromMS(r.created)}
		if it.Tags == nil {
			it.Tags = []string{}
		}
		if it.Inputs == nil {
			it.Inputs = []helm.ItemInput{}
		}
		if r.session.Valid {
			it.SessionID = &r.session.String
		}
		if r.timeline.Valid {
			// An item that names a sequence is its export: the gallery labels
			// it "timeline" rather than by a studio (M8 Q17).
			it.TimelineID = &r.timeline.String
		}
		if r.title.Valid {
			it.Title = &r.title.String
		}
		out = append(out, it)
	}
	return out, nil
}

func promptOf(params map[string]any) string {
	if p, ok := params["prompt"].(string); ok {
		return p
	}
	return ""
}

func (s *Service) GalleryAdd(ctx context.Context, body *helm.ItemCreate) (*helm.Item, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	params, err := encodeDoc(body.Params)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	_ = json.Unmarshal([]byte(params), &decoded)
	var out *helm.Item
	var id string
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := s.readAsset(ctx, tx, p, body.AssetID); err != nil {
			return err
		}
		seenInput := map[helm.ItemInput]bool{}
		var inputs []helm.ItemInput
		for _, x := range body.Inputs {
			if seenInput[x] {
				continue
			}
			seenInput[x] = true
			if _, err := s.readAsset(ctx, tx, p, x.AssetID); err != nil {
				return notFound("input asset %s: no such asset", x.AssetID)
			}
			inputs = append(inputs, x)
		}
		var session any
		if body.SessionID != nil {
			var ok int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ? AND studio_id = ? AND deleted_at IS NULL`, *body.SessionID, p.StudioID).Scan(&ok)
			if errors.Is(err, sql.ErrNoRows) {
				return unprocessable("unknown_session", "session %s is not one of this studio's live sessions", *body.SessionID)
			}
			if err != nil {
				return err
			}
			session = *body.SessionID
		}
		var title any
		if body.Title != nil {
			title = *body.Title
		}
		now := ms(s.now())
		id = s.newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO items (id, studio_id, session_id, kind, asset_id, title, params, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.StudioID, session, string(body.Kind), body.AssetID, title, params, now); err != nil {
			return err
		}
		refs := []any{body.AssetID}
		for _, x := range inputs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_inputs (item_id, asset_id, role) VALUES (?, ?, ?)`, id, x.AssetID, x.Role); err != nil {
				return err
			}
			refs = append(refs, x.AssetID)
		}
		seenTag := map[string]bool{}
		for _, t := range body.Tags {
			if !seenTag[t] {
				seenTag[t] = true
				if _, err := tx.ExecContext(ctx, `INSERT INTO tags (item_id, tag) VALUES (?, ?)`, id, t); err != nil {
					return err
				}
			}
		}
		ttl := ""
		if body.Title != nil {
			ttl = *body.Title
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (item_id, title, prompt) VALUES (?, ?, ?)`, id, ttl, promptOf(decoded)); err != nil {
			return err
		}
		// An asset becomes reclaimable only after it has been referenced once.
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET first_referenced_at = ? WHERE first_referenced_at IS NULL AND id IN (?`+strings.Repeat(",?", len(refs)-1)+`)`,
			append([]any{now}, refs...)...); err != nil {
			return err
		}
		item, err := s.loadItem(ctx, tx, p, id, true)
		out = item
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publishItem("added", *out)
	return out, nil
}

// loadItem reads one item the principal may see. ownOnly narrows to the
// caller's own items.
func (s *Service) loadItem(ctx context.Context, tx *sql.Tx, p Principal, id string, ownOnly bool) (*helm.Item, error) {
	var q interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	} = s.st.Reader()
	if tx != nil {
		q = tx
	}
	cond, args := bindVisible(visibleItem, p)
	if ownOnly {
		cond, args = `i.studio_id = ?`, []any{p.StudioID}
	}
	r, err := scanItem(q.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id = ? AND i.deleted_at IS NULL AND `+cond, append([]any{id}, args...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no item %s", id)
	}
	if err != nil {
		return nil, err
	}
	items, err := s.items(ctx, q, p, []*itemRow{r})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) GalleryGet(ctx context.Context, id string) (*helm.Item, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadItem(ctx, nil, p, id, false)
}

func (s *Service) GalleryQuery(ctx context.Context, params helm.GalleryQueryParams) (*helm.ItemPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	all := params.Scope != nil && *params.Scope == "all"
	if all && !p.has(string(helm.CapabilityGalleryReadAll)) {
		return nil, capabilityRequired(string(helm.CapabilityGalleryReadAll))
	}
	q := `SELECT ` + itemCols + ` FROM items i WHERE i.deleted_at IS NULL`
	var args []any
	if all {
		if params.Studio != nil {
			q += ` AND i.studio_id = ?`
			args = append(args, *params.Studio)
		}
	} else {
		q += ` AND i.studio_id = ?`
		args = append(args, p.StudioID)
	}
	if params.Kind != nil {
		q += ` AND i.kind = ?`
		args = append(args, string(*params.Kind))
	}
	for _, t := range params.Tag {
		q += ` AND EXISTS (SELECT 1 FROM tags t WHERE t.item_id = i.id AND t.tag = ?)`
		args = append(args, t)
	}
	if params.SessionID != nil {
		q += ` AND i.session_id = ?`
		args = append(args, *params.SessionID)
	}
	if params.Starred != nil {
		q += ` AND i.starred = ?`
		args = append(args, boolInt(*params.Starred))
	}
	if params.AssetID != nil {
		q += ` AND i.asset_id = ?`
		args = append(args, *params.AssetID)
	}
	if params.Since != nil {
		q += ` AND i.created_at >= ?`
		args = append(args, ms(*params.Since))
	}
	if params.Until != nil {
		q += ` AND i.created_at < ?`
		args = append(args, ms(*params.Until))
	}
	if params.Q != nil && *params.Q != "" {
		q += ` AND i.id IN (SELECT item_id FROM items_fts WHERE items_fts MATCH ?)`
		args = append(args, *params.Q)
	}
	digest := filterDigest("gallery", p.StudioID, all, params.Studio, params.Kind, params.Tag, params.SessionID, params.Starred, params.AssetID, params.Since, params.Until, params.Q)
	page, err := s.itemPage(ctx, p, q, args, digest, params.Cursor, params.Limit)
	if err != nil && strings.Contains(err.Error(), "fts5") {
		return nil, badRequest("q is not a valid full-text query: %v", err)
	}
	return page, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// itemPage runs an item query newest first with a keyset cursor.
func (s *Service) itemPage(ctx context.Context, p Principal, q string, args []any, digest string, cur *string, limit *int64) (*helm.ItemPage, error) {
	after, err := decodeCursor(cur, digest, 2)
	if err != nil {
		return nil, err
	}
	if after != nil {
		at, ok1 := cursorInt(after[0])
		id, ok2 := cursorString(after[1])
		if !ok1 || !ok2 {
			return nil, badCursor()
		}
		q += ` AND (i.created_at, i.id) < (?, ?)`
		args = append(args, at, id)
	}
	n := pageLimit(limit)
	q += ` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`
	args = append(args, n+1)
	rs, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var rows []*itemRow
	for rs.Next() {
		r, err := scanItem(rs)
		if err != nil {
			rs.Close()
			return nil, err
		}
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}
	page := &helm.ItemPage{}
	if len(rows) > n {
		rows = rows[:n]
		last := rows[n-1]
		page.NextCursor = encodeCursor(digest, last.created, last.id)
	}
	page.Items, err = s.items(ctx, s.st.Reader(), p, rows)
	return page, err
}

func (s *Service) GalleryUpdate(ctx context.Context, id string, body map[string]any) (*helm.Item, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Item
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.loadItem(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		title := cur.Title
		if v, ok := body["title"]; ok {
			switch t := v.(type) {
			case nil:
				title = nil
			case string:
				if len([]rune(t)) > 500 {
					return badRequest("title is at most 500 characters")
				}
				title = &t
			default:
				return badRequest("title must be a string or null")
			}
		}
		starred := cur.Starred
		if v, ok := body["starred"]; ok {
			b, isBool := v.(bool)
			if !isBool {
				return badRequest("starred must be true or false")
			}
			starred = b
		}
		var titleArg any
		ttl := ""
		if title != nil {
			titleArg, ttl = *title, *title
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items SET title = ?, starred = ? WHERE id = ?`, titleArg, boolInt(starred), id); err != nil {
			return err
		}
		if v, ok := body["tags"]; ok {
			list, isList := v.([]any)
			if !isList || len(list) > 32 {
				return badRequest("tags must be an array of at most 32 strings")
			}
			var tags []string
			for _, x := range list {
				t, isStr := x.(string)
				if !isStr || t == "" || len([]rune(t)) > 64 {
					return badRequest("each tag is a string of 1 to 64 characters")
				}
				if !slices.Contains(tags, t) {
					tags = append(tags, t)
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE item_id = ?`, id); err != nil {
				return err
			}
			sort.Strings(tags)
			for _, t := range tags {
				if _, err := tx.ExecContext(ctx, `INSERT INTO tags (item_id, tag) VALUES (?, ?)`, id, t); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items_fts SET title = ? WHERE item_id = ?`, ttl, id); err != nil {
			return err
		}
		out, err = s.loadItem(ctx, tx, p, id, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publishItem("updated", *out)
	return out, nil
}

func (s *Service) GalleryDelete(ctx context.Context, id string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	var gone *helm.Item
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := s.loadItem(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		gone = cur
		if _, err := tx.ExecContext(ctx, `UPDATE items SET deleted_at = ? WHERE id = ?`, ms(s.now()), id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM items_fts WHERE item_id = ?`, id)
		return err
	})
	if err == nil {
		s.publishItem("deleted", *gone)
	}
	return err
}

// lineage returns the page of visible items among ids (a recursive walk that
// passes through every item), newest first.
func (s *Service) lineagePage(ctx context.Context, p Principal, walk string, walkArgs []any, exclude, digest string, params struct {
	Limit  *int64
	Cursor *string
}) (*helm.ItemPage, error) {
	cond, vargs := bindVisible(visibleItem, p)
	q := `WITH RECURSIVE ` + walk + ` SELECT ` + itemCols + ` FROM items i WHERE i.id IN (SELECT item_id FROM lineage) AND i.id != ? AND i.deleted_at IS NULL AND ` + cond
	args := append(append(append([]any{}, walkArgs...), exclude), vargs...)
	return s.itemPage(ctx, p, q, args, digest, params.Cursor, params.Limit)
}

func (s *Service) AssetsLineage(ctx context.Context, id string, params helm.AssetsLineageParams) (*helm.ItemPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.readAsset(ctx, s.st.Reader(), p, id); err != nil {
		return nil, err
	}
	// 02 §8, downstream: the items that used this asset, then onward.
	walk := `lineage(item_id) AS (
		SELECT item_id FROM item_inputs WHERE asset_id = ?
		UNION
		SELECT x.item_id FROM lineage l JOIN items i ON i.id = l.item_id JOIN item_inputs x ON x.asset_id = i.asset_id)`
	return s.lineagePage(ctx, p, walk, []any{id}, "", filterDigest("lineage-down", p.StudioID, id), struct {
		Limit  *int64
		Cursor *string
	}{params.Limit, params.Cursor})
}

func (s *Service) GalleryLineage(ctx context.Context, id string, params helm.GalleryLineageParams) (*helm.ItemPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadItem(ctx, nil, p, id, false); err != nil {
		return nil, err
	}
	// Upstream: the items whose asset is one of this item's inputs, then
	// onward. The starting item is never its own ancestor.
	walk := `lineage(item_id) AS (
		SELECT i.id FROM item_inputs x JOIN items i ON i.asset_id = x.asset_id WHERE x.item_id = ?
		UNION
		SELECT i.id FROM lineage l JOIN item_inputs x ON x.item_id = l.item_id JOIN items i ON i.asset_id = x.asset_id)`
	return s.lineagePage(ctx, p, walk, []any{id}, id, filterDigest("lineage-up", p.StudioID, id), struct {
		Limit  *int64
		Cursor *string
	}{params.Limit, params.Cursor})
}

// ---------------------------------------------------------------- handoff

func (s *Service) HandoffSend(ctx context.Context, body *helm.HandoffRequest) (*helm.Handoff, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if body.ToStudio == p.StudioID {
		return nil, unprocessable("self_handoff", "a studio cannot hand an item to itself")
	}
	if s.cfg.Studios != nil {
		if _, ok := s.cfg.Studios.Manifest(body.ToStudio); !ok {
			return nil, notFound("no studio %q is known", body.ToStudio)
		}
	}
	if _, err := s.loadItem(ctx, nil, p, body.ItemID, false); err != nil {
		return nil, err
	}
	out := &helm.Handoff{ID: s.newID(), ToStudio: body.ToStudio, ItemID: body.ItemID, CreatedAt: fromMS(ms(s.now()))}
	if body.Role != nil {
		out.Role = body.Role
	}
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var role any
		if body.Role != nil {
			role = *body.Role
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO inbox (id, to_studio, from_studio, item_id, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			out.ID, body.ToStudio, p.StudioID, body.ItemID, role, ms(out.CreatedAt))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.events.Publish("inbox", helm.InboxEvent{InboxID: out.ID, ItemID: out.ItemID, FromStudio: p.StudioID, Role: out.Role}, onlyStudio(body.ToStudio))
	return out, nil
}

func (s *Service) InboxList(ctx context.Context, params helm.InboxListParams) (*helm.InboxPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("inbox", p.StudioID)
	after, err := decodeCursor(params.Cursor, digest, 2)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, from_studio, item_id, role, created_at FROM inbox WHERE to_studio = ? AND consumed_at IS NULL`
	args := []any{p.StudioID}
	if after != nil {
		at, ok1 := cursorInt(after[0])
		id, ok2 := cursorString(after[1])
		if !ok1 || !ok2 {
			return nil, badCursor()
		}
		q += ` AND (created_at, id) > (?, ?)`
		args = append(args, at, id)
	}
	n := pageLimit(params.Limit)
	q += ` ORDER BY created_at, id LIMIT ?`
	args = append(args, n+1)
	rs, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type entry struct {
		helm.InboxEntry
		created int64
	}
	var entries []entry
	for rs.Next() {
		var e entry
		var role sql.NullString
		if err := rs.Scan(&e.ID, &e.FromStudio, &e.ItemID, &role, &e.created); err != nil {
			rs.Close()
			return nil, err
		}
		if role.Valid {
			e.Role = &role.String
		}
		e.CreatedAt = fromMS(e.created)
		entries = append(entries, e)
	}
	rs.Close()
	page := &helm.InboxPage{Items: []helm.InboxEntry{}}
	if len(entries) > n {
		entries = entries[:n]
		page.NextCursor = encodeCursor(digest, entries[n-1].created, entries[n-1].ID)
	}
	for _, e := range entries {
		item, err := s.loadItem(ctx, nil, p, e.ItemID, false)
		switch {
		case err == nil:
			e.Item = item
		case helm.IsKind(err, helm.KindNotFound):
		default:
			return nil, err
		}
		page.Items = append(page.Items, e.InboxEntry)
	}
	return page, nil
}

func (s *Service) InboxConsume(ctx context.Context, id string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var consumed sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT consumed_at FROM inbox WHERE id = ? AND to_studio = ?`, id, p.StudioID).Scan(&consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("no inbox entry %s", id)
		}
		if err != nil || consumed.Valid {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inbox SET consumed_at = ? WHERE id = ?`, ms(s.now()), id)
		return err
	})
}
