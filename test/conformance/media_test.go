package conformance

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// ---------------------------------------------------------------- assets (R34-R37, Q9-Q14)

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// sameInodeExists reports whether any regular file under root is inode ino:
// a stored link to what the studio wrote, found without knowing how the
// store names its files.
func sameInodeExists(t *testing.T, root string, ino uint64) bool {
	t.Helper()
	found := false
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil && fi.Sys().(*syscall.Stat_t).Ino == ino {
				found = true
			}
		}
		return nil
	})
	return found
}

func TestUploadIsIdempotentOnContentAndProbesImages(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		img := pngBytes(t, 40, 20, 1)
		first := upload(t, e.C(A), img, helm.AssetKindImage, "ref.png")
		if first.Width == nil || *first.Width != 40 || *first.Height != 20 || first.Bytes != int64(len(img)) || first.Mime != "image/png" {
			t.Fatalf("upload = %+v", first)
		}
		if first.OriginStudio == nil || *first.OriginStudio != A || first.LibraryPath == nil {
			t.Fatalf("the origin studio sees its own origin and library path: %+v", first)
		}
		again := upload(t, e.C(A), img, helm.AssetKindImage, "other-name.png")
		if again.ID != first.ID {
			t.Fatalf("the same bytes stored twice: %s and %s", first.ID, again.ID)
		}
		// Another studio uploading identical bytes gets the same asset, and
		// learns nothing of who made it (first review #9).
		fromB := upload(t, e.C(B), img, helm.AssetKindImage, "b.png")
		if fromB.ID != first.ID || fromB.OriginStudio != nil || fromB.LibraryPath != nil {
			t.Fatalf("studio b's view of a dedup hit = %+v", fromB)
		}
		raw, err := e.C(B).Assets.Read(ctx, fromB.ID, nil)
		noErr(t, err)
		if got := readAll(t, raw); !bytes.Equal(got, img) {
			t.Fatal("studio b cannot read the bytes it uploaded")
		}
	})
}

func TestAssetBytesWithRange(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		data := bytes.Repeat([]byte("0123456789"), 100)
		a := upload(t, e.C(A), data, helm.AssetKindOther, "digits.bin")
		part, err := e.C(A).Assets.Read(ctx, a.ID, &helm.AssetsReadParams{Range: ptr("bytes=10-19")})
		noErr(t, err)
		if part.Status != 206 || string(readAll(t, part)) != "0123456789" || part.ContentRange != "bytes 10-19/1000" {
			t.Fatalf("range = %d %q", part.Status, part.ContentRange)
		}
		whole, err := e.C(A).Assets.Read(ctx, a.ID, nil)
		noErr(t, err)
		if whole.Status != 200 || !bytes.Equal(readAll(t, whole), data) || whole.ETag != `"`+a.SHA256+`"` {
			t.Fatalf("whole = %d etag %s", whole.Status, whole.ETag)
		}
		_, err = e.C(A).Assets.Read(ctx, a.ID, &helm.AssetsReadParams{Range: ptr("bytes=5000-")})
		wantErr(t, err, helm.KindInvalid, "range_not_satisfiable")
	})
}

func TestAdoptHardlinksFromStageAndNeverCopies(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		me, err := c.Me.Get(ctx)
		noErr(t, err)
		staged := filepath.Join(me.Paths.Stage, "take.mp4")
		data := bytes.Repeat([]byte("frame"), 2000)
		if err := os.WriteFile(staged, data, 0o644); err != nil {
			t.Fatal(err)
		}
		ino := inode(t, staged)
		root := filepath.Dir(filepath.Dir(me.Paths.Stage))
		a, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: staged, Kind: helm.AssetKindVideo, DurationS: ptr(5.17), Width: ptr(int64(800)), Height: ptr(int64(448))})
		noErr(t, err)
		if _, err := os.Lstat(staged); !os.IsNotExist(err) {
			t.Fatalf("the stage entry is still there after adopt: %v", err)
		}
		if a.DurationS == nil || *a.DurationS != 5.17 || *a.Width != 800 || a.Bytes != int64(len(data)) {
			t.Fatalf("adopted = %+v", a)
		}
		// Same inode, no copy: some file under the provider's roots is the
		// very inode the studio wrote.
		if !sameInodeExists(t, filepath.Dir(root), ino) {
			t.Fatalf("no stored file has inode %d: adoption copied", ino)
		}
		raw, err := c.Assets.Read(ctx, a.ID, nil)
		noErr(t, err)
		if !bytes.Equal(readAll(t, raw), data) {
			t.Fatal("the adopted bytes differ")
		}

		// The same bytes staged again: the entry goes, nothing new is stored.
		if err := os.WriteFile(staged, data, 0o644); err != nil {
			t.Fatal(err)
		}
		again, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: staged, Kind: helm.AssetKindVideo})
		noErr(t, err)
		if again.ID != a.ID {
			t.Fatalf("a duplicate adopt made asset %s beside %s", again.ID, a.ID)
		}
		if _, err := os.Lstat(staged); !os.IsNotExist(err) {
			t.Fatal("a duplicate's stage entry was left")
		}
	})
}

