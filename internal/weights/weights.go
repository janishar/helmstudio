// Package weights is the model cache: managed downloads from Hugging Face and
// linked directories a user already has, which the rest of the system cannot
// tell apart (docs/design/01-prd.md R14–R19b, docs/design/02-data-model.md
// §4–§7).
//
// Three rules shape every function here.
//
//   - A linked directory is read-only. Nothing in this package opens a path
//     inside one for writing, and nothing creates, truncates, renames or
//     removes anything there. The only filesystem change a link makes is the
//     symlink at <models>/<dest>, and unlink removes only that symlink.
//   - No delete path follows a symlink. Every removal goes through os.Root on
//     the models root, which removes a symlink rather than what it points at
//     and refuses a path that resolves outside the root; a managed artifact
//     whose directory has become a symlink is refused, not removed.
//   - There is no stored reference count. An artifact's references are its
//     studio_model_bindings rows, counted when read.
package weights

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
)

// Artifact states (docs/design/02-data-model.md §4, model_artifacts).
const (
	StateDeclared     = "declared"
	StateDownloading  = "downloading"
	StateInterrupted  = "interrupted"
	StateAuthRequired = "auth_required"
	StateReady        = "ready"
	StateLinked       = "linked"
	StateMissing      = "missing"

	SourceManaged = "managed"
	SourceLinked  = "linked"
)

var (
	// ErrInUse refuses removing an artifact a studio is bound to.
	ErrInUse = errors.New("the weight is in use")
	// ErrPreviewChanged refuses a reclaim whose confirmation no longer
	// matches what would be deleted.
	ErrPreviewChanged = errors.New("what reclaim would delete has changed since the preview")
	// ErrDiskSpace refuses a download that would not fit.
	ErrDiskSpace = errors.New("not enough free disk space")
	// ErrMissing means a linked directory is not there.
	ErrMissing = errors.New("the linked directory is missing")
	// ErrConflict refuses a link or download that would overwrite something
	// helmstudio does not own, or a dest another repository already uses.
	ErrConflict = errors.New("conflicts with what is already in the models directory")
	// ErrNoArtifact means no artifact has the id.
	ErrNoArtifact = errors.New("no such weight")
)

// Headroom is added to the bytes still to download before the free-space
// check (R17): the larger of 2 GiB and 5%.
func Headroom(remaining int64) int64 {
	return max(2<<30, remaining/20)
}

// Config is everything a Service needs.
type Config struct {
	Store *store.Store
	Dirs  *platform.Dirs
	// HF fetches listings and files. It may be nil for a service that only
	// resolves, links and reclaims.
	HF *HF
	// FreeDisk reports free bytes on the volume holding a path. Default
	// platform.FreeDiskBytes.
	FreeDisk func(path string) (uint64, error)
	// Concurrency is how many files of one artifact download at once.
	// Default 4.
	Concurrency int
	// Logf receives diagnostics. Default: discarded.
	Logf func(format string, args ...any)

	now func() time.Time
}

// Service is the weights cache.
type Service struct {
	cfg Config

	// mu serialises every change to which artifacts exist and what is bound
	// to them, so reclaim never deletes an artifact a binding is being
	// written for.
	mu sync.Mutex
	// fetching holds a lock per artifact id, so two bindings of one artifact
	// never download the same file at once; active marks those in progress,
	// which reclaim skips.
	fetchMu  sync.Mutex
	fetching map[string]*sync.Mutex
	active   map[string]int
}

