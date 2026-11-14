// Integration test of Catalog Agent: HTTP → outbox → (hand-driven agents) → projector → read models, against a
// scratch Postgres (TEST_DATABASE_URL, skipped without it), the memory store and no NATS.
package tests

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"maps"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/middleware"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/catalog"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/migrations"
	"github.com/helmstudio/studio_service/registry"
)

const (
	userA = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
	userB = "11111111-2222-4333-8444-555555555555"
)

// env is one test's API: a migrated scratch database, the memory store, the projector's handlers, a test server and
// the stops broadcast to agents (agents.cancel, agents.cancel.image).
type env struct {
	t         *testing.T
	pool      *pgxpool.Pool
	store     *storage.Memory
	events    *micro.NatsJetStreamGroup
	srv       *httptest.Server
	cancelled []string
}

// setup migrates a fresh schema, loads the registry fixtures and serves the API. It skips without TEST_DATABASE_URL.
func setup(t *testing.T) *env {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	if err := goose.Up(sqldb, "."); err != nil {
		t.Fatal(err)
	}
	sqldb.Close()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, key := range []string{"catalog_library", "catalog_prices"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", key+".valid.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.Set(key, data); err != nil {
			t.Fatal(err)
		}
	}
	e := &env{t: t, pool: pool, store: storage.NewMemory(), events: &micro.NatsJetStreamGroup{}}
	stop := catalog.Stopper{ // records "<run id>" for a whole run, "<run id> <image ids…>" for skips (none: nothing)
		Run: func(runID string) { e.cancelled = append(e.cancelled, runID) },
		Images: func(runID string, imageIDs []string) {
			if len(imageIDs) > 0 {
				e.cancelled = append(e.cancelled, runID+" "+strings.Join(imageIDs, " "))
			}
		},
	}
	catalog.NewProjector(pool, stop).MountNatsJetStream(e.events)
	e.srv = httptest.NewUnstartedServer(nil) // the signer needs the server's URL first
	signer := common.NewSigner("test-secret-0123456789", "http://"+e.srv.Listener.Addr().String(), time.Hour)
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(middleware.NewErrorCatcher(), middleware.NewNotFound(),
		middleware.NewBodyLimit(func(string) int64 { return 64 << 20 }), middleware.NewPrefixGuard("/v1/", fakeAuthentication))
	shutdown := make(chan struct{})
	t.Cleanup(func() { close(shutdown) })
	router.LoadControllers(catalog.NewController(catalog.NewService(pool, e.store, signer, nil, stop), shutdown))
	e.srv.Config.Handler = router.Engine()
	e.srv.Start()
	t.Cleanup(e.srv.Close)
	return e
}