func TestAdoptFromDataKeepsTheFileAsTheBlobsReadOnlyInode(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		me, err := c.Me.Get(ctx)
		noErr(t, err)
		out := filepath.Join(me.Paths.Data, "sessions", "example", "outputs", "take-1.mp4")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		data := []byte("a finished take")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
		ino := inode(t, out)
		a, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: out, Kind: helm.AssetKindVideo})
		noErr(t, err)
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatalf("adopting from {data} removed the studio's file: %v", err)
		}
		if fi.Sys().(*syscall.Stat_t).Ino != ino || fi.Sys().(*syscall.Stat_t).Nlink < 2 {
			t.Fatalf("the file is not a second link to the blob (inode %d, links %d)", fi.Sys().(*syscall.Stat_t).Ino, fi.Sys().(*syscall.Stat_t).Nlink)
		}
		if fi.Mode().Perm()&0o222 != 0 {
			t.Fatalf("mode %v; an adopted inode is read-only", fi.Mode())
		}
		if err := os.WriteFile(out, []byte("rewritten"), 0o644); err == nil {
			t.Fatal("the studio rewrote the blob in place")
		}
		raw, err := c.Assets.Read(ctx, a.ID, nil)
		noErr(t, err)
		if !bytes.Equal(readAll(t, raw), data) {
			t.Fatal("the stored bytes changed")
		}
	})
}

func TestAdoptRefusesPathsOutsideItsRootsAndThroughSymlinks(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		me, err := c.Me.Get(ctx)
		noErr(t, err)
		elsewhere := filepath.Join(t.TempDir(), "secret.txt")
		os.WriteFile(elsewhere, []byte("not the studio's"), 0o600)
		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: elsewhere, Kind: helm.AssetKindOther})
		wantErr(t, err, helm.KindInvalid, "outside_roots")

		link := filepath.Join(me.Paths.Stage, "link")
		os.Symlink(filepath.Dir(elsewhere), link)
		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: filepath.Join(link, "secret.txt"), Kind: helm.AssetKindOther})
		wantErr(t, err, helm.KindInvalid, "outside_roots")
		if fi, _ := os.Stat(elsewhere); fi.Mode().Perm() != 0o600 {
			t.Fatalf("a refused adopt changed the target's mode to %v", fi.Mode())
		}

		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: filepath.Join(me.Paths.Stage, "..", "..", "..", "secret"), Kind: helm.AssetKindOther})
		wantErr(t, err, helm.KindInvalid, "outside_roots")
		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: filepath.Join(me.Paths.Stage, "absent.mp4"), Kind: helm.AssetKindVideo})
		wantErr(t, err, helm.KindNotFound, "")
		os.Mkdir(filepath.Join(me.Paths.Stage, "dir"), 0o700)
		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: filepath.Join(me.Paths.Stage, "dir"), Kind: helm.AssetKindVideo})
		wantErr(t, err, helm.KindInvalid, "not_a_file")
		_, err = c.Assets.Adopt(ctx, helm.AdoptRequest{Path: "relative/take.mp4", Kind: helm.AssetKindVideo})
		wantErr(t, err, helm.KindInvalid, "outside_roots")
	})
}

