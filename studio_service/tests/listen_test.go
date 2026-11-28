package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Listen when it cannot run (API 1.108; owner, 2026-11-28: "if the model is not available for this then handle the
// state"): a 409 in plain words with what to do, never a generic error; from 1.109 the smaller Qwen2.5-Omni-3B answers
// when Qwen3-Omni cannot, with a note saying so.
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
	variant := func(id, dir, presence string, peak, gb float64) map[string]any {
		return map[string]any{"id": id, "title": id, "presence": presence, "precisions": []string{"int8"}, "dir": dir,
			"memory": []map[string]any{{"precision": "int8", "peak_gb": peak, "components": []map[string]any{
				{"component": "model", "variant": id, "precision": "int8", "gb": gb},
				{"component": "asr", "variant": id, "precision": "bf16", "gb": 4.38}}}}}
	}
	omni := func(presence string, small ...string) {
		variants := []map[string]any{variant("q8", "Qwen3-Omni-30B-A3B-Instruct-GGUF", presence, 40.36, 31.49)}
		if len(small) > 0 { // API 1.109: the smaller Qwen2.5-Omni-3B
			variants = append(variants, variant("3b", "Qwen2.5-Omni-3B-GGUF", small[0], 13.68, 4.8))
		}
		value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "mac", "env": "mlx",
			"published_at": time.Now().UTC(), "published_by": "test", "licences": []any{},
			"models": []map[string]any{{"id": "qwen3-omni-30b-a3b", "family": "omni", "title": "Qwen Omni",
				"runtimes": []map[string]any{{"runtime": "mlx", "env": "mlx"}}, "variants": variants}}})
		if err := e.registry.SetRegistry("mac.mlx.models", value); err != nil {
			t.Fatal(err)
		}
	}
	e.modelHost("mlx", time.Now(), nil, nil)
	omni("missing", "missing")
	if got := listen(); got != "Can't listen: neither Qwen3-Omni nor the smaller Qwen2.5-Omni-3B is on this Mac; download one in Models (or connect the drive that holds it) and ask again." {
		t.Fatal(got)
	}
	omni("unplugged")
	if got := listen(); got != "Can't listen: Qwen3-Omni is on the T9 drive, which is not connected, and the smaller Qwen2.5-Omni-3B is not on this Mac; connect the T9 or download the 3B in Models, and ask again." {
		t.Fatal(got)
	}

	// API 1.109: the 3B answers when Qwen3-Omni cannot, placed on its own (much smaller) plan, and the reply says so
	e.call(userA, "POST", "/v1/studio/sessions", nil, 201)
	answer := func() map[string]any {
		t.Helper()
		return e.call(userA, "POST", "/v1/studio/takes/"+take+"/listen", map[string]any{"question": "Abrupt?"}, 202)
	}
	omni("unplugged", "present")
	run := answer()
	if run["workflow_id"] != "omni-listen-3b" || run["host"] != "mac" ||
		run["note"] != "Answered by the smaller listen model (Qwen2.5-Omni-3B): the T9 with Qwen3-Omni is not connected." {
		t.Fatal(run)
	}
	omni("missing", "present")
	if run := answer(); run["note"] != "Answered by the smaller listen model (Qwen2.5-Omni-3B): Qwen3-Omni is not on this Mac." {
		t.Fatal(run)
	}
	omni("present", "present")
	if run := answer(); run["workflow_id"] != "omni-listen" || run["note"] != nil {
		t.Fatal(run)
	}
	// each is planned on its own memory plan: 20 GB holds the 3B's 13.68 GB peak, not Qwen3-Omni's 40.36
	e.modelHost("mlx", time.Now(), nil, map[string]any{"memory": map[string]any{"budget_gb": 20, "used_gb": 2}})
	if run := answer(); run["workflow_id"] != "omni-listen-3b" || !strings.HasPrefix(run["note"].(string),
		"Answered by the smaller listen model (Qwen2.5-Omni-3B): Qwen3-Omni can't run now (") {
		t.Fatal(run)
	}
	e.modelHost("mlx", time.Now(), nil, map[string]any{"memory": map[string]any{"budget_gb": 10, "used_gb": 2}})
	if got := listen(); got != "Can't listen now: needs 13.7 GB at its peak; host mac has 10.0 GB." { // the 3B's need
		t.Fatal(got)
	}
}
