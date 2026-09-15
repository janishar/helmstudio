package conformance

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// ---------------------------------------------------------------- kv (R31b, 07 §3)

func TestKVReadsBackWhatWasWrittenWithANewETagEachTime(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		first, err := c.KV.Put(ctx, "ui", "panel", map[string]any{"width": 320, "open": true}, nil)
		noErr(t, err)
		got, err := c.KV.Get(ctx, "ui", "panel")
		noErr(t, err)
		if got.Doc["width"] != float64(320) || got.Doc["open"] != true || got.ETag != first.ETag || got.NS != "ui" || got.Key != "panel" {
			t.Fatalf("get = %+v; want what was put, etag %s", got, first.ETag)
		}
		second, err := c.KV.Put(ctx, "ui", "panel", map[string]any{"width": 400}, nil)
		noErr(t, err)
		if second.ETag == first.ETag {
			t.Fatal("a write kept the same etag")
		}
		if _, ok := second.Doc["open"]; ok {
			t.Fatal("PUT merged instead of replacing")
		}
	})
}

func TestKVIfMatchRefusesAStaleWriteWith409(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		v1, err := c.KV.Put(ctx, "ui", "k", map[string]any{"n": 1}, nil)
		noErr(t, err)
		_, err = c.KV.Put(ctx, "ui", "k", map[string]any{"n": 2}, &helm.KVPutParams{IfMatch: ptr(v1.ETag)})
		noErr(t, err)
		_, err = c.KV.Put(ctx, "ui", "k", map[string]any{"n": 3}, &helm.KVPutParams{IfMatch: ptr(v1.ETag)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")
		_, err = c.KV.Patch(ctx, "ui", "k", map[string]any{"n": 3}, &helm.KVPatchParams{IfMatch: ptr(`"` + v1.ETag + `"`)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")
		err = c.KV.Delete(ctx, "ui", "k", &helm.KVDeleteParams{IfMatch: ptr(v1.ETag)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")
		_, err = c.KV.Put(ctx, "ui", "absent", map[string]any{}, &helm.KVPutParams{IfMatch: ptr(v1.ETag)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")
		got, err := c.KV.Get(ctx, "ui", "k")
		noErr(t, err)
		if got.Doc["n"] != float64(2) {
			t.Fatalf("a refused write changed the document: %+v", got.Doc)
		}
	})
}

func TestKVPatchIsAJSONMergePatch(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		_, err := c.KV.Put(ctx, "ui", "k", map[string]any{"keep": 1, "drop": 2, "nested": map[string]any{"a": 1, "b": 2}, "list": []any{1, 2}}, nil)
		noErr(t, err)
		got, err := c.KV.Patch(ctx, "ui", "k", map[string]any{"drop": nil, "nested": map[string]any{"b": nil, "c": 3}, "list": []any{9}}, nil)
		noErr(t, err)
		want := `map[keep:1 list:[9] nested:map[a:1 c:3]]`
		if fmt.Sprint(got.Doc) != want {
			t.Fatalf("patched = %v; want %s", got.Doc, want)
		}
		_, err = c.KV.Patch(ctx, "ui", "missing", map[string]any{"a": 1}, nil)
		wantErr(t, err, helm.KindNotFound, "not_found")
	})
}

func TestKVDeleteAndListKeysWithoutValues(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		for _, k := range []string{"b.2", "a.1", "a.3", "c"} {
			_, err := c.KV.Put(ctx, "prefs", k, map[string]any{"v": k}, nil)
			noErr(t, err)
		}
		var keys []string
		var cursor *string
		for {
			page, err := c.KV.List(ctx, "prefs", &helm.KVListParams{Limit: ptr(int64(2)), Cursor: cursor})
			noErr(t, err)
			for _, k := range page.Items {
				if k.Bytes <= 0 || k.UpdatedAt.IsZero() {
					t.Fatalf("key listing lacks size or time: %+v", k)
				}
				keys = append(keys, k.Key)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = page.NextCursor
		}
		if !slices.Equal(keys, []string{"a.1", "a.3", "b.2", "c"}) {
			t.Fatalf("keys = %v; want every key once, in key order", keys)
		}
		page, err := c.KV.List(ctx, "prefs", &helm.KVListParams{Prefix: ptr("a.")})
		noErr(t, err)
		if len(page.Items) != 2 || page.NextCursor != nil {
			t.Fatalf("prefix a. = %+v", page)
		}
		noErr(t, c.KV.Delete(ctx, "prefs", "c", nil))
		_, err = c.KV.Get(ctx, "prefs", "c")
		wantErr(t, err, helm.KindNotFound, "")
		wantErr(t, c.KV.Delete(ctx, "prefs", "c", nil), helm.KindNotFound, "")
	})
}

func TestKVSharedNamespaceNeedsKVShared(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		_, err := e.C(A).KV.Put(ctx, "shared", "prompts", map[string]any{"x": 1}, nil)
		wantErr(t, err, helm.KindForbidden, "capability_required")
		_, err = e.C(B).KV.Put(ctx, "shared", "prompts", map[string]any{"x": 1}, nil)
		noErr(t, err)
		_, err = e.C(A).KV.Get(ctx, "shared", "prompts")
		wantErr(t, err, helm.KindForbidden, "capability_required")
		// A's own namespace called "ui" is not B's.
		_, err = e.C(B).KV.Put(ctx, "ui", "k", map[string]any{"owner": "b"}, nil)
		noErr(t, err)
		_, err = e.C(A).KV.Get(ctx, "ui", "k")
		wantErr(t, err, helm.KindNotFound, "")
	})
}

func TestKVBytesQuotaRefusesWith429AndTheNumbers(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(Q)
		big := strings.Repeat("x", QuotaKVBytes)
		_, err := c.KV.Put(ctx, "ui", "big", map[string]any{"v": big}, nil)
		wantErr(t, err, helm.KindQuotaExceeded, "quota_exceeded")
		var he *helm.Error
		if !asError(err, &he) || he.Details["quota"] != "kv_bytes" || he.Details["limit"] != float64(QuotaKVBytes) {
			t.Fatalf("quota details = %+v", he)
		}
		// Session state counts toward the same quota (Q20).
		_, err = c.Sessions.Create(ctx, helm.SessionCreate{Name: "s", State: map[string]any{"v": big}})
		wantErr(t, err, helm.KindQuotaExceeded, "quota_exceeded")
		me, err := c.Me.Get(ctx)
		noErr(t, err)
		if me.Quota.KVBytes.Limit != QuotaKVBytes || me.Quota.Records.Limit != QuotaRecords {
			t.Fatalf("/me quota = %+v", me.Quota)
		}
	})
}

