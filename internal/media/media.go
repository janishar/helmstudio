// Package media is the byte half of the asset store: a content-addressed
// blob tree plus a human-readable hardlink tree, never a copy
// (docs/design/06-storage.md §4, docs/design/02-data-model.md §5-6).
//
// It owns files, not rows. The studio API decides what a request may do and
// records the result; this package makes the inode operations that back it.
//
// Rules held here:
//
//   - A blob is named by its sha256, fanned out two levels, with its real
//     extension, and is read-only (0444) once in place.
//   - Adopting a file is os.Link, never a copy. A link that would cross a
//     volume fails with ErrCrossDevice, and nothing is copied instead
//     (docs/decisions.md M4 Q11).
//   - A file is adopted only when every component of its path under the
//     allowed root is a real directory or the file itself: no symlink.
//   - Removal under the library root goes through os.Root, and a library file
//     is removed only while it is still the blob's inode.
package media

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// ErrCrossDevice means a hardlink would cross a volume. Adoption refuses
// rather than copying.
var ErrCrossDevice = errors.New("the file is on a different volume from the asset store, and adoption never copies")

// ErrOutside means a path is not inside the root it had to be in, or reaches
// it through a symlink.
var ErrOutside = errors.New("the path is not inside an allowed directory, or passes through a symlink")

// ErrTooLarge means an upload passed its size limit.
var ErrTooLarge = errors.New("the upload is over its size limit")

// Engine is the asset tree under three roots.
type Engine struct {
	// Assets holds blobs/ and tmp/. It must share a volume with every stage
	// and data directory adoption takes files from.
	Assets string
	// Derived holds regenerable thumbnails, under the cache root.
	Derived string
	// Library is the tree a person browses.
	Library string
	// LinkFunc makes a hardlink. Nil is os.Link; a test substitutes one that
	// fails with EXDEV, since a second volume cannot be mounted in a test.
	LinkFunc func(oldname, newname string) error
}

func (e *Engine) link(oldname, newname string) error {
	if e.LinkFunc != nil {
		return e.LinkFunc(oldname, newname)
	}
	return os.Link(oldname, newname)
}

func (e *Engine) tmpDir() string { return filepath.Join(e.Assets, "tmp") }

func randomName() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Staged is a file linked or written into the store's tmp directory, hashed,
// and read-only, waiting to be placed or discarded.
type Staged struct {
	Path   string
	SHA256 string
	Bytes  int64

	// origMode is the linked file's mode before it was made read-only, so a
	// duplicate that is discarded gives the studio its file back unchanged.
	origMode fs.FileMode
	linked   bool
}

// Resolve checks that path is a regular file inside root, reached with no
// symlink, and returns its cleaned absolute path. root itself may be reached
// through symlinks (on macOS /tmp is one); what lies below it may not.
func Resolve(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s: %w (it must be absolute)", path, ErrOutside)
	}
	path = filepath.Clean(path)
	rel, ok := relUnder(root, path)
	if !ok {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, ErrOutside)
		}
		if rel, ok = relUnder(realRoot, path); !ok {
			return "", fmt.Errorf("%s: %w", path, ErrOutside)
		}
	}
	cur := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%s: %w", path, ErrOutside)
		}
		if i == len(parts)-1 && !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", path)
		}
	}
	return cur, nil
}

func relUnder(root, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// Link hardlinks src into the tmp directory, makes the shared inode
// read-only, and hashes it. The caller places or discards the result.
func (e *Engine) Link(src string) (*Staged, error) {
	if err := os.MkdirAll(e.tmpDir(), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", e.tmpDir(), err)
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(e.tmpDir(), randomName())
	if err := e.link(src, tmp); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return nil, ErrCrossDevice
		}
		return nil, fmt.Errorf("linking %s into the asset store: %w", src, err)
	}
	st, err := e.finish(tmp)
	if err != nil {
		_ = os.Chmod(tmp, srcInfo.Mode().Perm())
		_ = os.Remove(tmp)
		return nil, err
	}
	st.origMode, st.linked = srcInfo.Mode().Perm(), true
	return st, nil
}

