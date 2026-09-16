package media_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/export"
	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// The export as a studio meets it: assets in the store, a sequence made
// through the API, a job that renders it, and a gallery item with the clips as
// its inputs (R46, R48).

type apiFixture struct {
	svc *studioapi.Service
	ctx context.Context
	tl  *export.Tool
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	tl := tool(t)
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := studioapi.NewService(studioapi.Config{
		Store: st,
		Media: &media.Engine{Assets: filepath.Join(d.Data(), "assets"), Derived: filepath.Join(d.Cache(), "derived"), Library: d.Library()},
		Paths: studioapi.DirPaths{StageRoot: d.Stage(), DataRoot: d.Data(), LogsRoot: d.Logs()},
		Logf:  t.Logf,
	})
	p := studioapi.Principal{StudioID: "fixture-studio", Capabilities: []string{"assets", "gallery", "timeline"}}
	return &apiFixture{svc: svc, ctx: studioapi.WithPrincipal(context.Background(), p), tl: tl}
}

// upload puts a fixture file in the asset store, with the duration a studio
// would have given when it adopted it.
func (f *apiFixture) upload(t *testing.T, name string, kind helm.AssetKind) string {
	t.Helper()
	path := filepath.Join(fixtures, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := f.tl.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	params := helm.AssetsUploadParams{Kind: kind, Filename: &name}
	if probe.Duration > 0 {
		d := probe.Duration
		params.DurationS = &d
	}
	a, _, err := f.svc.AssetsUpload(f.ctx, bytes.NewReader(raw), "video/mp4", int64(len(raw)), params)
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return a.ID
}

// awaitExport waits for the sequence's newest export to finish.
func (f *apiFixture) awaitExport(t *testing.T, timelineID string) helm.Job {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		page, err := f.svc.TimelineExports(f.ctx, timelineID, helm.TimelineExportsParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			t.Fatal("the sequence has no export job")
		}
		job := page.Items[0]
		switch job.State {
		case helm.JobStateQueued, helm.JobStateRunning:
		default:
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("the export was still %s after 90s", job.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A studio makes a sequence from two takes and exports it: the job succeeds,
// the file becomes an asset, and the item that records it names the sequence
// and every clip that went into it.
func TestAStudioExportsASequenceEndToEnd(t *testing.T) {
	f := newAPIFixture(t)
	a := f.upload(t, "take-a.mp4", helm.AssetKindVideo)
	b := f.upload(t, "take-b.mp4", helm.AssetKindVideo)
	tl, err := f.svc.TimelineCreate(f.ctx, &helm.TimelineCreate{
		Name:   "two takes",
		Target: helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24},
		Clips:  []helm.TimelineClip{{AssetID: a}, {AssetID: b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.svc.TimelinePlan(f.ctx, tl.ID, helm.TimelinePlanParams{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != timeline.ModeCopy {
		t.Fatalf("plan says %s because %v, want copy", plan.Mode, plan.Reasons)
	}
	if _, err := f.svc.TimelineExport(f.ctx, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264}); err != nil {
		t.Fatal(err)
	}
	job := f.awaitExport(t, tl.ID)
	if job.State != helm.JobStateSucceeded {
		t.Fatalf("the export ended %s: %+v", job.State, job.LastError)
	}

	items, err := f.svc.GalleryQuery(f.ctx, helm.GalleryQueryParams{})
	if err != nil {
		t.Fatal(err)
	}
	var made *helm.Item
	for i := range items.Items {
		if items.Items[i].TimelineID != nil && *items.Items[i].TimelineID == tl.ID {
			made = &items.Items[i]
		}
	}
	if made == nil {
		t.Fatalf("no gallery item names the sequence; items: %+v", items.Items)
	}
	if len(made.Inputs) != 2 {
		t.Errorf("the export has %d inputs, want one per clip", len(made.Inputs))
	}
	for _, in := range made.Inputs {
		if in.Role != "clip" {
			t.Errorf("input %s has role %q, want clip", in.AssetID, in.Role)
		}
	}
	if made.Asset.DurationS == nil || *made.Asset.DurationS < 3.4 || *made.Asset.DurationS > 3.6 {
		t.Errorf("the export's duration is %v, want about 3.5s", made.Asset.DurationS)
	}
	// An export holds its clips, so nothing it was made from is reclaimable.
	preview, err := f.svc.ReclaimPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 0 {
		t.Errorf("%d assets are reclaimable straight after an export that uses them", len(preview.Items))
	}
}

// A sequence that cannot be copied is rendered instead, and what comes out is
// the target it declared.
func TestAConformedExportEndToEnd(t *testing.T) {
	f := newAPIFixture(t)
	a := f.upload(t, "take-a.mp4", helm.AssetKindVideo)
	d := f.upload(t, "take-d-640x360.mp4", helm.AssetKindVideo)
	tl, err := f.svc.TimelineCreate(f.ctx, &helm.TimelineCreate{
		Name:   "two sizes",
		Target: helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24},
		Clips:  []helm.TimelineClip{{AssetID: a}, {AssetID: d}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.svc.TimelinePlan(f.ctx, tl.ID, helm.TimelinePlanParams{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != timeline.ModeConform || len(plan.Reasons) == 0 {
		t.Fatalf("plan says %s with %v, want conform with a reason", plan.Mode, plan.Reasons)
	}
	if _, err := f.svc.TimelineExport(f.ctx, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264}); err != nil {
		t.Fatal(err)
	}
	if job := f.awaitExport(t, tl.ID); job.State != helm.JobStateSucceeded {
		t.Fatalf("the export ended %s: %+v", job.State, job.LastError)
	}
}

// Two exports of one sequence at once would write over each other's work.
func TestOneExportAtATimePerSequence(t *testing.T) {
	f := newAPIFixture(t)
	a := f.upload(t, "take-a.mp4", helm.AssetKindVideo)
	tl, err := f.svc.TimelineCreate(f.ctx, &helm.TimelineCreate{
		Name:   "busy",
		Target: helm.TimelineTargetInput{Width: 320, Height: 180, FPS: 24},
		Clips:  []helm.TimelineClip{{AssetID: a}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TimelineExport(f.ctx, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264}); err != nil {
		t.Fatal(err)
	}
	_, second := f.svc.TimelineExport(f.ctx, tl.ID, &helm.ExportRequest{Preset: helm.ExportPresetH264})
	if second == nil {
		t.Error("a second export of the same sequence was allowed to start")
	}
	f.awaitExport(t, tl.ID)
}