// ---------------------------------------------------------------- sessions (R31a)

func TestSessionsLifecycle(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		s1, err := c.Sessions.Create(ctx, helm.SessionCreate{Name: "one", State: map[string]any{"seed": 1, "dims": map[string]any{"w": 800, "h": 448}}})
		noErr(t, err)
		if s1.OpenedAt != nil || s1.StudioID != A || s1.ETag == "" {
			t.Fatalf("created = %+v", s1)
		}
		_, err = c.Sessions.Create(ctx, helm.SessionCreate{Name: "one"})
		wantErr(t, err, helm.KindConflict, "name_taken")

		patched, err := c.Sessions.Update(ctx, s1.ID, map[string]any{"state": map[string]any{"dims": map[string]any{"h": nil, "fps": 24}}}, &helm.SessionsUpdateParams{IfMatch: ptr(s1.ETag)})
		noErr(t, err)
		if fmt.Sprint(patched.State) != "map[dims:map[fps:24 w:800] seed:1]" || patched.ETag == s1.ETag {
			t.Fatalf("merge-patched state = %v", patched.State)
		}
		_, err = c.Sessions.Update(ctx, s1.ID, map[string]any{"name": "renamed"}, &helm.SessionsUpdateParams{IfMatch: ptr(s1.ETag)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")

		noErr(t, c.Sessions.Activate(ctx, s1.ID))
		activated, err := c.Sessions.Get(ctx, s1.ID)
		noErr(t, err)
		if activated.OpenedAt == nil || activated.ETag != patched.ETag {
			t.Fatalf("activate = %+v; want opened_at set and the etag unchanged", activated)
		}

		dup, err := c.Sessions.Duplicate(ctx, s1.ID, helm.SessionDuplicate{Name: "copy"})
		noErr(t, err)
		if dup.OpenedAt != nil || fmt.Sprint(dup.State) != fmt.Sprint(patched.State) || dup.ID == s1.ID {
			t.Fatalf("duplicate = %+v", dup)
		}
		time.Sleep(2 * time.Millisecond)
		_, err = c.Sessions.Create(ctx, helm.SessionCreate{Name: "three"})
		noErr(t, err)

		page, err := c.Sessions.List(ctx, nil)
		noErr(t, err)
		var names []string
		for _, s := range page.Items {
			names = append(names, s.Name)
		}
		// Opened first, then never-opened newest first.
		if !slices.Equal(names, []string{"one", "three", "copy"}) {
			t.Fatalf("order = %v; want [one three copy]", names)
		}

		noErr(t, c.Sessions.Delete(ctx, s1.ID, nil))
		_, err = c.Sessions.Get(ctx, s1.ID)
		wantErr(t, err, helm.KindNotFound, "")
		_, err = c.Sessions.Create(ctx, helm.SessionCreate{Name: "one"})
		noErr(t, err) // a deleted session's name is free again
		_, err = e.C(B).Sessions.Get(ctx, dup.ID)
		wantErr(t, err, helm.KindNotFound, "")
	})
}

