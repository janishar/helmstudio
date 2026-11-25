// Integration test of a plugin's key from its host (API 1.73, contracts/README.md "Plugins: a key from its host"):
// Add as provider without a key asks the runner of its run host; the key is checked and written, never shown.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestAddAsProviderTakesTheKeyFromItsHost(t *testing.T) {
	e := setup(t)
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	var up atomic.Bool
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer health.Close()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"hk": `{"name":"hk","title":"Hk","version":"0.1.0","kind":"model-provider","description":"d","author":"a",
			"may_do":["m"],"url":"` + health.URL + `","health":"/h","run":{"cmd":["./run.sh"],"host":"box"},
			"provider":{"path":"/v1","models":["m1"],"key_hint":"k","key_from":{"file":"hk.env","var":"HK_KEY"}}}`,
		"plain": `{"name":"plain","title":"Plain","version":"0.1.0","kind":"model-provider","description":"d","author":"a",
			"may_do":["m"],"url":"` + health.URL + `","health":"/h","provider":{"path":"/v1","models":["m1"],"key_hint":"k"}}`,
	} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name, "plugin.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.service.SetModelOps(nil, func(context.Context, string, []byte) error { return nil })
	e.service.SetPluginDirs([]string{dir})
	e.service.SetUserPluginDir("")
	if _, err := e.service.ScanPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	const key = "hk-secret-0123456789"
	var written []string
	check := "ok"
	e.service.SetProviderKeyWrite(func(_ context.Context, req message.ProviderKeyWriteRequest) (message.ProviderKeyWriteReply, error) {
		written = append(written, req.Key)
		return message.ProviderKeyWriteReply{Found: true, Writable: true, Saved: check == "ok", Check: check}, nil
	})
	add := "/v1/studio/admin/plugins/hk/provider"
	body := map[string]any{"host": "fake"}
	for _, p := range e.call(admin, "GET", "/v1/studio/admin/plugins", nil, 200)["plugins"].([]any) {
		if p := p.(map[string]any); (p["name"] == "hk") != (p["key_from_host"] == true) {
			t.Fatal("key_from_host:", p)
		}
	}
	e.call(admin, "POST", add, body, 409) // off
	e.call(admin, "PUT", "/v1/studio/admin/plugins/hk", map[string]any{"enabled": true}, 200)
	e.call(admin, "PUT", "/v1/studio/admin/plugins/plain", map[string]any{"enabled": true}, 200)
	if got := e.call(admin, "POST", "/v1/studio/admin/plugins/plain/provider", body, 422); got["detail"] != "key must be 1-1024 characters" {
		t.Fatal("a plugin without key_from still needs a key:", got)
	}
	if got := e.call(admin, "POST", add, body, 409); got["detail"] != "No runner is up on box: start helmstudio there" {
		t.Fatal(got)
	}
	host, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "box", "env": "native", "state": "idle",
		"run_id": nil, "updated_at": time.Now().UTC(), "published_by": "test"})
	if err := e.registry.SetHost("box.native", host); err != nil {
		t.Fatal(err)
	}
	if got := e.call(admin, "POST", add, body, 409); got["detail"] != "Hk is not running: turn it on first" {
		t.Fatal(got)
	}
	up.Store(true)
	e.call(admin, "POST", add, body, 503) // no way to ask the runner
	var asked []string
	reply, failed := message.PluginKeyReply{Error: "its key is not made yet", Code: "no_key"}, error(nil)
	e.service.SetPluginKey(func(_ context.Context, h string, req message.PluginKeyRequest) (message.PluginKeyReply, error) {
		asked = append(asked, h+" "+req.Plugin)
		return reply, failed
	})
	if got := e.call(admin, "POST", add, body, 409); got["detail"] != "Hk makes its key on first start: turn it on, then Try again" {
		t.Fatal(got)
	}
	failed = errors.New("timeout")
	if got := e.call(admin, "POST", add, body, 503); got["detail"] != "the runner on box did not answer; try again" {
		t.Fatal(got)
	}
	failed, reply = nil, message.PluginKeyReply{Key: "bad key"}
	e.call(admin, "POST", add, body, 409)
	reply = message.PluginKeyReply{Key: key}
	check = "refused"
	if got := e.call(admin, "POST", add, body, 409); got["detail"] != "Hk refused its own key: turn it off and on, then Try again" {
		t.Fatal(got)
	}
	check = "ok"
	made := e.call(admin, "POST", add, body, 201)
	if made["plugin"] != "hk" || made["key_secret"] != "plugin-"+made["name"].(string) {
		t.Fatal(made)
	}
	if len(written) != 2 || written[0] != key || written[1] != key || asked[0] != "box hk" {
		t.Fatal("the host's key is checked and written:", len(written), asked)
	}
	if strings.Contains(fmt.Sprint(made), key) || strings.Contains(logged.String(), key) {
		t.Fatal("the key is in a response or the log")
	}
	written = nil
	e.call(admin, "POST", add, map[string]any{"host": "fake", "key": "pasted-key"}, 201) // a pasted key still works
	if len(written) != 1 || written[0] != "pasted-key" {
		t.Fatal(written)
	}
}
