// Integration tests of plugins (API 1.61, contracts/README.md "Plugins"): another admin's Only-you provider and its
// key file are 404 to everyone else (list, delete, key save and remove); its owner sees and deletes it.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/common"
)

func TestOnlyYouProviderIsNotAnotherAdminsToDelete(t *testing.T) {
	e := setup(t)
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO providers (name, kind, base_url, models, default_model, host,
			memory_gb, key_secret, owner_id, plugin) VALUES ('fake-provider-bbbbbbbb', 'server', 'http://127.0.0.1:1/v1',
			'{m}', 'm', 'fake', 0.5, 'plugin-fake-provider-bbbbbbbb', $1, 'fake-provider')`, admin2)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	names := func(user string) string {
		out := []string{}
		for _, p := range e.call(user, "GET", "/v1/studio/admin/providers", nil, 200)["providers"].([]any) {
			out = append(out, p.(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	if got := names(admin); strings.Contains(got, "fake-provider-bbbbbbbb") {
		t.Fatalf("another admin sees it: %s", got)
	}
	if got := names(admin2); !strings.Contains(got, "fake-provider-bbbbbbbb") {
		t.Fatalf("its owner does not see it: %s", got)
	}
	e.call(admin, "DELETE", "/v1/studio/admin/providers/fake-provider-bbbbbbbb", nil, 404)
	// its key file (plugin-<plugin>-<8 hex of its owner>)
	e.call(admin, "PUT", "/v1/studio/admin/provider-keys/plugin-fake-provider-bbbbbbbb", map[string]any{"key": "k", "provider": "x"}, 404)
	e.call(admin, "DELETE", "/v1/studio/admin/provider-keys/plugin-fake-provider-bbbbbbbb", nil, 404)
	e.call(admin2, "DELETE", "/v1/studio/admin/providers/fake-provider-bbbbbbbb", nil, 204)
}

// API 1.64: a scan that finds your skill broken (a copy in progress) never turns it off: blocked gates it, and once it
// reads well again it is on as before.
func TestBrokenScanKeepsASkillOn(t *testing.T) {
	e := setup(t)
	user := t.TempDir()
	dir := filepath.Join(user, "mine")
	write := func(files map[string]string) {
		_ = os.MkdirAll(dir, 0o755)
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		old := time.Now().Add(-time.Minute) // settled: a scan skips what changed in the last 2 s
		_ = filepath.WalkDir(user, func(p string, _ os.DirEntry, err error) error { return os.Chtimes(p, old, old) })
	}
	write(map[string]string{"SKILL.md": "# Mine", "plugin.json": `{"name":"mine","title":"Mine","version":"0.1.0",
		"kind":"skill","format":1,"description":"d","author":"a","may_do":["m"],"when":"w"}`})
	e.service.SetPluginDirs(nil)
	e.service.SetUserPluginDir(user)
	scan := func() {
		if _, err := e.service.ScanPlugins(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	status := func() string {
		for _, p := range e.call(admin, "GET", "/v1/studio/admin/plugins", nil, 200)["plugins"].([]any) {
			if p := p.(map[string]any); p["name"] == "mine" {
				return p["status"].(string)
			}
		}
		return "gone"
	}
	scan()
	e.call(admin, "PUT", "/v1/studio/admin/plugins/mine", map[string]any{"enabled": true}, 200)
	if got := status(); got != "on" {
		t.Fatal(got)
	}
	if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	write(nil)
	scan()
	if got := status(); got != "broken" {
		t.Fatal(got)
	}
	e.call(userA, "GET", "/v1/studio/skills/mine", nil, 404)
	write(map[string]string{"SKILL.md": "# Mine again"})
	scan()
	if got := status(); got != "on" {
		t.Fatal("still on once it reads well:", got)
	}
}

// API 1.67: turning a plugin that starts itself on writes host_settings <host>.plugins with its name; off empties it;
// the runner's registry plugins key shows as starting, running or Couldn't start.
func TestPluginsToRun(t *testing.T) {
	e := setup(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svc", "plugin.json"), []byte(`{"name":"svc","title":"Svc","version":"0.1.0",
		"kind":"assistant-tools","description":"d","author":"a","may_do":["m"],"url":"http://127.0.0.1:1","health":"/h",
		"run":{"cmd":["./run.sh"],"host":"box"},"tools":[{"name":"t","description":"d","input":{"type":"object"},
		"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	written, retry := map[string][]string{}, map[string]map[string]int{}
	e.service.SetModelOps(nil, func(_ context.Context, key string, value []byte) error {
		var v struct {
			Plugins []string
			Retry   map[string]int
		}
		_ = json.Unmarshal(value, &v)
		written[key], retry[key] = v.Plugins, v.Retry
		return nil
	})
	e.service.SetPluginDirs([]string{dir})
	e.service.SetUserPluginDir("")
	if _, err := e.service.ScanPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := written["box.plugins"]; ok && len(got) != 0 {
		t.Fatalf("off: %v", got)
	}
	e.call(admin, "PUT", "/v1/studio/admin/plugins/svc", map[string]any{"enabled": true}, 200)
	if got := written["box.plugins"]; len(got) != 1 || got[0] != "svc" {
		t.Fatalf("on: %v", written)
	}
	status := func() map[string]any {
		for _, p := range e.call(admin, "GET", "/v1/studio/admin/plugins", nil, 200)["plugins"].([]any) {
			if p := p.(map[string]any); p["name"] == "svc" {
				return p
			}
		}
		return nil
	}
	if p := status(); p["status"] != "not_running" || p["starts"].(map[string]any)["host"] != "box" {
		t.Fatal("no runner of box is up:", p)
	}
	host, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "box", "env": "native", "state": "idle",
		"run_id": nil, "updated_at": time.Now().UTC(), "published_by": "test"})
	if err := e.registry.SetHost("box.native", host); err != nil {
		t.Fatal(err)
	}
	if p := status(); p["status"] != "starting" {
		t.Fatal("a runner of box is up, the plugin not answering yet:", p)
	}
	// Try again: on while on raises the nonce
	e.call(admin, "PUT", "/v1/studio/admin/plugins/svc", map[string]any{"enabled": true}, 200)
	if got := retry["box.plugins"]["svc"]; got != 1 {
		t.Fatalf("retry: %v", retry)
	}
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "plugins": map[string]any{"svc": map[string]any{
		"state": "failed", "detail": "it stopped 3 times: exit 3: boom", "restarts": 3, "since": time.Now().UTC(),
		"log": "~/.helm/logs/svc.log"}}, "published_by": "runner box.native", "published_at": time.Now().UTC()})
	if err := e.registry.SetRegistry("box.native.plugins", value); err != nil {
		t.Fatal(err)
	}
	if p := status(); p["status"] != "failed" || p["problem"] != "it stopped 3 times: exit 3: boom" ||
		p["starts"].(map[string]any)["log"] != "~/.helm/logs/svc.log" {
		t.Fatal(p)
	}
	e.call(admin, "PUT", "/v1/studio/admin/plugins/svc", map[string]any{"enabled": false}, 200)
	if got := written["box.plugins"]; len(got) != 0 {
		t.Fatalf("off again: %v", got)
	}
}

