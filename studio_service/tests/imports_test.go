// Integration test of helm import's API (contracts/README.md "Import", API 1.16): imported takes land only in the
// named user's gallery, idempotent on their key, with lineage by key in either order, a legacy snapshot, videos with
// posters, and checks that free the key on failure.
package tests

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"
)

// pngBytes is a w × h PNG.
func pngBytes(w, h int) []byte {
	var out bytes.Buffer
	_ = png.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)))
	return out.Bytes()
}

// importBody is an image import of key for userA with parents.
func importBody(key string, file []byte, w, h int, parents ...string) map[string]any {
	if parents == nil {
		parents = []string{}
	}
	return map[string]any{"owner_id": userA, "key": key, "kind": "image", "filename": key + ".png", "content_type": "image/png",
		"bytes": len(file), "width": w, "height": h, "created_at": "2025-03-01T10:00:00Z", "starred": true, "title": "Old " + key,
		"legacy": map[string]any{"studio": "image", "old_id": key, "params": map[string]any{"steps": 8}}, "parents": parents}
}

// put stores file at the object key a signed PUT link names.
func (e *env) put(link string, file []byte) {
	e.t.Helper()
	key := link[strings.Index(link, "/files/")+len("/files/") : strings.Index(link, "?")]
	if err := e.store.Put(context.Background(), key, bytes.NewReader(file), int64(len(file)), http.DetectContentType(file)); err != nil {
		e.t.Fatal(err)
	}
}

func TestImport(t *testing.T) {
	e := setup(t)
	imports := "/v1/studio/admin/imports/takes"
	file := pngBytes(4, 3)
	e.call(admin, "POST", imports, map[string]any{"owner_id": userA}, 422)
	huge := importBody("helmstudio:9", file, 4, 3)
	huge["legacy"] = map[string]any{"text": strings.Repeat("x", 70000)}
	if got := e.call(admin, "POST", imports, huge, 422); got["detail"] != "legacy is larger than 64 KB" {
		t.Fatal(got)
	}

	// the child first: it shows its parent once both are complete
	child := e.call(admin, "POST", imports, importBody("helmstudio:2", file, 4, 3, "helmstudio:1"), 201)
	childID := child["take_id"].(string)
	dump(t, "ImportTake", child)
	if child["state"] != "pending" || !strings.Contains(child["upload_url"].(string), "/files/u/"+userA+"/imports/"+childID+".png?") ||
		child["poster_upload_url"] != nil {
		t.Fatal(child)
	}
	if again := e.call(admin, "POST", imports, importBody("helmstudio:2", file, 4, 3, "helmstudio:1"), 200); again["take_id"] != childID || again["state"] != "pending" {
		t.Fatal(again)
	}
	if got := e.call(admin, "POST", imports+"/"+childID+"/complete", nil, 409); got["detail"] != "upload not received" {
		t.Fatal(got)
	}
	if list := e.call(userA, "GET", "/v1/studio/takes", nil, 200)["takes"].([]any); len(list) != 0 {
		t.Fatal("a pending import is shown", list)
	}
	e.put(child["upload_url"].(string), file)
	took := e.call(admin, "POST", imports+"/"+childID+"/complete", nil, 200)
	dump(t, "Take", took)
	created, _ := time.Parse(time.RFC3339, took["created_at"].(string))
	if took["source"] != "import" || took["starred"] != true || took["made_with"] != nil || took["width"] != 4.0 ||
		!created.Equal(time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)) || len(took["parents"].([]any)) != 0 ||
		took["legacy"].(map[string]any)["settings"].(map[string]any)["old_id"] != "helmstudio:2" {
		t.Fatal(took)
	}
	e.call(userB, "GET", "/v1/studio/takes/"+childID, nil, 404)
	e.call(admin, "GET", "/v1/studio/takes/"+childID, nil, 404)
	if got := e.call(userA, "POST", "/v1/studio/takes/"+childID+"/reuse", nil, 409); got["detail"] == "" {
		t.Fatal(got)
	}

	parent := e.call(admin, "POST", imports, importBody("helmstudio:1", file, 4, 3), 201)
	e.put(parent["upload_url"].(string), file)
	e.call(admin, "POST", imports+"/"+parent["take_id"].(string)+"/complete", nil, 200)
	if got := e.call(userA, "GET", "/v1/studio/takes/"+childID, nil, 200); len(got["parents"].([]any)) != 1 || got["parents"].([]any)[0] != parent["take_id"] {
		t.Fatal(got)
	}
	ready := e.call(admin, "POST", imports, importBody("helmstudio:2", file, 4, 3, "helmstudio:1"), 200)
	dump(t, "ImportTake", ready)
	if ready["state"] != "ready" || ready["upload_url"] != nil || ready["expires_at"] != nil {
		t.Fatal(ready)
	}
	if list := e.call(userA, "GET", "/v1/studio/takes", nil, 200)["takes"].([]any); len(list) != 2 {
		t.Fatal(list)
	}

	// a video with its poster; a file that is not what it says is refused and its key freed
	poster := pngBytes(8, 6)
	video := importBody("helmstudio:3", nil, 8, 6)
	mp4 := append([]byte{0, 0, 0, 24}, []byte("ftypisom0000mp41")...)
	video["kind"], video["content_type"], video["bytes"], video["filename"] = "video", "video/mp4", len(mp4), "old.mp4"
	video["video"] = map[string]any{"duration_s": 2, "fps": 24, "frames": 48, "has_audio": false,
		"poster": map[string]any{"content_type": "image/png", "bytes": len(poster), "width": 8, "height": 6}}
	clip := e.call(admin, "POST", imports, video, 201)
	e.put(clip["upload_url"].(string), mp4)
	e.put(clip["poster_upload_url"].(string), poster)
	got := e.call(admin, "POST", imports+"/"+clip["take_id"].(string)+"/complete", nil, 200)
	dump(t, "Take", got)
	if got["kind"] != "video" || got["video"].(map[string]any)["frames"] != 48.0 || !strings.Contains(got["thumb_url"].(string), ".poster.png") {
		t.Fatal(got)
	}
	wrong := e.call(admin, "POST", imports, importBody("helmstudio:4", file, 5, 3), 201)
	e.put(wrong["upload_url"].(string), file)
	if got := e.call(admin, "POST", imports+"/"+wrong["take_id"].(string)+"/complete", nil, 422); got["detail"] != "width and height differ from the file" {
		t.Fatal(got)
	}
	if again := e.call(admin, "POST", imports, importBody("helmstudio:4", file, 4, 3), 201); again["take_id"] == wrong["take_id"] {
		t.Fatal(again)
	}
}
