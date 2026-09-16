package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/janishar/helmstudio/internal/platform/platformtest"
)

// The constraint Q20 asks for is an index, not a check in Go: two writers that
// both read "nothing is selected" and both write would otherwise leave a
// studio with two chosen checkpoints and no way to tell which one launches.
func TestOnlyOneWeightCanBeSelectedPerStudio(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))

	seed := func(studio string) {
		t.Helper()
		err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			for _, q := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
				  VALUES (?, 'd', ?, 'ready', 0, 0)`, []any{studio, "/tmp/" + studio}},
				{`INSERT INTO model_artifacts (id, hf_repo, revision, source, local_path, state, created_at)
				  VALUES (?, ?, 'main', 'managed', ?, 'ready', 0)`, []any{studio + "-art-a", studio + "/a", "/models/" + studio + "/a"}},
				{`INSERT INTO model_artifacts (id, hf_repo, revision, source, local_path, state, created_at)
				  VALUES (?, ?, 'main', 'managed', ?, 'ready', 0)`, []any{studio + "-art-b", studio + "/b", "/models/" + studio + "/b"}},
			} {
				if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	bind := func(studio, placeholder, artifact string, selected int) error {
		return s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO studio_model_bindings (studio_id, artifact_id, placeholder, selected) VALUES (?, ?, ?, ?)`,
				studio, artifact, placeholder, selected)
			return err
		})
	}

	seed("iris")
	if err := bind("iris", "a", "iris-art-a", 1); err != nil {
		t.Fatalf("the first selection should be allowed: %v", err)
	}
	if err := bind("iris", "b", "iris-art-b", 1); err == nil {
		t.Fatal("a second selected binding for one studio must be refused by the index")
	}
	if err := bind("iris", "b", "iris-art-b", 0); err != nil {
		t.Fatalf("an unselected second binding is ordinary: %v", err)
	}

	// The index is per studio, not global: two studios each choosing one is
	// the normal case.
	seed("wan")
	if err := bind("wan", "a", "wan-art-a", 1); err != nil {
		t.Fatalf("another studio's selection is unrelated: %v", err)
	}
}

// Every studio installed before v7 has no recorded approval, which is what
// makes each of them ask once at its next launch rather than silently
// inheriting one.
func TestApprovalColumnsStartEmpty(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, platformtest.Dirs(t))
	if err := s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
			 VALUES ('old', 'd', '/tmp/old', 'ready', 0, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var digest, commit *string
	var at *int64
	err := s.Reader().QueryRowContext(ctx,
		`SELECT approved_digest, approved_commit, approved_at FROM installations WHERE studio_id = 'old'`).
		Scan(&digest, &commit, &at)
	if err != nil {
		t.Fatal(err)
	}
	if digest != nil || commit != nil || at != nil {
		t.Errorf("a studio installed before v7 must have no approval: %v %v %v", digest, commit, at)
	}
}
