package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	plain := ask("plain").Pass // signed (the call needs one), but opens no takes
	for _, bad := range []string{plain, got.Pass + "x", "", "a.b"} {
		if _, err := e.service.PluginCaller(ctx, bad); !studio.IsPassError(err) {
			t.Fatal("verified", bad, err)
		}
	}
	e.call(admin, "PUT", "/v1/studio/admin/plugins/cutter", map[string]any{"enabled": false}, 200)
	if _, err := e.service.PluginCaller(ctx, got.Pass); !studio.IsPassError(err) {
		t.Fatal("a pass of a plugin turned off since:", err)
	}
}