// signedObject checks that link is a signed /files link under archives/ and returns its object from the store.
func (e *env) signedObject(link string) []byte {
	e.t.Helper()
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(u.Path, "/files/u/") || u.Query().Get("sig") == "" || u.Query().Get("exp") == "" {
		e.t.Fatalf("not a signed link: %s", link)
	}
	key := strings.TrimPrefix(u.Path, "/files/")
	if !strings.Contains(key, "/archives/") {
		e.t.Fatalf("archive outside u/<owner>/archives/ (the janitor would miss it): %s", key)
	}
	body, err := e.store.Get(context.Background(), key)
	if err != nil {
		e.t.Fatal(err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	return data
}

// fakeAuthentication stands in for the NATS token check: X-Test-User is the caller's id.
func fakeAuthentication(ctx *gin.Context) {
	id := ctx.GetHeader("X-Test-User")
	if id == "" {
		network.SendError(ctx, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}
	common.SetCaller(ctx, common.Caller{ID: id, Roles: []string{"user"}})
}

// do sends one request as user and returns the status and body.
func (e *env) do(user, method, path string, body io.Reader, contentType string) (int, []byte) {
	e.t.Helper()
	request, _ := http.NewRequest(method, e.srv.URL+path, body)
	request.Header.Set("X-Test-User", user)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	defer response.Body.Close()
	out, _ := io.ReadAll(response.Body)
	return response.StatusCode, out
}

// call sends JSON (nil: an empty body) and wants the status; it returns the decoded body.
func (e *env) call(user, method, path string, body any, want int) map[string]any {
	e.t.Helper()
	var reader io.Reader = strings.NewReader("")
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	code, out := e.do(user, method, path, reader, "application/json")
	if code != want {
		e.t.Fatalf("%s %s: %d %s, want %d", method, path, code, out, want)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	return decoded
}

// relay drains the outbox like the relay does and returns the commands.
func (e *env) relay() []gen.Envelope {
	e.t.Helper()
	var sent []gen.Envelope
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, _ string, envelope *gen.Envelope, _ string) error {
		sent = append(sent, *envelope)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return sent
}

// emit projects one evt.catalog.<session>.<typ> of the command's job, as agents_service would publish it.
func (e *env) emit(command gen.Envelope, sessionID string, seq int, typ string, data map[string]any) {
	e.t.Helper()
	data["seq"], data["attempt"], data["session_id"] = seq, 1, sessionID
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.catalog." + typ, Version: 1, ID: utility.NewID(), JobID: command.JobID,
		OwnerID: command.OwnerID, OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	subject := "evt.catalog." + sessionID + "." + typ
	if err := e.events.Handle(context.Background(), subject, body); err != nil {
		e.t.Fatal(err)
	}
}

// pngBytes is a small valid PNG.
func pngBytes() []byte {
	picture := image.NewRGBA(image.Rect(0, 0, 4, 4))
	picture.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, picture)
	return buf.Bytes()
}

// rawUpload posts n PNGs to /raw, with a families part per family id ("What are we shooting?").
func (e *env) rawUpload(user, sessionID string, n int, families ...string) (int, []byte) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("label", "SS26 rings")
	for _, family := range families {
		_ = writer.WriteField("families", family)
	}
	for i := range n {
		part, _ := writer.CreateFormFile("files", fmt.Sprintf("IMG_%d.png", i))
		_, _ = part.Write(pngBytes())
	}
	_ = writer.Close()
	return e.do(user, "POST", "/v1/catalog/sessions/"+sessionID+"/raw", &buf, writer.FormDataContentType())
}

// list is v as a list of objects.
func list(v any) []map[string]any {
	var out []map[string]any
	for _, item := range v.([]any) {
		out = append(out, item.(map[string]any))
	}
	return out
}

// TestCatalogFlow walks a session from raw upload to download, with another user always getting 404.
func TestCatalogFlow(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	detail := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{"project_name": "SS26"}, 201)
	sessionID := detail["session"].(map[string]any)["id"].(string)
	e.call(userB, "GET", base+sessionID, nil, 404)
	if code, _ := e.rawUpload(userB, sessionID, 1); code != 404 {
		t.Fatalf("another user's raw upload: %d", code)
	}

	// raw upload → cmd.catalog.process with the stored keys
	if code, body := e.rawUpload(userA, sessionID, 2); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	if code, _ := e.rawUpload(userA, sessionID, 1); code != 409 {
		t.Fatalf("raw while processing: %d, want 409", code)
	}
	commands := e.relay()
	if len(commands) != 1 || commands[0].Type != "cmd.catalog.process" {
		t.Fatalf("commands: %+v", commands)
	}
	var process struct {
		Ingest []struct{ Keys []string } `json:"ingest"`
	}
	_ = json.Unmarshal(commands[0].Data, &process)
	keys := process.Ingest[0].Keys
	if len(keys) != 2 || !common.Owns(userA, keys[0]) {
		t.Fatalf("ingest keys: %v", keys)
	}

	// agents: running, two SKUs, a blocking question, succeeded
	sku := func(code string) map[string]any {
		return map[string]any{"code": code, "name": "Ring " + code, "category": "rings", "subcategory": nil,
			"group_key": "statement", "group_label": "Statement", "variant_of": nil, "attributes": map[string]any{},
			"confirmed": false, "raw_keys": keys[:1]}
	}
	e.emit(commands[0], sessionID, 1, "status", map[string]any{"work": "process", "status": "running", "error": nil})
	e.emit(commands[0], sessionID, 2, "skus", map[string]any{"skus": []any{sku("R-001"), sku("R-002")}, "found": 2,
		"expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 3, "question", map[string]any{"key": "count:R-001", "check": "count",
		"text": "Four stones or six?", "blocking": true, "sku_codes": []string{"R-001"},
		"options": []any{map[string]any{"label": "Four", "apply": []any{ // a known subcategory is set, an unknown one skipped
			map[string]any{"sku_code": "R-001", "category": "rings", "subcategory": "band"},
			map[string]any{"sku_code": "R-002", "subcategory": "nope"}}}, map[string]any{"label": "Six", "apply": []any{}}}})
	e.emit(commands[0], sessionID, 3, "question", map[string]any{"key": "dupe", "check": "count", "text": "redelivery",
		"blocking": true, "sku_codes": []string{}, "options": []any{}}) // same seq: skipped
	e.emit(commands[0], sessionID, 4, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})

	page := e.call(userA, "GET", base+sessionID+"/events?since=0", nil, 200)
	if events := list(page["events"]); len(events) != 4 || page["last_seq"].(float64) != 4 ||
		len(list(events[1]["data"].(map[string]any)["skus"])) != 2 {
		t.Fatalf("events: %v", page)
	}
	if page := e.call(userA, "GET", base+sessionID+"/events?since=3", nil, 200); len(list(page["events"])) != 1 {
		t.Fatalf("events since 3: %v", page)
	}
	e.call(userA, "GET", base+sessionID+"/events?since=-1", nil, 422)
	e.call(userB, "GET", base+sessionID+"/events", nil, 404)

	// the blocking question holds Start until it is answered
	e.call(userA, "POST", base+sessionID+"/start", nil, 409)
	detail = e.call(userA, "GET", base+sessionID, nil, 200)
	questions := list(detail["questions"])
	if len(questions) != 1 {
		t.Fatalf("questions: %v", questions)
	}
	answer := "/v1/catalog/questions/" + questions[0]["id"].(string) + "/answer"
	e.call(userB, "POST", answer, map[string]any{"option": 0}, 404)
	e.call(userA, "POST", answer, map[string]any{"option": 2}, 422)
	for _, k := range list(detail["skus"]) { // a pose override that the same-product apply below keeps
		if k["code"] == "R-001" {
			e.call(userA, "PATCH", "/v1/catalog/skus/"+k["id"].(string), map[string]any{"poses": []string{"ring-packshot"}}, 200)
		}
	}
	answered := e.call(userA, "POST", answer, map[string]any{"option": 0}, 200)
	subs := map[string]any{}
	for _, k := range list(answered["skus"]) {
		subs[k["code"].(string)] = fmt.Sprint(k["subcategory"], k["brief"].(map[string]any)["poses"])
	}
	if fmt.Sprint(subs) != "map[R-001:band[ring-packshot] R-002:<nil> []]" {
		t.Fatalf("answer's subcategories and poses: %v", subs)
	}
	e.call(userA, "POST", answer, map[string]any{"option": 1}, 409)
	started := e.call(userA, "POST", base+sessionID+"/start", nil, 200)
	if fmt.Sprint(started["session"].(map[string]any)["stage"]) != "ready" {
		t.Fatalf("start: %v", started)
	}
	if card := e.call(userA, "GET", base+sessionID+"/next-step", nil, 200); len(list(card["options"])) == 0 {
		t.Fatalf("next step before any run: %v", card)
	}

	// a sample run: one SKU × its poses, rendered and reviewed by the agents
	accepted := e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "sample", "category": "rings"}, 202)
	images := list(accepted["images"])
	if len(images) == 0 {
		t.Fatalf("run: %v", accepted)
	}
	e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus", "sku_ids": []string{images[0]["sku_id"].(string)}}, 409)
	commands = e.relay()
	if len(commands) != 1 || commands[0].Type != "cmd.catalog.run" {
		t.Fatalf("commands: %+v", commands)
	}
	for i, img := range images {
		key := common.OwnerPrefix(userA) + "catalog/" + sessionID + "/renders/" + img["id"].(string) + ".png"
		if err := e.store.Put(context.Background(), key, bytes.NewReader(pngBytes()), int64(len(pngBytes())), "image/png"); err != nil {
			t.Fatal(err)
		}
		e.emit(commands[0], sessionID, i+1, "image", map[string]any{"image_id": img["id"], "sku_id": img["sku_id"],
			"status": "to_review", "key": key, "review": map[string]any{"passed": true}, "attempts": 1 + min(i, 1),
			"first_pass": i == 0, "error": nil}) // the first image passed first time, the others on a second render
	}
	e.emit(commands[0], sessionID, len(images)+1, "status", map[string]any{"work": "run", "status": "succeeded", "error": nil})

	e.call(userA, "POST", base+sessionID+"/download", nil, 409) // nothing approved yet
	e.call(userB, "POST", base+sessionID+"/approve", map[string]any{"category": "rings"}, 404)
	approved := e.call(userA, "POST", base+sessionID+"/approve", map[string]any{"category": "rings"}, 200)
	if int(approved["approved"].(float64)) != len(images) {
		t.Fatalf("approve: %v", approved)
	}
	e.call(userA, "PATCH", "/v1/catalog/images/"+images[0]["id"].(string), map[string]any{"status": "approved"}, 200)
	e.call(userB, "PATCH", "/v1/catalog/images/"+images[0]["id"].(string), map[string]any{"status": "rejected"}, 404)

	// the zip: <SKU>/<shot>.png per approved image plus catalog.csv
	download := e.call(userA, "POST", base+sessionID+"/download", nil, 200)
	archive := e.signedObject(download["url"].(string)) // files_service serves the link; here the store is read directly
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range reader.File {
		names[f.Name] = true
	}
	if !names["catalog.csv"] || len(names) != len(images)+1 {
		t.Fatalf("zip files: %v", names)
	}

	// dashboard counts and account totals; another user sees nothing
	sessions := list(e.call(userA, "GET", "/v1/catalog/sessions", nil, 200)["sessions"])
	if len(sessions) != 1 || sessions[0]["sku_count"].(float64) != 2 ||
		sessions[0]["counts"].(map[string]any)["images_approved"].(float64) != float64(len(images)) {
		t.Fatalf("sessions: %v", sessions)
	}
	stats := e.call(userA, "GET", "/v1/catalog/stats", nil, 200)
	if stats["sessions"].(float64) != 1 || stats["skus"].(float64) != 2 || stats["images_approved"].(float64) != float64(len(images)) {
		t.Fatalf("stats: %v", stats)
	}
	if other := e.call(userB, "GET", "/v1/catalog/stats", nil, 200); other["sessions"].(float64) != 0 {
		t.Fatalf("another user's stats: %v", other)
	}
	if other := e.call(userB, "GET", "/v1/catalog/sessions", nil, 200); len(other["sessions"].([]any)) != 0 {
		t.Fatalf("another user's sessions: %v", other)
	}

	// quality by product: the rings' first-pass rate, tries and credits × tries per approved image; owner-scoped
	metrics := list(e.call(userA, "GET", "/v1/catalog/metrics?session_id="+sessionID, nil, 200)["products"])
	n := float64(len(images))
	tries := 2*n - 1
	credits := accepted["run"].(map[string]any)["credits_quoted"].(float64)
	if len(metrics) != 1 || metrics[0]["product"] != "rings" || metrics[0]["images"].(float64) != n ||
		metrics[0]["first_pass_rate"].(float64) != 1/n || metrics[0]["avg_tries"].(float64) != tries/n ||
		metrics[0]["approved"].(float64) != n || metrics[0]["rejected"].(float64) != 0 ||
		math.Abs(metrics[0]["credits_per_approved"].(float64)-credits/n*tries/n) > 1e-9 {
		t.Fatalf("metrics: %v", metrics)
	}
	if other := list(e.call(userB, "GET", "/v1/catalog/metrics?session_id="+sessionID, nil, 200)["products"]); len(other) != 0 {
		t.Fatalf("another user's metrics: %v", other)
	}
	e.call(userA, "GET", "/v1/catalog/metrics?session_id=nope", nil, 422)

	// the typed request and pose suggestions need agents: 503 without them
	e.call(userA, "POST", base+sessionID+"/requests", map[string]any{"text": "approve the rings"}, 503)
	library := e.call(userA, "GET", "/v1/catalog/library", nil, 200)
	if rings := list(library["categories"])[0]; rings["max_attempts"].(float64) != 4 || rings["default_resolution"] != "2K" ||
		list(library["models"])[0]["full_body_url"] == nil {
		t.Fatalf("library product settings: %v", library)
	}
	e.call(userA, "GET", "/v1/catalog/estimate?images=4&resolution=2K", nil, 200)
	e.call(userA, "GET", "/v1/catalog/estimate?images=-1", nil, 422)
}

