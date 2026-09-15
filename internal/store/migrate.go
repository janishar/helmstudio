package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
)

// Migration moves the schema from Version-1 to Version. Up runs inside the
// transaction that also records the new version, so a failure leaves both the
// schema and the version untouched. Up runs with foreign-key enforcement off,
// so a table can be rebuilt without firing ON DELETE actions; any violation
// left at the end fails the migration.
type Migration struct {
	Version int
	Name    string
	Up      func(ctx context.Context, tx *sql.Tx) error
}

//go:embed migrations/0001_platform.sql
var schemaV1 string

//go:embed migrations/0002_supervision.sql
var schemaV2 string

//go:embed migrations/0003_install.sql
var schemaV3 string

//go:embed migrations/0004_api.sql
var schemaV4 string

//go:embed migrations/0005_settings.sql
var schemaV5 string

// migrations is forward-only and append-only. The embedded provider inside a
// standalone studio runs the same sequence, which is what makes adopting its
// ./.helm/helm.db an import rather than a merge. Never edit or reorder an
// entry that has shipped; add the next number.
var migrations = []Migration{
	{Version: 1, Name: "platform tables (docs/design/02-data-model.md §5)", Up: execScript(schemaV1)},
	{Version: 2, Name: "process and log tables (docs/design/02-data-model.md §4)", Up: execScript(schemaV2)},
	{Version: 3, Name: "installation, job and weights tables (docs/design/02-data-model.md §4)", Up: execScript(schemaV3)},
	{Version: 4, Name: "platform API: tokens, asset grants, record etags, search, task jobs (docs/design/02-data-model.md §4-5)", Up: execScript(schemaV4)},
	{Version: 5, Name: "launcher settings (docs/design/02-data-model.md §5)", Up: execScript(schemaV5)},
}

// LatestVersion is the schema version this binary migrates to.
func LatestVersion() int { return migrations[len(migrations)-1].Version }

func execScript(script string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, script)
		return err
	}
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func userVersion(ctx context.Context, q queryRower) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("reading the schema version: %w", err)
	}
	return v, nil
}

// migrate brings db to the last version in ms. Everything runs on one
// dedicated connection, because apply changes a connection-level pragma.
func migrate(ctx context.Context, db *sql.DB, paths Paths, ms []Migration) error {
	for i, m := range ms {
		if m.Version != i+1 {
			return fmt.Errorf("migration list is malformed: entry %d has version %d, want %d", i, m.Version, i+1)
		}
	}
	latest := len(ms)

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%s: taking a connection to migrate on: %w", paths.DB(), err)
	}
	defer conn.Close()

	current, err := userVersion(ctx, conn)
	if err != nil {
		return fmt.Errorf("%s: %w", paths.DB(), err)
	}
	if current > latest {
		return fmt.Errorf("%s has schema version %d, but this helmstudio only knows up to version %d; update helmstudio rather than run an older one against newer data", paths.DB(), current, latest)
	}
	if current == latest {
		return nil
	}

	// Snapshot before migrating existing data. An empty database has nothing
	// to lose.
	if current > 0 {
		if err := backup(ctx, conn, paths.DBBackup(current)); err != nil {
			return fmt.Errorf("%s: backing up before migrating from version %d, so no migration was run: %w", paths.DB(), current, err)
		}
	}

	for _, m := range ms[current:] {
		if err := apply(ctx, conn, m); err != nil {
			return fmt.Errorf("%s: migration %d (%s) failed and was rolled back; the database is still at version %d and helmstudio will not start on it: %w", paths.DB(), m.Version, m.Name, m.Version-1, err)
		}
	}
	return nil
}

// apply runs one migration with foreign-key enforcement off, then checks the
// result before committing.
//
// Enforcement has to be off: SQLite's documented way to change a table is to
// create a new one, copy, drop the old one and rename, and with foreign_keys
// ON the DROP fires the old table's ON DELETE actions — SET NULL on every
// item's session_id, CASCADE on every derived row — inside a transaction that
// then commits cleanly. PRAGMA foreign_keys is a no-op inside a transaction,
// so it is switched before BEGIN, and PRAGMA foreign_key_check stands in for
// enforcement before COMMIT.
func apply(ctx context.Context, conn *sql.Conn, m Migration) (err error) {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disabling foreign keys: %w", err)
	}
	defer func() {
		// Restore even when ctx is cancelled: this connection goes back to
		// the write pool. If restoring fails, Open fails and closes the pool.
		if _, onErr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON"); onErr != nil && err == nil {
			err = fmt.Errorf("re-enabling foreign keys: %w", onErr)
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = m.Up(ctx, tx); err != nil {
		return err
	}
	if err = foreignKeyCheck(ctx, tx); err != nil {
		return err
	}
	// PRAGMA takes no bound parameters; Version is an int from this binary.
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return fmt.Errorf("recording version %d: %w", m.Version, err)
	}
	return tx.Commit()
}

// foreignKeyCheck fails if any row violates a foreign key, naming the first few.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer rows.Close()
	const show = 5
	var violations []string
	n := 0
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("checking foreign keys: %w", err)
		}
		if n++; n <= show {
			violations = append(violations, fmt.Sprintf("%s row %v → %s", table, rowid.Int64, parent))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("the migration leaves %d foreign-key violation(s), e.g. %v", n, violations)
	}
	return nil
}

// backup writes a consistent snapshot with VACUUM INTO, to a temporary name
// first so a crash never leaves a truncated file under the real one. An
// earlier snapshot at the same version is replaced; snapshots at other
// versions are kept, one per version ever migrated from.
//
// To restore: stop helmstudio, delete helm.db-wal and helm.db-shm, then move
// the snapshot over helm.db. A leftover -wal would otherwise be replayed onto
// the restored file and corrupt it.
func backup(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, path string) error {
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing a stale partial backup: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("VACUUM INTO %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("moving the backup into place: %w", err)
	}
	return nil
}
