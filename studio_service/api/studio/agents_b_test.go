package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/registry"
)

// Keys of API 1.15: an agent's turns t<n>[r] below max_turns, the director's chat on row 0 (attempt 1, the wake below
// max_wakes), a fanned-out step's <item>-<seed> below its list max, a server-ask run naming its node's provider and
// model, and no run at all for a remote model.
func TestAgentRunKeys(t *testing.T) {
	two, director := 2, true
	p := storedPipeline{Doc: dto.PipelineDoc{Nodes: map[string]dto.PipelineNode{
		"boss":  {Kind: "agent", Caps: &dto.AgentCaps{MaxTurns: &two}, Director: &director, MaxWakes: &two},
		"plan":  {Kind: "agent"},
		"far":   {Kind: "decide"},
		"shots": {Kind: "step", Workflow: "echo", Seeds: &two},
	}}, Workflows: map[string]registry.Workflow{"server-ask": {ID: "server-ask"}, "echo-ask": {ID: "echo-ask"}, "echo": {ID: "echo"}},
		Models:  map[string]string{"boss": "server-ask", "plan": "echo-ask", "far": "remote"},
		Sources: map[string]map[string]string{"boss": {"kind": "server", "provider": "ollama", "model": "qwen2.5:7b", "host": "mac"}},
		Fan:     map[string]string{"shots": "plan"}, Lists: map[string]int{"plan": 3}}
	batch := "5a2e9c4b-1d7f-4b3a-9c8e-6f0d2a1b3c47"
	server := map[string]any{"ask.provider": "ollama", "ask.model": "qwen2.5:7b"}
	for _, c := range []struct {
		key, workflow string
		row           int
		inputs        map[string]any
		ok            bool
	}{
		{":1:1:boss:0:t0", "server-ask", 1, server, true},
		{":1:1:boss:0:t1r", "server-ask", 1, server, true},
		{":1:1:boss:0:t2", "server-ask", 1, server, false},                                                     // past max_turns
		{":1:1:boss:0:t0", "server-ask", 1, map[string]any{"ask.provider": "other"}, false},                    // another provider
		{":1:1:boss:0:t0", "server-ask", 1, map[string]any{"ask.provider": "ollama", "ask.model": "x"}, false}, // another model
		{":1:1:boss:0:0", "server-ask", 1, server, false},                                                      // an agent needs a turn
		{":0:1:boss:1:t0", "server-ask", 0, server, true},                                                      // the chat, wake 2
		{":0:1:boss:2:t0", "server-ask", 0, server, false},                                                     // past max_wakes
		{":0:1:plan:0:t0", "echo-ask", 0, nil, false},                                                          // row 0 is the director's
		{":0:2:boss:0:t0", "server-ask", 0, server, false},                                                     // the chat is attempt 1
		{":1:1:plan:0:t5", "echo-ask", 1, nil, true},
		{":1:1:far:0:0", "remote", 1, nil, false}, // a remote model never runs
		{":1:1:shots:0:2-1", "echo", 1, nil, true},
		{":1:1:shots:0:3-0", "echo", 1, nil, false}, // item past the list max
		{":1:1:shots:0:0-2", "echo", 1, nil, false}, // seed past seeds
		{":1:1:shots:0:0", "echo", 1, nil, false},   // a fanned step needs its item
	} {
		_, detail := pipelineStepRun(p, &message.RunsCreate{WorkflowID: c.workflow, Row: c.row, IdempotencyKey: batch + c.key,
			Inputs: c.inputs}, 1)
		if (detail == "") != c.ok {
			t.Errorf("%s: %q", c.key, detail)
		}
	}
	if !runsCreateKey.MatchString(batch + ":0:1:boss:1:t0") {
		t.Error("row 0 key refused")
	}
}

