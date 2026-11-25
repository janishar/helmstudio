package tests

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
