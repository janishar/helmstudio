package studioapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Records: a per-studio document store queried through a closed filter
// language, never SQL (R32, 06 §6, Q19 and first review #10). Every value is
// bound; field names are checked against a pattern before they reach a JSON
// path, and only then written into SQL.

const maxClauses = 8

type clause struct {
	sql  string
	args []any
}

// columnFields are the fields a filter reads from a column, not the document.
var columnFields = map[string]string{"id": "id", "created_at": "created_at", "updated_at": "updated_at"}

// fieldExpr is the SQL for a filterable field. Document fields use a literal
// JSON path so the manifest's expression indexes can serve the query.
func fieldExpr(field string) (expr string, column bool, err error) {
	if c, ok := columnFields[field]; ok {
		return c, true, nil
	}
	if !fieldName.MatchString(field) {
		return "", false, badFilter("%q is not a field name; fields are top-level keys matching %s", field, fieldName)
	}
	return "json_extract(doc, '$." + field + "')", false, nil
}

// filterValue parses a value: JSON when it is valid JSON, a literal string
// otherwise.
func filterValue(raw string) any {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() {
		return normalizeNumbers(v)
	}
	return raw
}

// sqlValue turns a parsed JSON value into what json_extract compares with:
// booleans are 1 and 0 there.
func sqlValue(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return 1
		}
		return 0
	}
	return v
}

func timeValue(field, raw string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return 0, badFilter("%s takes an RFC 3339 time, not %q", field, raw)
	}
	return ms(t), nil
}

// typeGuard restricts a document field to the JSON type of v.
func typeGuard(field string, column bool, v any) string {
	if column {
		return ""
	}
	path := "json_type(doc, '$." + field + "')"
	switch v.(type) {
	case string:
		return path + " = 'text' AND "
	case int64, float64:
		return path + " IN ('integer', 'real') AND "
	}
	return path + " IN ('true', 'false') AND "
}

func parseClause(w string) (clause, error) {
	field, rest, ok1 := strings.Cut(w, ":")
	op, raw, ok2 := strings.Cut(rest, ":")
	if !ok1 || !ok2 {
		return clause{}, badFilter("%q is not field:op:value", w)
	}
	expr, column, err := fieldExpr(field)
	if err != nil {
		return clause{}, err
	}
	isTime := field == "created_at" || field == "updated_at"
	scalar := func() (any, error) {
		if isTime {
			return timeValue(field, raw)
		}
		if column {
			return raw, nil
		}
		v := filterValue(raw)
		switch v.(type) {
		case map[string]any, []any:
			return nil, badFilter("%s %s takes a single value, not %s", field, op, raw)
		}
		return sqlValue(v), nil
	}
	cmp := map[string]string{"lt": "<", "lte": "<=", "gt": ">", "gte": ">="}
	switch op {
	case "eq", "ne":
		v, err := scalar()
		if err != nil {
			return clause{}, err
		}
		if v == nil {
			if op == "eq" {
				return clause{sql: expr + " IS NULL"}, nil
			}
			return clause{sql: expr + " IS NOT NULL"}, nil
		}
		if op == "eq" {
			return clause{sql: expr + " = ?", args: []any{v}}, nil
		}
		return clause{sql: expr + " IS NOT ?", args: []any{v}}, nil
	case "lt", "lte", "gt", "gte":
		v, err := scalar()
		if err != nil {
			return clause{}, err
		}
		if v == nil {
			return clause{}, badFilter("%s %s cannot compare with null", field, op)
		}
		// A comparison matches only stored values of the filter value's JSON
		// type: SQLite orders every string above every number, so "42" would
		// otherwise be greater than 10.
		return clause{sql: typeGuard(field, column, v) + expr + " " + cmp[op] + " ?", args: []any{v}}, nil
	case "in":
		var list []any
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&list); err != nil || len(list) == 0 {
			return clause{}, badFilter("%s in takes a non-empty JSON array, not %q", field, raw)
		}
		var args []any
		for _, x := range normalizeNumbers(list).([]any) {
			switch t := x.(type) {
			case map[string]any, []any, nil:
				return clause{}, badFilter("%s in takes an array of strings, numbers or booleans", field)
			case string:
				if isTime {
					n, err := timeValue(field, t)
					if err != nil {
						return clause{}, err
					}
					args = append(args, n)
					continue
				}
			}
			if isTime {
				return clause{}, badFilter("%s takes RFC 3339 times", field)
			}
			args = append(args, sqlValue(x))
		}
		return clause{sql: expr + " IN (?" + strings.Repeat(",?", len(args)-1) + ")", args: args}, nil
	case "contains":
		if column {
			return clause{}, badFilter("contains does not apply to %s", field)
		}
		v := filterValue(raw)
		path := "'$." + field + "'"
		return clause{
			sql:  "CASE json_type(doc, " + path + ") WHEN 'array' THEN EXISTS (SELECT 1 FROM json_each(doc, " + path + ") WHERE json_each.value = ?) WHEN 'text' THEN instr(" + expr + ", ?) > 0 ELSE 0 END",
			args: []any{sqlValue(v), fmt.Sprint(v)},
		}, nil
	case "exists":
		if column {
			return clause{}, badFilter("exists does not apply to %s", field)
		}
		switch raw {
		case "true":
			return clause{sql: "json_type(doc, '$." + field + "') IS NOT NULL"}, nil
		case "false":
			return clause{sql: "json_type(doc, '$." + field + "') IS NULL"}, nil
		}
		return clause{}, badFilter("%s exists takes true or false", field)
	}
	return clause{}, badFilter("%q is not an operator; use eq ne lt lte gt gte in contains exists", op)
}

