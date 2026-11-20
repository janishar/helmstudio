// Integration tests of M5b (API 1.17, contracts/README.md "Timeline"): sequences and their revisions, undo and redo,
// offline clips, isolation, caps, New shot filled by the projector, frames, re-render through the batch path, the
// export plan and export jobs with their projector, cancel and liveness.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
)

// clipTake stores a ready video take of owner (256 × 128, 24 fps, seconds long, with sound, its poster, last frame
// and stream facts as a 1.17 runner reports them; last false: none, as before 1.17) made with madeWith (nil: an
// upload), and returns its id.
func (e *env) clipTake(owner string, seconds float64, last bool, madeWith map[string]any) string {
	e.t.Helper()
	id := utility.NewID()
	prefix := "u/" + owner + "/runs/" + id + "/1/"
	for _, key := range []string{"0.mp4", "0.poster.png", "0.last.png"} {
		if err := e.store.Put(context.Background(), prefix+key, bytes.NewReader([]byte(key)), int64(len(key)), "image/png"); err != nil {
			e.t.Fatal(err)
		}
	}
	frame := func(key string) map[string]any {
		return map[string]any{"key": prefix + key, "width": 256, "height": 128, "bytes": 10}
	}
	video := map[string]any{"duration_s": seconds, "fps": 24, "frames": int(seconds * 24), "has_audio": true,
		"poster": frame("0.poster.png"), "stream": map[string]any{"container": "mp4", "codec": "h264", "profile": "High",
			"level": 21, "pix_fmt": "yuv420p", "width": 256, "height": 128, "sar": "1:1", "field_order": "progressive",
			"frame_rate": "24/1", "time_base": "1/12288", "color": map[string]any{"range": nil, "space": nil, "primaries": nil,
				"transfer": nil}, "extradata_sha256": strings.Repeat("1", 64)}}
	if last {
		video["last"] = frame("0.last.png")
	}
	source := "upload"
	if madeWith != nil {
		source = "create"
	}
	e.exec(`INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes, ready, video, made_with)
		VALUES ($1, $2, 'video', $3, 'a fox', $4, 'video/mp4', 256, 128, 400000, true, $5, $6)`, id, owner, source,
		prefix+"0.mp4", video, madeWith)
	return id
}

// mediaHost publishes the fake env with a media worker (libx264).
func (e *env) mediaHost(age time.Duration) {
	e.t.Helper()
	value, _ := json.Marshal(map[string]any{"schema_version": 1, "host": "fake", "env": "fake", "state": "idle", "run_id": nil,
		"updated_at": time.Now().Add(-age).UTC(), "published_by": "test",
		"media": map[string]any{"ffmpeg": "7.1", "encoders": []string{"libx264"}}})
	if err := e.registry.SetHost("fake.fake", value); err != nil {
		e.t.Fatal(err)
	}
}

// media projects one evt.media.fake.<kind> of jobID.
func (e *env) media(group *micro.NatsJetStreamGroup, owner, jobID, kind string, seq int, data map[string]any) {
	e.t.Helper()
	data["seq"], data["delivery"] = seq, 1
	raw, _ := json.Marshal(data)
	envelope := gen.Envelope{Type: "evt.media." + kind, Version: 1, ID: utility.NewID(), JobID: &jobID, OwnerID: owner,
		OccurredAt: time.Now().UTC(), Trace: gen.Trace{Traceparent: utility.Traceparent()}, Data: raw}
	body, _ := json.Marshal(envelope)
	if err := group.Handle(context.Background(), "evt.media.fake."+kind, body); err != nil {
		e.t.Fatal(err)
	}
}

func v1(timeline map[string]any) []any {
	return timeline["tracks"].([]any)[0].(map[string]any)["clips"].([]any)
}

