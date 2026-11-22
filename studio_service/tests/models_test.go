// Integration tests of M5a (API 1.16): the Models screen, model jobs (refusals, idempotency, release one per host,
// the projector, cancel), the hosts admin (settings, pins, unloads, the admin view) and the authority of every admin
// route.
package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// qwenModels is host mac's torch models key for these tests: base present, turbo missing and gated behind a licence
// to accept; turbo loads base's text encoder and vae.
func qwenModels(turbo string) []byte {
	component := func(name, from string, bytes int) map[string]any {
		c := map[string]any{"component": name, "from_variant": from, "precisions": []string{"bf16", "int8"}, "bytes": bytes}
		if turbo != "missing" || from == "base" { // API 1.34: read from the files on disk
			c["stored"] = "bf16"
		}
		return c
	}
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "mac", "env": "torch",
		"published_at": time.Now().UTC(), "published_by": "test",
		"licences": []map[string]any{{"id": "apache-2.0", "title": "Apache 2.0", "text": "Apache"},
			{"id": "qwen-turbo", "title": "Turbo licence", "text": "Accept me"}},
		"models": []map[string]any{{"id": "qwen-image-2.1", "family": "qwen-image", "title": "Qwen Image 2.1",
			"media": []string{"image"}, "runtimes": []map[string]any{{"runtime": "torch", "env": "torch"}},
			"variants": []map[string]any{
				{"id": "base", "title": "Base", "presence": "present", "bytes": 300, "precisions": []string{"bf16", "int8"},
					"dir": "Qwen-Image-2.1", "source": map[string]any{"repo": "Qwen/Qwen-Image-2.1", "revision": "main",
						"allow_patterns": []string{"*"}, "licence": "apache-2.0", "must_accept": false, "gated": false, "bytes": 300},
					"components": []any{component("transformer", "base", 100), component("text_encoder", "base", 150), component("vae", "base", 50)}},
				{"id": "turbo", "title": "Turbo", "presence": turbo, "precisions": []string{"bf16", "int8"}, "devices": []string{"mps"},
					"dir": "Qwen-Image-2.1-Turbo", "source": map[string]any{"repo": "Qwen/Qwen-Image-2.1-Turbo", "revision": "main",
						"allow_patterns": []string{"*"}, "licence": "qwen-turbo", "must_accept": true, "gated": true, "bytes": 90},
					"components": []any{component("transformer", "turbo", 90), component("text_encoder", "base", 150), component("vae", "base", 50)}},
			}}}})
	return value
}

// modelHost publishes mac.<env> idle, refreshed at, with the M5a fields and resident.
func (e *env) modelHost(env string, at time.Time, resident []map[string]any, extra map[string]any) {
	e.t.Helper()
	value := map[string]any{"host": "mac", "env": env, "state": "idle", "run_id": nil, "updated_at": at.UTC(),
		"resident": resident, "memory": map[string]any{"budget_gb": 48, "used_gb": 2},
		"models_disk": map[string]any{"root": "~/GenAI/models", "free_bytes": 1000, "total_bytes": 5000},
		"machine":     map[string]any{"os": "macos", "accel": "metal", "packaging": "native", "ram_gb": 64}, "hub_token": true}
	for k, v := range extra {
		value[k] = v
	}
	raw, _ := json.Marshal(value)
	if err := e.registry.SetHost("mac."+env, raw); err != nil {
		e.t.Fatal(err)
	}
}

// modelsSetup is twoEnvs with qwenModels on torch and the hosts keys' M5a fields.
func (e *env) modelsSetup(turbo string) {
	e.t.Helper()
	e.twoEnvs()
	if err := e.registry.SetRegistry("mac.torch.models", qwenModels(turbo)); err != nil {
		e.t.Fatal(err)
	}
	e.modelHost("torch", time.Now(), nil, nil)
	e.modelHost("mlx", time.Now(), nil, nil)
}