// TestCatalogProducts: the families choice reaches cmd.catalog.process, a furniture SKU is briefed and run without a
// model, and the brand can move a SKU to another product (validated against the library).
func TestCatalogProducts(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, _ := e.rawUpload(userA, sessionID, 1, "no-such-family"); code != 422 {
		t.Fatalf("unknown family: %d, want 422", code)
	}
	if code, body := e.rawUpload(userA, sessionID, 2, "furniture", "jewellery"); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	var process struct {
		Families []string                  `json:"families"`
		Ingest   []struct{ Keys []string } `json:"ingest"`
	}
	_ = json.Unmarshal(commands[0].Data, &process)
	if strings.Join(process.Families, ",") != "furniture,jewellery" {
		t.Fatalf("process families: %v", process.Families)
	}
	sku := func(code, category string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": category, "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{"description": "d", "counts": map[string]int{"stones": 2}},
			"confirmed": true,
			"raw_keys":  process.Ingest[0].Keys[:1]}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("SO-001", "sofas"), sku("R-001", "rings"),
		sku("R-002", "rings")}, "found": 3, "expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	briefs := map[string]map[string]any{}
	ids := map[string]string{}
	for _, s := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
		briefs[s["code"].(string)], ids[s["code"].(string)] = s["brief"].(map[string]any), s["id"].(string)
	}
	if briefs["SO-001"]["model"] != nil || briefs["SO-001"]["background"] != "living-room" || briefs["R-001"]["model"] != "m001" {
		t.Fatalf("briefs: %v", briefs)
	}

	// next step: rings first (library order), then the sofa
	if card := e.call(userA, "GET", base+sessionID+"/next-step", nil, 200); card["heading"] != "Next step · Rings" {
		t.Fatalf("next step: %v", card)
	}

	// product override: validated, poses and model follow the product, the code is kept
	skuPath := "/v1/catalog/skus/"
	e.call(userA, "PATCH", skuPath+ids["SO-001"], map[string]any{"model": "m001"}, 422)
	e.call(userA, "PATCH", skuPath+ids["R-001"], map[string]any{"poses": []string{"sofa-room"}}, 422)
	e.call(userA, "PATCH", skuPath+ids["R-001"], map[string]any{"category": "no-such-product"}, 422)
	e.call(userA, "PATCH", skuPath+ids["R-001"], map[string]any{"category": "sofas", "model": "m001"}, 422)
	e.call(userA, "PATCH", skuPath+ids["SO-001"], map[string]any{"background": "velvet"}, 422) // a jewellery background
	e.call(userA, "PATCH", skuPath+ids["R-001"], map[string]any{"category": "sofas", "background": "marble"}, 422)
	moved := list(e.call(userA, "PATCH", skuPath+ids["R-001"], map[string]any{"category": "sofas", "apply_to_category": true}, 200)["skus"])
	if len(moved) != 2 {
		t.Fatalf("moved: %v", moved)
	}
	for _, s := range moved {
		brief := s["brief"].(map[string]any)
		attrs := s["attributes"].(map[string]any)
		if s["category"] != "sofas" || !strings.HasPrefix(s["code"].(string), "R-") || brief["model"] != nil ||
			fmt.Sprint(brief["poses"]) != "[sofa-room]" || s["group_key"] != "sofas" || s["confirmed"] != false ||
			attrs["counts"] != nil || attrs["description"] != "d" {
			t.Fatalf("moved SKU: %v", s)
		}
	}
	back := list(e.call(userA, "PATCH", skuPath+ids["R-002"], map[string]any{"category": "rings"}, 200)["skus"])
	if brief := back[0]["brief"].(map[string]any); brief["model"] != "m001" || len(brief["poses"].([]any)) != 2 {
		t.Fatalf("moved back: %v", back)
	}

	// a run of a furniture SKU sends model null
	e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "sample", "category": "sofas"}, 202)
	var run struct {
		Skus []struct {
			Model *json.RawMessage `json:"model"`
		} `json:"skus"`
	}
	if commands = e.relay(); len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil || run.Skus[0].Model != nil {
		t.Fatalf("run command: %s", commands[0].Data)
	}
}

// TestCatalogEventStream: SSE replays the stored events after Last-Event-ID, then sends new ones as they are stored.
func TestCatalogEventStream(t *testing.T) {
	catalog.PollEvery = 50 * time.Millisecond
	e := setup(t)
	detail := e.call(userA, "POST", "/v1/catalog/sessions", nil, 201)
	sessionID := detail["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	command := e.relay()[0]
	e.emit(command, sessionID, 1, "log", map[string]any{"work": "process", "level": "info", "text": "one"})
	e.emit(command, sessionID, 2, "log", map[string]any{"work": "process", "level": "info", "text": "two"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/catalog/sessions/"+sessionID+"/events", nil)
	request.Header.Set("X-Test-User", userA)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(response.Body)
	next := func() string {
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), "id: ") {
				return lines.Text()
			}
		}
		t.Fatalf("stream ended: %v", lines.Err())
		return ""
	}
	if id := next(); id != "id: 2" {
		t.Fatalf("replay: %q, want id: 2", id)
	}
	e.emit(command, sessionID, 3, "log", map[string]any{"work": "process", "level": "info", "text": "three"})
	if id := next(); id != "id: 3" {
		t.Fatalf("live: %q, want id: 3", id)
	}
}

// TestStopRun stops a run: its generating images go, a finished one stays, the run fails "Stopped" and agents is told;
// a second stop is 409, another user 404, a command not relayed yet is dropped, and a job that reports in late is
// cancelled again without the run changing.
func TestStopRun(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	sku := func(code string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": "rings", "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{}, "confirmed": true, "raw_keys": []string{}}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("R-001"), sku("R-002")}, "found": 2,
		"expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	e.call(userA, "POST", base+sessionID+"/start", nil, 200)

	// a running run with one finished image
	accepted := e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "all", "category": "rings"}, 202)
	runID := accepted["run"].(map[string]any)["id"].(string)
	images := list(accepted["images"])
	command := e.relay()[0]
	e.emit(command, sessionID, 1, "status", map[string]any{"work": "run", "status": "running", "error": nil})
	done := images[0]
	key := common.OwnerPrefix(userA) + "catalog/" + sessionID + "/images/" + done["id"].(string) + ".png"
	e.emit(command, sessionID, 2, "image", map[string]any{"image_id": done["id"], "sku_id": done["sku_id"], "status": "to_review",
		"key": key, "review": nil, "attempts": 1, "first_pass": true, "error": nil})

	stop := base + sessionID + "/runs/" + runID + "/stop"
	e.call(userB, "POST", stop, nil, 404)
	e.call(userA, "POST", base+sessionID+"/runs/"+utility.NewID()+"/stop", nil, 404)
	e.call(userA, "POST", base+sessionID+"/runs/nope/stop", nil, 404)
	stopped := e.call(userA, "POST", stop, nil, 200)
	run := stopped["run"].(map[string]any)
	if run["status"] != "failed" || run["error"] != "Stopped" || run["finished_at"] == nil ||
		fmt.Sprint(run["image_ids"]) != fmt.Sprint([]any{done["id"]}) || len(list(stopped["skus"])) != 2 ||
		len(stopped["removed_image_ids"].([]any)) != len(images)-1 {
		t.Fatalf("stopped: %v", stopped)
	}
	if fmt.Sprint(e.cancelled) != fmt.Sprint([]string{runID}) {
		t.Fatalf("cancel broadcasts: %v", e.cancelled)
	}
	e.call(userA, "POST", stop, nil, 409)

	detail := e.call(userA, "GET", base+sessionID, nil, 200)
	if got := list(detail["images"]); len(got) != 1 || got[0]["id"] != done["id"] {
		t.Fatalf("images after stop: %v", got)
	}
	statuses := map[string]any{}
	for _, k := range list(detail["skus"]) {
		statuses[k["id"].(string)] = k["status"]
	}
	for _, k := range list(stopped["skus"]) {
		if want := map[bool]string{true: "To review", false: "Not run"}[k["id"] == done["sku_id"]]; k["status"] != want ||
			statuses[k["id"].(string)] != want {
			t.Fatalf("SKU %v after stop: %v / %v, want %s", k["code"], k["status"], statuses[k["id"].(string)], want)
		}
	}
	events := list(e.call(userA, "GET", base+sessionID+"/events?since=0", nil, 200)["events"])
	if last := events[len(events)-1]; last["type"] != "status" || last["work_id"] != runID ||
		fmt.Sprint(last["data"]) != fmt.Sprint(map[string]any{"session_id": sessionID, "work": "run", "status": "failed", "error": "Stopped"}) {
		t.Fatalf("last event: %v", last)
	}

	// agents' progress and its own end of the job leave the stopped run (and its events) as they are
	e.emit(command, sessionID, 3, "run", map[string]any{"sku_id": done["sku_id"], "sku_status": "Running", "images_done": 1,
		"images_total": len(images), "line": "Running R-001…"})
	e.emit(command, sessionID, 4, "status", map[string]any{"work": "run", "status": "failed", "error": "cancelled"})
	if run := list(e.call(userA, "GET", base+sessionID, nil, 200)["runs"])[0]; run["error"] != "Stopped" {
		t.Fatalf("run after agents' failed: %v", run)
	}
	if after := list(e.call(userA, "GET", base+sessionID+"/events?since=0", nil, 200)["events"]); len(after) != len(events) {
		t.Fatalf("events for a stopped run: %v", after[len(events):])
	}

	// stopped while still in the outbox: never sent; a job that starts anyway is cancelled again
	accepted = e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus",
		"sku_ids": []string{done["sku_id"].(string)}}, 202)
	runID = accepted["run"].(map[string]any)["id"].(string)
	e.call(userA, "POST", base+sessionID+"/runs/"+runID+"/stop", nil, 200)
	if sent := e.relay(); len(sent) != 0 {
		t.Fatalf("stopped run's command sent: %+v", sent)
	}
	late := gen.Envelope{JobID: &runID, OwnerID: userA}
	e.emit(late, sessionID, 1, "status", map[string]any{"work": "run", "status": "running", "error": nil})
	if len(e.cancelled) != 3 || e.cancelled[2] != runID {
		t.Fatalf("cancel broadcasts: %v", e.cancelled)
	}
	if run := list(e.call(userA, "GET", base+sessionID, nil, 200)["runs"])[0]; run["status"] != "failed" || run["error"] != "Stopped" {
		t.Fatalf("late running reopened the run: %v", run)
	}

	// a succeeded run whose command is delivered again is not cancelled, and stays succeeded
	accepted = e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus",
		"sku_ids": []string{done["sku_id"].(string)}}, 202)
	runID = accepted["run"].(map[string]any)["id"].(string)
	command = e.relay()[0]
	for i, status := range []string{"running", "succeeded", "running", "failed"} {
		e.emit(command, sessionID, i+1, "status", map[string]any{"work": "run", "status": status, "error": nil})
	}
	if len(e.cancelled) != 3 {
		t.Fatalf("a succeeded run was cancelled: %v", e.cancelled)
	}
	if run := list(e.call(userA, "GET", base+sessionID, nil, 200)["runs"])[0]; run["id"] != runID || run["status"] != "succeeded" {
		t.Fatalf("succeeded run after a late failed: %v", run)
	}
}