func TestTimelineEditing(t *testing.T) {
	e := setup(t)
	a, b := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil)
	image, foreign := e.image(userA), e.clipTake(userB, 2, true, nil)

	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "Fox", "take_ids": []string{a, image}}, 201)
	id := tl["id"].(string)
	target := tl["target"].(map[string]any)
	if tl["revision"] != float64(1) || target["width"] != float64(256) || target["fps"] != float64(24) || len(v1(tl)) != 2 ||
		v1(tl)[1].(map[string]any)["hold"] != float64(3) || tl["frames"] != float64(48+72) || tl["can_undo"] != false {
		t.Fatal(tl)
	}
	if tl["session_id"] == nil || len(tl["takes"].(map[string]any)) != 2 {
		t.Fatal("session and takes", tl)
	}
	e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{foreign}}, 422)
	if list := e.call(userA, "GET", "/v1/studio/timelines", nil, 200)["timelines"].([]any); len(list) != 1 ||
		list[0].(map[string]any)["clips"] != float64(2) || list[0].(map[string]any)["thumb_url"] == nil {
		t.Fatal(list)
	}

	// isolation: user B sees none of it
	base := "/v1/studio/timelines/" + id
	for _, c := range []struct{ method, path string }{{"GET", ""}, {"PATCH", ""}, {"DELETE", ""}, {"POST", "/undo"},
		{"POST", "/redo"}, {"POST", "/export/plan"}, {"GET", "/exports"}} {
		var body any
		switch c.path {
		case "/undo", "/redo":
			body = map[string]any{"revision": 1}
		case "":
			if c.method == "PATCH" {
				body = map[string]any{"name": "mine"}
			}
		}
		e.call(userB, c.method, base+c.path, body, 404)
	}
	e.call(userB, "POST", base+"/clips", map[string]any{"take_ids": []string{foreign}}, 404)
	if len(e.call(userB, "GET", "/v1/studio/timelines", nil, 200)["timelines"].([]any)) != 0 {
		t.Fatal("B lists A's sequence")
	}

	// a clip naming another user's take is refused, by Add to timeline and by an edit
	e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{foreign}}, 422)
	doc := map[string]any{"revision": 1, "target": tl["target"], "tracks": tl["tracks"]}
	clips := v1(tl)
	first := clips[0].(map[string]any)
	bad := map[string]any{"id": "x1", "take_id": foreign, "in": 0, "out": 1}
	doc["tracks"] = []any{map[string]any{"kind": "video", "clips": []any{first, bad}}}
	problems := e.call(userA, "PUT", base, doc, 422)
	if p := problems["problems"].([]any)[0].(map[string]any); p["code"] != "take_not_found" || p["clip"] != "x1" {
		t.Fatal(problems)
	}

	// an edit: trim the first clip to 1 s; stale revisions are 409
	first["out"] = 1
	doc["tracks"] = []any{map[string]any{"kind": "video", "clips": clips}}
	tl = e.call(userA, "PUT", base, doc, 200)
	if tl["revision"] != float64(2) || tl["frames"] != float64(24+72) || tl["can_undo"] != true {
		t.Fatal(tl)
	}
	if stale := e.call(userA, "PUT", base, doc, 409); stale["detail"] != "timeline changed since you opened it (now revision 2)" {
		t.Fatal(stale)
	}
	tl = e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{b}}, 200)
	if tl["revision"] != float64(3) || len(v1(tl)) != 3 {
		t.Fatal(tl)
	}

	// undo walks back (two edits), redo forward; an edit after an undo clears redo
	tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": 3}, 200)
	tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": 4}, 200)
	if tl["revision"] != float64(5) || tl["frames"] != float64(48+72) || tl["can_redo"] != true || tl["can_undo"] != false {
		t.Fatal("undo twice", tl)
	}
	e.call(userA, "POST", base+"/undo", map[string]any{"revision": 5}, 409)
	tl = e.call(userA, "POST", base+"/redo", map[string]any{"revision": 5}, 200)
	if tl["frames"] != float64(24+72) || tl["can_redo"] != true {
		t.Fatal("redo", tl)
	}
	tl = e.call(userA, "POST", base+"/redo", map[string]any{"revision": 6}, 200)
	if len(v1(tl)) != 3 || tl["can_redo"] != false {
		t.Fatal("redo again", tl)
	}
	e.call(userA, "POST", base+"/redo", map[string]any{"revision": 7}, 409)

	// rename and defaults: no revision; a deleted element is left out
	element := utility.NewID()
	e.exec(`INSERT INTO library_items (id, owner_id, kind, item_group, name, handle) VALUES ($1, $2, 'element', 'character', 'Fox', 'fox')`,
		element, userA)
	tl = e.call(userA, "PATCH", base, map[string]any{"name": " Fox cut ", "defaults": map[string]any{"cast": []string{element, utility.NewID()}}}, 200)
	if tl["name"] != "Fox cut" || tl["revision"] != float64(7) || len(tl["defaults"].(map[string]any)["cast"].([]any)) != 1 {
		t.Fatal(tl)
	}

	// a deleted take stays as an offline clip and blocks the export
	e.exec(`UPDATE takes SET deleted_at = now() WHERE id = $1`, a)
	tl = e.call(userA, "GET", base, nil, 200)
	if v1(tl)[0].(map[string]any)["offline"] != true || tl["takes"].(map[string]any)[a] != nil {
		t.Fatal("offline", tl)
	}
	e.mediaHost(0)
	plan := e.call(userA, "POST", base+"/export/plan", nil, 200)
	if blocked := plan["blocked"].([]any); len(blocked) != 1 || blocked[0].(map[string]any)["code"] != "offline" {
		t.Fatal(plan)
	}
	e.call(userA, "POST", base+"/export", map[string]any{"revision": 7}, 409)

	// the last 100 revisions are kept
	tl = e.call(userA, "GET", base, nil, 200)
	for i := range 101 {
		track := tl["tracks"].([]any)[0].(map[string]any)
		track["mute"] = i%2 == 0
		tl = e.call(userA, "PUT", base, map[string]any{"revision": tl["revision"], "target": tl["target"], "tracks": tl["tracks"]}, 200)
	}
	if n := e.count(`SELECT count(*) FROM timeline_revisions WHERE timeline_id = $1`, id); n != 100 {
		t.Fatal("revisions kept", n)
	}

	e.call(userA, "DELETE", base, nil, 204)
	e.call(userA, "GET", base, nil, 404)
	if n := e.count(`SELECT count(*) FROM timeline_revisions WHERE timeline_id = $1`, id); n != 0 {
		t.Fatal("a deleted sequence keeps its revisions", n)
	}
}