// ---------------------------------------------------------------- records (R32, 06 §6, Q19)

func seedTakes(t *testing.T, c *helm.Client) []*helm.Record {
	t.Helper()
	docs := []map[string]any{
		{"seed": 42, "state": "done", "tags": []any{"cafe", "dusk"}, "prompt": "a cafe window", "url": "http://x/y"},
		{"seed": "42", "state": "done", "prompt": "a street"},
		{"seed": 7, "state": "running", "tags": []any{"cafe"}, "flag": true},
		{"seed": 99, "state": "done", "prompt": "Cafe at noon", "flag": false},
		{"state": "failed"},
	}
	var out []*helm.Record
	for _, d := range docs {
		r, err := c.Records.Insert(ctx, "takes", d)
		noErr(t, err)
		out = append(out, r)
		time.Sleep(2 * time.Millisecond)
	}
	return out
}

func ids(rs []helm.Record) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestRecordsFilterLanguage(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		r := seedTakes(t, c)
		query := func(where ...string) []string {
			t.Helper()
			page, err := c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Where: where, Order: ptr("created_at:asc")})
			noErr(t, err)
			return ids(page.Items)
		}
		cases := []struct {
			where []string
			want  []*helm.Record
		}{
			{[]string{"seed:eq:42"}, []*helm.Record{r[0]}},   // a number, not the string "42"
			{[]string{`seed:eq:"42"`}, []*helm.Record{r[1]}}, // a quoted value is a string
			{[]string{"seed:gt:10"}, []*helm.Record{r[0], r[3]}},
			{[]string{"seed:lte:42", "state:eq:done"}, []*helm.Record{r[0]}},
			{[]string{"state:in:[\"running\",\"failed\"]"}, []*helm.Record{r[2], r[4]}},
			{[]string{"tags:contains:cafe"}, []*helm.Record{r[0], r[2]}},
			{[]string{"prompt:contains:cafe"}, []*helm.Record{r[0]}}, // case-sensitive
			{[]string{"flag:exists:true"}, []*helm.Record{r[2], r[3]}},
			{[]string{"seed:exists:false"}, []*helm.Record{r[4]}},
			{[]string{"flag:eq:true"}, []*helm.Record{r[2]}},
			{[]string{"state:ne:done"}, []*helm.Record{r[2], r[4]}},
			{[]string{"url:eq:http://x/y"}, []*helm.Record{r[0]}}, // a value may contain ':'
			{[]string{"created_at:gte:" + r[3].CreatedAt.Format(time.RFC3339Nano)}, []*helm.Record{r[3], r[4]}},
			{[]string{"id:eq:" + r[1].ID}, []*helm.Record{r[1]}},
		}
		for _, tc := range cases {
			if got, want := query(tc.where...), idsOf(tc.want); !slices.Equal(got, want) {
				t.Errorf("where %v = %v; want %v", tc.where, got, want)
			}
		}
		for _, bad := range [][]string{
			{"seed:like:4"}, {"seed"}, {"se-ed:eq:1"}, {"a.b:eq:1"}, {"seed:in:4"},
			{"created_at:gt:yesterday"}, {"created_at:exists:true"}, {"flag:exists:maybe"},
			{"a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1", "a:eq:1"},
		} {
			_, err := c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Where: bad})
			if helm.KindOf(err) != helm.KindInvalid {
				t.Errorf("where %v: %v; want Invalid", bad, err)
			}
		}
	})
}

