// Integration tests of looks (contracts/README.md "Looks"): own looks in the library with isolation, starter looks
// from the live registry (read-only), fit, "Save as look", runs with looks (the resolved graph, made_with, reuse
// after an edit, a delete and a pack that drops a look), the dispatcher seeing look LoRAs, and batches and pipelines.
package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

// looks publishes the fake runner's starter looks (echo/soft-light, echo/grain with a LoRA no host has).
func (e *env) looks() {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "looks.valid.json"))
	if err == nil {
		err = e.registry.SetRegistry("fake.fake.looks", data)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

// qwenLooks publishes the torch runner's starter looks on host mac.
func (e *env) qwenLooks() {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "qwen-image.looks.valid.json"))
	if err == nil {
		err = e.registry.SetRegistry("mac.torch.looks", data)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

// look posts a look of owner's and returns it.
func (e *env) look(owner, name, text string, takes ...string) map[string]any {
	e.t.Helper()
	images := []any{}
	for _, id := range takes {
		images = append(images, map[string]any{"take_id": id})
	}
	return e.call(owner, "POST", library, map[string]any{"kind": "look", "group": "film", "name": name, "text": text,
		"images": images}, 201)
}

// ids lists the ids of a library list.
func ids(list map[string]any) []string {
	var out []string
	for _, item := range list["items"].([]any) {
		out = append(out, item.(map[string]any)["id"].(string))
	}
	return out
}

// takeOf ends a run with one output and returns its take.
func (e *env) takeOf(owner, sessionID, runID string) string {
	e.t.Helper()
	e.emit(owner, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(owner, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + owner + "/runs/" + runID + "/1/0.png",
		"kind": "image", "width": 64, "height": 64, "bytes": 10})
	return e.call(owner, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
}

func TestLooksLibrary(t *testing.T) {
	e := setup(t)
	e.looks()
	e.qwen()
	e.shared()
	one := e.image(userA)
	noir := e.look(userA, " Noir ", " black and white ", one)
	id := noir["id"].(string)
	if noir["kind"] != "look" || noir["handle"] != nil || noir["source"] != "mine" || noir["family"] != nil ||
		noir["text"] != "black and white" || noir["from_take_id"] != nil || len(noir["loras"].([]any)) != 0 ||
		noir["thumb_url"] != noir["images"].([]any)[0].(map[string]any)["thumb_url"] {
		t.Fatal(noir)
	}
	if needs, _ := json.Marshal(noir["needs"]); string(needs) != `["text","images"]` {
		t.Fatal(string(needs))
	}
	mine, _ := e.upload(userA, "sketch.safetensors", loraFile(t, "kohya"), 200)
	theirs, _ := e.upload(userB, "b-style.safetensors", loraFile(t, "comfyui"), 200)
	lora := func(id string, strength float64) map[string]any {
		return map[string]any{"lora": map[string]any{"lora_id": id}, "strength": strength}
	}
	body := func(extra map[string]any) map[string]any {
		return with(map[string]any{"kind": "look", "group": "colour", "name": "X", "text": "x"}, extra)
	}
	five := []any{lora(mine, 1), lora(inkWash, 1), lora(fluxStyle, 1), lora(mine, 1), lora(inkWash, 1)}
	for want, b := range map[string]map[string]any{
		"a look has no handle":                       body(map[string]any{"handle": "x"}),
		"group character is not a look group":        body(map[string]any{"group": "character"}),
		"a look needs text, an image or a LoRA":      body(map[string]any{"text": " "}),
		"loras: a look has at most 4 LoRAs":          body(map[string]any{"loras": five}),
		"loras[2]: LoRA ink-wash is listed twice":    body(map[string]any{"loras": []any{lora(inkWash, 1), lora(inkWash, 0.5)}}),
		"loras[1].strength: must be between 0 and 2": body(map[string]any{"loras": []any{lora(inkWash, 5)}}),
		"from_take makes looks only": with(body(map[string]any{"kind": "element", "handle": "x", "group": "prop",
			"images": []any{map[string]any{"take_id": one}}}), map[string]any{"from_take": map[string]any{"take_id": one}}),
	} {
		if got := e.call(userA, "POST", library, b, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}
	if got := e.call(userA, "POST", library, body(map[string]any{"loras": []any{lora(inkWash, -1)}}), 422); got["detail"] !=
		"loras[1].strength: must be between 0 and 2" {
		t.Fatal(got)
	}
	if got := e.call(userA, "POST", library, body(map[string]any{"loras": []any{lora(theirs, 1)}}), 404); got["detail"] !=
		"loras[1].lora: LoRA "+theirs+" not found" { // another user's LoRA: nothing leaks
		t.Fatal(got)
	}
	warm := e.call(userA, "POST", library, body(map[string]any{"text": "", "loras": []any{lora(inkWash, 0.6), lora(mine, 1.5)}}), 201)
	if l, _ := json.Marshal(warm["loras"]); string(l) != `[{"lora":{"lora_id":"`+inkWash+`"},"name":"ink-wash","strength":0.6},`+
		`{"lora":{"lora_id":"`+mine+`"},"name":"sketch","strength":1.5}]` {
		t.Fatal(string(l))
	}
	// a strength saved before API 1.23 (-4..4) is clamped to 0..2 when read; nothing is rewritten
	e.sql(`UPDATE library_items SET loras = jsonb_set(jsonb_set(loras, '{0,strength}', '-1'), '{1,strength}', '3.5') WHERE id = $1`, warm["id"])
	if l, _ := json.Marshal(e.call(userA, "GET", library+"/"+warm["id"].(string), nil, 200)["loras"]); string(l) !=
		`[{"lora":{"lora_id":"`+inkWash+`"},"name":"ink-wash","strength":0},`+
			`{"lora":{"lora_id":"`+mine+`"},"name":"sketch","strength":2}]` {
		t.Fatal(string(l))
	}

	// the list: own items newest first, then the live starter looks by name; filters
	e.qwenLooks()
	if got := ids(e.call(userA, "GET", library+"?kind=look", nil, 200)); !slices.Equal(got, []string{warm["id"].(string), id,
		"echo/grain", "qwen-image/ink-wash", "qwen-image/kodak-portra", "qwen-image/silhouette", "echo/soft-light", "qwen-image/window-light"}) {
		t.Fatal(got)
	}
	if got := ids(e.call(userA, "GET", library+"?source=starter&family=echo", nil, 200)); !slices.Equal(got, []string{"echo/grain", "echo/soft-light"}) {
		t.Fatal(got)
	}
	if got := ids(e.call(userA, "GET", library+"?family=echo&group=film", nil, 200)); !slices.Equal(got, []string{id, "echo/grain"}) {
		t.Fatal(got)
	}
	if got := ids(e.call(userA, "GET", library+"?source=mine&q=no", nil, 200)); !slices.Equal(got, []string{id}) {
		t.Fatal(got)
	}
	e.call(userA, "GET", library+"?source=all", nil, 422)

	// fit against a workflow: echo has a prompt and reference rows but no LoRA stack
	fit := map[string]string{}
	for _, item := range e.call(userA, "GET", library+"?workflow=echo&family=echo", nil, 200)["items"].([]any) {
		reason, _ := item.(map[string]any)["reason"].(string)
		fit[item.(map[string]any)["id"].(string)] = reason
	}
	if fit[id] != "" || fit["echo/soft-light"] != "" || fit["echo/grain"] != "this workflow has no LoRA stack" ||
		fit[warm["id"].(string)] != "this workflow has no LoRA stack" {
		t.Fatal(fit)
	}
	qwenFit := e.call(userA, "GET", library+"?workflow=qwen-image&source=starter&family=qwen-image", nil, 200)["items"].([]any)
	for _, item := range qwenFit {
		x := item.(map[string]any)
		if x["fits"] != (x["id"] != "qwen-image/kodak-portra") {
			t.Fatal(x)
		}
	}
	if got := e.call(userA, "GET", library+"?workflow=nope", nil, 422); got["detail"] != "unknown workflow nope" {
		t.Fatal(got)
	}

	// starter looks: read-only for everyone
	starter := e.call(userB, "GET", library+"/echo/soft-light", nil, 200)
	if starter["source"] != "starter" || starter["family"] != "echo" || starter["version"] != float64(1) || starter["handle"] != nil {
		t.Fatal(starter)
	}
	if got := e.call(userB, "PUT", library+"/echo/soft-light", map[string]any{}, 409); got["detail"] != "starter looks are read-only; save your own copy" {
		t.Fatal(got)
	}
	e.call(userB, "DELETE", library+"/echo/soft-light", nil, 409)
	e.call(userB, "GET", library+"/echo/nope", nil, 404)
	e.call(userB, "DELETE", library+"/echo/nope", nil, 404)

	// isolation: B sees the starters but none of A's, and cannot get, change or delete A's look
	if got := ids(e.call(userB, "GET", library+"?kind=look&family=echo", nil, 200)); !slices.Equal(got, []string{"echo/grain", "echo/soft-light"}) {
		t.Fatal(got)
	}
	update := map[string]any{"group": "film", "name": "Noir", "text": "ink", "images": []any{}, "version": 1}
	e.call(userB, "GET", library+"/"+id, nil, 404)
	e.call(userB, "PUT", library+"/"+id, update, 404)
	e.call(userB, "DELETE", library+"/"+id, nil, 404)
	if got := e.call(userA, "PUT", library+"/"+id, update, 200); got["version"] != float64(2) || got["thumb_url"] != nil {
		t.Fatal(got)
	}
	if got := e.call(userA, "PUT", library+"/"+id, update, 409); got["detail"] != "look changed since you opened it (now version 2)" {
		t.Fatal(got)
	}

	// Save as look: the take's prompt as typed and its usable LoRAs (mode dropped); an upload and B's take are refused
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	run := e.qwenRun(userA, session, []any{row(inkWash, 0.7, "bake"), row(mine, 0.5, "adapter")}, 202)
	e.relay()
	take := e.takeOf(userA, session, run["id"].(string))
	saved := e.call(userA, "POST", library, map[string]any{"kind": "look", "group": "film", "name": "Fox look",
		"from_take": map[string]any{"take_id": take}}, 201)
	if l, _ := json.Marshal(saved["loras"]); saved["text"] != "a fox" || saved["from_take_id"] != take || saved["thumb_url"] == nil ||
		string(l) != `[{"lora":{"lora_id":"`+inkWash+`"},"name":"ink-wash","strength":0.7},{"lora":{"lora_id":"`+mine+`"},"name":"sketch","strength":0.5}]` {
		t.Fatal(saved, string(l))
	}
	textOnly := e.call(userA, "POST", library, map[string]any{"kind": "look", "group": "film", "name": "Fox text",
		"from_take": map[string]any{"take_id": take, "loras": false}}, 201)
	if len(textOnly["loras"].([]any)) != 0 || textOnly["text"] != "a fox" {
		t.Fatal(textOnly)
	}
	if got := e.call(userA, "POST", library, map[string]any{"kind": "look", "group": "film", "name": "Up",
		"from_take": map[string]any{"take_id": one}}, 422); got["detail"] != "from_take: take "+one+" was not made by a run" {
		t.Fatal(got)
	}
	if got := e.call(userB, "POST", library, map[string]any{"kind": "look", "group": "film", "name": "Steal",
		"from_take": map[string]any{"take_id": take}}, 404); got["detail"] != "from_take: take "+take+" not found" {
		t.Fatal(got)
	}
	// a take made before API 1.23 (strengths -4..4): Save as look clamps them to 0..2
	e.sql(`UPDATE takes SET made_with = jsonb_set(jsonb_set(made_with, '{loras,0,strength}', '-1'), '{loras,1,strength}', '3.5')
		WHERE id = $1`, take)
	old := e.call(userA, "POST", library, map[string]any{"kind": "look", "group": "film", "name": "Old fox",
		"from_take": map[string]any{"take_id": take}}, 201)
	if l, _ := json.Marshal(old["loras"]); string(l) != `[{"lora":{"lora_id":"`+inkWash+`"},"name":"ink-wash","strength":0},`+
		`{"lora":{"lora_id":"`+mine+`"},"name":"sketch","strength":2}]` {
		t.Fatal(string(l))
	}
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, take)
	if got := e.call(userA, "GET", library+"/"+saved["id"].(string), nil, 200); got["from_take_id"] != nil || got["thumb_url"] != nil {
		t.Fatal(got)
	}

	// a starter look from an older host (strength 3) reads clamped to 2
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "looks.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var published map[string]any
	if err := json.Unmarshal(data, &published); err != nil {
		t.Fatal(err)
	}
	published["looks"].([]any)[1].(map[string]any)["loras"].([]any)[0].(map[string]any)["strength"] = 3
	if data, err = json.Marshal(published); err == nil {
		err = e.registry.SetRegistry("fake.fake.looks", data)
	}
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := json.Marshal(e.call(userA, "GET", library+"/echo/grain", nil, 200)["loras"]); string(l) !=
		`[{"lora":null,"name":"echo-grain","strength":2}]` {
		t.Fatal(string(l))
	}
}

func TestRunWithLooks(t *testing.T) {
	e := setup(t)
	e.looks()
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + session + "/runs"
	img := e.image(userA)
	noir := e.look(userA, "Noir", "black and white", img)
	id := noir["id"].(string)
	fox := e.element(userA, "Fox", "fox", "", e.image(userA))

	run := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "a fox"},
		"looks": []string{id, "echo/soft-light", id}}, 202)
	if l, _ := json.Marshal(run["looks"]); string(l) != `["`+id+`","echo/soft-light","`+id+`"]` { // as requested
		t.Fatal(string(l))
	}
	nodes := e.command()
	if nodes["prompt"].Params["text"] != "a fox\n\nblack and white <image1>\nsoft diffuse light" ||
		!slices.Equal(takeIDsOf(nodes["echo"].Params["refs"]), []string{img}) {
		t.Fatal(nodes)
	}
	take := e.takeOf(userA, session, run["id"].(string))
	made := e.call(userA, "GET", "/v1/studio/takes/"+take, nil, 200)["made_with"].(map[string]any)
	if l, _ := json.Marshal(made["looks"]); string(l) != `[{"id":"`+id+`","name":"Noir","source":"mine","version":1},`+
		`{"id":"echo/soft-light","name":"Soft light","source":"starter","version":1}]` || made["typed"].(map[string]any)["prompt.text"] != "a fox" {
		t.Fatal(string(l), made)
	}

	// unknown (B naming A's look, an element, a malformed id) and not fitting
	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	for user, test := range map[string]struct {
		looks  []string
		detail string
		status int
	}{
		userB: {[]string{"echo/soft-light", id}, "looks[2]: unknown look " + id, 422},
		userA: {[]string{fox["id"].(string), "echo/grain"}, "looks[1]: unknown look " + fox["id"].(string), 422},
	} {
		path := map[string]string{userA: runs, userB: "/v1/studio/sessions/" + sessionB + "/runs"}[user]
		if got := e.call(user, "POST", path, map[string]any{"workflow_id": "echo", "looks": test.looks}, test.status); got["detail"] != test.detail {
			t.Errorf("%s: %v, want %s", user, got["detail"], test.detail)
		}
	}
	if got := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "looks": []string{"echo/grain"}}, 422); got["detail"] !=
		"looks[1]: look Grain does not fit this workflow (this workflow has no LoRA stack)" {
		t.Fatal(got)
	}
	e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "looks": []string{"Window"}}, 422)

	// reuse: the typed view and the look ids; then changed (edited), deleted, missing (the pack dropped it)
	reuse := func(want string) {
		t.Helper()
		got := e.call(userA, "POST", "/v1/studio/takes/"+take+"/reuse", nil, 200)
		statuses, _ := json.Marshal(got["looks_status"])
		if got["inputs"].(map[string]any)["prompt.text"] != "a fox" || len(got["inputs"].(map[string]any)["echo.refs"].([]any)) != 0 ||
			!slices.Equal(anyStrings(got["looks"]), []string{id, "echo/soft-light"}) || string(statuses) != want {
			t.Fatal(got, string(statuses))
		}
	}
	reuse(`[{"id":"` + id + `","status":"current"},{"id":"echo/soft-light","status":"current"}]`)
	e.call(userA, "PUT", library+"/"+id, map[string]any{"group": "film", "name": "Noir", "text": "ink", "images": []any{}, "version": 1}, 200)
	reuse(`[{"id":"` + id + `","status":"changed"},{"id":"echo/soft-light","status":"current"}]`)
	e.call(userA, "DELETE", library+"/"+id, nil, 204)
	_ = e.registry.SetRegistry("fake.fake.looks", nil)
	reuse(`[{"id":"` + id + `","status":"deleted"},{"id":"echo/soft-light","status":"missing"}]`)
	if again := e.call(userA, "GET", "/v1/studio/takes/"+take, nil, 200)["made_with"].(map[string]any); again["inputs"].(map[string]any)["prompt.text"] !=
		"a fox\n\nblack and white <image1>\nsoft diffuse light" {
		t.Fatal(again) // the take keeps what it ran
	}
	if got := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "looks": []string{id, "echo/soft-light"}}, 422); got["detail"] !=
		"looks[1]: unknown look "+id+"; looks[2]: unknown look echo/soft-light" {
		t.Fatal(got)
	}
}

