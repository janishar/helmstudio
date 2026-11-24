// Integration tests of plugins (API 1.61, contracts/README.md "Plugins"): another admin's Only-you provider and its
// key file are 404 to everyone else (list, delete, key save and remove); its owner sees and deletes it.
package tests

import (
	"context"
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