func TestTimelineCap(t *testing.T) {
	e := setup(t)
	for range studio.MaxTimelines {
		e.exec(`INSERT INTO timelines (id, owner_id, name, doc) VALUES ($1, $2, 'x', '{}')`, utility.NewID(), userA)
	}
	if out := e.call(userA, "POST", "/v1/studio/timelines", nil, 409); !strings.Contains(out["detail"].(string), "100 sequences") {
		t.Fatal(out)
	}
	e.call(userB, "POST", "/v1/studio/timelines", map[string]any{}, 201)
}

func TestTimelineExport(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	studio.NewProjector(e.pool).MountMedia(group)
	a, b := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 1, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a, b}}, 201)
	base := "/v1/studio/timelines/" + tl["id"].(string)

	plan := e.call(userA, "POST", base+"/export/plan", nil, 200)
	if plan["blocked"].([]any)[0].(map[string]any)["code"] != "no_host" {
		t.Fatal("no media env", plan)
	}
	e.mediaHost(0)
	plan = e.call(userA, "POST", base+"/export/plan", map[string]any{}, 200)
	if plan["stream_copy"] != true || len(plan["blocked"].([]any)) != 0 || plan["host"] != "fake" || plan["hardware"] != false ||
		plan["frames"] != float64(72) {
		t.Fatal(plan)
	}
	e.call(userA, "POST", base+"/export", map[string]any{"revision": 1, "host": "elsewhere"}, 422)
	job := e.call(userA, "POST", base+"/export", map[string]any{"revision": 1}, 202)
	jobID := job["id"].(string)
	if job["status"] != "queued" || job["mode"] != "copy" || job["revision"] != float64(1) {
		t.Fatal(job)
	}
	e.call(userA, "POST", base+"/export", map[string]any{"revision": 1}, 409)
	e.call(userB, "GET", "/v1/studio/media-jobs/"+jobID, nil, 404)
	e.call(userB, "POST", "/v1/studio/media-jobs/"+jobID+"/cancel", nil, 404)

	subjects, sent := e.relay()
	if len(subjects) != 1 || subjects[0] != "cmd.media.fake.fake" || sent[0].Type != "cmd.media.job" {
		t.Fatal(subjects)
	}
	var command struct {
		Kind         string           `json:"kind"`
		Inputs       []map[string]any `json:"inputs"`
		OutputPrefix string           `json:"output_prefix"`
		Export       map[string]any   `json:"export"`
	}
	_ = json.Unmarshal(sent[0].Data, &command)
	if command.Kind != "export" || len(command.Inputs) != 2 || command.OutputPrefix != "u/"+userA+"/exports/"+jobID+"/" ||
		command.Export["mode"] != "copy" || command.Export["frames"] != float64(72) || command.Export["conform_args"] == nil {
		t.Fatal(string(sent[0].Data))
	}

	prefix := "u/" + userA + "/exports/" + jobID + "/"
	frame := func(name string) map[string]any {
		return map[string]any{"key": prefix + name, "width": 256, "height": 128, "bytes": 9}
	}
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "running", "encoder": "copy"})
	e.media(group, userA, jobID, "progress", 2, map[string]any{"frames": 30, "total": 72})
	if got := e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 200); got["status"] != "running" ||
		got["progress"].(map[string]any)["frames"] != float64(30) {
		t.Fatal(got)
	}
	e.media(group, userA, jobID, "status", 3, map[string]any{"status": "succeeded", "encoder": "copy", "result": map[string]any{
		"key": prefix + "export.mp4", "width": 256, "height": 128, "bytes": 700000, "mode": "copy",
		"video": map[string]any{"duration_s": 3, "fps": 24, "frames": 72, "has_audio": true,
			"poster": frame("export.poster.png"), "last": frame("export.last.png")}}})
	got := e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 200)
	if got["status"] != "succeeded" || got["take_id"] == nil {
		t.Fatal(got)
	}
	take := e.call(userA, "GET", "/v1/studio/takes/"+got["take_id"].(string), nil, 200)
	export := take["export"].(map[string]any)
	if take["source"] != "export" || take["made_with"] != nil || export["revision"] != float64(1) || export["mode"] != "copy" ||
		fmt.Sprint(take["parents"]) != fmt.Sprint([]any{a, b}) {
		t.Fatal(take)
	}
	e.call(userA, "POST", "/v1/studio/media-jobs/"+jobID+"/cancel", nil, 409)
	if exports := e.call(userA, "GET", base+"/exports", nil, 200)["jobs"].([]any); len(exports) != 1 {
		t.Fatal(exports)
	}

	// cancel asks the runner; a silent env's job fails host_lost in the sweep
	job = e.call(userA, "POST", base+"/export", map[string]any{"revision": 1}, 202)
	e.call(userA, "POST", "/v1/studio/media-jobs/"+job["id"].(string)+"/cancel", nil, 200)
	if len(e.cancelled) != 1 || e.cancelled[0] != job["id"] {
		t.Fatal(e.cancelled)
	}
	e.mediaHost(2 * time.Minute)
	var purged []string
	purge := func(_ context.Context, subject string) error { purged = append(purged, subject); return nil }
	var stopped []string
	stop := func(_ context.Context, id string) error { stopped = append(stopped, id); return nil }
	if err := studio.SweepMediaJobs(context.Background(), e.pool, e.registry, purge, stop, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got = e.call(userA, "GET", "/v1/studio/media-jobs/"+job["id"].(string), nil, 200); got["status"] == "failed" {
		t.Fatal(got) // heard within 60 s: a clean restart of its runner is no host loss
	}
	if err := studio.SweepMediaJobs(context.Background(), e.pool, e.registry, purge, stop,
		time.Now().Add(studio.StaleJobKey+time.Second)); err != nil {
		t.Fatal(err)
	}
	got = e.call(userA, "GET", "/v1/studio/media-jobs/"+job["id"].(string), nil, 200)
	if failure, _ := got["failure"].(map[string]any); got["status"] != "failed" || failure["class"] != "host_lost" ||
		failure["detail"] != "host fake stopped responding" || fmt.Sprint(purged) != "[cmd.media.fake.fake]" ||
		fmt.Sprint(stopped) != fmt.Sprint([]any{job["id"]}) { // API 1.18: host_lost; a runner that comes back never runs it
		t.Fatal(got, purged, stopped)
	}

	// at most MaxOpenMediaJobs unfinished media jobs per user (exports and frames share every env's one worker)
	e.mediaHost(0)
	for range studio.MaxOpenMediaJobs {
		e.exec(`INSERT INTO media_jobs (id, owner_id, kind, host, env) VALUES ($1, $2, 'frame', 'fake', 'fake')`, utility.NewID(), userA)
	}
	if out := e.call(userA, "POST", base+"/export", map[string]any{"revision": 1}, 409); !strings.Contains(out["detail"].(string), "unfinished") {
		t.Fatal(out)
	}
}

