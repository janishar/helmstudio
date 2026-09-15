// Package store owns helm.db: one SQLite file for every row, opened by one
// process, written through one connection (docs/design/06-storage.md §8,
// docs/design/02-data-model.md §9).
//
// The single-writer rule is enforced, not a convention. The write connection is
// unexported and capped at one, so every mutation goes through Update and
// serialises. The read pool is opened query_only, so a write through it fails
// in SQLite itself. An exclusive lock file stops a second process — or a
// second Open in this one — from becoming another writer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/janishar/helmstudio/internal/platform"
)

// Paths is the part of platform.Dirs the store needs. Every path it touches
// comes from here.
type Paths interface {
	DB() string
	DBLock() string
	DBBackup(version int) string
}

// Store is an open helm.db at the latest schema version.
type Store struct {
	w       *sql.DB // exactly one connection; never exposed
	r       *sql.DB // query_only pool
	release func() error
}

// Pragmas every connection is opened with, writer and readers alike. They are
// per-connection settings (journal_mode aside, which persists in the file), so
// they must be in the DSN rather than executed once after Open.
var pragmas = []string{
	"journal_mode(WAL)",   // readers never block the writer
	"synchronous(NORMAL)", // durable across crashes; a power cut loses at most the last transaction
	"busy_timeout(5000)",  // a transient lock waits; with one writer it should never fire
	"foreign_keys(ON)",    // off by default in SQLite, and without it ON DELETE RESTRICT does nothing
}

// dsn builds a file: URI. The path is percent-encoded because SQLite parses it
// as a URI: a '?' or '#' in a directory name would otherwise end the path.
func dsn(path string, extra ...string) string {
	q := url.Values{}
	for _, p := range append(append([]string{}, pragmas...), extra...) {
		q.Add("_pragma", p)
	}
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}

// Open locks, opens and migrates the database. It refuses to start on a
// database newer than this binary, or when a migration fails.
func Open(ctx context.Context, paths Paths) (*Store, error) {
	return open(ctx, paths, migrations)
}

func open(ctx context.Context, paths Paths, ms []Migration) (_ *Store, err error) {
	release, err := platform.LockExclusive(paths.DBLock())
	if err != nil {
		if errors.Is(err, platform.ErrLocked) {
			return nil, fmt.Errorf("opening %s: another helmstudio process already has it open; stop it first: %w", paths.DB(), err)
		}
		return nil, fmt.Errorf("opening %s: %w", paths.DB(), err)
	}
	s := &Store{release: release}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()

	// _txlock=immediate takes the write lock at BEGIN, so a transaction that
	// reads before it writes cannot fail part-way on a lock upgrade.
	s.w, err = sql.Open("sqlite", dsn(paths.DB())+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening %s for writing: %w", paths.DB(), err)
	}
	s.w.SetMaxOpenConns(1)
	s.w.SetMaxIdleConns(1)
	s.w.SetConnMaxLifetime(0)
	if err = s.w.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("opening %s for writing: %w", paths.DB(), err)
	}

	if err = migrate(ctx, s.w, paths, ms); err != nil {
		return nil, err
	}

	// Readers open after migration, so the file is already in WAL mode and
	// setting it again is a no-op a query_only connection is allowed to make.
	s.r, err = sql.Open("sqlite", dsn(paths.DB(), "query_only(1)"))
	if err != nil {
		return nil, fmt.Errorf("opening %s for reading: %w", paths.DB(), err)
	}
	s.r.SetMaxOpenConns(max(4, runtime.NumCPU()))
	if err = s.r.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("opening %s for reading: %w", paths.DB(), err)
	}
	return s, nil
}

// Close closes both pools and releases the lock.
func (s *Store) Close() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	if s.w != nil {
		errs = append(errs, s.w.Close())
	}
	if s.release != nil {
		errs = append(errs, s.release())
		s.release = nil
	}
	return errors.Join(errs...)
}

// Reader returns the read pool. Every connection in it is query_only, so a
// write attempted through it fails. That stops accidents, not intent: a caller
// can clear the pragma on its connection with PRAGMA query_only = 0. Reads
// through Reader inside Update do not see that transaction's own uncommitted
// writes; read through the *sql.Tx instead.
func (s *Store) Reader() *sql.DB { return s.r }

// ErrNestedUpdate means Update was called with a context that came from an
// Update still running on the same store.
var ErrNestedUpdate = errors.New("Update called from inside an Update on the same store; do the work through the *sql.Tx you were given")

// inUpdateKey marks a context handed to an Update callback. Its value is the
// *Store, so an Update on a different store is not mistaken for re-entry.
type inUpdateKey struct{}

// Update runs fn in one write transaction, committing if fn returns nil and
// rolling back otherwise. Record an item, its inputs and its tags in one
// Update, not three.
//
// Updates serialise on the single write connection, so an Update called from
// inside fn would wait forever for the connection its caller holds. The ctx
// passed to fn is marked, and Update returns ErrNestedUpdate at once when
// called with it (or anything derived from it) — so use that ctx inside fn.
//
// The guard cannot see an Update called from inside fn with a context that did
// not come from fn's ctx, such as context.Background() or the caller's own
// outer ctx: that call still blocks forever. Go gives no supported way to tell
// such a call apart from a concurrent Update on another goroutine, which must
// wait.
func (s *Store) Update(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) (err error) {
	if ctx.Value(inUpdateKey{}) == s {
		return ErrNestedUpdate
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning a write transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(context.WithValue(ctx, inUpdateKey{}, s), tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing a write transaction: %w", err)
	}
	return nil
}

// Version reports the schema version recorded in the file.
func (s *Store) Version(ctx context.Context) (int, error) {
	return userVersion(ctx, s.r)
}