// Write streams r into the tmp directory, fsyncs it and makes it read-only.
// More than limit bytes fails with ErrTooLarge and leaves nothing behind.
func (e *Engine) Write(r io.Reader, limit int64) (*Staged, error) {
	if err := os.MkdirAll(e.tmpDir(), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", e.tmpDir(), err)
	}
	tmp := filepath.Join(e.tmpDir(), randomName()+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("creating an upload file: %w", err)
	}
	fail := func(err error) (*Staged, error) {
		f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		return fail(fmt.Errorf("writing the upload: %w", err))
	}
	if n > limit {
		return fail(ErrTooLarge)
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("syncing the upload: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("closing the upload: %w", err)
	}
	st, err := e.finish(tmp)
	if err != nil {
		_ = os.Remove(tmp)
	}
	return st, err
}

func (e *Engine) finish(tmp string) (*Staged, error) {
	if err := os.Chmod(tmp, 0o444); err != nil {
		return nil, fmt.Errorf("making the blob read-only: %w", err)
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, fmt.Errorf("hashing: %w", err)
	}
	return &Staged{Path: tmp, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}

// Discard removes a staged tmp link. A linked original gets its mode back.
func (e *Engine) Discard(st *Staged) {
	if st == nil {
		return
	}
	if _, err := os.Lstat(st.Path); err != nil {
		return // already placed or removed
	}
	if st.linked {
		_ = os.Chmod(st.Path, st.origMode)
	}
	_ = os.Remove(st.Path)
}

// BlobRel is where a blob with this hash and extension lives, relative to the
// assets root.
func BlobRel(sha, ext string) string {
	return filepath.ToSlash(filepath.Join("blobs", sha[:2], sha[2:4], sha+strings.ToLower(ext)))
}

// Place renames a staged file to its content address and returns the path
// relative to the assets root. A blob already at that address (bytes with no
// row, left by a crash) has the same content by construction and is kept.
func (e *Engine) Place(st *Staged, ext string) (string, error) {
	rel := BlobRel(st.SHA256, ext)
	abs := filepath.Join(e.Assets, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(abs), err)
	}
	if _, err := os.Lstat(abs); err == nil {
		e.Discard(st)
		return rel, nil
	}
	if err := os.Rename(st.Path, abs); err != nil {
		return "", fmt.Errorf("placing the blob: %w", err)
	}
	return rel, nil
}

// BlobPath is the absolute path of a blob.
func (e *Engine) BlobPath(rel string) string { return filepath.Join(e.Assets, filepath.FromSlash(rel)) }

// LibraryLink makes the readable hardlink library/<studio>/<YYYY-MM>/<name>,
// adding -2, -3… when the name is taken. A link across volumes is not made:
// it returns "" and a warning, and the asset is still adopted (Q11).
func (e *Engine) LibraryLink(blobRel, studio string, at time.Time, name string) (rel string, warning string, err error) {
	name = filepath.Base(name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "asset"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	dir := filepath.Join(studio, at.Format("2006-01"))
	if err := os.MkdirAll(filepath.Join(e.Library, dir), 0o755); err != nil {
		return "", "", fmt.Errorf("creating the library folder %s: %w", filepath.Join(e.Library, dir), err)
	}
	blob := e.BlobPath(blobRel)
	for i := 1; i < 1000; i++ {
		candidate := stem + ext
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		r := filepath.Join(dir, candidate)
		err := e.link(blob, filepath.Join(e.Library, r))
		switch {
		case err == nil:
			return filepath.ToSlash(r), "", nil
		case errors.Is(err, fs.ErrExist):
			continue
		case errors.Is(err, syscall.EXDEV):
			return "", fmt.Sprintf("no library file was made: the library at %s is on a different volume from the asset store, and adoption never copies", e.Library), nil
		default:
			return "", "", fmt.Errorf("making the library file: %w", err)
		}
	}
	return "", "", fmt.Errorf("no free name for %s in %s", name, filepath.Join(e.Library, dir))
}

// LinkedOutside reports whether a blob's inode has a name outside the asset
// store and its library file: a studio's own {data} file it was adopted from.
// Removing such a blob frees no space (second review #7). When the platform
// cannot count links it reports false.
func (e *Engine) LinkedOutside(blobRel, libraryRel string) bool {
	blob, err := os.Lstat(e.BlobPath(blobRel))
	if err != nil {
		return false
	}
	n, ok := platform.LinkCount(blob)
	if !ok {
		return false
	}
	inStore := uint64(1)
	if libraryRel != "" {
		if lib, err := os.OpenRoot(e.Library); err == nil {
			if li, err := lib.Lstat(filepath.FromSlash(libraryRel)); err == nil && os.SameFile(li, blob) {
				inStore++
			}
			lib.Close()
		}
	}
	return n > inStore
}

// Removed says what Remove did.
type Removed struct {
	KeptLibraryPath string // set when the library path no longer held the blob's inode
}

// Remove deletes a blob, its derived directory, and its library file while
// that is still the same inode. Nothing is followed through a symlink.
func (e *Engine) Remove(blobRel, libraryRel, sha string) (Removed, error) {
	var out Removed
	blob := e.BlobPath(blobRel)
	blobInfo, blobErr := os.Lstat(blob)
	if libraryRel != "" {
		lib, err := os.OpenRoot(e.Library)
		if err == nil {
			li, lerr := lib.Lstat(filepath.FromSlash(libraryRel))
			switch {
			case lerr != nil:
			case blobErr == nil && li.Mode().IsRegular() && os.SameFile(li, blobInfo):
				if err := lib.Remove(filepath.FromSlash(libraryRel)); err != nil {
					lib.Close()
					return out, fmt.Errorf("removing the library file %s: %w", libraryRel, err)
				}
			default:
				out.KeptLibraryPath = libraryRel
			}
			lib.Close()
		}
	}
	if blobErr == nil {
		if blobInfo.Mode()&fs.ModeSymlink != 0 {
			return out, fmt.Errorf("%s is a symlink, not a blob; not removed", blob)
		}
		if err := os.Remove(blob); err != nil {
			return out, fmt.Errorf("removing the blob %s: %w", blobRel, err)
		}
	}
	if sha != "" && e.Derived != "" {
		if d, err := os.OpenRoot(e.Derived); err == nil {
			_ = d.RemoveAll(sha)
			d.Close()
		}
	}
	return out, nil
}
