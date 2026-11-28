package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Listen when it cannot run (API 1.108; owner, 2026-11-28: "if the model is not available for this then handle the
// state"): a 409 in plain words with what to do, never a generic error.
func TestListenStates(t *testing.T) {
	e := setup(t)
	take := e.audioTake(userA, 2)
	listen := func() string {
		t.Helper()
		return e.call(userA, "POST", "/v1/studio/takes/"+take+"/listen", map[string]any{"question": "Abrupt?"}, 409)["detail"].(string)
	}
	e.host(time.Hour) // the fake runner is off
	if got := listen(); got != "Can't listen: no runner that can listen is on; start the Mac's runner and ask again." {
		t.Fatal(got)
	}
	for _, kind := range []string{"nodes", "workflows"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "omni."+kind+".valid.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.registry.SetRegistry("mac.mlx."+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	omni := func(presence string) {
		value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "mac", "env": "mlx",
			"published_at": time.Now().UTC(), "published_by": "test", "licences": []any{},
			"models": []map[string]any{{"id": "qwen3-omni-30b-a3b", "family": "omni", "title": "Qwen3-Omni 30B",
				"runtimes": []map[string]any{{"runtime": "mlx", "env": "mlx"}},
				"variants": []map[string]any{{"id": "q8", "title": "Q8_0", "presence": presence, "precisions": []string{"int8"},
					"dir": "Qwen3-Omni-30B-A3B-Instruct-GGUF"}}}}})
		if err := e.registry.SetRegistry("mac.mlx.models", value); err != nil {
			t.Fatal(err)
		}
	}
	e.modelHost("mlx", time.Now(), nil, nil)
	omni("missing")
	if got := listen(); got != "Can't listen: Qwen3-Omni is not on this Mac (connect the T9 drive that holds it, or download it in Models) and ask again." {
		t.Fatal(got)
	}
	omni("unplugged")
	if got := listen(); got != "Can't listen: Qwen3-Omni is on the T9 drive, which is not connected; connect the T9 and ask again." {
		t.Fatal(got)
	}
}
