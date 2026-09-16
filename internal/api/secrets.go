package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
)

// The Hugging Face token (docs/design/01-prd.md R18, R43; docs/decisions.md
// M6 Q21):
//
//	GET    /api/v1/launcher/settings/huggingface-token   {present, added_at?, unsupported?}
//	PUT    /api/v1/launcher/settings/huggingface-token   {token} → the same
//	DELETE /api/v1/launcher/settings/huggingface-token   204
//
// Write-only. The value goes to the OS secret store and is never read back
// through the API: R43 keeps it out of helm.db, and a token a screen can read
// is a token every script on that screen can read. What helm.db holds is when
// helmstudio stored it, which is the one thing a Settings screen needs to say
// beyond "a token is stored".

// HFTokenName is the secret the Hugging Face downloader reads
// (cmd/helmstudio, internal/weights).
const HFTokenName = "huggingface-token"

// hfAddedKey is the settings row recording when helmstudio stored the token.
// A token added with `security add-generic-password`, which is how M3 said to
// add one, has no row — which is why added_at is optional rather than a guess.
const hfAddedKey = "huggingface_token_added_at"

// noSecretStore is what a Settings screen says on a platform with no secret
// store, in 03 §18's terms: the limit as a fact, and what follows from it.
const noSecretStore = "This machine has no secret store helmstudio can use, so it cannot keep a Hugging Face token. Downloads that need one will ask anonymously and fail on a gated repository."

// WithSecrets serves the Hugging Face token operations. secrets is the OS
// secret store; st records when a token was stored. Without this option the
// three routes are not served, which is what `helm dev` wants: a development
// daemon has no business writing the user's Keychain.
func WithSecrets(secrets platform.SecretStore, st *store.Store) Option {
	return func(s *Server) {
		s.secrets, s.secretStore = secrets, st
	}
}

func (s *Server) routeSecrets() {
	if s.secrets == nil {
		return
	}
	const path = Base + "/launcher/settings/huggingface-token"
	s.mux.HandleFunc("GET "+path, s.getHFToken)
	s.mux.HandleFunc("PUT "+path, s.putHFToken)
	s.mux.HandleFunc("DELETE "+path, s.deleteHFToken)
}

// secretState is api/openapi.yaml's SecretState. The token is not a field of
// it, in any variation, which is the whole point of the shape.
type secretState struct {
	Present     bool   `json:"present"`
	AddedAt     string `json:"added_at,omitempty"`
	Unsupported string `json:"unsupported,omitempty"`
}

func (s *Server) getHFToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := s.secrets.Get(ctx, HFTokenName)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, secretState{Present: true, AddedAt: s.hfAddedAt(ctx)})
	case errors.Is(err, platform.ErrNoSecretStore):
		writeJSON(w, http.StatusOK, secretState{Unsupported: noSecretStore})
	case errors.Is(err, platform.ErrSecretNotFound):
		writeJSON(w, http.StatusOK, secretState{})
	default:
		s.fail(w, fmt.Errorf("reading whether a Hugging Face token is stored: %w", err))
	}
}

func (s *Server) putHFToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `the body must be {"token": "hf_…"}`)
		return
	}
	if err := s.secrets.Set(r.Context(), HFTokenName, body.Token); err != nil {
		if errors.Is(err, platform.ErrNoSecretStore) {
			writeError(w, http.StatusNotImplemented, "unsupported", noSecretStore)
			return
		}
		// The store refuses an empty secret and anything outside printable
		// ASCII (internal/platform.checkSecretValue). A token pasted with a
		// trailing newline lands here, which is the common mistake and
		// deserves a sentence rather than a 500.
		writeError(w, http.StatusUnprocessableEntity, "invalid_token",
			"That does not look like a Hugging Face token. Paste the token itself, with no spaces or line breaks around it.")
		return
	}
	now := time.Now()
	s.setHFAddedAt(r.Context(), now)
	writeJSON(w, http.StatusOK, secretState{Present: true, AddedAt: rfc3339(now)})
}

func (s *Server) deleteHFToken(w http.ResponseWriter, r *http.Request) {
	err := s.secrets.Delete(r.Context(), HFTokenName)
	switch {
	case err == nil, errors.Is(err, platform.ErrSecretNotFound):
		// Removing a token that is not there leaves no token stored, which is
		// what the caller asked for, so the button is safe to press twice.
		s.clearHFAddedAt(r.Context())
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, platform.ErrNoSecretStore):
		writeError(w, http.StatusNotImplemented, "unsupported", noSecretStore)
	default:
		s.fail(w, fmt.Errorf("removing the Hugging Face token: %w", err))
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// hfAddedAt is when helmstudio stored the token, or "" when it did not — a
// token added by hand outside helmstudio is present with no time, and saying
// so is more honest than inventing one.
func (s *Server) hfAddedAt(ctx context.Context) string {
	if s.secretStore == nil {
		return ""
	}
	var ms int64
	err := s.secretStore.Reader().QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, hfAddedKey).Scan(&ms)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.logf("reading when the Hugging Face token was stored: %v", err)
		}
		return ""
	}
	return rfc3339(time.UnixMilli(ms))
}

func (s *Server) setHFAddedAt(ctx context.Context, at time.Time) {
	s.writeSetting(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			hfAddedKey, at.UnixMilli(), at.UnixMilli())
		return err
	}, "recording when the Hugging Face token was stored")
}

func (s *Server) clearHFAddedAt(ctx context.Context) {
	s.writeSetting(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, hfAddedKey)
		return err
	}, "clearing when the Hugging Face token was stored")
}

// writeSetting logs rather than fails: the secret itself is already stored or
// gone by now, and losing the note of when is not worth telling the caller the
// operation failed when it did not.
func (s *Server) writeSetting(ctx context.Context, fn func(context.Context, *sql.Tx) error, what string) {
	if s.secretStore == nil {
		return
	}
	if err := s.secretStore.Update(ctx, fn); err != nil {
		s.logf("%s: %v", what, err)
	}
}
