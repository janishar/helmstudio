package studio

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// collect and one take a plain value as well as a struct (a []string from collect panicked before).
func TestCollectPlainValues(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	words, err := collect[string](ctx, tx, `SELECT x FROM unnest(ARRAY['a','b']) x`)
	if err != nil || len(words) != 2 || words[1] != "b" {
		t.Fatal(words, err)
	}
	n, err := one[int](ctx, tx, `SELECT 7`)
	if err != nil || n != 7 {
		t.Fatal(n, err)
	}
	type pair struct {
		A string
		B int
	}
	ps, err := collect[pair](ctx, tx, `SELECT 'x', 1`)
	if err != nil || len(ps) != 1 || ps[0].B != 1 {
		t.Fatal(ps, err)
	}
}
