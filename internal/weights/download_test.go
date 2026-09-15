package weights

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

// DoD: a download interrupted at 60% resumes at 60%. The first daemon is cut
// off with 60% of the file on disk; a fresh service (a restarted daemon) asks
// for exactly the remaining bytes and ends with the right file.
func TestInterruptedDownloadResumesWhereItStopped(t *testing.T) {
	f := newFixture(t)
	const size = 1 << 20
	body := hubtest.Content(size, 7)
	f.hub.Add("org/big", "model.safetensors", body, true)
	f.hub.Add("org/big", "config.json", []byte(`{"a":1}`), false)
	sixty := int64(size * 6 / 10)
	f.hub.StallAt["model.safetensors"] = sixty
	f.install("studio-a")
	w := weight("big", "org/big", "Big")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.svc.Fetch(ctx, "studio-a", w, nil) }()
	part := filepath.Join(f.dirs.Models(), "Big", "model.safetensors.part")
	select {
	case <-f.hub.Stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the download never reached 60%")
	}
	waitFor(t, "the partial file to reach 60%", func() bool {
		fi, err := os.Stat(part)
		return err == nil && fi.Size() == sixty
	})
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted fetch: err = %v, want context.Canceled", err)
	}
	if _, state, _, _ := f.artifactState("org/big"); state != StateInterrupted {
		t.Fatalf("state after the interruption = %q, want interrupted", state)
	}
	if fi, err := os.Stat(part); err != nil || fi.Size() != sixty {
		t.Fatalf("partial file after the interruption: %v %v; want %d bytes kept", fi, err, sixty)
	}
	if done, total, err := f.svc.ArtifactProgress(context.Background(), mustID(f, "org/big")); err != nil || done < sixty || total != size+7 {
		t.Fatalf("progress = %d/%d (%v); want at least %d of %d", done, total, err, sixty, size+7)
	}

	before := len(f.hub.Requests("/cdn/org/big/model.safetensors"))
	restarted := f.service()
	if err := restarted.Fetch(context.Background(), "studio-a", w, nil); err != nil {
		t.Fatalf("resumed fetch: %v", err)
	}
	resumed := f.hub.Requests("/cdn/org/big/model.safetensors")[before:]
	if len(resumed) != 1 || resumed[0].Range != "bytes="+strconv.FormatInt(sixty, 10)+"-" || resumed[0].Status != 206 {
		t.Fatalf("resume requests = %+v; want one 206 for bytes=%d-", resumed, sixty)
	}
	if got := readFile(t, filepath.Join(f.dirs.Models(), "Big", "model.safetensors")); !bytes.Equal(got, body) {
		t.Fatal("the resumed file differs from the original")
	}
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the partial file is still there: %v", err)
	}
	if _, state, _, _ := f.artifactState("org/big"); state != StateReady {
		t.Fatalf("state = %q, want ready", state)
	}
}

func mustID(f *fixture, repo string) string {
	id, _, _, n := f.artifactState(repo)
	if n != 1 {
		f.t.Fatalf("%s has %d artifact rows", repo, n)
	}
	return id
}

// DoD: a 403 mid-download resumes rather than restarting. The connection
// drops halfway; the next signed URLs have expired and answer 403. The
// downloader resolves again and continues from the same byte, never from
// zero, and never reads the 403 as a token problem.
func TestExpiredSignedURLResumesInsteadOfRestarting(t *testing.T) {
	f := newFixture(t)
	const size = 512 * 1024
	body := hubtest.Content(size, 3)
	f.hub.Add("org/gated", "dit.safetensors", body, true)
	half := int64(size / 2)
	f.hub.CutAfter["dit.safetensors"] = half
	f.install("studio-a")
	w := weight("dit", "org/gated", "Gated")

	// The retry after the dropped connection finds the signed URLs expired:
	// the next two answer 403.
	armed := false
	f.svc.cfg.HF.Sleep = func(context.Context, time.Duration) error {
		f.hub.Mu.Lock()
		defer f.hub.Mu.Unlock()
		if !armed {
			armed = true
			f.hub.ExpireNext = 2
		}
		return nil
	}

	if err := f.svc.Fetch(context.Background(), "studio-a", w, nil); err != nil {
		t.Fatalf("fetch across an expired URL: %v", err)
	}
	cdn := f.hub.Requests("/cdn/org/gated/dit.safetensors")
	var forbidden, resumed int
	for i, r := range cdn {
		if i == 0 {
			if r.Range != "bytes=0-" {
				t.Fatalf("first request range = %q", r.Range)
			}
			continue
		}
		if r.Range != "bytes="+strconv.FormatInt(half, 10)+"-" {
			t.Fatalf("request %d asked for %q after the cut; want every retry to continue at byte %d: %+v", i, r.Range, half, cdn)
		}
		if r.Status == 403 {
			forbidden++
		} else {
			resumed++
		}
	}
	if forbidden != 2 || resumed != 1 {
		t.Fatalf("after the cut: %d expired and %d resumed requests, want 2 and 1: %+v", forbidden, resumed, cdn)
	}
	if n := len(f.hub.Requests("/resolve/")); n != 4 {
		t.Errorf("resolve requests = %d, want 4 (the first, one per expired URL, and the one that worked)", n)
	}
	if got := readFile(t, filepath.Join(f.dirs.Models(), "Gated", "dit.safetensors")); !bytes.Equal(got, body) {
		t.Fatal("the file differs from the original")
	}
	if _, state, _, _ := f.artifactState("org/gated"); state != StateReady {
		t.Fatalf("state = %q, want ready (not auth_required)", state)
	}
}

