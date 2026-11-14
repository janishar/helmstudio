// Package postgres migrates a database and opens its connection pool.
package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Open applies the migrations at the root of the FS, then returns a pool on url.
// A Postgres session lock lets replicas start together: the others wait, then find
// nothing left to apply. options go to goose after that lock.
func Open(ctx context.Context, url string, migrations fs.FS, options ...goose.ProviderOption) (*pgxpool.Pool, error) {
	if err := migrate(ctx, url, migrations, options); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return pgxpool.New(ctx, url)
}

// migrate runs the pending migrations over a short-lived connection, holding goose's session lock.
func migrate(ctx context.Context, url string, migrations fs.FS, options []goose.ProviderOption) error {
	// pgxpool.ParseConfig removes pool_max_conns and the other pool settings. A plain
	// sql.Open("pgx", url) sends them to the server, which refuses the connection.
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*config.ConnConfig)
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	options = append([]goose.ProviderOption{goose.WithSessionLocker(locker)}, options...)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
