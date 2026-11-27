// Integration tests of Magic board names (API 1.91, contracts/README.md "Magic", Names): an untitled board named once
// from its first block's prompt, at once and then by the namer through the board's session (made then, the board id
// as the run id); a typed name or a rename is the user's and kept.
package tests

import (
	"encoding/json"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestMagicBoardNames(t *testing.T) {
	e := setup(t)
	e.service.SetRemote(true)
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	e.call(admin, "PUT", "/v1/studio/admin/session-names", map[string]any{"mode": "remote", "provider": "ollama"}, 200)
	const boards = "/v1/studio/magic/boards"
	b := e.call(userA, "POST", boards, nil, 201)
	id, base := b["id"].(string), boards+"/"+b["id"].(string)
	if b["name"] != "Untitled board" || b["title_source"] != "default" || b["named_by"] != nil || b["session_id"] != nil {
		t.Fatal(b)
	}
	e.sent()
	edit := func(revision float64, prompt string) map[string]any {
		block := map[string]any{"lane": "shots", "at": 0, "length": 24, "kind": "video", "name": "Shot",
			"request": map[string]any{"kind": "video", "prompt": prompt}, "takes": []string{}, "effects": []any{}}
		ops := []any{map[string]any{"op": "add", "mode": "insert", "block": block}}
		if revision == 1 {
			ops = append([]any{map[string]any{"op": "lane_add", "lane": map[string]any{"id": "shots", "name": "Shots", "role": "shots"}}}, ops...)
		}
		return e.call(userA, "POST", base+"/edits", map[string]any{"base_revision": revision, "ops": ops}, 200)["board"].(map[string]any)
	}
	// a block without a prompt names nothing; the first with one names it at once and asks the namer once
	if got := edit(1, " "); got["title_source"] != "default" {
		t.Fatal(got)
	}
	got := edit(2, "a red fox <image1> in the snow at dawn")
	session, _ := got["session_id"].(string)
	if got["name"] != "A red fox in the" || got["title_source"] != "prompt" || session == "" {
		t.Fatal(got)
	}
	_, sent := e.sent()
	asked := e.typed(sent, "cmd.pipeline.name")
	if len(asked) != 1 || *asked[0].JobID != session {
		t.Fatal(asked)
	}
	var cmd message.Name
	_ = json.Unmarshal(asked[0].Data, &cmd)
	if cmd.RunID != id || cmd.Prompt != "a red fox in the snow at dawn" || cmd.SessionID != session {
		t.Fatalf("%+v", cmd)
	}
	edit(3, "gulls over the sea")
	if _, sent := e.sent(); len(e.typed(sent, "cmd.pipeline.name")) != 0 {
		t.Fatal("once per board")
	}

	// the answer names the board and its session, with a session event; a rename wins over a late answer
	group := e.namer()
	e.named(group, userB, session, id, "Red fox at dawn") // another owner: dropped
	e.named(group, userA, session, id, "Red fox at dawn")
	if got := e.call(userA, "GET", base, nil, 200); got["name"] != "Red fox at dawn" || got["title_source"] != "model" ||
		got["named_by"] != "Ollama" {
		t.Fatal(got)
	}
	events := e.call(userA, "GET", "/v1/studio/sessions/"+session+"/events?since=0", nil, 200)["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["run_id"] != id {
		t.Fatal(events)
	}
	if got := e.call(userA, "PATCH", base, map[string]any{"name": "Mine"}, 200); got["title_source"] != "user" || got["named_by"] != nil {
		t.Fatal(got)
	}
	e.named(group, userA, session, id, "Something else")
	if got := e.call(userA, "GET", base, nil, 200); got["name"] != "Mine" {
		t.Fatal(got)
	}

	// a typed name is never asked about; a create from a document with a prompt is named at once
	doc := magicBoard()
	doc["blocks"].([]any)[2].(map[string]any)["request"] = map[string]any{"kind": "voice", "line": "the tide is turning, Mara"}
	if got := e.call(userA, "POST", boards, map[string]any{"name": "Typed", "doc": doc}, 201); got["name"] != "Typed" ||
		got["title_source"] != "user" {
		t.Fatal(got)
	}
	if got := e.call(userA, "POST", boards, map[string]any{"doc": doc}, 201); got["name"] != "The tide is turning, Mara" ||
		got["title_source"] != "prompt" {
		t.Fatal(got)
	}
}