// release releases model jobs and drains the outbox: the subjects, Nats-Msg-Ids and commands' data.
func (e *env) release() ([]string, []string, []map[string]any) {
	e.t.Helper()
	if err := studio.ReleaseModelJobs(context.Background(), e.pool, e.registry, nil, time.Now()); err != nil {
		e.t.Fatal(err)
	}
	var subjects, ids []string
	var data []map[string]any
	_, err := messaging.RelayOnce(context.Background(), e.pool, func(_ context.Context, subject string, envelope *gen.Envelope, msgID string) error {
		var d map[string]any
		_ = json.Unmarshal(envelope.Data, &d)
		subjects, ids, data = append(subjects, subject), append(ids, msgID), append(data, d)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return subjects, ids, data
}

// modelEvent projects one evt.model.mac.<kind> of job as the runner would publish it.
func (e *env) modelEvent(jobID, kind string, delivery, seq int, data map[string]any) {
	e.t.Helper()
	data["seq"], data["delivery"] = seq, delivery
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.model." + kind, Version: 1, ID: utility.NewID(), JobID: &jobID, OwnerID: admin,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := e.events.Handle(context.Background(), "evt.model.mac."+kind, body); err != nil {
		e.t.Fatal(err)
	}
}

// dump writes body as <schema>.<n>.json under CONTRACT_DUMP (unset: nothing), for a check against the openapi schema.
func dump(t *testing.T, schema string, body any) {
	dir := os.Getenv("CONTRACT_DUMP")
	if dir == "" {
		return
	}
	raw, _ := json.MarshalIndent(body, "", " ")
	if err := os.WriteFile(filepath.Join(dir, schema+"."+utility.NewID()[:8]+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// variantOn is the state of model's variant on host in GET /models as user.
func (e *env) variantOn(user, model, variant, host string) map[string]any {
	e.t.Helper()
	for _, m := range e.call(user, "GET", "/v1/studio/models", nil, 200)["models"].([]any) {
		if m.(map[string]any)["id"] != model {
			continue
		}
		for _, v := range m.(map[string]any)["variants"].([]any) {
			if v.(map[string]any)["id"] == variant {
				for _, h := range v.(map[string]any)["hosts"].([]any) {
					if h.(map[string]any)["host"] == host {
						return h.(map[string]any)
					}
				}
			}
		}
	}
	e.t.Fatalf("%s %s not on %s", model, variant, host)
	return nil
}

// Every admin route answers 403 to anyone else, before anything else is checked.
func TestModelsAuthority(t *testing.T) {
	e := setup(t)
	e.modelsSetup("missing")
	job := utility.NewID()
	for _, r := range []struct{ method, path string }{
		{"POST", "/v1/studio/admin/models/qwen-image-2.1/download"},
		{"DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac"},
		{"DELETE", "/v1/studio/admin/loras/" + strings.Repeat("a", 64) + "?host=mac"},
		{"GET", "/v1/studio/admin/model-jobs"},
		{"GET", "/v1/studio/admin/model-jobs/" + job},
		{"POST", "/v1/studio/admin/model-jobs/" + job + "/cancel"},
		{"POST", "/v1/studio/admin/hosts/mac/unload"},
		{"PUT", "/v1/studio/admin/hosts/mac/settings"},
		{"PUT", "/v1/studio/admin/hosts/mac/pins"},
		{"POST", "/v1/studio/admin/imports/takes"},
		{"POST", "/v1/studio/admin/imports/takes/" + job + "/complete"},
	} {
		if got := e.call(userA, r.method, r.path, map[string]any{"host": "mac"}, 403); got["detail"] != "admin role required" {
			t.Fatal(r, got)
		}
	}
}

// The Models screen, a download (licence, idempotency, release, progress, cancel, end), a delete (keeps, loaded, in
// use) and a shared LoRA's delete.
func TestModelJobs(t *testing.T) {
	e := setup(t)
	e.modelsSetup("missing")
	var purged []string
	e.service.SetModelOps(func(_ context.Context, subject string) error { purged = append(purged, subject); return nil }, nil)

	list := e.call(userA, "GET", "/v1/studio/models", nil, 200)
	dump(t, "ModelList", list)
	dump(t, "ModelList", e.call(admin, "GET", "/v1/studio/models", nil, 200))
	models := list["models"].([]any)
	qwen := models[slices.IndexFunc(models, func(m any) bool { return m.(map[string]any)["id"] == "qwen-image-2.1" })]
	variants := qwen.(map[string]any)["variants"].([]any)
	baseV, turboV := variants[0].(map[string]any), variants[1].(map[string]any)
	if _, ok := baseV["devices"]; ok || !slices.Equal(turboV["devices"].([]any), []any{"mps"}) {
		t.Fatal(baseV["devices"], turboV["devices"]) // API 1.34: devices passed on, absent when the key has none
	}
	if tc := turboV["components"].([]any); tc[0].(map[string]any)["stored"] != nil || tc[1].(map[string]any)["stored"] != "bf16" {
		t.Fatal(tc) // turbo's own transformer is on no disk: no stored; base's text encoder is
	}
	hosts := list["hosts"].([]any)
	if len(hosts) != 2 || hosts[1].(map[string]any)["live"] != true || hosts[1].(map[string]any)["models_root"] != "~/GenAI/models" {
		t.Fatal(hosts) // fake, then mac
	}
	if _, ok := hosts[1].(map[string]any)["hub_token"]; ok {
		t.Fatal("hub_token shown to a user")
	}
	if e.call(admin, "GET", "/v1/studio/models", nil, 200)["hosts"].([]any)[1].(map[string]any)["hub_token"] != true {
		t.Fatal("admin hub_token")
	}
	if got := e.variantOn(userA, "qwen-image-2.1", "base", "mac"); got["state"] != "present" || got["bytes"] != 300.0 {
		t.Fatal(got)
	}
	if got := e.variantOn(userA, "qwen-image-2.1", "turbo", "mac"); got["state"] != "missing" || got["bytes"] != nil {
		t.Fatal(got)
	}
	var licences []string
	for _, l := range list["licences"].([]any) {
		licences = append(licences, l.(map[string]any)["id"].(string))
	}
	if !slices.Contains(licences, "qwen-turbo") || !slices.Contains(licences, "apache-2.0") {
		t.Fatal(licences)
	}

	download := "/v1/studio/admin/models/qwen-image-2.1/download"
	e.call(admin, "POST", "/v1/studio/admin/models/nope/download", map[string]any{"host": "mac"}, 404)
	if got := e.call(admin, "POST", download, map[string]any{"host": "nope"}, 422); got["detail"] != "host nope has no env for qwen-image-2.1" {
		t.Fatal(got)
	}
	if got := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "turbo"}, 422); got["detail"] != "licence qwen-turbo must be accepted" {
		t.Fatal(got)
	}
	if got := e.call(admin, "POST", download, map[string]any{"host": "mac"}, 409); got["detail"] != "Qwen Image 2.1 Base is already on mac" {
		t.Fatal(got)
	}
	e.call(admin, "POST", download, map[string]any{"host": "mac", "token": "x"}, 422)
	job := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "turbo", "accept_licence": true}, 202)
	sum := sha256.Sum256([]byte("Accept me"))
	if job["state"] != "queued" || job["env"] != "torch" || job["title"] != "Qwen Image 2.1 · Turbo" ||
		job["licence"].(map[string]any)["sha256"] != hex.EncodeToString(sum[:]) || job["requested_by"] != admin {
		t.Fatal(job)
	}
	jobID := job["id"].(string)
	if again := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "turbo", "accept_licence": true}, 200); again["id"] != jobID {
		t.Fatal(again)
	}
	if got := e.variantOn(userA, "qwen-image-2.1", "turbo", "mac"); got["state"] != "downloading" || got["job"].(map[string]any)["state"] != "queued" {
		t.Fatal(got)
	}
	if got := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=turbo", nil, 409); got["detail"] != "Qwen Image 2.1 Turbo is not on mac" {
		t.Fatal(got)
	}

	// a shared LoRA's delete queues behind the download: one released job per host
	e.shared()
	ink := ""
	for id := range e.registry.Published()["mac.torch"].Loras {
		ink = e.registry.Published()["mac.torch"].Loras[id].ID
	}
	e.call(admin, "DELETE", "/v1/studio/admin/loras/"+ink, nil, 422)
	e.call(admin, "DELETE", "/v1/studio/admin/loras/"+utility.NewID()+"?host=mac", nil, 404)
	loraJob := e.call(admin, "DELETE", "/v1/studio/admin/loras/"+ink+"?host=mac", nil, 202)
	if loraJob["kind"] != "lora_delete" || loraJob["lora_id"] != ink {
		t.Fatal(loraJob)
	}
	subjects, ids, data := e.release()
	if len(subjects) != 1 || subjects[0] != "cmd.model.mac.torch" || ids[0] != jobID+":1" || data[0]["kind"] != "download" ||
		data[0]["variant"] != "turbo" || data[0]["licence"].(map[string]any)["id"] != "qwen-turbo" {
		t.Fatal(subjects, ids, data)
	}
	if s, _, _ := e.release(); len(s) != 0 {
		t.Fatal("a second job released to the host", s)
	}
	if got := e.call(admin, "POST", "/v1/studio/admin/model-jobs/"+loraJob["id"].(string)+"/cancel", nil, 200); got["state"] != "cancelled" {
		t.Fatal(got)
	}

	// the runner's events: running, progress (a repeat skipped), a cancel asked, the end, nothing after it
	e.modelEvent(jobID, "status", 1, 1, map[string]any{"status": "running"})
	e.modelEvent(jobID, "progress", 1, 2, map[string]any{"done_bytes": 40, "total_bytes": 90, "files_done": 1, "files_total": 3, "file": "a"})
	e.modelEvent(jobID, "progress", 1, 2, map[string]any{"done_bytes": 1, "total_bytes": 90, "files_done": 0, "files_total": 3})
	got := e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+jobID, nil, 200)
	dump(t, "ModelJob", got)
	dump(t, "ModelList", e.call(admin, "GET", "/v1/studio/models", nil, 200))
	if got["state"] != "running" || got["started_at"] == nil || got["progress"].(map[string]any)["done_bytes"] != 40.0 {
		t.Fatal(got)
	}
	if v := e.variantOn(userA, "qwen-image-2.1", "turbo", "mac"); v["job"].(map[string]any)["done_bytes"] != 40.0 {
		t.Fatal(v)
	}
	if got := e.call(admin, "POST", "/v1/studio/admin/model-jobs/"+jobID+"/cancel", nil, 200); got["state"] != "running" || !slices.Contains(e.cancelled, jobID) {
		t.Fatal(got, e.cancelled)
	}
	e.modelEvent(jobID, "status", 2, 1, map[string]any{"status": "running"}) // a redelivery after a runner restart
	e.modelEvent(jobID, "status", 2, 2, map[string]any{"status": "succeeded", "result": map[string]any{"bytes": 90, "files": 3, "reused": []string{"text_encoder", "vae"}}})
	e.modelEvent(jobID, "status", 2, 3, map[string]any{"status": "failed", "failure": map[string]any{"class": "internal", "detail": "late"}})
	got = e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+jobID, nil, 200)
	if got["state"] != "succeeded" || got["ended_at"] == nil || got["failure"] != nil || got["result"].(map[string]any)["files"] != 3.0 {
		t.Fatal(got)
	}
	e.call(admin, "POST", "/v1/studio/admin/model-jobs/"+jobID+"/cancel", nil, 409)
	if jobs := e.call(admin, "GET", "/v1/studio/admin/model-jobs?active=true", nil, 200)["jobs"].([]any); len(jobs) != 0 {
		t.Fatal(jobs)
	}
	dump(t, "ModelJobList", e.call(admin, "GET", "/v1/studio/admin/model-jobs?host=mac", nil, 200))
	if jobs := e.call(admin, "GET", "/v1/studio/admin/model-jobs?host=mac", nil, 200)["jobs"].([]any); len(jobs) != 2 {
		t.Fatal(jobs)
	}
	e.call(admin, "GET", "/v1/studio/admin/model-jobs?active=maybe", nil, 422)

	// turbo is on disk now: deleting base keeps the components turbo loads, refused while one it removes is loaded or
	// a queued run uses the model
	if err := e.registry.SetRegistry("mac.torch.models", qwenModels("present")); err != nil {
		t.Fatal(err)
	}
	e.modelHost("torch", time.Now(), []map[string]any{{"component": "transformer", "model": "qwen-image-2.1", "variant": "base",
		"precision": "int8", "gb": 7, "pinned": false}}, nil)
	deleteBase := "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=base"
	if got := e.call(admin, "DELETE", deleteBase, nil, 409); got["detail"] != "transformer of Qwen Image 2.1 is loaded on mac; unload it first" {
		t.Fatal(got)
	}
	e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1", nil, 422)
	e.modelHost("torch", time.Now(), []map[string]any{{"component": "text_encoder", "model": "qwen-image-2.1", "variant": "base",
		"precision": "int8", "gb": 7, "pinned": false}}, nil)
	runID, _ := e.start(userA, "qwen-image", turbo)
	if got := e.call(admin, "DELETE", deleteBase, nil, 409); got["detail"] != "Qwen Image 2.1 is in use by a queued or running run on mac" {
		t.Fatal(got, runID)
	}
	e.sql(`UPDATE runs SET status = 'cancelled', finished_at = now() WHERE id = $1`, runID)
	del := e.call(admin, "DELETE", deleteBase, nil, 202)
	dump(t, "ModelJob", del)
	keeps, _ := json.Marshal(del["keeps"])
	if del["kind"] != "delete" || del["variant"] != "base" ||
		string(keeps) != `[{"component":"text_encoder","for":["turbo"],"from_variant":"base"},{"component":"vae","for":["turbo"],"from_variant":"base"}]` {
		t.Fatal(del, string(keeps))
	}
	if again := e.call(admin, "DELETE", deleteBase, nil, 200); again["id"] != del["id"] {
		t.Fatal(again)
	}
	if subjects, _, data := e.release(); len(subjects) != 1 || data[0]["kind"] != "delete" || data[0]["variant"] != "base" {
		t.Fatal(subjects, data)
	}
	if got := e.call(admin, "POST", "/v1/studio/admin/model-jobs/"+del["id"].(string)+"/cancel", nil, 409); got["detail"] != "a running delete cannot be cancelled" {
		t.Fatal(got)
	}
	e.modelEvent(del["id"].(string), "status", 1, 1, map[string]any{"status": "succeeded", "result": map[string]any{"files": 2, "freed_bytes": 100, "kept": []string{"text_encoder", "vae"}}})

	// a refusal found at release fails the job (class in_use); a released download whose runner went quiet is
	// cancelled by studio, its command purged
	if err := e.registry.SetRegistry("mac.torch.models", qwenModels("missing")); err != nil {
		t.Fatal(err)
	}
	queued := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "turbo", "accept_licence": true}, 202)
	if err := e.registry.SetRegistry("mac.torch.models", qwenModels("present")); err != nil {
		t.Fatal(err)
	}
	e.release()
	got = e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+queued["id"].(string), nil, 200)
	if got["state"] != "failed" || got["failure"].(map[string]any)["class"] != "in_use" ||
		got["failure"].(map[string]any)["detail"] != "Qwen Image 2.1 Turbo is already on mac" {
		t.Fatal(got)
	}
	if err := e.registry.SetRegistry("mac.torch.models", qwenModels("partial")); err != nil {
		t.Fatal(err)
	}
	stale := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "turbo", "accept_licence": true}, 202)
	if s, _, _ := e.release(); len(s) != 1 {
		t.Fatal(s)
	}
	e.modelHost("torch", time.Now().Add(-2*time.Minute), nil, nil)
	if got := e.call(admin, "POST", "/v1/studio/admin/model-jobs/"+stale["id"].(string)+"/cancel", nil, 200); got["state"] != "cancelled" ||
		!slices.Contains(purged, "cmd.model.mac.torch") {
		t.Fatal(got, purged)
	}

	// a shared LoRA baked into a loaded component cannot be deleted
	e.modelHost("torch", time.Now(), []map[string]any{{"component": "transformer", "model": "qwen-image-2.1", "variant": "turbo",
		"precision": "int8", "gb": 7, "pinned": false, "loras": []map[string]any{{"lora_id": ink, "name": "Ink", "strength": 1, "bytes": 10}}}}, nil)
	if got := e.call(admin, "DELETE", "/v1/studio/admin/loras/"+ink+"?host=mac", nil, 409); !strings.Contains(got["detail"].(string), "is baked into a loaded component on mac; unload it first") {
		t.Fatal(got)
	}
}

