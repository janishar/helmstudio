// Package apikey: client API keys (issue, list, revoke) and the internal check used by the Kong plugin.
package apikey

import (
	"context"
	"errors"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/utility"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/auth_service/api/apikey/dto"
	"github.com/helmstudio/auth_service/api/apikey/model"
)

// Errors the service returns, each with the status and detail a client sees.
var (
	ErrInvalid  = network.NewUnauthorizedError("invalid api key", nil) // missing, malformed, unknown or revoked
	ErrNotFound = network.NewNotFoundError("not found", nil)
)

// Service stores and checks API keys.
type Service struct{ DB *pgxpool.Pool }

// NewService returns the service.
func NewService(db *pgxpool.Pool) *Service { return &Service{DB: db} }

// keyQuery selects the columns scan reads; callers append a WHERE or ORDER BY.
const keyQuery = `SELECT k.id, k.client_id, c.name, k.prefix, k.scopes, k.created_at, k.revoked_at, k.last_used_at
	FROM api_keys k JOIN clients c ON c.id = k.client_id`

// scan reads one keyQuery row.
func scan(row pgx.Row) (model.APIKey, error) {
	var key model.APIKey
	err := row.Scan(&key.ID, &key.ClientID, &key.Client, &key.Prefix, &key.Scopes, &key.CreatedAt, &key.RevokedAt, &key.LastUsedAt)
	return key, err
}

// Create issues a key for a client (created on first use). The raw key is returned once.
func (s *Service) Create(ctx context.Context, body *dto.Create) (dto.Created, error) {
	key := "helm_" + utility.RandomToken(32)
	var id string
	err := s.DB.QueryRow(ctx, `WITH c AS (
			INSERT INTO clients (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id)
		INSERT INTO api_keys (client_id, hash, prefix, scopes) SELECT c.id, $2, $3, $4 FROM c RETURNING id`,
		body.Client, utility.SHA256(key), key[:12], body.Scopes).Scan(&id)
	if err != nil {
		return dto.Created{}, err
	}
	stored, err := scan(s.DB.QueryRow(ctx, keyQuery+` WHERE k.id = $1`, id))
	return dto.Created{APIKey: stored, Key: key}, err
}

// Ensure makes key an API key of client (compose secrets WEB_API_KEY, HELM_API_KEY); a key that exists, even
// revoked, is left alone.
func (s *Service) Ensure(ctx context.Context, client, key string) error {
	if len(key) < 20 || len(key) > 200 {
		return errors.New("use 20 to 200 characters")
	}
	_, err := s.DB.Exec(ctx, `WITH c AS (
			INSERT INTO clients (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id)
		INSERT INTO api_keys (client_id, hash, prefix, scopes) SELECT c.id, $2, $3, '{}' FROM c ON CONFLICT (hash) DO NOTHING`,
		client, utility.SHA256(key), key[:12])
	return err
}

// List returns every key, revoked ones included, newest first.
func (s *Service) List(ctx context.Context) ([]model.APIKey, error) {
	rows, err := s.DB.Query(ctx, keyQuery+` ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []model.APIKey{}
	for rows.Next() {
		key, err := scan(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// Revoke disables the active key with this id, or returns ErrNotFound when there is none.
func (s *Service) Revoke(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Verify finds the active key by its hash and returns its client, or ErrInvalid.
func (s *Service) Verify(ctx context.Context, key string) (dto.Verified, error) {
	var verified dto.Verified
	if key == "" || len(key) > 200 {
		return verified, ErrInvalid
	}
	// The row is written at most once a minute per key, because last_used_at needs no more precision than that. Postgres
	// runs a writing CTE even though nothing reads it, and the SELECT sees the row from before the write.
	err := s.DB.QueryRow(ctx, `WITH touched AS (
			UPDATE api_keys SET last_used_at = now()
			WHERE hash = $1 AND revoked_at IS NULL
				AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute'))
		SELECT k.client_id, c.name, k.scopes FROM api_keys k JOIN clients c ON c.id = k.client_id
		WHERE k.hash = $1 AND k.revoked_at IS NULL`, utility.SHA256(key)).Scan(&verified.ClientID, &verified.Client, &verified.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return verified, ErrInvalid
	}
	return verified, err
}
