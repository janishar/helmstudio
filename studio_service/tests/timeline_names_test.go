// Integration tests of alpha.5 items 12 and 14 (API 1.76; contracts/README.md "Timeline", Clone and Names): a clone
// of a sequence (new clip ids, the same takes, revision 1, 404 for another user's), and an untitled sequence named
// once from its first take's prompt, then by the namer through its session (a rename wins).
package tests

import (
	"encoding/json"
	"testing"

	"github.com/helmstudio/studio_service/api/studio/message"
)

func TestTimelineClone(t *testing.T) {
	e := setup(t)
	a, b := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 1, true, nil)
	src := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "The Last Tide", "take_ids": []string{a, b}}, 201)
	id := src["id"].(string)
	clone := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"from_timeline_id": id}, 201)
	if clone["name"] != "The Last Tide clone" || clone["title_source"] != "user" || clone["revision"] != float64(1) ||
		clone["id"] == id || clone["session_id"] == src["session_id"] || clone["duration_s"] != src["duration_s"] {
		t.Fatal(clone)
	}
	from, to := v1(src), v1(clone)
	if len(to) != 2 {
		t.Fatal(to)
	}
	for i := range from {
		f, c := from[i].(map[string]any), to[i].(map[string]any)
		if f["id"] == c["id"] || f["take_id"] != c["take_id"] || f["out"] != c["out"] {
			t.Fatal(f, c)
		}
	}
	if got := e.call(userA, "GET", "/v1/studio/timelines/"+id, nil, 200); got["revision"] != src["revision"] {
		t.Fatal("the original is unchanged", got)
	}
	e.call(userB, "POST", "/v1/studio/timelines", map[string]any{"from_timeline_id": id}, 404)
	if got := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"from_timeline_id": id, "take_ids": []string{a}}, 422); got["detail"] != "from_timeline_id: target and take_ids are not allowed with it" {
		t.Fatal(got)
	}
}

func TestTimelineNames(t *testing.T) {
	e := setup(t)
	e.service.SetRemote(true)
	e.call(admin, "PUT", "/v1/studio/admin/providers/ollama", providerServer, 201)
	e.call(admin, "PUT", "/v1/studio/admin/session-names", map[string]any{"mode": "remote", "provider": "ollama"}, 200)
	session := e.call(userA, "POST", "/v1/studio/sessions", map[string]any{"title": "Shots"}, 201)["id"].(string)
	run := e.call(userA, "POST", "/v1/studio/sessions/"+session+"/runs", map[string]any{"workflow_id": "echo",
		"inputs": map[string]any{"prompt.text": "a red fox in the snow at dawn"}}, 202)["id"].(string)
	upload, made := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil)
	e.exec(`UPDATE takes SET run_id = $1 WHERE id = $2`, run, made)
	e.sent()

	tl := e.call(userA, "POST", "/v1/studio/timelines", nil, 201)
	base, tlSession := "/v1/studio/timelines/"+tl["id"].(string), tl["session_id"].(string)
	if tl["name"] != "Untitled sequence" || tl["title_source"] != "default" || tl["named_by"] != nil {
		t.Fatal(tl)
	}
	// an upload has no prompt: still untitled
	if got := e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{upload}}, 200); got["title_source"] != "default" {
		t.Fatal(got)
	}
	got := e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{made}}, 200)
	if got["name"] != "A red fox in the" || got["title_source"] != "prompt" {
		t.Fatal(got)
	}
	_, sent := e.sent()
	asked := e.typed(sent, "cmd.pipeline.name")
	if len(asked) != 1 || *asked[0].JobID != tlSession {
		t.Fatal(asked)
	}
	var cmd message.Name
	_ = json.Unmarshal(asked[0].Data, &cmd)
	if cmd.RunID != run || cmd.Prompt != "a red fox in the snow at dawn" || cmd.SessionID != tlSession {
		t.Fatalf("%+v", cmd)
	}
	e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{made}}, 200)
	if _, sent := e.sent(); len(e.typed(sent, "cmd.pipeline.name")) != 0 {
		t.Fatal("once per sequence")
	}

	// the answer names the sequence and its session, with one session event each time
	group := e.namer()
	e.named(group, userB, tlSession, run, "Red fox at dawn") // another owner: dropped
	e.named(group, userA, tlSession, run, "Red fox at dawn")
	got = e.call(userA, "GET", base, nil, 200)
	if got["name"] != "Red fox at dawn" || got["title_source"] != "model" || got["named_by"] != "Ollama" {
		t.Fatal(got)
	}
	if s := e.call(userA, "GET", "/v1/studio/sessions/"+tlSession, nil, 200); s["title"] != "Red fox at dawn" {
		t.Fatal(s)
	}
	var sources []string
	for _, item := range e.call(userA, "GET", "/v1/studio/sessions/"+tlSession+"/events?since=0", nil, 200)["events"].([]any) {
		if ev := item.(map[string]any); ev["type"] == "session" {
			sources = append(sources, ev["data"].(map[string]any)["title_source"].(string))
		}
	}
	if len(sources) != 2 || sources[0] != "prompt" || sources[1] != "model" {
		t.Fatal(sources)
	}
	// a rename is the user's: a late answer never replaces it
	if got := e.call(userA, "PATCH", base, map[string]any{"name": "Mine"}, 200); got["title_source"] != "user" || got["named_by"] != nil {
		t.Fatal(got)
	}
	e.named(group, userA, tlSession, run, "Something else")
	if got := e.call(userA, "GET", base, nil, 200); got["name"] != "Mine" {
		t.Fatal(got)
	}
	// a typed name is never asked about; a create with a prompted take is named at once
	if got := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "Typed", "take_ids": []string{made}}, 201); got["name"] != "Typed" || got["title_source"] != "user" {
		t.Fatal(got)
	}
	if got := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{made}}, 201); got["name"] != "A red fox in the" {
		t.Fatal(got)
	}
}

// A New shot's fill names an untitled sequence from the shot's prompt (the projector).
func TestTimelineNamedByItsShot(t *testing.T) {
	e := setup(t)
	group := e.namer()
	tl := e.call(userA, "POST", "/v1/studio/timelines", nil, 201)
	base, sessionID := "/v1/studio/timelines/"+tl["id"].(string), tl["session_id"].(string)
	shot := e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"at": 0, "length": 1},
		"run": map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{"prompt.text": "old fisherman on a pier, at dawn"}}}, 202)
	runID := shot["run"].(map[string]any)["id"].(string)
	if shot["timeline"].(map[string]any)["title_source"] != "default" {
		t.Fatal("a pending shot has no take yet")
	}
	e.relay()
	e.emitTo(group, userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := videoOutput(userA, runID)
	out["width"], out["height"] = 256, 128
	e.emitTo(group, userA, sessionID, runID, "output", 2, out)
	if got := e.call(userA, "GET", base, nil, 200); got["name"] != "Old fisherman on a pier" || got["title_source"] != "prompt" {
		t.Fatal(got)
	}
}
