// Integration tests of elements (contracts/README.md "Elements", "Mentions"): the library endpoints with isolation,
// handle uniqueness and the stale write; a run that mentions one (the resolved graph sent and stored, made_with, reuse
// after an edit and a delete); batches of a workflow and of a pipeline.
package tests

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

const library = "/v1/studio/library"

// element posts an element of owner's takes and returns it.
func (e *env) element(owner, name, handle, text string, takes ...string) map[string]any {
	e.t.Helper()
	images := []any{}
	for _, id := range takes {
		images = append(images, map[string]any{"take_id": id})
	}
	return e.call(owner, "POST", library, map[string]any{"kind": "element", "group": "character", "name": name,
		"handle": handle, "text": text, "images": images}, 201)
}

func TestLibraryItems(t *testing.T) {
	e := setup(t)
	one, two, theirs, video := e.image(userA), e.image(userA), e.image(userB), e.image(userA, "video")
	fox := e.element(userA, " Fox ", "fox", " a small red fox ", one, two)
	id := fox["id"].(string)
	images := fox["images"].([]any)
	if !utility.IsUUID(id) || fox["name"] != "Fox" || fox["text"] != "a small red fox" || fox["version"] != float64(1) ||
		fox["kind"] != "element" || len(images) != 2 || images[0].(map[string]any)["take_id"] != one ||
		images[0].(map[string]any)["width"] != float64(64) || fox["thumb_url"] != images[0].(map[string]any)["thumb_url"] {
		t.Fatal(fox)
	}
	body := func(handle string, takes ...string) map[string]any {
		images := []any{}
		for _, id := range takes {
			images = append(images, map[string]any{"take_id": id})
		}
		return map[string]any{"kind": "element", "group": "prop", "name": "X", "handle": handle, "images": images}
	}
	for want, b := range map[string]map[string]any{
		"handle must be a slug: a-z, 0-9, - and _, starting with a letter, ending with a letter or digit, at most 40 characters": body("Fox-", one),
		"images: an element has 1 to 4 images":                                  body("x"),
		"images[2]: take " + one + " is listed twice":                           body("x", one, one),
		"images[1]: take " + video + " is not an image":                         body("x", video),
		"name must be 1-80 characters":                                          with(body("x", one), map[string]any{"name": "  "}),
		"group film is not an element group":                                    with(body("x", one), map[string]any{"group": "film"}),
		"group prop is not a look group":                                        with(body("x", one), map[string]any{"kind": "look"}),
		"kind must be element, look or voice":                                   with(body("x", one), map[string]any{"kind": "x"}),
		"loras: an element has no LoRAs":                                        with(body("x", one), map[string]any{"loras": []any{map[string]any{"lora": map[string]any{"lora_id": "x"}, "strength": 1}}}),
		"unknown field lora":                                                    with(body("x", one), map[string]any{"lora": "x"}),
		"text must be at most 2000 characters":                                  with(body("x", one), map[string]any{"text": strings.Repeat("é", 2001)}),
		"name must not contain control characters":                              with(body("x", one), map[string]any{"name": "a\u0000b"}),
		"text must not contain control characters other than newlines and tabs": with(body("x", one), map[string]any{"text": "a\nb\u0000"}),
	} {
		if got := e.call(userA, "POST", library, b, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}
	if got := e.call(userA, "POST", library, body("x", theirs), 404); got["detail"] != "images[1]: take "+theirs+" not found" {
		t.Fatal(got)
	}
	if got := e.call(userA, "POST", library, body("fox", one), 409); got["detail"] != "handle @fox is already used" {
		t.Fatal(got)
	}
	e.element(userA, "Fox cub", "fox-cub", "", two)
	e.element(userA, "Old barn", "barn", "", one)

	// the @ menu: prefix on handle or name, the exact handle first; without q newest first
	names := func(path string) []string {
		var out []string
		for _, item := range e.call(userA, "GET", path, nil, 200)["items"].([]any) {
			out = append(out, item.(map[string]any)["handle"].(string))
		}
		return out
	}
	if got := names(library + "?kind=element&q=%40FOX&limit=8"); !slices.Equal(got, []string{"fox", "fox-cub"}) {
		t.Fatal(got)
	}
	if got := names(library + "?q=old"); !slices.Equal(got, []string{"barn"}) {
		t.Fatal(got)
	}
	if got := names(library); !slices.Equal(got, []string{"barn", "fox-cub", "fox"}) {
		t.Fatal(got)
	}
	if got := names(library + "?limit=1&group=character"); !slices.Equal(got, []string{"barn"}) {
		t.Fatal(got)
	}
	if got := names(library + "?group=location"); len(got) != 0 {
		t.Fatal(got)
	}
	e.call(userA, "GET", library+"?limit=0", nil, 422)
	e.call(userA, "GET", library+"?kind=x", nil, 422)

	// isolation: B cannot list, get, change or delete A's; B may use the same handle
	if items := e.call(userB, "GET", library, nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatal(items)
	}
	update := map[string]any{"group": "character", "name": "Fox", "handle": "fox", "text": "", "images": []any{map[string]any{"take_id": one}},
		"version": 1}
	e.call(userB, "GET", library+"/"+id, nil, 404)
	e.call(userB, "PUT", library+"/"+id, update, 404)
	e.call(userB, "DELETE", library+"/"+id, nil, 404)
	e.element(userB, "Fox", "fox", "", theirs)

	// replace: version + 1, then the old version is stale; the handle may stay its own but not take another's
	if got := e.call(userA, "PUT", library+"/"+id, update, 200); got["version"] != float64(2) || len(got["images"].([]any)) != 1 || got["text"] != "" {
		t.Fatal(got)
	}
	if got := e.call(userA, "PUT", library+"/"+id, update, 409); got["detail"] != "element changed since you opened it (now version 2)" {
		t.Fatal(got)
	}
	if got := e.call(userA, "PUT", library+"/"+id, with(update, map[string]any{"version": 2, "handle": "barn"}), 409); got["detail"] != "handle @barn is already used" {
		t.Fatal(got)
	}
	e.call(userA, "PUT", library+"/"+id, with(update, map[string]any{"version": 2, "text": nil}), 422)

	// a deleted take drops out of the element; the element stays
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, one)
	if got := e.call(userA, "GET", library+"/"+id, nil, 200); len(got["images"].([]any)) != 0 || got["thumb_url"] != nil {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", library+"/"+id, nil, 204)
	e.call(userA, "DELETE", library+"/"+id, nil, 404)
	e.call(userA, "GET", library+"/"+id, nil, 404)

	// at most 200 per user
	e.exec(`INSERT INTO library_items (id, owner_id, kind, item_group, name, handle)
		SELECT gen_random_uuid(), $1, 'element', 'prop', 'n', 'h' || n FROM generate_series(1, 198) n`, userA)
	if got := e.call(userA, "POST", library, body("last", two), 409); got["detail"] != "You have 200 library items; delete some first." {
		t.Fatal(got)
	}
}

// command returns the nodes of the one cmd.run.execute the dispatcher sends now.
func (e *env) command() map[string]struct{ Params map[string]any } {
	e.t.Helper()
	_, sent := e.relay()
	if len(sent) != 1 {
		e.t.Fatal(sent)
	}
	var command struct {
		Workflow struct {
			Nodes map[string]struct{ Params map[string]any }
		}
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	return command.Workflow.Nodes
}

// stored returns the graph a run was stored with.
func (e *env) stored(runID string) map[string]struct{ Params map[string]any } {
	e.t.Helper()
	var nodes map[string]struct{ Params map[string]any }
	err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(context.Background(), `SELECT nodes FROM runs WHERE id = $1`, runID).Scan(&raw)
		if err == nil {
			err = json.Unmarshal(raw, &nodes)
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return nodes
}

// takeIDsOf lists the take ids of an echo.refs value (as sent: descriptors, or as stored: {take_id}).
func takeIDsOf(value any) []string {
	var out []string
	for _, row := range value.([]any) {
		out = append(out, row.(map[string]any)["image"].(map[string]any)["take_id"].(string))
	}
	return out
}

func TestRunWithMentions(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	own, foxA, foxB := e.image(userA), e.image(userA), e.image(userA)
	fox := e.element(userA, "Fox", "fox", "a small red fox", foxA, foxB)
	typed := "@Fox meets <image1>, mail me@fox.com @@fox"
	run := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": typed, "echo.refs": refs(own)}}, 202)
	if run["inputs"].(map[string]any)["prompt.text"] != typed { // Run.inputs stays as requested
		t.Fatal(run["inputs"])
	}
	nodes := e.command()
	resolved := "Fox <image2> <image3> meets <image1>, mail me@fox.com @fox\n\nFox: a small red fox"
	if nodes["prompt"].Params["text"] != resolved || !slices.Equal(takeIDsOf(nodes["echo"].Params["refs"]), []string{own, foxA, foxB}) {
		t.Fatal(nodes)
	}

	// unknown handles: one 422 naming each; B mentioning A's handle is unknown; a lone @ or @@ is literal
	if bad := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "@wolf and @owl and @wolf"}}, 422); bad["detail"] != "prompt.text: unknown element @wolf; prompt.text: unknown element @owl" {
		t.Fatal(bad)
	}
	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	if bad := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "@fox"}}, 422); bad["detail"] != "prompt.text: unknown element @fox" {
		t.Fatal(bad)
	}
	if bad := e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "@fox", "echo.refs": refs(e.image(userA), e.image(userA), e.image(userA), e.image(userA),
			e.image(userA), e.image(userA), e.image(userA), e.image(userA), e.image(userA))}}, 422); bad["detail"] != "echo.refs: at most 10 images; 9 used, @fox +2 do not fit" {
		t.Fatal(bad)
	}
	e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "@ 3pm @@"}}, 202)
	e.relay()

	// the take: the resolved graph in inputs, the prompt as typed, typed, elements; lineage is the user's rows only
	runID := run["id"].(string)
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.png",
		"kind": "image", "width": 64, "height": 64, "bytes": 10})
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	take := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)
	made := take["made_with"].(map[string]any)
	inputs, typedValues := made["inputs"].(map[string]any), made["typed"].(map[string]any)
	elements, _ := json.Marshal(made["elements"].([]any)[0])
	if made["prompt"] != typed || inputs["prompt.text"] != resolved || typedValues["prompt.text"] != typed ||
		!slices.Equal(takeIDsOf(inputs["echo.refs"]), []string{own, foxA, foxB}) || !slices.Equal(takeIDsOf(typedValues["echo.refs"]), []string{own}) ||
		string(elements) != `{"handle":"fox","id":"`+fox["id"].(string)+`","name":"Fox","version":1}` {
		t.Fatal(made)
	}
	if parents, _ := json.Marshal(take["parents"]); string(parents) != `["`+own+`"]` || !strings.HasPrefix(take["title"].(string), "@Fox meets") {
		t.Fatal(string(parents), take["title"])
	}

	// reuse: the typed view, the element current; then changed (renamed), then deleted; the take keeps what it ran
	reuse := func(status, current string) {
		t.Helper()
		got := e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/reuse", nil, 200)
		statuses, _ := json.Marshal(got["elements_status"])
		want := `[{"current_handle":` + current + `,"handle":"fox","id":"` + fox["id"].(string) + `","status":"` + status + `"}]`
		if got["inputs"].(map[string]any)["prompt.text"] != typed || !slices.Equal(takeIDsOf(got["inputs"].(map[string]any)["echo.refs"]), []string{own}) ||
			string(statuses) != want {
			t.Fatal(got, string(statuses))
		}
	}
	reuse("current", `"fox"`)
	e.call(userA, "PUT", library+"/"+fox["id"].(string), map[string]any{"group": "character", "name": "Vixen", "handle": "vixen",
		"text": "", "images": []any{map[string]any{"take_id": foxA}}, "version": 1}, 200)
	reuse("changed", `"vixen"`)
	e.call(userA, "DELETE", library+"/"+fox["id"].(string), nil, 204)
	reuse("deleted", "null")
	again := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)["made_with"].(map[string]any)
	if again["inputs"].(map[string]any)["prompt.text"] != resolved || again["prompt"] != typed {
		t.Fatal(again)
	}
	// reusing it as is now fails: the handle is gone
	e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": typed}}, 422)

	// a Qwen edit on the auto canvas keeps the size of the user's last row (64x32 → 1440x736), not the element's
	e.qwen()
	e.element(userA, "Cub", "cub", "", e.image(userA))
	edit := e.call(userA, "POST", runs, map[string]any{"workflow_id": "qwen-image",
		"inputs": map[string]any{"prompt.text": "put @cub in <image1>", "encode.refs": refs(own)}}, 202)
	if canvas := e.stored(edit["id"].(string))["canvas"].Params; canvas["mode"] != "fixed" || canvas["width"] != float64(1440) ||
		canvas["height"] != float64(736) {
		t.Fatal(canvas)
	}
}