// TestStopImages stops single images and a SKU's set while the run keeps going: only those generating rows go, agents
// is told to skip them (again when the job reports running), finished or stopped ones are 409, and stopping the last
// generating image stops the run whole.
func TestStopImages(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	sku := func(code string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": "rings", "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{}, "confirmed": true, "raw_keys": []string{}}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("R-001"), sku("R-002")}, "found": 2,
		"expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	codes := map[string]string{}
	for _, k := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
		codes[k["id"].(string)] = k["code"].(string)
	}
	accepted := e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "all", "category": "rings"}, 202)
	runID := accepted["run"].(map[string]any)["id"].(string)
	of := map[string][]map[string]any{}
	for _, img := range list(accepted["images"]) {
		code := codes[img["sku_id"].(string)]
		of[code] = append(of[code], img)
	}
	a1, a2, b1, b2 := of["R-001"][0], of["R-001"][1], of["R-002"][0], of["R-002"][1]
	command := e.relay()[0]
	e.emit(command, sessionID, 1, "status", map[string]any{"work": "run", "status": "running", "error": nil})
	key := common.OwnerPrefix(userA) + "catalog/" + sessionID + "/images/" + a1["id"].(string) + ".png"
	e.emit(command, sessionID, 2, "image", map[string]any{"image_id": a1["id"], "sku_id": a1["sku_id"], "status": "to_review",
		"key": key, "review": nil, "attempts": 1, "first_pass": true, "error": nil})

	stop := base + sessionID + "/images/stop"
	ids := func(images ...map[string]any) map[string]any {
		out := []string{}
		for _, img := range images {
			out = append(out, img["id"].(string))
		}
		return map[string]any{"image_ids": out}
	}
	e.call(userB, "POST", stop, ids(a2), 404)
	e.call(userA, "POST", stop, map[string]any{"image_ids": []string{utility.NewID()}}, 404)
	e.call(userA, "POST", stop, map[string]any{"image_ids": []string{}}, 422)

	// one tile: only that image goes, the run keeps going
	one := e.call(userA, "POST", stop, ids(a2), 200)
	if fmt.Sprint(one["removed_image_ids"]) != fmt.Sprint([]any{a2["id"]}) || list(one["runs"])[0]["status"] != "running" ||
		list(one["skus"])[0]["status"] != "To review" {
		t.Fatalf("one image stopped: %v", one)
	}
	if fmt.Sprint(e.cancelled) != fmt.Sprint([]string{runID + " " + a2["id"].(string)}) {
		t.Fatalf("skip broadcasts: %v", e.cancelled)
	}
	e.call(userA, "POST", stop, ids(a2), 409) // stopped already
	e.call(userA, "POST", stop, ids(a1), 409) // finished
	images := map[string]string{}
	for _, img := range list(e.call(userA, "GET", base+sessionID, nil, 200)["images"]) {
		images[img["id"].(string)] = img["status"].(string)
	}
	if fmt.Sprint(images) != fmt.Sprint(map[string]string{a1["id"].(string): "to_review", b1["id"].(string): "generating",
		b2["id"].(string): "generating"}) {
		t.Fatalf("images after one stop: %v", images)
	}

	// the job reports running again (a redelivery, or picked up after the stop): the skip is broadcast again
	e.emit(command, sessionID, 3, "status", map[string]any{"work": "run", "status": "running", "error": nil})
	if len(e.cancelled) != 2 || e.cancelled[1] != e.cancelled[0] {
		t.Fatalf("skip re-broadcast: %v", e.cancelled)
	}

	// a SKU row (and a finished image among the ids): R-002's generating images go; nothing is left, so the run stops whole
	row := e.call(userA, "POST", stop, ids(b1, b2, a1), 200)
	run := list(row["runs"])[0]
	if len(row["removed_image_ids"].([]any)) != 2 || run["status"] != "failed" || run["error"] != "Stopped" ||
		len(list(row["skus"])) != 1 || list(row["skus"])[0]["status"] != "Not run" {
		t.Fatalf("row stopped: %v", row)
	}
	if len(e.cancelled) != 3 || e.cancelled[2] != runID {
		t.Fatalf("whole-run cancel: %v", e.cancelled)
	}

	// stopped while the command waits in the outbox: their shots leave it (R-002 entirely, one of R-001's)
	accepted = e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus",
		"sku_ids": []string{a1["sku_id"].(string), b1["sku_id"].(string)}}, 202)
	of = map[string][]map[string]any{}
	for _, img := range list(accepted["images"]) {
		code := codes[img["sku_id"].(string)]
		of[code] = append(of[code], img)
	}
	e.call(userA, "POST", stop, ids(of["R-002"]...), 200)
	e.call(userA, "POST", stop, ids(of["R-001"][1]), 200)
	var queued struct {
		Skus []struct {
			Code  string `json:"code"`
			Shots []struct {
				ImageID string `json:"image_id"`
			} `json:"shots"`
		} `json:"skus"`
	}
	sent := e.relay()
	if len(sent) != 1 || json.Unmarshal(sent[0].Data, &queued) != nil || len(queued.Skus) != 1 || queued.Skus[0].Code != "R-001" ||
		len(queued.Skus[0].Shots) != 1 || queued.Skus[0].Shots[0].ImageID != of["R-001"][0]["id"] {
		t.Fatalf("command after stops: %s", sent[0].Data)
	}
}

