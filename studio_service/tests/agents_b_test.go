// Integration tests of M4b stage B (contracts/README.md "Agents, fan-out, model sources and custom nodes", API 1.15):
// the admin's providers (authority, the host rule, the remote switch, redaction), Submit's sources, egress and reserved
// memory, agent and chat turn runs, fan-out branches, the studio.tools endpoints acting strictly as the batch's owner,
// the director's chat (isolation, sizes, the rate limit, events), the projector's agent steps, and a local server's
// runs going only to its host without admission while its memory is reserved.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
)

// directorPipeline is the shot-list director on the echo family: a director agent with every studio tool answering a
// list of up to 3 shots, a step fanned out over them (one branch per shot), the output of every branch's take.
func directorPipeline(model string) map[string]any {
	return map[string]any{"title": "Shots", "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "script", "type": "Text"}}},
		"director": map[string]any{"kind": "agent", "director": true, "instruction": "Make a shot list.", "from": []string{"$script"},
			"tools": []string{"gallery.search", "library.read", "rows.add", "rows.edit"}, "list": map[string]any{"max": 3},
			"model": model},
		"shots": map[string]any{"kind": "step", "workflow": "echo", "each": "director.items", "bind": map[string]any{"prompt.text": "director.item"}},
		"out":   map[string]any{"kind": "output", "from": "shots.takes", "collection": "Shots"},
	}, "edges": []any{[]any{"inputs", "director"}, []any{"director", "shots"}, []any{"shots", "out"}}}
}

// providerServer is ollama on host mac at a private address, 6.5 GB reserved.
var providerServer = map[string]any{"kind": "server", "base_url": "http://192.168.1.20:11434/v1", "models": []string{"qwen2.5:7b",
	"llama3.2:3b"}, "host": "mac", "memory_gb": 6.5}

// serverHost publishes pipelines' server executor on host mac (the server fixtures) and its live hosts key.
func (e *env) serverHost() {
	e.t.Helper()
	for _, kind := range []string{"nodes", "workflows"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "server."+kind+".valid.json"))
		if err == nil {
			err = e.registry.SetRegistry("mac.server."+kind, data)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	value, _ := json.Marshal(map[string]any{"host": "mac", "env": "server", "state": "idle", "run_id": nil,
		"updated_at": time.Now().UTC()})
	if err := e.registry.SetHost("mac.server", value); err != nil {
		e.t.Fatal(err)
	}
}

// tool sends studio.tools as pipelines would.
func (e *env) tool(owner, batchID string, row int, call, tool string, args any) message.ToolsReply {
	raw, _ := json.Marshal(args)
	return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: owner, BatchID: batchID, Row: row,
		Node: "director", CallID: fmt.Sprintf("%s:%d:1:director:0:t0:%s", batchID, row, call), Tool: tool, Args: raw})
}