func TestBatchMentions(t *testing.T) {
	e := setup(t)
	foxA := e.image(userA)
	fox := e.element(userA, "Fox", "fox", "a small red fox", foxA)

	// every row that cannot resolve, in row order, at most 10
	values := []string{"@fox", "@wolf"}
	for range 12 {
		values = append(values, "@owl")
	}
	bad := e.call(userA, "POST", "/v1/studio/batches", lines(values...), 422)["detail"].(string)
	if !strings.HasPrefix(bad, "row 2: prompt.text: unknown element @wolf; row 3: prompt.text: unknown element @owl; row 4:") ||
		!strings.HasSuffix(bad, "; and 3 more rows") {
		t.Fatal(bad)
	}

	// a good batch keeps a copy: its runs (Retry too) resolve against it after the element is edited or deleted
	batch := e.submit(userA, lines("@fox at dawn", "plain"))
	e.sent()
	e.call(userA, "PUT", library+"/"+fox["id"].(string), map[string]any{"group": "prop", "name": "Changed", "handle": "fox",
		"text": "", "images": []any{map[string]any{"take_id": foxA}}, "version": 1}, 200)
	created, refused := e.ask(userA, batch, 1, nil)
	if refused != nil {
		t.Fatal(refused)
	}
	nodes := e.command()
	if nodes["prompt"].Params["text"] != "Fox <image1> at dawn\n\nFox: a small red fox" || !slices.Equal(takeIDsOf(nodes["echo"].Params["refs"]), []string{foxA}) {
		t.Fatal(nodes)
	}
	e.call(userA, "DELETE", library+"/"+fox["id"].(string), nil, 204)
	second, refused := e.ask(userA, batch, 2, nil)
	if nodes := e.stored(second.RunID); refused != nil || nodes["prompt"].Params["text"] != "plain" || len(nodes["echo"].Params["refs"].([]any)) != 0 {
		t.Fatal(refused, nodes)
	}
	row := e.call(userA, "GET", "/v1/studio/runs/"+created.RunID, nil, 200)
	if row["inputs"].(map[string]any)["prompt.text"] != "@fox at dawn" {
		t.Fatal(row)
	}

	// a pipeline batch: unknown handles at Submit, in its rows' Text inputs and its steps' text inputs
	pipeline := e.call(userA, "POST", "/v1/studio/pipelines", echoPipeline("Night"), 201)
	submit := func(prompt string) map[string]any {
		return map[string]any{"pipeline_id": pipeline["id"],
			"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}, {prompt}}}}}
	}
	if got := e.call(userA, "POST", "/v1/studio/batches", submit("@fox again"), 422); got["detail"] !=
		"row 2: edit: prompt.text: unknown element @fox; row 2: still: prompt.text: unknown element @fox" {
		t.Fatal(got)
	}
	// a step workflow's own saved prompt is checked and copied too, as its runs resolve it
	snowy := echoDoc("Snowy", 2)
	snowy["nodes"].(map[string]any)["prompt"].(map[string]any)["params"] = map[string]any{"text": "@owl in snow"}
	saved := e.call(userA, "POST", "/v1/studio/workflows", snowy, 201)["id"].(string)
	snow := e.call(userA, "POST", "/v1/studio/pipelines", map[string]any{"title": "Snow", "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "steps", "type": "Int"}}},
		"s1":     map[string]any{"kind": "step", "workflow": saved, "bind": map[string]any{"echo.steps": "$steps"}},
		"out":    map[string]any{"kind": "output", "from": "s1.takes", "collection": "Snow"},
	}, "edges": []any{[]any{"inputs", "s1"}, []any{"s1", "out"}}}, 201)
	snowRows := map[string]any{"pipeline_id": snow["id"],
		"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.steps"}, "rows": [][]any{{2}}}}}
	if got := e.call(userA, "POST", "/v1/studio/batches", snowRows, 422); got["detail"] != "row 1: s1: prompt.text: unknown element @owl" {
		t.Fatal(got)
	}
	owl := e.element(userA, "Owl", "owl", "", e.image(userA))
	snowBatch := e.submit(userA, snowRows)
	e.sent()
	run, refused := e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: snowBatch["session_id"].(string), WorkflowID: saved, Inputs: map[string]any{"echo.steps": float64(2)},
		Lane: "batch", BatchID: snowBatch["id"].(string), Row: 1, IdempotencyKey: snowBatch["id"].(string) + ":1:1:s1:0:0"})
	if refused != nil || !run.Created || e.stored(run.RunID)["prompt"].Params["text"] != "Owl <image1> in snow" {
		t.Fatal(run, refused)
	}
	piped := e.submit(userA, submit("@owl at night"))
	e.sent()
	e.call(userA, "DELETE", library+"/"+owl["id"].(string), nil, 204)
	run, refused = e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: piped["session_id"].(string), WorkflowID: "echo", Inputs: map[string]any{"prompt.text": "@owl at night",
			"echo.steps": float64(2)}, Lane: "batch", BatchID: piped["id"].(string), Row: 2, IdempotencyKey: piped["id"].(string) + ":2:1:still:0:0"})
	if refused != nil || !run.Created {
		t.Fatal(run, refused)
	}
	if nodes := e.stored(run.RunID); nodes["prompt"].Params["text"] != "Owl <image1> at night" {
		t.Fatal(nodes)
	}
	// a mention not in the copy fails that run
	if _, refused := e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: piped["session_id"].(string), WorkflowID: "echo", Inputs: map[string]any{"prompt.text": "@fox"}, Lane: "batch",
		BatchID: piped["id"].(string), Row: 1, IdempotencyKey: piped["id"].(string) + ":1:1:still:0:0"}); refused == nil ||
		refused.Code != "invalid" || refused.Error != "prompt.text: unknown element @fox" {
		t.Fatal(refused)
	}
}