func TestTakeFrame(t *testing.T) {
	e := setup(t)
	group := &micro.NatsJetStreamGroup{}
	studio.NewProjector(e.pool).MountMedia(group)
	made := map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{}}
	video, old, upload := e.clipTake(userA, 2, true, made), e.clipTake(userA, 2, false, nil), e.clipTake(userA, 2, true, nil)
	first := e.call(userA, "POST", "/v1/studio/takes/"+video+"/frame", map[string]any{"at": "first"}, 200)["take"].(map[string]any)
	if first["source"] != "frame" || first["kind"] != "image" || first["frame_of"].(map[string]any)["at"] != "first" ||
		first["title"] != "a fox · first frame" || fmt.Sprint(first["parents"]) != fmt.Sprint([]any{video}) {
		t.Fatal(first)
	}
	again := e.call(userA, "POST", "/v1/studio/takes/"+video+"/frame", map[string]any{"at": "first"}, 200)["take"].(map[string]any)
	if again["id"] != first["id"] {
		t.Fatal("not idempotent")
	}
	e.call(userA, "POST", "/v1/studio/takes/"+video+"/frame", map[string]any{"at": "last"}, 200)
	e.call(userB, "POST", "/v1/studio/takes/"+video+"/frame", map[string]any{"at": "last"}, 404)
	e.call(userA, "POST", "/v1/studio/takes/"+e.image(userA)+"/frame", map[string]any{"at": "last"}, 422)

	// a take from before 1.17 has no stored last frame: a media job makes it
	e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 409) // no media env
	e.mediaHost(0)
	job := e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 202)["job"].(map[string]any)
	same := e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 202)["job"].(map[string]any)
	if job["id"] != same["id"] || job["kind"] != "frame" {
		t.Fatal(job, same)
	}
	jobID := job["id"].(string)
	e.media(group, userA, jobID, "status", 1, map[string]any{"status": "running"})
	e.media(group, userA, jobID, "status", 2, map[string]any{"status": "succeeded", "result": map[string]any{
		"key": "u/" + userA + "/frames/" + jobID + "/frame.png", "width": 256, "height": 128, "bytes": 5}})
	done := e.call(userA, "GET", "/v1/studio/media-jobs/"+jobID, nil, 200)
	stored := e.call(userA, "POST", "/v1/studio/takes/"+old+"/frame", map[string]any{"at": "last"}, 200)["take"].(map[string]any)
	if done["status"] != "succeeded" || done["take_id"] != stored["id"] {
		t.Fatal(done, stored)
	}

	// an upload's (or an export's) poster is not its frame 0: a frame job decodes frame 0
	job = e.call(userA, "POST", "/v1/studio/takes/"+upload+"/frame", map[string]any{"at": "first"}, 202)["job"].(map[string]any)
	_, sent := e.relay()
	var command struct {
		Frame map[string]any `json:"frame"`
	}
	_ = json.Unmarshal(sent[len(sent)-1].Data, &command)
	if job["kind"] != "frame" || command.Frame["at"] != "first" || command.Frame["index"] != float64(0) {
		t.Fatal(job, command)
	}
}