// anyStrings is a JSON list of strings.
func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// A starter look's shared LoRA joins the look stack and narrows the hosts like any shared LoRA; the user's own row of
// the same LoRA wins.
func TestLookLorasDispatch(t *testing.T) {
	e := setup(t)
	e.qwen()
	e.shared()
	e.qwenLooks()
	for _, kind := range []string{"nodes", "workflows", "models"} { // host box (before mac): the same runner without the LoRA
		data, _ := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "qwen-image."+kind+".valid.json"))
		if err := e.registry.SetRegistry("box.torch."+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "box", "env": "torch", "state": "idle", "run_id": nil,
		"updated_at": "2099-01-01T00:00:00Z", "published_by": "test"})
	if err := e.registry.SetHost("box.torch", value); err != nil {
		t.Fatal(err)
	}
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + session + "/runs"
	run := e.call(userA, "POST", runs, map[string]any{"workflow_id": "qwen-image", "inputs": map[string]any{"prompt.text": "a fox"},
		"looks": []string{"qwen-image/ink-wash"}}, 202)
	rows := e.stored(run["id"].(string))["lora"].Params["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["strength"] != 0.8 || rows[0].(map[string]any)["mode"] != "adapter" {
		t.Fatal(rows)
	}
	e.relay()
	if got := e.call(userA, "GET", "/v1/studio/runs/"+run["id"].(string), nil, 200); got["host"] != "mac" {
		t.Fatal(got)
	}
	e.idleMac()
	mine := e.call(userA, "POST", runs, map[string]any{"workflow_id": "qwen-image", "inputs": map[string]any{"prompt.text": "a fox",
		"lora.rows": []any{row(inkWash, 0.3, "bake")}}, "looks": []string{"qwen-image/ink-wash"}}, 202)
	if rows := e.stored(mine["id"].(string))["lora"].Params["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["strength"] != 0.3 {
		t.Fatal(rows)
	}
	if got := e.call(userA, "POST", runs, map[string]any{"workflow_id": "qwen-image", "looks": []string{"qwen-image/kodak-portra"}}, 422); got["detail"] !=
		"looks[1]: look Portra does not fit this workflow (LoRA kodak-portra is on no live host)" {
		t.Fatal(got)
	}
}