// New returns a Service.
func New(cfg Config) *Service {
	if cfg.FreeDisk == nil {
		cfg.FreeDisk = platform.FreeDiskBytes
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 4
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Service{cfg: cfg, fetching: map[string]*sync.Mutex{}, active: map[string]int{}}
}

func (s *Service) now() int64 { return s.cfg.now().UnixMilli() }

func (s *Service) modelsRoot() string { return s.cfg.Dirs.Models() }

// openModels opens the models root, creating it if needed. Every write and
// removal under it goes through the returned root.
func (s *Service) openModels() (*os.Root, error) {
	if err := os.MkdirAll(s.modelsRoot(), 0o755); err != nil {
		return nil, fmt.Errorf("creating the models directory %s: %w", s.modelsRoot(), err)
	}
	r, err := os.OpenRoot(s.modelsRoot())
	if err != nil {
		return nil, fmt.Errorf("opening the models directory %s: %w", s.modelsRoot(), err)
	}
	return r, nil
}

// checkDest accepts a dest that names a directory directly under or below
// the models root, never outside it.
func checkDest(dest string) error {
	if !filepath.IsLocal(dest) || dest == "." {
		return fmt.Errorf("weight dest %q is not a directory name under the models directory", dest)
	}
	return nil
}

// Artifact is one weights cache entry as the launcher shows it.
type Artifact struct {
	ID           string `json:"id"`
	HFRepo       string `json:"hf_repo"`
	Revision     string `json:"revision"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Source       string `json:"source"`
	State        string `json:"state"`
	Dest         string `json:"dest"`
	Path         string `json:"path"`
	ExternalPath string `json:"external_path,omitempty"`
	Realpath     string `json:"realpath,omitempty"`
	TotalBytes   int64  `json:"total_bytes,omitempty"`
	// BytesOnDisk is what a managed artifact holds under the models root,
	// partial files included. Linked bytes are the user's and never counted
	// here (R19a).
	BytesOnDisk int64 `json:"bytes_on_disk"`
	// RefCount is the number of bindings, counted when read.
	RefCount   int        `json:"ref_count"`
	Studios    []string   `json:"studios"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// LastUsedAt is when a process of a studio bound to it last went running
	// (02 §7); nil until one has.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// Unobtainable, set only by Link, names the optional weights of the same
	// repository the linked directory lacks. helmstudio never completes a
	// linked directory, so they cannot be fetched while the link stands.
	Unobtainable []string `json:"unobtainable,omitempty"`
}

type artifactRow struct {
	id, repo, revision, commit, source, dest, external, real, state string
	total                                                           sql.NullInt64
	verified                                                        sql.NullInt64
	lastUsed                                                        sql.NullInt64
	created                                                         int64
}

const artifactCols = `id, hf_repo, revision, COALESCE(commit_sha,''), source, local_path, COALESCE(external_path,''), COALESCE(realpath,''), state, total_bytes, verified_at, last_used_at, created_at`

type rowScanner interface{ Scan(...any) error }

func scanArtifact(r rowScanner) (artifactRow, error) {
	var a artifactRow
	err := r.Scan(&a.id, &a.repo, &a.revision, &a.commit, &a.source, &a.dest, &a.external, &a.real, &a.state, &a.total, &a.verified, &a.lastUsed, &a.created)
	return a, err
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func artifactByKey(ctx context.Context, q querier, repo, revision string) (artifactRow, bool, error) {
	a, err := scanArtifact(q.QueryRowContext(ctx, `SELECT `+artifactCols+` FROM model_artifacts WHERE hf_repo = ? AND revision = ?`, repo, revision))
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

func artifactByID(ctx context.Context, q querier, id string) (artifactRow, error) {
	a, err := scanArtifact(q.QueryRowContext(ctx, `SELECT `+artifactCols+` FROM model_artifacts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("weight %s: %w", id, ErrNoArtifact)
	}
	return a, err
}

func boundStudios(ctx context.Context, q querier, artifactID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT studio_id FROM studio_model_bindings WHERE artifact_id = ? ORDER BY studio_id`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Service) setState(ctx context.Context, id, state string) {
	if err := s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET state = ? WHERE id = ?`, state, id)
		return err
	}); err != nil {
		s.cfg.Logf("weights: recording %s as %s: %v", id, state, err)
	}
}

// destsOverlap reports whether two dests are the same directory or one lies
// inside the other. Removing either would remove files of the other.
func destsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(b, a+sep) || strings.HasPrefix(a, b+sep)
}

// overlapping returns another artifact whose dest is dest, contains it, or
// sits inside it. exceptID is skipped.
func overlapping(ctx context.Context, q querier, dest, exceptID string) (artifactRow, bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+artifactCols+` FROM model_artifacts ORDER BY local_path`)
	if err != nil {
		return artifactRow{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return artifactRow{}, false, err
		}
		if a.id != exceptID && destsOverlap(a.dest, dest) {
			return a, true, nil
		}
	}
	return artifactRow{}, false, rows.Err()
}

// ensureArtifact finds the artifact for a weight's (repo, revision), creating
// a managed, declared one at its dest when there is none. A dest that is,
// contains or sits inside another repository's dest is refused.
func (s *Service) ensureArtifact(ctx context.Context, w manifest.Weight) (artifactRow, error) {
	if err := checkDest(w.Dest); err != nil {
		return artifactRow{}, err
	}
	var out artifactRow
	err := s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		a, ok, err := artifactByKey(ctx, tx, w.Repo, w.EffectiveRevision())
		if err != nil || ok {
			out = a
			return err
		}
		other, found, err := overlapping(ctx, tx, w.Dest, "")
		if err != nil {
			return err
		}
		if found {
			studios, _ := boundStudios(ctx, tx, other.id)
			return fmt.Errorf("weight %q wants %s at %s, which overlaps %s, where helmstudio keeps %s at %s (used by %s); give one of them a dest outside the other: %w",
				w.Name, w.Repo, filepath.Join(s.modelsRoot(), w.Dest), filepath.Join(s.modelsRoot(), other.dest), other.repo, other.revision, strings.Join(studios, ", "), ErrConflict)
		}
		id := store.NewID(s.cfg.now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_artifacts (id, hf_repo, revision, source, local_path, state, created_at)
			VALUES (?, ?, ?, 'managed', ?, 'declared', ?)`, id, w.Repo, w.EffectiveRevision(), w.Dest, s.now()); err != nil {
			return err
		}
		out, err = artifactByID(ctx, tx, id)
		return err
	})
	return out, err
}

func bindTx(ctx context.Context, tx *sql.Tx, studioID, artifactID string, w manifest.Weight) error {
	var files any
	if len(w.Files) > 0 {
		b, _ := json.Marshal(w.Files)
		files = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO studio_model_bindings (studio_id, artifact_id, placeholder, files) VALUES (?, ?, ?, ?)
		ON CONFLICT (studio_id, placeholder) DO UPDATE SET artifact_id = excluded.artifact_id, files = excluded.files`,
		studioID, artifactID, w.Name, files)
	if err != nil {
		return fmt.Errorf("binding weight %q to %s: %w", w.Name, studioID, err)
	}
	return nil
}

// Unbind removes a studio's bindings for placeholders it no longer declares.
// The artifacts and their bytes stay, for reclaim.
func (s *Service) Unbind(ctx context.Context, studioID string, keep []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT placeholder FROM studio_model_bindings WHERE studio_id = ?`, studioID)
		if err != nil {
			return err
		}
		var drop []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			if !contains(keep, p) {
				drop = append(drop, p)
			}
		}
		rows.Close()
		for _, p := range drop {
			if _, err := tx.ExecContext(ctx, `DELETE FROM studio_model_bindings WHERE studio_id = ? AND placeholder = ?`, studioID, p); err != nil {
				return err
			}
		}
		return nil
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// lockArtifact serialises fetches of one artifact and marks it active.
func (s *Service) lockArtifact(id string) func() {
	s.fetchMu.Lock()
	m := s.fetching[id]
	if m == nil {
		m = &sync.Mutex{}
		s.fetching[id] = m
	}
	s.active[id]++
	s.fetchMu.Unlock()
	m.Lock()
	return func() {
		m.Unlock()
		s.fetchMu.Lock()
		if s.active[id]--; s.active[id] == 0 {
			delete(s.active, id)
		}
		s.fetchMu.Unlock()
	}
}

func (s *Service) isActive(id string) bool {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	return s.active[id] > 0
}

// Progress reports what a fetch has so far.
type Progress struct {
	ArtifactID string
	Done       int64
	Total      int64
}

// Fetch makes a studio's weight available: it binds the studio to the
// artifact for the weight's (repo, revision) and downloads whatever of the
// weight's files the artifact does not already have. A linked artifact is
// bound after checking the weight's files are present, and nothing is
// downloaded into it. The studio must have an installation row.
//
// onStart, when set, receives the artifact id before any bytes move, so the
// caller can record a job against it.
func (s *Service) Fetch(ctx context.Context, studioID string, w manifest.Weight, onStart func(artifactID string)) error {
	s.mu.Lock()
	a, err := s.ensureArtifact(ctx, w)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if onStart != nil {
		onStart(a.id)
	}
	unlock := s.lockArtifact(a.id)
	defer unlock()

	if a, err = artifactByID(ctx, s.cfg.Store.Reader(), a.id); err != nil {
		return err
	}
	if a.source == SourceLinked {
		return s.bindLinked(ctx, studioID, a, w)
	}
	return s.fetchManaged(ctx, studioID, a, w)
}

// MissingLink reports whether the artifact for a weight is linked to a
// directory that is not there, recording it missing, and returns the path the
// user linked. It reads nothing inside the directory.
func (s *Service) MissingLink(ctx context.Context, w manifest.Weight) (string, bool, error) {
	a, ok, err := artifactByKey(ctx, s.cfg.Store.Reader(), w.Repo, w.EffectiveRevision())
	if err != nil || !ok || a.source != SourceLinked {
		return "", false, err
	}
	if fi, err := os.Stat(a.real); err == nil && fi.IsDir() {
		return "", false, nil
	}
	if a.state != StateMissing {
		s.setState(ctx, a.id, StateMissing)
	}
	return a.external, true, nil
}

// afterUninstalling phrases a next step that is only possible once no studio
// is bound to the artifact, so a refusal never offers a step that would then
// be refused in turn.
func afterUninstalling(ctx context.Context, q querier, artifactID, step string) string {
	studios, _ := boundStudios(ctx, q, artifactID)
	if len(studios) == 0 {
		return step
	}
	return fmt.Sprintf("uninstall %s (which use it), then %s", strings.Join(studios, ", "), step)
}

func (s *Service) bindLinked(ctx context.Context, studioID string, a artifactRow, w manifest.Weight) error {
	r := s.cfg.Store.Reader()
	if fi, err := os.Stat(a.real); err != nil || !fi.IsDir() {
		s.setState(ctx, a.id, StateMissing)
		return fmt.Errorf("weight %q is linked to %s, which is not there; reconnect it, or to download instead, %s: %w",
			w.Name, a.external, afterUninstalling(ctx, r, a.id, "unlink it and install again"), ErrMissing)
	}
	if err := checkPresent(a.real, w); err != nil {
		return fmt.Errorf("weight %q: %s at %s is linked to %s, which helmstudio never completes or writes into: %w. Link a directory that holds every weight of %s these studios use, or to download instead, %s",
			w.Name, a.repo, a.revision, a.external, err, a.repo, afterUninstalling(ctx, r, a.id, "unlink it and install again"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET state = 'linked' WHERE id = ?`, a.id); err != nil {
			return err
		}
		return bindTx(ctx, tx, studioID, a.id, w)
	})
}

func (s *Service) fetchManaged(ctx context.Context, studioID string, a artifactRow, w manifest.Weight) (err error) {
	if s.cfg.HF == nil {
		return errors.New("downloading weights is not configured")
	}
	root, err := s.openModels()
	if err != nil {
		return err
	}
	defer root.Close()
	// Never write into something that is not a plain directory helmstudio
	// made: a symlink here is someone's link, not a managed download.
	if fi, lerr := root.Lstat(a.dest); lerr == nil && !fi.IsDir() {
		return fmt.Errorf("%s is not a directory helmstudio manages (it is a %s); not downloading into it: %w",
			filepath.Join(s.modelsRoot(), a.dest), fi.Mode().Type(), ErrConflict)
	}

	defer func() {
		switch {
		case err == nil:
		case errors.Is(err, ErrAuthRequired):
			s.setState(context.WithoutCancel(ctx), a.id, StateAuthRequired)
		default:
			s.setState(context.WithoutCancel(ctx), a.id, StateInterrupted)
		}
	}()

	commit := a.commit
	if commit == "" {
		if commit, err = s.cfg.HF.Resolve(ctx, a.repo, a.revision); err != nil {
			return err
		}
	}
	listing, err := s.cfg.HF.List(ctx, a.repo, commit)
	if err != nil {
		return err
	}
	var wanted []RemoteFile
	for _, f := range listing {
		if matchesAny(w.Files, f.Path) {
			wanted = append(wanted, f)
		}
	}
	for _, p := range w.Files {
		if !anyMatch(p, listing) {
			return fmt.Errorf("weight %q: %s at %s has no file matching %q", w.Name, a.repo, commit, p)
		}
	}
	if len(wanted) == 0 {
		return fmt.Errorf("weight %q: %s at %s has no files", w.Name, a.repo, commit)
	}

	s.mu.Lock()
	err = s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET commit_sha = ?, state = 'downloading' WHERE id = ?`, commit, a.id); err != nil {
			return err
		}
		for _, f := range wanted {
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_files (id, artifact_id, rel_path, size_bytes, etag, state) VALUES (?, ?, ?, ?, ?, 'pending')
				ON CONFLICT (artifact_id, rel_path) DO NOTHING`, store.NewID(s.cfg.now()), a.id, f.Path, f.Size, f.ETag); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET total_bytes = (SELECT SUM(size_bytes) FROM model_files WHERE artifact_id = ?) WHERE id = ?`, a.id, a.id); err != nil {
			return err
		}
		return bindTx(ctx, tx, studioID, a.id, w)
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}

	pending, err := s.pendingFiles(ctx, a.id)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		if err := root.MkdirAll(a.dest, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Join(s.modelsRoot(), a.dest), err)
		}
		if err := s.checkDisk(root, a, pending); err != nil {
			return err
		}
		if err := s.download(ctx, root, a, commit, pending); err != nil {
			return err
		}
	}
	return s.finishIfComplete(ctx, a.id)
}

func anyMatch(pattern string, files []RemoteFile) bool {
	for _, f := range files {
		if Match(pattern, f.Path) {
			return true
		}
	}
	return false
}

type fileRow struct {
	id   string
	file RemoteFile
}

func (s *Service) pendingFiles(ctx context.Context, artifactID string) ([]fileRow, error) {
	rows, err := s.cfg.Store.Reader().QueryContext(ctx, `SELECT id, rel_path, size_bytes, COALESCE(etag,'') FROM model_files
		WHERE artifact_id = ? AND state = 'pending' ORDER BY rel_path`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileRow
	for rows.Next() {
		var r fileRow
		if err := rows.Scan(&r.id, &r.file.Path, &r.file.Size, &r.file.ETag); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func partName(dest, rel string) string { return filepath.Join(dest, filepath.FromSlash(rel)) + ".part" }

func finalName(dest, rel string) string { return filepath.Join(dest, filepath.FromSlash(rel)) }

// checkDisk refuses a download that would not fit, with the numbers (R17).
func (s *Service) checkDisk(root *os.Root, a artifactRow, pending []fileRow) error {
	var remaining int64
	for _, p := range pending {
		have := int64(0)
		if fi, err := root.Lstat(partName(a.dest, p.file.Path)); err == nil && fi.Mode().IsRegular() {
			have = min(fi.Size(), p.file.Size)
		}
		remaining += p.file.Size - have
	}
	free, err := s.cfg.FreeDisk(s.modelsRoot())
	if err != nil {
		return err
	}
	need := remaining + Headroom(remaining)
	if int64(free) < need {
		return fmt.Errorf("%s at %s needs %s more (%s to download plus %s headroom), and the volume holding %s has %s free: %w",
			a.repo, a.revision, humanBytes(need), humanBytes(remaining), humanBytes(Headroom(remaining)), s.modelsRoot(), humanBytes(int64(free)), ErrDiskSpace)
	}
	return nil
}

func humanBytes(n int64) string {
	const gib = 1 << 30
	if n >= gib {
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// rootPart is a partial file inside the models root.
type rootPart struct {
	root *os.Root
	name string
	abs  string
}

func (p rootPart) Size() (int64, error) {
	fi, err := p.root.Lstat(p.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, nil
	case err != nil:
		return 0, err
	case !fi.Mode().IsRegular():
		return 0, fmt.Errorf("%s is not a regular file; not writing to it: %w", p.abs, ErrConflict)
	}
	return fi.Size(), nil
}

func (p rootPart) Open(truncate bool) (io.WriteCloser, error) {
	if err := p.root.MkdirAll(filepath.Dir(p.name), 0o755); err != nil {
		return nil, fmt.Errorf("creating the directory for %s: %w", p.abs, err)
	}
	if fi, err := p.root.Lstat(p.name); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file; not writing to it: %w", p.abs, ErrConflict)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	f, err := p.root.OpenFile(p.name, flags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", p.abs, err)
	}
	return syncCloser{f}, nil
}

func (p rootPart) Path() string { return p.abs }

type syncCloser struct{ *os.File }

func (s syncCloser) Close() error {
	serr := s.File.Sync()
	cerr := s.File.Close()
	return errors.Join(serr, cerr)
}

func (s *Service) download(ctx context.Context, root *os.Root, a artifactRow, commit string, pending []fileRow) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan fileRow)
	errs := make(chan error, len(pending))
	var wg sync.WaitGroup
	for i := 0; i < min(s.cfg.Concurrency, len(pending)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fr := range work {
				if err := s.downloadOne(ctx, root, a, commit, fr); err != nil {
					errs <- err
					cancel()
					return
				}
			}
		}()
	}
feed:
	for _, fr := range pending {
		select {
		case work <- fr:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	if len(all) > 0 {
		// The first real failure, not the cancellations it caused.
		for _, err := range all {
			if !errors.Is(err, context.Canceled) {
				return err
			}
		}
		return all[0]
	}
	return ctx.Err()
}

func (s *Service) downloadOne(ctx context.Context, root *os.Root, a artifactRow, commit string, fr fileRow) error {
	part := rootPart{root: root, name: partName(a.dest, fr.file.Path), abs: filepath.Join(s.modelsRoot(), partName(a.dest, fr.file.Path))}
	if err := s.cfg.HF.Download(ctx, a.repo, commit, fr.file, part, nil); err != nil {
		return err
	}
	final := finalName(a.dest, fr.file.Path)
	if fi, err := root.Lstat(final); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; not replacing it: %w", filepath.Join(s.modelsRoot(), final), ErrConflict)
	}
	if err := root.Rename(part.name, final); err != nil {
		return fmt.Errorf("moving %s into place: %w", part.abs, err)
	}
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_files SET state = 'complete' WHERE id = ?`, fr.id)
		return err
	})
}

// finishIfComplete marks an artifact ready once no file is pending.
func (s *Service) finishIfComplete(ctx context.Context, id string) error {
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET state = 'ready', verified_at = ?
			WHERE id = ? AND source = 'managed' AND NOT EXISTS (SELECT 1 FROM model_files WHERE artifact_id = ? AND state = 'pending')`,
			s.now(), id, id)
		return err
	})
}