// A 401 or 403 from huggingface.co itself is a gated repository: the
// artifact asks for a token, and nothing is written.
func TestHubRefusalIsAuthRequired(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("org/private", "model.safetensors", hubtest.Content(1024, 1), true)
	f.hub.NeedToken = true
	f.install("studio-a")
	err := f.svc.Fetch(context.Background(), "studio-a", weight("m", "org/private", "Private"), nil)
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("err = %v, want ErrAuthRequired", err)
	}
	if _, state, _, _ := f.artifactState("org/private"); state != StateAuthRequired {
		t.Fatalf("state = %q, want auth_required", state)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Private")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something was written for a refused repository: %v", err)
	}

	// With the token it downloads.
	f.svc.cfg.HF.Token = func(context.Context) (string, error) { return "good-token", nil }
	if err := f.svc.Fetch(context.Background(), "studio-a", weight("m", "org/private", "Private"), nil); err != nil {
		t.Fatalf("fetch with a token: %v", err)
	}
}

// Review M3 #7: an etag the Hub reports that differs from the listing fails
// before any byte is written — never after downloading the whole file, and
// never by truncating a partial file already on disk.
func TestETagMismatchFailsBeforeDownloading(t *testing.T) {
	f := newFixture(t)
	body := hubtest.Content(4096, 9)
	f.hub.Add("org/bad", "model.safetensors", body, true)
	f.hub.WrongETag["model.safetensors"] = true
	f.install("studio-a")
	err := f.svc.Fetch(context.Background(), "studio-a", weight("m", "org/bad", "Bad"), nil)
	if !errors.Is(err, ErrVerify) || !strings.Contains(err.Error(), "the listing says") {
		t.Fatalf("err = %v, want ErrVerify naming both etags", err)
	}
	if n := len(f.hub.Requests("/cdn/org/bad/")); n != 0 {
		t.Errorf("CDN requests = %d, want 0: nothing is downloaded once the etags disagree", n)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Bad", "model.safetensors.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a partial file was written: %v", err)
	}
	if _, state, _, _ := f.artifactState("org/bad"); state != StateInterrupted {
		t.Fatalf("state = %q, want interrupted", state)
	}

	// A partial file from before, resumed and complete on disk, is left as
	// it was, both mid-file and complete.
	for _, have := range []int{1000, len(body)} {
		part := filepath.Join(f.dirs.Models(), "Bad", "model.safetensors.part")
		os.MkdirAll(filepath.Dir(part), 0o755)
		os.WriteFile(part, body[:have], 0o644)
		f.hub.WrongETag["model.safetensors"] = true
		if err := f.svc.Fetch(context.Background(), "studio-a", weight("m", "org/bad", "Bad"), nil); !errors.Is(err, ErrVerify) {
			t.Fatalf("with %d bytes on disk: err = %v, want ErrVerify", have, err)
		}
		if fi, err := os.Stat(part); err != nil || fi.Size() != int64(have) {
			t.Fatalf("with %d bytes on disk, the partial file became %v (%v)", have, fi, err)
		}
	}
}

