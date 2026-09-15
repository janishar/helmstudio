package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
)

func openTest(t *testing.T, paths Paths) *Store {
	t.Helper()
	s, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type pragmaValues struct {
	journalMode string
	synchronous int
	busyTimeout int
	foreignKeys int
}

func readPragmas(t *testing.T, ctx context.Context, c *sql.Conn) pragmaValues {
	t.Helper()
	var p pragmaValues
	for q, dst := range map[string]any{
		"PRAGMA journal_mode": &p.journalMode,
		"PRAGMA synchronous":  &p.synchronous,
		"PRAGMA busy_timeout": &p.busyTimeout,
		"PRAGMA foreign_keys": &p.foreignKeys,
	} {
		if err := c.QueryRowContext(ctx, q).Scan(dst); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return p
}

// synchronous reads back as a number: 1 is NORMAL.
var wantPragmas = pragmaValues{journalMode: "wal", synchronous: 1, busyTimeout: 5000, foreignKeys: 1}

func TestPragmasOnTheWriter(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	c, err := s.w.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := readPragmas(t, ctx, c); got != wantPragmas {
		t.Fatalf("writer pragmas = %+v, want %+v", got, wantPragmas)
	}
}

// Pragmas other than journal_mode are per connection. Checking one reader would
// pass even if they were set once on the first connection only, so hold
// several open at once and check each.
func TestPragmasOnEveryReaderConnection(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	const n = 4
	conns := make([]*sql.Conn, n)
	for i := range conns {
		c, err := s.Reader().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns[i] = c
	}
	for i, c := range conns {
		if got := readPragmas(t, ctx, c); got != wantPragmas {
			t.Fatalf("reader connection %d pragmas = %+v, want %+v", i, got, wantPragmas)
		}
	}
}

func TestReaderCannotWrite(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	_, err := s.Reader().ExecContext(ctx, `INSERT INTO kv (studio_id, ns, key, doc, etag, updated_at) VALUES ('s','n','k','{}','e',1)`)
	if err == nil {
		t.Fatal("insert through the read pool succeeded; the single-writer rule is not enforced")
	}
	var n int
	if err := s.Reader().QueryRowContext(ctx, `SELECT count(*) FROM kv`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("kv rows = %d (%v), want 0", n, err)
	}
}

func TestWriterIsOneConnection(t *testing.T) {
	s := openTest(t, platformtest.Dirs(t))
	if got := s.w.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer MaxOpenConnections = %d, want 1", got)
	}
}

func TestConcurrentUpdatesSerialise(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	if err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv VALUES ('s','n','counter','0','e',0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Read-then-write in each transaction: without serialisation some
	// increments would be lost or fail with SQLITE_BUSY.
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
				var v int
				if err := tx.QueryRowContext(ctx, `SELECT CAST(doc AS INTEGER) FROM kv WHERE key='counter'`).Scan(&v); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `UPDATE kv SET doc = ? WHERE key='counter'`, v+1)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var v int
	if err := s.Reader().QueryRowContext(ctx, `SELECT CAST(doc AS INTEGER) FROM kv WHERE key='counter'`).Scan(&v); err != nil || v != workers {
		t.Fatalf("counter = %d (%v), want %d", v, err, workers)
	}
}

// A nested Update with the ctx Update passed in must fail at once. The base
// ctx carries a deadline so that, if the guard is broken, the inner call gives
// up waiting for the connection instead of hanging the test binary.
func TestNestedUpdateWithPassedContextFailsPromptly(t *testing.T) {
	base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := openTest(t, platformtest.Dirs(t))

	var inner error
	var took time.Duration
	err := s.Update(base, func(ctx context.Context, tx *sql.Tx) error {
		start := time.Now()
		inner = s.Update(ctx, func(context.Context, *sql.Tx) error { return nil })
		took = time.Since(start)
		// The outer transaction is still usable after the refused call.
		_, err := tx.ExecContext(ctx, `INSERT INTO kv VALUES ('s','n','outer','{}','e',1)`)
		return err
	})
	if !errors.Is(inner, ErrNestedUpdate) {
		t.Fatalf("nested Update err = %v after %v, want ErrNestedUpdate", inner, took)
	}
	if took > time.Second {
		t.Fatalf("nested Update took %v to refuse, want immediately", took)
	}
	if err != nil {
		t.Fatalf("outer Update: %v", err)
	}
	var n int
	if err := s.Reader().QueryRowContext(base, `SELECT count(*) FROM kv WHERE key = 'outer'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("outer write = %d rows (%v), want committed", n, err)
	}
}

// The marker is per store: a ctx from one store's Update does not refuse an
// Update on another.
func TestMarkedContextDoesNotBlockAnotherStore(t *testing.T) {
	ctx := context.Background()
	a := openTest(t, platformtest.Dirs(t))
	b := openTest(t, platformtest.Dirs(t))
	err := a.Update(ctx, func(ctx context.Context, _ *sql.Tx) error {
		return b.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO kv VALUES ('s','n','k','{}','e',1)`)
			return err
		})
	})
	if err != nil {
		t.Fatalf("Update on store b inside store a's Update: %v", err)
	}
}

func TestUpdateRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	boom := errors.New("boom")
	err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv VALUES ('s','n','k','{}','e',1)`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update err = %v, want boom", err)
	}
	var n int
	if err := s.Reader().QueryRowContext(ctx, `SELECT count(*) FROM kv`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("kv rows = %d (%v), want 0 after rollback", n, err)
	}
}

func seedItem(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path) VALUES ('as_in', 'aa', 'image', 'image/png', 10, 'aa.png')`,
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path) VALUES ('as_out', 'bb', 'video', 'video/mp4', 20, 'bb.mp4')`,
			`INSERT INTO items (id, studio_id, kind, asset_id, created_at) VALUES ('it_1', 'h3-studio', 'video', 'as_out', 1)`,
			`INSERT INTO item_inputs (item_id, asset_id, role) VALUES ('it_1', 'as_in', 'first_frame')`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
}

func isForeignKeyError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// items.asset_id and item_inputs.asset_id are both ON DELETE RESTRICT.
func TestOnDeleteRestrictRestricts(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	seedItem(t, ctx, s)

	for _, id := range []string{"as_out", "as_in"} {
		err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, id)
			return err
		})
		if !isForeignKeyError(err) {
			t.Fatalf("deleting referenced asset %s: err = %v, want a FOREIGN KEY constraint failure", id, err)
		}
	}
	var n int
	if err := s.Reader().QueryRowContext(ctx, `SELECT count(*) FROM assets`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("assets = %d (%v), want both still present", n, err)
	}
}

// The control for the test above: the same delete on the same schema succeeds
// on a connection without foreign_keys(ON). If it did not, the RESTRICT test
// would pass whether or not the pragma was set.
func TestWithoutForeignKeysRestrictIsInert(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s := openTest(t, d)
	seedItem(t, ctx, s)
	s.Close()

	raw, err := sql.Open("sqlite", "file:"+d.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var fk int
	if err := raw.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("control connection foreign_keys = %d (%v), want SQLite's default 0", fk, err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM assets WHERE id = 'as_out'`); err != nil {
		t.Fatalf("delete without foreign_keys: %v, want success", err)
	}
}