// recordOrder is the one sort key a query orders by.
type recordOrder struct {
	field string
	expr  string
	desc  bool
	time  bool
}

func parseOrder(raw *string) (recordOrder, error) {
	spec := "created_at:desc"
	if raw != nil && *raw != "" {
		spec = *raw
	}
	field, dir, ok := strings.Cut(spec, ":")
	if !ok || (dir != "asc" && dir != "desc") {
		return recordOrder{}, badFilter("order is field:asc or field:desc, not %q", spec)
	}
	expr, _, err := fieldExpr(field)
	if err != nil {
		return recordOrder{}, err
	}
	return recordOrder{field: field, expr: expr, desc: dir == "desc", time: field == "created_at" || field == "updated_at"}, nil
}

func (s *Service) ensureIndexes(ctx context.Context, studioID string) {
	if s.cfg.Studios == nil {
		return
	}
	m, ok := s.cfg.Studios.Manifest(studioID)
	if !ok || m.Storage == nil {
		return
	}
	s.indexOnce(studioID, func() {
		err := s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			for _, c := range m.Storage.Collections {
				for _, f := range c.Index {
					if !fieldName.MatchString(f) || !fieldName.MatchString(c.Name) {
						continue
					}
					// Values in a CREATE INDEX cannot be bound; the studio id and
					// collection come from a validated manifest and are quoted.
					name := "idx_rec_" + sqlIdent(studioID, c.Name, f)
					q := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON records(json_extract(doc, '$.%s')) WHERE studio_id = '%s' AND collection = '%s' AND deleted_at IS NULL`,
						name, f, strings.ReplaceAll(studioID, "'", "''"), c.Name)
					if _, err := tx.ExecContext(ctx, q); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			s.cfg.Logf("studioapi: creating the record indexes %s declares: %v", studioID, err)
		}
	})
}

var indexed sync.Map // studio id → struct{}, per process

func (s *Service) indexOnce(studioID string, fn func()) {
	if _, done := indexed.LoadOrStore(fmt.Sprintf("%p/%s", s, studioID), struct{}{}); !done {
		fn()
	}
}

const recordCols = `id, collection, doc, etag, created_at, updated_at`

func scanRecord(sc interface{ Scan(...any) error }, extra ...any) (*helm.Record, error) {
	var r helm.Record
	var doc string
	var created, updated sql.NullInt64
	if err := sc.Scan(append([]any{&r.ID, &r.Collection, &doc, &r.ETag, &created, &updated}, extra...)...); err != nil {
		return nil, err
	}
	d, err := decodeDoc(doc)
	if err != nil {
		return nil, err
	}
	r.Doc, r.CreatedAt, r.UpdatedAt = d, fromMS(created.Int64), fromMS(updated.Int64)
	return &r, nil
}

func (s *Service) RecordsQuery(ctx context.Context, collection string, params helm.RecordsQueryParams) (*helm.RecordPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if len(params.Where) > maxClauses {
		return nil, badFilter("at most %d where clauses", maxClauses)
	}
	s.ensureIndexes(ctx, p.StudioID)
	var clauses []clause
	for _, w := range params.Where {
		c, err := parseClause(w)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, c)
	}
	ord, err := parseOrder(params.Order)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("records", p.StudioID, collection, params.Where, ord)
	after, err := decodeCursor(params.Cursor, digest, 3)
	if err != nil {
		return nil, err
	}
	sortKey := ord.expr
	q := `SELECT ` + recordCols + `, ` + sortKey + ` IS NULL, ` + sortKey + ` FROM records WHERE studio_id = ? AND collection = ? AND deleted_at IS NULL`
	args := []any{p.StudioID, collection}
	for _, c := range clauses {
		q += " AND (" + c.sql + ")"
		args = append(args, c.args...)
	}
	// Keyset over (value is null, value, id): nulls sort last either way.
	cmp, dir := ">", "ASC"
	if ord.desc {
		cmp, dir = "<", "DESC"
	}
	if after != nil {
		isNull, ok := cursorInt(after[0])
		id, ok2 := cursorString(after[2])
		if !ok || !ok2 {
			return nil, badCursor()
		}
		if isNull == 1 {
			q += ` AND ` + sortKey + ` IS NULL AND id ` + cmp + ` ?`
			args = append(args, id)
		} else {
			q += ` AND (` + sortKey + ` IS NULL OR ` + sortKey + ` ` + cmp + ` ? OR (` + sortKey + ` = ? AND id ` + cmp + ` ?))`
			args = append(args, after[1], after[1], id)
		}
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY ` + sortKey + ` IS NULL, ` + sortKey + ` ` + dir + `, id ` + dir + ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", collection, err)
	}
	defer rows.Close()
	page := &helm.RecordPage{Items: []helm.Record{}}
	var last []any
	for rows.Next() {
		var isNull int64
		var val any
		r, err := scanRecord(rows, &isNull, &val)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			page.NextCursor = encodeCursor(digest, last...)
			break
		}
		page.Items = append(page.Items, *r)
		last = []any{isNull, val, r.ID}
	}
	return page, rows.Err()
}

func (s *Service) RecordsInsert(ctx context.Context, collection string, body map[string]any) (*helm.Record, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	s.ensureIndexes(ctx, p.StudioID)
	doc, err := encodeDoc(body)
	if err != nil {
		return nil, err
	}
	var out *helm.Record
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		limit, _ := s.quotas(p.StudioID)
		used, err := recordsUsed(ctx, tx, p.StudioID)
		if err != nil {
			return err
		}
		if used >= limit {
			return quotaExceeded("records", limit, used)
		}
		id, now := s.newID(), ms(s.now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO records (id, studio_id, collection, doc, etag, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, p.StudioID, collection, doc, newETag(), now, now); err != nil {
			return err
		}
		out, err = readRecord(ctx, tx, p.StudioID, collection, id)
		return err
	})
	return out, err
}

func readRecord(ctx context.Context, q querier, studioID, collection, id string) (*helm.Record, error) {
	r, err := scanRecord(q.QueryRowContext(ctx, `SELECT `+recordCols+` FROM records WHERE id = ? AND studio_id = ? AND collection = ? AND deleted_at IS NULL`, id, studioID, collection))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no record %s in %s", id, collection)
	}
	return r, err
}