// ArtifactProgress reports the bytes a managed artifact has on disk against
// its listed total, from file lengths rather than any stored counter
// (docs/design/02-data-model.md §1: download progress is derived).
func (s *Service) ArtifactProgress(ctx context.Context, id string) (done, total int64, err error) {
	a, err := artifactByID(ctx, s.cfg.Store.Reader(), id)
	if err != nil {
		return 0, 0, err
	}
	rows, err := s.cfg.Store.Reader().QueryContext(ctx, `SELECT rel_path, size_bytes, state FROM model_files WHERE artifact_id = ?`, id)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var rel, state string
		var size int64
		if err := rows.Scan(&rel, &size, &state); err != nil {
			return 0, 0, err
		}
		total += size
		if state == "complete" {
			done += size
		} else if fi, err := os.Lstat(filepath.Join(s.modelsRoot(), partName(a.dest, rel))); err == nil && fi.Mode().IsRegular() {
			done += min(fi.Size(), size)
		}
	}
	return done, total, rows.Err()
}

// checkPresent is the linked-directory check (R14a): every files pattern
// matches at least one regular file under dir, and when size_gb is declared
// the matched files total at least 90% of it. It only reads.
func checkPresent(dir string, w manifest.Weight) error {
	patterns := w.Files
	matched := make(map[string]bool, len(patterns))
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		fi, err := os.Stat(p) // a file symlink, as in a Hugging Face cache snapshot, counts by its target
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if len(patterns) == 0 {
			total += fi.Size()
			return nil
		}
		hit := false
		for _, pat := range patterns {
			if Match(pat, rel) {
				matched[pat] = true
				hit = true
			}
		}
		if hit {
			total += fi.Size()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	var missing []string
	for _, p := range patterns {
		if !matched[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s has no files matching %s", dir, strings.Join(missing, ", "))
	}
	if len(patterns) == 0 && total == 0 {
		return fmt.Errorf("%s holds no files", dir)
	}
	if w.SizeGB > 0 {
		want := int64(w.SizeGB * 1e9)
		if total < want*9/10 {
			return fmt.Errorf("%s holds %s of matching files, and weight %q declares %.0f GB; it looks incomplete", dir, humanBytes(total), w.Name, w.SizeGB)
		}
	}
	return nil
}

// Link records a directory the user already has as the artifact for a
// weight, instead of downloading it (R14a). It checks the directory is
// readable and holds the weight's files, creates <models>/<dest> as a symlink
// to it, and records the artifact linked. When studioID is installed, the
// studio is bound to it too; a studio that is not installed yet is bound
// when it installs.
//
// Link never writes into the directory. It refuses when <models>/<dest>
// already holds anything other than a symlink to the same directory — a
// managed download is reclaimed first, never merged or overwritten.
//
// siblings are the studio's other declared weights. Those of the same
// repository and revision share the artifact (one per repository), so the
// directory must hold every one of them that is not optional; an optional one
// it lacks is named in Unobtainable (decided in the M3 review).
func (s *Service) Link(ctx context.Context, studioID string, w manifest.Weight, path string, siblings ...manifest.Weight) (Artifact, error) {
	if err := checkDest(w.Dest); err != nil {
		return Artifact{}, err
	}
	if !filepath.IsAbs(path) {
		return Artifact{}, fmt.Errorf("link %q: use an absolute path", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("link %s: %w", path, err)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return Artifact{}, fmt.Errorf("link %s: not a directory", path)
	}
	if _, err := os.ReadDir(real); err != nil {
		return Artifact{}, fmt.Errorf("link %s: not readable: %w", path, err)
	}
	if err := checkPresent(real, w); err != nil {
		return Artifact{}, fmt.Errorf("link %s: %w", path, err)
	}
	var unobtainable []string
	for _, sib := range siblings {
		if sib.Name == w.Name || sib.Repo != w.Repo || sib.EffectiveRevision() != w.EffectiveRevision() {
			continue
		}
		err := checkPresent(real, sib)
		switch {
		case err == nil:
		case sib.Optional:
			unobtainable = append(unobtainable, fmt.Sprintf("%s: %v", sib.Name, err))
		default:
			return Artifact{}, fmt.Errorf("link %s: weight %q shares %s with %q and is required, and %w. helmstudio never completes a linked directory, so link one that holds every required weight of %s",
				path, sib.Name, w.Repo, w.Name, err, w.Repo)
		}
	}
	if within(s.modelsRoot(), real) {
		return Artifact{}, fmt.Errorf("link %s: it is inside the models directory %s, which helmstudio manages; link a directory outside it", path, s.modelsRoot())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.openModels()
	if err != nil {
		return Artifact{}, err
	}
	defer root.Close()

	existing, ok, err := artifactByKey(ctx, s.cfg.Store.Reader(), w.Repo, w.EffectiveRevision())
	if err != nil {
		return Artifact{}, err
	}
	dest := w.Dest
	if !ok {
		// Before touching the filesystem: a link inside another artifact's
		// directory, or around one, would be removed with it.
		if other, found, err := overlapping(ctx, s.cfg.Store.Reader(), dest, ""); err != nil {
			return Artifact{}, err
		} else if found {
			return Artifact{}, fmt.Errorf("%s overlaps %s, where helmstudio keeps %s at %s: %w",
				filepath.Join(s.modelsRoot(), dest), filepath.Join(s.modelsRoot(), other.dest), other.repo, other.revision, ErrConflict)
		}
	}
	if ok {
		dest = existing.dest
		if s.isActive(existing.id) {
			return Artifact{}, fmt.Errorf("%s at %s is downloading; cancel it before linking: %w", w.Repo, w.EffectiveRevision(), ErrConflict)
		}
	}
	linkAbs := filepath.Join(s.modelsRoot(), dest)

	madeLink := false
	fi, lerr := root.Lstat(dest)
	switch {
	case errors.Is(lerr, fs.ErrNotExist):
		if dir := filepath.Dir(dest); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return Artifact{}, fmt.Errorf("creating %s: %w", filepath.Dir(linkAbs), err)
			}
		}
		if err := root.Symlink(real, dest); err != nil {
			return Artifact{}, fmt.Errorf("creating the link %s: %w", linkAbs, err)
		}
		madeLink = true
	case lerr != nil:
		return Artifact{}, lerr
	case fi.Mode()&fs.ModeSymlink != 0:
		target, _ := filepath.EvalSymlinks(linkAbs)
		if target != real {
			step := "unlink it, then link again"
			if ok {
				step = afterUninstalling(ctx, s.cfg.Store.Reader(), existing.id, step)
			}
			return Artifact{}, fmt.Errorf("%s already links to %s; %s: %w", linkAbs, target, step, ErrConflict)
		}
	default:
		step := "reclaim it, then link"
		if ok {
			step = afterUninstalling(ctx, s.cfg.Store.Reader(), existing.id, step)
		}
		return Artifact{}, fmt.Errorf("%s already holds a download; helmstudio never merges a link into it. To link %s instead, %s: %w", linkAbs, path, step, ErrConflict)
	}

	var id string
	err = s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if !ok {
			other, found, err := overlapping(ctx, tx, dest, "")
			if err != nil {
				return err
			}
			if found {
				return fmt.Errorf("%s overlaps %s, where helmstudio keeps %s at %s: %w", linkAbs, filepath.Join(s.modelsRoot(), other.dest), other.repo, other.revision, ErrConflict)
			}
			id = store.NewID(s.cfg.now())
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_artifacts (id, hf_repo, revision, source, local_path, external_path, realpath, state, created_at)
				VALUES (?, ?, ?, 'linked', ?, ?, ?, 'linked', ?)`, id, w.Repo, w.EffectiveRevision(), dest, path, real, s.now()); err != nil {
				return err
			}
		} else {
			id = existing.id
			if existing.source == SourceLinked && existing.real != real {
				return fmt.Errorf("%s at %s is already linked to %s; %s: %w", w.Repo, w.EffectiveRevision(), existing.external,
					afterUninstalling(ctx, tx, existing.id, "unlink it, then link again"), ErrConflict)
			}
			// A managed artifact with nothing on disk becomes linked; its
			// file rows described a download that never happened.
			if _, err := tx.ExecContext(ctx, `DELETE FROM model_files WHERE artifact_id = ?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET source = 'linked', state = 'linked', commit_sha = NULL, total_bytes = NULL,
				verified_at = NULL, external_path = ?, realpath = ? WHERE id = ?`, path, real, id); err != nil {
				return err
			}
		}
		var installed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM installations WHERE studio_id = ?`, studioID).Scan(&installed); err != nil {
			return err
		}
		if installed > 0 {
			return bindTx(ctx, tx, studioID, id, w)
		}
		return nil
	})
	if err != nil {
		if madeLink {
			if fi, lerr := root.Lstat(dest); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
				_ = root.Remove(dest)
			}
		}
		return Artifact{}, err
	}
	a, err := s.Get(ctx, id)
	a.Unobtainable = unobtainable
	return a, err
}

