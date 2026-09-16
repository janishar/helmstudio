package conformance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/store"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
	"github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded"
)

// ---------------------------------------------------------------- jobs (R40, Q24, first review #2)

func TestTaskJobsAreReportedByTheStudio(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		job, err := c.Jobs.Create(ctx, helm.TaskCreate{SubjectKind: ptr("take"), ProgressDen: ptr(int64(12))})
		noErr(t, err)
		if job.Kind != helm.JobKindTask || job.State != helm.JobStateRunning || job.StartedAt == nil || *job.StudioID != A {
			t.Fatalf("created = %+v", job)
		}
		_, err = c.Jobs.Update(ctx, job.ID, map[string]any{"progress_num": 13})
		wantErr(t, err, helm.KindInvalid, "")
		mid, err := c.Jobs.Update(ctx, job.ID, map[string]any{"progress_num": 6})
		noErr(t, err)
		if mid.ProgressNum != 6 || mid.ProgressDen != 12 {
			t.Fatalf("progress = %d/%d", mid.ProgressNum, mid.ProgressDen)
		}
		noErr(t, c.Jobs.AppendLog(ctx, job.ID, helm.LogAppend{Lines: []string{"step 1 of 12", "step 2 of 12"}}))

		noErr(t, c.Jobs.Cancel(ctx, job.ID))
		cancelled, err := c.Jobs.Get(ctx, job.ID)
		noErr(t, err)
		if cancelled.CancelRequestedAt == nil || cancelled.State != helm.JobStateRunning {
			t.Fatalf("after cancel = %+v; want a request recorded and the job still running until the studio ends it", cancelled)
		}
		done, err := c.Jobs.Update(ctx, job.ID, map[string]any{"state": "cancelled", "last_error": map[string]any{"code": "stopped", "message": "stopped at step 6"}})
		noErr(t, err)
		if done.FinishedAt == nil || done.LastError == nil || done.LastError.Code != "stopped" {
			t.Fatalf("finished = %+v", done)
		}
		_, err = c.Jobs.Update(ctx, job.ID, map[string]any{"progress_num": 7})
		wantErr(t, err, helm.KindConflict, "job_finished")
		wantErr(t, c.Jobs.Cancel(ctx, job.ID), helm.KindConflict, "job_finished")
		wantErr(t, c.Jobs.AppendLog(ctx, job.ID, helm.LogAppend{Lines: []string{"late"}}), helm.KindConflict, "job_finished")

		stream, err := c.Jobs.Logs(ctx, job.ID)
		noErr(t, err)
		defer stream.Close()
		var lines []string
		var ended bool
		for !ended {
			ev, err := stream.Next()
			noErr(t, err)
			switch ev.Name {
			case "line":
				var l helm.JobLogLine
				noErr(t, ev.Decode(&l))
				lines = append(lines, l.Text)
			case "end":
				var end helm.JobLogEnd
				noErr(t, ev.Decode(&end))
				ended = end.State == helm.JobStateCancelled
			}
		}
		if strings.Join(lines, "|") != "step 1 of 12|step 2 of 12" {
			t.Fatalf("log lines = %q", lines)
		}

		_, err = e.C(B).Jobs.Get(ctx, job.ID)
		wantErr(t, err, helm.KindNotFound, "")
		page, err := e.C(B).Jobs.List(ctx, nil)
		noErr(t, err)
		if len(page.Items) != 0 {
			t.Fatalf("studio b lists studio a's jobs: %+v", page.Items)
		}
	})
}