// The host rule (contracts/README.md, Model sources): a server only on loopback or private addresses, after DNS;
// link-local (metadata) never; numeric forms other than canonical dotted decimal refused; no user, query or fragment.
func TestHostRule(t *testing.T) {
	names := map[string][]string{"ollama.lan": {"192.168.1.20"}, "mixed.lan": {"10.0.0.2", "8.8.8.8"},
		"metadata.lan": {"169.254.169.254"}, "v6.lan": {"fd00::5"}, "mapped.lan": {"::ffff:8.8.8.8"}}
	s := &Service{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		found, ok := names[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []netip.Addr
		for _, a := range found {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}}
	for raw, ok := range map[string]bool{
		"http://127.0.0.1:11434/v1":       true,
		"http://[::1]:8080/v1":            true,
		"https://10.1.2.3/v1":             true,
		"http://172.20.0.5:1234":          true,
		"http://ollama.lan:11434/v1":      true,
		"http://v6.lan/v1":                true,
		"http://[fd12::1]/v1":             true,
		"http://8.8.8.8/v1":               false,
		"http://169.254.169.254/latest":   false,
		"http://[fe80::1]/v1":             false,
		"http://[::ffff:169.254.169.254]": false,
		"http://[::ffff:127.0.0.1]/v1":    true,
		"http://mixed.lan/v1":             false, // one public answer is enough to refuse
		"http://metadata.lan/v1":          false,
		"http://mapped.lan/v1":            false,
		"http://nowhere.lan/v1":           false,
		"http://2130706433/v1":            false, // decimal 127.0.0.1: resolvers disagree
		"http://0x7f000001/v1":            false,
		"http://0177.0.0.1/v1":            false,
		"http://127.1/v1":                 false,
		"http://0x08.0x08.0x08.0x08/v1":   false,
		"http://user:pw@127.0.0.1/v1":     false,
		"http://127.0.0.1/v1?x=1":         false,
		"http://127.0.0.1/v1#f":           false,
		"ftp://127.0.0.1/v1":              false,
		"http://[fe80::1%25en0]/v1":       false,
	} {
		if detail := s.hostRule(context.Background(), "server", raw); (detail == "") != ok {
			t.Errorf("server %s: %q", raw, detail)
		}
	}
	for raw, ok := range map[string]bool{"https://api.example.com/v1": true, "http://api.example.com/v1": false,
		"https://169.254.169.254/v1": false, "https://127.0.0.1/v1": false, "https://2130706433/v1": false,
		"https://10.1.2.3/v1": false, "https://192.168.1.20/v1": false, "https://localhost/v1": false, "https://[::1]/v1": false,
		"https://8.8.8.8/v1": true} {
		if detail := s.hostRule(context.Background(), "remote", raw); (detail == "") != ok {
			t.Errorf("remote %s: %q", raw, detail)
		}
	}
}

// OrbStack: host.docker.internal is 0.250.250.254 in the container, neither loopback nor private. A server may live at
// whatever that name resolves to here, by that name, another name or the literal; nothing else outside the ranges.
func TestHostRuleDockerHost(t *testing.T) {
	names := map[string][]string{"host.docker.internal": {"0.250.250.254"}, "other.example": {"0.250.250.254"}}
	s := &Service{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range names[host] {
			out = append(out, netip.MustParseAddr(a))
		}
		if out == nil {
			return nil, errors.New("no such host")
		}
		return out, nil
	}}
	check := func(kind string, cases map[string]bool) {
		t.Helper()
		for raw, ok := range cases {
			if detail := s.hostRule(context.Background(), kind, raw); (detail == "") != ok {
				t.Errorf("%s %s (host.docker.internal %v): %q", kind, raw, names["host.docker.internal"], detail)
			}
		}
	}
	check("server", map[string]bool{"http://host.docker.internal:11434/v1": true, "http://other.example/v1": true,
		"http://0.250.250.254/v1": true, "http://[::ffff:0.250.250.254]/v1": true, "http://0.250.250.253/v1": false,
		"http://8.8.8.8/v1": false, "http://169.254.169.254/v1": false})
	check("remote", map[string]bool{"http://host.docker.internal/v1": false, "http://0.250.250.254/v1": false})
	// Docker Desktop: OrbStack's address is refused again
	names["host.docker.internal"] = []string{"192.168.65.254"}
	check("server", map[string]bool{"http://host.docker.internal/v1": true, "http://other.example/v1": false,
		"http://0.250.250.254/v1": false})
	delete(names, "host.docker.internal") // the name does not resolve: nothing extra
	check("server", map[string]bool{"http://host.docker.internal/v1": false, "http://0.250.250.254/v1": false})
}