// TestCatalogPosesAndTries: pose ratios and notes round-trip through PATCH /skus and reach each shot of
// cmd.catalog.run from that shot's own SKU; every render try is stored (owner's keys only) and signed; a regeneration
// renders at the ratio asked for; the library carries each product's pose noun.
func TestCatalogPosesAndTries(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	sku := func(code string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": "rings", "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{}, "confirmed": true, "raw_keys": []string{}}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("R-001"), sku("R-002")}, "found": 2,
		"expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	ids := map[string]string{}
	for _, s := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
		ids[s["code"].(string)] = s["id"].(string)
		if brief := s["brief"].(map[string]any); fmt.Sprint(brief["pose_ratios"], brief["pose_notes"]) != "map[] map[]" {
			t.Fatalf("empty pose maps: %v", brief)
		}
	}

	// pose ratios and notes: validated against the library, replaced whole, kept on the brief
	skuPath := "/v1/catalog/skus/" + ids["R-001"]
	e.call(userA, "PATCH", skuPath, map[string]any{"pose_ratios": map[string]string{"ring-packshot": "16:9"}}, 422)
	e.call(userA, "PATCH", skuPath, map[string]any{"pose_ratios": map[string]string{"ring-packshot": "wide"}}, 422)
	e.call(userA, "PATCH", skuPath, map[string]any{"pose_ratios": map[string]string{"sofa-room": "4:3"}}, 422)
	e.call(userA, "PATCH", skuPath, map[string]any{"pose_notes": map[string]string{"ring-packshot": strings.Repeat("x", 501)}}, 422)
	e.call(userA, "PATCH", skuPath, map[string]any{"pose_notes": map[string]string{"ring-packshot": "   "}}, 422)
	e.call(userB, "PATCH", skuPath, map[string]any{"pose_notes": map[string]string{"ring-packshot": "top view"}}, 404)
	patched := list(e.call(userA, "PATCH", skuPath, map[string]any{"pose_ratios": map[string]string{"ring-hand-close-up": "4:3"},
		"pose_notes": map[string]string{"ring-packshot": "top view"}}, 200)["skus"])
	if brief := patched[0]["brief"].(map[string]any); fmt.Sprint(brief["pose_ratios"], brief["pose_notes"]) !=
		"map[ring-hand-close-up:4:3] map[ring-packshot:top view]" {
		t.Fatalf("patched brief: %v", brief)
	}

	// each shot: its SKU's pose ratio, else its brief ratio, and its pose note
	accepted := e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "all", "category": "rings"}, 202)
	type shot struct {
		ImageID string `json:"image_id"`
		PoseID  string `json:"pose_id"`
		Ratio   string `json:"ratio"`
		Notes   string `json:"notes"`
	}
	var run struct {
		Ratio string `json:"ratio"`
		Skus  []struct {
			Code  string `json:"code"`
			Shots []shot `json:"shots"`
		} `json:"skus"`
	}
	commands = e.relay()
	if len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil || run.Ratio != "1:1" || len(run.Skus) != 2 {
		t.Fatalf("run command: %s", commands[0].Data)
	}
	got := map[string]string{}
	for _, s := range run.Skus {
		for _, sh := range s.Shots {
			got[s.Code+"/"+sh.PoseID] = sh.Ratio + "|" + sh.Notes
		}
	}
	want := map[string]string{"R-001/ring-hand-close-up": "4:3|", "R-001/ring-packshot": "1:1|top view",
		"R-002/ring-hand-close-up": "1:1|", "R-002/ring-packshot": "1:1|"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("shots: %v, want %v", got, want)
	}

	// tries: every one stored under the owner's prefix and signed; a try under another owner's prefix is refused
	images := list(accepted["images"])
	prefix := common.OwnerPrefix(userA) + "catalog/" + sessionID + "/images/"
	review := map[string]any{"passed": true, "summary": "ok", "goals": []any{}}
	// while generating: a progress event stores the tries so far on the image, which stays generating
	live := images[0]["id"].(string)
	e.emit(commands[0], sessionID, 50, "image", map[string]any{"image_id": live, "sku_id": images[0]["sku_id"],
		"status": "generating", "key": nil, "review": nil, "attempts": 1, "first_pass": false, "error": nil,
		"attempts_detail": []any{map[string]any{"n": 1, "key": prefix + live + "/attempt_1.png", "review": nil,
			"failed_goals": 0, "best": false}}})
	for _, img := range list(e.call(userA, "GET", base+sessionID, nil, 200)["images"]) {
		if img["id"] == live && (img["status"] != "generating" || img["url"] != nil || len(list(img["attempts"])) != 1 ||
			!strings.Contains(list(img["attempts"])[0]["url"].(string), "/attempt_1.png?")) {
			t.Fatalf("a try while generating: %v", img)
		}
	}
	for i, img := range images {
		id := img["id"].(string)
		tries := []any{
			map[string]any{"n": 1, "key": prefix + id + "/attempt_1.png", "review": nil, "failed_goals": 1, "best": false},
			map[string]any{"n": 2, "key": prefix + id + "/attempt_2.png", "review": review, "failed_goals": 0, "best": true}}
		if i == 1 {
			tries[0].(map[string]any)["key"] = common.OwnerPrefix(userB) + "catalog/x/images/" + id + "/attempt_1.png"
		}
		e.emit(commands[0], sessionID, i+1, "image", map[string]any{"image_id": id, "sku_id": img["sku_id"], "status": "to_review",
			"key": prefix + id + ".png", "review": review, "attempts": 2, "first_pass": false, "attempts_detail": tries, "error": nil})
	}
	e.call(userB, "GET", base+sessionID, nil, 404)
	byID := map[string]map[string]any{}
	for _, img := range list(e.call(userA, "GET", base+sessionID, nil, 200)["images"]) {
		byID[img["id"].(string)] = img
	}
	first, refused := byID[images[0]["id"].(string)], byID[images[1]["id"].(string)]
	attempts := list(first["attempts"])
	if len(attempts) != 2 || attempts[0]["index"].(float64) != 1 || attempts[0]["best"] != false || attempts[0]["review"] != nil ||
		attempts[1]["best"] != true || attempts[1]["review"].(map[string]any)["passed"] != true ||
		!strings.Contains(attempts[1]["url"].(string), "/files/"+prefix+images[0]["id"].(string)+"/attempt_2.png?") ||
		!strings.Contains(attempts[1]["url"].(string), "sig=") {
		t.Fatalf("attempts: %v", attempts)
	}
	if refused["status"] != "generating" || len(refused["attempts"].([]any)) != 0 {
		t.Fatalf("image with another owner's try key: %v", refused)
	}
	// a progress event that comes after the final one changes nothing
	e.emit(commands[0], sessionID, 51, "image", map[string]any{"image_id": live, "sku_id": images[0]["sku_id"],
		"status": "generating", "key": nil, "review": nil, "attempts": 1, "first_pass": false, "error": nil,
		"attempts_detail": []any{map[string]any{"n": 1, "key": prefix + live + "/attempt_9.png", "review": nil,
			"failed_goals": 0, "best": false}}})
	for _, img := range list(e.call(userA, "GET", base+sessionID, nil, 200)["images"]) {
		if img["id"] == live && (img["status"] != "to_review" || len(list(img["attempts"])) != 2) {
			t.Fatalf("a late try: %v", img)
		}
	}

	// the brand picks the other try: it becomes the image (url, review), best and picked; an approved image stays approved
	pick := "/v1/catalog/images/" + first["id"].(string) + "/pick"
	e.call(userA, "PATCH", "/v1/catalog/images/"+first["id"].(string), map[string]any{"status": "approved"}, 200)
	e.call(userB, "POST", pick, map[string]any{"attempt": 1}, 404)
	e.call(userA, "POST", pick, map[string]any{"attempt": 3}, 422)
	e.call(userA, "POST", pick, map[string]any{"attempt": 0}, 422)
	e.call(userA, "POST", "/v1/catalog/images/"+refused["id"].(string)+"/pick", map[string]any{"attempt": 1}, 409)
	picked := e.call(userA, "POST", pick, map[string]any{"attempt": 1}, 200)["image"].(map[string]any)
	attempts = list(picked["attempts"])
	if picked["status"] != "approved" || picked["review"] != nil || !strings.Contains(picked["url"].(string), "/attempt_1.png?") ||
		attempts[0]["best"] != true || attempts[0]["picked"] != true || attempts[1]["best"] != false || attempts[1]["picked"] != nil {
		t.Fatalf("picked: %v", picked)
	}
	e.emit(commands[0], sessionID, len(images)+1, "status", map[string]any{"work": "run", "status": "succeeded", "error": nil})

	// regenerate at another ratio: validated, then every new shot renders at it
	regenerate := base + sessionID + "/regenerate"
	one := []string{images[0]["id"].(string)}
	e.call(userA, "POST", regenerate, map[string]any{"image_ids": one, "feedback": "brighter", "ratio": "16:9"}, 422)
	e.call(userA, "POST", regenerate, map[string]any{"image_ids": one, "feedback": "brighter", "ratio": "wide"}, 422)
	e.call(userB, "POST", regenerate, map[string]any{"image_ids": one, "feedback": "brighter", "ratio": "4:3"}, 404)
	e.call(userA, "POST", regenerate, map[string]any{"image_ids": one, "feedback": "brighter", "ratio": "4:3"}, 202)
	run.Skus = nil
	if commands = e.relay(); len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil ||
		len(run.Skus) != 1 || run.Skus[0].Shots[0].Ratio != "4:3" {
		t.Fatalf("regenerate command: %s", commands[0].Data)
	}

	// the library names each product's poses
	nouns := map[string]any{}
	for _, category := range list(e.call(userA, "GET", "/v1/catalog/library", nil, 200)["categories"]) {
		nouns[category["id"].(string)] = category["pose_noun"]
	}
	if fmt.Sprint(nouns) != "map[mens-rings:map[plural:poses singular:pose] rings:map[plural:poses singular:pose] sofas:map[plural:angles singular:angle]]" {
		t.Fatalf("pose nouns: %v", nouns)
	}
}