// A studio adopts only from its own stage and data directories, never from
// another studio's (Q10 as amended by first review #4).
func TestAdoptNeverReachesAnotherStudiosDirectories(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		a, err := e.C(A).Me.Get(ctx)
		noErr(t, err)
		b, err := e.C(B).Me.Get(ctx)
		noErr(t, err)
		if a.Paths.Stage == b.Paths.Stage || a.Paths.Data == b.Paths.Data {
			t.Fatalf("two studios share a directory: a %+v, b %+v", a.Paths, b.Paths)
		}
		for _, dir := range []string{b.Paths.Stage, b.Paths.Data} {
			theirs := filepath.Join(dir, "b-private.mp4")
			if err := os.WriteFile(theirs, []byte("studio b's "+dir), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := e.C(A).Assets.Adopt(ctx, helm.AdoptRequest{Path: theirs, Kind: helm.AssetKindVideo})
			wantErr(t, err, helm.KindInvalid, "outside_roots")
			if _, err := os.Stat(theirs); err != nil {
				t.Fatalf("a refused adopt touched studio b's file: %v", err)
			}
		}
	})
}

func TestAssetReadRuleIsNotFoundForAnotherStudiosAsset(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		a := upload(t, e.C(A), pngBytes(t, 4, 4, 9), helm.AssetKindImage, "a.png")
		_, err := e.C(B).Assets.Read(ctx, a.ID, nil)
		// B holds gallery.read_all, which reads any asset (Q9).
		noErr(t, err)
		// Q holds assets? No: kv and records only, so the capability refuses first.
		_, err = e.C(Q).Assets.Read(ctx, a.ID, nil)
		wantErr(t, err, helm.KindForbidden, "capability_required")
	})
}

// Thumbnails: an image is scaled by the standard library, and a video gets a
// poster frame from ffmpeg (M8 Q6). Bytes that are not a video it can read are
// refused as unsupported rather than reported as a fault of the daemon's — the
// same answer an image the standard library cannot decode gets.
func TestThumbnailsForImagesAndBytesThatAreNotAVideo(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		img := upload(t, e.C(A), pngBytes(t, 640, 480, 2), helm.AssetKindImage, "big.png")
		thumb, err := e.C(A).Assets.Thumb(ctx, img.ID, nil)
		noErr(t, err)
		b := readAll(t, thumb)
		if thumb.ContentType != "image/jpeg" || len(b) < 3 || b[0] != 0xFF || b[1] != 0xD8 {
			t.Fatalf("thumb = %s, %d bytes", thumb.ContentType, len(b))
		}
		vid := upload(t, e.C(A), []byte("not really a video"), helm.AssetKindVideo, "v.mp4")
		_, err = e.C(A).Assets.Thumb(ctx, vid.ID, nil)
		wantErr(t, err, helm.KindUnsupported, "unsupported")
	})
}

// ---------------------------------------------------------------- gallery (R38, Q15-Q17)

