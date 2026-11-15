package main

import (
	"bytes"
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