// TestGarmentMigration: 00006 moves stored kurti and kurta-pant ids to tops and sets, merging a SKU's two old shots
// of one pose (newest live) and two favorites of one pose.
func TestGarmentMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the system role is a session setting
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	if err := goose.UpTo(db, ".", 5); err != nil {
		t.Fatal(err)
	}
	exec(`SELECT set_config('app.role', 'system', false)`)
	exec(`INSERT INTO catalog_sessions (id, owner_id, project_name, inputs) VALUES ('00000000-0000-4000-8000-000000000001', '` + userA + `', 'p',
		'{"poses": {"items": [{"id": "1", "option": "pick", "label": "kurti-01", "poses": {"kurtis": ["kurti-full-front"],
		"kurta-pant": ["kurta-pant-without-dupatta-flat-lay"]}}], "skipped": false}}')`)
	exec(`INSERT INTO catalog_skus (id, owner_id, session_id, code, name, category, subcategory, group_key, group_label, attributes, brief) VALUES
		('00000000-0000-4000-8000-000000000002', '` + userA + `', '00000000-0000-4000-8000-000000000001', 'KP-1', 'n', 'kurta-pant', 'anarkali',
		 'kurta-pant', 'Kurta pant without dupatta', '{}', '{"poses": ["kurta-pant-without-dupatta-flat-lay"], "pose_ratios": {"kurta-pant-without-dupatta-flat-lay": "4:5"}}'),
		('00000000-0000-4000-8000-000000000003', '` + userA + `', '00000000-0000-4000-8000-000000000001', 'KT-1', 'n', 'kurtis', 'tunic',
		 NULL, 'Kurtis / tops', '{}', '{"poses": ["kurti-full-front"]}')`)
	exec(`INSERT INTO catalog_runs (id, owner_id, session_id, scope, category, sku_ids, image_ids, credits_quoted) VALUES
		('00000000-0000-4000-8000-000000000004', '` + userA + `', '00000000-0000-4000-8000-000000000001', 'sample', 'kurta-pant-dupatta', '{}', '{}', 0)`)
	exec(`INSERT INTO catalog_images (id, owner_id, session_id, sku_id, run_id, shot, revision, created_at) VALUES
		('00000000-0000-4000-8000-000000000005', '` + userA + `', '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000002',
		 '00000000-0000-4000-8000-000000000004', 'kurta-pant-with-dupatta-flat-lay', 1, now() - interval '1 hour'),
		('00000000-0000-4000-8000-000000000006', '` + userA + `', '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000002',
		 '00000000-0000-4000-8000-000000000004', 'kurta-pant-without-dupatta-flat-lay', 1, now())`)
	exec(`INSERT INTO catalog_favorites (owner_id, kind, item_id) VALUES ('` + userA + `', 'poses', 'kurta-pant-with-dupatta-flat-lay'),
		('` + userA + `', 'poses', 'kurta-pant-without-dupatta-flat-lay'), ('` + userA + `', 'poses', 'ring-on-hand')`)
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow(`SELECT (SELECT string_agg(concat_ws(' ', category, subcategory, group_key, group_label, brief::text), ' | ' ORDER BY code) FROM catalog_skus)
		|| ' | ' || (SELECT string_agg(shot || ':' || revision, ',' ORDER BY created_at) FROM catalog_images)
		|| ' | ' || (SELECT category FROM catalog_runs)
		|| ' | ' || (SELECT string_agg(item_id, ',' ORDER BY item_id) FROM catalog_favorites)
		|| ' | ' || (SELECT inputs->'poses'->'items'->0 FROM catalog_sessions)::text`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	want := `sets kurta-sets sets Sets {"poses": ["set-flat-lay"], "pose_ratios": {"set-flat-lay": "4:5"}} | ` +
		`tops tunics Tops {"poses": ["top-full-front"]} | set-flat-lay:1,set-flat-lay:2 | sets | ring-on-hand,set-flat-lay | ` +
		`{"id": "1", "label": "kurti-01", "poses": {"sets": ["set-flat-lay"], "tops": ["top-full-front"]}, "option": "pick"}`
	if got != want {
		t.Fatalf("migrated:\n%s\nwant:\n%s", got, want)
	}
}

// TestJewelleryMigration: 00007 maps every jewellery SKU's old style subcategory to its group's type (a necklace's
// mangalsutra and a pendant's charm to their own), keeps null and a new type, renames the batches named after renamed groups and
// makes garment_kind questions kind questions.
func TestJewelleryMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the system role is a session setting
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	if err := goose.UpTo(db, ".", 6); err != nil {
		t.Fatal(err)
	}
	exec(`SELECT set_config('app.role', 'system', false)`)
	exec(`INSERT INTO catalog_sessions (id, owner_id, project_name, inputs) VALUES ('00000000-0000-4000-8000-000000000001', '` + userA + `', 'p', '{}')`)
	sku := func(n int, code, category, subcategory, label string) string {
		return fmt.Sprintf(`('00000000-0000-4000-8000-%012d', '%s', '00000000-0000-4000-8000-000000000001', '%s', 'n', '%s', %s, %s, '{}', '{}')`,
			n, userA, code, category, subcategory, label)
	}
	exec(`INSERT INTO catalog_skus (id, owner_id, session_id, code, name, category, subcategory, group_label, attributes, brief) VALUES ` +
		strings.Join([]string{
			sku(2, "R-001", "rings", "'solitaire'", "'Statement & solitaires'"),
			sku(3, "MR-001", "mens-rings", "'signet'", "NULL"),
			sku(4, "E-001", "earrings", "'jhumka'", "NULL"),
			sku(5, "N-001", "necklaces", "'mangalsutra'", "'Necklaces'"),
			sku(6, "N-002", "necklaces", "'choker'", "NULL"),
			sku(7, "NS-001", "necklace-sets", "'bridal'", "'Necklace sets'"),
			sku(8, "PD-001", "pendants", "'charm'", "'Pendants with chain'"),
			sku(9, "PD-002", "pendants", "'enamel'", "NULL"),
			sku(10, "B-001", "bracelets", "'tennis'", "NULL"),
			sku(11, "BG-001", "bangles", "'kada'", "NULL"),
			sku(12, "BG-002", "bangles", "NULL", "NULL"),
			sku(13, "A-001", "anklets", "'charm'", "NULL"),
			sku(14, "TP-001", "tops", "'kurtis'", "NULL"),
			sku(16, "R-002", "rings", "'diamond-rings'", "NULL"),
		}, ", "))
	exec(`INSERT INTO catalog_questions (id, owner_id, session_id, key, check_id, text, options, blocking) VALUES
		('00000000-0000-4000-8000-000000000015', '` + userA + `', '00000000-0000-4000-8000-000000000001', 'garment_kind:tp-001',
		 'garment_kind', 'q', '[]', true)`)
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow(`SELECT string_agg(concat_ws(' ', code, coalesce(subcategory, '-'), group_label), ' | ' ORDER BY id)
		|| ' | ' || (SELECT check_id || ' ' || key FROM catalog_questions) FROM catalog_skus`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	want := `R-001 rings Statement & solitaires | MR-001 mens-rings | E-001 earrings | N-001 mangalsutra Necklaces & chains | ` +
		`N-002 necklaces | NS-001 jewellery-sets Jewellery sets | PD-001 charms Pendants | PD-002 pendants | ` +
		`B-001 bracelets | BG-001 bangles | BG-002 - | A-001 anklets | TP-001 kurtis | R-002 diamond-rings | kind kind:tp-001`
	if got != want {
		t.Fatalf("migrated:\n%s\nwant:\n%s", got, want)
	}
}

