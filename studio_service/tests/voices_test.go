// Integration tests of voices (contracts/README.md "Voices", API 1.30): designed voices in the library, starter voices
// from the live registry (read-only), fit, runs with a voice (the command's defs voice, made_with.voices, the audio
// take) and the mention rule of an audio workflow.
package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/helmstudio/studio_service/api/gallery"
)

// voices publishes the fake runner's starter voices (echo/ada, echo/bo).
func (e *env) voices() {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "voices.valid.json"))
	if err == nil {
		err = e.registry.SetRegistry("fake.fake.voices", data)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestVoicesLibrary(t *testing.T) {
	e := setup(t)
	e.voices()
	e.looks()
	designed := func(extra map[string]any) map[string]any {
		return with(map[string]any{"kind": "voice", "group": "designed", "name": "Teen",
			"voice": map[string]any{"design": " male, 17, tenor, nervous ", "language": "en"}}, extra)
	}
	teen := e.call(userA, "POST", library, designed(nil), 201)
	id := teen["id"].(string)
	if v, _ := json.Marshal(teen["voice"]); string(v) != `{"design":"male, 17, tenor, nervous","language":"en","source":"designed"}` ||
		teen["kind"] != "voice" || teen["handle"] != nil || teen["text"] != "" || teen["thumb_url"] != nil ||
		len(teen["needs"].([]any)) != 0 || teen["source"] != "mine" || teen["family"] != nil {
		t.Fatal(teen)
	}
	for want, b := range map[string]map[string]any{
		"preset voices come from packs":                    designed(map[string]any{"group": "preset"}),
		"voice.design: a cloned voice has no design":       designed(map[string]any{"group": "cloned"}),
		"a voice has no text; describe it in voice.design": designed(map[string]any{"text": "hi"}),
		"voice: a designed voice needs voice.design":       designed(map[string]any{"voice": map[string]any{}}),
		"unknown field speaker":                            designed(map[string]any{"voice": map[string]any{"design": "x", "speaker": "Ryan"}}),
		"voice: only a voice has voice fields":             designed(map[string]any{"kind": "look", "group": "film", "text": "x"}),
	} {
		if got := e.call(userA, "POST", library, b, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}

	// PUT: checked like POST, a stale version is 409; DELETE is never refused; another user's is 404
	put := map[string]any{"group": "designed", "name": "Teen", "text": "", "images": []any{},
		"voice": map[string]any{"design": "male, 17, very nervous", "style": "whispering"}, "version": 1}
	if got := e.call(userA, "PUT", library+"/"+id, put, 200); got["version"] != 2.0 ||
		got["voice"].(map[string]any)["style"] != "whispering" || got["voice"].(map[string]any)["language"] != nil {
		t.Fatal(got)
	}
	if got := e.call(userA, "PUT", library+"/"+id, put, 409); got["detail"] != "voice changed since you opened it (now version 2)" {
		t.Fatal(got)
	}
	e.call(userB, "GET", library+"/"+id, nil, 404)

	// the list: own first, then starter voices and looks by name; kind and group filter; fit against a workflow
	all := e.call(userA, "GET", library, nil, 200)
	if got := ids(all); !slices.Equal(got, []string{id, "echo/ada", "echo/bo", "echo/grain", "echo/soft-light"}) {
		t.Fatal(got)
	}
	if got := ids(e.call(userA, "GET", library+"?kind=voice&group=preset", nil, 200)); !slices.Equal(got, []string{"echo/ada", "echo/bo"}) {
		t.Fatal(got)
	}
	fit := e.call(userA, "GET", library+"?workflow=echo-voice-over", nil, 200)["items"].([]any)
	reasons := map[string]any{}
	for _, raw := range fit {
		item := raw.(map[string]any)
		reasons[item["id"].(string)] = item["reason"]
	}
	if reasons[id] != nil || reasons["echo/ada"] != nil || reasons["echo/soft-light"] != "looks do not apply to speech" {
		t.Fatal(reasons)
	}
	if got := e.call(userA, "GET", library+"?kind=voice&workflow=echo", nil, 200)["items"].([]any)[0].(map[string]any); got["reason"] != "this workflow takes no voice" {
		t.Fatal(got)
	}

	// a starter voice: served as a library item, read-only
	ada := e.call(userA, "GET", library+"/echo/ada", nil, 200)
	if v, _ := json.Marshal(ada["voice"]); string(v) != `{"family":"echo","language":"en","source":"preset","speaker":"ada"}` ||
		ada["group"] != "preset" || ada["source"] != "starter" || ada["family"] != "echo" {
		t.Fatal(ada)
	}
	if got := e.call(userA, "PUT", library+"/echo/ada", put, 409); got["detail"] != "starter voices are read-only" {
		t.Fatal(got)
	}
	e.call(userA, "DELETE", library+"/echo/ada", nil, 409)
	e.call(userA, "DELETE", library+"/"+id, nil, 204)
}

func TestRunWithVoice(t *testing.T) {
	e := setup(t)
	e.voices()
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	run := func(voice any, text string, want int) map[string]any {
		return e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo-voice-over",
			"inputs": map[string]any{"prompt.text": text, "speak.voice": voice}}, want)
	}
	theirs := e.call(userB, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "B",
		"voice": map[string]any{"design": "low"}}, 201)["id"].(string)
	for want, voice := range map[string]any{
		"speak.voice: a voice is required":                                nil,
		`speak.voice: must be {"voice_id": <id>}`:                         map[string]any{"voice_id": "Ada"},
		"speak.voice: voice echo/zed not found":                           map[string]any{"voice_id": "echo/zed"},
		"speak.voice: voice " + theirs + " not found":                     map[string]any{"voice_id": theirs},
		"prompt.text: mentions are not read aloud; use a dialogue script": nil,
	} {
		text := "hi"
		if voice == nil && want[0] == 'p' {
			text, voice = "hi @mara", map[string]any{"voice_id": "echo/ada"}
		}
		if got := run(voice, text, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}
	if got := e.call(userA, "GET", "/v1/studio/workflows?media=audio", nil, 200)["workflows"].([]any); len(got) != 1 ||
		got[0].(map[string]any)["id"] != "echo-voice-over" {
		t.Fatal(got)
	}

	// the command carries defs voice; the prompt reaches the runner as typed; the take is audio with made_with.voices
	runID := run(map[string]any{"voice_id": "echo/ada"}, "mail a@b.c @@home", 202)["id"].(string)
	nodes := e.command()
	if v, _ := json.Marshal(nodes["speak"].Params["voice"]); string(v) != `{"family":"echo","item_id":"echo/ada","language":"en","source":"preset","speaker":"ada","version":1}` ||
		nodes["prompt"].Params["text"] != "mail a@b.c @@home" {
		t.Fatal(nodes)
	}
	if ref, _ := json.Marshal(e.stored(runID)["speak"].Params["voice"]); string(ref) != `{"voice_id":"echo/ada"}` {
		t.Fatal(string(ref))
	}
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": "u/" + userA + "/runs/" + runID + "/1/0.wav",
		"kind": "audio", "bytes": 96044, "audio": map[string]any{"duration_s": 2.0, "sample_rate": 24000}})
	output := e.call(userA, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)
	take := e.call(userA, "GET", "/v1/studio/takes/"+output["take_id"].(string), nil, 200)
	made := take["made_with"].(map[string]any)
	voices, _ := json.Marshal(made["voices"])
	audio, _ := json.Marshal(take["audio"])
	if take["kind"] != "audio" || take["content_type"] != "audio/wav" || take["width"] != nil || take["thumb_url"] != take["url"] ||
		string(audio) != `{"duration_s":2,"sample_rate":24000}` || output["audio"] == nil ||
		string(voices) != `[{"id":"echo/ada","name":"Ada","source":"preset","version":1}]` {
		t.Fatal(take, output)
	}
	if ref, _ := json.Marshal(made["inputs"].(map[string]any)["speak.voice"]); string(ref) != `{"voice_id":"echo/ada"}` {
		t.Fatal(string(ref))
	}

	e.emit(userA, sessionID, runID, "status", 3, map[string]any{"status": "succeeded", "host": "fake"})

	// an own designed voice sends its design
	mine := e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Low",
		"voice": map[string]any{"design": "low and slow", "style": "calm"}}, 201)["id"].(string)
	run(map[string]any{"voice_id": mine}, "hello", 202)
	if v, _ := json.Marshal(e.command()["speak"].Params["voice"]); string(v) != `{"item_id":"`+mine+`","source":"design","style":"calm","text":"low and slow","version":1}` {
		t.Fatal(string(v))
	}
}

