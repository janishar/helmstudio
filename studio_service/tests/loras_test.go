// Integration tests of the LoRA library (contracts/README.md "LoRAs"): upload, the checks at complete, the library,
// rename and delete, the caps, LoRA values in runs, batch rows, workflows and pipeline steps, the command, the
// dispatcher's host rule, made_with and reuse, the hosts redaction, and isolation between users. The LoRA files are
// synthetic: contracts/fixtures/loras headers with random data.
package tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/lora"
)

const (
	inkWash   = "3f0a6c1e9b2d4a7c8e5f1b3d6a9c2e4f7b0d3a6c9e2f5b8d1a4c7e0f3b6d9a2c" // shared, qwen-image
	fluxStyle = "9d4c2b7a1e6f3c8d5a0b9e4f2c7d1a6b3e8f5c0d9a4b2e7f1c6d3a8b5e0f9c4d" // shared, unknown
)

// loraFile writes contracts/fixtures/loras/<name>.json as a safetensors file.
func loraFile(t *testing.T, name string) []byte {
	t.Helper()
	var fixture struct {
		Metadata map[string]string `json:"metadata"`
		Tensors  map[string]struct {
			DType string  `json:"dtype"`
			Shape []int64 `json:"shape"`
		} `json:"tensors"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "loras", name+".json"))
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]any{"__metadata__": fixture.Metadata}
	var offset int64
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for k := range fixture.Tensors {
			if !yield(k) {
				return
			}
		}
	}) {
		tensor := fixture.Tensors[key]
		size := int64(map[string]int{"F32": 4, "F16": 2, "BF16": 2}[tensor.DType])
		for _, d := range tensor.Shape {
			size *= d
		}
		header[key] = map[string]any{"dtype": tensor.DType, "shape": tensor.Shape, "data_offsets": []int64{offset, offset + size}}
		offset += size
	}
	text, _ := json.Marshal(header)
	out := append(binary.LittleEndian.AppendUint64(nil, uint64(len(text))), text...)
	body := make([]byte, offset)
	_, _ = rand.Read(body)
	return append(out, body...)
}

// shared publishes the canonical shared LoRAs on host mac (ink-wash for qwen-image, flux-style unknown).
func (e *env) shared() {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "qwen-image.loras.valid.json"))
	if err == nil {
		err = e.registry.SetRegistry("mac.torch.loras", data)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

// upload starts an upload of file as user, PUTs it into the store and completes it, wanting the status; it returns
// the LoRA id and the complete call's body.
func (e *env) upload(user, filename string, file []byte, want int) (string, map[string]any) {
	e.t.Helper()
	created := e.call(user, "POST", "/v1/studio/loras", map[string]any{"filename": filename, "bytes": len(file)}, 201)
	id := created["lora"].(map[string]any)["id"].(string)
	key := "u/" + user + "/loras/" + id + ".safetensors"
	if !strings.Contains(created["upload_url"].(string), "/files/"+key+"?") || created["content_type"] != "application/octet-stream" {
		e.t.Fatal(created)
	}
	if err := e.store.Put(context.Background(), key, bytes.NewReader(file), int64(len(file)), "application/octet-stream"); err != nil {
		e.t.Fatal(err)
	}
	return id, e.call(user, "POST", "/v1/studio/loras/"+id+"/complete", nil, want)
}

// names lists the names of a LoRA list.
func names(list map[string]any) []string {
	var out []string
	for _, item := range list["loras"].([]any) {
		out = append(out, item.(map[string]any)["name"].(string))
	}
	return out
}

func TestLoraUploadAndLibrary(t *testing.T) {
	e := setup(t)
	e.qwen()
	e.shared()
	file := loraFile(t, "peft")
	sum := sha256.Sum256(file)

	// complete before the PUT is 409; then the checks, the hash, the family
	created := e.call(userA, "POST", "/v1/studio/loras", map[string]any{"filename": "My Style.SafeTensors", "bytes": len(file)}, 201)
	pending := created["lora"].(map[string]any)
	if pending["state"] != "pending" || pending["name"] != "My Style" || pending["source"] != "mine" || pending["rank"] != nil {
		t.Fatal(created)
	}
	id := pending["id"].(string)
	e.call(userA, "POST", "/v1/studio/loras/"+id+"/complete", nil, 409)
	_ = e.store.Put(context.Background(), "u/"+userA+"/loras/"+id+".safetensors", bytes.NewReader(file), int64(len(file)), "application/octet-stream")
	ready := e.call(userA, "POST", "/v1/studio/loras/"+id+"/complete", nil, 200)
	if ready["state"] != "ready" || ready["family"] != "qwen-image" || ready["key_style"] != "peft" || ready["rank"] != float64(4) ||
		ready["licence"] != "apache-2.0" || ready["sha256"] != hex.EncodeToString(sum[:]) || len(ready["hosts"].([]any)) != 0 {
		t.Fatal(ready)
	}
	if again := e.call(userA, "POST", "/v1/studio/loras/"+id+"/complete", nil, 200); again["state"] != "ready" {
		t.Fatal(again)
	}

	// rejected uploads stay listed with their reason; complete answers the same 422 again
	pickle := append([]byte{0x80, 0x04}, make([]byte, 30)...)
	bad, failed := e.upload(userA, "pickled.safetensors", pickle, 422)
	if failed["detail"] != "pickle checkpoints are not accepted; convert it to safetensors" {
		t.Fatal(failed)
	}
	e.call(userA, "POST", "/v1/studio/loras/"+bad+"/complete", nil, 422)
	if got := e.call(userA, "GET", "/v1/studio/loras/"+bad, nil, 200); got["state"] != "rejected" || got["reason"] != failed["detail"] {
		t.Fatal(got)
	}
	_, failed = e.upload(userA, "te.safetensors", loraFile(t, "kohya-te"), 422)
	if !strings.HasSuffix(failed["detail"].(string), " is not a LoRA weight") {
		t.Fatal(failed)
	}
	other, _ := e.upload(userA, "flux.safetensors", loraFile(t, "other-model"), 200) // fits no published family
	if got := e.call(userA, "GET", "/v1/studio/loras/"+other, nil, 200); got["family"] != "unknown" || got["state"] != "ready" {
		t.Fatal(got)
	}
	short := e.call(userA, "POST", "/v1/studio/loras", map[string]any{"filename": "short.safetensors", "bytes": len(file) + 1}, 201)
	shortID := short["lora"].(map[string]any)["id"].(string)
	_ = e.store.Put(context.Background(), "u/"+userA+"/loras/"+shortID+".safetensors", bytes.NewReader(file), int64(len(file)), "application/octet-stream")
	if got := e.call(userA, "POST", "/v1/studio/loras/"+shortID+"/complete", nil, 422); got["detail"] != "size differs from the declared bytes" {
		t.Fatal(got)
	}

	// the library: own first (newest first), then shared by name; filters
	all := e.call(userA, "GET", "/v1/studio/loras", nil, 200)
	if got := names(all); !slices.Equal(got, []string{"short", "flux", "te", "pickled", "My Style", "flux-style", "ink-wash"}) {
		t.Fatal(got)
	}
	ink := all["loras"].([]any)[6].(map[string]any)
	if ink["source"] != "shared" || ink["id"] != inkWash || !slices.Equal(ink["hosts"].([]any), []any{"mac"}) || ink["state"] != "ready" {
		t.Fatal(ink)
	}
	if got := names(e.call(userA, "GET", "/v1/studio/loras?source=shared&family=unknown", nil, 200)); !slices.Equal(got, []string{"flux-style"}) {
		t.Fatal(got)
	}
	if got := names(e.call(userA, "GET", "/v1/studio/loras?q=MY+sty", nil, 200)); !slices.Equal(got, []string{"My Style"}) {
		t.Fatal(got)
	}
	e.call(userA, "GET", "/v1/studio/loras?source=all", nil, 422)

	// rename, shared ones are managed on their host
	if got := e.call(userA, "PATCH", "/v1/studio/loras/"+id, map[string]any{"name": "  Ink sketch "}, 200); got["name"] != "Ink sketch" {
		t.Fatal(got)
	}
	e.call(userA, "PATCH", "/v1/studio/loras/"+id, map[string]any{"name": "  "}, 422)
	e.call(userA, "PATCH", "/v1/studio/loras/"+id, map[string]any{"name": "a\tb"}, 422)
	e.call(userA, "PATCH", "/v1/studio/loras/"+inkWash, map[string]any{"name": "x"}, 409)
	e.call(userA, "DELETE", "/v1/studio/loras/"+inkWash, nil, 409)
	e.call(userA, "POST", "/v1/studio/loras/"+inkWash+"/complete", nil, 404)
	e.call(userA, "GET", "/v1/studio/loras/"+strings.Repeat("0", 64), nil, 404)
	e.call(userA, "GET", "/v1/studio/loras/not-an-id", nil, 404)

	// user B: A's LoRA is 404 everywhere and in no list
	for _, call := range [][2]string{{"GET", ""}, {"POST", "/complete"}, {"DELETE", ""}} {
		e.call(userB, call[0], "/v1/studio/loras/"+id+call[1], nil, 404)
	}
	e.call(userB, "PATCH", "/v1/studio/loras/"+id, map[string]any{"name": "mine now"}, 404)
	if got := names(e.call(userB, "GET", "/v1/studio/loras", nil, 200)); !slices.Equal(got, []string{"flux-style", "ink-wash"}) {
		t.Fatal(got)
	}

	// delete: gone at once; the sweep removes the object a day later
	e.call(userA, "DELETE", "/v1/studio/loras/"+other, nil, 204)
	e.call(userA, "GET", "/v1/studio/loras/"+other, nil, 404)
	e.exec(`UPDATE loras SET deleted_at = clock_timestamp() - interval '2 days' WHERE id = $1`, other)
	if n, err := lora.Sweep(context.Background(), e.pool, e.store); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := e.store.Stat(context.Background(), "u/"+userA+"/loras/"+other+".safetensors"); err == nil {
		t.Fatal("the swept object is still there")
	}
	if _, err := e.store.Stat(context.Background(), "u/"+userA+"/loras/"+bad+".safetensors"); err == nil {
		t.Fatal("a rejected LoRA's object stays")
	}
	if got := e.call(userA, "GET", "/v1/studio/loras/"+bad, nil, 200); got["state"] != "rejected" {
		t.Fatal("a rejected LoRA stays listed", got)
	}
}

func TestLoraCaps(t *testing.T) {
	e := setup(t)
	create := func(user, filename string, size int64, want int) map[string]any {
		return e.call(user, "POST", "/v1/studio/loras", map[string]any{"filename": filename, "bytes": size}, want)
	}
	if got := create(userA, "model.ckpt", 10, 422); got["detail"] != "only .safetensors files can be uploaded" {
		t.Fatal(got)
	}
	if got := create(userA, "big.safetensors", lora.MaxBytes+1, 422); got["detail"] != "file is larger than 2 GB" {
		t.Fatal(got)
	}
	e.call(userA, "POST", "/v1/studio/loras", map[string]any{"filename": "a.safetensors", "bytes": 1, "path": "x"}, 422)
	for range 10 { // 10 × 2 GiB = 20 GiB: at the cap, not over it
		create(userA, "big.safetensors", lora.MaxBytes, 201)
	}
	if got := create(userA, "one-more.safetensors", 1, 409); got["detail"] != "Your LoRAs would use more than 20 GB; delete some first." {
		t.Fatal(got)
	}
	create(userB, "b.safetensors", lora.MaxBytes, 201)                                     // per user
	e.exec(`UPDATE loras SET state = 'rejected', reason = 'x' WHERE owner_id = $1`, userA) // rejected ones count, not their bytes
	for range lora.MaxLoras - 10 {
		create(userA, "small.safetensors", 1, 201)
	}
	if got := create(userA, "fifty-one.safetensors", 1, 409); got["detail"] != "You have 50 LoRAs; delete some first." {
		t.Fatal(got)
	}
	// a pending upload older than an hour is deleted by the sweep, its count freed
	e.exec(`UPDATE loras SET created_at = clock_timestamp() - interval '2 hours' WHERE owner_id = $1 AND state = 'pending'`, userA)
	if n, err := lora.Sweep(context.Background(), e.pool, e.store); err != nil || n != lora.MaxLoras-10 {
		t.Fatal(n, err)
	}
	create(userA, "again.safetensors", 1, 201)
}

// qwenRun asks for a qwen-image run with the lora rows and wants the status.
func (e *env) qwenRun(user, sessionID string, rows []any, want int) map[string]any {
	e.t.Helper()
	return e.call(user, "POST", "/v1/studio/sessions/"+sessionID+"/runs", map[string]any{"workflow_id": "qwen-image",
		"inputs": map[string]any{"prompt.text": "a fox", "lora.rows": rows}}, want)
}

// row is a lora.rows item.
func row(id string, strength float64, mode string) map[string]any {
	return map[string]any{"lora": map[string]any{"lora_id": id}, "strength": strength, "mode": mode}
}

func TestLoraRuns(t *testing.T) {
	e := setup(t)
	e.qwen()
	e.shared()
	mine, _ := e.upload(userA, "sketch.safetensors", loraFile(t, "kohya"), 200)
	theirs, _ := e.upload(userB, "b-style.safetensors", loraFile(t, "comfyui"), 200)
	unknown, _ := e.upload(userA, "flux.safetensors", loraFile(t, "other-model"), 200)
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)

	// the run's checks: one message for anything the caller cannot use (another user's included)
	for _, test := range []struct {
		rows   []any
		detail string
	}{
		{[]any{row(theirs, 1, "adapter")}, "lora.rows[1].lora: LoRA " + theirs + " not found"},
		{[]any{row(strings.Repeat("a", 64), 1, "adapter")}, "lora.rows[1].lora: LoRA " + strings.Repeat("a", 64) + " not found"},
		{[]any{row(mine, 1, "adapter"), row(fluxStyle, 1, "adapter")}, "lora.rows[2].lora: LoRA flux-style does not fit qwen-image (its model is unknown)"},
		{[]any{row(unknown, 1, "adapter")}, "lora.rows[1].lora: LoRA flux does not fit qwen-image (its model is unknown)"},
		{[]any{row(mine, 1, "adapter"), row(inkWash, 1, "bake"), row(mine, 0.5, "adapter")}, "lora.rows[3].lora: LoRA sketch is already in row 1"},
		{[]any{row(mine, 5, "adapter")}, "lora.rows[1].strength: must be between -4 and 4"},
		{[]any{map[string]any{"strength": 1}}, "lora.rows[1].lora: a LoRA is required"},
		{[]any{map[string]any{"lora": map[string]any{"lora_id": mine, "key": "u/x"}}}, `lora.rows[1].lora: must be {"lora_id": <id>}`},
	} {
		if got := e.qwenRun(userA, session, test.rows, 422); got["detail"] != test.detail {
			t.Errorf("got %v, want %s", got["detail"], test.detail)
		}
	}

	// a good run: the command carries each LoRA resolved, the own one with its key under the owner's prefix
	run := e.qwenRun(userA, session, []any{row(mine, 0.8, "adapter"), row(inkWash, 1, "bake")}, 202)
	if run["status"] != "queued" {
		t.Fatal(run)
	}
	nodes := e.command()
	sent := nodes["lora"].Params["rows"].([]any)
	own, shared := sent[0].(map[string]any)["lora"].(map[string]any), sent[1].(map[string]any)["lora"].(map[string]any)
	if own["source"] != "own" || own["name"] != "sketch" || own["key"] != "u/"+userA+"/loras/"+mine+".safetensors" ||
		own["sha256"] == nil || own["bytes"] == nil || shared["source"] != "shared" || shared["sha256"] != inkWash ||
		shared["name"] != "ink-wash" || shared["key"] != nil {
		t.Fatal(sent)
	}
	if stored := e.stored(run["id"].(string))["lora"].Params["rows"].([]any)[0].(map[string]any)["lora"]; len(stored.(map[string]any)) != 1 {
		t.Fatal("the stored graph keeps {lora_id}", stored)
	}

	// the take: made_with.loras; reuse says whether each is usable now; the LoRA's thumb is that take
	runID := run["id"].(string)
	e.emit(userA, session, runID, "status", 1, map[string]any{"status": "running", "host": "mac"})
	e.emit(userA, session, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.png",
		"kind": "image", "width": 64, "height": 64, "bytes": 10})
	e.emit(userA, session, runID, "status", 3, map[string]any{"status": "succeeded"})
	takeID := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
	made := e.call(userA, "GET", "/v1/studio/takes/"+takeID, nil, 200)["made_with"].(map[string]any)
	used := made["loras"].([]any)
	if len(used) != 2 || used[0].(map[string]any)["node"] != "lora" || used[0].(map[string]any)["source"] != "mine" ||
		used[0].(map[string]any)["strength"] != 0.8 || used[1].(map[string]any)["mode"] != "bake" || used[1].(map[string]any)["bytes"] != nil {
		t.Fatal(used)
	}
	if got := e.call(userA, "GET", "/v1/studio/loras/"+mine, nil, 200); got["thumb_url"] == nil {
		t.Fatal("no thumb", got)
	}
	if got := e.call(userB, "GET", "/v1/studio/loras/"+inkWash, nil, 200); got["thumb_url"] != nil {
		t.Fatal("B sees A's take as the thumb", got)
	}
	status := func() []any {
		return e.call(userA, "POST", "/v1/studio/takes/"+takeID+"/reuse", nil, 200)["loras_status"].([]any)
	}
	if got := status(); got[0].(map[string]any)["status"] != "current" || got[1].(map[string]any)["status"] != "current" {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", "/v1/studio/loras/"+mine, nil, 204)
	_ = e.registry.SetRegistry("mac.torch.loras", nil)
	if got := status(); got[0].(map[string]any)["status"] != "deleted" || got[1].(map[string]any)["status"] != "missing" {
		t.Fatal(got)
	}
	e.qwenRun(userA, session, []any{row(mine, 0.8, "adapter")}, 422) // a deleted one cannot run again
}

func TestLoraDispatchAndIsolation(t *testing.T) {
	e := setup(t)
	e.qwen()
	e.shared()
	mine, _ := e.upload(userA, "sketch.safetensors", loraFile(t, "kohya"), 200)
	theirs, _ := e.upload(userB, "b-style.safetensors", loraFile(t, "comfyui"), 200)
	session := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	failed := func(runID string) any { return e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["error"] }

	// a queued run whose own LoRA was deleted fails at the next pass; one whose shared LoRA left every host too
	e.busyMac()
	gone := e.qwenRun(userA, session, []any{row(mine, 1, "adapter")}, 202)["id"].(string)
	away := e.qwenRun(userA, session, []any{row(inkWash, 1, "adapter")}, 202)["id"].(string)
	e.call(userA, "DELETE", "/v1/studio/loras/"+mine, nil, 204)
	_ = e.registry.SetRegistry("mac.torch.loras", []byte(`{"loras": []}`))
	e.relay()
	if failed(gone) != "LoRA sketch was deleted" || failed(away) != "no live host for env torch has LoRA ink-wash" {
		t.Fatal(failed(gone), failed(away))
	}
	e.shared()

	// B names A's LoRA: refused in a run, a batch row, a workflow document's run and a pipeline step
	sessionB := e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	mineToo, _ := e.upload(userA, "sketch2.safetensors", loraFile(t, "peft"), 200)
	notFound := "lora.rows[1].lora: LoRA " + mineToo + " not found"
	if got := e.qwenRun(userB, sessionB, []any{row(mineToo, 1, "adapter")}, 422); got["detail"] != notFound {
		t.Fatal(got)
	}
	batch := e.call(userB, "POST", "/v1/studio/batches", map[string]any{"workflow_id": "qwen-image",
		"inputs": map[string]any{"lora.rows": []any{row(mineToo, 1, "adapter")}},
		"rows":   map[string]any{"lines": map[string]any{"param": "prompt.text", "values": []string{"a fox"}}}}, 422)
	if batch["detail"] != "row 1: "+notFound {
		t.Fatal(batch)
	}
	template := e.call(userB, "GET", "/v1/studio/workflows/qwen-image", nil, 200)
	doc := map[string]any{"title": "B's", "family": "qwen-image", "env": "torch", "nodes": template["nodes"]}
	doc["nodes"].(map[string]any)["lora"].(map[string]any)["params"] = map[string]any{"rows": []any{row(mineToo, 1, "adapter")}}
	saved := e.call(userB, "POST", "/v1/studio/workflows", doc, 201) // lora_unknown blocks running only
	problems := saved["problems"].([]any)
	if len(problems) != 1 || problems[0].(map[string]any)["code"] != "lora_unknown" ||
		strings.Contains(problems[0].(map[string]any)["message"].(string), "sketch2") {
		t.Fatal(problems)
	}
	if valid := e.call(userA, "POST", "/v1/studio/workflows/validate", doc, 200); valid["can_run"] != true {
		t.Fatal("A can run it", valid)
	}
	got := e.call(userB, "POST", "/v1/studio/sessions/"+sessionB+"/runs", map[string]any{"workflow_id": saved["id"]}, 422)
	if got["detail"] != notFound {
		t.Fatal(got)
	}
	pipeline := e.call(userB, "POST", "/v1/studio/pipelines", map[string]any{"title": "B's pipeline", "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
		"s1": map[string]any{"kind": "step", "workflow": "qwen-image", "inputs": map[string]any{"lora.rows": []any{row(mineToo, 1, "adapter")}},
			"bind": map[string]any{"prompt.text": "$prompt"}},
		"out": map[string]any{"kind": "output", "from": "s1.takes", "collection": "x"},
	}, "edges": []any{[]any{"inputs", "s1"}, []any{"s1", "out"}}}, 201)
	got = e.call(userB, "POST", "/v1/studio/batches", map[string]any{"pipeline_id": pipeline["id"],
		"rows": map[string]any{"table": map[string]any{"columns": []string{"inputs.prompt"}, "rows": [][]any{{"a fox"}}}}}, 422)
	if got["detail"] != "row 1: s1: "+notFound {
		t.Fatal(got)
	}
	// B's own run: its command names B's key only
	e.relay() // frees nothing: mac is busy
	e.idleMac()
	e.qwenRun(userB, sessionB, []any{row(theirs, 1, "bake")}, 202)
	sent := e.command()["lora"].Params["rows"].([]any)[0].(map[string]any)["lora"].(map[string]any)
	if sent["key"] != "u/"+userB+"/loras/"+theirs+".safetensors" {
		t.Fatal(sent)
	}

	// GET /hosts: a baked LoRA of another user's shows only its size
	strength := 1.0
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "mac", "env": "torch", "state": "busy", "run_id": nil,
		"updated_at": time.Now().UTC(), "published_by": "test", "resident": []any{map[string]any{"component": "transformer",
			"model": "qwen-image-2.1", "variant": "base", "precision": "int8", "gb": 20, "pinned": true, "loras": []any{
				map[string]any{"lora_id": theirs, "name": "b-style", "strength": strength, "bytes": 100},
				map[string]any{"lora_id": inkWash, "name": "ink-wash", "strength": strength, "bytes": 200}}}}})
	if err := e.registry.SetHost("mac.torch", value); err != nil {
		t.Fatal(err)
	}
	baked := func(user string) []any {
		for _, host := range e.call(user, "GET", "/v1/studio/hosts", nil, 200)["hosts"].([]any) {
			if host.(map[string]any)["host"] == "mac" {
				return host.(map[string]any)["resident"].([]any)[0].(map[string]any)["loras"].([]any)
			}
		}
		return nil
	}
	if b := baked(userB); b[0].(map[string]any)["name"] != "b-style" || b[1].(map[string]any)["name"] != "ink-wash" {
		t.Fatal(b)
	}
	a := baked(userA)
	if hidden := a[0].(map[string]any); hidden["lora_id"] != nil || hidden["name"] != nil || hidden["strength"] != nil ||
		hidden["bytes"] != float64(100) || a[1].(map[string]any)["name"] != "ink-wash" {
		t.Fatal(a)
	}
}

// busyMac publishes host mac busy with a run studio does not know: the dispatcher still sees it free by its own table,
// so a run of another user is put on it first.
func (e *env) busyMac() {
	e.t.Helper()
	other := e.call(userC, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	e.qwenRun(userC, other, []any{}, 202)
	e.relay() // userC's run holds mac
}

// idleMac ends what holds mac: every released run on it is marked succeeded.
func (e *env) idleMac() {
	e.t.Helper()
	e.exec(`UPDATE runs SET status = 'succeeded', finished_at = clock_timestamp() WHERE host = 'mac' AND status IN ('queued', 'running')`)
}
