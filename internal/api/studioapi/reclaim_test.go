package studioapi

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

type staticStudios map[string]*manifest.Manifest

func (s staticStudios) Manifest(id string) (*manifest.Manifest, bool) { m, ok := s[id]; return m, ok }

// Second review #3: an upload of the same bytes that lands between reclaim's
// commit and its file removal must not lose its blob.
func TestAnUploadDuringReclaimKeepsItsBlob(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := Principal{StudioID: "s", Capabilities: []string{"assets", "gallery"}}
	svc := NewService(Config{
		Store:   st,
		Media:   &media.Engine{Assets: filepath.Join(d.Data(), "assets"), Derived: filepath.Join(d.Cache(), "derived"), Library: d.Library()},
		Paths:   DirPaths{StageRoot: d.Stage(), DataRoot: d.Data(), LogsRoot: d.Logs()},
		Studios: staticStudios{"s": {ID: "s", Capabilities: p.Capabilities}},
	})
	pctx := WithPrincipal(ctx, p)
	data := []byte("the same render, twice")
	first, _, err := svc.AssetsUpload(pctx, bytes.NewReader(data), "video/mp4", int64(len(data)), helm.AssetsUploadParams{Kind: helm.AssetKindVideo})
	if err != nil {
		t.Fatal(err)
	}
	item, err := svc.GalleryAdd(pctx, &helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: first.ID, Params: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GalleryDelete(pctx, item.ID); err != nil {
		t.Fatal(err)
	}

	type result struct {
		asset *helm.Asset
		err   error
	}
	done := make(chan result, 1)
	svc.afterReclaimCommit = func() {
		// The row is gone and the file is not yet removed: the moment the
		// race needs. Start the second upload and give it every chance to run.
		go func() {
			a, _, err := svc.AssetsUpload(pctx, bytes.NewReader(data), "video/mp4", int64(len(data)), helm.AssetsUploadParams{Kind: helm.AssetKindVideo})
			done <- result{a, err}
		}()
		time.Sleep(200 * time.Millisecond)
	}
	preview, err := svc.ReclaimPreview(ctx)
	if err != nil || len(preview.Items) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, _, err := svc.Reclaim(ctx, preview.Confirm); err != nil {
		t.Fatal(err)
	}
	var second result
	select {
	case second = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the second upload never finished")
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/assets/"+second.asset.ID, nil)
	if err := svc.AssetsRead(pctx, rec, req, second.asset.ID, helm.AssetsReadParams{}); err != nil {
		t.Fatalf("reading the asset uploaded during reclaim: %v", err)
	}
	if got, _ := io.ReadAll(rec.Body); !bytes.Equal(got, data) {
		t.Fatalf("the asset uploaded during reclaim reads %q", got)
	}
}