func TestAStudioSeesItsInstallJobButCannotCancelIt(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		id := store.NewID(time.Now())
		err := e.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, progress_num, progress_den, created_at, started_at) VALUES (?, 'install', ?, 'running', 1, 4, ?, ?)`,
				id, A, time.Now().UnixMilli(), time.Now().UnixMilli())
			return err
		})
		noErr(t, err)
		page, err := e.C(A).Jobs.List(ctx, nil)
		noErr(t, err)
		if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Kind != helm.JobKindInstall {
			t.Fatalf("a's jobs = %+v", page.Items)
		}
		wantErr(t, e.C(A).Jobs.Cancel(ctx, id), helm.KindForbidden, "not_a_task")
		_, err = e.C(A).Jobs.Update(ctx, id, map[string]any{"state": "failed"})
		wantErr(t, err, helm.KindForbidden, "not_a_task")
		wantErr(t, e.C(B).Jobs.Cancel(ctx, id), helm.KindNotFound, "")
	})
}

// ---------------------------------------------------------------- events (R41)

func nextNamed(t *testing.T, s *helm.EventStream, name string, within time.Duration) helm.Event {
	t.Helper()
	type res struct {
		ev  helm.Event
		err error
	}
	ch := make(chan res, 1)
	go func() {
		for {
			ev, err := s.Next()
			if err != nil || name == "" || ev.Name == name {
				ch <- res{ev, err}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		noErr(t, r.err)
		return r.ev
	case <-time.After(within):
		t.Fatalf("no %s event within %s", name, within)
		return helm.Event{}
	}
}

func TestEventsCarryOnlyWhatAStudioMaySee(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		sub, cancel := context.WithCancel(ctx)
		defer cancel()
		dStream, err := e.C(D).Events.Subscribe(sub, nil)
		noErr(t, err)
		defer dStream.Close()
		bStream, err := e.C(B).Events.Subscribe(sub, nil)
		noErr(t, err)
		defer bStream.Close()

		// a records an item: b (read_all) hears it, d does not.
		asset := upload(t, e.C(A), []byte("a's secret take"), helm.AssetKindVideo, "s.mp4")
		aItem := addItem(t, e.C(A), asset.ID, map[string]any{})
		var ie helm.ItemEvent
		noErr(t, nextNamed(t, bStream, "item", 5*time.Second).Decode(&ie))
		if ie.Item.ID != aItem.ID || ie.Change != "added" {
			t.Fatalf("b's item event = %+v", ie)
		}
		// d records its own: the first item event d hears is its own.
		mine := upload(t, e.C(D), []byte("d's take"), helm.AssetKindVideo, "d.mp4")
		dItem := addItem(t, e.C(D), mine.ID, map[string]any{})
		noErr(t, nextNamed(t, dStream, "item", 5*time.Second).Decode(&ie))
		if ie.Item.ID != dItem.ID {
			t.Fatalf("d heard %s before its own %s: another studio's item leaked", ie.Item.ID, dItem.ID)
		}

		// A handoff to d arrives as an inbox event.
		_, err = e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: aItem.ID, ToStudio: D})
		noErr(t, err)
		var in helm.InboxEvent
		noErr(t, nextNamed(t, dStream, "inbox", 5*time.Second).Decode(&in))
		if in.ItemID != aItem.ID || in.FromStudio != A {
			t.Fatalf("inbox event = %+v", in)
		}

		// A task job's progress reaches its own studio as a job event.
		aStream, err := e.C(A).Events.Subscribe(sub, nil)
		noErr(t, err)
		defer aStream.Close()
		job, err := e.C(A).Jobs.Create(ctx, helm.TaskCreate{})
		noErr(t, err)
		var je helm.JobEvent
		noErr(t, nextNamed(t, aStream, "job", 5*time.Second).Decode(&je))
		if je.Job.ID != job.ID {
			t.Fatalf("job event = %+v", je)
		}
	})
}

// ---------------------------------------------------------------- /me and capabilities (07 §3, Q8)

func TestMeReportsTheCaller(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		me, err := e.C(B).Me.Get(ctx)
		noErr(t, err)
		if me.StudioID != B || len(me.Capabilities) != 8 || me.APIVersion != helm.APIVersion || me.Provider != e.Name || me.Paths.Stage == "" || me.Paths.Data == "" {
			t.Fatalf("me = %+v", me)
		}
		if me.Quota.Records.Limit != 100000 || me.Quota.KVBytes.Limit != 8<<20 {
			t.Fatalf("default quotas = %+v", me.Quota)
		}
	})
}

func TestEveryOperationChecksItsCapability(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(C) // kv only
		check := func(name, capability string, err error) {
			t.Helper()
			var he *helm.Error
			if !asError(err, &he) || he.Status != http.StatusForbidden || he.Code != "capability_required" || he.Details["capability"] != capability {
				t.Errorf("%s: %v; want 403 capability_required naming %s", name, err, capability)
			}
		}
		_, err := c.Records.Query(ctx, "takes", nil)
		check("records", "records", err)
		_, err = c.Assets.Upload(ctx, bytes.NewReader([]byte("x")), "", &helm.AssetsUploadParams{Kind: helm.AssetKindOther})
		check("assets", "assets", err)
		_, err = c.Gallery.Query(ctx, nil)
		check("gallery", "gallery", err)
		_, err = c.Jobs.List(ctx, nil)
		check("jobs", "jobs", err)
		_, err = c.Handoff.Send(ctx, helm.HandoffRequest{ItemID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ToStudio: A})
		check("handoff", "handoff.send", err)
		_, err = c.Inbox.List(ctx, nil)
		check("inbox", "gallery", err)
		_, err = e.C(D).Sessions.List(ctx, nil)
		check("sessions", "kv", err)
		_, err = c.Me.Get(ctx)
		noErr(t, err)
		_, err = c.KV.Put(ctx, "ui", "x", map[string]any{}, nil)
		noErr(t, err)
	})
}

// ---------------------------------------------------------------- contract validation

func raw(t *testing.T, e *Env, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.URL+"/api/v1"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	noErr(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func TestRequestsTheSchemaForbidsAreRefusedAlike(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		_, err := c.Sessions.Create(ctx, helm.SessionCreate{Name: ""})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Gallery.Add(ctx, helm.ItemCreate{Kind: "hologram", AssetID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Params: map[string]any{}})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: "not-a-ulid", Params: map[string]any{}})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Params: map[string]any{},
			Inputs: []helm.ItemInput{{AssetID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Role: "Not A Role"}}})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Sessions.List(ctx, &helm.SessionsListParams{Limit: ptr(int64(0))})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Sessions.List(ctx, &helm.SessionsListParams{Limit: ptr(int64(201))})
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.KV.Get(ctx, "Bad NS", "k")
		wantErr(t, err, helm.KindInvalid, "bad_request")
		_, err = c.Records.Get(ctx, "takes", "../../etc")
		if helm.KindOf(err) != helm.KindInvalid && helm.KindOf(err) != helm.KindNotFound {
			t.Fatalf("a path id that is not a ULID: %v", err)
		}
		big := map[string]any{"v": strings.Repeat("x", 1<<20)}
		_, err = c.KV.Put(ctx, "ui", "big", big, nil)
		wantErr(t, err, helm.KindInvalid, "too_large")
	})
}

// ---------------------------------------------------------------- tokens (07 §3, Q6, Q7) — daemon only

// A studio token must not reach another studio's data by any route: an id
// guessed or learned, a list, a handoff, a job, the inbox, or leaving the
// token off.
func TestATokenCannotReachAnotherStudiosNamespace(t *testing.T) {
	e := openDaemon(t)
	a, b := e.C(A), e.C(B)

	// Everything B owns, created through B's own token.
	_, err := b.KV.Put(ctx, "ui", "secret", map[string]any{"b": true}, nil)
	noErr(t, err)
	rec, err := b.Records.Insert(ctx, "takes", map[string]any{"b": true})
	noErr(t, err)
	sess, err := b.Sessions.Create(ctx, helm.SessionCreate{Name: "b's"})
	noErr(t, err)
	asset := upload(t, b, []byte("b's render"), helm.AssetKindVideo, "b.mp4")
	item := addItem(t, b, asset.ID, map[string]any{"b": true})
	job, err := b.Jobs.Create(ctx, helm.TaskCreate{})
	noErr(t, err)

	// A, with its own valid token and every id above.
	_, err = a.KV.Get(ctx, "ui", "secret")
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Records.Get(ctx, "takes", rec.ID)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Records.Patch(ctx, "takes", rec.ID, map[string]any{"a": 1}, nil)
	wantErr(t, err, helm.KindNotFound, "")
	wantErr(t, a.Records.Delete(ctx, "takes", rec.ID, nil), helm.KindNotFound, "")
	_, err = a.Sessions.Get(ctx, sess.ID)
	wantErr(t, err, helm.KindNotFound, "")
	wantErr(t, a.Sessions.Activate(ctx, sess.ID), helm.KindNotFound, "")
	_, err = a.Assets.Read(ctx, asset.ID, nil)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Assets.Thumb(ctx, asset.ID, nil)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Assets.Lineage(ctx, asset.ID, nil)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Gallery.Get(ctx, item.ID)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Gallery.Lineage(ctx, item.ID, nil)
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Gallery.Update(ctx, item.ID, map[string]any{"starred": true})
	wantErr(t, err, helm.KindNotFound, "")
	wantErr(t, a.Gallery.Delete(ctx, item.ID), helm.KindNotFound, "")
	_, err = a.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: asset.ID, Params: map[string]any{}})
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: D})
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Jobs.Get(ctx, job.ID)
	wantErr(t, err, helm.KindNotFound, "")
	wantErr(t, a.Jobs.Cancel(ctx, job.ID), helm.KindNotFound, "")
	_, err = a.Jobs.Update(ctx, job.ID, map[string]any{"state": "failed"})
	wantErr(t, err, helm.KindNotFound, "")
	_, err = a.Jobs.Logs(ctx, job.ID)
	wantErr(t, err, helm.KindNotFound, "")
	wantErr(t, a.Jobs.AppendLog(ctx, job.ID, helm.LogAppend{Lines: []string{"x"}}), helm.KindNotFound, "")
	for _, page := range []func() (int, error){
		func() (int, error) { p, err := a.Records.Query(ctx, "takes", nil); return lenOr(p, err) },
		func() (int, error) { p, err := a.Sessions.List(ctx, nil); return lenOr(p, err) },
		func() (int, error) { p, err := a.Gallery.Query(ctx, nil); return lenOr(p, err) },
		func() (int, error) { p, err := a.Jobs.List(ctx, nil); return lenOr(p, err) },
		func() (int, error) { p, err := a.Inbox.List(ctx, nil); return lenOr(p, err) },
		func() (int, error) { p, err := a.KV.List(ctx, "ui", nil); return lenOr(p, err) },
	} {
		if n, err := page(); err != nil || n != 0 {
			t.Errorf("a list shows %d of b's rows (%v)", n, err)
		}
	}
	// A cursor issued to b is not a way into b's data for a.
	bPage, err := b.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Limit: ptr(int64(1))})
	noErr(t, err)
	b.Records.Insert(ctx, "takes", map[string]any{"b": 2})
	bPage, _ = b.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Limit: ptr(int64(1))})
	if bPage.NextCursor != nil {
		_, err = a.Records.Query(ctx, "takes", &helm.RecordsQueryParams{Limit: ptr(int64(1)), Cursor: bPage.NextCursor})
		wantErr(t, err, helm.KindInvalid, "bad_cursor")
	}

	// Leaving the token off, a wrong token, another scheme, a revoked token.
	for _, tok := range []string{"", "hs_live_" + strings.Repeat("a", 52), "not-a-token"} {
		if status, body := raw(t, e, "GET", "/kv/ui/secret", tok, ""); status != http.StatusUnauthorized || body["error"] != "unauthenticated" {
			t.Errorf("token %q: %d %v; want 401 unauthenticated", tok, status, body)
		}
	}
	for _, op := range [][2]string{{"GET", "/gallery/items/" + item.ID}, {"GET", "/assets/" + asset.ID}, {"GET", "/jobs"}, {"GET", "/inbox"}, {"GET", "/events"}, {"GET", "/me"}, {"POST", "/handoff"}} {
		if status, _ := raw(t, e, op[0], op[1], "", ""); status != http.StatusUnauthorized {
			t.Errorf("%s %s with no token: %d; want 401", op[0], op[1], status)
		}
	}
	// A token is a header, never a URL, where it would land in logs.
	if status, _ := raw(t, e, "GET", "/me?token="+e.Tokens[A], "", ""); status != http.StatusUnauthorized {
		t.Errorf("a token in the query string: %d; want 401", status)
	}
	req, _ := http.NewRequest("GET", e.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Basic "+e.Tokens[A])
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a token under another auth scheme: %v %v; want 401", resp.StatusCode, err)
	}

	e.Revoke(t, B)
	_, err = b.KV.Get(ctx, "ui", "secret")
	wantErr(t, err, helm.KindUnauthenticated, "unauthenticated")

}

func lenOr[P interface {
	*helm.RecordPage | *helm.SessionPage | *helm.ItemPage | *helm.JobPage | *helm.InboxPage | *helm.KVKeyPage
}](p P, err error) (int, error) {
	if err != nil {
		return -1, err
	}
	b, _ := json.Marshal(p)
	var v struct{ Items []any }
	json.Unmarshal(b, &v)
	return len(v.Items), nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------- the embedded provider itself

// Opened from a studio's own manifest, the embedded provider grants what the
// manifest declares and nothing more, keeps its data in ./.helm, and refuses a
// second process (Q26, first review #7).
func TestEmbeddedProviderOpensFromTheStudiosManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "helmstudio.yaml")
	os.WriteFile(manifestPath, []byte(`id: tiny-studio