// API 1.67: for a plugin that starts itself the built-in wins; your copy is never used and the row says so.
func TestBuiltInWinsForAPluginThatStartsItself(t *testing.T) {
	e := setup(t)
	builtIn, user := t.TempDir(), t.TempDir()
	manifest := `{"name":"svc","title":"Svc","version":"%s","kind":"assistant-tools","description":"d","author":"a",
		"may_do":["m"],"url":"http://127.0.0.1:1","health":"/h","run":{"cmd":["%s"],"host":"box"},
		"tools":[{"name":"t","description":"d","input":{"type":"object"},"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}}]}`
	for dir, args := range map[string][2]string{builtIn: {"0.1.0", "./run.sh"}, user: {"9.0.0", "./evil.sh"}} {
		_ = os.MkdirAll(filepath.Join(dir, "svc"), 0o755)
		body := strings.Replace(strings.Replace(manifest, "%s", args[0], 1), "%s", args[1], 1)
		if err := os.WriteFile(filepath.Join(dir, "svc", "plugin.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Minute)
	_ = filepath.WalkDir(user, func(p string, _ os.DirEntry, err error) error { return os.Chtimes(p, old, old) })
	e.service.SetPluginDirs([]string{builtIn})
	e.service.SetUserPluginDir(user)
	if _, err := e.service.ScanPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range e.call(admin, "GET", "/v1/studio/admin/plugins", nil, 200)["plugins"].([]any) {
		if p := p.(map[string]any); p["name"] == "svc" {
			if p["origin"] != "built_in" || p["version"] != "0.1.0" || !strings.Contains(fmt.Sprint(p["problem"]),
				"Can't replace a built-in that starts itself") {
				t.Fatal(p)
			}
			return
		}
	}
	t.Fatal("svc not listed")
}