func within(root, p string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	rel, err := filepath.Rel(realRoot, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Unlink removes a linked artifact: the symlink at <models>/<dest> and the
// row. The user's directory is never touched (R19a). A linked artifact a
// studio is bound to is refused, naming the studios.
func (s *Service) Unlink(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := artifactByID(ctx, s.cfg.Store.Reader(), id)
	if err != nil {
		return err
	}
	if a.source != SourceLinked {
		return fmt.Errorf("%s at %s is a download, not a link; reclaim removes downloads: %w", a.repo, a.revision, ErrConflict)
	}
	studios, err := boundStudios(ctx, s.cfg.Store.Reader(), id)
	if err != nil {
		return err
	}
	if len(studios) > 0 {
		return fmt.Errorf("%s at %s is used by %s; uninstall them first: %w", a.repo, a.revision, strings.Join(studios, ", "), ErrInUse)
	}
	root, err := s.openModels()
	if err != nil {
		return err
	}
	defer root.Close()
	fi, lerr := root.Lstat(a.dest)
	switch {
	case errors.Is(lerr, fs.ErrNotExist):
	case lerr != nil:
		return lerr
	case fi.Mode()&fs.ModeSymlink == 0:
		return fmt.Errorf("%s is not a symlink, so it is not removed: %w", filepath.Join(s.modelsRoot(), a.dest), ErrConflict)
	default:
		// os.Root.Remove on a symlink removes the link itself.
		if err := root.Remove(a.dest); err != nil {
			return fmt.Errorf("removing the link %s: %w", filepath.Join(s.modelsRoot(), a.dest), err)
		}
	}
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM model_artifacts WHERE id = ? AND source = 'linked'`, id)
		return err
	})
}

// Launch resolves a studio's {models.<name>} values for a launch. Values are
// real paths; refuse names the placeholders that cannot resolve and why, so
// only a command that uses one is refused. A linked directory that has gone
// is recorded missing (R19b); one that has come back is recorded linked
// again. The install state is never changed.
func (s *Service) Launch(ctx context.Context, studioID string, ws []manifest.Weight) (values, refuse map[string]string, err error) {
	values, refuse = map[string]string{}, map[string]string{}
	for _, w := range ws {
		key := "models." + w.Name
		var artifactID string
		var filesJSON sql.NullString
		err := s.cfg.Store.Reader().QueryRowContext(ctx, `SELECT artifact_id, files FROM studio_model_bindings WHERE studio_id = ? AND placeholder = ?`,
			studioID, w.Name).Scan(&artifactID, &filesJSON)
		if errors.Is(err, sql.ErrNoRows) {
			if w.Optional {
				refuse[key] = fmt.Sprintf("optional weight %q has not been fetched; fetch it before launching with it", w.Name)
			} else {
				refuse[key] = fmt.Sprintf("weight %q is not set up for this installation; run install again", w.Name)
			}
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		a, err := artifactByID(ctx, s.cfg.Store.Reader(), artifactID)
		if err != nil {
			return nil, nil, err
		}
		linkPath := filepath.Join(s.modelsRoot(), a.dest)
		if a.source == SourceLinked {
			target, terr := filepath.EvalSymlinks(linkPath)
			fi, serr := os.Stat(a.real)
			if serr != nil || !fi.IsDir() {
				if a.state != StateMissing {
					s.setState(ctx, a.id, StateMissing)
				}
				refuse[key] = fmt.Sprintf("weight %q is linked to %s, which is not there; reconnect the drive, or relink or download it", w.Name, a.external)
				continue
			}
			if terr != nil || target != a.real {
				refuse[key] = fmt.Sprintf("weight %q: the link %s no longer points at %s; relink it", w.Name, linkPath, a.real)
				continue
			}
			if a.state == StateMissing {
				s.setState(ctx, a.id, StateLinked)
			}
			values[key] = a.real
			continue
		}
		var patterns []string
		if filesJSON.Valid {
			_ = json.Unmarshal([]byte(filesJSON.String), &patterns)
		}
		if why := s.managedUnready(ctx, a, patterns); why != "" {
			refuse[key] = fmt.Sprintf("weight %q: %s", w.Name, why)
			continue
		}
		real, err := filepath.EvalSymlinks(linkPath)
		if err != nil {
			refuse[key] = fmt.Sprintf("weight %q: %s: %v", w.Name, linkPath, err)
			continue
		}
		values[key] = real
	}

	// {models.selected} is whichever selectable weight the user chose (M7 Q20,
	// Q21). It resolves to the same path as that weight's own placeholder, so
	// a manifest may use either — and a studio with selectable weights and no
	// choice is refused rather than launched with an arbitrary one.
	if sel, ok := selectedWeight(ws); ok {
		key := "models.selected"
		name, err := s.selectedName(ctx, studioID)
		switch {
		case err != nil:
			return nil, nil, err
		case name == "":
			refuse[key] = fmt.Sprintf("no checkpoint is chosen for this studio; choose one of %s before launching",
				strings.Join(selectableNames(ws), ", "))
		default:
			if path, ok := values["models."+name]; ok {
				values[key] = path
			} else if why, ok := refuse["models."+name]; ok {
				refuse[key] = why
			} else {
				refuse[key] = fmt.Sprintf("the chosen checkpoint %q is not set up for this installation; fetch or link it, or choose another", name)
			}
		}
		_ = sel
	}
	return values, refuse, nil
}

// selectedWeight reports whether the manifest declares any selectable weight.
func selectedWeight(ws []manifest.Weight) (manifest.Weight, bool) {
	for _, w := range ws {
		if w.Selectable {
			return w, true
		}
	}
	return manifest.Weight{}, false
}

func selectableNames(ws []manifest.Weight) []string {
	var out []string
	for _, w := range ws {
		if w.Selectable {
			out = append(out, w.Name)
		}
	}
	return out
}

// selectedName reads the chosen checkpoint's placeholder, or "" when none is
// chosen. The partial unique index makes at most one row possible.
func (s *Service) selectedName(ctx context.Context, studioID string) (string, error) {
	var name string
	err := s.cfg.Store.Reader().QueryRowContext(ctx,
		`SELECT placeholder FROM studio_model_bindings WHERE studio_id = ? AND selected = 1`, studioID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// Select records which selectable weight a studio launches with. Exactly one
// is selected at a time, which the partial unique index enforces — this only
// has to clear the old one inside the same transaction.
func (s *Service) Select(ctx context.Context, studioID, name string) error {
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE studio_model_bindings SET selected = 0 WHERE studio_id = ? AND selected = 1`, studioID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE studio_model_bindings SET selected = 1 WHERE studio_id = ? AND placeholder = ?`, studioID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%q is not a weight this installation has; fetch or link it first", name)
		}
		return nil
	})
}

// Selected returns the chosen checkpoint's placeholder, or "".
func (s *Service) Selected(ctx context.Context, studioID string) (string, error) {
	return s.selectedName(ctx, studioID)
}

// managedUnready explains why a binding's files are not all downloaded, or
// returns "". Every pattern must match a recorded file, every matching file
// must be complete, and each must still be on disk.
func (s *Service) managedUnready(ctx context.Context, a artifactRow, patterns []string) string {
	rows, err := s.cfg.Store.Reader().QueryContext(ctx, `SELECT rel_path, state FROM model_files WHERE artifact_id = ?`, a.id)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	matched := map[string]bool{}
	n := 0
	for rows.Next() {
		var rel, state string
		if err := rows.Scan(&rel, &state); err != nil {
			return err.Error()
		}
		if !matchesAny(patterns, rel) {
			continue
		}
		n++
		for _, p := range patterns {
			if Match(p, rel) {
				matched[p] = true
			}
		}
		if state != "complete" {
			return fmt.Sprintf("%s at %s is %s, not downloaded (%s); retry the install", a.repo, a.revision, a.state, rel)
		}
		if fi, err := os.Lstat(filepath.Join(s.modelsRoot(), finalName(a.dest, rel))); err != nil || !fi.Mode().IsRegular() {
			return fmt.Sprintf("%s is gone from %s; retry the install to download it again", rel, filepath.Join(s.modelsRoot(), a.dest))
		}
	}
	if n == 0 {
		return fmt.Sprintf("%s at %s has not been downloaded to %s (%s); retry the install", a.repo, a.revision, filepath.Join(s.modelsRoot(), a.dest), a.state)
	}
	for _, p := range patterns {
		if !matched[p] {
			return fmt.Sprintf("no downloaded file of %s matches %q; retry the install", a.repo, p)
		}
	}
	return ""
}

// Get returns one artifact.
func (s *Service) Get(ctx context.Context, id string) (Artifact, error) {
	a, err := artifactByID(ctx, s.cfg.Store.Reader(), id)
	if err != nil {
		return Artifact{}, err
	}
	return s.present(ctx, a)
}

// Used records that studioID went running, on every artifact bound to it —
// 02 §7's `starting` → `running` row. It is what Models & disk reads as
// "Last used".
func (s *Service) Used(ctx context.Context, studioID string) error {
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET last_used_at = ?
			WHERE id IN (SELECT artifact_id FROM studio_model_bindings WHERE studio_id = ?)`, s.now(), studioID)
		return err
	})
}

