package media

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func engine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	return &Engine{Assets: filepath.Join(root, "assets"), Derived: filepath.Join(root, "cache", "derived"), Library: filepath.Join(root, "library")}, root
}

func ino(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// Adoption is a hardlink: the blob and the library file are the studio's
// inode, read-only, and no bytes were copied.
func TestLinkPlaceAndLibraryShareOneInode(t *testing.T) {
	e, root := engine(t)
	src := filepath.Join(root, "stage", "take.mp4")
	os.MkdirAll(filepath.Dir(src), 0o700)
	os.WriteFile(src, []byte("take"), 0o644)
	want := ino(t, src)

	st, err := e.Link(src)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := e.Place(st, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	lib, warn, err := e.LibraryLink(rel, "h3-studio", time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), "take.mp4")
	if err != nil || warn != "" {
		t.Fatal(err, warn)
	}
	if lib != "h3-studio/2026-09/take.mp4" || !strings.HasPrefix(rel, "blobs/") {
		t.Fatalf("library %q, blob %q", lib, rel)
	}
	for _, p := range []string{e.BlobPath(rel), filepath.Join(e.Library, lib), src} {
		if ino(t, p) != want {
			t.Fatalf("%s is inode %d; want %d: a copy was made", p, ino(t, p), want)
		}
	}
	if fi, _ := os.Stat(src); fi.Mode().Perm() != 0o444 {
		t.Fatalf("mode %v; want 0444", fi.Mode())
	}
	// A second name for the same library file gets a suffix.
	lib2, _, _ := e.LibraryLink(rel, "h3-studio", time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), "take.mp4")
	if lib2 != "h3-studio/2026-09/take-2.mp4" {
		t.Fatalf("second library name = %q", lib2)
	}
}

// A hardlink across volumes is refused, and nothing is copied instead: no file
// appears in the store and the source keeps its mode (Q11).
func TestCrossDeviceAdoptionNeverFallsBackToACopy(t *testing.T) {
	e, root := engine(t)
	e.LinkFunc = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EXDEV}
	}
	src := filepath.Join(root, "stage", "big.mp4")
	os.MkdirAll(filepath.Dir(src), 0o700)
	os.WriteFile(src, bytes.Repeat([]byte("x"), 1<<16), 0o640)

	if _, err := e.Link(src); !errors.Is(err, ErrCrossDevice) {
		t.Fatalf("Link across volumes: %v; want ErrCrossDevice", err)
	}
	entries, _ := os.ReadDir(e.tmpDir())
	if len(entries) != 0 {
		t.Fatalf("the store holds %d file(s) after a refused cross-device adopt", len(entries))
	}
	if fi, _ := os.Stat(src); fi.Mode().Perm() != 0o640 {
		t.Fatalf("the source's mode changed to %v", fi.Mode())
	}

	// A library on another volume: the asset is kept, the library file is not
	// made, and the caller is told.
	e2, root2 := engine(t)
	blobSrc := filepath.Join(root2, "stage", "t.mp4")
	os.MkdirAll(filepath.Dir(blobSrc), 0o700)
	os.WriteFile(blobSrc, []byte("t"), 0o644)
	st, err := e2.Link(blobSrc)
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := e2.Place(st, ".mp4")
	e2.LinkFunc = func(oldname, newname string) error { return &os.LinkError{Err: syscall.EXDEV} }
	lib, warn, err := e2.LibraryLink(rel, "s", time.Now(), "t.mp4")
	if err != nil || lib != "" || !strings.Contains(warn, "never copies") {
		t.Fatalf("library across volumes = %q, %q, %v", lib, warn, err)
	}
}

func TestResolveRefusesSymlinksAndEscapes(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	os.MkdirAll(filepath.Join(stage, "real"), 0o700)
	os.WriteFile(filepath.Join(stage, "real", "f"), []byte("x"), 0o600)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	os.Symlink(outside, filepath.Join(stage, "link"))
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(stage, "filelink"))

	if _, err := Resolve(stage, filepath.Join(stage, "real", "f")); err != nil {
		t.Fatalf("a real file: %v", err)
	}
	for _, p := range []string{
		filepath.Join(stage, "link", "secret"),
		filepath.Join(stage, "filelink"),
		filepath.Join(outside, "secret"),
		filepath.Join(stage, "..", "stage", "..", "x"),
		"relative/f",
	} {
		if _, err := Resolve(stage, p); !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(%s) = %v; want ErrOutside", p, err)
		}
	}
	if _, err := Resolve(stage, filepath.Join(stage, "real")); err == nil || errors.Is(err, ErrOutside) {
		t.Errorf("a directory: %v; want a not-a-regular-file error", err)
	}
}