func TestProviders(t *testing.T) {
	e := setup(t)
	e.service.SetLookup(func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "meta.lan" {
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
		return nil, fmt.Errorf("no such host")
	})
	path := "/v1/studio/admin/providers"
	// authority: every admin route is 403 for a user, before anything else
	e.call(userA, "GET", path, nil, 403)
	e.call(userA, "PUT", path+"/ollama", providerServer, 403)
	e.call(userA, "DELETE", path+"/ollama", nil, 403)
	e.call(userA, "POST", path+"/ollama/test", nil, 403)
	// the host rule
	for _, url := range []string{"http://8.8.8.8/v1", "http://169.254.169.254/v1", "http://meta.lan/v1", "http://2130706433/v1",
		"http://[fe80::1]/v1", "http://unknown.lan/v1"} {
		bad := map[string]any{"kind": "server", "base_url": url, "models": []string{"m"}, "host": "mac", "memory_gb": 1}
		e.call(admin, "PUT", path+"/bad", bad, 422)
	}
	created := e.call(admin, "PUT", path+"/ollama", providerServer, 201)
	if created["default_model"] != "qwen2.5:7b" || created["memory_gb"] != 6.5 || created["key_secret"] != nil {
		t.Fatal(created)
	}
	e.call(admin, "PUT", path+"/ollama", providerServer, 200)
	remote := map[string]any{"kind": "remote", "base_url": "https://fake-openai.test/v1", "models": []string{"fake-large"},
		"key_secret": "provider-fakeai"}
	if off := e.call(admin, "PUT", path+"/fakeai", remote, 409); off["detail"] != "remote models are off on this deployment" {
		t.Fatal(off)
	}
	e.service.SetRemote(true)
	e.call(admin, "PUT", path+"/fakeai", remote, 201)
	e.call(admin, "PUT", path+"/fakeai", providerServer, 409) // a kind never changes in place
	list := e.call(admin, "GET", path, nil, 200)
	if list["remote_enabled"] != true || len(list["providers"].([]any)) != 2 {
		t.Fatal(list)
	}
	// users see names and models only; remote ones only while the switch is on
	sources := e.call(userA, "GET", "/v1/studio/model-sources", nil, 200)
	raw, _ := json.Marshal(sources)
	for _, secret := range []string{"base_url", "key_secret", "192.168", "provider-fakeai", "memory_gb", "\"host\""} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("model-sources shows %s: %s", secret, raw)
		}
	}
	if len(sources["sources"].([]any)) != 2 {
		t.Fatal(sources)
	}
	e.service.SetRemote(false)
	if sources := e.call(userA, "GET", "/v1/studio/model-sources", nil, 200); len(sources["sources"].([]any)) != 1 {
		t.Fatal("a remote provider listed with the switch off", sources)
	}
	// the test is pipelines': no answer is 503, a failed test is 200 with ok false
	e.call(admin, "POST", path+"/ollama/test", nil, 503)
	var asked message.ProviderTestRequest
	e.service.SetProviderTest(func(_ context.Context, req message.ProviderTestRequest) (message.ProviderTestReply, error) {
		asked = req
		return message.ProviderTestReply{Error: "ollama: network error: connection refused", Code: "network", Seconds: 2}, nil
	})
	test := e.call(admin, "POST", path+"/ollama/test?model=qwen2.5:7b", nil, 200)
	if test["ok"] != false || test["failure"] != "network" || asked.Provider["base_url"] != "http://192.168.1.20:11434/v1" ||
		asked.Model == nil || *asked.Model != "qwen2.5:7b" {
		t.Fatal(test, asked)
	}
	e.call(admin, "POST", path+"/nope/test", nil, 404)
	e.call(admin, "DELETE", path+"/nope", nil, 404)
	e.call(admin, "DELETE", path+"/fakeai", nil, 204)
	// custom nodes: [] before pipelines published them, then the registry's
	if nodes := e.call(userA, "GET", "/v1/studio/custom-nodes", nil, 200); len(nodes["nodes"].([]any)) != 0 {
		t.Fatal(nodes)
	}
	custom, _ := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "pipelines.custom.valid.json"))
	if err := e.registry.SetRegistry("pipelines.engine.custom", custom); err != nil {
		t.Fatal(err)
	}
	if nodes := e.call(userA, "GET", "/v1/studio/custom-nodes", nil, 200); len(nodes["nodes"].([]any)) != 1 {
		t.Fatal(nodes)
	}
	// a custom node's text field bound into a step; the snapshot carries its version
	doc := map[string]any{"title": "Prefixed", "nodes": map[string]any{
		"inputs": map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
		"prefix": map[string]any{"kind": "custom", "node": "text/prefix", "inputs": map[string]any{"prefix": "still, "},
			"bind": map[string]any{"text": "$prompt"}},
		"still": map[string]any{"kind": "step", "workflow": "echo", "bind": map[string]any{"prompt.text": "prefix.text"}},
		"out":   map[string]any{"kind": "output", "from": "still.takes", "collection": "Prefixed"},
	}, "edges": []any{[]any{"inputs", "prefix"}, []any{"prefix", "still"}, []any{"still", "out"}}}
	pipelineID := e.call(userA, "POST", "/v1/studio/pipelines", doc, 201)["id"].(string)
	e.submit(userA, map[string]any{"pipeline_id": pipelineID,
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.prompt", "values": []string{"a fox"}}}})
	prefix := data(e.commands("cmd.pipeline.submit")[0])["snapshot"].(map[string]any)["pipeline"].(map[string]any)["nodes"].(map[string]any)["prefix"]
	if fmt.Sprint(prefix) != "map[bind:map[text:$prompt] inputs:map[prefix:still, ] kind:custom network:false node:text/prefix version:3f9c2a1b7d40]" {
		t.Fatal(prefix)
	}
	if err := e.registry.SetRegistry("pipelines.engine.custom", nil); err != nil {
		t.Fatal(err)
	}
	if again := e.call(userA, "GET", "/v1/studio/pipelines/"+pipelineID, nil, 200); !strings.Contains(fmt.Sprint(again["problems"]), "unknown_custom") {
		t.Fatal("an unregistered custom node", again["problems"])
	}
}

func TestDirectorBatch(t *testing.T) {
	e := setup(t)
	check := e.call(userA, "POST", "/v1/studio/pipelines/validate", directorPipeline("echo-ask"), 200)
	if check["can_run"] != true || fmt.Sprint(check["bounds"]) != "map[added_rows:100 chat:map[calls:1200 tokens:1.6e+06 tool_calls:1200] llm_calls:6 remote_calls:0 remote_tokens:0 runs:3 tool_calls:12]" ||
		fmt.Sprint(check["egress"]) != "[]" || fmt.Sprint(check["models"]) != "map[director:echo-ask]" {
		t.Fatal(check)
	}
	pipelineID := e.call(userA, "POST", "/v1/studio/pipelines", directorPipeline("echo-ask"), 201)["id"].(string)
	batch := e.submit(userA, map[string]any{"pipeline_id": pipelineID,
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.script", "values": []string{"a fox", "a den"}}}})
	id, sessionID := batch["id"].(string), batch["session_id"].(string)
	if fmt.Sprint(batch["director"]) != "map[max_wakes:100 node:director state:idle wakes:0]" || batch["remote"] != nil ||
		fmt.Sprint(batch["egress"]) != "[]" {
		t.Fatal(batch)
	}
	snapshot := data(e.commands("cmd.pipeline.submit")[0])["snapshot"].(map[string]any)["pipeline"].(map[string]any)
	nodes := snapshot["nodes"].(map[string]any)
	director, shots := nodes["director"].(map[string]any), nodes["shots"].(map[string]any)
	if director["workflow"] != "echo-ask" || fmt.Sprint(director["source"]) != "map[kind:local workflow:echo-ask]" ||
		fmt.Sprint(director["caps"]) != "map[max_tokens:16000 max_tool_calls:12 max_turns:6]" || director["director"] != true ||
		director["max_wakes"] != float64(100) || shots["fanout_max"] != float64(3) || shots["each"] != "director.items" ||
		fmt.Sprint(snapshot["remote"]) != "map[enabled:false max_calls:200 max_tokens:400000 providers:[]]" {
		t.Fatal(snapshot)
	}

	// an agent turn and a chat turn are ask runs; a fanned branch is a step run
	turn := func(row int, key string, workflow string, inputs map[string]any) (message.RunsCreated, *message.RunsCreateError) {
		return e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA, SessionID: sessionID,
			WorkflowID: workflow, Inputs: inputs, Lane: "batch", BatchID: id, Row: row, IdempotencyKey: id + key})
	}
	ask := map[string]any{"ask.prompt": "This is turn 1 of at most 6.", "ask.schema": `{"type":"object"}`}
	if run, refused := turn(1, ":1:1:director:0:t0", "echo-ask", ask); refused != nil || !run.Created {
		t.Fatal(refused)
	}
	if run, refused := turn(0, ":0:1:director:0:t0", "echo-ask", ask); refused != nil || !run.Created {
		t.Fatal("chat turn", refused)
	}
	if _, refused := turn(0, ":0:1:shots:0:0-0", "echo", nil); refused == nil {
		t.Fatal("row 0 for a step")
	}
	if run, refused := turn(1, ":1:1:shots:0:2-0", "echo", map[string]any{"prompt.text": "shot 3"}); refused != nil || !run.Created {
		t.Fatal("branch 2", refused)
	}
	if _, refused := turn(1, ":1:1:shots:0:3-0", "echo", map[string]any{"prompt.text": "x"}); refused == nil {
		t.Fatal("branch past the list max")
	}

	// tools act as the batch's owner, whatever the request says
	if forged := e.tool(userB, id, 1, "0", "gallery.search", map[string]any{}); forged.Code != "not_found" {
		t.Fatal("a forged owner", forged)
	}
	takeA, takeB := e.image(userA), e.image(userB)
	found := e.tool(userA, id, 1, "0", "gallery.search", map[string]any{"limit": 20})
	raw, _ := json.Marshal(found)
	if found.Code != "" || !strings.Contains(string(raw), takeA) || strings.Contains(string(raw), takeB) ||
		strings.Contains(string(raw), "u/"+userA) {
		t.Fatalf("gallery.search: %s", raw)
	}
	if bad := e.tool(userA, id, 1, "1", "gallery.search", map[string]any{"limit": 99}); bad.Code != "invalid" {
		t.Fatal(bad)
	}
	if bad := e.tool(userA, id, 1, "1", "memory.write", map[string]any{"key": "a", "text": "b"}); bad.Code != "invalid" {
		t.Fatal("a tool studio does not serve", bad)
	}
	e.element(userA, "Fox", "fox", "a red fox", takeA)
	e.element(userB, "Wolf", "wolf", "a grey wolf", takeB)
	library := e.tool(userA, id, 1, "2", "library.read", map[string]any{"kind": "element"})
	raw, _ = json.Marshal(library)
	if !strings.Contains(string(raw), `"handle":"fox"`) || strings.Contains(string(raw), "wolf") {
		t.Fatalf("library.read: %s", raw)
	}
	// rows.add: Submit's checks, the next number, idempotent on call_id
	if bad := e.tool(userA, id, 1, "3", "rows.add", map[string]any{"inputs": map[string]any{"nope": "x"}}); bad.Code != "invalid" ||
		bad.Error != "row: inputs.nope: not an input of this pipeline" {
		t.Fatal(bad)
	}
	added := e.tool(userA, id, 2, "1", "rows.add", map[string]any{"inputs": map[string]any{"script": "the den at dawn"}})
	again := e.tool(userA, id, 2, "1", "rows.add", map[string]any{"inputs": map[string]any{"script": "something else"}})
	if added.Row == nil || added.Row.Row != 3 || added.Row.Inputs["inputs.script"] != "the den at dawn" || again.Row == nil ||
		again.Row.Row != 3 || e.count(`SELECT count(*) FROM batch_rows WHERE batch_id = $1`, id) != 3 {
		t.Fatal(added, again)
	}
	if bad := e.tool(userA, id, 2, "2", "rows.add", map[string]any{"inputs": map[string]any{"script": 5}}); bad.Code != "invalid" {
		t.Fatal("a number for a Text input", bad)
	}
	if bad := e.tool(userA, id, 1, "3", "rows.edit", map[string]any{"row": 3, "inputs": map[string]any{"script": "x"}}); bad.Code != "invalid" {
		t.Fatal("rows.edit from a row", bad)
	}
	edited := e.tool(userA, id, 0, "0", "rows.edit", map[string]any{"row": 3, "inputs": map[string]any{"script": "close-up"}})
	if edited.Code != "" || edited.Row.Inputs["inputs.script"] != "close-up" {
		t.Fatal(edited)
	}
	// the reply row always carries seeds (required by the reply schema), {} with no seed param
	if raw, _ := json.Marshal(edited.Row); !strings.Contains(string(raw), `"seeds":{`) {
		t.Fatal("no seeds in the reply row", string(raw))
	}
	if started := e.tool(userA, id, 0, "1", "rows.edit", map[string]any{"row": 1, "inputs": map[string]any{"script": "x"}}); started.Code != "conflict" {
		t.Fatal("row 1 has a run", started)
	}
	e.exec(`UPDATE batches SET rows_added = 100 WHERE id = $1`, id)
	if capped := e.tool(userA, id, 0, "2", "rows.add", map[string]any{"inputs": map[string]any{"script": "x"}}); capped.Code != "cap" {
		t.Fatal(capped)
	}

	// the chat: the owner's only, sizes, the rate limit, the command, the events
	chat := "/v1/studio/batches/" + id + "/chat"
	e.call(userB, "GET", chat, nil, 404)
	e.call(userB, "POST", chat, map[string]any{"text": "hi"}, 404)
	e.call(userA, "POST", chat, map[string]any{"text": "   "}, 422)
	e.call(userA, "POST", chat, map[string]any{"text": strings.Repeat("a", 2001)}, 422)
	said := e.call(userA, "POST", chat, map[string]any{"text": " Make shot 3 a close-up. "}, 202)
	if said["role"] != "user" || said["text"] != "Make shot 3 a close-up." || said["wake"] != nil {
		t.Fatal(said)
	}
	commands := e.commands("cmd.pipeline.chat")
	if len(commands) != 1 || data(commands[0])["message_id"] != said["id"] || data(commands[0])["text"] != said["text"] {
		t.Fatal(commands)
	}
	for range 5 {
		e.call(userA, "POST", chat, map[string]any{"text": "more"}, 202)
	}
	e.call(userA, "POST", chat, map[string]any{"text": "too fast"}, 429)
	reply := "8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5d"
	e.pipeline(userA, id, "chat", map[string]any{"seq": 1, "message": map[string]any{"id": reply, "role": "director",
		"text": "Row 3 is a close-up now.", "wake": 1, "rows": []int{3, 99},
		"tool_calls": []any{map[string]any{"id": id + ":0:1:director:0:t0:0", "tool": "rows_edit", "args": map[string]any{"row": 3},
			"result": map[string]any{"row": 3, "status": "queued"}, "error": nil}}, "at": time.Now().UTC().Format(time.RFC3339Nano)}})
	e.pipeline(userB, id, "chat", map[string]any{"seq": 2, "message": map[string]any{"id": "8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5e",
		"role": "director", "text": "forged", "wake": 1, "rows": []int{}, "tool_calls": []any{}, "at": time.Now().UTC().Format(time.RFC3339Nano)}})
	e.pipeline(userA, id, "batch", map[string]any{"seq": 3, "status": "running", "director": map[string]any{"node": "director",
		"state": "thinking", "wakes": 1, "max_wakes": 100}})
	page := e.call(userA, "GET", chat, nil, 200)
	messages := page["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if len(messages) != 7 || last["id"] != reply || fmt.Sprint(last["rows"]) != "[3]" || len(last["tool_calls"].([]any)) != 1 ||
		fmt.Sprint(page["director"]) != "map[max_wakes:100 node:director state:thinking wakes:1]" {
		t.Fatal(page)
	}
	events := e.call(userA, "GET", "/v1/studio/batches/"+id+"/events?since=0", nil, 200)["events"].([]any)
	var chats []string
	for _, event := range events {
		if ev := event.(map[string]any); ev["type"] == "chat" {
			chats = append(chats, ev["data"].(map[string]any)["text"].(string))
		}
	}
	if len(chats) != 7 || chats[6] != "Row 3 is a close-up now." {
		t.Fatal(chats)
	}
	// an agent's tools part with its calls, and its decision on the execution view
	e.pipeline(userA, id, "step", map[string]any{"seq": 4, "row": 1, "attempt": 1, "step": 2, "node": "director", "part": "tools",
		"visit": 1, "status": "finished", "what": "2 tool calls", "changed": []string{}, "edge": nil, "loops": map[string]any{},
		"run_ids": []string{}, "take_ids": []string{}, "error": nil, "checkpoint_id": nil, "turn": 1,
		"tool_calls": []any{map[string]any{"id": id + ":1:1:director:0:t0:0", "tool": "gallery_search", "args": map[string]any{},
			"result": map[string]any{"takes": []any{}, "more": false}, "error": nil}}})
	e.pipeline(userA, id, "step", map[string]any{"seq": 5, "row": 1, "attempt": 1, "step": 3, "node": "director", "part": "route",
		"visit": 1, "status": "finished", "what": "3 shots", "changed": []string{}, "edge": map[string]any{"to": "shots", "via": "next"},
		"loops": map[string]any{}, "run_ids": []string{}, "take_ids": []string{}, "error": nil, "checkpoint_id": nil,
		"decision": map[string]any{"id": id + ":1:1:director:1", "node": "director", "kind": "agent", "visit": 1, "attempt": 1,
			"workflow": "echo-ask", "source": map[string]any{"kind": "remote", "provider": "x", "model": "y"}, "replayable": true,
			"agent": map[string]any{"turns": []any{}, "route": nil, "to": nil, "forced": false, "items": []string{"a", "b", "c"},
				"say": "done", "stopped": nil, "tool_calls": 1, "tokens": 900}}})
	execution := e.call(userA, "GET", "/v1/studio/batches/"+id+"/rows/1/execution", nil, 200)
	steps := execution["steps"].([]any)
	tools, route := steps[0].(map[string]any), steps[1].(map[string]any)
	decision := route["decision"].(map[string]any)
	if tools["turn"] != float64(1) || len(tools["tool_calls"].([]any)) != 1 || decision["kind"] != "agent" ||
		decision["replayable"] != false || decision["agent"].(map[string]any)["say"] != "done" {
		t.Fatal(execution)
	}
	// a batch whose pipeline has no director has no chat
	plain := e.submit(userA, map[string]any{"workflow_id": "echo", "rows": map[string]any{"lines": map[string]any{
		"param": "prompt.text", "values": []string{"x"}}}})
	e.call(userA, "GET", "/v1/studio/batches/"+plain["id"].(string)+"/chat", nil, 404)
	// the director's messages never fill the chat: 520 notices, and the user still posts; the page is the newest 500
	e.exec(`UPDATE batch_chat SET at = at - interval '3 minutes' WHERE batch_id = $1`, id)
	e.exec(`INSERT INTO batch_chat (id, batch_id, owner_id, role, text, wake, tool_calls, rows, seq, at)
		SELECT gen_random_uuid(), id, owner_id, 'notice', 'notice ' || g, 1, '[]', '{}', 100 + g,
		clock_timestamp() - interval '2 minutes' + g * interval '1 millisecond' FROM batches, generate_series(1, 520) g
		WHERE id = $1`, id)
	newest := e.call(userA, "POST", chat, map[string]any{"text": "still here"}, 202)
	page = e.call(userA, "GET", chat, nil, 200)
	messages = page["messages"].([]any)
	if len(messages) != 500 || messages[499].(map[string]any)["id"] != newest["id"] ||
		messages[0].(map[string]any)["text"] != "notice 22" {
		t.Fatal(len(messages), messages[0], messages[len(messages)-1])
	}
	// stopped: the director is off
	e.call(userA, "POST", "/v1/studio/batches/"+id+"/stop", nil, 202)
	e.pipeline(userA, id, "batch", map[string]any{"seq": 6, "status": "stopped"})
	e.exec(`DELETE FROM batch_chat WHERE batch_id = $1 AND role = 'user'`, id)
	if off := e.call(userA, "POST", chat, map[string]any{"text": "hello"}, 409); !strings.HasPrefix(off["detail"].(string), "the director is off") {
		t.Fatal(off)
	}
	if conflict := e.tool(userA, id, 0, "3", "rows.add", map[string]any{"inputs": map[string]any{"script": "x"}}); conflict.Code != "conflict" {
		t.Fatal(conflict)
	}
}

// A write on a local server: Submit lists what leaves the process and the reserved memory, snapshots the provider; its
// runs must name the provider and model, go only to the provider's host's server executor with no admission; the
// server's memory is subtracted from the host's budget for every other run; a used provider cannot be deleted.
func TestServerModel(t *testing.T) {
	e := setup(t)
	e.service.SetLookup(func(context.Context, string) ([]netip.Addr, error) { return nil, fmt.Errorf("unused") })
	e.serverHost()
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	doc := judgeFree("server:ollama")
	check := e.call(userA, "POST", "/v1/studio/pipelines/validate", doc, 200)
	egress := check["egress"].([]any)
	if check["can_run"] != true || len(egress) != 1 || egress[0].(map[string]any)["where"] != "server" ||
		fmt.Sprint(check["reserved"]) != "[map[host:mac memory_gb:6.5 provider:ollama]]" ||
		check["models"].(map[string]any)["rewrite"] != "server:ollama/qwen2.5:7b" {
		t.Fatal(check)
	}
	e.service.SetRemote(false)
	if remote := e.call(userA, "POST", "/v1/studio/pipelines/validate", judgeFree("remote:fakeai/fake-large"), 200); !strings.Contains(
		fmt.Sprint(remote["problems"]), "remote_off") {
		t.Fatal(remote)
	}
	pipelineID := e.call(userA, "POST", "/v1/studio/pipelines", doc, 201)["id"].(string)
	batch := e.submit(userA, map[string]any{"pipeline_id": pipelineID,
		"rows": map[string]any{"lines": map[string]any{"param": "inputs.prompt", "values": []string{"a fox"}}}})
	id, sessionID := batch["id"].(string), batch["session_id"].(string)
	if len(batch["egress"].([]any)) != 1 || len(batch["reserved"].([]any)) != 1 {
		t.Fatal(batch)
	}
	snapshot := data(e.commands("cmd.pipeline.submit")[0])["snapshot"].(map[string]any)["pipeline"].(map[string]any)
	rewrite := snapshot["nodes"].(map[string]any)["rewrite"].(map[string]any)
	provider := snapshot["providers"].(map[string]any)["ollama"].(map[string]any)
	if rewrite["workflow"] != "server-ask" || fmt.Sprint(rewrite["source"]) != "map[host:mac kind:server model:qwen2.5:7b provider:ollama]" ||
		provider["base_url"] != "http://192.168.1.20:11434/v1" || provider["memory_gb"] != 6.5 || provider["key_secret"] != nil ||
		snapshot["workflows"].(map[string]any)["server-ask"] == nil {
		t.Fatal(snapshot)
	}
	e.call(admin, "DELETE", "/v1/studio/admin/providers/ollama", nil, 409)
	create := func(inputs map[string]any) (message.RunsCreated, *message.RunsCreateError) {
		return e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA, SessionID: sessionID,
			WorkflowID: "server-ask", Inputs: inputs, Lane: "batch", BatchID: id, Row: 1, IdempotencyKey: id + ":1:1:rewrite:0:0"})
	}
	if _, refused := create(map[string]any{"ask.provider": "other", "ask.model": "qwen2.5:7b", "ask.prompt": "x"}); refused == nil {
		t.Fatal("another provider")
	}
	run, refused := create(map[string]any{"ask.provider": "ollama", "ask.model": "qwen2.5:7b", "ask.prompt": "fix it"})
	if refused != nil || run.Status != "queued" {
		t.Fatal(refused, run)
	}
	// only mac runs the server executor, and the run is pinned there
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	if got := e.gatePass(d, time.Now()); !slices.Equal(got, []string{"cmd.run.mac.server " + run.RunID}) {
		t.Fatal(got)
	}
	if pinned := e.count(`SELECT count(*) FROM runs WHERE id = $1 AND pin_host = 'mac'`, run.RunID); pinned != 1 {
		t.Fatal("not pinned")
	}
	// Run again calls the providers as they are now: never a disabled one, a moved base_url is the new one
	e.exec(`UPDATE batches SET status = 'done' WHERE id = $1`, id)
	off := maps(providerServer)
	off["enabled"] = false
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", off, 200)
	if refused := e.call(userA, "POST", "/v1/studio/batches/"+id+"/again", map[string]any{"decisions": "again"}, 422); !strings.Contains(
		refused["detail"].(string), "provider ollama is gone or disabled") {
		t.Fatal(refused)
	}
	moved := maps(providerServer)
	moved["base_url"] = "http://192.168.1.21:11434/v1"
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", moved, 200)
	e.call(userA, "POST", "/v1/studio/batches/"+id+"/again", map[string]any{"decisions": "again"}, 201)
	submits := e.commands("cmd.pipeline.submit")
	provider = data(submits[len(submits)-1])["snapshot"].(map[string]any)["pipeline"].(map[string]any)["providers"].(map[string]any)["ollama"].(map[string]any)
	if provider["base_url"] != "http://192.168.1.21:11434/v1" {
		t.Fatal(provider)
	}
}