// Disk is the models directory and the free space on its volume (03 §12).
type Disk struct {
	Root      string `json:"root"`
	FreeBytes uint64 `json:"free_bytes"`
}

// Disk measures the volume the models directory is on. A directory that does
// not exist yet — nothing has been downloaded — is measured at the nearest
// directory above it that does, which is the volume it will be made on.
func (s *Service) Disk() (Disk, error) {
	root := s.modelsRoot()
	at := root
	for {
		if fi, err := os.Stat(at); err == nil && fi.IsDir() {
			break
		}
		up := filepath.Dir(at)
		if up == at {
			break
		}
		at = up
	}
	free, err := s.cfg.FreeDisk(at)
	if err != nil {
		return Disk{}, err
	}
	return Disk{Root: root, FreeBytes: free}, nil
}

// List returns every artifact, checking each linked one is still there.
func (s *Service) List(ctx context.Context) ([]Artifact, error) {
	rows, err := s.cfg.Store.Reader().QueryContext(ctx, `SELECT `+artifactCols+` FROM model_artifacts ORDER BY local_path`)
	if err != nil {
		return nil, err
	}
	var all []artifactRow
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, a)
	}
	rows.Close()
	out := []Artifact{}
	for _, a := range all {
		v, err := s.present(ctx, a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) present(ctx context.Context, a artifactRow) (Artifact, error) {
	if a.source == SourceLinked {
		fi, err := os.Stat(a.real)
		gone := err != nil || !fi.IsDir()
		switch {
		case gone && a.state != StateMissing:
			s.setState(ctx, a.id, StateMissing)
			a.state = StateMissing
		case !gone && a.state == StateMissing:
			s.setState(ctx, a.id, StateLinked)
			a.state = StateLinked
		}
	}
	studios, err := boundStudios(ctx, s.cfg.Store.Reader(), a.id)
	if err != nil {
		return Artifact{}, err
	}
	var refs int
	if err := s.cfg.Store.Reader().QueryRowContext(ctx, `SELECT count(*) FROM studio_model_bindings WHERE artifact_id = ?`, a.id).Scan(&refs); err != nil {
		return Artifact{}, err
	}
	v := Artifact{ID: a.id, HFRepo: a.repo, Revision: a.revision, CommitSHA: a.commit, Source: a.source, State: a.state,
		Dest: a.dest, Path: filepath.Join(s.modelsRoot(), a.dest), ExternalPath: a.external, Realpath: a.real,
		TotalBytes: a.total.Int64, RefCount: refs, Studios: studios, CreatedAt: time.UnixMilli(a.created)}
	if a.verified.Valid {
		t := time.UnixMilli(a.verified.Int64)
		v.VerifiedAt = &t
	}
	if a.lastUsed.Valid {
		t := time.UnixMilli(a.lastUsed.Int64)
		v.LastUsedAt = &t
	}
	if a.source == SourceManaged {
		v.BytesOnDisk = s.bytesUnder(a.dest)
	}
	return v, nil
}

// bytesUnder totals regular files below dest, never following a symlink.
func (s *Service) bytesUnder(dest string) int64 {
	root := filepath.Join(s.modelsRoot(), dest)
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return 0
	}
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ReclaimItem is one managed artifact reclaim would delete.
type ReclaimItem struct {
	ID       string `json:"id"`
	HFRepo   string `json:"hf_repo"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
}

// ReclaimPreview is exactly what a confirmed reclaim deletes.
type ReclaimPreview struct {
	Items      []ReclaimItem `json:"items"`
	TotalBytes int64         `json:"total_bytes"`
	// Confirm is the digest a reclaim must present. It changes when the set
	// of artifacts changes.
	Confirm string `json:"confirm"`
}

// PreviewReclaim lists every managed artifact no studio is bound to, with
// the bytes deleting it frees. Linked artifacts are never included (02 §8),
// and neither is one being downloaded.
func (s *Service) PreviewReclaim(ctx context.Context) (ReclaimPreview, error) {
	return s.previewReclaim(ctx, s.cfg.Store.Reader(), "")
}

// PreviewReclaimOne is PreviewReclaim limited to one artifact: one item when
// it is a download no studio uses, none otherwise.
func (s *Service) PreviewReclaimOne(ctx context.Context, id string) (ReclaimPreview, error) {
	return s.previewReclaim(ctx, s.cfg.Store.Reader(), id)
}

func (s *Service) previewReclaim(ctx context.Context, q querier, onlyID string) (ReclaimPreview, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id, a.hf_repo, a.revision, a.local_path FROM model_artifacts a
		WHERE a.source = 'managed'
		  AND NOT EXISTS (SELECT 1 FROM studio_model_bindings b WHERE b.artifact_id = a.id)
		  AND (?1 = '' OR a.id = ?1)
		ORDER BY a.id`, onlyID)
	if err != nil {
		return ReclaimPreview{}, err
	}
	defer rows.Close()
	p := ReclaimPreview{Items: []ReclaimItem{}}
	h := sha256.New()
	for rows.Next() {
		var it ReclaimItem
		var dest string
		if err := rows.Scan(&it.ID, &it.HFRepo, &it.Revision, &dest); err != nil {
			return ReclaimPreview{}, err
		}
		if s.isActive(it.ID) {
			continue
		}
		it.Path = filepath.Join(s.modelsRoot(), dest)
		it.Bytes = s.bytesUnder(dest)
		p.Items = append(p.Items, it)
		p.TotalBytes += it.Bytes
		fmt.Fprintf(h, "%s\x00%s\n", it.ID, dest)
	}
	p.Confirm = hex.EncodeToString(h.Sum(nil))
	return p, rows.Err()
}