// A released delete holds the runs that would load its model on that host until it ends; a released job studio no
// longer hears of fails after JobSilence, its command purged, and the host's next job goes out.
func TestModelJobGuards(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	e.modelHost("torch", time.Now(), nil, nil)
	e.modelHost("mlx", time.Now(), nil, nil)
	var purged []string

	del := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=base", nil, 202)
	if s, _, _ := e.release(); len(s) != 1 {
		t.Fatal(s)
	}
	runID, _ := e.start(userA, "qwen-image", turbo)
	if subjects, _ := e.relay(); len(subjects) != 0 {
		t.Fatal("a run was released to a host deleting its model", subjects)
	}
	e.modelEvent(del["id"].(string), "status", 1, 1, map[string]any{"status": "succeeded", "result": map[string]any{"files": 2, "freed_bytes": 100}})
	if subjects, _ := e.relay(); !slices.Equal(subjects, []string{"cmd.run.mac.torch"}) {
		t.Fatal(subjects, runID)
	}
	e.sql(`UPDATE runs SET status = 'cancelled', finished_at = now() WHERE id = $1`, runID)

	first := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=turbo", nil, 202)
	if s, _, _ := e.release(); len(s) != 1 {
		t.Fatal(s)
	}
	second := e.call(admin, "DELETE", "/v1/studio/admin/models/ltx-2.5?host=mac", nil, 202)
	e.modelEvent(first["id"].(string), "status", 1, 1, map[string]any{"status": "running"}) // heard: not silent
	e.sql(`UPDATE model_jobs SET released_at = released_at - interval '1 hour' WHERE id = $1`, first["id"])
	if s, _, _ := e.release(); len(s) != 0 {
		t.Fatal("released behind a job heard of lately", s)
	}
	e.sql(`UPDATE model_jobs SET heard_at = now() - $2::interval WHERE id = $1`, first["id"], (studio.JobSilence + time.Minute).String())
	purge := func(_ context.Context, subject string) error { purged = append(purged, subject); return nil }
	if err := studio.ReleaseModelJobs(context.Background(), e.pool, e.registry, purge, time.Now()); err != nil {
		t.Fatal(err)
	}
	subjects, _, data := e.release()
	got := e.call(admin, "GET", "/v1/studio/admin/model-jobs/"+first["id"].(string), nil, 200)
	if got["state"] != "failed" || got["failure"].(map[string]any)["detail"] != "the runner stopped reporting this job" ||
		!slices.Equal(purged, []string{"cmd.model.mac.torch"}) {
		t.Fatal(got, purged)
	}
	if len(subjects) != 1 || subjects[0] != "cmd.model.mac.mlx" || data[0]["model"] != "ltx-2.5" {
		t.Fatal("the host's next job did not go out", subjects, data, second["id"])
	}
}

