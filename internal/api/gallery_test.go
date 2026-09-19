package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Ids the store will accept: an asset id is a ULID, and the generated router
// checks it before the service ever sees it.
const (
	h3Asset  = "01JB0000000000000000000AH3"
	ltxAsset = "01JB0000000000000000000ATX"
	h3Item   = "01JB000000000000000000TEM3"
	ltxItem  = "01JB00000000000000000TEMTX"
)

// galleryServer is the daemon with a studio API and two studios' work in the
// store, and nothing holding a token.
func galleryServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: d, API: "http://" + addr + studioapi.Base, Logf: t.Logf})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Platform: plat.Launches})
	svc, h := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
	t.Cleanup(func() { svc.Events().Shutdown("test over") })
	srv, err := New(sup, fstest.MapFS{}, addr, t.Logf, WithStudioAPI(h, svc))
	if err != nil {
		t.Fatal(err)
	}
	err = st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path) VALUES ('01JB0000000000000000000AH3', 'aa', 'video', 'video/mp4', 20, 'aa.mp4')`,
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path) VALUES ('01JB0000000000000000000ATX', 'bb', 'video', 'video/mp4', 30, 'bb.mp4')`,
			`INSERT INTO items (id, studio_id, kind, asset_id, title, created_at) VALUES ('01JB000000000000000000TEM3', 'h3-studio', 'video', '01JB0000000000000000000AH3', 'café window drift', 1)`,
			`INSERT INTO items (id, studio_id, kind, asset_id, title, created_at) VALUES ('01JB00000000000000000TEMTX', 'ltx-studio', 'video', '01JB0000000000000000000ATX', 'rain street plate', 2)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// The launcher's gallery is every studio's (03 §10, amended 2026-09-19), and
// it holds no token to ask with.
func TestLauncherGalleryReadsEveryStudio(t *testing.T) {
	srv := galleryServer(t)

	page := decode[helm.ItemPage](t, do(t, srv, "GET", Base+"/launcher/gallery/items", nil))
	if len(page.Items) != 2 {
		t.Fatalf("the launcher's gallery has %d items; want both studios'", len(page.Items))
	}
	// Newest first, as GET /gallery/items orders.
	if page.Items[0].ID != ltxItem || page.Items[1].ID != h3Item {
		t.Errorf("order = %s, %s; want ltx studio's then h3's", page.Items[0].ID, page.Items[1].ID)
	}

	one := decode[helm.ItemPage](t, do(t, srv, "GET", Base+"/launcher/gallery/items?studio=h3-studio", nil))
	if len(one.Items) != 1 || one.Items[0].ID != h3Item {
		t.Errorf("narrowed to h3-studio = %+v; want only h3 studio's", one.Items)
	}

	// The parameters are the studio-api operation's, checked by the generated
	// router rather than by anything written here.
	if rec := do(t, srv, "GET", Base+"/launcher/gallery/items?kind=sculpture", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown kind: %d %s; want 400", rec.Code, rec.Body)
	}
}

// An asset reaches the page directly, because <video src> cannot send a
// header. The row is here and its blob never was, which is the read rule's
// answer for a file that has gone: 410 rather than 404 or 200.
func TestLauncherReadsAnAssetWithoutAToken(t *testing.T) {
	srv := galleryServer(t)

	if rec := do(t, srv, "GET", Base+"/launcher/assets/01JB0000000000000000000ATX", nil); rec.Code != http.StatusGone {
		t.Errorf("reading ltx studio's asset: %d %s; want 410", rec.Code, rec.Body)
	}
	if rec := do(t, srv, "GET", Base+"/launcher/assets/01JB0000000000000000000000", nil); rec.Code != http.StatusNotFound {
		t.Errorf("reading an asset that never existed: %d; want 404", rec.Code)
	}
}

// 07 §3's property, which is why these are launcher operations of their own
// rather than the studio-api paths opened to a caller with no token.
func TestStudioAPIStillRefusesEveryTokenlessRequest(t *testing.T) {
	srv := galleryServer(t)

	for _, path := range []string{"/gallery/items?scope=all", "/gallery/items", "/assets/01JB0000000000000000000ATX", "/assets/01JB0000000000000000000ATX/thumb"} {
		if rec := do(t, srv, "GET", Base+path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no token: %d %s; want 401", path, rec.Code, rec.Body)
		}
	}
	// And the launcher's own paths are not a second way into the rest of it.
	for _, path := range []string{"/launcher/kv/anything", "/launcher/gallery/items/" + h3Item, "/launcher/records"} {
		if rec := do(t, srv, "GET", Base+path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d; want 404", path, rec.Code)
		}
	}
}