func TestBatchLooks(t *testing.T) {
	e := setup(t)
	e.looks()
	img := e.image(userA)
	noir := e.look(userA, "Noir", "black and white", img)
	id := noir["id"].(string)
	table := func(cells ...any) map[string]any {
		var rows [][]any
		for i, cell := range cells {
			rows = append(rows, []any{[]string{"a fox", "a heron", "a cabin"}[i], cell})
		}
		return map[string]any{"workflow_id": "echo", "looks": []string{"echo/soft-light"},
			"rows": map[string]any{"table": map[string]any{"columns": []string{"prompt.text", "looks"}, "rows": rows}}}
	}
	if got := e.call(userB, "POST", "/v1/studio/batches", table(nil, []string{id}), 422); got["detail"] != "row 2: looks[1]: unknown look "+id {
		t.Fatal(got)
	}
	e.call(userA, "POST", "/v1/studio/batches", table([]string{"Window"}), 422)
	batch := e.submit(userA, table(nil, []string{id}, []string{}))
	e.sent()
	rows := e.call(userA, "GET", "/v1/studio/batches/"+batch["id"].(string)+"/rows", nil, 200)["rows"].([]any)
	if l, _ := json.Marshal([]any{rows[0].(map[string]any)["looks"], rows[1].(map[string]any)["looks"], rows[2].(map[string]any)["looks"]}); string(l) !=
		`[["echo/soft-light"],["`+id+`"],null]` {
		t.Fatal(string(l))
	}
	// the runs resolve against the batch's copy, after the look is edited and deleted
	e.call(userA, "PUT", library+"/"+id, map[string]any{"group": "film", "name": "Noir", "text": "ink", "images": []any{}, "version": 1}, 200)
	if _, refused := e.ask(userA, batch, 2, nil); refused != nil {
		t.Fatal(refused)
	}
	if nodes := e.command(); nodes["prompt"].Params["text"] != "a heron\n\nblack and white <image1>" {
		t.Fatal(nodes)
	}
	e.call(userA, "DELETE", library+"/"+id, nil, 204)
	first, refused := e.ask(userA, batch, 1, nil)
	if refused != nil || e.stored(first.RunID)["prompt"].Params["text"] != "a fox\n\nsoft diffuse light" {
		t.Fatal(refused)
	}
	if got := e.call(userA, "GET", "/v1/studio/runs/"+first.RunID, nil, 200); !slices.Equal(anyStrings(got["looks"]), []string{"echo/soft-light"}) {
		t.Fatal(got)
	}

	// pipelines: a batch takes its looks from its steps; a step's unknown or unfitting look is a problem
	mine := e.look(userA, "Dusk", "at dusk")["id"].(string)
	steps := func(looks ...string) map[string]any {
		return map[string]any{"title": "Looks", "nodes": map[string]any{
			"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
			"s1":     map[string]any{"kind": "step", "workflow": "echo", "looks": looks, "bind": map[string]any{"prompt.text": "$prompt"}},
			"out":    map[string]any{"kind": "output", "from": "s1.takes", "collection": "x"},
		}, "edges": []any{[]any{"inputs", "s1"}, []any{"s1", "out"}}}
	}
	problem := func(user string, doc map[string]any) string {
		problems := e.call(user, "POST", "/v1/studio/pipelines/validate", doc, 200)["problems"].([]any)
		if len(problems) != 1 {
			return ""
		}
		return problems[0].(map[string]any)["code"].(string)
	}
	if problem(userB, steps(mine)) != "unknown_look" || problem(userA, steps("echo/grain")) != "look_fit" || problem(userA, steps(mine)) != "" {
		t.Fatal(problem(userB, steps(mine)), problem(userA, steps("echo/grain")))
	}
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", steps(mine), 201)
	rows2 := map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}}}}
	if got := e.call(userA, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": pipeline["id"], "rows": rows2,
		"looks": []string{mine}}, 422); got["detail"] != "looks: a pipeline batch takes its looks from its steps" {
		t.Fatal(got)
	}
	piped := e.submit(userA, map[string]any{"pipeline_id": pipeline["id"], "rows": rows2})
	e.sent()
	e.call(userA, "DELETE", library+"/"+mine, nil, 204) // the copy still applies it
	run, refused := e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: piped["session_id"].(string), WorkflowID: "echo", Inputs: map[string]any{"prompt.text": "a fox"},
		Lane: "batch", BatchID: piped["id"].(string), Row: 1, IdempotencyKey: piped["id"].(string) + ":1:1:s1:0:0"})
	if refused != nil || e.stored(run.RunID)["prompt"].Params["text"] != "a fox\n\nat dusk" {
		t.Fatal(run, refused)
	}
	e.call(userB, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": pipeline["id"], "rows": rows2}, 422) // another user's pipeline is unknown
}
