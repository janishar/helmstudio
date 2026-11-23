package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
)

// The in-app assistant in studio (plan 18, 22.3 and 22.11; contracts/README.md "Assistant", API 1.44).

const tabA, tabB = "tab-aaaaaaaa", "tab-bbbbbbbb"

// screen is a screen summary of route with actions.
func screen(route string, actions ...string) map[string]any {
	return map[string]any{"route": route, "ids": map[string]any{}, "roles": []string{"user"}, "as_of": time.Now().UTC().Format(time.RFC3339),
		"hidden": false, "actions": append([]string{"navigate", "await"}, actions...), "fields": map[string]any{}}
}

// assistantOn turns the assistant on with the server provider ollama.
func (e *env) assistantOn() {
	e.t.Helper()
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	e.call(admin, "PUT", "/v1/studio/admin/assistant", map[string]any{"enabled": true, "provider": "ollama"}, 200)
}

// turns drains the outbox and returns the assistant commands, decoded.
func (e *env) turns() []gen.Message[message.AssistantTurn] {
	e.t.Helper()
	_, sent := e.relay()
	var out []gen.Message[message.AssistantTurn]
	for _, env := range sent {
		if strings.HasPrefix(env.Type, "cmd.assistant.") {
			var data message.AssistantTurn
			_ = json.Unmarshal(env.Data, &data)
			out = append(out, gen.Message[message.AssistantTurn]{Type: env.Type, JobID: env.JobID, OwnerID: env.OwnerID, Data: data})
		}
	}
	return out
}

// step applies one evt.assistant.step as the projector would.
func (e *env) step(owner string, step message.AssistantStep) {
	e.t.Helper()
	if step.Caps == nil {
		step.Caps = json.RawMessage(`{"model_calls": 1, "tool_calls": 0, "memory_writes": 0}`)
	}
	if err := studio.NewProjector(e.pool).ApplyAssistantStep(context.Background(), owner, step); err != nil {
		e.t.Fatal(err)
	}
}

func TestAssistantSettings(t *testing.T) {
	e := setup(t)
	path := "/v1/studio/admin/assistant"
	e.call(userA, "GET", path, nil, 403)
	e.call(userA, "PUT", path, map[string]any{"enabled": false}, 403)
	got := e.call(admin, "GET", path, nil, 200)
	if got["enabled"] != false || got["provider"] != nil || got["updated_at"] != nil ||
		got["limits"].(map[string]any)["messages_per_minute"] != float64(6) {
		t.Fatal(got)
	}
	// off: the list is served with status off, New chat is 409
	if list := e.call(userA, "GET", "/v1/studio/assistant/threads", nil, 200); list["status"].(map[string]any)["enabled"] != false {
		t.Fatal(list)
	}
	e.call(userA, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 409)
	for _, bad := range []map[string]any{{"enabled": true}, {"enabled": true, "provider": "nope"},
		{"enabled": false, "model": "x"}, {"enabled": true, "provider": "ollama", "model": "gpt-9"},
		{"enabled": false, "limits": map[string]any{"model_calls_per_day": 0, "tokens_per_day": 1000, "look_at_per_hour": 1,
			"messages_per_minute": 1}}, {"enabled": false, "web_search": true}, {"enabled": false, "unknown": 1}} {
		e.call(admin, "PUT", path, bad, 422)
	}
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	// a search provider is like a remote one: the deployment's switch, https, a key, api brave and models [web]
	search := map[string]any{"kind": "search", "api": "brave", "base_url": "https://api.search.brave.com/res/v1",
		"models": []string{"web"}, "key_secret": "provider-brave"}
	e.call(admin, "PUT", "/v1/studio/admin/providers/brave", search, 409)
	e.service.SetRemote(true)
	e.call(admin, "PUT", "/v1/studio/admin/providers/brave", map[string]any{"kind": "search", "base_url": "https://x.test/v1",
		"models": []string{"web"}, "key_secret": "k"}, 422) // no api brave
	e.call(admin, "PUT", "/v1/studio/admin/providers/brave", search, 201)
	sources, _ := json.Marshal(e.call(userA, "GET", "/v1/studio/model-sources", nil, 200))
	if strings.Contains(string(sources), "brave") {
		t.Fatalf("a search provider is never a model source: %s", sources)
	}
	e.call(admin, "PUT", path, map[string]any{"enabled": true, "provider": "brave"}, 422)
	e.call(admin, "PUT", path, map[string]any{"enabled": true, "provider": "ollama", "web_search": true, "search_provider": "ollama"}, 422)
	set := e.call(admin, "PUT", path, map[string]any{"enabled": true, "provider": "ollama", "model": "llama3.2:3b",
		"web_search": true, "search_provider": "brave", "limits": map[string]any{"model_calls_per_day": 50,
			"tokens_per_day": 100000, "look_at_per_hour": 0, "messages_per_minute": 2}}, 200)
	if set["enabled"] != true || set["model"] != "llama3.2:3b" || set["remote_enabled"] != true || set["updated_at"] == nil {
		t.Fatal(set)
	}
	// limits absent: unchanged
	set = e.call(admin, "PUT", path, map[string]any{"enabled": true, "provider": "ollama", "web_search": true, "search_provider": "brave"}, 200)
	if set["limits"].(map[string]any)["messages_per_minute"] != float64(2) {
		t.Fatal(set)
	}
	status := e.call(userA, "GET", "/v1/studio/assistant/threads", nil, 200)["status"].(map[string]any)
	if status["enabled"] != true || status["web_search"] != "brave" || status["thinks"] != false ||
		status["provider"].(map[string]any)["remote"] != false || status["provider"].(map[string]any)["title"] != "ollama" {
		t.Fatal(status)
	}
	// web search goes with the switch
	e.service.SetRemote(false)
	if status := e.call(userA, "GET", "/v1/studio/assistant/threads", nil, 200)["status"].(map[string]any); status["web_search"] != nil {
		t.Fatal(status)
	}
}