// Remove deletes a library file only while it is still the blob, never
// follows a symlink, and a discarded duplicate gets its mode back.
func TestRemoveKeepsAReplacedLibraryFileAndDiscardRestoresMode(t *testing.T) {
	e, root := engine(t)
	src := filepath.Join(root, "stage", "a.png")
	os.MkdirAll(filepath.Dir(src), 0o700)
	os.WriteFile(src, []byte("a"), 0o644)
	st, _ := e.Link(src)
	rel, _ := e.Place(st, ".png")
	lib, _, _ := e.LibraryLink(rel, "s", time.Now(), "a.png")

	// The user replaced the library file with their own.
	libPath := filepath.Join(e.Library, lib)
	os.Remove(libPath)
	os.WriteFile(libPath, []byte("the user's own file"), 0o644)
	removed, err := e.Remove(rel, lib, st.SHA256)
	if err != nil || removed.KeptLibraryPath != lib {
		t.Fatalf("Remove = %+v, %v", removed, err)
	}
	if b, _ := os.ReadFile(libPath); string(b) != "the user's own file" {
		t.Fatal("Remove deleted a file the user put in the library")
	}
	if _, err := os.Lstat(e.BlobPath(rel)); !os.IsNotExist(err) {
		t.Fatal("the blob is still there")
	}

	// A duplicate linked in and discarded gets its mode back.
	dup := filepath.Join(root, "data", "dup.png")
	os.MkdirAll(filepath.Dir(dup), 0o700)
	os.WriteFile(dup, []byte("dup"), 0o640)
	st2, err := e.Link(dup)
	if err != nil {
		t.Fatal(err)
	}
	e.Discard(st2)
	if fi, _ := os.Stat(dup); fi.Mode().Perm() != 0o640 {
		t.Fatalf("a discarded duplicate's mode is %v; want 0640 back", fi.Mode())
	}

	// A symlinked library directory is not followed by Remove.
	victim := t.TempDir()
	os.WriteFile(filepath.Join(victim, "keep.png"), []byte("k"), 0o644)
	os.MkdirAll(filepath.Join(e.Library, "evil"), 0o755)
	os.Remove(filepath.Join(e.Library, "evil"))
	os.Symlink(victim, filepath.Join(e.Library, "evil"))
	if _, err := e.Remove("blobs/none", "evil/keep.png", ""); err != nil {
		t.Logf("Remove through a symlinked library directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(victim, "keep.png")); err != nil {
		t.Fatal("Remove followed a symlink out of the library")
	}
}

// Second review #7: a blob adopted from a studio's own file is linked outside
// the store, so removing it frees nothing; one staged and unlinked is not.
func TestLinkedOutsideSeesAStudiosOwnLink(t *testing.T) {
	e, root := engine(t)
	kept := filepath.Join(root, "data", "outputs", "take.mp4")
	staged := filepath.Join(root, "stage", "other.mp4")
	for _, p := range []string{kept, staged} {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(p), 0o644)
	}
	stKept, _ := e.Link(kept)
	relKept, _ := e.Place(stKept, ".mp4")
	libKept, _, _ := e.LibraryLink(relKept, "s", time.Now(), "take.mp4")
	stStaged, _ := e.Link(staged)
	relStaged, _ := e.Place(stStaged, ".mp4")
	libStaged, _, _ := e.LibraryLink(relStaged, "s", time.Now(), "other.mp4")
	os.Remove(staged) // adopt unlinks a stage entry

	if !e.LinkedOutside(relKept, libKept) {
		t.Error("a blob the studio still links from {data} is not reported as linked outside")
	}
	if e.LinkedOutside(relStaged, libStaged) {
		t.Error("a blob linked only from the store and library is reported as linked outside")
	}
}
