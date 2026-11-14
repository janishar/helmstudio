// Integration tests of image inputs (contracts/README.md "Image inputs"): the checks when a run is created (isolation
// included), the descriptors at release, a reference deleted before release, lineage, "Use in…" and batches.
package tests

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/common"
)

// image stores a ready take of owner (kind image unless given) with its object in the store, as a run output would
// be (no content hash yet), and returns its id.
func (e *env) image(owner string, kind ...string) string {
	e.t.Helper()
	id, data := utility.NewID(), []byte("png bytes of "+owner)
	key := "u/" + owner + "/uploads/" + id + ".png"
	if err := e.store.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		e.t.Fatal(err)
	}
	takeKind := "image"
	if len(kind) > 0 {
		takeKind = kind[0]
	}
	e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes, ready)
		VALUES ($1, $2, $3, 'upload', 'ref', $4, 'image/png', 64, 32, $5, true)`, id, owner, takeKind, key, len(data))
	return id
}

// exec runs one statement as System.
func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), query, args...)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

// refs is an echo.refs value of the takes.
func refs(ids ...string) []any {
	out := []any{}
	for _, id := range ids {
		out = append(out, map[string]any{"image": map[string]any{"take_id": id}})
	}
	return out
}

func TestImageInputs(t *testing.T) {
	e := setup(t)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	run := func(user string, ids []string, want int) map[string]any {
		return e.call(user, "POST", runs, map[string]any{"workflow_id": "echo",
			"inputs": map[string]any{"prompt.text": "the fox at night", "echo.refs": refs(ids...)}}, want)
	}
	first, second, theirs, video := e.image(userA), e.image(userA), e.image(userB), e.image(userA, "video")
	pending, missing, deleted, sizeless := e.image(userA), e.image(userA), e.image(userA), e.image(userA)
	e.exec(`UPDATE takes SET ready = false WHERE id = $1`, pending)
	e.exec(`UPDATE takes SET width = NULL, height = NULL WHERE id = $1`, sizeless) // an output recorded without a size
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, deleted)
	_ = e.store.Delete(context.Background(), "u/"+userA+"/uploads/"+missing+".png")

	// every bad reference is one 422 naming the param and item; another user's take is "not found" like an unknown one
	for id, want := range map[string]string{
		theirs:          "echo.refs[2].image: take " + theirs + " not found",
		pending:         "echo.refs[2].image: take " + pending + " not found",
		missing:         "echo.refs[2].image: take " + missing + " not found",
		deleted:         "echo.refs[2].image: take " + deleted + " not found",
		utility.NewID(): "",
		video:           "echo.refs[2].image: take " + video + " is not an image",
		sizeless:        "echo.refs[2].image: take " + sizeless + " has no size",
	} {
		bad := run(userA, []string{first, id}, 422)
		if want != "" && bad["detail"] != want {
			t.Errorf("%s: %v, want %s", id, bad["detail"], want)
		}
	}
	// user B cannot use A's take even in B's own session
	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	if bad := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"echo.refs": refs(first)}}, 422); bad["detail"] != "echo.refs[1].image: take "+first+" not found" {
		t.Fatal(bad)
	}

	// a good run keeps {take_id}; the command carries the descriptors, the content hash computed lazily
	made := run(userA, []string{second, first, second}, 202)
	if got, _ := json.Marshal(made["inputs"].(map[string]any)["echo.refs"]); string(got) != `[{"image":{"take_id":"`+second+
		`"}},{"image":{"take_id":"`+first+`"}},{"image":{"take_id":"`+second+`"}}]` {
		t.Fatalf("%s", got)
	}
	_, sent := e.relay()
	if len(sent) != 1 {
		t.Fatal(sent)
	}
	var command struct {
		Workflow struct {
			Nodes map[string]struct{ Params map[string]any }
		}
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	ref := command.Workflow.Nodes["echo"].Params["refs"].([]any)[1].(map[string]any)["image"].(map[string]any)
	sum := md5.Sum([]byte("png bytes of " + userA))
	if ref["take_id"] != first || ref["key"] != "u/"+userA+"/uploads/"+first+".png" || ref["content_type"] != "image/png" ||
		ref["width"] != float64(64) || ref["height"] != float64(32) || ref["bytes"] != float64(len("png bytes of "+userA)) ||
		ref["etag"] != hex.EncodeToString(sum[:]) || len(ref) != 7 {
		t.Fatal(ref)
	}

	// lineage: the output's take has its references as parents, each once, in input order; the reference lists it
	runID := made["id"].(string)
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.png",
		"kind": "image", "width": 64, "height": 64, "bytes": 10, "etag": "0123456789abcdef0123456789abcdef"})
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	var etag string // the runner's content hash is the take's: using it as a reference reads no object
	err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT etag FROM takes WHERE id = $1`, takeID).Scan(&etag)
	})
	if err != nil || etag != "0123456789abcdef0123456789abcdef" {
		t.Fatal(etag, err)
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)
	if got, _ := json.Marshal(take["parents"]); string(got) != `["`+second+`","`+first+`"]` {
		t.Fatalf("parents %s", got)
	}
	if got, _ := json.Marshal(take["made_with"].(map[string]any)["inputs"].(map[string]any)["echo.refs"]); !bytes.Contains(got, []byte(`{"take_id":"`+first+`"}`)) || bytes.Contains(got, []byte("key")) {
		t.Fatalf("made_with %s", got)
	}
	children := e.call(userA, "GET", "/v1/studio/takes/"+first, nil, 200)["children"].([]any)
	if len(children) != 1 || children[0].(map[string]any)["id"] != takeID || children[0].(map[string]any)["title"] != "the fox at night" {
		t.Fatal(children)
	}
	if list := e.call(userA, "GET", "/v1/studio/takes?kind=image", nil, 200)["takes"].([]any); list[0].(map[string]any)["children"] != nil {
		t.Fatal("children only on GET one take")
	}
	// a deleted parent leaves parents; its child leaves children
	e.call(userA, "DELETE", "/v1/studio/takes/"+second, nil, 204)
	if got, _ := json.Marshal(e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)["parents"]); string(got) != `["`+first+`"]` {
		t.Fatalf("parents after delete %s", got)
	}

	// a reference deleted while the run waits fails it at release: never sent
	waiting := run(userA, []string{first}, 202)
	e.host(0)
	e.exec(`UPDATE runs SET status = 'succeeded', finished_at = now() WHERE id = $1`, runID) // frees the host
	e.call(userA, "DELETE", "/v1/studio/takes/"+first, nil, 204)
	if _, sent := e.relay(); len(sent) != 0 {
		t.Fatal("a dead key was sent", sent)
	}
	if gone := e.call(userA, "GET", "/v1/studio/runs/"+waiting["id"].(string), nil, 200); gone["status"] != "failed" ||
		gone["error"] != "input image "+first+" is gone" {
		t.Fatal(gone)
	}
}

func TestUseIn(t *testing.T) {
	e := setup(t)
	mine, theirs, video := e.image(userA), e.image(userB), e.image(userA, "video")
	use := func(user, id string, body map[string]any, want int) map[string]any {
		return e.call(user, "POST", "/v1/studio/takes/"+id+"/use", body, want)
	}
	got := use(userA, mine, map[string]any{"workflow_id": "echo"}, 200)
	if data, _ := json.Marshal(got); string(data) != `{"inputs":{"echo.refs":[{"image":{"take_id":"`+mine+`"}}]},"workflow_id":"echo"}` {
		t.Fatalf("%s", data)
	}
	use(userA, mine, map[string]any{"workflow_id": "echo", "param": "echo.refs"}, 200)
	use(userB, mine, map[string]any{"workflow_id": "echo"}, 404)
	use(userA, theirs, map[string]any{"workflow_id": "echo"}, 404)
	for body, want := range map[string]string{
		`{"workflow_id": "nope"}`:                        "unknown workflow nope",
		`{"workflow_id": "echo", "param": "echo.steps"}`: "echo.steps: not an image input of workflow echo",
		`{"workflow_id": "echo", "extra": 1}`:            "extra: not a field of TakeUse",
	} {
		var decoded map[string]any
		_ = json.Unmarshal([]byte(body), &decoded)
		if bad := use(userA, mine, decoded, 422); bad["detail"] != want {
			t.Errorf("%s: %v", body, bad["detail"])
		}
	}
	if bad := use(userA, video, map[string]any{"workflow_id": "echo"}, 422); bad["detail"] != "take "+video+" is not an image" {
		t.Fatal(bad)
	}
	sizeless := e.image(userA)
	e.exec(`UPDATE takes SET width = NULL WHERE id = $1`, sizeless)
	if bad := use(userA, sizeless, map[string]any{"workflow_id": "echo"}, 422); bad["detail"] != "take "+sizeless+" has no size" {
		t.Fatal(bad)
	}
}

func TestBatchImages(t *testing.T) {
	e := setup(t)
	mine, other := e.image(userA), e.image(userA)
	body := func(ids ...string) map[string]any {
		return map[string]any{"workflow_id": "echo", "inputs": map[string]any{"prompt.text": "at night"},
			"rows": map[string]any{"lines": map[string]any{"param": "echo.refs", "values": ids}}}
	}
	// user B cannot put A's take in a batch
	if bad := e.call(userB, "POST", "/v1/studio/batches", body(e.image(userB), mine), 422); bad["detail"] != "row 2: echo.refs[1].image: take "+mine+" not found" {
		t.Fatal(bad)
	}
	batch := e.submit(userA, body(mine, other))
	e.sent()
	created, refused := e.ask(userA, batch, 1, nil)
	if refused != nil || !created.Created {
		t.Fatal(created, refused)
	}
	// a take deleted after Submit: the row's run is refused, not created with a dead reference
	e.call(userA, "DELETE", "/v1/studio/takes/"+other, nil, 204)
	if _, refused := e.ask(userA, batch, 2, nil); refused == nil || refused.Code != "invalid" ||
		refused.Error != "echo.refs[1].image: take "+other+" not found" {
		t.Fatal(refused)
	}
}
