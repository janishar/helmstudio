package studio

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"
)

// Search in lists (API 1.92; contracts/README.md, Search in lists): q's words, a newest-first keyset cursor, a limit.

// Page is a list's query: After the opaque next_cursor of the page before, Limit the page size, Words q split on
// whitespace.
type Page struct {
	After string
	Limit int
	Words []string
}

// ParsePage reads cursor, limit (1-200; def when absent) and q (at most 200 characters); the second result is the 422
// detail.
func ParsePage(query url.Values, def int) (Page, string) {
	page := Page{After: query.Get("cursor"), Limit: def}
	if text := query.Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 200 {
			return page, "limit must be a whole number from 1 to 200"
		}
		page.Limit = n
	}
	q := query.Get("q")
	if utf8.RuneCountInString(q) > 200 {
		return page, "q must be at most 200 characters"
	}
	page.Words = strings.Fields(q)
	if page.After != "" {
		if _, err := base64.RawURLEncoding.DecodeString(page.After); err != nil {
			return page, "cursor is not a next_cursor"
		}
	}
	return page, ""
}

// sqlArgs are a query's arguments; add appends one and returns its placeholder.
type sqlArgs []any

func (a *sqlArgs) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

// matchSQL is " AND (…)" once per word: the word occurs, case-insensitively, in one of texts (SQL text expressions).
// ponytail: ILIKE over one owner's rows (reached by the owner indexes); a pg_trgm GIN index if a list grows to tens of
// thousands.
func matchSQL(words []string, args *sqlArgs, texts ...string) string {
	var out strings.Builder
	for _, word := range words {
		p := args.add("%" + likeEscape(word) + "%")
		ors := make([]string, len(texts))
		for i, text := range texts {
			ors[i] = text + ` ILIKE ` + p
		}
		out.WriteString(` AND (` + strings.Join(ors, ` OR `) + `)`)
	}
	return out.String()
}

// jsonTexts is the text of every string value anywhere in the jsonb column col (keys left out), for matchSQL.
func jsonTexts(col string) string {
	return `jsonb_path_query_array(` + col + `, 'strict $.** ? (@.type() == "string")')::text`
}

// keysetSQL is " AND … ORDER BY at DESC, id DESC LIMIT …": the rows after page.After (base64 "<RFC3339Nano>|<id>" of
// the last row, as batches' cursors), one more than the page so pageOf knows there is a next one. A malformed cursor
// is 422.
func keysetSQL(page Page, args *sqlArgs, at, id string) (string, error) {
	out := ""
	if page.After != "" {
		raw, _ := base64.RawURLEncoding.DecodeString(page.After)
		stamp, key, _ := strings.Cut(string(raw), "|")
		when, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || !utility.IsUUID(key) {
			return "", network.NewUnprocessableError("cursor is not a next_cursor", nil)
		}
		out = ` AND (` + at + `, ` + id + `) < (` + args.add(when) + `, ` + args.add(key) + `::uuid)`
	}
	return out + ` ORDER BY ` + at + ` DESC, ` + id + ` DESC LIMIT ` + args.add(page.Limit+1), nil
}

// pageOf keeps limit rows and, when there were more, returns the next_cursor of the last one kept.
func pageOf[T any](rows []T, limit int, key func(T) (time.Time, string)) ([]T, *string) {
	if len(rows) <= limit {
		return rows, nil
	}
	rows = rows[:limit]
	at, id := key(rows[limit-1])
	next := base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
	return rows, &next
}