func TestAssistantChat(t *testing.T) {
	e := setup(t)
	e.assistantOn()
	threads := "/v1/studio/assistant/threads"
	e.call(userA, "POST", threads, map[string]any{"client_id": "short"}, 422)
	chat := e.call(userA, "POST", threads, map[string]any{"client_id": tabA}, 201)
	id := chat["id"].(string)
	if chat["title"] != "New chat" || chat["state"] != "idle" || chat["client_id"] != tabA || chat["pending"] != nil {
		t.Fatal(chat)
	}
	msgs := threads + "/" + id + "/messages"
	// another user's chat is 404 everywhere, before any 422
	e.call(userB, "GET", threads+"/"+id, nil, 404)
	e.call(userB, "POST", msgs, map[string]any{"kind": "continue"}, 404)
	e.call(userB, "GET", threads+"/"+id+"/events", nil, 404)
	e.call(userB, "DELETE", threads+"/"+id, nil, 404)
	e.call(userA, "GET", threads+"/not-a-uuid", nil, 404)
	for _, bad := range []map[string]any{{"kind": "message", "client_id": tabA, "text": "  ", "screen": screen("/")},
		{"kind": "message", "client_id": tabA, "text": "hi"},
		{"kind": "message", "client_id": tabA, "text": "hi", "screen": map[string]any{"route": "/", "x": 1}},
		{"kind": "message", "client_id": tabA, "text": "hi", "screen": screen("/"), "card_id": id + ":1:0"},
		{"kind": "message", "client_id": tabA, "text": "hi", "screen": screen("/", "bad-name")},
		{"kind": "message", "client_id": tabA, "text": "hi", "screen": func() map[string]any {
			s := screen("/")
			s["fields"] = map[string]any{"big": strings.Repeat("x", 16001)}
			return s
		}()},
		{"kind": "shout", "client_id": tabA}} {
		e.call(userA, "POST", msgs, bad, 422)
	}

	accepted := e.call(userA, "POST", msgs, map[string]any{"kind": "message", "client_id": tabA,
		"text": "  Make a portrait of @mara  ", "screen": screen("/gallery", "search")}, 202)
	entry := accepted["entry"].(map[string]any)
	if entry["kind"] != "user" || entry["text"] != "Make a portrait of @mara" || entry["turn"] != float64(1) ||
		accepted["thread"].(map[string]any)["state"] != "working" || accepted["thread"].(map[string]any)["title"] != "Make a portrait of @mara" {
		t.Fatal(accepted)
	}
	// one input = one command: owner from the row, roles from the token, the offered tools, the screen's actions
	sent := e.turns()
	if len(sent) != 1 || sent[0].Type != "cmd.assistant.turn" || *sent[0].JobID != id || sent[0].OwnerID != userA {
		t.Fatal(sent)
	}
	turn := sent[0].Data
	if turn.Turn != 1 || turn.ClientID != tabA || !slices.Equal(turn.Roles, []string{"user"}) ||
		!slices.Equal(turn.Actions, []string{"navigate", "await", "search"}) || turn.Input["kind"] != "message" ||
		turn.Settings.Source["provider"] != "ollama" || turn.Settings.Source["host"] != "mac" || turn.Settings.Search != nil {
		t.Fatalf("%+v", turn)
	}
	// admin tools never reach a user; web_search only while it is on
	for _, never := range []string{"model_jobs", "web_search"} {
		if slices.Contains(turn.Tools, never) {
			t.Fatalf("offered %s: %v", never, turn.Tools)
		}
	}
	// a message while it works is 409
	e.call(userA, "POST", msgs, map[string]any{"kind": "message", "client_id": tabA, "text": "again", "screen": screen("/")}, 409)

	// the step parks on two actions and a card: entries, then waiting with pending
	call := func(n int) string { return fmt.Sprintf("%s:1:%d", id, n) }
	action := func(n int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"id": %q, "name": "search", "args": {"q": "fox"}}`, call(n)))
	}
	card := json.RawMessage(fmt.Sprintf(`{"id": %q, "kind": "web_search", "title": "Search the web?", "rows": [],
		"tags": ["sends to Brave"], "state": "waiting"}`, call(2)))
	line := json.RawMessage(fmt.Sprintf(`{"id": %q, "status": "done", "text": "Remembered: likes blue", "undo": true}`, call(3)))
	parked := message.AssistantStep{ThreadID: id, Turn: 1, Seq: 1, Text: "Searching the gallery.", Lines: []json.RawMessage{line},
		Actions: []json.RawMessage{action(0), action(1)}, Cards: []json.RawMessage{card}}
	e.step(userB, parked) // another owner's step: dropped
	if got := e.call(userA, "GET", threads+"/"+id, nil, 200); got["state"] != "working" {
		t.Fatal(got)
	}
	e.step(userA, parked)
	e.step(userA, parked) // a republish: nothing twice
	got := e.call(userA, "GET", threads+"/"+id, nil, 200)
	pending := got["pending"].(map[string]any)
	if got["state"] != "waiting" || pending["turn"] != float64(1) || len(pending["actions"].([]any)) != 2 ||
		pending["cards"].([]any)[0] != call(2) || len(got["entries"].([]any)) != 4 {
		t.Fatal(got)
	}
	// two tabs: only the tab that runs the chat posts results; Continue here moves it
	results := map[string]any{"kind": "results", "client_id": tabB, "turn": 1, "results": []map[string]any{
		{"id": call(0), "status": "done", "line": map[string]any{"text": "Searched: fox", "undo": false}, "result": map[string]any{"n": 3}}}}
	if conflict := e.call(userA, "POST", msgs, results, 409); conflict["detail"] != "this chat is working in another tab" {
		t.Fatal(conflict)
	}
	moved := e.call(userA, "POST", msgs, map[string]any{"kind": "continue", "client_id": tabB}, 202)
	if moved["thread"].(map[string]any)["client_id"] != tabB || moved["entry"] != nil || len(e.turns()) != 0 {
		t.Fatal(moved)
	}
	// only what it waits on
	wrong := map[string]any{"kind": "answer", "client_id": tabB, "card_id": call(9), "answer": "yes"}
	if conflict := e.call(userA, "POST", msgs, wrong, 409); conflict["detail"] != "nothing waits on "+call(9) {
		t.Fatal(conflict)
	}
	e.call(userA, "POST", msgs, results, 202)
	sent = e.turns()
	if len(sent) != 1 || sent[0].Data.Turn != 2 || sent[0].Data.Input["kind"] != "results" ||
		!slices.Equal(sent[0].Data.Actions, []string{"navigate", "await", "search"}) {
		t.Fatal(sent)
	}
	// the resumed turn parks on the card alone; Not now answers it
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 2, Seq: 2, Cards: []json.RawMessage{card}})
	e.call(userA, "POST", msgs, map[string]any{"kind": "answer", "client_id": tabB, "card_id": call(2), "answer": "not_now"}, 202)
	sent = e.turns()
	if len(sent) != 1 || sent[0].Data.Turn != 3 || sent[0].Data.Input["answer"] != "not_now" {
		t.Fatal(sent)
	}
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 3, Seq: 3, Text: "OK, no search.", Done: true})

	// a closed tab: the next message resumes the parked turn with each action did_not_run, its cards cancelled
	e.call(userA, "POST", msgs, map[string]any{"kind": "message", "client_id": tabA, "text": "now?", "screen": screen("/")}, 202)
	_ = e.turns()
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 4, Seq: 4, Actions: []json.RawMessage{json.RawMessage(
		fmt.Sprintf(`{"id": "%s:4:0", "name": "navigate", "args": {}}`, id))}, Cards: []json.RawMessage{json.RawMessage(
		strings.ReplaceAll(string(card), call(2), id+":4:1"))}})
	e.call(userA, "POST", msgs, map[string]any{"kind": "message", "client_id": tabA, "text": "hello?", "screen": screen("/")}, 202)
	sent = e.turns()
	resumed, _ := json.Marshal(sent[0].Data.Input["results"])
	if len(sent) != 1 || !strings.Contains(string(resumed), `"status":"did_not_run"`) || !strings.Contains(string(resumed), id+":4:0") ||
		!strings.Contains(string(resumed), "the tab was closed") {
		t.Fatalf("%s", resumed)
	}
	detail := e.call(userA, "GET", threads+"/"+id, nil, 200)
	var cancelled bool
	for _, raw := range detail["entries"].([]any) {
		entry := raw.(map[string]any)
		if c, ok := entry["card"].(map[string]any); ok && c["id"] == id+":4:1" {
			cancelled = c["state"] == "cancelled"
		}
	}
	if !cancelled {
		t.Fatal(detail["entries"])
	}
	// an older turn's end leaves the new turn working; an error ends a turn as a notice
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 4, Seq: 5, Done: true})
	if got := e.call(userA, "GET", threads+"/"+id, nil, 200); got["state"] != "working" {
		t.Fatal(got["state"])
	}
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 5, Seq: 6, Done: true,
		Error: json.RawMessage(`{"code": "cap_day", "text": "You used today's 200 model calls."}`)})
	last := e.call(userA, "GET", threads+"/"+id, nil, 200)
	entries := last["entries"].([]any)
	notice := entries[len(entries)-1].(map[string]any)
	if last["state"] != "idle" || notice["kind"] != "notice" || notice["error"].(map[string]any)["code"] != "cap_day" {
		t.Fatal(notice)
	}

	// events: JSON after since, entry and thread events in seq order
	page := e.call(userA, "GET", threads+"/"+id+"/events?since=0", nil, 200)
	events := page["events"].([]any)
	if len(events) < 10 || events[0].(map[string]any)["seq"] != float64(1) || page["last_seq"] != events[len(events)-1].(map[string]any)["seq"] {
		t.Fatal(page)
	}
	tail := e.call(userA, "GET", fmt.Sprintf("%s/%s/events?since=%v", threads, id, page["last_seq"]), nil, 200)
	if len(tail["events"].([]any)) != 0 || tail["last_seq"] != page["last_seq"] {
		t.Fatal(tail)
	}
	e.call(userA, "GET", threads+"/"+id+"/events?since=-1", nil, 422)

	// messages a minute: 429 at the admin's cap (3 sent this minute)
	e.step(userA, message.AssistantStep{ThreadID: id, Turn: 6, Seq: 7, Done: true})
	e.exec(`UPDATE assistant_settings SET limits = jsonb_set(limits, '{messages_per_minute}', '3')`)
	if many := e.call(userA, "POST", msgs, map[string]any{"kind": "message", "client_id": tabA, "text": "more", "screen": screen("/")},
		429); many["detail"] != "at most 3 messages a minute" {
		t.Fatal(many)
	}

	// New chat asks for the previous chat's summary; delete writes cmd.assistant.delete
	second := e.call(userA, "POST", threads, map[string]any{"client_id": tabA}, 201)
	sent = e.turns()
	if len(sent) != 1 || *sent[0].JobID != id || sent[0].Data.Input["kind"] != "summarize" || len(sent[0].Data.Tools) != 0 {
		t.Fatal(sent)
	}
	if list := e.call(userA, "GET", threads, nil, 200)["threads"].([]any); len(list) != 2 ||
		list[0].(map[string]any)["id"] != second["id"] {
		t.Fatal(list)
	}
	if list := e.call(userB, "GET", threads, nil, 200)["threads"].([]any); len(list) != 0 {
		t.Fatal("another user's chats listed", list)
	}
	e.call(userA, "DELETE", threads+"/"+id, nil, 204)
	sent = e.turns()
	if len(sent) != 1 || sent[0].Type != "cmd.assistant.delete" || *sent[0].JobID != id {
		t.Fatal(sent)
	}
	e.call(userA, "GET", threads+"/"+id, nil, 404)
}

func TestAssistantOffered(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "tools", "offered.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Description string   `json:"description"`
			Screen      string   `json:"screen"`
			Roles       []string `json:"roles"`
			WebSearch   bool     `json:"web_search"`
			LookAt      bool     `json:"look_at"`
			Expect      []string `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range fixture.Cases {
		if c.Screen != "" {
			continue // screen_actions: web_service's
		}
		ran++
		if got := studio.OfferedTools(c.Roles, c.WebSearch, c.LookAt); !slices.Equal(got, c.Expect) {
			t.Errorf("%s: %v, want %v", c.Description, got, c.Expect)
		}
	}
	if ran == 0 {
		t.Fatal("no offer cases")
	}
}