// Reclaim deletes exactly what PreviewReclaim returned when its Confirm
// matches the set as it is now, and refuses with ErrPreviewChanged
// otherwise. Everything is checked before anything is deleted: an artifact
// whose directory has become a symlink or a file stops the whole reclaim.
func (s *Service) Reclaim(ctx context.Context, confirm string) (ReclaimPreview, error) {
	return s.reclaim(ctx, confirm, "")
}

// ReclaimOne deletes one unused download, given the Confirm of its
// PreviewReclaimOne; the same rules as Reclaim apply.
func (s *Service) ReclaimOne(ctx context.Context, id, confirm string) (ReclaimPreview, error) {
	return s.reclaim(ctx, confirm, id)
}

func (s *Service) reclaim(ctx context.Context, confirm, onlyID string) (ReclaimPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.previewReclaim(ctx, s.cfg.Store.Reader(), onlyID)
	if err != nil {
		return ReclaimPreview{}, err
	}
	if confirm != p.Confirm {
		return p, ErrPreviewChanged
	}
	if len(p.Items) == 0 {
		return p, nil
	}
	root, err := s.openModels()
	if err != nil {
		return ReclaimPreview{}, err
	}
	defer root.Close()

	dests := make(map[string]string, len(p.Items))
	for _, it := range p.Items {
		dest, _ := filepath.Rel(s.modelsRoot(), it.Path)
		if err := checkDest(dest); err != nil {
			return ReclaimPreview{}, err
		}
		fi, lerr := root.Lstat(dest)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
		case lerr != nil:
			return ReclaimPreview{}, lerr
		case !fi.IsDir():
			return ReclaimPreview{}, fmt.Errorf("%s is recorded as a download but is a %s; nothing was reclaimed: %w", it.Path, fi.Mode().Type(), ErrConflict)
		}
		// Another artifact inside this one's directory would be deleted
		// with it, without being in the preview.
		if other, found, err := overlapping(ctx, s.cfg.Store.Reader(), dest, it.ID); err != nil {
			return ReclaimPreview{}, err
		} else if found {
			return ReclaimPreview{}, fmt.Errorf("%s overlaps %s, which holds %s at %s; nothing was reclaimed: %w",
				it.Path, filepath.Join(s.modelsRoot(), other.dest), other.repo, other.revision, ErrConflict)
		}
		dests[it.ID] = dest
	}
	for _, it := range p.Items {
		// os.Root.RemoveAll removes symlinks inside the tree as links and
		// never descends into what they point at.
		if err := root.RemoveAll(dests[it.ID]); err != nil {
			return ReclaimPreview{}, fmt.Errorf("reclaiming %s: %w", it.Path, err)
		}
		if err := s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM model_artifacts WHERE id = ? AND source = 'managed'
				AND NOT EXISTS (SELECT 1 FROM studio_model_bindings WHERE artifact_id = ?)`, it.ID, it.ID)
			return err
		}); err != nil {
			return ReclaimPreview{}, fmt.Errorf("reclaiming %s: the files are gone but the record remains: %w", it.Path, err)
		}
	}
	return p, nil
}

// SweepInterrupted records downloads a previous daemon left running as
// interrupted. Nothing is fetched until a user retries (P3: the only
// outbound calls are the ones a user action implies).
func (s *Service) SweepInterrupted(ctx context.Context) error {
	return s.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE model_artifacts SET state = 'interrupted' WHERE state = 'downloading'`)
		return err
	})
}
