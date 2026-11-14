// Package dto: request/response bodies of the auth routes (contracts/openapi.yaml, tag auth).
package dto

import (
	"net/mail"
	"strings"

	"github.com/ora/auth_service/api/auth/model"
	"github.com/ora/auth_service/utils"
)

// SignupRequest is the sign-up body.
type SignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// SigninRequest is the sign-in body.
type SigninRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RefreshRequest is the token rotation body.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// PasswordChange is the body for changing one's own password.
type PasswordChange struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// Validate returns the 422 detail for a weak new password, or "".
func (p *PasswordChange) Validate() string { return utils.WeakPassword(p.NewPassword) }

// Session is what sign-up, sign-in and refresh answer with.
type Session struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	User         model.User `json:"user"`
}

// NormalizeEmail lower-cases and trims; "" when it is not a plain address.
func NormalizeEmail(value string) string {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return ""
	}
	return email
}

// Validate normalises Email and Name in place, then returns a 422 detail, or "".
func (r *SignupRequest) Validate() string {
	r.Email = NormalizeEmail(r.Email)
	r.Name = strings.TrimSpace(r.Name)
	switch {
	case r.Email == "":
		return "invalid email"
	case r.Name == "" || len(r.Name) > 200:
		return "name must be 1-200 characters"
	}
	return utils.WeakPassword(r.Password)
}
