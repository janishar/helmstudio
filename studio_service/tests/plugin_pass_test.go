package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
)

// API 1.70 (contracts/README.md "Plugins: takes"): studio signs a call's pass only for a plugin that is on, and the
// pass opens the plugin API only while that plugin is on and says `takes`; a forged or other plugin's pass is refused.
func TestPluginPass(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	dir := t.TempDir()
	for name, takes := range map[string]string{"cutter": `,"takes":true`, "plain": ""} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name, "plugin.json"), []byte(`{"name":"`+name+`","title":"T","version":"0.1.0",
			"kind":"assistant-tools","description":"d","author":"a","may_do":["m"],"url":"http://127.0.0.1:1","health":"/h"`+takes+`,
			"tools":[{"name":"t","description":"d","input":{"type":"object"},"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.service.SetPluginDirs([]string{dir})
	e.service.SetUserPluginDir("")
	if _, err := e.service.ScanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	ask := func(plugin string) studio.PassReply {
		return e.service.PluginPass(ctx, &studio.PassRequest{OwnerID: userA, Plugin: plugin, CallID: "t:1:0"})
	}
	if got := ask("cutter"); got.Code != "not_found" {
		t.Fatal("a pass for a plugin that is off:", got)
	}
	if got := e.service.PluginPass(ctx, &studio.PassRequest{OwnerID: "x", Plugin: "cutter", CallID: "c"}); got.Code != "invalid" {
		t.Fatal(got)
	}
	for _, name := range []string{"cutter", "plain"} {
		e.call(admin, "PUT", "/v1/studio/admin/plugins/"+name, map[string]any{"enabled": true}, 200)
	}
	got := ask("cutter")
	if got.Pass == "" || got.API != "http://kong" {
		t.Fatal(got)
	}
	caller, err := e.service.PluginCaller(ctx, got.Pass)
	if err != nil || caller.ID != userA || caller.Plugin != "cutter" {
		t.Fatal(caller, err)
	}
	if p := ask("plain"); p.Pass != "" || p.API != "http://kong" { // no takes: no pass
		t.Fatal("a pass for a plugin without takes:", p)
	}
	for _, bad := range []string{got.Pass + "x", "", "a.b"} {
		if _, err := e.service.PluginCaller(ctx, bad); !studio.IsPassError(err) {
			t.Fatal("verified", bad, err)
		}
	}
	e.call(admin, "PUT", "/v1/studio/admin/plugins/cutter", map[string]any{"enabled": false}, 200)
	if _, err := e.service.PluginCaller(ctx, got.Pass); !studio.IsPassError(err) {
		t.Fatal("a pass of a plugin turned off since:", err)
	}

	// made: only the takes this plugin made under this call vouch for a line's Undo; the source take, another call's
	// take or another plugin's never do
	mine, other, src := utility.NewID(), utility.NewID(), e.image(userA)
	for id, tool := range map[string]string{mine: `{"plugin":"cutter","tool":"t","input":{},"call":"t:1:0"}`,
		other: `{"plugin":"cutter","tool":"t","input":{},"call":"t:2:0"}`} {
		e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, tool)
			VALUES ($1, $2, 'image', 'tool', 'made', $3, 'image/png', 1, true, $4)`, id, userA, "u/"+userA+"/uploads/"+id+".png", tool)
	}
	made := e.service.PluginPass(ctx, &studio.PassRequest{OwnerID: userA, Plugin: "cutter", CallID: "t:1:0",
		TakeIDs: []string{mine, other, src}})
	if made.TakeIDs == nil || !slices.Equal(*made.TakeIDs, []string{mine}) || made.Pass != "" {
		t.Fatal("made:", made)
	}
	if made = e.service.PluginPass(ctx, &studio.PassRequest{OwnerID: userB, Plugin: "cutter", CallID: "t:1:0",
		TakeIDs: []string{mine}}); made.TakeIDs == nil || len(*made.TakeIDs) != 0 {
		t.Fatal("another owner's made:", made)
	}
}