name: tiny studio
kinds: [image]
repo: https://example.com/tiny.git
license: MIT
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
capabilities: [kv, gallery]
processes:
  - { name: web, role: main, cmd: "true" }
`), 0o644)
	p, err := embedded.Open(embedded.Options{Dir: filepath.Join(dir, ".helm"), Manifest: manifestPath})
	noErr(t, err)
	defer p.Close()
	me, err := p.Client.Me.Get(ctx)
	noErr(t, err)
	if me.StudioID != "tiny-studio" || me.Provider != "embedded" || me.Paths.Stage != filepath.Join(dir, ".helm", "stage") || me.Paths.Data != filepath.Join(dir, ".helm", "data") {
		t.Fatalf("me = %+v", me)
	}
	_, err = p.Client.Records.Insert(ctx, "x", map[string]any{})
	wantErr(t, err, helm.KindForbidden, "capability_required")
	_, err = p.Client.KV.Put(ctx, "ui", "k", map[string]any{"v": 1}, nil)
	noErr(t, err)
	if _, err := os.Stat(filepath.Join(dir, ".helm", "helm.db")); err != nil {
		t.Fatalf("no ./.helm/helm.db: %v", err)
	}
	_, err = embedded.Open(embedded.Options{Dir: filepath.Join(dir, ".helm"), Manifest: manifestPath})
	if err == nil || !strings.Contains(err.Error(), "helm dev") {
		t.Fatalf("a second open: %v; want a refusal naming helm dev", err)
	}
	_, err = embedded.Open(embedded.Options{Dir: filepath.Join(dir, "other"), Manifest: filepath.Join(dir, "absent.yaml")})
	if err == nil {
		t.Fatal("opened with no manifest; capabilities must come from one")
	}
}

// ---------------------------------------------------------------- every route (second review #4)

// Every studio-api operation that names a resource by id in its path is tried
// by studio A with an id studio B owns, and must answer 404. The list of
// operations comes from the generated router, so an operation added to the
// contract without a case here fails this test rather than going untried.
func TestEveryIDRouteRefusesAnotherStudiosResource(t *testing.T) {
	e := openDaemon(t)
	b := e.C(B)
	rec, err := b.Records.Insert(ctx, "takes", map[string]any{"b": true})
	noErr(t, err)
	sess, err := b.Sessions.Create(ctx, helm.SessionCreate{Name: "b's session"})
	noErr(t, err)
	asset := upload(t, b, pngBytes(t, 4, 4, 21), helm.AssetKindImage, "b.png")
	item := addItem(t, b, asset.ID, map[string]any{"b": true})
	job, err := b.Jobs.Create(ctx, helm.TaskCreate{})
	noErr(t, err)
	// An inbox entry addressed to B: sent by A, which holds handoff.send.
	aAsset := upload(t, e.C(A), []byte("a's take"), helm.AssetKindVideo, "a.mp4")
	aItem := addItem(t, e.C(A), aAsset.ID, map[string]any{})
	entry, err := e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: aItem.ID, ToStudio: B})
	noErr(t, err)

	tl, err := b.Timeline.Create(ctx, helm.TimelineCreate{Name: "b's sequence",
		Target: helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24}})
	noErr(t, err)
	// An export job of b's sequence, written straight into the store: making a
	// real one needs media and ffmpeg, and what is tried here is whether the
	// route refuses another studio, not whether a render works.
	exportJob := store.NewID(time.Now())
	noErr(t, e.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, created_at)
			VALUES (?, 'export', ?, 'running', 'timeline', ?, 0, 1, ?)`, exportJob, B, tl.ID, time.Now().UnixMilli())
		return err
	}))

	ids := map[string]string{
		"/timeline":      tl.ID,
		"/sessions":      sess.ID,
		"/records":       rec.ID,
		"/assets":        asset.ID,
		"/gallery/items": item.ID,
		"/jobs":          job.ID,
		"/inbox":         entry.ID,
	}
	// A body the schema accepts, so a refusal is about ownership, not shape.
	bodies := map[string]string{
		"sessionsUpdate":    `{"name":"taken over"}`,
		"sessionsDuplicate": `{"name":"a copy"}`,
		"recordsReplace":    `{"a":1}`,
		"recordsPatch":      `{"a":1}`,
		"galleryUpdate":     `{"starred":true}`,
		"jobsUpdate":        `{"state":"failed"}`,
		"jobsAppendLog":     `{"lines":["x"]}`,
		"timelineUpdate":    `{"name":"taken over"}`,
		"timelineRevert":    `{"revision":1}`,
		"timelineExport":    `{"preset":"h264"}`,
	}
	// Headers an operation's contract requires, so a refusal is about
	// ownership rather than a missing precondition.
	headers := map[string]map[string]string{
		"timelineUpdate": {"If-Match": "1"},
		"timelineRevert": {"If-Match": "1"},
	}
	tried := 0
	for _, op := range studioapi.Operations() {
		if !strings.Contains(op.Path, "{id}") {
			continue
		}
		prefix := op.Path[:strings.Index(op.Path, "/{id}")]
		if i := strings.Index(prefix, "/{collection}"); i >= 0 {
			prefix = prefix[:i]
		}
		id, ok := ids[prefix]
		if !ok {
			t.Errorf("%s %s (%s) takes an id and this test has no resource of studio b for it; add one", op.Method, op.Path, op.ID)
			continue
		}
		path := strings.NewReplacer("{id}", id, "{collection}", "takes", "{job}", exportJob).Replace(op.Path)
		body := bodies[op.ID]
		needsBody := op.Method == "PUT" || op.Method == "PATCH" ||
			(op.Method == "POST" && (strings.HasSuffix(op.Path, "/logs") || strings.HasSuffix(op.Path, ":revert") || strings.HasSuffix(op.Path, ":export")))
		if needsBody && body == "" {
			t.Errorf("%s %s (%s) takes a body and this test has none for it; add one", op.Method, op.Path, op.ID)
			continue
		}
		req, _ := http.NewRequest(op.Method, e.URL+"/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.Tokens[A])
		for k, v := range headers[op.ID] {
			req.Header.Set(k, v)
		}
		if body != "" {
			ct := "application/json"
			if op.Method == "PATCH" {
				ct = "application/merge-patch+json"
			}
			req.Header.Set("Content-Type", ct)
		}
		resp, err := http.DefaultClient.Do(req)
		noErr(t, err)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		tried++
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s (%s) with studio b's id as studio a: %d %s; want 404", op.Method, op.Path, op.ID, resp.StatusCode, raw)
		}
	}
	if tried < 20 {
		t.Fatalf("only %d id routes were tried; the route table is not what this test expects", tried)
	}

	// B's resources are untouched.
	got, err := b.Sessions.Get(ctx, sess.ID)
	noErr(t, err)
	if got.Name != "b's session" {
		t.Fatalf("b's session was changed through studio a: %+v", got)
	}
	gotJob, err := b.Jobs.Get(ctx, job.ID)
	noErr(t, err)
	if gotJob.State != helm.JobStateRunning || gotJob.CancelRequestedAt != nil {
		t.Fatalf("b's job was changed through studio a: %+v", gotJob)
	}
	inbox, err := b.Inbox.List(ctx, nil)
	noErr(t, err)
	if len(inbox.Items) != 1 {
		t.Fatalf("b's inbox entry was consumed through studio a")
	}
}