func TestNewShot(t *testing.T) {
	e := setup(t)
	a := e.clipTake(userA, 2, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a}}, 201)
	base := "/v1/studio/timelines/" + tl["id"].(string)
	sessionID := tl["session_id"].(string)
	run := map[string]any{"workflow_id": "echo-video", "inputs": map[string]any{"prompt.text": "a fox"}}
	if kf := e.call(userA, "GET", "/v1/studio/workflows/echo-image-to-video", nil, 200)["keyframes"].(map[string]any); kf["param"] != "keyframes.images" {
		t.Fatal(kf)
	}
	if kf := e.call(userA, "GET", "/v1/studio/workflows/echo-video", nil, 200); kf["keyframes"] != nil {
		t.Fatal("text-to-video offers no keyframes", kf["keyframes"])
	}

	e.call(userB, "POST", base+"/shots", map[string]any{"slot": map[string]any{"at": 2, "length": 1}, "run": run}, 404)
	shot := e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"at": 2, "length": 1, "id": "gap"}, "run": run}, 202)
	runID := shot["run"].(map[string]any)["id"].(string)
	slot := v1(shot["timeline"].(map[string]any))[1].(map[string]any)
	if slot["id"] != "gap" || slot["take_id"] != nil || slot["shot"].(map[string]any)["run_id"] != runID ||
		slot["shot"].(map[string]any)["status"] != "queued" {
		t.Fatal(slot)
	}
	e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"clip": "gap"}, "run": run}, 409)
	e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"clip": "nope"}, "run": run}, 422)
	revision := shot["timeline"].(map[string]any)["revision"].(float64)

	// the run's first take fills the slot (a studio revision: undo skips it)
	e.relay()
	e.emit(userA, sessionID, runID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	out := videoOutput(userA, runID)
	out["width"], out["height"] = 256, 128
	e.emit(userA, sessionID, runID, "output", 2, out)
	tl = e.call(userA, "GET", base, nil, 200)
	slot = v1(tl)[1].(map[string]any)
	if slot["take_id"] == nil || slot["shot"] != nil || tl["revision"] != revision+1 || slot["out"] != float64(1) ||
		len(slot["alternatives"].([]any)) != 1 {
		t.Fatal("filled", slot, tl["revision"])
	}
	tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": revision + 1}, 200)
	if len(v1(tl)) != 1 {
		t.Fatal("undo goes back past the fill to before the shot", tl)
	}

	// a cancelled shot is dropped
	shot = e.call(userA, "POST", base+"/shots", map[string]any{"slot": map[string]any{"clip": v1(tl)[0].(map[string]any)["id"]}, "run": run}, 202)
	e.call(userA, "POST", "/v1/studio/runs/"+shot["run"].(map[string]any)["id"].(string)+"/cancel", nil, 202)
	tl = e.call(userA, "GET", base, nil, 200)
	if v1(tl)[0].(map[string]any)["shot"] != nil {
		t.Fatal("cancelled shot kept", tl)
	}
}