// judgeFree is prompt → a write on model → an echo step → the output: no judge (a judge needs a local model).
func judgeFree(model string) map[string]any {
	return map[string]any{"title": "Server", "nodes": map[string]any{
		"inputs":  map[string]any{"kind": "inputs", "inputs": []any{map[string]any{"name": "prompt", "type": "Text"}}},
		"rewrite": map[string]any{"kind": "write", "from": []string{"$prompt"}, "to": "$prompt", "model": model},
		"still":   map[string]any{"kind": "step", "workflow": "echo", "bind": map[string]any{"prompt.text": "$prompt"}},
		"out":     map[string]any{"kind": "output", "from": "still.takes", "collection": "Server"},
	}, "edges": []any{[]any{"inputs", "rewrite"}, []any{"rewrite", "still"}, []any{"still", "out"}}}
}

// A local server's declared memory is reserved on its host: a run's budget_gb is the host's 48 GB less the 6.5 GB an
// enabled server declared, and the whole 48 once it is disabled.
func TestServerMemoryReserved(t *testing.T) {
	e := setup(t)
	e.twoEnvs()
	d := studio.NewDispatcher(e.pool, e.registry, nil, nil)
	server := map[string]any{"kind": "server", "base_url": "http://127.0.0.1:11434/v1", "models": []string{"m"}, "host": "mac",
		"memory_gb": 6.5}
	e.call(admin, "PUT", "/v1/studio/admin/providers/local", server, 201)
	first, session := e.start(userA, "qwen-image", turbo)
	if got := e.gatePass(d, time.Now()); !slices.Equal(got, []string{"cmd.run.mac.torch " + first}) || budgets[first] != 41.5 {
		t.Fatal(got, budgets[first])
	}
	e.done(userA, session, first)
	server["enabled"] = false
	e.call(admin, "PUT", "/v1/studio/admin/providers/local", server, 200)
	second, _ := e.start(userA, "qwen-image", turbo)
	if got := e.gatePass(d, time.Now()); !slices.Equal(got, []string{"cmd.run.mac.torch " + second}) || budgets[second] != float64(48) {
		t.Fatal("disabled: nothing reserved", got, budgets[second])
	}
}

