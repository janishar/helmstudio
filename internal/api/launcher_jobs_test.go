package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
)

// First review #2: the launcher's job queue is its own path, and never shows
// a task job — a studio's own data, which reaches only that studio's token.
func TestLauncherJobQueueHidesTaskJobs(t *testing.T) {
	ctx := context.Background()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := weights.New(weights.Config{Store: st, Dirs: d})
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Weights: w})
	in := install.New(install.Config{Store: st, Dirs: d, Supervisor: sup, Weights: w})
	srv, err := New(sup, fstest.MapFS{}, addr, nil, WithInstall(in, w, d.Logs()))
	if err != nil {
		t.Fatal(err)
	}

	installJob, task := store.NewID(time.Now()), store.NewID(time.Now())
	err = st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, created_at) VALUES (?, 'install', 'toy-studio', 'succeeded', ?)`, installJob, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, created_at) VALUES (?, 'task', 'toy-studio', 'running', ?)`, task, now+1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	page := decode[Page[install.Job]](t, do(t, srv, "GET", Base+"/launcher/jobs?studio=toy-studio", nil))
	if len(page.Items) != 1 || page.Items[0].ID != installJob || page.NextCursor != nil {
		t.Fatalf("launcher queue = %+v; want only the install job", page)
	}
	if rec := do(t, srv, "GET", Base+"/launcher/jobs/"+installJob, nil); rec.Code != http.StatusOK {
		t.Fatalf("GET the install job: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/launcher/jobs/" + task, "/launcher/jobs/" + task + "/logs"} {
		if rec := do(t, srv, "GET", Base+path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d; want 404", path, rec.Code)
		}
	}
	if rec := do(t, srv, "POST", Base+"/launcher/jobs/"+task+":cancel", nil); rec.Code != http.StatusNotFound {
		t.Errorf("cancelling a task job from the launcher: %d; want 404", rec.Code)
	}
	var state string
	st.Reader().QueryRow(`SELECT state FROM jobs WHERE id = ?`, task).Scan(&state)
	if state != "running" {
		t.Errorf("the task job is %s after the launcher's refused cancel", state)
	}
	// The old shared path belongs to the studio API now; the launcher has none.
	if rec := do(t, srv, "GET", Base+"/jobs", nil); rec.Code == http.StatusOK {
		t.Errorf("GET /jobs is still served to the launcher: %s", rec.Body)
	}
}
