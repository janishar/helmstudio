// Model folders (API 1.78, contracts/README.md "Model folders"): PUT …/folders checked by the host's runner and
// written to the KV only when set; the Models screen's folders, folder and copies; a download into a folder; a delete
// by folder, the two-copies 409 and the unplugged 409.
package tests

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
)

var (
	macID = studio.FolderID("~/GenAI/models")
	t9ID  = studio.FolderID("/Volumes/T9/models")
)

// macDisks is mac's models_disks: Mac (the downloads folder), then T9.
func macDisks(t9Connected bool) map[string]any {
	t9 := map[string]any{"id": t9ID, "name": "T9", "root": "/Volumes/T9/models", "connected": t9Connected,
		"writable": t9Connected, "downloads": false}
	if t9Connected {
		t9["free_bytes"], t9["total_bytes"] = 10, 20
	}
	return map[string]any{"models_disks": []any{map[string]any{"id": macID, "name": "Mac", "root": "~/GenAI/models",
		"connected": true, "free_bytes": 1000, "total_bytes": 5000, "writable": true, "downloads": true}, t9}}
}

// foldersSetup is modelsSetup with mac.torch reporting two folders (Mac, then T9) and base in both, loaded from Mac.
func (e *env) foldersSetup(t9Connected bool) {
	e.t.Helper()
	e.modelsSetup("missing")
	var value map[string]any
	_ = json.Unmarshal(qwenModels("missing"), &value)
	base := value["models"].([]any)[0].(map[string]any)["variants"].([]any)[0].(map[string]any)
	base["folder"] = macID
	t9 := map[string]any{"folder": t9ID, "state": "present", "bytes": 300, "freed_bytes": 300}
	if !t9Connected {
		t9 = map[string]any{"folder": t9ID, "state": "unplugged"}
	}
	base["copies"] = []any{map[string]any{"folder": macID, "state": "present", "bytes": 300, "freed_bytes": 250}, t9}
	raw, _ := json.Marshal(value)
	if err := e.registry.SetRegistry("mac.torch.models", raw); err != nil {
		e.t.Fatal(err)
	}
	e.modelHost("torch", time.Now(), nil, macDisks(t9Connected))
}

