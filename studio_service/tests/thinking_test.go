// Thinking per model (plan 28, API 1.53; contracts/README.md "Model sources: thinking"): a PUT's levels checked against
// the preset's rules, a strings-only PUT and a row from before 1.53 as they were, thinking_levels, the snapshot resolved
// from the rules (a saved level's fragment and tokens, a default's room, always thinks), a model source's levels, and
// Fetch's thinking from the rules or from OpenRouter's reasoning.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestProviderThinking(t *testing.T) {
	e := setup(t)
	e.service.SetRemote(true)
	var asked []message.ProviderTestRequest
	reply := message.ProviderTestReply{Models: []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat"}, Seconds: 0.1}
	e.service.SetProviderTest(func(_ context.Context, req message.ProviderTestRequest) (message.ProviderTestReply, error) {
		asked = append(asked, req)
		return reply, nil
	})
	path := "/v1/studio/admin/providers/deepseek"
	body := map[string]any{"kind": "remote", "base_url": "https://api.deepseek.com", "key_secret": "deepseek", "preset": "deepseek",
		"models": []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat"}}
	with := func(th map[string]any) map[string]any {
		b := maps(body)
		b["thinking"] = th
		return b
	}
	for th, detail := range map[string]string{
		`{"deepseek-flash": "medium"}`: "thinking: deepseek-flash takes off, low, high",
		`{"deepseek-chat": "off"}`:     "thinking: deepseek-chat has no thinking setting",
		`{"gpt-6": "low"}`:             "thinking: gpt-6 is not one of models",
		`{"deepseek-flash": "max"}`:    "thinking: deepseek-flash: a level is off, on, low, medium or high",
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(th), &m)
		if got := e.call(admin, "PUT", path, with(m), 422); got["detail"] != detail {
			t.Errorf("%s: %v", th, got)
		}
	}

	// strings only: as before (no thinking), thinking_levels for the models a rule covers
	plain := e.call(admin, "PUT", path, body, 201)
	if plain["thinking"] != nil || fmt.Sprint(plain["thinking_levels"]) !=
		"map[deepseek-flash:map[default:high levels:[off low high]] deepseek-v4-pro:map[default:high levels:[off low high]]]" {
		t.Fatal(plain)
	}
	// nothing saved: the default's room only (body {}, high's tokens); deepseek-chat no rule
	e.call(admin, "POST", path+"/test?model=deepseek-flash", nil, 200)
	snap, _ := json.Marshal(asked[0].Provider["thinking"])
	if string(snap) != `{"deepseek-flash":{"body":{},"level":"high","tokens":8192},"deepseek-v4-pro":{"body":{},"level":"high","tokens":8192}}` {
		t.Fatalf("%s", snap)
	}
	// Test lists the models a rule covers
	if got := e.call(admin, "POST", path+"/test", nil, 200); fmt.Sprint(got["thinking"]) !=
		"map[deepseek-flash:map[default:high levels:[off low high]] deepseek-v4-pro:map[default:high levels:[off low high]]]" {
		t.Fatal(got)
	}

	set := e.call(admin, "PUT", path, with(map[string]any{"deepseek-flash": "off", "deepseek-v4-pro": "low"}), 200)
	if fmt.Sprint(set["thinking"]) != "map[deepseek-flash:off deepseek-v4-pro:low]" {
		t.Fatal(set)
	}
	e.call(admin, "POST", path+"/test?model=deepseek-flash", nil, 200)
	snap, _ = json.Marshal(asked[len(asked)-1].Provider["thinking"])
	if string(snap) != `{"deepseek-flash":{"body":{"thinking":{"type":"disabled"}},"level":"off","tokens":0},`+
		`"deepseek-v4-pro":{"body":{"thinking":{"type":"enabled"},"reasoning_effort":"low"},"level":"low","tokens":1024}}` {
		t.Fatalf("%s", snap)
	}
	sources := e.call(userA, "GET", "/v1/studio/model-sources", nil, 200)
	if raw, _ := json.Marshal(sources); !strings.Contains(string(raw), `"thinking":{"deepseek-flash":"off","deepseek-v4-pro":"low"}`) {
		t.Fatalf("%s", raw)
	}

	// always thinks: no setting (422), the on tokens in the snapshot
	claude := map[string]any{"kind": "remote", "base_url": "https://api.anthropic.com/v1/", "key_secret": "anthropic", "preset": "anthropic",
		"models": []string{"claude-sonnet-5-5"}}
	bad := maps(claude)
	bad["thinking"] = map[string]any{"claude-sonnet-5-5": "on"}
	e.call(admin, "PUT", "/v1/studio/admin/providers/anthropic", bad, 422)
	if got := e.call(admin, "PUT", "/v1/studio/admin/providers/anthropic", claude, 201); fmt.Sprint(got["thinking_levels"]) !=
		"map[claude-sonnet-5-5:map[always:true levels:[]]]" {
		t.Fatal(got)
	}
	e.call(admin, "POST", "/v1/studio/admin/providers/anthropic/test?model=claude-sonnet-5-5", nil, 200)
	snap, _ = json.Marshal(asked[len(asked)-1].Provider["thinking"])
	if string(snap) != `{"claude-sonnet-5-5":{"body":{},"level":"on","tokens":4096}}` {
		t.Fatalf("%s", snap)
	}

	// a row from before 1.53 (thinking {} by default, no preset): read as before, nothing in the snapshot
	e.sql(`INSERT INTO providers (name, kind, base_url, models, default_model, key_secret, max_in_flight)
		VALUES ('old', 'remote', 'https://api.example.com/v1', '{deepseek-flash}', 'deepseek-flash', 'old', 2)`)
	list := e.call(admin, "GET", "/v1/studio/admin/providers", nil, 200)
	for _, p := range list["providers"].([]any) {
		if p := p.(map[string]any); p["name"] == "old" && (p["thinking"] != nil || p["thinking_levels"] != nil) {
			t.Fatal(p)
		}
	}
	e.call(admin, "POST", "/v1/studio/admin/providers/old/test?model=deepseek-flash", nil, 200)
	if _, ok := asked[len(asked)-1].Provider["thinking"]; ok {
		t.Fatal(asked[len(asked)-1].Provider)
	}

	// OpenRouter: Fetch's thinking from its reasoning (off unless mandatory; fewer than two levels: none), every level
	// allowed at PUT, sent as reasoning {effort}
	reply = message.ProviderTestReply{Models: []string{"a/thinks", "a/free", "a/one", "a/plain"}, Reasoning: map[string]message.ModelReasoning{
		"a/thinks": {Efforts: []string{"low", "high", "max"}, Mandatory: true},
		"a/free":   {Efforts: []string{"none", "medium"}},
		"a/one":    {Efforts: []string{"high"}, Mandatory: true},
	}}
	if got := e.call(admin, "POST", "/v1/studio/admin/provider-presets/openrouter/models", nil, 200); fmt.Sprint(got["thinking"]) !=
		"map[a/free:map[levels:[off medium]] a/thinks:map[levels:[low high]]]" {
		t.Fatal(got)
	}
	router := map[string]any{"kind": "remote", "base_url": "https://openrouter.ai/api/v1", "key_secret": "openrouter", "preset": "openrouter",
		"models": []string{"a/free", "a/thinks"}, "thinking": map[string]any{"a/free": "off", "a/thinks": "high"}}
	e.call(admin, "PUT", "/v1/studio/admin/providers/openrouter", router, 201)
	e.call(admin, "POST", "/v1/studio/admin/providers/openrouter/test?model=a/free", nil, 200)
	snap, _ = json.Marshal(asked[len(asked)-1].Provider["thinking"])
	if string(snap) != `{"a/free":{"body":{"reasoning":{"effort":"none"}},"level":"off","tokens":0},`+
		`"a/thinks":{"body":{"reasoning":{"effort":"high"}},"level":"high","tokens":8192}}` {
		t.Fatalf("%s", snap)
	}
}
