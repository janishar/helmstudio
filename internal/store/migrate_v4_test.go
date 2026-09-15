package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/platform/platformtest"
)

// A database at version 3 with real rows migrates to 4 without losing any of
// them: records get a non-empty etag, referenced assets are marked referenced
// and unreferenced ones are not, jobs and log_files keep every row and the
// step_runs that point at them, and search finds an existing item by the
// prompt inside its params.
func TestMigrationV4KeepsExistingRows(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)

	s, err := open(ctx, d, migrations[:3])
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO records (id, studio_id, collection, doc, created_at, updated_at) VALUES ('r1', 'h3-studio', 'takes', '{"seed":42}', 1, 1)`,
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path, created_at) VALUES ('as_in', 'aa', 'image', 'image/png', 10, 'aa.png', 5)`,
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path, created_at) VALUES ('as_out', 'bb', 'video', 'video/mp4', 20, 'bb.mp4', 6)`,
			`INSERT INTO assets (id, sha256, kind, mime, bytes, blob_path, created_at) VALUES ('as_lone', 'cc', 'video', 'video/mp4', 30, 'cc.mp4', 7)`,
			`INSERT INTO items (id, studio_id, kind, asset_id, title, params, created_at) VALUES ('it_1', 'h3-studio', 'video', 'as_out', 'drift', '{"prompt":"a cafe window at dusk"}', 1)`,
			`INSERT INTO item_inputs (item_id, asset_id, role) VALUES ('it_1', 'as_in', 'first_frame')`,
			`INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at) VALUES ('h3-studio', 'd', '/x', 'ready', 1, 1)`,
			`INSERT INTO jobs (id, kind, studio_id, state, progress_num, progress_den, created_at) VALUES ('j1', 'install', 'h3-studio', 'succeeded', 2, 2, 1)`,
			`INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES ('l1', 'studios/h3-studio/build-j1-00.log', 'build', 'step_run', 's1', 'h3-studio', 1)`,
			`INSERT INTO step_runs (id, studio_id, job_id, step_index, step_name, command, state, log_file_id) VALUES ('s1', 'h3-studio', 'j1', 0, 'make', 'make', 'succeeded', 'l1')`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openTest(t, d)
	r := s.Reader()
	scan := func(q string, dest ...any) {
		t.Helper()
		if err := r.QueryRowContext(ctx, q).Scan(dest...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	var etag string
	scan(`SELECT etag FROM records WHERE id = 'r1'`, &etag)
	if len(etag) != 32 {
		t.Errorf("records.etag = %q; want a fresh 32-hex value, never the '' default", etag)
	}

	for id, referenced := range map[string]bool{"as_in": true, "as_out": true, "as_lone": false} {
		var at sql.NullInt64
		scan(`SELECT first_referenced_at FROM assets WHERE id = '`+id+`'`, &at)
		if at.Valid != referenced {
			t.Errorf("%s first_referenced_at = %v; want set = %v", id, at, referenced)
		}
	}

	var jobs, steps, logs int
	scan(`SELECT count(*) FROM jobs WHERE id = 'j1' AND state = 'succeeded' AND progress_num = 2`, &jobs)
	scan(`SELECT count(*) FROM step_runs s JOIN jobs j ON j.id = s.job_id JOIN log_files l ON l.id = s.log_file_id`, &steps)
	scan(`SELECT count(*) FROM log_files`, &logs)
	if jobs != 1 || steps != 1 || logs != 1 {
		t.Errorf("jobs %d, joined step_runs %d, log_files %d; want 1, 1, 1", jobs, steps, logs)
	}

	// The rebuilt tables accept what M4 adds.
	err = s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, created_at, cancel_requested_at) VALUES ('t1', 'task', 'h3-studio', 'running', 2, 3)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES ('l2', 'studios/h3-studio/task-t1.log', 'task', 'job', 't1', 'h3-studio', 2)`)
		return err
	})
	if err != nil {
		t.Fatalf("writing a task job and its log after v4: %v", err)
	}

	var found string
	scan(`SELECT i.id FROM items_fts f JOIN items i ON i.id = f.item_id WHERE items_fts MATCH 'NEAR(cafe window)'`, &found)
	if found != "it_1" {
		t.Errorf("search found %q; want it_1 from its params.prompt", found)
	}

	// Foreign keys still hold on the rebuilt parent: a job with step_runs
	// cannot be deleted out from under them.
	err = s.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id = 'j1'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("deleting a job its step_runs reference: %v; want a foreign key failure", err)
	}
}