func TestHostFolders(t *testing.T) {
	e := setup(t)
	e.modelsSetup("present")
	var kv map[string]any
	e.service.SetModelOps(nil, func(_ context.Context, _ string, value []byte) error {
		kv = nil
		return json.Unmarshal(value, &kv)
	})
	folders := "/v1/studio/admin/hosts/mac/folders"
	body := func(downloads int, paths ...string) map[string]any {
		list := []any{}
		for _, p := range paths {
			list = append(list, map[string]any{"path": p})
		}
		return map[string]any{"folders": list, "downloads": downloads}
	}
	// no runner answers: offline
	e.call(admin, "PUT", folders, body(0, "~/GenAI/models"), 503)
	asked := []string{}
	e.service.SetFoldersCheck(func(_ context.Context, host string, req message.FoldersCheckRequest) (message.FoldersCheckReply, error) {
		var out message.FoldersCheckReply
		if host != "mac" {
			return out, errors.New("no answer")
		}
		asked = req.Paths
		for _, p := range req.Paths {
			f := struct {
				Path      string `json:"path"`
				Connected bool   `json:"connected"`
				Writable  bool   `json:"writable"`
				Drive     bool   `json:"drive"`
			}{Path: p, Connected: p == "~/GenAI/models" || p == "/ro", Writable: p == "~/GenAI/models", Drive: p == "/Volumes/T9/models"}
			out.Folders = append(out.Folders, f)
		}
		return out, nil
	})
	e.call(admin, "PUT", "/v1/studio/admin/hosts/nope/folders", body(0, "~/GenAI/models"), 404)
	e.call(userA, "PUT", folders, body(0, "~/GenAI/models"), 403)
	for _, c := range []struct {
		body   map[string]any
		detail string
	}{
		{body(0, "models"), "paths must be absolute or start with ~/"},
		{body(0, "~/GenAI/models", "~/GenAI/models/"), "~/GenAI/models/ is listed twice"},
		{body(2, "~/GenAI/models"), "downloads must be an index of folders"},
		{body(0, "/nowhere"), "/nowhere is not a folder on mac"},
		{body(1, "~/GenAI/models", "/Volumes/T9/models"), "downloads must be a connected folder"},
		{body(1, "~/GenAI/models", "/ro"), "/ro is not writable on mac"},
	} {
		if got := e.call(admin, "PUT", folders, c.body, 422); got["detail"] != c.detail {
			t.Fatal(c.body, got)
		}
	}
	// a settings PUT before any list: no folders in the KV (an older runner keeps HELMSTUDIO_MODELS)
	got := e.call(admin, "PUT", "/v1/studio/admin/hosts/mac/settings", map[string]any{"after_run": "keep_warm", "idle_unload_min": 0, "reserve_gb": 8}, 200)
	if _, has := kv["folders"]; has || got["folders"] != nil || got["downloads"] != nil {
		t.Fatal(got, kv)
	}
	got = e.call(admin, "PUT", folders, body(0, "~/GenAI/models", "/Volumes/T9/models"), 200)
	list := got["folders"].([]any)
	if got["version"] != 2.0 || len(list) != 2 || list[1].(map[string]any)["id"] != t9ID || got["downloads"] != macID ||
		got["after_run"] != "keep_warm" || !slices.Equal(asked, []string{"~/GenAI/models", "/Volumes/T9/models"}) {
		t.Fatal(got, asked)
	}
	if !slices.Equal(kv["folders"].([]any), []any{"~/GenAI/models", "/Volumes/T9/models"}) || kv["downloads"] != 0.0 {
		t.Fatal(kv)
	}
	// the settings PUT keeps the list
	e.call(admin, "PUT", "/v1/studio/admin/hosts/mac/settings", map[string]any{"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 8}, 200)
	if len(kv["folders"].([]any)) != 2 {
		t.Fatal(kv)
	}
}

func TestModelFolderJobs(t *testing.T) {
	e := setup(t)
	e.foldersSetup(true)
	e.service.SetModelOps(func(context.Context, string) error { return nil }, nil)

	list := e.call(admin, "GET", "/v1/studio/models", nil, 200)
	dump(t, "ModelList", list)
	mac := list["hosts"].([]any)[1].(map[string]any)
	if f := mac["folders"].([]any); len(f) != 2 || f[1].(map[string]any)["name"] != "T9" || f[1].(map[string]any)["free_bytes"] != 10.0 {
		t.Fatal(mac)
	}
	if fake := list["hosts"].([]any)[0].(map[string]any); len(fake["folders"].([]any)) != 0 {
		t.Fatal("an older runner's host has folders", fake)
	}
	base := e.variantOn(userA, "qwen-image-2.1", "base", "mac")
	if base["folder"] != macID || len(base["copies"].([]any)) != 2 || base["copies"].([]any)[0].(map[string]any)["freed_bytes"] != 250.0 {
		t.Fatal(base)
	}

	del := "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=base"
	if got := e.call(admin, "DELETE", del, nil, 409); got["detail"] != "Qwen Image 2.1 Base is in 2 folders on mac; pick one" {
		t.Fatal(got)
	}
	e.call(admin, "DELETE", del+"&folder=T9", nil, 422)
	if got := e.call(admin, "DELETE", del+"&folder=000000000000", nil, 422); got["detail"] != "000000000000 is not a folder of mac" {
		t.Fatal(got)
	}
	// base's vae loaded (from Mac): deleting only T9's copy is not refused for it; Mac's is
	loaded := []map[string]any{{"component": "vae", "model": "qwen-image-2.1", "variant": "base", "precision": "int8", "gb": 1, "pinned": false}}
	e.modelHost("torch", time.Now(), loaded, macDisks(true))
	if got := e.call(admin, "DELETE", del+"&folder="+macID, nil, 409); got["detail"] != "vae of Qwen Image 2.1 is loaded on mac; unload it first" {
		t.Fatal(got)
	}
	job := e.call(admin, "DELETE", del+"&folder="+t9ID, nil, 202)
	if _, _, data := e.release(); len(data) != 1 || !slices.Equal(data[0]["folders"].([]any), []any{t9ID}) {
		t.Fatal(data)
	}
	e.modelEvent(job["id"].(string), "status", 1, 1, map[string]any{"status": "succeeded", "result": map[string]any{"files": 3, "freed_bytes": 300}})
	e.modelHost("torch", time.Now(), nil, macDisks(true))
	var value map[string]any // the rescan after it: base on Mac only
	_ = json.Unmarshal(qwenModels("missing"), &value)
	base = value["models"].([]any)[0].(map[string]any)["variants"].([]any)[0].(map[string]any)
	base["folder"], base["copies"] = macID, []any{map[string]any{"folder": macID, "state": "present", "bytes": 300, "freed_bytes": 300}}
	raw, _ := json.Marshal(value)
	if err := e.registry.SetRegistry("mac.torch.models", raw); err != nil {
		t.Fatal(err)
	}

	// a download into T9 of a variant only on Mac: a second copy; into Mac: already there
	download := "/v1/studio/admin/models/qwen-image-2.1/download"
	if got := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "base", "folder": macID}, 409); got["detail"] != "Qwen Image 2.1 Base is already on mac" {
		t.Fatal(got)
	}
	e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "base", "folder": "T9"}, 422)
	dl := e.call(admin, "POST", download, map[string]any{"host": "mac", "variant": "base", "folder": t9ID}, 202)
	if _, _, data := e.release(); len(data) != 1 || data[0]["folder"] != t9ID {
		t.Fatal(data, dl)
	}
}

// T9 unplugged: a download into it and a delete of its copy are 409, and so is a delete of a copy only there.
func TestUnpluggedFolder(t *testing.T) {
	e := setup(t)
	e.foldersSetup(false)
	if got := e.call(admin, "POST", "/v1/studio/admin/models/qwen-image-2.1/download", map[string]any{"host": "mac", "variant": "turbo",
		"accept_licence": true, "folder": t9ID}, 409); got["detail"] != "T9 is not connected on mac" {
		t.Fatal(got)
	}
	del := "/v1/studio/admin/models/qwen-image-2.1?host=mac&variant=base"
	if got := e.call(admin, "DELETE", del+"&folder="+t9ID, nil, 409); got["detail"] != "T9 is not connected on mac" {
		t.Fatal(got)
	}
	var value map[string]any
	_ = json.Unmarshal(qwenModels("missing"), &value)
	base := value["models"].([]any)[0].(map[string]any)["variants"].([]any)[0].(map[string]any)
	base["presence"], base["copies"] = "unplugged", []any{map[string]any{"folder": t9ID, "state": "unplugged"}}
	delete(base, "bytes")
	raw, _ := json.Marshal(value)
	if err := e.registry.SetRegistry("mac.torch.models", raw); err != nil {
		t.Fatal(err)
	}
	if got := e.variantOn(userA, "qwen-image-2.1", "base", "mac"); got["state"] != "unplugged" || got["folder"] != nil {
		t.Fatal(got)
	}
	if got := e.call(admin, "DELETE", del, nil, 409); got["detail"] != "T9 is not connected on mac" {
		t.Fatal(got)
	}
}
