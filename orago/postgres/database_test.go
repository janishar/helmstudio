package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ora/orago/utility"
	"github.com/pressly/goose/v3"
)

// createWidgets fails if it runs twice, so a repeated migration can't pass unnoticed.
const createWidgets = `-- +goose Up
CREATE TABLE widgets (id int PRIMARY KEY);
-- +goose Down
DROP TABLE widgets;
`

func migrationFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, contents := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(contents)}
	}
	return fsys
}

// scratchDatabase creates a database of its own on the TEST_DATABASE_URL server and
// returns its URL. It drops the database when the test ends.
func scratchDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(ctx) })

	name := "orago_test_" + utility.RandomHex(8)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func open(t *testing.T, databaseURL string, fsys fstest.MapFS, options ...goose.ProviderOption) *pgxpool.Pool {
	t.Helper()
	pool, err := Open(context.Background(), databaseURL, fsys, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func appliedVersions(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	// Version 0 is goose's own row, written when it creates the table.
	query := "SELECT count(*) FROM goose_db_version WHERE version_id > 0"
	if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOpenMigratesOnceAndKeepsDefaultTable(t *testing.T) {
	databaseURL := scratchDatabase(t)
	fsys := migrationFS(map[string]string{"00001_widgets.sql": createWidgets})
	ctx := context.Background()

	pool := open(t, databaseURL, fsys)
	if got := appliedVersions(t, pool); got != 1 {
		t.Fatalf("applied versions = %d, want 1", got)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO widgets VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	again := open(t, databaseURL, fsys)
	if got := appliedVersions(t, again); got != 1 {
		t.Fatalf("applied versions after second Open = %d, want 1", got)
	}
	var rows int
	if err := again.QueryRow(ctx, "SELECT count(*) FROM widgets").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("widgets rows after second Open = %d, want 1", rows)
	}
}

func TestOpenAcceptsPoolSettingsInURL(t *testing.T) {
	databaseURL := scratchDatabase(t)
	fsys := migrationFS(map[string]string{"00001_widgets.sql": createWidgets})

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "2")
	parsed.RawQuery = query.Encode()

	pool := open(t, parsed.String(), fsys)
	if got := pool.Config().MaxConns; got != 2 {
		t.Fatalf("MaxConns = %d, want 2", got)
	}
}

func TestOpenPassesOptionsToGoose(t *testing.T) {
	databaseURL := scratchDatabase(t)
	later := map[string]string{"00002_later.sql": "-- +goose Up\nCREATE TABLE later (id int);\n"}
	open(t, databaseURL, migrationFS(later))

	both := map[string]string{"00001_widgets.sql": createWidgets, "00002_later.sql": later["00002_later.sql"]}
	if _, err := Open(context.Background(), databaseURL, migrationFS(both)); err == nil {
		t.Fatal("want an error for an out-of-order migration without the option")
	}
	pool := open(t, databaseURL, migrationFS(both), goose.WithAllowOutofOrder(true))
	if got := appliedVersions(t, pool); got != 2 {
		t.Fatalf("applied versions = %d, want 2", got)
	}
}

func TestOpenConcurrentReplicasEachSucceed(t *testing.T) {
	databaseURL := scratchDatabase(t)
	fsys := migrationFS(map[string]string{"00001_widgets.sql": createWidgets})

	const replicas = 4
	var wait sync.WaitGroup
	errs := make([]error, replicas)
	for i := range replicas {
		wait.Go(func() {
			pool, err := Open(context.Background(), databaseURL, fsys)
			errs[i] = err
			if err == nil {
				pool.Close()
			}
		})
	}
	wait.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWrapsMigrationFailure(t *testing.T) {
	databaseURL := scratchDatabase(t)
	fsys := migrationFS(map[string]string{"00001_broken.sql": "-- +goose Up\nNOT SQL;\n"})

	_, err := Open(context.Background(), databaseURL, fsys)
	if err == nil || !strings.HasPrefix(err.Error(), "migrate: ") {
		t.Fatalf("err = %v, want a migrate: prefix", err)
	}
}

// This needs no server: the URL fails before anything connects.
func TestOpenWrapsInvalidURL(t *testing.T) {
	fsys := migrationFS(map[string]string{"00001_widgets.sql": createWidgets})

	pool, err := Open(context.Background(), "postgres://host:not-a-port/db", fsys)
	if pool != nil || err == nil || !strings.HasPrefix(err.Error(), "migrate: ") {
		t.Fatalf("pool = %v, err = %v, want nil and a migrate: prefix", pool, err)
	}
}