func TestGalleryRecordsProvenanceAndQueriesByEveryFilter(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		frame := upload(t, c, pngBytes(t, 8, 8, 3), helm.AssetKindImage, "frame.png")
		take := upload(t, c, []byte("take bytes"), helm.AssetKindVideo, "take.mp4")
		sess, err := c.Sessions.Create(ctx, helm.SessionCreate{Name: "example"})
		noErr(t, err)
		it, err := c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: take.ID, Title: ptr("café window drift"),
			SessionID: ptr(sess.ID), Params: map[string]any{"seed": 42, "prompt": "The woman at the café window"},
			Inputs: []helm.ItemInput{{AssetID: frame.ID, Role: "first_frame"}, {AssetID: frame.ID, Role: "first_frame"}},
			Tags:   []string{"cafe", "test", "cafe"}})
		noErr(t, err)
		if len(it.Inputs) != 1 || it.Inputs[0].Role != "first_frame" || !slices.Equal(it.Tags, []string{"cafe", "test"}) ||
			it.Params["seed"] != float64(42) || it.Asset.ID != take.ID || *it.SessionID != sess.ID {
			t.Fatalf("item = %+v", it)
		}
		time.Sleep(3 * time.Millisecond) // distinct created_at, so order and since/until are exact
		other := addItem(t, c, frame.ID, map[string]any{"prompt": "a street at noon"})

		find := func(p helm.GalleryQueryParams) []string {
			t.Helper()
			page, err := c.Gallery.Query(ctx, &p)
			noErr(t, err)
			var got []string
			for _, x := range page.Items {
				got = append(got, x.ID)
			}
			return got
		}
		for name, tc := range map[string]struct {
			p    helm.GalleryQueryParams
			want []string
		}{
			"all newest first": {helm.GalleryQueryParams{}, []string{other.ID, it.ID}},
			"kind":             {helm.GalleryQueryParams{Kind: ptr(helm.AssetKindVideo)}, []string{it.ID}},
			"tags all present": {helm.GalleryQueryParams{Tag: []string{"cafe", "test"}}, []string{it.ID}},
			"missing tag":      {helm.GalleryQueryParams{Tag: []string{"cafe", "nope"}}, nil},
			"session":          {helm.GalleryQueryParams{SessionID: ptr(sess.ID)}, []string{it.ID}},
			"asset":            {helm.GalleryQueryParams{AssetID: ptr(frame.ID)}, []string{other.ID}},
			"text in prompt":   {helm.GalleryQueryParams{Q: ptr("NEAR(café window)")}, []string{it.ID}},
			"text in title":    {helm.GalleryQueryParams{Q: ptr("drift")}, []string{it.ID}},
			"since the second": {helm.GalleryQueryParams{Since: ptr(other.CreatedAt)}, []string{other.ID}},
			"until the second": {helm.GalleryQueryParams{Until: ptr(other.CreatedAt)}, []string{it.ID}},
		} {
			if got := find(tc.p); !slices.Equal(got, tc.want) {
				t.Errorf("%s: %v; want %v", name, got, tc.want)
			}
		}

		_, err = c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: take.ID, Params: map[string]any{}, SessionID: ptr("01ARZ3NDEKTSV4RRFFQ69G5FAV")})
		wantErr(t, err, helm.KindInvalid, "unknown_session")
		_, err = c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Params: map[string]any{}})
		wantErr(t, err, helm.KindNotFound, "")
	})
}

func TestGalleryScopeUpdateAndDelete(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		a := upload(t, e.C(A), []byte("a's take"), helm.AssetKindVideo, "a.mp4")
		item := addItem(t, e.C(A), a.ID, map[string]any{"seed": 1})

		_, err := e.C(A).Gallery.Query(ctx, &helm.GalleryQueryParams{Scope: ptr("all")})
		wantErr(t, err, helm.KindForbidden, "capability_required")
		page, err := e.C(B).Gallery.Query(ctx, &helm.GalleryQueryParams{Scope: ptr("all"), Studio: ptr(A)})
		noErr(t, err)
		if len(page.Items) != 1 || page.Items[0].ID != item.ID || page.Items[0].Asset.OriginStudio != nil {
			t.Fatalf("scope=all from b = %+v", page.Items)
		}
		self, err := e.C(B).Gallery.Query(ctx, nil)
		noErr(t, err)
		if len(self.Items) != 0 {
			t.Fatal("scope=self shows another studio's items")
		}
		_, err = e.C(B).Gallery.Get(ctx, item.ID)
		noErr(t, err)
	})
}

func TestGalleryMergePatchAndOwnItemsOnly(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		a := upload(t, e.C(A), []byte("a"), helm.AssetKindVideo, "a.mp4")
		created, err := e.C(A).Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: a.ID, Title: ptr("first"), Params: map[string]any{"seed": 1}, Tags: []string{"x", "y"}})
		noErr(t, err)
		got, err := e.C(A).Gallery.Update(ctx, created.ID, map[string]any{"title": nil, "starred": true, "tags": []any{"z"}})
		noErr(t, err)
		if got.Title != nil || !got.Starred || !slices.Equal(got.Tags, []string{"z"}) || got.Params["seed"] != float64(1) {
			t.Fatalf("patched = %+v", got)
		}
		_, err = e.C(A).Gallery.Update(ctx, created.ID, map[string]any{"params": map[string]any{"seed": 2}})
		wantErr(t, err, helm.KindInvalid, "")
		_, err = e.C(B).Gallery.Update(ctx, created.ID, map[string]any{"starred": false})
		wantErr(t, err, helm.KindNotFound, "")
		wantErr(t, e.C(B).Gallery.Delete(ctx, created.ID), helm.KindNotFound, "")

		noErr(t, e.C(A).Gallery.Delete(ctx, created.ID))
		_, err = e.C(A).Gallery.Get(ctx, created.ID)
		wantErr(t, err, helm.KindNotFound, "")
		page, err := e.C(A).Gallery.Query(ctx, &helm.GalleryQueryParams{Q: ptr("first")})
		noErr(t, err)
		if len(page.Items) != 0 {
			t.Fatal("a deleted item is still found by search")
		}
	})
}