// Settings and pins per host (stored, versioned, published to KV, applied when the runners report them), the admin's
// view of GET /hosts, and the admin's unloads through the dispatcher, which hold the host until answered.
func TestHostsAdmin(t *testing.T) {
	e := setup(t)
	e.modelsSetup("present")
	var mu sync.Mutex
	published := map[string][]byte{}
	e.service.SetModelOps(nil, func(_ context.Context, host string, value []byte) error {
		mu.Lock()
		defer mu.Unlock()
		published[host] = value
		return nil
	})

	settings := "/v1/studio/admin/hosts/mac/settings"
	e.call(admin, "PUT", "/v1/studio/admin/hosts/nope/settings", map[string]any{"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 8}, 404)
	if got := e.call(admin, "PUT", settings, map[string]any{"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 64}, 422); got["detail"] != "reserve_gb leaves no memory on mac" {
		t.Fatal(got)
	}
	e.call(admin, "PUT", settings, map[string]any{"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 8.25}, 422)
	e.call(admin, "PUT", settings, map[string]any{"after_run": "never", "idle_unload_min": 0, "reserve_gb": 8}, 422)
	got := e.call(admin, "PUT", settings, map[string]any{"after_run": "keep_pinned", "idle_unload_min": 30, "reserve_gb": 12.5}, 200)
	if got["version"] != 1.0 || got["applied"] != false || len(got["pins"].([]any)) != 0 {
		t.Fatal(got)
	}
	var kv map[string]any
	_ = json.Unmarshal(published["mac"], &kv)
	if kv["schema_version"] != 1.0 || kv["version"] != 1.0 || kv["after_run"] != "keep_pinned" || kv["reserve_gb"] != 12.5 {
		t.Fatal(kv)
	}
	// published under the host's lock before the commit: a KV failure stores nothing (the KV never lags the table)
	e.service.SetModelOps(nil, func(context.Context, string, []byte) error { return context.DeadlineExceeded })
	e.call(admin, "PUT", settings, map[string]any{"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 8}, 503)
	if n := e.count(`SELECT count(*) FROM host_settings WHERE host = 'mac' AND version = 1 AND after_run = 'keep_pinned'`); n != 1 {
		t.Fatal("a change that did not reach the KV was stored", n)
	}
	e.service.SetModelOps(nil, func(_ context.Context, host string, value []byte) error {
		mu.Lock()
		defer mu.Unlock()
		published[host] = value
		return nil
	})

	pins := "/v1/studio/admin/hosts/mac/pins"
	pin := map[string]any{"env": "torch", "component": "text_encoder", "model": "qwen-image-2.1", "variant": "base", "precision": "int8"}
	if got := e.call(admin, "PUT", pins, map[string]any{"pins": []any{map[string]any{"env": "cuda", "component": "vae", "model": "qwen-image-2.1", "variant": "base", "precision": "bf16"}}}, 422); got["detail"] != "env cuda is not on host mac" {
		t.Fatal(got)
	}
	if got := e.call(admin, "PUT", pins, map[string]any{"pins": []any{map[string]any{"env": "mlx", "component": "vae", "model": "qwen-image-2.1", "variant": "base", "precision": "bf16"}}}, 422); got["detail"] != "qwen-image-2.1 is not published by mac.mlx" {
		t.Fatal(got)
	}
	got = e.call(admin, "PUT", pins, map[string]any{"pins": []any{pin, pin}}, 200)
	dump(t, "HostSettings", got)
	if got["version"] != 2.0 || len(got["pins"].([]any)) != 1 || got["after_run"] != "keep_pinned" {
		t.Fatal(got)
	}

	// the runners apply it: applied, and the admin's view of GET /hosts
	other := utility.NewID() // another user's own LoRA baked in: admins see its id and strength, never its name
	policy := map[string]any{"policy": map[string]any{"after_run": "keep_pinned", "idle_unload_min": 30, "reserve_gb": 12.5, "settings_version": 2}}
	resident := []map[string]any{{"component": "text_encoder", "model": "qwen-image-2.1", "variant": "base", "precision": "int8",
		"gb": 1.5, "pinned": true, "pinned_why": "pin"},
		{"component": "transformer", "model": "qwen-image-2.1", "variant": "turbo", "precision": "int8", "gb": 0.25, "pinned": false,
			"loras": []map[string]any{{"lora_id": other, "name": "Secret", "strength": 0.8, "bytes": 10}}}}
	e.modelHost("torch", time.Now(), resident, policy)
	e.modelHost("mlx", time.Now(), nil, policy)
	find := func(user, env string) map[string]any {
		for _, h := range e.call(user, "GET", "/v1/studio/hosts", nil, 200)["hosts"].([]any) {
			if h.(map[string]any)["host"] == "mac" && h.(map[string]any)["env"] == env {
				return h.(map[string]any)
			}
		}
		t.Fatal("no mac." + env)
		return nil
	}
	dump(t, "HostList", e.call(admin, "GET", "/v1/studio/hosts", nil, 200))
	dump(t, "HostList", e.call(userA, "GET", "/v1/studio/hosts", nil, 200))
	dump(t, "LoraList", e.call(userA, "GET", "/v1/studio/loras", nil, 200))
	if usage := e.call(userA, "GET", "/v1/studio/loras", nil, 200)["usage"].(map[string]any); usage["max_count"] != 50.0 || usage["count"] != 0.0 {
		t.Fatal(usage)
	}
	torch := find(admin, "torch")
	adminView := torch["admin"].(map[string]any)
	if adminView["settings"].(map[string]any)["applied"] != true || adminView["hub_token"] != true ||
		adminView["policy"].(map[string]any)["settings_version"] != 2.0 || adminView["run"] != nil || adminView["unload_pending"] != false {
		t.Fatal(adminView)
	}
	baked := torch["resident"].([]any)[1].(map[string]any)["loras"].([]any)[0].(map[string]any)
	if baked["lora_id"] != other || baked["strength"] != 0.8 || baked["name"] != nil {
		t.Fatal(baked)
	}
	if torch["cached_gb"] != 0.3 || torch["machine"].(map[string]any)["ram_gb"] != 64.0 ||
		torch["resident"].([]any)[0].(map[string]any)["pinned_why"] != "pin" {
		t.Fatal(torch)
	}
	user := find(userA, "torch")
	if _, ok := user["admin"]; ok {
		t.Fatal("admin view shown to a user")
	}
	if baked := user["resident"].([]any)[1].(map[string]any)["loras"].([]any)[0].(map[string]any); baked["lora_id"] != nil || baked["name"] != nil || baked["strength"] != nil {
		t.Fatal(baked)
	}

	// unloads: by admin, one request per env, while unanswered the host releases nothing (until 60 s after idle)
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	var asked []string
	answer := `{"error": "a run is in progress", "code": "busy"}`
	d.Unload = func(_ context.Context, subject string, body []byte) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, subject+" "+string(body))
		return []byte(answer), nil
	}
	e.service.SetDispatcher(d)
	e.modelHost("torch", time.Now(), nil, map[string]any{"memory": nil}) // no admission: only the unload holds the host
	e.modelHost("mlx", time.Now(), nil, map[string]any{"memory": nil})
	unload := "/v1/studio/admin/hosts/mac/unload"
	e.call(admin, "POST", "/v1/studio/admin/hosts/nope/unload", map[string]any{"all": true}, 404)
	e.call(admin, "POST", unload, map[string]any{"all": true, "cached_env": "mlx"}, 422)
	if got := e.call(admin, "POST", unload, map[string]any{"cached_env": "cuda"}, 422); got["detail"] != "env cuda is not on host mac" {
		t.Fatal(got)
	}
	accepted := e.call(admin, "POST", unload, map[string]any{"all": true}, 202)
	dump(t, "HostUnloadAccepted", accepted)
	if requests, _ := json.Marshal(accepted["requests"]); string(requests) != `[{"env":"mlx","state":"asked"},{"env":"torch","state":"asked"}]` {
		t.Fatal(string(requests))
	}
	d.Settle()
	slices.Sort(asked)
	if len(asked) != 2 || asked[0] != `runner.unload.mac.mlx {"for_env":"mlx","by":"admin"}` {
		t.Fatal(asked)
	}
	if find(admin, "torch")["admin"].(map[string]any)["unload_pending"] != true {
		t.Fatal("unload not pending")
	}
	runID, _ := e.start(userA, "qwen-image", turbo)
	start := time.Now()
	if err := d.Pass(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	d.Settle()
	if e.count(`SELECT count(*) FROM runs WHERE id = $1 AND released_at IS NOT NULL`, runID) != 0 {
		t.Fatal("released to a host an admin unload holds")
	}
	mu.Lock()
	answer = `{"unloaded_gb": 1.75, "freed": [], "resident": []}`
	mu.Unlock()
	if err := d.Pass(context.Background(), start.Add(studio.AskEvery)); err != nil { // asked again: ok, held no more
		t.Fatal(err)
	}
	d.Settle()
	if err := d.Pass(context.Background(), start.Add(studio.AskEvery+time.Second)); err != nil {
		t.Fatal(err)
	}
	d.Settle()
	if e.count(`SELECT count(*) FROM runs WHERE id = $1 AND released_at IS NOT NULL`, runID) != 1 {
		t.Fatal("not released after the unload was answered")
	}
	asked = nil
	if got := e.call(admin, "POST", unload, map[string]any{"entries": []any{pin}}, 202); got["requests"].([]any)[0].(map[string]any)["state"] != "after_run" {
		t.Fatal(got) // torch runs the run now: asked after it
	}
	e.call(admin, "POST", unload, map[string]any{"cached_env": "mlx"}, 202)
	d.Settle()
	if len(asked) != 1 || asked[0] != `runner.unload.mac.mlx {"for_env":"mlx","by":"admin","cached_only":true}` {
		t.Fatal(asked)
	}
}

// withAddon is qwenModels with an optional add-on (API 1.36) on Qwen Image, in the given presence.
func withAddon(turbo, presence string) []byte {
	var value map[string]any
	_ = json.Unmarshal(qwenModels(turbo), &value)
	addon := map[string]any{"component": "enhancer", "title": "Enhancer", "dir": "Qwen-Image-2.1-PE", "presence": presence,
		"source": map[string]any{"repo": "Qwen/Qwen-Image-2.1-PE", "revision": "main", "allow_patterns": []string{"*"},
			"licence": "apache-2.0", "must_accept": false, "gated": false, "bytes": 40}, "variants": []string{"base", "turbo"}}
	if presence != "missing" {
		addon["bytes"] = 40
	}
	value["models"].([]any)[0].(map[string]any)["addons"] = []any{addon}
	raw, _ := json.Marshal(value)
	return raw
}

// Optional add-ons (API 1.36): GET /models lists them per host; a download or delete job of one names it (variant
// null), is idempotent, carries it in its command, and is refused with the contract's texts.
func TestModelAddonJobs(t *testing.T) {
	e := setup(t)
	e.modelsSetup("missing")
	if err := e.registry.SetRegistry("mac.torch.models", withAddon("missing", "missing")); err != nil {
		t.Fatal(err)
	}
	addonOn := func() map[string]any {
		list := e.call(userA, "GET", "/v1/studio/models", nil, 200)
		dump(t, "ModelList", list)
		for _, m := range list["models"].([]any) {
			if m.(map[string]any)["id"] == "qwen-image-2.1" {
				a := m.(map[string]any)["addons"].([]any)[0].(map[string]any)
				if a["title"] != "Enhancer" || a["licence"].(map[string]any)["id"] != "apache-2.0" || a["download_bytes"] != 40.0 ||
					fmt.Sprint(a["variants"]) != "[base turbo]" { // API 1.40: as the hosts' entries scope it
					t.Fatal(a)
				}
				return a["hosts"].([]any)[0].(map[string]any)
			}
		}
		t.Fatal("no qwen")
		return nil
	}
	if got := addonOn(); got["host"] != "mac" || got["state"] != "missing" {
		t.Fatal(got)
	}
	download := "/v1/studio/admin/models/qwen-image-2.1/download"
	for body, want := range map[string]string{
		`{"host": "mac", "addon": "enhancer", "variant": "base"}`: "give a variant or an add-on, not both",
		`{"host": "mac", "addon": "nope"}`:                        "qwen-image-2.1 has no add-on nope",
		`{"host": "mac", "addon": "Bad"}`:                         "addon must be a component name",
	} {
		var b map[string]any
		_ = json.Unmarshal([]byte(body), &b)
		if got := e.call(admin, "POST", download, b, 422); got["detail"] != want {
			t.Fatal(body, got)
		}
	}
	job := e.call(admin, "POST", download, map[string]any{"host": "mac", "addon": "enhancer"}, 202)
	dump(t, "ModelJob", job)
	if job["addon"] != "enhancer" || job["variant"] != nil || job["title"] != "Qwen Image 2.1 · Enhancer" || job["licence"] != nil {
		t.Fatal(job)
	}
	if again := e.call(admin, "POST", download, map[string]any{"host": "mac", "addon": "enhancer"}, 200); again["id"] != job["id"] {
		t.Fatal(again)
	}
	if got := addonOn(); got["state"] != "downloading" || got["job"].(map[string]any)["id"] != job["id"] {
		t.Fatal(got)
	}
	if got := e.variantOn(userA, "qwen-image-2.1", "base", "mac"); got["state"] != "present" {
		t.Fatal("an add-on's download is no variant's", got)
	}
	_, _, data := e.release()
	if len(data) != 1 || data[0]["kind"] != "download" || data[0]["addon"] != "enhancer" || data[0]["variant"] != nil {
		t.Fatal(data)
	}
	if got := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&addon=enhancer", nil, 409); got["detail"] != "Qwen Image 2.1 Enhancer is not on mac" {
		t.Fatal(got)
	}
	e.modelEvent(job["id"].(string), "status", 1, 1, map[string]any{"status": "succeeded", "result": map[string]any{"bytes": 40, "files": 1}})
	if err := e.registry.SetRegistry("mac.torch.models", withAddon("missing", "present")); err != nil {
		t.Fatal(err)
	}
	if got := e.call(admin, "POST", download, map[string]any{"host": "mac", "addon": "enhancer"}, 409); got["detail"] != "Qwen Image 2.1 Enhancer is already on mac" {
		t.Fatal(got)
	}
	e.modelHost("torch", time.Now(), []map[string]any{{"component": "enhancer", "model": "qwen-image-2.1", "variant": "base",
		"precision": "int8", "gb": 9, "pinned": false}}, nil)
	if got := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&addon=enhancer", nil, 409); got["detail"] != "enhancer of Qwen Image 2.1 is loaded on mac; unload it first" {
		t.Fatal(got)
	}
	if got := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&addon=enhancer&variant=base", nil, 422); got["detail"] != "give a variant or an add-on, not both" {
		t.Fatal(got)
	}
	e.modelHost("torch", time.Now(), nil, nil)
	del := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&addon=enhancer", nil, 202)
	if del["kind"] != "delete" || del["addon"] != "enhancer" || del["variant"] != nil || del["title"] != "Qwen Image 2.1 · Enhancer" {
		t.Fatal(del)
	}
	if again := e.call(admin, "DELETE", "/v1/studio/admin/models/qwen-image-2.1?host=mac&addon=enhancer", nil, 200); again["id"] != del["id"] {
		t.Fatal(again)
	}
	if _, _, data := e.release(); len(data) != 1 || data[0]["kind"] != "delete" || data[0]["addon"] != "enhancer" {
		t.Fatal(data)
	}
}