// TestProviderPresets: API 1.51 (plan 25): the presets list is the embedded file, admin only; a preset's Fetch asks
// pipelines with the preset's own URL (never the body's: 422), 404 unknown, off without asking while remote is off;
// a key file's status, 404 malformed, 503 silent; compat and preset round-trip through PUT and GET and the snapshot.
func TestProviderPresets(t *testing.T) {
	e := setup(t)
	file, err := os.ReadFile(filepath.Join("..", "..", "contracts", "providers", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	_ = json.Unmarshal(file, &want)
	offered := []any{} // API 1.61: a plugin's preset is never in the list
	for _, p := range want["presets"].([]any) {
		if _, plugin := p.(map[string]any)["plugin"]; !plugin {
			offered = append(offered, p)
		}
	}
	want["presets"] = offered
	e.call(userA, "GET", "/v1/studio/admin/provider-presets", nil, 403)
	list := e.call(admin, "GET", "/v1/studio/admin/provider-presets", nil, 200)
	if list["remote_enabled"] != false || fmt.Sprint(list["presets"]) != fmt.Sprint(want["presets"]) {
		t.Fatal(list)
	}

	fetch := "/v1/studio/admin/provider-presets/"
	e.call(userA, "POST", fetch+"ollama/models", nil, 403)
	e.call(admin, "POST", fetch+"ollama/models", nil, 503) // pipelines silent
	var asked []message.ProviderTestRequest
	e.service.SetProviderTest(func(_ context.Context, req message.ProviderTestRequest) (message.ProviderTestReply, error) {
		asked = append(asked, req)
		return message.ProviderTestReply{Models: []string{"qwen3:8b", "llama3.2:3b"}, Seconds: 0.2}, nil
	})
	got := e.call(admin, "POST", fetch+"ollama/models", nil, 200)
	if got["ok"] != true || fmt.Sprint(got["models"]) != "[qwen3:8b llama3.2:3b]" || len(asked) != 1 || asked[0].Model != nil ||
		fmt.Sprint(asked[0].Provider) != "map[api:openai base_url:http://host.docker.internal:11434/v1 key_secret:<nil> kind:server timeout_s:15]" {
		t.Fatal(got, asked)
	}
	e.call(admin, "POST", fetch+"ollama/models", map[string]any{"base_url": "http://169.254.169.254/latest"}, 422)
	e.call(admin, "POST", fetch+"nope/models", map[string]any{}, 404)
	e.call(admin, "POST", fetch+"claude-code-bridge/models", map[string]any{}, 404) // a plugin's preset
	off := e.call(admin, "POST", fetch+"anthropic/models", nil, 200)
	if off["ok"] != false || off["failure"] != "off" || len(asked) != 1 {
		t.Fatal("a remote preset with the switch off", off, asked)
	}
	e.service.SetRemote(true)
	e.call(admin, "POST", fetch+"anthropic/models", map[string]any{"key_secret": "claude-2"}, 200)
	if p := asked[1].Provider; p["base_url"] != "https://api.anthropic.com/v1/" || *p["key_secret"].(*string) != "claude-2" ||
		p["kind"] != "remote" {
		t.Fatal(p)
	}
	raw, _ := json.Marshal(asked[1].Provider)
	if !strings.Contains(string(raw), `"compat":{"temperature":false,"strict_schema":true,"headers":{"anthropic-version":"2023-06-01"}}`) { // API 1.55
		t.Fatalf("%s", raw)
	}

	keys := "/v1/studio/admin/provider-keys/"
	e.call(userA, "GET", keys+"deepseek", nil, 403)
	e.call(admin, "GET", keys+"Bad..Name", nil, 404)
	e.call(admin, "GET", keys+"deepseek", nil, 503)
	e.service.SetProviderKey(func(_ context.Context, req message.ProviderKeyRequest) (message.ProviderKeyReply, error) {
		return message.ProviderKeyReply{Found: req.KeySecret == "deepseek"}, nil
	})
	if k := e.call(admin, "GET", keys+"deepseek", nil, 200); fmt.Sprint(k) != "map[found:true key_secret:deepseek path:deploy/secrets/providers/deepseek writable:false]" {
		t.Fatal(k)
	}
	if k := e.call(admin, "GET", keys+"openai", nil, 200); k["found"] != false {
		t.Fatal(k)
	}

	// compat and preset round-trip; a server may name a key; the snapshot carries both
	body := map[string]any{"kind": "remote", "base_url": "https://api.anthropic.com/v1/", "models": []string{"claude-sonnet-5-5"},
		"key_secret": "anthropic", "compat": map[string]any{"temperature": false, "strict_schema": true, "headers": map[string]any{"anthropic-version": "2023-06-01"}},
		"preset": "anthropic"}
	unknown := maps(body)
	unknown["compat"] = map[string]any{"retry": true}
	e.call(admin, "PUT", "/v1/studio/admin/providers/anthropic", unknown, 422)
	for _, h := range []string{"authorization", "x-api-key", "cookie", "host"} { // API 1.55: only allow-listed headers
		unknown["compat"] = map[string]any{"headers": map[string]any{h: "v"}}
		e.call(admin, "PUT", "/v1/studio/admin/providers/anthropic", unknown, 422)
	}
	put := e.call(admin, "PUT", "/v1/studio/admin/providers/anthropic", body, 201)
	if fmt.Sprint(put["compat"]) != "map[headers:map[anthropic-version:2023-06-01] strict_schema:true temperature:false]" || put["preset"] != "anthropic" {
		t.Fatal(put)
	}
	server := map[string]any{"kind": "server", "base_url": "http://192.168.1.20:9931/v1", "models": []string{"qwen3"}, "host": "mac",
		"memory_gb": 4, "key_secret": "llama-server"}
	if s := e.call(admin, "PUT", "/v1/studio/admin/providers/llama", server, 201); s["key_secret"] != "llama-server" || s["compat"] != nil {
		t.Fatal(s)
	}
	listed := e.call(admin, "GET", "/v1/studio/admin/providers", nil, 200)
	raw, _ = json.Marshal(listed)
	if !strings.Contains(string(raw), `"compat":{"headers":{"anthropic-version":"2023-06-01"},"strict_schema":true,"temperature":false}`) || !strings.Contains(string(raw), `"preset":"anthropic"`) {
		t.Fatalf("%s", raw)
	}
	e.call(admin, "POST", "/v1/studio/admin/providers/llama/test", nil, 200)
	e.call(admin, "POST", "/v1/studio/admin/providers/anthropic/test", nil, 200)
	raw, _ = json.Marshal(asked[2:])
	if !strings.Contains(string(raw), `"key_secret":"llama-server"`) || !strings.Contains(string(raw), `"compat":{"temperature":false,"strict_schema":true,"headers":{"anthropic-version":"2023-06-01"}}`) {
		t.Fatalf("%s", raw)
	}
}

// TestProviderKeySave: API 1.52 (plan 27): saving a key is admin only; the check's snapshot comes from the preset file
// or the stored provider (never the body: a base_url is 422), without key_secret, timeout 15; the key is trimmed and its
// 422 texts name the rule, never the value; 409 when pipelines says not writable or a folder; 503 on silence; the log
// line names the file and the admin, never the key. Remove answers the status after it.
func TestProviderKeySave(t *testing.T) {
	e := setup(t)
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	const key = "sk-test-secret-0123456789"
	keys := "/v1/studio/admin/provider-keys/"
	save := func(over map[string]any) map[string]any {
		body := map[string]any{"key": "  " + key + "\n", "preset": "deepseek"}
		for k, v := range over {
			if v == nil {
				delete(body, k)
			} else {
				body[k] = v
			}
		}
		return body
	}
	e.call(userA, "PUT", keys+"deepseek", save(nil), 403)
	e.call(userA, "DELETE", keys+"deepseek", nil, 403)
	e.call(admin, "PUT", keys+"deepseek", save(nil), 503) // pipelines silent
	e.call(admin, "DELETE", keys+"deepseek", nil, 503)

	var asked []message.ProviderKeyWriteRequest
	answer := message.ProviderKeyWriteReply{Found: true, Writable: true, Saved: true, Check: "ok"}
	e.service.SetProviderKeyWrite(func(_ context.Context, req message.ProviderKeyWriteRequest) (message.ProviderKeyWriteReply, error) {
		asked = append(asked, req)
		return answer, nil
	})
	for _, bad := range []map[string]any{
		{"base_url": "https://evil.example.com/v1"}, {"provider": "fakeai"}, {"preset": nil}, {"key": "sk with space"},
		{"key": "   "}, {"key": strings.Repeat("k", 1025)}, {"key": "sk-\u00e9"},
	} {
		got := e.call(admin, "PUT", keys+"deepseek", save(bad), 422)
		if strings.Contains(fmt.Sprint(got), "sk") && bad["key"] != nil {
			t.Fatal("a 422 echoes the key", got)
		}
	}
	for _, bad := range []string{"sk-ant-api03-abc\u2026", "sk-\u201cabc\u201d", "sk-a\u00a0b", "sk-a\u200bb"} {
		got := e.call(admin, "PUT", keys+"deepseek", save(map[string]any{"key": bad}), 422)
		if got["detail"] != dto.KeyCharsText || strings.Contains(fmt.Sprint(got), "sk-") {
			t.Fatal("a key with a non-ASCII character: not our fixed text", got)
		}
	}
	e.call(admin, "PUT", keys+"Bad..Name", save(nil), 404)
	e.call(admin, "PUT", keys+"deepseek", save(map[string]any{"preset": "nope"}), 404)
	e.call(admin, "PUT", keys+"deepseek", save(map[string]any{"preset": nil, "provider": "nope"}), 404)
	if len(asked) != 0 {
		t.Fatal("asked pipelines for a refused body", len(asked))
	}

	got := e.call(admin, "PUT", keys+"deepseek", save(nil), 200)
	if fmt.Sprint(got) != "map[check:ok found:true key_secret:deepseek path:deploy/secrets/providers/deepseek saved:true]" {
		t.Fatal(got)
	}
	if r := asked[0]; r.Op != "save" || r.Key != key || r.KeySecret != "deepseek" || r.Force ||
		fmt.Sprint(r.Provider) != "map[api:openai base_url:https://api.deepseek.com kind:remote timeout_s:15]" {
		t.Fatal(r.Op, r.KeySecret, r.Force, r.Provider)
	}
	if _, ok := asked[0].Provider["key_secret"]; ok {
		t.Fatal("the snapshot names a key file")
	}

	// a saved provider's stored URL; force passes through; refused and unreachable carry our detail
	e.service.SetRemote(true)
	e.call(admin, "PUT", "/v1/studio/admin/providers/fakeai", map[string]any{"kind": "remote", "base_url": "https://fake.test/v1",
		"models": []string{"m"}, "key_secret": "provider-fakeai"}, 201)
	answer = message.ProviderKeyWriteReply{Found: true, Writable: true, Saved: true, Check: "skipped"}
	got = e.call(admin, "PUT", keys+"provider-fakeai", save(map[string]any{"preset": nil, "provider": "fakeai", "force": true}), 200)
	if r := asked[1]; got["check"] != "skipped" || !r.Force || r.Provider["base_url"] != "https://fake.test/v1" || r.Provider["timeout_s"] != 15 ||
		r.Provider["key_secret"] != nil {
		t.Fatal(got, r.Provider)
	}
	answer = message.ProviderKeyWriteReply{Writable: true, Check: "refused", Detail: "the provider refused this key; not saved"}
	got = e.call(admin, "PUT", keys+"deepseek", save(nil), 200)
	if got["saved"] != false || got["check"] != "refused" || got["detail"] != "the provider refused this key; not saved" {
		t.Fatal(got)
	}
	answer = message.ProviderKeyWriteReply{Writable: true, Check: "unreachable", Detail: "timeout"}
	if got = e.call(admin, "PUT", keys+"deepseek", save(nil), 200); got["check"] != "unreachable" || got["detail"] != "timeout" {
		t.Fatal(got)
	}

	// not writable, a folder of that name, pipelines' internal error
	answer = message.ProviderKeyWriteReply{Error: "keys are set in files on this deployment", Code: "readonly"}
	if got = e.call(admin, "PUT", keys+"deepseek", save(nil), 409); got["detail"] != "keys are set in files on this deployment" {
		t.Fatal(got)
	}
	e.call(admin, "DELETE", keys+"deepseek", nil, 409)
	answer = message.ProviderKeyWriteReply{Error: "x", Code: "not_file"}
	e.call(admin, "PUT", keys+"deepseek", save(nil), 409)
	answer = message.ProviderKeyWriteReply{Error: "x", Code: "internal"}
	e.call(admin, "PUT", keys+"deepseek", save(nil), 503)

	// remove: the status after it (a later folder may still have one)
	answer = message.ProviderKeyWriteReply{Found: false, Writable: true}
	e.call(admin, "DELETE", keys+"Bad..Name", nil, 404)
	if got = e.call(admin, "DELETE", keys+"deepseek", nil, 200); fmt.Sprint(got) != "map[found:false key_secret:deepseek path:deploy/secrets/providers/deepseek writable:true]" {
		t.Fatal(got)
	}
	if r := asked[len(asked)-1]; r.Op != "remove" || r.Key != "" || r.Provider != nil {
		t.Fatal(r)
	}
	text := logged.String()
	for _, want := range []string{"provider key deepseek saved (check ok) by " + admin, "provider key provider-fakeai saved (check skipped) by " + admin,
		"provider key deepseek refused by the provider, not saved, by " + admin, "provider key deepseek not saved (could not check) by " + admin,
		"provider key deepseek removed by " + admin} {
		if !strings.Contains(text, want) {
			t.Fatalf("log misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, key) || strings.Contains(text, "sk with space") {
		t.Fatal("the key is in the log")
	}
}