// TestFurnitureMigration: 00009 maps every furniture SKU's old style subcategory to its group's type, keeps null and
// a new type and another family's ids, and renames the Side tables batch to Tables.
func TestFurnitureMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the system role is a session setting
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	if err := goose.UpTo(db, ".", 8); err != nil {
		t.Fatal(err)
	}
	exec(`SELECT set_config('app.role', 'system', false)`)
	exec(`INSERT INTO catalog_sessions (id, owner_id, project_name, inputs) VALUES ('00000000-0000-4000-8000-000000000001', '` + userA + `', 'p', '{}')`)
	sku := func(n int, code, category, subcategory, label string) string {
		return fmt.Sprintf(`('00000000-0000-4000-8000-%012d', '%s', '00000000-0000-4000-8000-000000000001', '%s', 'n', '%s', %s, %s, '{}', '{}')`,
			n, userA, code, category, subcategory, label)
	}
	exec(`INSERT INTO catalog_skus (id, owner_id, session_id, code, name, category, subcategory, group_label, attributes, brief) VALUES ` +
		strings.Join([]string{
			sku(2, "SO-001", "sofas", "'three-seater'", "'Sofas'"),
			sku(3, "SO-002", "sofas", "'sectional'", "NULL"),
			sku(4, "SO-003", "sofas", "'recliner'", "NULL"),
			sku(5, "BD-001", "beds", "'queen'", "NULL"),
			sku(6, "BD-002", "beds", "'storage'", "NULL"),
			sku(7, "ST-001", "side-tables", "'round'", "'Side tables'"),
			sku(8, "ST-002", "side-tables", "'nesting'", "NULL"),
			sku(9, "ST-003", "side-tables", "'bedside'", "NULL"),
			sku(10, "TV-001", "tv-units", "'wall-mounted'", "NULL"),
			sku(11, "TV-002", "tv-units", "NULL", "NULL"),
			sku(12, "SO-004", "sofas", "'sofa-beds'", "NULL"),
			sku(13, "R-001", "rings", "'rings'", "NULL"),
		}, ", "))
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow(`SELECT string_agg(concat_ws(' ', code, coalesce(subcategory, '-'), group_label), ' | ' ORDER BY id)
		FROM catalog_skus`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	want := `SO-001 sofas Sofas | SO-002 sectional-sofas | SO-003 recliners | BD-001 beds | BD-002 storage-beds | ` +
		`ST-001 side-tables Tables | ST-002 nesting-tables | ST-003 bedside-tables | TV-001 tv-units | TV-002 - | ` +
		`SO-004 sofa-beds | R-001 rings`
	if got != want {
		t.Fatalf("migrated:\n%s\nwant:\n%s", got, want)
	}
}

// TestDescribedChoices: a models or backgrounds step item needs picks or a description (trimmed, at most 500
// characters); Start puts the step text on every SKU, and with no pick the model or background stays null; a run sends
// a described-only choice as null with its description; a SKU edit sets or clears its own description.
func TestDescribedChoices(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1, "jewellery", "furniture"); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	var process struct {
		Ingest []struct{ Keys []string } `json:"ingest"`
	}
	_ = json.Unmarshal(commands[0].Data, &process)
	item := func(ids []string, text string) map[string]any {
		return map[string]any{"items": []any{map[string]any{"id": "1", "option": "library", "label": "x", "ids": ids, "text": text}}}
	}
	patch := func(step string, body map[string]any, want int) {
		e.call(userA, "PATCH", base+sessionID, map[string]any{"inputs": map[string]any{step: body}}, want)
	}
	patch("models", item([]string{}, "   "), 422)                         // neither picks nor a description
	patch("backgrounds", item([]string{}, strings.Repeat("a", 501)), 422) // too long
	patch("models", item([]string{}, "  South Indian woman, late 20s  "), 200)
	patch("backgrounds", item([]string{"living-room"}, "sunlit terrace, palm shadows"), 200) // suits sofas only
	sku := func(code, category string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": category, "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{"description": "d"}, "confirmed": true,
			"raw_keys": process.Ingest[0].Keys[:1]}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("R-001", "rings"), sku("SO-001", "sofas")},
		"found": 2, "expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	for _, recheck := range e.relay() { // the input changes re-ran the checks
		e.emit(recheck, sessionID, 1, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	}
	briefs, ids := map[string]map[string]any{}, map[string]string{}
	for _, s := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
		briefs[s["code"].(string)], ids[s["code"].(string)] = s["brief"].(map[string]any), s["id"].(string)
	}
	if r := briefs["R-001"]; r["model"] != nil || r["model_description"] != "South Indian woman, late 20s" ||
		r["background"] != "white" || r["background_description"] != "sunlit terrace, palm shadows" { // the default + the text
		t.Fatalf("ring brief: %v", r)
	}
	if s := briefs["SO-001"]; s["model"] != nil || s["model_description"] != nil || s["background"] != "living-room" {
		t.Fatalf("sofa brief: %v", s) // no model for furniture; the description still goes
	}
	e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["SO-001"], map[string]any{"model_description": "a man"}, 422)
	e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["R-001"], map[string]any{"background_description": strings.Repeat("a", 501)}, 422)

	e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "sample", "category": "rings"}, 202)
	var run struct {
		Skus []struct {
			Model                 *json.RawMessage `json:"model"`
			ModelDescription      *string          `json:"model_description"`
			Background            *struct{ ID string }
			BackgroundDescription *string `json:"background_description"`
		} `json:"skus"`
	}
	if commands = e.relay(); len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil || run.Skus[0].Model != nil ||
		run.Skus[0].ModelDescription == nil || run.Skus[0].Background == nil || run.Skus[0].Background.ID != "white" ||
		run.Skus[0].BackgroundDescription == nil {
		t.Fatalf("run command: %s", commands[0].Data)
	}

	// the SKU's own descriptions: set (trimmed), and blank clears it
	edited := list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["SO-001"], map[string]any{"background_description": "  dusk  "}, 200)["skus"])
	if b := edited[0]["brief"].(map[string]any); b["background_description"] != "dusk" {
		t.Fatalf("set description: %v", b)
	}
	edited = list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["SO-001"], map[string]any{"background_description": nil}, 200)["skus"])
	if b := edited[0]["brief"].(map[string]any); b["background_description"] != nil || b["background"] != "living-room" {
		t.Fatalf("cleared description: %v", b)
	}

	// clearing the only model choice (a description) gives the SKU its product's default model, so it can still run
	edited = list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["R-001"], map[string]any{"model_description": nil}, 200)["skus"])
	if b := edited[0]["brief"].(map[string]any); b["model"] != "m001" || b["model_description"] != nil {
		t.Fatalf("cleared model description: %v", b)
	}
	// a SKU moved onto a product with a model gets the models step's description (described only: no pick)
	moved := list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["SO-001"], map[string]any{"category": "rings"}, 200)["skus"])
	if b := moved[0]["brief"].(map[string]any); b["model"] != nil || b["model_description"] != "South Indian woman, late 20s" {
		t.Fatalf("moved SKU: %v", b)
	}
}

// TestCustomShots: a poses item's custom shots and reference photos are checked against the item and the session,
// Start puts the brief (links removed) on the product's SKUs as shots_brief, a run carries a custom shot's prompt and
// its reference photo, and a SKU edit accepts the custom shot.
func TestCustomShots(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if code, body := e.rawUpload(userA, sessionID, 1, "jewellery"); code != 202 {
		t.Fatalf("raw: %d %s", code, body)
	}
	commands := e.relay()
	var process struct {
		Ingest []struct{ Keys []string } `json:"ingest"`
	}
	_ = json.Unmarshal(commands[0].Data, &process)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("kind", "pose_ref")
	part, _ := writer.CreateFormFile("file", "swing.png")
	_, _ = part.Write(pngBytes())
	_ = writer.Close()
	code, body := e.do(userA, "POST", base+sessionID+"/uploads", &buf, writer.FormDataContentType())
	var upload struct{ ID string }
	if code != 201 || json.Unmarshal(body, &upload) != nil {
		t.Fatalf("pose_ref upload: %d %s", code, body)
	}
	swing := map[string]any{"id": "custom-ring-on-a-swing", "category": "rings", "name": "On a swing",
		"prompt": "Sitting on a garden swing, the ring hand on the rope", "on_model": true}
	item := func(poses []string, custom []any, refs map[string]string, brief string) map[string]any {
		return map[string]any{"inputs": map[string]any{"poses": map[string]any{"items": []any{map[string]any{
			"id": "1", "option": "brief", "label": "x", "poses": map[string]any{"rings": poses}, "custom_poses": custom,
			"pose_refs": refs, "briefs": map[string]string{"rings": brief}, "upload_ids": []string{upload.ID}}}}}}
	}
	plan := []string{"ring-packshot", "custom-ring-on-a-swing"}
	e.call(userA, "PATCH", base+sessionID, item(plan, nil, nil, ""), 422)                                                                 // an undefined custom shot
	e.call(userA, "PATCH", base+sessionID, item(plan[:1], []any{swing}, map[string]string{upload.ID: "custom-ring-on-a-swing"}, ""), 422) // a ref to an unplanned shot
	e.call(userA, "PATCH", base+sessionID, item(plan, []any{swing}, map[string]string{upload.ID: "custom-ring-on-a-swing"},
		"one swing shot,\n like this photo https://example.com/look"), 200)
	sku := func(code string) map[string]any {
		return map[string]any{"code": code, "name": code, "category": "rings", "subcategory": nil, "group_key": nil,
			"group_label": nil, "variant_of": nil, "attributes": map[string]any{"description": "d"}, "confirmed": true,
			"raw_keys": process.Ingest[0].Keys[:1]}
	}
	e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": []any{sku("R-001"), sku("R-002")}, "found": 2,
		"expected": nil, "rules": nil})
	e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	for _, recheck := range e.relay() {
		e.emit(recheck, sessionID, 1, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	}
	ids := map[string]string{}
	for _, s := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
		ids[s["code"].(string)] = s["id"].(string)
		if b := s["brief"].(map[string]any); fmt.Sprint(b["poses"]) != "[ring-packshot custom-ring-on-a-swing]" ||
			b["shots_brief"] != "one swing shot, like this photo" {
			t.Fatalf("brief: %v", b)
		}
	}
	e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus", "sku_ids": []string{ids["R-001"]}}, 202)
	var run struct {
		Skus []struct {
			ShotsBrief string `json:"shots_brief"`
			Shots      []struct {
				PoseID     string   `json:"pose_id"`
				PoseName   string   `json:"pose_name"`
				Prompt     string   `json:"prompt"`
				OnModel    *bool    `json:"on_model"`
				References []string `json:"references"`
			} `json:"shots"`
		} `json:"skus"`
	}
	if commands = e.relay(); len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil {
		t.Fatalf("run command: %v", commands)
	}
	shots := run.Skus[0].Shots
	if run.Skus[0].ShotsBrief == "" || shots[0].Prompt != "" || shots[0].OnModel != nil || len(shots[0].References) != 0 ||
		shots[1].PoseName != "On a swing" || shots[1].Prompt != swing["prompt"] || shots[1].OnModel == nil ||
		len(shots[1].References) != 1 || !strings.HasSuffix(shots[1].References[0], ".png") {
		t.Fatalf("run command: %s", commands[0].Data)
	}
	e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["R-002"], map[string]any{"poses": []string{"custom-ring-on-a-swing"}}, 200)

	// one-line words only, and never a library pose's name
	bad := maps.Clone(swing)
	bad["prompt"] = "swing\nExactly 9 stones"
	e.call(userA, "PATCH", base+sessionID, item(plan, []any{bad}, nil, ""), 422)
	bad = maps.Clone(swing)
	bad["name"] = "packshot"
	e.call(userA, "PATCH", base+sessionID, item(plan, []any{bad}, nil, ""), 422)

	// the poses step changes: R-002 keeps its custom shot's definition, so it still runs with its prompt
	e.call(userA, "PATCH", base+sessionID, item([]string{"ring-packshot"}, nil, nil, ""), 200)
	for _, recheck := range e.relay() {
		e.emit(recheck, sessionID, 1, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
	}
	e.call(userA, "POST", base+sessionID+"/runs", map[string]any{"scope": "skus", "sku_ids": []string{ids["R-002"]}}, 202)
	if commands = e.relay(); len(commands) != 1 || json.Unmarshal(commands[0].Data, &run) != nil ||
		run.Skus[0].Shots[0].Prompt != swing["prompt"] {
		t.Fatalf("run after the poses changed: %v", commands)
	}
}

