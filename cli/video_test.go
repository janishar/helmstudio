package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const videoWorkflow = `{"id":"echo-video","title":"Echo video","family":"echo","env":"fake","media":"video",
 "nodes":{"prompt":{"kind":"prompt"},"duration":{"kind":"echo/duration"},"video":{"kind":"echo/video"},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text"}]},
  "echo/duration":{"params":[{"name":"frames","type":"frames","min":9,"max":121,"step":8,"fps":"fps"},{"name":"fps","type":"int"}]},
  "echo/video":{"params":[{"name":"steps","type":"int"}]},"output":{"params":[]}}}`

const videoTakeJSON = `{"id":"t2","kind":"video","source":"create","title":"a fox","width":256,"height":128,"created_at":"2026-11-14T11:00:00Z","starred":false,
 "video":{"duration_s":2.125,"fps":8,"frames":17,"has_audio":true,"poster_url":"/files/u/o1/runs/r1/0.poster.png?exp=1&sig=ok"}}`

var mp4 = []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomavc1")

func TestRunVideoSavesTheMP4(t *testing.T) {
	f, _ := setup(t, "succeeded")
	dir := t.TempDir()
	var out, errb bytes.Buffer
	args := []string{"run", "echo-video", "-p", "duration.frames=17", "-p", "duration.fps=8", "-o", dir}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s\n%s", code, errb.String(), out.String())
	}
	if f.workflowID != "echo-video" || f.inputs["duration.frames"] != 17.0 || f.inputs["duration.fps"] != 8.0 {
		t.Fatalf("workflow %q inputs %v", f.workflowID, f.inputs)
	}
	dest := filepath.Join(dir, "r1_0.mp4")
	if b, err := os.ReadFile(dest); err != nil || !bytes.Equal(b, mp4) {
		t.Fatalf("output file: %v", err)
	}
	if !strings.Contains(out.String(), "take t9\n"+dest+"\n") {
		t.Fatalf("output lines:\n%s", out.String())
	}
	errb.Reset()
	if code := helm([]string{"run", "echo-video", "-p", "duration.frames=2.5", "-o", dir}, nil, &out, &errb, nil); code != 1 ||
		!strings.Contains(errb.String(), "duration.frames: not a valid frames") {
		t.Fatalf("bad frames: %d %q", code, errb.String())
	}
}

const i2vWorkflow = `{"id":"echo-image-to-video","title":"Echo image to video","family":"echo","env":"fake","media":"video",
 "nodes":{"prompt":{"kind":"prompt"},"duration":{"kind":"echo/duration","params":{"frames":25,"fps":24}},
  "keyframes":{"kind":"echo/keyframes","params":{"images":[]}},"sample":{"kind":"echo/sample"},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text"}]},
  "echo/duration":{"params":[{"name":"frames","type":"frames","min":9,"max":121,"step":8,"fps":"fps","auto":{"min_s":1,"max_s":5}},
   {"name":"fps","type":"int","default":24}]},
  "echo/keyframes":{"params":[{"name":"images","type":"rows","min":1,"max":8,"default":[],"item":{"image":"Image",
   "seconds":{"type":"float","default":0,"position":"seconds"},"strength":{"type":"float","default":1}}}]},
  "echo/sample":{"params":[{"name":"steps","type":"int","values":[2,4]}]},"output":{"params":[]}}}`

