package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/platform/platformtest"
)

// rebuildSessions is SQLite's documented table-change procedure — create new,
// copy, drop old, rename — applied to a parent table: items.session_id
// references sessions(id) ON DELETE SET NULL.
const rebuildSessions = `
CREATE TABLE sessions_new (
  id TEXT PRIMARY KEY,
  studio_id TEXT NOT NULL,
  name TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT '{}',
  etag TEXT NOT NULL,
  created_at INTEGER NOT NULL, opened_at INTEGER, deleted_at INTEGER,
  pinned INTEGER NOT NULL DEFAULT 0);
INSERT INTO sessions_new (id, studio_id, name, state, etag, created_at, opened_at, deleted_at)
  SELECT id, studio_id, name, state, etag, created_at, opened_at, deleted_at FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;
CREATE UNIQUE INDEX uq_session_name ON sessions(studio_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_session_recent ON sessions(studio_id, opened_at DESC) WHERE deleted_at IS NULL;
`

func sessionOf(t *testing.T, ctx context.Context, s *Store) sql.NullString {
	t.Helper()
	var sid sql.NullString
	if err := s.Reader().QueryRowContext(ctx, `SELECT session_id FROM items WHERE id = 'it_1'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestRebuildingAParentTableKeepsChildReferences(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, ctx, s)
	if err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, studio_id, name, etag, created_at) VALUES ('se_1', 'h3-studio', 'example', 'e', 1)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE items SET session_id = 'se_1' WHERE id = 'it_1'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	next := append(slices.Clone(migrations), Migration{Version: LatestVersion() + 1, Name: "rebuild sessions", Up: execScript(rebuildSessions)})
	s2, err := open(ctx, d, next)
	if err != nil {
		t.Fatalf("open with the rebuild: %v", err)
	}
	defer s2.Close()

	if got := sessionOf(t, ctx, s2); got.String != "se_1" {
		t.Fatalf("items.session_id = %v after rebuilding sessions, want se_1: the DROP fired ON DELETE SET NULL", got)
	}

	// Enforcement is back on afterwards, and the child's foreign key still
	// points at the rebuilt table.
	if err := s2.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = 'se_1'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, ctx, s2); got.Valid {
		t.Fatalf("items.session_id = %v after deleting the session, want NULL: ON DELETE SET NULL no longer fires", got)
	}
}

func TestMigrationLeavingForeignKeyViolationsIsRolledBack(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, ctx, s)
	s.Close()

	latest := LatestVersion()
	orphaning := append(slices.Clone(migrations), Migration{
		Version: latest + 1, Name: "orphans an input",
		Up: execScript(`INSERT INTO item_inputs (item_id, asset_id, role) VALUES ('it_1', 'as_missing', 'reference')`),
	})
	if _, err := open(ctx, d, orphaning); err == nil || !strings.Contains(err.Error(), "foreign-key violation") {
		t.Fatalf("open: err = %v, want a foreign-key violation refusal", err)
	}

	s2 := openTest(t, d)
	if v, _ := s2.Version(ctx); v != latest {
		t.Errorf("version = %d, want %d", v, latest)
	}
	var n int
	if err := s2.Reader().QueryRowContext(ctx, `SELECT count(*) FROM item_inputs WHERE asset_id = 'as_missing'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("orphaned rows = %d (%v), want 0", n, err)
	}
}
