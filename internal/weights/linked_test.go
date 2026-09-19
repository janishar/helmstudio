package weights

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

// snapshot records everything a write could change under dir: every entry's
// path, type, mode, size, modification time and, for files, content hash.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		line := fmt.Sprintf("%s %v %d %d", rel, fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			line += " -> " + target
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			line += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// userCheckpoint builds a directory a user already has, outside helmstudio.
func userCheckpoint(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Checkpoints", "MiniMax-H3")
	for rel, n := range map[string]int{"FL2VA/config.json": 20, "FL2VA/dit.safetensors": 4096, "Ref2VA/dit.safetensors": 2048} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, hubtest.Content(n, byte(n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// DoD: a linked weight directory is never written to. Every operation that
// touches a linked artifact runs against it, including the ones that must
// refuse, and the directory is byte-for-byte, mode-for-mode and
// mtime-for-mtime what it was.
func TestLinkedDirectoryIsNeverWrittenTo(t *testing.T) {
	f := newFixture(t)
	f.hub.Add("MiniMaxAI/MiniMax-H3", "FL2VA/dit.safetensors", hubtest.Content(64, 1), true)
	user := userCheckpoint(t)
	before := snapshot(t, filepath.Dir(user))
	ctx := context.Background()

	fl := weight("fl2va", "MiniMaxAI/MiniMax-H3", "MiniMax-H3", "FL2VA/**")
	ref := weight("ref2va", "MiniMaxAI/MiniMax-H3", "MiniMax-H3", "Ref2VA/**")
	missing := weight("ref2va", "MiniMaxAI/MiniMax-H3", "MiniMax-H3", "Extra/**")

	// Linking before install records the artifact and binds nothing.
	art, err := f.svc.Link(ctx, "h3-studio", fl, user)
	if err != nil {
		t.Fatal(err)
	}
	if art.Source != SourceLinked || art.State != StateLinked || art.RefCount != 0 || art.VerifiedAt != nil {
		t.Fatalf("linked artifact = %+v; want linked, unverified and unbound", art)
	}
	if target, _ := os.Readlink(filepath.Join(f.dirs.Models(), "MiniMax-H3")); target != mustReal(t, user) {
		t.Fatalf("models/MiniMax-H3 links to %q, want %q", target, user)
	}
	// Linking again is idempotent.
	if _, err := f.svc.Link(ctx, "h3-studio", fl, user); err != nil {
		t.Fatalf("relinking the same directory: %v", err)
	}
	f.install("h3-studio")
	// Install binds to the linked artifact and downloads nothing.
	if err := f.svc.Fetch(ctx, "h3-studio", fl, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Fetch(ctx, "h3-studio", ref, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(f.hub.Requests("/")); n != 0 {
		t.Fatalf("a linked weight made %d requests to Hugging Face", n)
	}
	// A binding whose files the linked directory lacks is refused, never
	// completed in place.
	if err := f.svc.Fetch(ctx, "h3-studio", missing, nil); err == nil || !strings.Contains(err.Error(), "Extra/**") {
		t.Fatalf("binding a weight the linked directory lacks: err = %v; want a refusal naming Extra/**", err)
	}
	values, refuse, err := f.svc.Launch(ctx, "h3-studio", []manifest.Weight{fl, ref})
	if err != nil || len(refuse) != 0 || values["models.fl2va"] != mustReal(t, user) {
		t.Fatalf("launch: values=%v refuse=%v err=%v", values, refuse, err)
	}
	if _, err := f.svc.List(ctx); err != nil {
		t.Fatal(err)
	}
	// Unlink refuses while bound, and reclaim never includes a linked row.
	if err := f.svc.Unlink(ctx, art.ID); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "h3-studio") {
		t.Fatalf("unlink while bound: err = %v; want ErrInUse naming the studio", err)
	}
	p, _ := f.svc.PreviewReclaim(ctx)
	if len(p.Items) != 0 {
		t.Fatalf("reclaim would delete %+v; a linked artifact is never reclaimable", p.Items)
	}
	f.uninstall("h3-studio")
	p, _ = f.svc.PreviewReclaim(ctx)
	if _, err := f.svc.Reclaim(ctx, p.Confirm); err != nil || len(p.Items) != 0 {
		t.Fatalf("reclaim after uninstall: %+v %v", p, err)
	}
	if err := f.svc.Unlink(ctx, art.ID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "MiniMax-H3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link is still there after unlink: %v", err)
	}

	if after := snapshot(t, filepath.Dir(user)); after != before {
		t.Fatalf("the linked directory changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// R14a and R14c: a directory that lacks the declared files, or holds far less
// than the declared size, is refused with what is missing; so is linking over
// a managed download.
func TestLinkChecksTheDirectoryAndNeverReplacesADownload(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)

	_, err := f.svc.Link(ctx, "s", weight("x", "org/x", "X", "FL2VA/**", "Missing/*.bin"), user)
	if err == nil || !strings.Contains(err.Error(), "Missing/*.bin") {
		t.Fatalf("err = %v; want a refusal naming the missing pattern", err)
	}
	big := weight("x", "org/x", "X", "FL2VA/**")
	big.SizeGB = 60
	if _, err := f.svc.Link(ctx, "s", big, user); err == nil || !strings.Contains(err.Error(), "60 GB") {
		t.Fatalf("err = %v; want a refusal stating the declared 60 GB", err)
	}
	if _, err := f.svc.Link(ctx, "s", weight("x", "org/x", "X"), "relative/path"); err == nil {
		t.Fatal("a relative path was accepted")
	}

	f.hub.Add("org/dl", "model.safetensors", hubtest.Content(128, 1), true)
	f.install("s")
	if err := f.svc.Fetch(ctx, "s", weight("m", "org/dl", "Downloaded"), nil); err != nil {
		t.Fatal(err)
	}
	downloaded := snapshot(t, filepath.Join(f.dirs.Models(), "Downloaded"))
	_, err = f.svc.Link(ctx, "s", weight("m", "org/dl", "Downloaded", "FL2VA/**"), user)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("linking over a download: err = %v, want ErrConflict", err)
	}
	if snapshot(t, filepath.Join(f.dirs.Models(), "Downloaded")) != downloaded {
		t.Fatal("a refused link changed the downloaded copy")
	}
}

// R19b: a linked directory that disappears makes the artifact missing and
// refuses the launch with the path — not an error, not a download — and it
// comes back when the directory does.
func TestLinkedDirectoryThatDisappearsIsMissing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)
	w := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	f.install("s")
	art, err := f.svc.Link(ctx, "s", w, user)
	if err != nil {
		t.Fatal(err)
	}
	unplugged := user + "-unplugged"
	if err := os.Rename(user, unplugged); err != nil {
		t.Fatal(err)
	}
	values, refuse, err := f.svc.Launch(ctx, "s", []manifest.Weight{w})
	if err != nil {
		t.Fatalf("a missing link is a refusal, not an error: %v", err)
	}
	if _, ok := values["models.fl2va"]; ok || !strings.Contains(refuse["models.fl2va"], user) || !strings.Contains(refuse["models.fl2va"], "not there") {
		t.Fatalf("values=%v refuse=%v; want a refusal naming %s as not there", values, refuse, user)
	}
	// Read the row itself: launch must record missing, not only a listing.
	if _, state, _, _ := f.artifactState("org/h3"); state != StateMissing {
		t.Fatalf("state after launch = %q, want missing", state)
	}
	if n := len(f.hub.Requests("/")); n != 0 {
		t.Fatalf("a missing link started a download: %d requests", n)
	}
	if err := os.Rename(unplugged, user); err != nil {
		t.Fatal(err)
	}
	values, refuse, _ = f.svc.Launch(ctx, "s", []manifest.Weight{w})
	if values["models.fl2va"] != mustReal(t, user) || len(refuse) != 0 {
		t.Fatalf("after reconnecting: values=%v refuse=%v", values, refuse)
	}
	if got, _ := f.svc.Get(ctx, art.ID); got.State != StateLinked {
		t.Fatalf("state after reconnecting = %q, want linked", got.State)
	}
}

// DoD: reclaim's preview matches what it deletes exactly. Unreferenced
// downloads are listed with their bytes; a referenced one and a linked one
// are not; a stale confirmation deletes nothing.
func TestReclaimDeletesExactlyItsPreview(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.hub.Add("org/a", "a.bin", hubtest.Content(1000, 1), true)
	f.hub.Add("org/b", "b.bin", hubtest.Content(3000, 2), true)
	f.hub.Add("org/kept", "k.bin", hubtest.Content(500, 3), true)
	f.install("old")
	f.install("current")
	for _, w := range []manifest.Weight{weight("a", "org/a", "A"), weight("b", "org/b", "B")} {
		if err := f.svc.Fetch(ctx, "old", w, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.Fetch(ctx, "current", weight("k", "org/kept", "Kept"), nil); err != nil {
		t.Fatal(err)
	}
	user := userCheckpoint(t)
	if _, err := f.svc.Link(ctx, "nobody", weight("l", "org/linked", "Linked", "FL2VA/**"), user); err != nil {
		t.Fatal(err)
	}

	empty, _ := f.svc.PreviewReclaim(ctx)
	if len(empty.Items) != 0 {
		t.Fatalf("with every download referenced, reclaim offers %+v", empty.Items)
	}
	f.uninstall("old")
	p, err := f.svc.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range p.Items {
		names = append(names, it.HFRepo)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "org/a,org/b" || p.TotalBytes != 4000 {
		t.Fatalf("preview = %+v; want org/a and org/b, 4000 bytes", p)
	}

	if _, err := f.svc.Reclaim(ctx, "stale"); !errors.Is(err, ErrPreviewChanged) {
		t.Fatalf("stale confirmation: err = %v, want ErrPreviewChanged", err)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "A", "a.bin")); err != nil {
		t.Fatalf("a refused reclaim deleted something: %v", err)
	}

	done, err := f.svc.Reclaim(ctx, p.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(done) != fmt.Sprint(p) {
		t.Fatalf("reclaimed %+v, previewed %+v", done, p)
	}
	for _, it := range p.Items {
		if _, err := os.Lstat(it.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived reclaim: %v", it.Path, err)
		}
	}
	for _, kept := range []string{filepath.Join(f.dirs.Models(), "Kept", "k.bin"), filepath.Join(user, "FL2VA", "dit.safetensors")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("reclaim removed %s: %v", kept, err)
		}
	}
	arts, _ := f.svc.List(ctx)
	if len(arts) != 2 {
		t.Fatalf("artifacts after reclaim = %+v; want the referenced and the linked one", arts)
	}
	// A reclaim whose set changed after the preview deletes nothing.
	f.uninstall("current")
	if _, err := f.svc.Reclaim(ctx, p.Confirm); !errors.Is(err, ErrPreviewChanged) {
		t.Fatalf("err = %v, want ErrPreviewChanged once another artifact became reclaimable", err)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Kept", "k.bin")); err != nil {
		t.Fatalf("a reclaim confirmed for a different set deleted Kept: %v", err)
	}
}

// No delete path in this package follows a symlink: reclaim of a download
// holding a symlink, reclaim of a download whose directory was replaced by a
// symlink, unlink, and a partial file that is a symlink. Each points at a
// directory with a sentinel file, which must survive.
func TestNoWeightsDeletePathFollowsASymlink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sub", "sentinel.safetensors")
	os.WriteFile(sentinel, []byte("the user's"), 0o644)
	before := snapshot(t, outside)

	// 1. Reclaim of a download with a symlink inside it.
	f.hub.Add("org/one", "model.bin", hubtest.Content(100, 1), true)
	f.install("s")
	if err := f.svc.Fetch(ctx, "s", weight("one", "org/one", "One"), nil); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(f.dirs.Models(), "One", "nested", "escape")
	os.MkdirAll(filepath.Dir(inner), 0o755)
	if err := os.Symlink(outside, inner); err != nil {
		t.Fatal(err)
	}

	// 2. A download whose directory has been replaced by a symlink.
	f.hub.Add("org/two", "model.bin", hubtest.Content(100, 2), true)
	if err := f.svc.Fetch(ctx, "s", weight("two", "org/two", "Two"), nil); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(f.dirs.Models(), "Two")
	os.RemoveAll(two)
	if err := os.Symlink(outside, two); err != nil {
		t.Fatal(err)
	}

	f.uninstall("s")
	p, _ := f.svc.PreviewReclaim(ctx)
	if _, err := f.svc.Reclaim(ctx, p.Confirm); !errors.Is(err, ErrConflict) {
		t.Fatalf("reclaim over a symlinked download directory: err = %v, want ErrConflict before deleting anything", err)
	}
	if _, err := os.Lstat(inner); err != nil {
		t.Fatalf("the refused reclaim deleted from another artifact first: %v", err)
	}
	os.Remove(two) // the user fixes it; now reclaim runs
	p, _ = f.svc.PreviewReclaim(ctx)
	if _, err := f.svc.Reclaim(ctx, p.Confirm); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "One")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("One survived reclaim: %v", err)
	}

	// 3. Unlink.
	user := userCheckpoint(t)
	linkedSentinel := snapshot(t, user)
	art, err := f.svc.Link(ctx, "nobody", weight("l", "org/linked", "Linked", "FL2VA/**"), user)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Unlink(ctx, art.ID); err != nil {
		t.Fatal(err)
	}
	if snapshot(t, user) != linkedSentinel {
		t.Fatal("unlink changed the linked directory")
	}

	// 4. A partial file that is a symlink is never written through or
	// truncated.
	f.hub.Add("org/three", "model.bin", hubtest.Content(100, 3), true)
	f.install("s")
	three := filepath.Join(f.dirs.Models(), "Three")
	os.MkdirAll(three, 0o755)
	if err := os.Symlink(sentinel, filepath.Join(three, "model.bin.part")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Fetch(ctx, "s", weight("three", "org/three", "Three"), nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("fetch into a symlinked partial file: err = %v, want ErrConflict", err)
	}

	if after := snapshot(t, outside); after != before {
		t.Fatalf("a delete path followed a symlink.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Review M3 #6: when a weight of h3's shape cannot be completed from a
// partial link or a partial download, the refusal names the steps that are
// actually possible — never an unlink or a reclaim that is itself refused
// while the studio is installed.
func TestRefusalsNeverOfferARefusedStep(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fl := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	ref := weight("ref2va", "org/h3", "MiniMax-H3", "Ref2VA/**")

	// (a) Link a directory holding only FL2VA, then fetch ref2va.
	onlyFL := filepath.Join(t.TempDir(), "only-fl2va")
	os.MkdirAll(filepath.Join(onlyFL, "FL2VA"), 0o755)
	os.WriteFile(filepath.Join(onlyFL, "FL2VA", "dit.safetensors"), []byte("x"), 0o644)
	f.install("h3-studio")
	art, err := f.svc.Link(ctx, "h3-studio", fl, onlyFL)
	if err != nil {
		t.Fatal(err)
	}
	err = f.svc.Fetch(ctx, "h3-studio", ref, nil)
	if err == nil || !strings.Contains(err.Error(), "uninstall h3-studio") || strings.Contains(err.Error(), "or unlink it and download") {
		t.Fatalf("fetching ref2va against a partial link: %v", err)
	}
	if uerr := f.svc.Unlink(ctx, art.ID); !errors.Is(uerr, ErrInUse) {
		t.Fatalf("unlink while bound: %v; the message above must not have offered it", uerr)
	}
	f.uninstall("h3-studio")
	if err := f.svc.Unlink(ctx, art.ID); err != nil {
		t.Fatalf("the step the message named (uninstall, then unlink) is refused: %v", err)
	}

	// (b) Download FL2VA, then link ref2va to a directory holding Ref2VA.
	f.hub.Add("org/h3", "FL2VA/dit.safetensors", hubtest.Content(64, 1), true)
	f.install("h3-studio")
	if err := f.svc.Fetch(ctx, "h3-studio", fl, nil); err != nil {
		t.Fatal(err)
	}
	onlyRef := filepath.Join(t.TempDir(), "only-ref2va")
	os.MkdirAll(filepath.Join(onlyRef, "Ref2VA"), 0o755)
	os.WriteFile(filepath.Join(onlyRef, "Ref2VA", "dit.safetensors"), []byte("y"), 0o644)
	_, err = f.svc.Link(ctx, "h3-studio", ref, onlyRef)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "uninstall h3-studio (which use it), then reclaim it") {
		t.Fatalf("linking over a bound download: %v", err)
	}
	if p, _ := f.svc.PreviewReclaim(ctx); len(p.Items) != 0 {
		t.Fatalf("preview while bound = %+v", p)
	}
	f.uninstall("h3-studio")
	if p, _ := f.svc.PreviewReclaim(ctx); len(p.Items) != 1 {
		t.Fatalf("after the uninstall the message named, reclaim offers %+v; want the download", p)
	}
}

// Review M3 #6, the human's decision: a link must hold every required weight
// of the repository it stands for, and names the optional ones it lacks,
// which cannot be fetched while it stands.
func TestLinkRequiresEveryRequiredWeightOfTheRepository(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fl := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	ref := weight("ref2va", "org/h3", "MiniMax-H3", "Ref2VA/**")
	ref.Optional = true
	other := weight("vae", "org/other", "Other") // another repository: not the link's business
	h3 := []manifest.Weight{fl, ref, other}

	onlyFL := filepath.Join(t.TempDir(), "only-fl2va")
	os.MkdirAll(filepath.Join(onlyFL, "FL2VA"), 0o755)
	os.WriteFile(filepath.Join(onlyFL, "FL2VA", "dit.safetensors"), []byte("x"), 0o644)
	onlyRef := filepath.Join(t.TempDir(), "only-ref2va")
	os.MkdirAll(filepath.Join(onlyRef, "Ref2VA"), 0o755)
	os.WriteFile(filepath.Join(onlyRef, "Ref2VA", "dit.safetensors"), []byte("y"), 0o644)

	// Linking the optional weight to a directory without the required one
	// is refused, naming it.
	if _, err := f.svc.Link(ctx, "h3-studio", ref, onlyRef, h3...); err == nil || !strings.Contains(err.Error(), `"fl2va"`) || !strings.Contains(err.Error(), "required") {
		t.Fatalf("linking ref2va without FL2VA: err = %v; want a refusal naming fl2va as required", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "MiniMax-H3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused link left a symlink: %v", err)
	}

	// A directory with only the required weight links, and says the
	// optional one is unobtainable.
	art, err := f.svc.Link(ctx, "h3-studio", fl, onlyFL, h3...)
	if err != nil {
		t.Fatal(err)
	}
	if len(art.Unobtainable) != 1 || !strings.HasPrefix(art.Unobtainable[0], "ref2va:") || !strings.Contains(art.Unobtainable[0], "Ref2VA/**") {
		t.Fatalf("unobtainable = %q; want ref2va named with its pattern", art.Unobtainable)
	}
}

// Used marks the weights of the studio that went running and no other
// studio's: "Last used" on a model nobody launched would be a lie.
func TestUsedMarksOnlyThatStudiosWeights(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.install("s")
	f.install("other")
	mine, err := f.svc.Link(ctx, "s", weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**"), userCheckpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := f.svc.Link(ctx, "other", weight("fl2va", "org/h3-other", "MiniMax-H3-other", "FL2VA/**"), userCheckpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Used(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, mine.ID); got.LastUsedAt == nil {
		t.Error("the studio that went running has no last-used time on its weights")
	}
	if got, _ := f.svc.Get(ctx, theirs.ID); got.LastUsedAt != nil {
		t.Errorf("another studio's weights were marked used at %v", got.LastUsedAt)
	}
}

// Before anything is downloaded the models directory does not exist, and the
// free space asked about is that of the volume it will be made on — not an
// error, and not a directory created just to measure it.
func TestDiskMeasuresTheVolumeTheModelsDirectoryWillBeOn(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.dirs.Models()); err != nil {
		t.Fatal(err)
	}
	var asked string
	svc := New(Config{Store: f.st, Dirs: f.dirs, FreeDisk: func(p string) (uint64, error) {
		asked = p
		return 285 << 30, nil
	}})
	d, err := svc.Disk()
	if err != nil {
		t.Fatal(err)
	}
	if d.Root != f.dirs.Models() || d.FreeBytes != 285<<30 {
		t.Errorf("disk = %+v; want %s with 285 GiB free", d, f.dirs.Models())
	}
	if fi, err := os.Stat(asked); err != nil || !fi.IsDir() {
		t.Errorf("free space was asked of %q, which is not a directory: %v", asked, err)
	}
	if !strings.HasPrefix(f.dirs.Models(), asked) {
		t.Errorf("free space was asked of %q, which does not hold %s", asked, f.dirs.Models())
	}
	if _, err := os.Stat(f.dirs.Models()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("measuring created the models directory: %v", err)
	}
}

// A weight whose manifest names a directory on this machine is linked from it,
// one file at a time, and never downloaded (docs/decisions.md, per-weight
// local_path). The studio is handed a directory holding exactly the files its
// manifest declares — not the user's directory, which holds more — and the
// user's directory is not touched at all.
func TestLocalPathLinksTheFilesTheManifestNames(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)
	before := snapshot(t, user)
	f.install("s")

	w := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	w.LocalPath = user
	// The funnel install itself uses: a weight with local_path never reaches
	// Hugging Face.
	if err := f.svc.Fetch(ctx, "s", w, nil); err != nil {
		t.Fatalf("linking from %s: %v", user, err)
	}
	if n := len(f.hub.Requests("/")); n != 0 {
		t.Fatalf("a local_path weight contacted Hugging Face: %d requests", n)
	}

	dest := filepath.Join(f.dirs.Models(), "MiniMax-H3")
	real := mustReal(t, user)
	for _, rel := range []string{"FL2VA/dit.safetensors", "FL2VA/config.json"} {
		link := filepath.Join(dest, rel)
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("%s is not a link: %v", link, err)
		}
		target, _ := os.Readlink(link)
		if target != filepath.Join(real, rel) {
			t.Errorf("%s points at %s, want %s", link, target, filepath.Join(real, rel))
		}
	}
	// Only what the weight declares. Ref2VA is in the directory and in no
	// weight of this manifest, so it is not exposed to the studio.
	if _, err := os.Lstat(filepath.Join(dest, "Ref2VA/dit.safetensors")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a file the weight does not declare was linked: %v", err)
	}

	// The artifact is linked: no bytes are counted, and the path it came from
	// is what Models & disk shows.
	arts, err := f.svc.List(ctx)
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts: %v %v", arts, err)
	}
	if a := arts[0]; a.Source != SourceLinked || a.BytesOnDisk != 0 || a.ExternalPath != user {
		t.Errorf("artifact = %+v; want linked from %s with no bytes counted", a, user)
	}

	// The studio is handed the directory of links.
	values, refuse, err := f.svc.Launch(ctx, "s", []manifest.Weight{w})
	if err != nil || len(refuse) != 0 || values["models.fl2va"] != dest {
		t.Fatalf("launch: values=%v refuse=%v err=%v; want %s", values, refuse, err, dest)
	}

	if after := snapshot(t, user); after != before {
		t.Errorf("the user's directory was written to:\n%s", after)
	}
}

// A directory that lacks a file the weight declares is refused, by name, and
// nothing is downloaded to make up the difference: half a weight from a folder
// and half from Hugging Face is a state nobody asked for.
func TestLocalPathRefusesWhatTheDirectoryLacks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)
	f.install("s")

	w := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**", "duration_head/head.safetensors")
	w.LocalPath = user
	err := f.svc.Fetch(ctx, "s", w, nil)
	if err == nil {
		t.Fatal("a directory missing a declared file was accepted")
	}
	if !strings.Contains(err.Error(), "duration_head/head.safetensors") {
		t.Errorf("the refusal does not name the missing file: %v", err)
	}
	if n := len(f.hub.Requests("/")); n != 0 {
		t.Errorf("a refused link downloaded something: %d requests", n)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "MiniMax-H3")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused link left %s behind: %v", filepath.Join(f.dirs.Models(), "MiniMax-H3"), err)
	}
}

// The layout MiniMax-H3 actually ships: FL2VA's tokenizer, processor, text
// encoder and VAEs are symlinks to Ref2VA's, so seventy gigabytes are not on
// the disk twice. A walk that does not follow them linked FL2VA without its
// tokenizer, and the first render failed on a file the install had just
// reported present.
func TestLocalPathFollowsADeduplicatedCheckpoint(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)
	real := filepath.Join(user, "Ref2VA", "tokenizer")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "tokenizer.json"), hubtest.Content(64, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "Ref2VA", "tokenizer"), filepath.Join(user, "FL2VA", "tokenizer")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, user)
	f.install("s")

	w := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	w.LocalPath = user
	if err := f.svc.Fetch(ctx, "s", w, nil); err != nil {
		t.Fatalf("linking %s: %v", user, err)
	}
	link := filepath.Join(f.dirs.Models(), "MiniMax-H3", "FL2VA", "tokenizer", "tokenizer.json")
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("the file behind a symlinked subdirectory was not linked: %v", err)
	}
	if b, err := os.ReadFile(link); err != nil || len(b) != 64 {
		t.Errorf("the link does not resolve to the file: %d bytes, %v", len(b), err)
	}
	if after := snapshot(t, user); after != before {
		t.Errorf("the user's directory was written to:\n%s", after)
	}
}

// A subdirectory linked out of the directory refuses it, naming where it
// points. Following it would hand the studio files from a directory nobody
// named; a file symlink is the other way round — a Hugging Face snapshot's
// files all point at ../../blobs — and still counts by its target.
func TestLocalPathRefusesASubdirectoryLinkedOutside(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := userCheckpoint(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "tokenizer.json"), hubtest.Content(32, 3), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(user, "FL2VA", "tokenizer")); err != nil {
		t.Fatal(err)
	}
	f.install("s")

	w := weight("fl2va", "org/h3", "MiniMax-H3", "FL2VA/**")
	w.LocalPath = user
	err := f.svc.Fetch(ctx, "s", w, nil)
	if err == nil {
		t.Fatal("a subdirectory linked outside the directory was accepted")
	}
	if !strings.Contains(err.Error(), mustReal(t, outside)) {
		t.Errorf("the refusal does not say where the link points: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "MiniMax-H3")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused link left the destination behind: %v", err)
	}
}
