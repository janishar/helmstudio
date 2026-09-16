// Command sequencer is the fixture studio M8's conform demo runs through
// (docs/decisions.md, "2026-09-16 · M8 timeline and export", Q21).
//
// The demo wants a sequence built from clips several studios made. ltx, AuK and
// iris do not record their outputs through the runtime SDK yet — that was left
// out of their own milestones — so this studio uploads their files instead, and
// the demo says plainly that every clip but h3's carries this one origin.
//
// It is a studio like any other: a manifest, a process, a health endpoint, and
// the runtime SDK. It holds assets, timeline and gallery.read_all, the last so
// that it may put h3's own take in the sequence beside the files it uploaded.
//
//	helm dev -f test/studios/sequencer/helmstudio.yaml
//	curl -X POST 'http://127.0.0.1:<port>/run?dir=/path/to/clips'
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

func main() {
	port := flag.Int("port", 8730, "the port helmstudio assigned")
	flag.Parse()

	client, err := helm.FromEnv()
	if err != nil {
		log.Fatalf("sequencer: %v", err)
	}
	s := &sequencer{client: client}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/run", s.run)
	mux.HandleFunc("/", s.page)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("sequencer: http://%s", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

type sequencer struct {
	client *helm.Client
	last   string
}

func (s *sequencer) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>sequencer</title>
<body style="font:13px ui-sans-serif,system-ui;padding:24px">
<h1>sequencer</h1>
<p>POST /run?dir=&lt;a folder of clips&gt; builds a sequence from what it finds and exports it.</p>
<pre>%s</pre>`, s.last)
}

// run uploads every clip in a folder, puts them in one sequence with a
// dissolve and a gain, and exports it.
func (s *sequencer) run(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "give ?dir=<folder of clips>", http.StatusBadRequest)
		return
	}
	var log strings.Builder
	out, err := s.sequence(ctx, dir, &log)
	if err != nil {
		fmt.Fprintf(&log, "\nfailed: %v\n", err)
	}
	s.last = log.String()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"log": s.last, "export": out})
}

func (s *sequencer) sequence(ctx context.Context, dir string, log *strings.Builder) (any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp4", ".mov", ".png", ".jpg", ".jpeg", ".wav", ".mp3", ".flac":
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("%s holds no clips", dir)
	}

	var video, sound []helm.TimelineClip
	for _, name := range names {
		kind := kindOf(name)
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		asset, err := s.client.Assets.Upload(ctx, f, "", &helm.AssetsUploadParams{Kind: kind, Filename: &name})
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("uploading %s: %w", name, err)
		}
		fmt.Fprintf(log, "uploaded %-28s %s (%s)\n", name, asset.ID, kind)
		clip := helm.TimelineClip{AssetID: asset.ID}
		switch kind {
		case helm.AssetKindAudio:
			sound = append(sound, clip)
		case helm.AssetKindImage:
			hold := 2.0
			clip.Hold = &hold
			video = append(video, clip)
		default:
			video = append(video, clip)
		}
	}
	if len(video) == 0 {
		return nil, fmt.Errorf("%s holds no picture to sequence", dir)
	}
	// A dissolve on the last cut and a little off the sound, so the demo
	// exercises the conform path rather than the copy.
	if len(video) > 1 {
		last := &video[len(video)-1]
		if last.Hold == nil {
			last.TransitionIn = &helm.TimelineTransition{Type: "dissolve", Duration: 0.5}
		}
	}
	tracks := []helm.TimelineTrack{{Kind: "video", Clips: video}}
	if len(sound) > 0 {
		gain := -3.0
		tracks = append(tracks, helm.TimelineTrack{Kind: "audio", GainDb: &gain, Clips: sound})
	}
	tl, err := s.client.Timeline.Create(ctx, helm.TimelineCreate{
		Name:   "the M8 demo",
		Target: helm.TimelineTargetInput{Width: 1280, Height: 720, FPS: 24},
		Tracks: tracks,
	})
	if err != nil {
		return nil, fmt.Errorf("creating the sequence: %w", err)
	}
	fmt.Fprintf(log, "\nsequence %s: %.2fs, %d track(s)\n", tl.ID, tl.DurationS, len(tl.Tracks))

	plan, err := s.client.Timeline.Plan(ctx, tl.ID, nil)
	if err != nil {
		return nil, fmt.Errorf("asking for the plan: %w", err)
	}
	fmt.Fprintf(log, "plan: %s\n", plan.Mode)
	for _, reason := range plan.Reasons {
		fmt.Fprintf(log, "  %-14s %s\n", reason.Code, reason.Message)
	}
	job, err := s.client.Timeline.Export(ctx, tl.ID, helm.ExportRequest{Preset: helm.ExportPresetH264})
	if err != nil {
		return nil, fmt.Errorf("exporting: %w", err)
	}
	fmt.Fprintf(log, "\nexport job %s\n", job.ID)
	started := time.Now()
	for {
		page, err := s.client.Timeline.Exports(ctx, tl.ID, nil)
		if err != nil {
			return nil, err
		}
		j := page.Items[0]
		if j.State != helm.JobStateQueued && j.State != helm.JobStateRunning {
			fmt.Fprintf(log, "%s after %s (%d of %d frames)\n", j.State, time.Since(started).Round(time.Millisecond), j.ProgressNum, j.ProgressDen)
			if j.LastError != nil {
				fmt.Fprintf(log, "  %s: %s\n", j.LastError.Code, j.LastError.Message)
			}
			return j, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func kindOf(name string) helm.AssetKind {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav", ".mp3", ".flac":
		return helm.AssetKindAudio
	case ".png", ".jpg", ".jpeg":
		return helm.AssetKindImage
	}
	return helm.AssetKindVideo
}