func objects(t *testing.T, db *sql.DB, typ string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type = ? AND name NOT LIKE 'sqlite_%' ORDER BY name`, typ)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

// Names from docs/design/02-data-model.md §5.
func TestMigrationsFromEmptyCreateSchemaV1(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	if _, err := os.Stat(d.DB()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database exists before Open: %v", err)
	}
	s := openTest(t, d)

	if v, err := s.Version(ctx); err != nil || v != 1 || LatestVersion() != 1 {
		t.Fatalf("version = %d (%v), latest = %d; want 1", v, err, LatestVersion())
	}

	tables := slices.DeleteFunc(objects(t, s.Reader(), "table"), func(n string) bool {
		return strings.HasPrefix(n, "items_fts_") // FTS5's own shadow tables
	})
	wantTables := []string{"assets", "derived", "inbox", "item_inputs", "items", "items_fts", "kv", "records", "sessions", "tags", "timelines"}
	if !slices.Equal(tables, wantTables) {
		t.Errorf("tables = %v, want %v", tables, wantTables)
	}
	wantIndexes := []string{"idx_inbox_pending", "idx_inputs_asset", "idx_items_asset", "idx_items_feed", "idx_items_studio", "idx_rec_scan", "idx_session_recent", "uq_session_name"}
	if got := objects(t, s.Reader(), "index"); !slices.Equal(got, wantIndexes) {
		t.Errorf("indexes = %v, want %v", got, wantIndexes)
	}
}

func TestMigrationsAreIdempotentOnReopen(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, ctx, s)
	s.Close()

	s2 := openTest(t, d)
	if v, err := s2.Version(ctx); err != nil || v != LatestVersion() {
		t.Fatalf("version after reopen = %d (%v)", v, err)
	}
	var n int
	if err := s2.Reader().QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("items after reopen = %d (%v), want the seeded row intact", n, err)
	}
	if _, err := os.Stat(d.DBBackup(1)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a reopen with nothing to migrate took a backup: %v", err)
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = Open(ctx, d)
	if err == nil || !strings.Contains(err.Error(), "version 99") || !strings.Contains(err.Error(), "update helmstudio") {
		t.Fatalf("Open on a newer database: err = %v, want a refusal naming version 99", err)
	}
}

func TestFailedMigrationLeavesVersionAndSchemaUntouched(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	broken := append(slices.Clone(migrations), Migration{
		Version: 2, Name: "half-applied",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE should_not_survive (x)`); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `THIS IS NOT SQL`)
			return err
		},
	})
	if _, err := open(ctx, d, broken); err == nil || !strings.Contains(err.Error(), "still at version 1") {
		t.Fatalf("open with a failing migration: err = %v, want a refusal at version 1", err)
	}

	s2 := openTest(t, d)
	if v, _ := s2.Version(ctx); v != 1 {
		t.Errorf("version = %d after a failed migration, want 1", v)
	}
	if got := objects(t, s2.Reader(), "table"); slices.Contains(got, "should_not_survive") {
		t.Error("the failed migration's table survived the rollback")
	}
}

func TestExistingDataIsBackedUpBeforeMigrating(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, ctx, s)
	s.Close()

	next := append(slices.Clone(migrations), Migration{
		Version: 2, Name: "add a table",
		Up: execScript(`CREATE TABLE added_in_v2 (x)`),
	})
	s2, err := open(ctx, d, next)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()

	bak, err := sql.Open("sqlite", "file:"+d.DBBackup(1)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	var v, n int
	if err := bak.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("backup version = %d (%v), want 1", v, err)
	}
	if err := bak.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup items = %d (%v), want the seeded row", n, err)
	}
	if slices.Contains(objects(t, bak, "table"), "added_in_v2") {
		t.Error("backup was taken after migrating, not before")
	}
}

func TestMalformedMigrationListIsRefused(t *testing.T) {
	d := platformtest.Dirs(t)
	gap := []Migration{migrations[0], {Version: 3, Name: "skips 2", Up: execScript(`SELECT 1`)}}
	if _, err := open(context.Background(), d, gap); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("err = %v, want a malformed-list refusal", err)
	}
}

func TestSecondOpenIsRefused(t *testing.T) {
	d := platformtest.Dirs(t)
	s := openTest(t, d)
	_, err := Open(context.Background(), d)
	if !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("second Open err = %v, want ErrLocked", err)
	}
	s.Close()
	s2, err := Open(context.Background(), d)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	s2.Close()
}

// "Application Support" has a space in it; a '?' or '#' in a directory name
// must not end the path either.
func TestPathsThatNeedEscaping(t *testing.T) {
	d, err := platform.Under(filepath.Join(t.TempDir(), "Application Support", "odd?name#here%20"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, d)
	s.Close()
	if _, err := os.Stat(d.DB()); err != nil {
		t.Fatalf("database not at the resolved path: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(filepath.Dir(d.DB())))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("unexpected files beside the roots: %v", names)
	}
}
