// Package user: the current user and admin user management.
package user

import (
	"context"
	"strings"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/auth_service/api/auth"
	"github.com/helmstudio/auth_service/api/auth/model"
	"github.com/helmstudio/auth_service/api/user/dto"
)

// ErrAdminAccount refuses changes to the ADMIN_EMAIL account: its roles and state come from auth's .env
// (startup.EnsureAdmin applies them on every start); only the admin itself changes its password (PUT /auth/password).
var ErrAdminAccount = network.NewConflictError("the admin account is set by ADMIN_EMAIL / ADMIN_PASSWORD in auth's .env", nil)

// Service holds the admin updates, on top of the auth service that owns the user tables. adminEmail is the
// normalised ADMIN_EMAIL ("" when unset).
type Service struct {
	Auth       *auth.Service
	adminEmail string
}

// NewService returns the service.
func NewService(authService *auth.Service, adminEmail string) *Service {
	return &Service{Auth: authService, adminEmail: strings.ToLower(strings.TrimSpace(adminEmail))}
}

// Create makes an account the user signs in with (sign-up is closed). body must already be validated; a taken email
// is auth.ErrConflict.
func (s *Service) Create(ctx context.Context, body *dto.NewUser) (model.User, error) {
	return s.Auth.CreateUser(ctx, body.Email, body.Name, body.Password, body.Roles)
}

// Editable refuses the ADMIN_EMAIL account (ErrAdminAccount); an unknown id passes, for the update to 404.
func (s *Service) Editable(ctx context.Context, id string) error {
	var email string
	if err := s.Auth.DB.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email); err == nil && email == s.adminEmail {
		return ErrAdminAccount
	}
	return nil
}

// SetDisabled disables or enables a user, keeping the first disabled_at. Disabling also ends every session;
// enabling doesn't bring them back. An unknown id is auth.ErrNotFound.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	tag, err := s.Auth.DB.Exec(ctx, `UPDATE users SET disabled_at = CASE WHEN $2 THEN coalesce(disabled_at, now()) END WHERE id = $1`, id, disabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	if disabled {
		return s.Auth.RevokeUser(ctx, id, "disabled")
	}
	return nil
}

// SetRoles replaces the roles and revokes sessions, so new tokens carry the new roles.
func (s *Service) SetRoles(ctx context.Context, id string, roles []string) error {
	tx, err := s.Auth.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return auth.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE user_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO roles (user_id, role) SELECT DISTINCT $1::uuid, unnest($2::text[])`, id, roles); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.Auth.RevokeUser(ctx, id, "admin")
}