func (s *Service) RecordsGet(ctx context.Context, collection, id string) (*helm.Record, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return readRecord(ctx, s.st.Reader(), p.StudioID, collection, id)
}

func (s *Service) recordWrite(ctx context.Context, collection, id string, ifMatch *string, change func(map[string]any) map[string]any) (*helm.Record, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Record
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := readRecord(ctx, tx, p.StudioID, collection, id)
		if err != nil {
			return err
		}
		if ifMatch != nil && unquoteETag(*ifMatch) != cur.ETag {
			return etagMismatch("record " + id)
		}
		doc, err := encodeDoc(change(cur.Doc))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE records SET doc = ?, etag = ?, updated_at = ? WHERE id = ?`, doc, newETag(), ms(s.now()), id); err != nil {
			return err
		}
		out, err = readRecord(ctx, tx, p.StudioID, collection, id)
		return err
	})
	return out, err
}

func (s *Service) RecordsReplace(ctx context.Context, collection, id string, body map[string]any, params helm.RecordsReplaceParams) (*helm.Record, error) {
	return s.recordWrite(ctx, collection, id, params.IfMatch, func(map[string]any) map[string]any { return body })
}

func (s *Service) RecordsPatch(ctx context.Context, collection, id string, body map[string]any, params helm.RecordsPatchParams) (*helm.Record, error) {
	return s.recordWrite(ctx, collection, id, params.IfMatch, func(cur map[string]any) map[string]any { return mergePatch(cur, body) })
}

func (s *Service) RecordsDelete(ctx context.Context, collection, id string, params helm.RecordsDeleteParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := readRecord(ctx, tx, p.StudioID, collection, id)
		if err != nil {
			return err
		}
		if params.IfMatch != nil && unquoteETag(*params.IfMatch) != cur.ETag {
			return etagMismatch("record " + id)
		}
		_, err = tx.ExecContext(ctx, `UPDATE records SET deleted_at = ?, updated_at = ? WHERE id = ?`, ms(s.now()), ms(s.now()), id)
		return err
	})
}