func TestAssistantTools(t *testing.T) {
	e := setup(t)
	e.assistantOn()
	e.qwen()
	threads := "/v1/studio/assistant/threads"
	id := e.call(userA, "POST", threads, map[string]any{"client_id": tabA}, 201)["id"].(string)
	adminChat := e.call(admin, "POST", threads, map[string]any{"client_id": tabA}, 201)["id"].(string)
	for _, chat := range []struct{ user, id string }{{userA, id}, {admin, adminChat}} {
		e.call(chat.user, "POST", threads+"/"+chat.id+"/messages", map[string]any{"kind": "message", "client_id": tabA,
			"text": "hi", "screen": screen("/")}, 202)
	}
	sent := e.turns()
	if len(sent) != 2 || !slices.Contains(sent[1].Data.Tools, "model_jobs") || slices.Contains(sent[0].Data.Tools, "model_jobs") ||
		!slices.Contains(sent[0].Data.Tools, "look_at") {
		t.Fatal(sent[0].Data.Tools, sent[1].Data.Tools)
	}
	take := e.image(userA)
	other := e.image(userB)
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	ask := func(owner, thread, call, tool string, args any) message.ToolsReply {
		raw, _ := json.Marshal(args)
		return e.service.Tool(context.Background(), &message.ToolsRequest{OwnerID: owner, ThreadID: thread,
			CallID: thread + ":1:" + call, Tool: tool, Args: raw})
	}
	result := func(reply message.ToolsReply) map[string]any {
		t.Helper()
		if reply.Code != "" {
			t.Fatal(reply)
		}
		raw, _ := json.Marshal(reply.Result)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	// the API's own shapes, as the thread's owner
	found := result(ask(userA, id, "0", "search_takes", map[string]any{"order": "name", "limit": 5}))
	takes := found["takes"].([]any)
	if len(takes) != 1 || takes[0].(map[string]any)["id"] != take {
		t.Fatal(found)
	}
	if reply := ask(userA, id, "0", "search_takes", map[string]any{}); reply.Tool != "search_takes" {
		t.Fatal(reply) // asked again: the first reply
	}
	if got := result(ask(userA, id, "1", "get_take", map[string]any{"take_id": take})); got["take"] == nil || got["uses"] == nil {
		t.Fatal(got)
	}
	if got := result(ask(userA, id, "2", "sessions", map[string]any{"session_id": sessionID})); got["runs"] == nil || got["takes"] == nil {
		t.Fatal(got)
	}
	if got := result(ask(userA, id, "3", "hosts", map[string]any{})); got["hosts"] == nil || got["queue"] == nil {
		t.Fatal(got)
	}
	if got := result(ask(userA, id, "4", "get_workflow", map[string]any{"workflow_id": "echo"})); got["workflow"] == nil ||
		len(got["nodes"].([]any)) == 0 {
		t.Fatal(got)
	}
	// another user's take, thread or forged owner is not_found; admin tools are forbidden to a user
	for _, reply := range []message.ToolsReply{
		ask(userA, id, "5", "get_take", map[string]any{"take_id": other}),
		ask(userB, id, "6", "list_timelines", map[string]any{}),
	} {
		if reply.Code != "not_found" {
			t.Fatal(reply)
		}
	}
	if reply := ask(userA, id, "7", "model_jobs", map[string]any{}); reply.Code != "forbidden" {
		t.Fatal(reply)
	}
	if got := result(ask(admin, adminChat, "0", "model_jobs", map[string]any{})); got["jobs"] == nil {
		t.Fatal(got)
	}
	for _, args := range []map[string]any{{"limit": 21}, {"session_id": "x"}, {"take_id": take}} {
		if reply := ask(userA, id, "8", "sessions", args); reply.Code != "invalid" {
			t.Fatal(args, reply)
		}
	}
	if reply := ask(userA, id, "9", "web_search", map[string]any{"query": "x"}); reply.Code != "invalid" {
		t.Fatal(reply) // served by pipelines
	}
	// look_at after the Yes: an interactive ask run of the takes, kept with its call
	look := result(ask(userA, id, "10", "look_at", map[string]any{"take_ids": []string{take}, "question": "Is it sharp?"}))
	if look["workflow_id"] != "qwen-image-ask" || look["session_id"] != sessionID || look["run_id"] == nil {
		t.Fatal(look)
	}
	if again := result(ask(userA, id, "10", "look_at", map[string]any{"take_ids": []string{take}, "question": "Is it sharp?"})); again["run_id"] != look["run_id"] {
		t.Fatal(again)
	}
	if reply := ask(userA, id, "11", "look_at", map[string]any{"take_ids": []string{other}, "question": "?"}); reply.Code != "not_found" {
		t.Fatal(reply)
	}
	// API 1.49 (after look_at: a sequence makes its own session): show_timeline is the agent view; timeline_revisions the history or one revision
	tlID := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{take}}, 201)["id"].(string)
	if got := result(ask(userA, id, "21", "show_timeline", map[string]any{"timeline_id": tlID})); got["timeline"].(map[string]any)["clips"] == nil ||
		got["timeline"].(map[string]any)["takes"] != nil {
		t.Fatal(got)
	}
	if got := result(ask(userA, id, "22", "timeline_revisions", map[string]any{"timeline_id": tlID})); len(got["revisions"].(map[string]any)["revisions"].([]any)) != 1 {
		t.Fatal(got)
	}
	if got := result(ask(userA, id, "23", "timeline_revisions", map[string]any{"timeline_id": tlID, "revision": 1})); got["revision"].(map[string]any)["document"] == nil {
		t.Fatal(got)
	}
	if reply := ask(userA, id, "24", "timeline_revisions", map[string]any{"timeline_id": tlID, "revision": 1, "before": 2}); reply.Code != "invalid" {
		t.Fatal(reply)
	}
}

