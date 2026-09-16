package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"gopkg.in/yaml.v3"
)

// Writing to the local directory (docs/decisions.md M7 Q5, Q8, Q9).
//
// `<data>/studios/` holds three things side by side: `<id>.yaml`, the local
// manifests; `<id>/`, the installed checkouts; and `_reverted/`, where a
// reverted manifest goes. The underscore is load-bearing — an id may not begin
// with one, so that directory can never collide with a studio's own.
//
// The rule that shapes all of this: **a manifest the user wrote is never
// deleted.** Revert moves it. Uninstall does not touch it. A save that would
// overwrite one needs the digest of what the editor last read.

// Local writes and removes manifests in <data>/studios.
type Local struct{ Dir string }

// NewLocal returns the store over <data>/studios.
func NewLocal(dataRoot string) *Local { return &Local{Dir: filepath.Join(dataRoot, "studios")} }

// RevertedDir is where a reverted manifest goes. Not scanned, and named so it
// cannot be an id.
const RevertedDir = "_reverted"

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)

// ValidID reports whether id can name a studio, and therefore a file.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Path is where a local manifest for id lives.
func (l *Local) Path(id string) string { return filepath.Join(l.Dir, id+".yaml") }

// Digest is sha256 over bytes, which is what If-Match compares.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Read returns the local manifest for id and its digest.
func (l *Local) Read(id string) ([]byte, string, error) {
	b, err := os.ReadFile(l.Path(id))
	if err != nil {
		return nil, "", err
	}
	return b, Digest(b), nil
}

// Save writes a local manifest. ifMatch, when set, must equal the digest of
// what is on disk — so two editors open on one file cannot silently overwrite
// each other. An empty ifMatch is only allowed when no file exists.
func (l *Local) Save(id string, data []byte, ifMatch string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%q cannot name a studio: use lowercase letters, digits and hyphens, starting with a letter", id)
	}
	existing, digest, err := l.Read(id)
	switch {
	case err == nil && ifMatch == "":
		return "", ErrIfMatchRequired
	case err == nil && ifMatch != digest:
		return "", fmt.Errorf("%w: the file changed since it was opened", ErrDigestMismatch)
	case err != nil && !os.IsNotExist(err):
		return "", err
	}
	_ = existing

	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", err
	}
	// Written through a temporary file in the same directory, so a crash
	// mid-write cannot leave a user's manifest truncated.
	tmp, err := os.CreateTemp(l.Dir, "."+id+".*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), l.Path(id)); err != nil {
		return "", err
	}
	return Digest(data), nil
}

// Revert moves a local manifest out of the way and returns where it went.
//
// It moves rather than deletes. A Revert that deleted could destroy the only
// copy of something the user wrote, and "I meant the other one" is not a
// recoverable mistake if the file is gone.
func (l *Local) Revert(id string) (string, error) {
	src := l.Path(id)
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	dir := filepath.Join(l.Dir, RevertedDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s-%s.yaml", id, time.Now().UTC().Format("20060102-150405")))
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	_ = os.Remove(l.provenancePath(id))
	return dst, nil
}

// Provenance is where a local entry came from — a note for the card, never a
// trust signal (Q8).
//
// It lives beside the manifest rather than inside it, because a note inside
// would be exported upstream: R3d says an exported manifest is unchanged and
// ready to commit, and "imported from a URL on a Tuesday" is not something
// anyone wants in a pull request.
type Provenance struct {
	Kind   string    `json:"kind"` // imported | duplicated | written | folder
	URL    string    `json:"url,omitempty"`
	Path   string    `json:"path,omitempty"`
	FromID string    `json:"from_id,omitempty"`
	At     time.Time `json:"at"`
}

func (l *Local) provenancePath(id string) string { return filepath.Join(l.Dir, id+".source.json") }

// SetProvenance records where an entry came from.
func (l *Local) SetProvenance(id string, p Provenance) error {
	if p.At.IsZero() {
		p.At = time.Now()
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.provenancePath(id), append(b, '\n'), 0o644)
}

// Provenance reads the note, or nil when there is none.
func (l *Local) Provenance(id string) *Provenance {
	b, err := os.ReadFile(l.provenancePath(id))
	if err != nil {
		return nil
	}
	var p Provenance
	if err := json.Unmarshal(b, &p); err != nil {
		return nil
	}
	return &p
}

// Exists reports whether a local manifest exists for id.
func (l *Local) Exists(id string) bool {
	_, err := os.Stat(l.Path(id))
	return err == nil
}

// Rename rewrites a document's `id` so a duplicate claims its new name.
//
// It goes through the YAML node editor rather than a decode and re-encode, so
// a duplicate keeps the comments and the key order of what it was duplicated
// from. Changing that one field and nothing else is what Duplicate means: the
// point is to fork a studio to point at different weights or flags, and
// arriving at a reformatted file is not a good start.
func Rename(data []byte, newID string) ([]byte, error) {
	out, err := manifest.Edit(data, "/id", newID)
	if err != nil {
		return nil, err
	}
	if manifest.DetectKind(data) == manifest.KindPointer {
		// A pointer's inline manifest carries the id too, and the two must
		// agree or the entry will not validate.
		var inner map[string]any
		if err := yaml.Unmarshal(out, &inner); err == nil {
			if _, ok := inner["manifest"]; ok {
				if renamed, err := manifest.Edit(out, "/manifest/id", newID); err == nil {
					out = renamed
				}
			}
		}
	}
	return out, nil
}

// errors this package returns, so the API layer can map them to statuses
// without matching on message text.
var (
	ErrIfMatchRequired = fmt.Errorf("if-match required")
	ErrDigestMismatch  = fmt.Errorf("digest mismatch")
)