// TestWearerModels: a SKU's attributes.wearer picks its model at Start (only the picks of its wearer, randomized within
// them, else the product's default for it: the men's for men's wear; a description of the other wear's look gives way
// to that default), Apply to all never crosses wears, a move to a men's product makes the SKU men's wear, and the
// drawer's wearer edit sets it and swaps a model of the other wearer for the new wearer's.
func TestWearerModels(t *testing.T) {
	e := setup(t)
	base := "/v1/catalog/sessions/"
	// start is a session of rings with these wearers (nil = unknown) and models step: code → (brief, id)
	start := func(models map[string]any, wearers ...any) (map[string]map[string]any, map[string]string) {
		sessionID := e.call(userA, "POST", "/v1/catalog/sessions", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
		if code, body := e.rawUpload(userA, sessionID, 1, "jewellery"); code != 202 {
			t.Fatalf("raw: %d %s", code, body)
		}
		commands := e.relay()
		var process struct {
			Ingest []struct{ Keys []string } `json:"ingest"`
		}
		_ = json.Unmarshal(commands[0].Data, &process)
		var skus []any
		for n, wearer := range wearers {
			skus = append(skus, map[string]any{"code": fmt.Sprintf("R-%03d", n+1), "name": "ring", "category": "rings",
				"subcategory": nil, "group_key": nil, "group_label": nil, "variant_of": nil,
				"attributes": map[string]any{"description": "d", "wearer": wearer}, "confirmed": true,
				"raw_keys": process.Ingest[0].Keys[:1]})
		}
		e.emit(commands[0], sessionID, 1, "skus", map[string]any{"skus": skus, "found": len(skus), "expected": nil, "rules": nil})
		e.emit(commands[0], sessionID, 2, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
		e.call(userA, "PATCH", base+sessionID, map[string]any{"inputs": map[string]any{"models": models}}, 200)
		for _, recheck := range e.relay() {
			e.emit(recheck, sessionID, 1, "status", map[string]any{"work": "process", "status": "succeeded", "error": nil})
		}
		briefs, ids := map[string]map[string]any{}, map[string]string{}
		for _, s := range list(e.call(userA, "POST", base+sessionID+"/start", nil, 200)["skus"]) {
			briefs[s["code"].(string)], ids[s["code"].(string)] = s["brief"].(map[string]any), s["id"].(string)
		}
		return briefs, ids
	}
	item := func(ids []string, text string) map[string]any {
		return map[string]any{"items": []any{map[string]any{"id": "1", "option": "library", "label": "x", "ids": ids,
			"randomize": len(ids) > 1, "text": text}}}
	}

	// a women's and a men's pick, randomized: each SKU gets one of its own wear, rotating within that wear
	briefs, ids := start(item([]string{"m001", "m018"}, ""), "men", nil, "women")
	if briefs["R-001"]["model"] != "m018" || briefs["R-002"]["model"] != "m001" || briefs["R-003"]["model"] != "m001" {
		t.Fatalf("models by wearer: %v", briefs)
	}
	// Apply to all puts a women's model only on the SKUs it suits
	for _, s := range list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["R-003"],
		map[string]any{"model": "m001", "apply_to_category": true}, 200)["skus"]) {
		want := "m001"
		if s["code"] == "R-001" { // men's wear keeps its men's model
			want = "m018"
		}
		if s["brief"].(map[string]any)["model"] != want {
			t.Fatalf("apply to all crossed wears: %v", s)
		}
	}
	// a move to a men's product makes the SKU men's wear, on a men's model
	moved := list(e.call(userA, "PATCH", "/v1/catalog/skus/"+ids["R-002"], map[string]any{"category": "mens-rings"}, 200)["skus"])[0]
	if moved["attributes"].(map[string]any)["wearer"] != "men" || moved["brief"].(map[string]any)["model"] != "m018" {
		t.Fatalf("moved to men's rings: %v", moved)
	}

	// the drawer: a bad wearer is a 422; women → men swaps the women's model for a men's one, and back
	path := "/v1/catalog/skus/" + ids["R-003"]
	e.call(userA, "PATCH", path, map[string]any{"wearer": "kids"}, 422)
	edited := list(e.call(userA, "PATCH", path, map[string]any{"wearer": "men"}, 200)["skus"])[0]
	if edited["attributes"].(map[string]any)["wearer"] != "men" || edited["brief"].(map[string]any)["model"] != "m018" {
		t.Fatalf("set men: %v", edited)
	}
	edited = list(e.call(userA, "PATCH", path, map[string]any{"wearer": "women"}, 200)["skus"])[0]
	if edited["attributes"].(map[string]any)["wearer"] != "women" || edited["brief"].(map[string]any)["model"] != "m001" {
		t.Fatalf("set women: %v", edited)
	}
	edited = list(e.call(userA, "PATCH", path, map[string]any{"wearer": nil}, 200)["skus"])[0]
	if edited["attributes"].(map[string]any)["wearer"] != nil || edited["brief"].(map[string]any)["model"] != "m001" {
		t.Fatalf("clear wearer: %v", edited)
	}

	// a women's pick, or a description of a woman, gives a men's SKU its men's default photo; a neutral one stays
	briefs, _ = start(item([]string{"m001"}, ""), "men")
	if briefs["R-001"]["model"] != "m018" {
		t.Fatalf("women's pick on men's wear: %v", briefs)
	}
	briefs, _ = start(item(nil, "a woman in her 20s"), "men", "women")
	if b := briefs["R-001"]; b["model"] != "m018" || b["model_description"] != nil {
		t.Fatalf("a woman described for men's wear: %v", b)
	}
	if b := briefs["R-002"]; b["model"] != nil || b["model_description"] != "a woman in her 20s" {
		t.Fatalf("a woman described for women's wear: %v", b)
	}
	briefs, _ = start(item(nil, "South Indian appearance"), "men")
	if b := briefs["R-001"]; b["model"] != nil || b["model_description"] != "South Indian appearance" {
		t.Fatalf("a neutral description: %v", b)
	}
}
