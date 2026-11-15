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
