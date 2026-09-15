package weights

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

// ---- service fixture ----

type fixture struct {
	t    *testing.T
	dirs *platform.Dirs
	st   *store.Store
	hub  *hubtest.Hub
	svc  *Service
	free uint64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{t: t, dirs: d, st: st, hub: hubtest.New(t), free: 1 << 50}
	f.svc = f.service()
	return f
}

// service returns a fresh Service on the same store, as a restarted daemon
// would have.
func (f *fixture) service() *Service {
	hf := &HF{
		Endpoint: f.hub.URL(),
		Token:    func(context.Context) (string, error) { return "", nil },
		Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		Backoff: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		Sleep:   func(context.Context, time.Duration) error { return nil },
	}
	return New(Config{Store: f.st, Dirs: f.dirs, HF: hf, FreeDisk: func(string) (uint64, error) { return f.free, nil }, Logf: f.t.Logf})
}

func (f *fixture) install(studioID string) {
	f.t.Helper()
	if err := f.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
			VALUES (?, 'd', ?, 'fetching_weights', 1, 1)`, studioID, "/nonexistent/"+studioID)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) uninstall(studioID string) {
	f.t.Helper()
	if err := f.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM installations WHERE studio_id = ?`, studioID)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) artifactState(repo string) (id, state, source string, n int) {
	f.t.Helper()
	rows, err := f.st.Reader().Query(`SELECT id, state, source FROM model_artifacts WHERE hf_repo = ?`, repo)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		n++
		rows.Scan(&id, &state, &source)
	}
	return id, state, source, n
}

func weight(name, repo, dest string, files ...string) manifest.Weight {
	return manifest.Weight{Name: name, Repo: repo, Dest: dest, Files: files}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