func TestAssistantMemory(t *testing.T) {
	e := setup(t)
	path := "/v1/studio/assistant/memory"
	e.call(userA, "GET", path, nil, 503) // pipelines not reachable
	e.assistantOn()
	chat := e.call(userA, "POST", "/v1/studio/assistant/threads", map[string]any{"client_id": tabA}, 201)
	var asked []message.AssistantMemoryRequest
	item := message.AssistantMemoryItem{ID: "3b2c6f1e-8a4d-4c1b-9e7f-2a5d8c0b1e47", Kind: "preference", Text: "likes blue hour",
		ThreadID: new(string), CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z"}
	*item.ThreadID = chat["id"].(string)
	e.service.SetAssistantMemory(func(_ context.Context, req message.AssistantMemoryRequest) (message.AssistantMemoryReply, error) {
		asked = append(asked, req)
		if req.OwnerID != userA || req.ItemID != "" && req.ItemID != item.ID {
			return message.AssistantMemoryReply{Error: "memory item not found", Code: "not_found"}, nil
		}
		switch req.Op {
		case "list":
			return message.AssistantMemoryReply{Items: []message.AssistantMemoryItem{item}, Counts: map[string]int{"preference": 1}}, nil
		case "edit":
			edited := item
			edited.Text = req.Text
			return message.AssistantMemoryReply{Item: &edited}, nil
		}
		return message.AssistantMemoryReply{Deleted: new(int)}, nil
	})
	list := e.call(userA, "GET", path+"?kind=preference", nil, 200)
	got := list["items"].([]any)[0].(map[string]any)
	if got["thread"].(map[string]any)["title"] != "New chat" || list["counts"].(map[string]any)["summary"] != float64(0) {
		t.Fatal(list)
	}
	e.call(userA, "GET", path+"?kind=likes", nil, 422)
	if edited := e.call(userA, "PATCH", path+"/"+item.ID, map[string]any{"text": "  likes dusk  "}, 200); edited["text"] != "likes dusk" {
		t.Fatal(edited)
	}
	e.call(userA, "PATCH", path+"/"+item.ID, map[string]any{"text": "  "}, 422)
	// another user's item is 404: studio asks only in the caller's namespace
	e.call(userB, "PATCH", path+"/"+item.ID, map[string]any{"text": "mine now"}, 404)
	e.call(userB, "DELETE", path+"/"+item.ID, nil, 404)
	e.call(userA, "DELETE", path+"/not-a-uuid", nil, 404)
	e.call(userA, "DELETE", path+"/"+item.ID, nil, 204)
	e.call(userA, "DELETE", path, nil, 204)
	if last := asked[len(asked)-1]; last.Op != "clear" || last.OwnerID != userA {
		t.Fatal(asked)
	}
	// a deleted chat's items name no chat
	e.call(userA, "DELETE", "/v1/studio/assistant/threads/"+chat["id"].(string), nil, 204)
	if got := e.call(userA, "GET", path, nil, 200)["items"].([]any)[0].(map[string]any); got["thread"] != nil {
		t.Fatal(got)
	}
}

func TestGalleryOrder(t *testing.T) {
	e := setup(t)
	ids := []string{e.image(userA), e.image(userA), e.image(userA)}
	e.exec(`UPDATE takes SET title = 'b' WHERE id = $1`, ids[0])
	e.exec(`UPDATE takes SET title = 'A', starred = true WHERE id = $1`, ids[1])
	e.exec(`UPDATE takes SET title = 'c', created_at = created_at - interval '1 hour' WHERE id = $1`, ids[2])
	order := func(query string) []string {
		var out []string
		for path := "/v1/studio/takes?limit=1&" + query; ; {
			page := e.call(userA, "GET", path, nil, 200)
			for _, take := range page["takes"].([]any) {
				out = append(out, take.(map[string]any)["title"].(string))
			}
			next, ok := page["next_cursor"].(string)
			if !ok {
				return out
			}
			path = "/v1/studio/takes?limit=1&cursor=" + next + "&" + query
		}
	}
	for query, want := range map[string]string{"order=name": "A,b,c", "order=starred": "A,", "order=oldest": "c,"} {
		if got := strings.Join(order(query), ","); !strings.HasPrefix(got, want) || len(order(query)) != 3 {
			t.Errorf("%s: %s, want %s…", query, got, want)
		}
	}
	page := e.call(userA, "GET", "/v1/studio/takes?limit=1&order=name", nil, 200)
	e.call(userA, "GET", "/v1/studio/takes?order=oldest&cursor="+page["next_cursor"].(string), nil, 422)
	e.call(userA, "GET", "/v1/studio/takes?order=best", nil, 422)
}
