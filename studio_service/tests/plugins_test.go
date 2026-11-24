// Integration tests of plugins (API 1.61, contracts/README.md "Plugins"): another admin's Only-you provider and its
// key file are 404 to everyone else (list, delete, key save and remove); its owner sees and deletes it.
package tests

import (
	"context"
	"strings"
	"testing"

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