// ---------------------------------------------------------------- resuming events across a restart (second review #2)

func TestResumingEventsAcrossARestartSendsAGap(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		sub, cancel := context.WithCancel(ctx)
		defer cancel()
		stream, err := e.C(A).Events.Subscribe(sub, nil)
		noErr(t, err)
		asset := upload(t, e.C(A), []byte("before the restart"), helm.AssetKindVideo, "before.mp4")
		addItem(t, e.C(A), asset.ID, map[string]any{})
		old := nextNamed(t, stream, "item", 5*time.Second)
		stream.Close()
		if old.ID == "" {
			t.Fatal("an event carried no id")
		}

		e.Restart(t)

		// The restarted provider's own events pass the old id's number, which
		// is when a number alone would read the old id as a real position.
		oldSeq, _ := strconv.Atoi(old.ID[strings.LastIndex(old.ID, "-")+1:])
		for i := 0; i <= oldSeq+1; i++ {
			a := upload(t, e.C(A), []byte(fmt.Sprintf("after the restart %d", i)), helm.AssetKindVideo, "n.mp4")
			addItem(t, e.C(A), a.ID, map[string]any{})
		}

		// Resuming from the previous provider's id: a gap first, never silence.
		resumed, err := e.C(A).Events.Subscribe(sub, &helm.EventsSubscribeParams{LastEventID: ptr(old.ID)})
		noErr(t, err)
		if first := nextNamed(t, resumed, "", 5*time.Second); first.Name != "gap" {
			t.Fatalf("resuming from the old provider's id %s began with %q; want gap", old.ID, first.Name)
		}
		after := upload(t, e.C(A), []byte("after the restart"), helm.AssetKindVideo, "after.mp4")
		addItem(t, e.C(A), after.ID, map[string]any{})
		fresh := nextNamed(t, resumed, "item", 5*time.Second)
		resumed.Close()
		if fresh.ID == old.ID {
			t.Fatalf("the restarted provider reissued id %s", old.ID)
		}

		// An id this provider never issued, and one that does not parse.
		boot, _, _ := strings.Cut(fresh.ID, "-")
		for _, id := range []string{boot + "-99999999", "garbage"} {
			s, err := e.C(A).Events.Subscribe(sub, &helm.EventsSubscribeParams{LastEventID: ptr(id)})
			noErr(t, err)
			nextNamed(t, s, "gap", 5*time.Second)
			s.Close()
		}
		// Resuming from a real id of this provider replays what followed, with no gap.
		s, err := e.C(A).Events.Subscribe(sub, &helm.EventsSubscribeParams{LastEventID: ptr(fresh.ID)})
		noErr(t, err)
		third := upload(t, e.C(A), []byte("third"), helm.AssetKindVideo, "third.mp4")
		addItem(t, e.C(A), third.ID, map[string]any{})
		ev := nextNamed(t, s, "", 5*time.Second) // the very next event, whatever it is
		s.Close()
		if ev.Name != "item" {
			t.Fatalf("a valid resume began with %q; want the item event and no gap", ev.Name)
		}
	})
}