// voiceTake ends a run of echo-voice-over with one WAV output of seconds (its object put in the store) and returns
// its take.
func (e *env) voiceTake(owner, sessionID, runID string, seconds float64) string {
	e.t.Helper()
	key, data := "u/"+owner+"/runs/"+runID+"/1/0.wav", []byte("RIFF wav of "+runID)
	if err := e.store.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), "audio/wav"); err != nil {
		e.t.Fatal(err)
	}
	e.emit(owner, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(owner, sessionID, runID, "output", 2, map[string]any{"index": 0, "key": key, "kind": "audio",
		"bytes": len(data), "audio": map[string]any{"duration_s": seconds, "sample_rate": 24000}})
	e.emit(owner, sessionID, runID, "status", 3, map[string]any{"status": "succeeded", "host": "fake"})
	return e.call(owner, "GET", "/v1/studio/runs/"+runID, nil, 200)["outputs"].([]any)[0].(map[string]any)["take_id"].(string)
}

// API 1.31 (README, Voices: cloned voices): a voice from a clip of a take, Save as voice, runs that send the clip
// (also after its take was deleted), lineage, and the sweep keeping the take's object while the voice keeps it.
func TestClonedVoices(t *testing.T) {
	e := setup(t)
	e.voices()
	sessionID := e.call(userA, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)
	runs := "/v1/studio/sessions/" + sessionID + "/runs"
	run := func(voice string, want int) map[string]any {
		return e.call(userA, "POST", runs, map[string]any{"workflow_id": "echo-voice-over",
			"inputs": map[string]any{"prompt.text": "hello there", "speak.voice": map[string]any{"voice_id": voice}}}, want)
	}
	runID := run("echo/ada", 202)["id"].(string)
	e.relay()
	takeID := e.voiceTake(userA, sessionID, runID, 5.0)
	key := "u/" + userA + "/runs/" + runID + "/1/0.wav"
	cloned := func(clip map[string]any, consent any) map[string]any {
		v := map[string]any{"clip": clip}
		if consent != nil {
			v["consent"] = consent
		}
		return map[string]any{"kind": "voice", "group": "cloned", "name": "Jon", "voice": v}
	}
	for want, b := range map[string]map[string]any{
		`voice.consent: tick "I have the right to use this voice"`:          cloned(map[string]any{"take_id": takeID}, false),
		"voice.clip: the part lasts 2.0 s; this input takes at least 3.0 s": cloned(map[string]any{"take_id": takeID, "in": 1, "out": 3}, true),
		"voice.clip: out 6.0 is past the take's end 5.0":                    cloned(map[string]any{"take_id": takeID, "out": 6}, true),
		"voice: a cloned voice needs voice.clip":                            {"kind": "voice", "group": "cloned", "name": "Jon", "voice": map[string]any{"consent": true}},
		"voice.clip: give the clip or from_take, not both":                  with(cloned(map[string]any{"take_id": takeID}, true), map[string]any{"from_take": map[string]any{"take_id": takeID}}),
		"from_take: a look takes the whole take (no in or out)":             {"kind": "look", "group": "film", "name": "L", "from_take": map[string]any{"take_id": takeID, "in": 1}},
		"from_take: take " + takeID + " was not made with a designed voice": {"kind": "voice", "group": "designed", "name": "D", "voice": map[string]any{"consent": true}, "from_take": map[string]any{"take_id": takeID}},
	} {
		if got := e.call(userA, "POST", library, b, 422); got["detail"] != want {
			t.Errorf("%v, want %s", got["detail"], want)
		}
	}
	// another user's take is not found
	if got := e.call(userB, "POST", library, cloned(map[string]any{"take_id": takeID}, true), 404); got["detail"] != "voice.clip: take "+takeID+" not found" {
		t.Fatal(got)
	}
	jon := e.call(userA, "POST", library, cloned(map[string]any{"take_id": takeID, "in": 0.5}, true), 201)
	voiceID := jon["id"].(string)
	if v, _ := json.Marshal(jon["voice"]); string(v) != `{"clip":{"in":0.5,"out":5,"take_id":"`+takeID+`"},"consent":true,"source":"cloned"}` ||
		jon["from_take_id"] != takeID || jon["thumb_url"] == nil {
		t.Fatal(jon)
	}
	e.call(userB, "GET", library+"/"+voiceID, nil, 404)
	if got := e.call(userB, "POST", runs[:0]+"/v1/studio/sessions/"+e.call(userB, "POST", "/v1/studio/sessions", nil, 201)["id"].(string)+"/runs",
		map[string]any{"workflow_id": "echo-voice-over", "inputs": map[string]any{"prompt.text": "hi",
			"speak.voice": map[string]any{"voice_id": voiceID}}}, 422); got["detail"] != "speak.voice: voice "+voiceID+" not found" {
		t.Fatal(got)
	}

	// a run sends the clip as a media value; the take's lineage adds the clip's take
	clipped := run(voiceID, 202)["id"].(string)
	speak := e.command()["speak"].Params["voice"].(map[string]any)
	clip, _ := speak["clip"].(map[string]any)
	if speak["source"] != "clip" || speak["text"] != nil || clip["take_id"] != takeID || clip["key"] != key ||
		clip["in"] != 0.5 || clip["out"] != 5.0 || clip["etag"] == "" || clip["kind"] != "audio" {
		t.Fatal(speak)
	}
	made := e.voiceTake(userA, sessionID, clipped, 3.2)
	if parents, _ := json.Marshal(e.call(userA, "GET", "/v1/studio/takes/"+made, nil, 200)["parents"]); !bytes.Contains(parents, []byte(takeID)) {
		t.Fatal(string(parents))
	}

	// the take deleted: hidden, from_take_id null, but the voice still plays and runs; the sweep keeps its object
	e.call(userA, "DELETE", "/v1/studio/takes/"+takeID, nil, 204)
	if got := e.call(userA, "GET", library+"/"+voiceID, nil, 200); got["from_take_id"] != nil || got["thumb_url"] == nil {
		t.Fatal(got)
	}
	again := run(voiceID, 202)["id"].(string)
	if e.command()["speak"].Params["voice"].(map[string]any)["clip"].(map[string]any)["take_id"] != takeID {
		t.Fatal("the deleted take's clip is not sent")
	}
	e.voiceTake(userA, sessionID, again, 3.0)
	if got := e.call(userA, "POST", library, cloned(map[string]any{"take_id": takeID}, true), 404); got["detail"] != "voice.clip: take "+takeID+" not found" {
		t.Fatal(got)
	}
	// a PUT keeping its clip is fine, though the take is deleted
	put := map[string]any{"group": "cloned", "name": "Jon 2", "text": "", "images": []any{}, "version": 1,
		"voice": map[string]any{"clip": map[string]any{"take_id": takeID, "in": 0.5}, "consent": true}}
	if got := e.call(userA, "PUT", library+"/"+voiceID, put, 200); got["name"] != "Jon 2" {
		t.Fatal(got)
	}
	e.exec(`UPDATE takes SET deleted_at = now() - interval '2 hours' WHERE id = $1`, takeID)
	sweeper := gallery.NewSweeper(e.pool, e.store)
	if n, err := sweeper.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// its object gone: the run is refused
	data, _ := e.store.Get(context.Background(), key)
	body, _ := io.ReadAll(data)
	_ = e.store.Delete(context.Background(), key)
	if got := run(voiceID, 422); got["detail"] != "speak.voice: the clip of voice Jon 2 is gone" {
		t.Fatal(got)
	}
	_ = e.store.Put(context.Background(), key, bytes.NewReader(body), int64(len(body)), "audio/wav")
	// deleting the voice frees the take for the next sweep
	e.call(userA, "DELETE", library+"/"+voiceID, nil, 204)
	if n, err := sweeper.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := e.store.Stat(context.Background(), key); err == nil {
		t.Fatal("the clip's object is left behind")
	}

	// Save as voice from a take a designed voice made: the design copied, the take its clip; echo/speak takes it by
	// its design first
	teen := e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Teen",
		"voice": map[string]any{"design": "male, 17", "language": "en"}}, 201)["id"].(string)
	designRun := run(teen, 202)["id"].(string)
	e.relay()
	designTake := e.voiceTake(userA, sessionID, designRun, 4.0)
	saved := e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Teen 2",
		"voice": map[string]any{"consent": true}, "from_take": map[string]any{"take_id": designTake, "out": 3.5}}, 201)
	if v, _ := json.Marshal(saved["voice"]); string(v) != `{"clip":{"in":0,"out":3.5,"take_id":"`+designTake+`"},"consent":true,"design":"male, 17","language":"en","source":"designed"}` ||
		saved["from_take_id"] != designTake {
		t.Fatal(saved)
	}
	run(saved["id"].(string), 202)
	if v := e.command()["speak"].Params["voice"].(map[string]any); v["source"] != "design" || v["clip"] != nil || v["text"] != "male, 17" {
		t.Fatal(v)
	}
	e.call(userA, "DELETE", library+"/"+teen, nil, 204)
	if got := e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Teen 3",
		"voice": map[string]any{"consent": true}, "from_take": map[string]any{"take_id": designTake}}, 422); got["detail"] != "from_take: voice Teen was deleted" {
		t.Fatal(got)
	}
}

// Migration 00024 down deletes the saved voices before it drops their voice column (row-level security forced).
func TestVoicesMigrationDown(t *testing.T) {
	e := setup(t)
	e.call(userA, "POST", library, map[string]any{"kind": "voice", "group": "designed", "name": "Teen",
		"voice": map[string]any{"design": "male, 17", "language": "en"}}, 201)
	sqldb, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	count := func() int { // as system: the table forces row-level security
		var n int
		err := sqldb.QueryRow(`SELECT count(*) FROM library_items`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	sqldb.SetMaxOpenConns(1)
	if _, err = sqldb.Exec(`SELECT set_config('app.role', 'system', false)`); err != nil || count() != 1 {
		t.Fatal(err)
	}
	if err = goose.DownTo(sqldb, ".", 23); err != nil {
		t.Fatal(err)
	}
	if _, err = sqldb.Exec(`SELECT set_config('app.role', 'system', false)`); err != nil || count() != 0 {
		t.Fatal(err)
	}
}