func TestRunFirstFrameKeyframesAutoLength(t *testing.T) {
	f, _ := setup(t, "succeeded")
	file := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(file, png, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	args := []string{"run", "echo-image-to-video", "--image", file, "--keyframe", "16:t7", "--keyframe", "8:@" + file,
		"--duration", "auto", "-p", "duration.fps=8", "-o", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s\n%s", code, errb.String(), out.String())
	}
	// the file is uploaded per flag; frames become seconds at -p's 8 fps
	want := `[map[image:map[take_id:t1] seconds:0] map[image:map[take_id:t7] seconds:2] map[image:map[take_id:t1] seconds:1]]`
	if got := fmt.Sprint(f.inputs["keyframes.images"]); got != want || f.inputs["duration.frames"] != "auto" {
		t.Fatalf("keyframes %s, frames %v", got, f.inputs["duration.frames"])
	}
	if !strings.Contains(out.String(), "uploaded "+file+" as take t1\n") {
		t.Fatalf("out %s", out.String())
	}
	// without -p the workflow's fps (24) counts
	if code := helm([]string{"run", "echo-image-to-video", "--keyframe", "12:t7", "-o", t.TempDir()}, nil, &out, &errb, nil); code != 0 ||
		fmt.Sprint(f.inputs["keyframes.images"]) != `[map[image:map[take_id:t7] seconds:0.5]]` {
		t.Fatalf("fps 24: %d %v %s", code, f.inputs["keyframes.images"], errb.String())
	}
	for _, bad := range [][]string{
		{"run", "echo-image-to-video", "--keyframe", "x:t7"},
		{"run", "echo-image-to-video", "--keyframe", "-1:t7"},
		{"run", "echo-image-to-video", "--keyframe", "4:"},
		{"run", "echo-image-to-video", "--duration", "long"},
		{"run", "echo", "--image", "t7"},
		{"run", "echo", "--duration", "auto"},
	} {
		errb.Reset()
		if code := helm(append(bad, "-o", t.TempDir()), nil, &out, &errb, nil); code != 1 {
			t.Errorf("%v: %d %s", bad, code, errb.String())
		}
	}
	if !strings.Contains(errb.String(), "workflow echo has no length") {
		t.Errorf("no frames param: %s", errb.String())
	}
}

func TestResolvedLengthIsPrinted(t *testing.T) {
	var out bytes.Buffer
	e := event{Type: "status", Data: []byte(`{"status":"succeeded","resolved":{"seed":3,"params":{"duration.frames":41}}}`)}
	if printEvent(e, &out) != "succeeded" || out.String() != "succeeded (seed 3, duration.frames 41)\n" {
		t.Fatalf("%q", out.String())
	}
}

const retakeWorkflow = `{"id":"echo-retake","title":"Echo retake","family":"echo","env":"fake","media":"video",
 "nodes":{"prompt":{"kind":"prompt"},"retake":{"kind":"echo/retake","in":{"text":"prompt.text"}},"a2v":{"kind":"echo/a2v"},"refs":{"kind":"echo/refs"},"output":{"kind":"output"}},
 "node_schemas":{"prompt":{"params":[{"name":"text","type":"text"}]},
  "echo/retake":{"params":[{"name":"source","type":"media","accepts":["video"],"grid":32},{"name":"span","type":"span"},{"name":"delay_ms","type":"int"}]},
  "echo/a2v":{"params":[{"name":"source","type":"media","accepts":["audio"],"trim":true}]},
  "echo/refs":{"params":[{"name":"videos","type":"rows","item":{"video":{"type":"media","accepts":["video"],"trim":true},"sound":{"type":"enum","enum":["keep","silent"],"default":"keep"}}},
   {"name":"audios","type":"rows","item":{"audio":{"type":"media","accepts":["audio"],"trim":true}}}]},"output":{"params":[]}}}`

const t2Uses = `{"uses":[
 {"workflow_id":"echo-retake","title":"Echo retake","action":"Retake a section","family":"echo","media":"video","param":"retake.source",
  "inputs":{"retake.source":{"take_id":"t2"},"retake.span":{"start":0,"end":2.125}}},
 {"workflow_id":"echo-a2v","title":"Echo audio to video","action":"Audio to video","family":"echo","media":"video","param":"a2v.source",
  "inputs":{"a2v.source":{"take_id":"t2","in":0,"out":2.125}}},
 {"workflow_id":"w9","title":"My clip","family":"echo","media":"video","param":"x.source","inputs":{"x.source":{"take_id":"t2"},"x.seconds":1.3,"x.direction":"after"}}]}`

func TestRunMediaAndSpan(t *testing.T) {
	f, _ := setup(t, "succeeded")
	var out, errb bytes.Buffer
	args := []string{"run", "echo-retake", "--media", "retake.source=t2", "--span", "0.5:1.25", "-p", "a2v.source=t3:0.25:2", "-o", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s\n%s", code, errb.String(), out.String())
	}
	want := `map[a2v.source:map[in:0.25 out:2 take_id:t3] retake.source:map[take_id:t2] retake.span:map[end:1.25 start:0.5]]`
	if got := fmt.Sprint(f.inputs); got != want {
		t.Fatalf("inputs %s", got)
	}
	errb.Reset()
	if code := helm([]string{"run", "echo-retake", "--media", "a2v.source=t3:1:2", "-o", t.TempDir()}, nil, &out, &errb, nil); code != 0 ||
		fmt.Sprint(f.inputs["a2v.source"]) != "map[in:1 out:2 take_id:t3]" {
		t.Fatalf("trim via --media: %d %v %s", code, f.inputs, errb.String())
	}
	for _, c := range []struct{ wf, flag, val, msg string }{
		{"echo-retake", "--media", "retake.delay_ms=t2", "want <node.param>=<take id>[:in:out] on a media input"},
		{"echo-retake", "--media", "retake.source=t2:1", "retake.source: want <take id>[:in:out]"},
		{"echo-retake", "--media", "retake.source=t2:x:2", "retake.source: in is not a number of seconds"},
		{"echo-retake", "--span", "1", "retake.span: want start:end in seconds"},
		{"echo", "--span", "0:1", "--span: workflow echo takes no range"},
	} {
		errb.Reset()
		if code := helm([]string{"run", c.wf, c.flag, c.val, "-o", t.TempDir()}, nil, &out, &errb, nil); code != 1 || !strings.Contains(errb.String(), c.msg) {
			t.Errorf("%s %s: %d %q", c.flag, c.val, code, errb.String())
		}
	}
}

// API 1.22: -p types rows of media (MiniMax H3's video and audio references): <take>[:in:out][:<enum value>] each.
func TestRunMediaRows(t *testing.T) {
	f, _ := setup(t, "succeeded")
	var out, errb bytes.Buffer
	args := []string{"run", "echo-retake", "-p", "refs.videos=v1:0.5:2.5:silent, v2", "-o", t.TempDir()}
	if code := helm(args, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	if got := fmt.Sprint(f.inputs["refs.videos"]); got != "[map[sound:silent video:map[in:0.5 out:2.5 take_id:v1]] map[video:map[take_id:v2]]]" {
		t.Fatalf("videos %s", got)
	}
	// audios has no enum field: "keep" is no value of it, so it reads as part of the take ref and is refused
	if code := helm([]string{"run", "echo-retake", "-p", "refs.audios=a1:keep", "-o", t.TempDir()}, nil, &out, &errb, nil); code != 1 ||
		!strings.Contains(errb.String(), "refs.audios: want <take id>[:in:out]") {
		t.Fatalf("audios: %d %q", code, errb.String())
	}
	errb.Reset()
	if code := helm([]string{"run", "echo-retake", "-p", "refs.audios=a1:0:3,a2", "-o", t.TempDir()}, nil, &out, &errb, nil); code != 0 ||
		fmt.Sprint(f.inputs["refs.audios"]) != "[map[audio:map[in:0 out:3 take_id:a1]] map[audio:map[take_id:a2]]]" {
		t.Fatalf("audios: %d %v %s", code, f.inputs, errb.String())
	}
}

func TestTakeUses(t *testing.T) {
	setup(t, "")
	var out, errb bytes.Buffer
	if code := helm([]string{"take", "uses", "t2"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("uses: %d %s", code, errb.String())
	}
	want := "Retake a section · Echo retake\n  helm run echo-retake -p retake.source=t2 -p retake.span=0:2.125\n" +
		"Audio to video · Echo audio to video\n  helm run echo-a2v -p a2v.source=t2:0:2.125\n" +
		"My clip\n  helm run w9 -p x.direction=after -p x.seconds=1.3 -p x.source=t2\n"
	if out.String() != want {
		t.Fatalf("got\n%s", out.String())
	}
	out.Reset()
	if code := helm([]string{"take", "uses", "t3"}, nil, &out, &errb, nil); code != 0 || out.String() != "no workflow takes it\n" {
		t.Fatalf("none: %d %q", code, out.String())
	}
	for _, bad := range [][]string{{"take"}, {"take", "uses"}, {"take", "list", "t2"}} {
		if code := helm(bad, nil, &out, &errb, nil); code != 1 {
			t.Errorf("%v: %d", bad, code)
		}
	}
}