func TestRerender(t *testing.T) {
	e := setup(t)
	made := map[string]any{"workflow_id": "echo-video", "workflow_title": "Echo video", "inputs": map[string]any{"prompt.text": "a fox"}, "seed": 7}
	a, upload := e.clipTake(userA, 2, true, made), e.clipTake(userA, 2, true, nil)
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"name": "Fox", "take_ids": []string{a, upload}}, 201)
	base := "/v1/studio/timelines/" + tl["id"].(string)
	clips := v1(tl)
	first := clips[0].(map[string]any)["id"].(string)

	if out := e.call(userA, "POST", base+"/rerender", map[string]any{"revision": 1}, 422); !strings.Contains(out["detail"].(string), "not made by a workflow") {
		t.Fatal(out)
	}
	e.call(userA, "POST", base+"/rerender", map[string]any{"revision": 2, "clips": []string{first}}, 409)
	e.call(userA, "POST", base+"/rerender", map[string]any{"revision": 1, "clips": []string{first}, "preset": "final"}, 422)
	out := e.call(userA, "POST", base+"/rerender", map[string]any{"revision": 1, "clips": []string{first}}, 202)
	batch := out["batch"].(map[string]any)
	if batch["timeline_id"] != tl["id"] || batch["pipeline_title"] != "Re-render · Fox" || batch["pipeline_id"] != nil {
		t.Fatal(batch)
	}
	shot := v1(out["timeline"].(map[string]any))[0].(map[string]any)["shot"].(map[string]any)
	if shot["batch_id"] != batch["id"] || shot["step"] != "clip_"+first || v1(out["timeline"].(map[string]any))[1].(map[string]any)["shot"] != nil {
		t.Fatal(shot)
	}
	subjects, sent := e.relay()
	if len(subjects) != 1 || subjects[0] != "cmd.pipeline.submit" {
		t.Fatal(subjects)
	}
	var submit struct {
		Snapshot struct {
			Pipeline struct {
				ID    string                    `json:"id"`
				Nodes map[string]map[string]any `json:"nodes"`
			} `json:"pipeline"`
		} `json:"snapshot"`
	}
	_ = json.Unmarshal(sent[0].Data, &submit)
	step := submit.Snapshot.Pipeline.Nodes["clip_"+first]
	if submit.Snapshot.Pipeline.ID != tl["id"] || step["workflow"] != "echo-video" ||
		submit.Snapshot.Pipeline.Nodes["out"]["from"] != "clip_"+first+".takes" || // a pipelineSource, never a bare node id
		step["inputs"].(map[string]any)["prompt.text"] != "a fox" {
		t.Fatal(string(sent[0].Data))
	}
	e.call(userB, "POST", base+"/rerender", map[string]any{"revision": 2}, 404)

	// pipelines asks for the step's run; its take fills the slot and becomes its current alternative
	created, refused := e.service.CreateBatchRun(context.Background(), &message.RunsCreate{OwnerID: userA,
		SessionID: batch["session_id"].(string), WorkflowID: "echo-video", Inputs: step["inputs"].(map[string]any), Lane: "batch",
		BatchID: batch["id"].(string), Row: 1, IdempotencyKey: fmt.Sprintf("%s:1:1:clip_%s:0:0", batch["id"], first)})
	if refused != nil {
		t.Fatal(refused)
	}
	e.relay()
	e.emit(userA, batch["session_id"].(string), created.RunID, "status", 1, map[string]any{"status": "running", "host": "fake"})
	e.emit(userA, batch["session_id"].(string), created.RunID, "output", 2, videoOutput(userA, created.RunID))
	clip := v1(e.call(userA, "GET", base, nil, 200))[0].(map[string]any)
	if clip["shot"] != nil || clip["take_id"] == a || len(clip["alternatives"].([]any)) != 2 {
		t.Fatal("re-render fill", clip)
	}
	// the take names the re-render as its pipeline: the timeline, its revision and the step (README, Re-render)
	madeWith := e.call(userA, "GET", "/v1/studio/takes/"+clip["take_id"].(string), nil, 200)["made_with"].(map[string]any)
	if p, _ := madeWith["pipeline"].(map[string]any); p == nil || p["id"] != tl["id"] || p["node"] != "clip_"+first || p["version"] != float64(1) {
		t.Fatal("re-render take's made_with.pipeline", madeWith["pipeline"])
	}

	// a step that never ran (a failed step before it ends the row) leaves its slot when the batch ends
	tl = e.call(userA, "GET", base, nil, 200)
	out = e.call(userA, "POST", base+"/rerender", map[string]any{"revision": tl["revision"], "clips": []string{first}}, 202)
	again := out["batch"].(map[string]any)["id"].(string)
	if v1(e.call(userA, "GET", base, nil, 200))[0].(map[string]any)["shot"] == nil {
		t.Fatal("no shot")
	}
	e.pipeline(userA, again, "batch", map[string]any{"seq": 1, "status": "done"})
	if clip := v1(e.call(userA, "GET", base, nil, 200))[0].(map[string]any); clip["shot"] != nil {
		t.Fatal("a step that never ran keeps its slot locked", clip)
	}
}