func idsOf(rs []*helm.Record) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestRecordsOrderAndCursorPagination(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		r := seedTakes(t, c)
		// seed desc: numbers before text in SQLite order is not asserted;
		// what is asserted is nulls last and every record exactly once.
		collect := func(order string, limit int64, where []string) []string {
			var got []string
			var cursor *string
			for i := 0; i < 20; i++ {
				page, err := c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Order: ptr(order), Limit: ptr(limit), Cursor: cursor, Where: where})
				noErr(t, err)
				got = append(got, ids(page.Items)...)
				if page.NextCursor == nil {
					return got
				}
				cursor = page.NextCursor
			}
			t.Fatal("pagination did not end")
			return nil
		}
		all := collect("created_at:desc", 50, nil)
		if want := []string{r[4].ID, r[3].ID, r[2].ID, r[1].ID, r[0].ID}; !slices.Equal(all, want) {
			t.Fatalf("default order = %v; want newest first %v", all, want)
		}
		if paged := collect("created_at:desc", 2, nil); !slices.Equal(paged, all) {
			t.Fatalf("paged = %v; want %v", paged, all)
		}
		for _, order := range []string{"seed:asc", "seed:desc", "flag:asc", "state:desc"} {
			whole := collect(order, 50, nil)
			paged := collect(order, 1, nil)
			if !slices.Equal(whole, paged) || len(whole) != 5 {
				t.Errorf("%s: paged %v, whole %v", order, paged, whole)
			}
		}
		if seedAsc := collect("seed:asc", 50, nil); seedAsc[len(seedAsc)-1] != r[4].ID {
			t.Errorf("seed:asc = %v; the record without a seed sorts last", seedAsc)
		}
		if seedDesc := collect("seed:desc", 50, nil); seedDesc[len(seedDesc)-1] != r[4].ID {
			t.Errorf("seed:desc = %v; the record without a seed sorts last", seedDesc)
		}
		page, err := c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Limit: ptr(int64(1))})
		noErr(t, err)
		_, err = c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Limit: ptr(int64(1)), Cursor: page.NextCursor, Where: []string{"state:eq:done"}})
		wantErr(t, err, helm.KindInvalid, "bad_cursor")
		_, err = c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Cursor: ptr("not-a-cursor")})
		wantErr(t, err, helm.KindInvalid, "bad_cursor")
		_, err = c.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Order: ptr("seed:sideways")})
		wantErr(t, err, helm.KindInvalid, "bad_filter")
	})
}

func TestRecordsWritesETagsSoftDeleteAndIsolation(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		rec, err := c.Records.Insert(ctx, "takes", map[string]any{"seed": 1, "meta": map[string]any{"a": 1}})
		noErr(t, err)
		if rec.ETag == "" || rec.Collection != "takes" {
			t.Fatalf("insert = %+v", rec)
		}
		got, err := c.Records.Get(ctx, "takes", rec.ID)
		noErr(t, err)
		if got.ETag != rec.ETag {
			t.Fatalf("get etag %s; want %s", got.ETag, rec.ETag)
		}
		replaced, err := c.Records.Replace(ctx, "takes", rec.ID, map[string]any{"seed": 2}, &helm.RecordsReplaceParams{IfMatch: ptr(rec.ETag)})
		noErr(t, err)
		if fmt.Sprint(replaced.Doc) != "map[seed:2]" || replaced.ETag == rec.ETag {
			t.Fatalf("replace = %+v", replaced)
		}
		_, err = c.Records.Patch(ctx, "takes", rec.ID, map[string]any{"seed": 3}, &helm.RecordsPatchParams{IfMatch: ptr(rec.ETag)})
		wantErr(t, err, helm.KindConflict, "etag_mismatch")
		patched, err := c.Records.Patch(ctx, "takes", rec.ID, map[string]any{"note": "x"}, nil)
		noErr(t, err)
		if fmt.Sprint(patched.Doc) != "map[note:x seed:2]" {
			t.Fatalf("patch = %v", patched.Doc)
		}
		_, err = c.Records.Get(ctx, "presets", rec.ID)
		wantErr(t, err, helm.KindNotFound, "")
		_, err = e.C(B).Records.Get(ctx, "takes", rec.ID)
		wantErr(t, err, helm.KindNotFound, "")
		page, err := e.C(B).Records.Query(ctx, "takes", nil)
		noErr(t, err)
		if len(page.Items) != 0 {
			t.Fatalf("studio b sees studio a's records: %+v", page.Items)
		}

		noErr(t, c.Records.Delete(ctx, "takes", rec.ID, nil))
		_, err = c.Records.Get(ctx, "takes", rec.ID)
		wantErr(t, err, helm.KindNotFound, "")
		page, err = c.Records.Query(ctx, "takes", nil)
		noErr(t, err)
		if len(page.Items) != 0 {
			t.Fatalf("a deleted record is still listed: %+v", page.Items)
		}
	})
}

func TestRecordsQuotaCountsLiveRows(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(Q)
		var last *helm.Record
		for i := 0; i < QuotaRecords; i++ {
			r, err := c.Records.Insert(ctx, "x", map[string]any{"i": i})
			noErr(t, err)
			last = r
		}
		_, err := c.Records.Insert(ctx, "y", map[string]any{})
		wantErr(t, err, helm.KindQuotaExceeded, "quota_exceeded")
		var he *helm.Error
		if !asError(err, &he) || he.Details["quota"] != "records" || he.Details["used"] != float64(QuotaRecords) {
			t.Fatalf("details = %+v", he)
		}
		noErr(t, c.Records.Delete(ctx, "x", last.ID, nil))
		_, err = c.Records.Insert(ctx, "y", map[string]any{})
		noErr(t, err)
	})
}
