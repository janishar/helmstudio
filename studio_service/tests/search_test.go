package tests

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/common"
)

// titles is the list's field of every item of out[key].
func titles(out map[string]any, key, field string) []string {
	var got []string
	for _, item := range out[key].([]any) {
		got = append(got, item.(map[string]any)[field].(string))
	}
	return got
}

// TestSearchInLists (API 1.92): q's words in any order and case, another user's items never, cursor pages that join
// up, the old first page without q or limit, and the 422s.
func TestSearchInLists(t *testing.T) {
	e := setup(t)
	for _, title := range []string{"Mara, rooftop", "Snow owl", "mara on the night bus", "Fox at dusk"} {
		e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": title}, 201)
	}
	e.call(userB, "POST", "/v1/studio/sessions", map[string]any{"title": "Mara rooftop secret"}, 201)

	all := e.call(userA, "GET", "/v1/studio/sessions", nil, 200)
	if got := titles(all, "sessions", "title"); len(got) != 4 || got[0] != "Fox at dusk" || all["next_cursor"] != nil {
		t.Fatalf("first page = %v, next %v", got, all["next_cursor"])
	}
	hits := e.call(userA, "GET", "/v1/studio/sessions?q="+url.QueryEscape("ROOFTOP mara"), nil, 200)
	if got := titles(hits, "sessions", "title"); len(got) != 1 || got[0] != "Mara, rooftop" {
		t.Fatalf("q = %v", got)
	}
	if got := titles(e.call(userA, "GET", "/v1/studio/sessions?q=mara", nil, 200), "sessions", "title"); len(got) != 2 {
		t.Fatalf("q mara = %v", got)
	}
	if got := e.call(userA, "GET", "/v1/studio/sessions?q=secret", nil, 200)["sessions"].([]any); len(got) != 0 {
		t.Fatalf("another user's session matched: %v", got)
	}

	// pages of 3 then 1, newest first, joined by the cursor
	first := e.call(userA, "GET", "/v1/studio/sessions?limit=3", nil, 200)
	next, _ := first["next_cursor"].(string)
	rest := e.call(userA, "GET", "/v1/studio/sessions?limit=3&cursor="+next, nil, 200)
	got := append(titles(first, "sessions", "title"), titles(rest, "sessions", "title")...)
	if want := titles(all, "sessions", "title"); next == "" || rest["next_cursor"] != nil || len(got) != 4 || got[3] != want[3] {
		t.Fatalf("pages = %v, want %v", got, want)
	}

	// the same rule on boards and sequences
	for _, name := range []string{"The Last Tide", "Neon rain teaser"} {
		e.call(userA, "POST", "/v1/studio/magic/boards", map[string]any{"name": name}, 201)
	}
	if got := titles(e.call(userA, "GET", "/v1/studio/magic/boards?q=tide", nil, 200), "boards", "name"); len(got) != 1 || got[0] != "The Last Tide" {
		t.Fatalf("boards q = %v", got)
	}
	if got := e.call(userB, "GET", "/v1/studio/magic/boards?q=tide", nil, 200)["boards"].([]any); len(got) != 0 {
		t.Fatalf("another user's board matched: %v", got)
	}

	for path, detail := range map[string]string{
		"/v1/studio/sessions?cursor=bm9wZQ":                  "cursor is not a next_cursor",
		"/v1/studio/timelines?limit=201":                     "limit must be a whole number from 1 to 200",
		"/v1/studio/pipelines?q=" + strings.Repeat("x", 201): "q must be at most 200 characters",
		"/v1/studio/batches?state=nope":                      "state must be one of queued, running, waiting, paused, done, failed, stopped",
		"/v1/studio/assistant/threads?cursor=%25%25":         "cursor is not a next_cursor",
	} {
		if out := e.call(userA, "GET", path, nil, 422); out["detail"] != detail {
			t.Fatalf("%s: %v", path, out["detail"])
		}
	}
	if out := e.call(userA, "GET", "/v1/studio/batches?state=running&state=done&q=x", nil, 200); len(out["batches"].([]any)) != 0 {
		t.Fatalf("batches = %v", out)
	}

	// chats (their titles come from a first message: written here as the projector would)
	for user, title := range map[string]string{userA: "Rainy London still of @mara", userB: "London secret"} {
		err := common.WithUser(context.Background(), e.pool, common.Caller{ID: user}, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `INSERT INTO assistant_threads (id, owner_id, title) VALUES ($1, $2, $3)`,
				utility.NewID(), user, title)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := titles(e.call(userA, "GET", "/v1/studio/assistant/threads?q=london", nil, 200), "threads", "title"); len(got) != 1 || got[0] != "Rainy London still of @mara" {
		t.Fatalf("threads q = %v", got)
	}

	// state=waiting is the shown status: a running batch whose every unended row waits on a gate (review fix)
	waiting := utility.NewID()
	e.exec(`INSERT INTO batches (id, owner_id, title, workflow_id, workflow_title, snapshot, inputs, status)
		VALUES ($1, $2, 'Gate pick', 'wf', 'WF', '{}', '{}', 'running')`, waiting, userA)
	e.exec(`INSERT INTO batch_rows (batch_id, n, owner_id, inputs, status) VALUES ($1, 1, $2, '{}', 'waiting'),
		($1, 2, $2, '{}', 'succeeded')`, waiting, userA)
	if got := titles(e.call(userA, "GET", "/v1/studio/batches?state=waiting", nil, 200), "batches", "title"); len(got) != 1 || got[0] != "Gate pick" {
		t.Fatalf("state=waiting = %v", got)
	}
	if got := e.call(userA, "GET", "/v1/studio/batches?state=running", nil, 200)["batches"].([]any); len(got) != 0 {
		t.Fatalf("a waiting batch listed as running: %v", got)
	}

	// jsonTexts (search.go's expression): only string values, at any depth, never keys, quotes or commas
	var fox, comma bool
	err := e.pool.QueryRow(context.Background(), `WITH t AS (SELECT (SELECT string_agg(v #>> '{}', E'\n')
		FROM jsonb_path_query('{"prompt.text": "Fox \"den\"", "n": 3, "refs": [{"take": "Snow"}]}'::jsonb,
		'strict $.** ? (@.type() == "string")') v) AS x) SELECT x ILIKE '%fox "den"%' AND x ILIKE '%snow%'
		AND x NOT ILIKE '%prompt%', x ILIKE '%,%' OR x ILIKE '%\\%' FROM t`).Scan(&fox, &comma)
	if err != nil || !fox || comma {
		t.Fatalf("json texts: words %v, punctuation %v, %v", fox, comma, err)
	}
}