// TestShotNumbers: a V1 clip's number (API 1.29) is given when it is added and kept when it moves, whatever a write
// says; a removed clip brought back by undo keeps its own; a split's right part gets the next.
func TestShotNumbers(t *testing.T) {
	e := setup(t)
	a, b, c := e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil), e.clipTake(userA, 2, true, nil)
	nums := func(tl map[string]any) (out []any) {
		for _, clip := range v1(tl) {
			out = append(out, clip.(map[string]any)["number"])
		}
		return out
	}
	want := func(tl map[string]any, n ...float64) {
		t.Helper()
		if got := nums(tl); fmt.Sprint(got) != fmt.Sprint(n) {
			t.Fatal("numbers", got, "want", n)
		}
	}
	tl := e.call(userA, "POST", "/v1/studio/timelines", map[string]any{"take_ids": []string{a, b}}, 201)
	want(tl, 1, 2)
	base := "/v1/studio/timelines/" + tl["id"].(string)
	tl = e.call(userA, "POST", base+"/clips", map[string]any{"take_ids": []string{c}}, 200)
	want(tl, 1, 2, 3)

	// move the third clip first and try to renumber it: it keeps 3
	clips := v1(tl)
	clips[2].(map[string]any)["number"] = 1
	put := func(clips []any) map[string]any {
		return e.call(userA, "PUT", base, map[string]any{"revision": tl["revision"], "target": tl["target"],
			"tracks": []any{map[string]any{"kind": "video", "clips": clips}}}, 200)
	}
	tl = put([]any{clips[2], clips[0], clips[1]})
	want(tl, 3, 1, 2)

	// remove shot 2, then undo: it comes back as 2; a new clip carrying 2 meanwhile gets the next
	clips = v1(tl)
	tl = put([]any{clips[0], clips[1]})
	want(tl, 3, 1)
	tl = e.call(userA, "POST", base+"/undo", map[string]any{"revision": tl["revision"]}, 200)
	want(tl, 3, 1, 2)
	clips = v1(tl)
	split := map[string]any{"id": "s1", "take_id": a, "number": 2}
	tl = put([]any{clips[0], clips[1], clips[2], split})
	want(tl, 3, 1, 2, 4)
}
