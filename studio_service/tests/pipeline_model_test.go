// Integration tests of the pipelines default model (contracts/README.md "Model sources: the pipelines default model",
// API 1.74, plan 33): admin only, the 422s and 409, what a write's auto resolves to (falling back to the local LLM when
// the setting names nothing usable), and Submit turning the snapshot's remote on for a remote default.
package tests

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/common"
)

func TestPipelineDefaultModel(t *testing.T) {
	e := setup(t)
	e.service.SetLookup(func(context.Context, string) ([]netip.Addr, error) { return nil, fmt.Errorf("unused") })
	path := "/v1/studio/admin/pipeline-model"
	e.call(userA, "GET", path, nil, 403)
	e.call(userA, "PUT", path, map[string]any{"mode": "local"}, 403)
	if got := e.call(admin, "GET", path, nil, 200); got["mode"] != "local" || got["provider"] != nil || got["updated_at"] != nil {
		t.Fatal(got)
	}
	for _, bad := range []map[string]any{{"mode": "agent"}, {"mode": "local", "provider": "ollama"}, {"mode": "remote"},
		{"mode": "assistant", "model": "m"}, {"mode": "remote", "provider": "ollama"}} {
		e.call(admin, "PUT", path, bad, 422)
	}
	if got := e.call(admin, "PUT", path, map[string]any{"mode": "assistant"}, 422); got["detail"] != "the Assistant is not set up" {
		t.Fatal(got)
	}
	if err := common.System(context.Background(), e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO providers (name, kind, base_url, models, default_model, host,
			memory_gb, owner_id) VALUES ('mine', 'server', 'http://127.0.0.1:1/v1', '{m}', 'm', 'mac', 0.5, $1)`, admin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.call(admin, "PUT", path, map[string]any{"mode": "remote", "provider": "mine"}, 422); got["detail"] != "mine is only yours; pick a shared provider" {
		t.Fatal(got)
	}

	auto := func() any { return e.call(userA, "GET", "/v1/studio/model-sources", nil, 200)["auto"] }
	rewrite := func() any {
		return e.call(userA, "POST", "/v1/studio/pipelines/validate", judgeFree("auto"), 200)["models"].(map[string]any)["rewrite"]
	}
	if auto() != nil || rewrite() != nil {
		t.Fatal("local: the family ask (echo has none: unknown_model)", auto(), rewrite())
	}
	// a server on this network: no switch needed, the model defaults to its default_model
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	e.call(admin, "PUT", path, map[string]any{"mode": "remote", "provider": "ollama", "model": "gpt-9"}, 422)
	if got := e.call(admin, "PUT", path, map[string]any{"mode": "remote", "provider": "ollama"}, 200); got["model"] != "qwen2.5:7b" || got["updated_at"] == nil {
		t.Fatal(got)
	}
	if fmt.Sprint(auto()) != "map[kind:server model:qwen2.5:7b provider:ollama title:ollama]" || rewrite() != "server:ollama/qwen2.5:7b" {
		t.Fatal(auto(), rewrite())
	}

	// a remote one: under the deployment's switch; with it off auto falls back to the local LLM
	e.service.SetRemote(true)
	e.call(admin, "PUT", "/v1/studio/admin/providers/fakeai", map[string]any{"kind": "remote", "base_url": "https://fake-openai.test/v1",
		"models": []string{"fake-large"}, "key_secret": "provider-fakeai"}, 201)
	e.call(admin, "PUT", path, map[string]any{"mode": "remote", "provider": "fakeai"}, 200)
	e.service.SetRemote(false)
	if got := e.call(admin, "PUT", path, map[string]any{"mode": "remote", "provider": "fakeai"}, 409); got["detail"] != "remote models are off on this deployment" {
		t.Fatal(got)
	}
	if auto() != nil || rewrite() != nil {
		t.Fatal("switch off: local", auto(), rewrite())
	}
	e.service.SetRemote(true)
	// Submit: no pipeline remote switch, yet the snapshot's remote is on for fakeai, and egress lists it
	pipelineID := e.call(userA, "POST", "/v1/studio/pipelines", judgeFree("auto"), 201)["id"].(string)
	batch := e.submit(userA, map[string]any{"pipeline_id": pipelineID,
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.prompt", "values": []string{"a fox"}}}})
	if egress := batch["egress"].([]any); len(egress) != 1 || egress[0].(map[string]any)["provider"] != "fakeai" {
		t.Fatal(batch)
	}
	snapshot := data(e.commands("cmd.pipeline.submit")[0])["snapshot"].(map[string]any)["pipeline"].(map[string]any)
	node := snapshot["nodes"].(map[string]any)["rewrite"].(map[string]any)
	if fmt.Sprint(snapshot["remote"]) != "map[enabled:true max_calls:200 max_tokens:400000 providers:[fakeai]]" ||
		fmt.Sprint(node["source"]) != "map[kind:remote model:fake-large provider:fakeai]" || snapshot["providers"].(map[string]any)["fakeai"] == nil {
		t.Fatal(snapshot)
	}

	// Same as Assistant follows the Assistant's provider; a disabled one is the local LLM again
	e.call(admin, "PUT", "/v1/studio/admin/assistant", map[string]any{"enabled": true, "provider": "ollama"}, 200)
	if got := e.call(admin, "PUT", path, map[string]any{"mode": "assistant"}, 200); got["mode"] != "assistant" || got["provider"] != nil {
		t.Fatal(got)
	}
	if rewrite() != "server:ollama/qwen2.5:7b" {
		t.Fatal(rewrite())
	}
	off := maps(providerServer)
	off["enabled"] = false
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", off, 200)
	if auto() != nil || rewrite() != nil {
		t.Fatal("disabled: local", auto(), rewrite())
	}
	e.call(admin, "PUT", path, map[string]any{"mode": "local"}, 200)
}