func TestLineageFollowsInputsAcrossStudiosAndHidesWhatACallerMayNotSee(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		// b makes a still; a hands... no: a makes a still, b uses it, a uses b's output.
		still := upload(t, e.C(A), pngBytes(t, 4, 4, 5), helm.AssetKindImage, "still.png")
		stillItem := addItem(t, e.C(A), still.ID, map[string]any{"prompt": "a still"})
		// b can read a's still through gallery.read_all.
		clip := upload(t, e.C(B), []byte("b's clip"), helm.AssetKindVideo, "clip.mp4")
		clipItem, err := e.C(B).Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: clip.ID, Params: map[string]any{}, Inputs: []helm.ItemInput{{AssetID: still.ID, Role: "first_frame"}}})
		noErr(t, err)
		// a gets b's clip by handoff? a has no read access to it yet, so it cannot use it.
		_, err = e.C(A).Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: still.ID, Params: map[string]any{}, Inputs: []helm.ItemInput{{AssetID: clip.ID, Role: "clip"}}})
		wantErr(t, err, helm.KindNotFound, "")

		// Downstream from the still, as b (read_all) sees it: b's clip.
		down, err := e.C(B).Assets.Lineage(ctx, still.ID, nil)
		noErr(t, err)
		if len(down.Items) != 1 || down.Items[0].ID != clipItem.ID {
			t.Fatalf("b's downstream view = %+v", down.Items)
		}
		// a does not see b's item in its own downstream walk.
		down, err = e.C(A).Assets.Lineage(ctx, still.ID, nil)
		noErr(t, err)
		if len(down.Items) != 0 {
			t.Fatalf("a sees another studio's item in lineage: %+v", down.Items)
		}
		// Upstream from b's clip: a's still item, visible to b.
		up, err := e.C(B).Gallery.Lineage(ctx, clipItem.ID, nil)
		noErr(t, err)
		if len(up.Items) != 1 || up.Items[0].ID != stillItem.ID {
			t.Fatalf("upstream = %+v", up.Items)
		}
		_, err = e.C(A).Gallery.Lineage(ctx, clipItem.ID, nil)
		wantErr(t, err, helm.KindNotFound, "")
	})
}

// ---------------------------------------------------------------- handoff and inbox (R39, Q22)

func TestHandoffDeliversAnItemAndItsAssetUntilConsumed(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		img := upload(t, e.C(A), pngBytes(t, 6, 6, 7), helm.AssetKindImage, "for-b.png")
		item := addItem(t, e.C(A), img.ID, map[string]any{"prompt": "hand me off"})

		_, err := e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: A})
		wantErr(t, err, helm.KindInvalid, "self_handoff")
		_, err = e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: "nobody-studio"})
		wantErr(t, err, helm.KindNotFound, "")
		_, err = e.C(B).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: A})
		wantErr(t, err, helm.KindForbidden, "capability_required")

		sent, err := e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: C, Role: ptr("reference")})
		noErr(t, err)
		// C holds kv only: it cannot read its inbox without gallery.
		_, err = e.C(C).Inbox.List(ctx, nil)
		wantErr(t, err, helm.KindForbidden, "capability_required")

		sent, err = e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: B, Role: ptr("reference")})
		noErr(t, err)
		inbox, err := e.C(B).Inbox.List(ctx, nil)
		noErr(t, err)
		if len(inbox.Items) != 1 || inbox.Items[0].ID != sent.ID || inbox.Items[0].FromStudio != A || inbox.Items[0].Item == nil || *inbox.Items[0].Role != "reference" {
			t.Fatalf("b's inbox = %+v", inbox.Items)
		}
		// Reading does not consume.
		again, err := e.C(B).Inbox.List(ctx, nil)
		noErr(t, err)
		if len(again.Items) != 1 {
			t.Fatal("reading the inbox consumed it")
		}
		noErr(t, e.C(B).Inbox.Consume(ctx, sent.ID))
		noErr(t, e.C(B).Inbox.Consume(ctx, sent.ID))
		wantErr(t, e.C(A).Inbox.Consume(ctx, sent.ID), helm.KindNotFound, "")
		after, err := e.C(B).Inbox.List(ctx, nil)
		noErr(t, err)
		if len(after.Items) != 0 {
			t.Fatalf("a consumed entry is still pending: %+v", after.Items)
		}
	})
}