// h3's shape: two weights of one repository with different files. One
// artifact, two bindings, and the second downloads only what it adds.
func TestTwoWeightsOfOneRepoShareOneArtifact(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("org/h3", "FL2VA/config.json", []byte(`{}`), false)
	f.hub.Add("org/h3", "FL2VA/dit.safetensors", hubtest.Content(8192, 1), true)
	f.hub.Add("org/h3", "Ref2VA/dit.safetensors", hubtest.Content(8192, 2), true)
	f.install("h3-studio")
	fl := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	ref := weight("ref2va", "org/h3", "MiniMax-H3", "Ref2VA/**")
	ref.Optional = true

	if err := f.svc.Fetch(context.Background(), "h3-studio", fl, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(f.hub.Requests("/Ref2VA/")); n != 0 {
		t.Fatalf("fetching fl2va requested Ref2VA files: %d", n)
	}
	values, refuse, err := f.svc.Launch(context.Background(), "h3-studio", []manifest.Weight{fl, ref})
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := filepath.EvalSymlinks(filepath.Join(f.dirs.Models(), "MiniMax-H3"))
	if values["models.fl2va"] != dir {
		t.Fatalf("models.fl2va = %q, want %q", values["models.fl2va"], dir)
	}
	if !strings.Contains(refuse["models.ref2va"], "optional weight") {
		t.Fatalf("an unfetched optional weight resolved: values=%v refuse=%v", values, refuse)
	}

	before := len(f.hub.Requests("/FL2VA/"))
	if err := f.svc.Fetch(context.Background(), "h3-studio", ref, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(f.hub.Requests("/FL2VA/")); after != before {
		t.Fatalf("fetching ref2va requested FL2VA files again (%d → %d)", before, after)
	}
	if _, _, _, n := f.artifactState("org/h3"); n != 1 {
		t.Fatalf("artifact rows = %d, want 1", n)
	}
	values, refuse, _ = f.svc.Launch(context.Background(), "h3-studio", []manifest.Weight{fl, ref})
	if values["models.ref2va"] != dir || len(refuse) != 0 {
		t.Fatalf("after fetching both: values=%v refuse=%v", values, refuse)
	}
	arts, _ := f.svc.List(context.Background())
	if len(arts) != 1 || arts[0].RefCount != 2 || arts[0].State != StateReady {
		t.Fatalf("artifacts = %+v; want one ready artifact with two references", arts)
	}
}

// A second studio declaring the same weight downloads no file, and the
// reference count is the number of bindings.
func TestSecondStudioDownloadsNothing(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("org/shared", "model.safetensors", hubtest.Content(4096, 4), true)
	f.install("first")
	f.install("second")
	w := weight("m", "org/shared", "Shared")
	if err := f.svc.Fetch(context.Background(), "first", w, nil); err != nil {
		t.Fatal(err)
	}
	before := len(f.hub.Requests("/resolve/")) + len(f.hub.Requests("/cdn/"))
	w2 := weight("base", "org/shared", "SomewhereElse")
	if err := f.svc.Fetch(context.Background(), "second", w2, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(f.hub.Requests("/resolve/")) + len(f.hub.Requests("/cdn/")); after != before {
		t.Fatalf("the second studio downloaded file bytes: %d → %d requests", before, after)
	}
	values, _, _ := f.svc.Launch(context.Background(), "second", []manifest.Weight{w2})
	if !strings.HasSuffix(values["models.base"], "Shared") {
		t.Fatalf("the second studio resolves to %q, want the existing Shared directory", values["models.base"])
	}
	art, _ := f.svc.Get(context.Background(), mustID(f, "org/shared"))
	if art.RefCount != 2 || strings.Join(art.Studios, ",") != "first,second" {
		t.Fatalf("artifact = %+v; want two references", art)
	}
	var cols int
	f.st.Reader().QueryRow(`SELECT count(*) FROM pragma_table_info('model_artifacts') WHERE name LIKE '%ref%'`).Scan(&cols)
	if cols != 0 {
		t.Fatal("model_artifacts has a reference-count column; references must be counted from bindings")
	}
}

// A different repository cannot take a dest that is already used.
func TestDestOfAnotherRepoIsRefused(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("org/one", "a.bin", hubtest.Content(10, 1), false)
	f.hub.Add("org/two", "b.bin", hubtest.Content(10, 2), false)
	f.install("s")
	if err := f.svc.Fetch(context.Background(), "s", weight("one", "org/one", "Same"), nil); err != nil {
		t.Fatal(err)
	}
	err := f.svc.Fetch(context.Background(), "s", weight("two", "org/two", "Same"), nil)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "org/one") {
		t.Fatalf("err = %v, want a conflict naming org/one", err)
	}
}

// R17: a download that would not fit is refused with the numbers, before a
// byte moves.
func TestDownloadThatWouldNotFitIsRefused(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("org/huge", "model.safetensors", hubtest.Content(1<<20, 5), true)
	f.install("s")
	f.free = 1 << 30 // 1 GiB free; 1 MiB + 2 GiB headroom needed
	err := f.svc.Fetch(context.Background(), "s", weight("m", "org/huge", "Huge"), nil)
	if !errors.Is(err, ErrDiskSpace) || !strings.Contains(err.Error(), "1.0 GiB free") || !strings.Contains(err.Error(), "2.0 GiB headroom") {
		t.Fatalf("err = %v, want a refusal with the numbers", err)
	}
	if n := len(f.hub.Requests("/cdn/")); n != 0 {
		t.Fatalf("%d file requests were made before the disk check refused", n)
	}
	if Headroom(100<<30) != 5<<30 || Headroom(1) != 2<<30 {
		t.Fatalf("headroom is max(2 GiB, 5%%): got %d and %d", Headroom(100<<30), Headroom(1))
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Resume appends only when the server confirms it is sending the next byte:
// a 206 that starts anywhere else is refused, and the partial file is left
// exactly as it was.
func TestResumeRefusesAServerSendingTheWrongRange(t *testing.T) {
	f := newFixture(t)
	const size = 64 * 1024
	f.hub.Add("org/range", "model.safetensors", hubtest.Content(size, 6), true)
	f.hub.CutAfter["model.safetensors"] = size / 4
	f.hub.IgnoreRange["model.safetensors"] = true
	f.install("s")
	err := f.svc.Fetch(context.Background(), "s", weight("m", "org/range", "Range"), nil)
	if err == nil || !strings.Contains(err.Error(), "not appending") {
		t.Fatalf("err = %v; want a refusal to append a wrong range", err)
	}
	part := filepath.Join(f.dirs.Models(), "Range", "model.safetensors.part")
	if fi, err := os.Stat(part); err != nil || fi.Size() != size/4 {
		t.Fatalf("partial file = %v, %v; want the %d bytes from before, untouched", fi, err, size/4)
	}
}

// Review M3 #2: dests may not nest. Reclaiming "Shared" would otherwise
// remove the files of an artifact at "Shared/child" without listing them.
func TestNestedDestsAreRefusedAndReclaimNeverDeletesAnotherArtifact(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.hub.Add("org/parent", "parent.bin", hubtest.Content(100, 1), true)
	f.hub.Add("org/child", "model.bin", hubtest.Content(100, 2), true)
	f.install("parent-studio")
	f.install("child-studio")
	if err := f.svc.Fetch(ctx, "parent-studio", weight("p", "org/parent", "Shared"), nil); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"Shared/child", "Shared"} {
		err := f.svc.Fetch(ctx, "child-studio", weight("c", "org/child", dest), nil)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "overlaps") {
			t.Fatalf("dest %s inside or equal to Shared: err = %v, want an overlap refusal", dest, err)
		}
	}
	if _, err := f.svc.Link(ctx, "child-studio", weight("c", "org/child", "Shared/deeper/child", "FL2VA/**"), userCheckpoint(t)); !errors.Is(err, ErrConflict) {
		t.Fatalf("linking inside Shared: err = %v, want ErrConflict", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "Shared", "deeper")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused link created something inside another artifact's directory: %v", err)
	}
	if err := f.svc.Fetch(ctx, "child-studio", weight("c", "org/child", "Outer"), nil); err != nil {
		t.Fatal(err)
	}

	// A database written before this check can still hold nested dests:
	// reclaim refuses rather than delete the inner artifact's files.
	if err := f.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET local_path = 'Shared/child' WHERE hf_repo = 'org/child'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(f.dirs.Models(), "Shared", "child"), 0o755)
	if err := os.Rename(filepath.Join(f.dirs.Models(), "Outer", "model.bin"), filepath.Join(f.dirs.Models(), "Shared", "child", "model.bin")); err != nil {
		t.Fatal(err)
	}
	f.uninstall("parent-studio")
	p, _ := f.svc.PreviewReclaim(ctx)
	if len(p.Items) != 1 || p.Items[0].HFRepo != "org/parent" {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := f.svc.Reclaim(ctx, p.Confirm); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "org/child") {
		t.Fatalf("reclaim over a nested artifact: err = %v, want a refusal naming org/child", err)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Shared", "child", "model.bin")); err != nil {
		t.Fatalf("the inner artifact's file is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Shared", "parent.bin")); err != nil {
		t.Fatalf("a refused reclaim deleted the outer artifact: %v", err)
	}
}