// API 1.70 (contracts/README.md "Plugins: takes", The Gallery's Edit): Save as new runs a takes plugin's tool for the
// signed-in user with a pass studio signs for them; only the takes it made under that call come back, a risky tool or
// a plugin that is off is 404, the plugin's 422 stays a 422 and any other failure is a plain 502.
func TestRunPluginTool(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	src, foreign := e.image(userA), e.image(userB)
	var made string
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/h" {
			return
		}
		var body struct{ Input map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		caller, err := e.service.PluginCaller(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		switch {
		case err != nil || caller.ID != r.Header.Get("X-Helm-Owner") || caller.Call != r.Header.Get("X-Helm-Call"):
			w.WriteHeader(401)
		case body.Input["fail"] == "bad":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"detail":"input: crop is off the picture"}`))
		case body.Input["fail"] == "slow":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"detail":"it took too long; try a shorter part"}`))
		default:
			made = utility.NewID()
			e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, tool)
				VALUES ($1, $2, 'image', 'tool', 'cropped', $3, 'image/png', 1, true, $4)`, made, caller.ID,
				"u/"+caller.ID+"/uploads/"+made+".png", `{"plugin":"cutter","tool":"t","input":{},"call":"`+caller.Call+`"}`)
			_, _ = w.Write([]byte(`{"result":{},"line":"Cropped · src","take_ids":["` + made + `","` + foreign + `","` + src + `"]}`))
		}
	}))
	defer plugin.Close()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "cutter"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "cutter", "plugin.json"), []byte(`{"name":"cutter","title":"Cutter","version":"0.1.0",
		"kind":"assistant-tools","description":"d","author":"a","may_do":["m"],"url":"`+plugin.URL+`","health":"/h","takes":true,
		"needs":"ffmpeg (brew install ffmpeg)","tools":[
		{"name":"t","title":"Crop","summary":"Crop it","description":"d","input":{"type":"object"},"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}},
		{"name":"gpu","description":"d","input":{"type":"object"},"risk":{"uses_gpu":true,"deletes":false,"sends_off_machine":false}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.service.SetPluginDirs([]string{dir})
	e.service.SetUserPluginDir("")
	if _, err := e.service.ScanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	run := func(tool string, input map[string]any, want int) map[string]any {
		return e.call(userA, "POST", "/v1/studio/plugins/cutter/tools/"+tool, map[string]any{"input": input}, want)
	}
	e.call(userA, "GET", "/v1/studio/plugins/cutter", nil, 404) // off
	run("t", map[string]any{"take_id": src}, 404)
	e.call(admin, "PUT", "/v1/studio/admin/plugins/cutter", map[string]any{"enabled": true}, 200)
	use := e.call(userA, "GET", "/v1/studio/plugins/cutter", nil, 200)
	if use["running"] != true || fmt.Sprint(use["tools"]) != "[t]" || use["title"] != "Cutter" {
		t.Fatal(use)
	}
	list := e.call(admin, "GET", "/v1/studio/admin/plugins", nil, 200)["plugins"].([]any)[0].(map[string]any)
	if list["takes"] != true || list["needs"] != "ffmpeg (brew install ffmpeg)" ||
		list["tools"].([]any)[0].(map[string]any)["title"] != "Crop" {
		t.Fatal(list)
	}
	got := run("t", map[string]any{"take_id": src}, 200)
	if fmt.Sprint(got["take_ids"]) != "["+made+"]" || got["line"] != "Cropped · src" {
		t.Fatal("only the take it made under this call:", got)
	}
	run("gpu", map[string]any{"take_id": src}, 404) // a risk flag needs a question first
	run("nope", map[string]any{"take_id": src}, 404)
	if got := run("t", map[string]any{"fail": "bad"}, 422); got["detail"] != "input: crop is off the picture" {
		t.Fatal(got)
	}
	if got := run("t", map[string]any{"fail": "slow"}, 502); got["detail"] != "it took too long; try a shorter part" {
		t.Fatal(got)
	}
	e.call(userA, "POST", "/v1/studio/plugins/cutter/tools/t", map[string]any{}, 422) // input is required
	plugin.Close()
	if got := run("t", map[string]any{"take_id": src}, 502); got["detail"] != "Cutter did not answer" {
		t.Fatal(got)
	}
	if use = e.call(userA, "GET", "/v1/studio/plugins/cutter", nil, 200); use["running"] != false {
		t.Fatal(use)
	}
}