// A provider body: a server has host and memory (from 1.51 maybe a key); a remote one a key file and https.
func TestProviderPutShape(t *testing.T) {
	host, mem, key, one, bad := "mac", 6.5, "provider-x", 1, "Not a preset"
	for _, c := range []struct {
		body dto.ProviderPut
		ok   bool
	}{
		{dto.ProviderPut{Kind: "server", BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen2.5:7b"}, Host: &host, MemoryGB: &mem}, true},
		{dto.ProviderPut{Kind: "server", BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen2.5:7b"}, Host: &host}, false},
		{dto.ProviderPut{Kind: "server", BaseURL: "http://127.0.0.1/v1", Models: []string{"m"}, Host: &host, MemoryGB: &mem, KeySecret: &key}, true}, // 1.51
		{dto.ProviderPut{Kind: "server", BaseURL: "http://127.0.0.1/v1", Models: []string{"m"}, Host: &host, MemoryGB: &mem, MaxInFlight: &one}, false},
		{dto.ProviderPut{Kind: "remote", BaseURL: "https://api.example.com/v1", Models: []string{"m"}, KeySecret: &key, Preset: &bad}, false},
		{dto.ProviderPut{Kind: "remote", BaseURL: "https://api.example.com/v1", Models: []string{"m"}, KeySecret: &key}, true},
		{dto.ProviderPut{Kind: "remote", BaseURL: "https://api.example.com/v1", Models: []string{"m"}}, false},
		{dto.ProviderPut{Kind: "remote", BaseURL: "http://api.example.com/v1", Models: []string{"m"}, KeySecret: &key}, false},
		{dto.ProviderPut{Kind: "remote", BaseURL: "https://a.example.com/v1", Models: []string{"m", "m"}, KeySecret: &key}, false},
	} {
		if detail := c.body.Validate(); (detail == "") != c.ok {
			t.Errorf("%+v: %q", c.body, detail)
		}
	}
}

// A step event's tool calls keep the contract's shape only: known tools, object args, results cut at 8000 characters.
func TestStepToolCalls(t *testing.T) {
	long := `{"x": "` + strings.Repeat("a", 8000) + `"}`
	raw := json.RawMessage(`[{"id": "k:0", "tool": "gallery_search", "args": {"text": "fox"}, "result": {"takes": []}, "error": null},
		{"id": "k:1", "tool": "shell", "args": {}, "result": null, "error": null},
		{"id": "k:2", "tool": "rows_add", "args": "x", "result": null, "error": null},
		{"id": "k:3", "tool": "memory_read", "args": {}, "result": ` + long + `, "error": null}]`)
	out, _ := json.Marshal(toolCalls(raw, 4))
	var calls []map[string]any
	_ = json.Unmarshal(out, &calls)
	if len(calls) != 2 || calls[0]["id"] != "k:0" || calls[1]["id"] != "k:3" || calls[1]["result"] != nil {
		t.Fatalf("%s", out)
	}
}

// A custom field's value checked against its JSON schema (the subset pydantic writes).
func TestFitsSchema(t *testing.T) {
	for schema, cases := range map[string]map[string]bool{
		`{"type": "string", "maxLength": 3}`:                            {`"abc"`: true, `"abcd"`: false, `1`: false},
		`{"type": "integer", "minimum": 1}`:                             {`1`: true, `0`: false, `1.5`: false},
		`{"type": ["string", "null"]}`:                                  {`null`: true, `"x"`: true, `true`: false},
		`{"enum": ["a", "b"]}`:                                          {`"a"`: true, `"c"`: false},
		`{"type": "array", "items": {"type": "string"}, "maxItems": 1}`: {`["a"]`: true, `["a", "b"]`: false, `[1]`: false},
	} {
		for value, ok := range cases {
			var v any
			_ = json.Unmarshal([]byte(value), &v)
			if fitsSchema(json.RawMessage(schema), v) != ok {
				t.Errorf("%s ← %s", schema, value)
			}
		}
	}
}
