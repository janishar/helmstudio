package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/janishar/helmstudio/internal/approval"
	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// The approval gate (docs/design/01-prd.md R62; docs/decisions.md M7 Q10).
//
//	GET  /api/v1/studios/{id}/approval   the preview and its digest
//	     ?approval=<digest> on :install, :retry and :launch
//
// One rule, enforced here rather than on a screen: **no install, retry or
// launch runs while the recorded approval does not match what would run now.**
//
// Three things follow from putting it here rather than in the UI.
//
// Launch is gated, not only install. `processes[].cmd` and `health.exec` run
// at every launch and never pass through install at all, so a gate on install
// alone would let an Override that edits a command simply run.
//
// No source is exempt. Import, paste and Duplicate all put someone else's text
// into a Local entry, so "you wrote it" cannot be inferred from where a file
// sits — and a rule that turns on who wrote a file cannot be enforced by a
// daemon that sees only files.
//
// The honest limit, recorded rather than papered over: until M9's cookie, any
// local process can fetch a preview and send its digest back. The digest proves
// the screen was current, not that a person read it.

// WithApproval serves the preview and enforces the gate. Without it the
// operations are ungated, which is what `helm dev` wants: a development daemon
// runs one studio the author is sitting in front of.
func WithApproval(st *store.Store) Option {
	return func(s *Server) { s.approvals = st }
}

// approvalRecord is what v7 stores on the installation.
type approvalRecord struct {
	Digest string    `json:"digest"`
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
}

// approvalKey is the settings row an approval is stored under.
//
// **This is not where 02 §5 says it goes.** The design records the approval on
// the installation, and schema v7 adds the columns for it — but an approval
// necessarily precedes the installation it authorises, and `install_state` has
// no value meaning "not installed", so there is no row to write it to at the
// moment someone approves an install. Recording it in two places would be the
// duplication every other decision here avoids, so it lives in one: `settings`.
// The v7 columns are unused. See this milestone's report: either they go, or
// install copies the approval onto the row it creates, and that is a decision
// rather than something to settle in passing.
func approvalKey(id string) string { return "approval:" + id }

// recorded reads what was last approved for id.
func (s *Server) recorded(ctx context.Context, id string) (approvalRecord, error) {
	if s.approvals == nil {
		return approvalRecord{}, nil
	}
	var raw string
	err := s.approvals.Reader().QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, approvalKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return approvalRecord{}, nil
	}
	if err != nil {
		return approvalRecord{}, fmt.Errorf("reading %s's recorded approval: %w", id, err)
	}
	var rec approvalRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		// A row that does not parse is not an approval. Treating it as one
		// would be the one failure mode this whole path exists to prevent.
		return approvalRecord{}, nil
	}
	return rec, nil
}

// record stores an approval. It is written only when a matching digest arrives
// with an operation — never when the preview is merely read.
func (s *Server) record(ctx context.Context, id string, p approval.Preview) error {
	if s.approvals == nil {
		return nil
	}
	raw, err := json.Marshal(approvalRecord{Digest: p.Digest, Commit: p.Commit, At: time.Now()})
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	return s.approvals.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			approvalKey(id), string(raw), now)
		return err
	})
}

// preview builds the approval preview for a studio the supervisor knows.
func (s *Server) preview(ctx context.Context, st supervisor.Studio) (approval.Preview, error) {
	m := st.Manifest
	in := approval.Input{
		Manifest: m,
		Source:   string(library.SourceRegistry),
		Level:    string(library.LevelUnverified),
	}
	if m.LocalPath != "" {
		in.Level = string(library.LevelDraft)
	}
	// The commit the preview is for. An installed studio is previewed at the
	// commit it was built from; anything else at whatever its manifest pins,
	// which for a Local manifest may be nothing at all.
	if s.installer != nil {
		if info, err := s.installer.Info(ctx, m.ID, m); err == nil && info.CommitSHA != "" {
			in.Commit = info.CommitSHA
		}
	}
	if in.Commit == "" {
		in.Commit = m.Ref
	}
	for _, w := range m.Weights {
		in.Weights = append(in.Weights, approval.Weight{
			Name: w.Name, Repo: w.Repo, Revision: w.Revision,
			Selectable: w.Selectable, Optional: w.Optional,
		})
	}
	return approval.Build(in)
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, ok := s.findStudio(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q", id))
		return
	}
	p, err := s.preview(r.Context(), st)
	if err != nil {
		s.fail(w, err)
		return
	}
	rec, err := s.recorded(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	body := struct {
		approval.Preview
		AlreadyApproved bool `json:"already_approved"`
	}{p, rec.Digest == p.Digest}

	// Reading the preview approves nothing. Writing the approval here would
	// make fetching the screen equivalent to consenting to it, and :install
	// with no digest at all would then simply run — which is the one thing
	// this whole path exists to prevent. The approval is recorded when the
	// digest comes back on the operation.
	writeJSON(w, http.StatusOK, body)
}

// requireApproval is the gate. It answers true when the operation may proceed,
// and writes the refusal itself when it may not.
func (s *Server) requireApproval(w http.ResponseWriter, r *http.Request, id string) bool {
	if s.approvals == nil {
		return true // ungated, as `helm dev` is
	}
	st, ok := s.findStudio(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q", id))
		return false
	}
	p, err := s.preview(r.Context(), st)
	if err != nil {
		s.fail(w, err)
		return false
	}
	rec, err := s.recorded(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return false
	}
	if rec.Digest == p.Digest {
		return true // already approved, and nothing has changed since
	}

	given := r.URL.Query().Get("approval")
	switch {
	case given == "":
		writeErrorDetails(w, http.StatusConflict, "approval_required",
			fmt.Sprintf("%s has not been approved, or what it would run has changed since it was. Nothing has been cloned, built or started.", st.Manifest.Name),
			map[string]any{"approval": p})
		return false
	case given != p.Digest:
		writeErrorDetails(w, http.StatusConflict, "preview_changed",
			fmt.Sprintf("What %s would run changed while the approval screen was open, so nothing was started. Read it again.", st.Manifest.Name),
			map[string]any{"approval": p})
		return false
	}
	if err := s.record(r.Context(), id, p); err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

// findStudio returns the supervisor's view of one studio.
func (s *Server) findStudio(id string) (supervisor.Studio, bool) {
	for _, st := range s.sup.Studios() {
		if st.Manifest != nil && st.Manifest.ID == id {
			return st, true
		}
	}
	return supervisor.Studio{}, false
}
