package common

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithUser runs fn in a transaction with app.user_id / app.role set for row-level security; commits when fn
// returns nil. Every query on owned tables goes through here.
func WithUser(ctx context.Context, pool *pgxpool.Pool, caller Caller, fn func(tx pgx.Tx) error) error {
	role := "user"
	if caller.IsAdmin() {
		role = "admin"
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true), set_config('app.role', $2, true)`, caller.ID, role); err != nil {
			return err
		}
		return fn(tx)
	})
}

// System runs fn as the service itself (projector, relay): RLS bypassed via app.role = 'system'.
func System(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', '', true), set_config('app.role', 'system', true)`); err != nil {
			return err
		}
		return fn(tx)
	})
}