func TestHandoffGrantsTheRecipientTheItemAndItsAssets(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		frame := upload(t, e.C(A), pngBytes(t, 3, 3, 11), helm.AssetKindImage, "frame.png")
		take := upload(t, e.C(A), []byte("a's take"), helm.AssetKindVideo, "take.mp4")
		item := addItem(t, e.C(A), take.ID, map[string]any{}, helm.ItemInput{AssetID: frame.ID, Role: "first_frame"})
		for _, id := range []string{take.ID, frame.ID} {
			_, err := e.C(D).Assets.Read(ctx, id, nil)
			wantErr(t, err, helm.KindNotFound, "")
		}
		_, err := e.C(D).Gallery.Get(ctx, item.ID)
		wantErr(t, err, helm.KindNotFound, "")

		_, err = e.C(A).Handoff.Send(ctx, helm.HandoffRequest{ItemID: item.ID, ToStudio: D})
		noErr(t, err)
		got, err := e.C(D).Gallery.Get(ctx, item.ID)
		noErr(t, err)
		if got.Asset.OriginStudio != nil || got.Asset.LibraryPath != nil {
			t.Fatalf("the recipient sees the sender's origin or library path: %+v", got.Asset)
		}
		for _, id := range []string{take.ID, frame.ID} {
			_, err := e.C(D).Assets.Read(ctx, id, nil)
			noErr(t, err)
		}
		// D can now use the handed frame as its own input.
		mine := upload(t, e.C(D), []byte("d's render"), helm.AssetKindVideo, "d.mp4")
		addItem(t, e.C(D), mine.ID, map[string]any{}, helm.ItemInput{AssetID: frame.ID, Role: "first_frame"})
	})
}

// ---------------------------------------------------------------- reclaim (Q12, first review #1, #3)

func TestReclaimAfterDeletingAnItemRemovesItsAssetWithoutError(t *testing.T) {
	each(t, func(t *testing.T, e *Env) {
		c := e.C(A)
		frame := upload(t, c, pngBytes(t, 5, 5, 13), helm.AssetKindImage, "frame.png")
		take := upload(t, c, []byte("take to delete"), helm.AssetKindVideo, "take.mp4")
		never := upload(t, c, []byte("adopted, never recorded"), helm.AssetKindVideo, "never.mp4")
		pinned, err := c.Assets.Upload(ctx, bytes.NewReader([]byte("user import")), "", &helm.AssetsUploadParams{Kind: helm.AssetKindOther, Pinned: ptr(true)})
		noErr(t, err)
		keeper := upload(t, c, []byte("kept"), helm.AssetKindVideo, "kept.mp4")
		addItem(t, c, keeper.ID, map[string]any{})
		pinnedItem := addItem(t, c, pinned.ID, map[string]any{})
		item, err := c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: take.ID, Params: map[string]any{}, Inputs: []helm.ItemInput{{AssetID: frame.ID, Role: "first_frame"}}})
		noErr(t, err)
		noErr(t, c.Gallery.Delete(ctx, item.ID))
		noErr(t, c.Gallery.Delete(ctx, pinnedItem.ID))

		res := e.Reclaim(t)
		var gone []string
		for _, it := range res.Items {
			gone = append(gone, it.ID)
		}
		slices.Sort(gone)
		want := []string{frame.ID, take.ID}
		slices.Sort(want)
		if !slices.Equal(gone, want) {
			t.Fatalf("reclaimed %v; want the deleted item's asset and input %v (never the never-referenced %s, the pinned %s or the kept %s)", gone, want, never.ID, pinned.ID, keeper.ID)
		}
		for _, id := range want {
			_, err := c.Assets.Read(ctx, id, nil)
			wantErr(t, err, helm.KindNotFound, "")
		}
		for _, id := range []string{never.ID, pinned.ID, keeper.ID} {
			_, err := c.Assets.Read(ctx, id, nil)
			noErr(t, err)
		}
	})
}
