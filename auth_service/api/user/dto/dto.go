// Package dto: admin user-management bodies.
package dto

import (
	"strings"

	authdto "github.com/helmstudio/auth_service/api/auth/dto"
	"github.com/helmstudio/auth_service/utils"
)

// grantable reports whether the admin may give the role: admin belongs to the ADMIN_EMAIL account alone.
func grantable(roles []string) string {
	if len(roles) == 0 {
		return "roles must not be empty"
	}
	for _, role := range roles {
		if role == "admin" {
			return "admin is only the ADMIN_EMAIL account (auth's .env)"
		}
		if role != "user" && role != "dev" {
			return "roles must be user or dev"
		}
	}
	return ""
}

// NewUser is the body the admin creates an account with.
type NewUser struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
}

// Validate normalises Email and Name in place (an empty name becomes the part before @), then returns the 422
// detail, or "".
func (u *NewUser) Validate() string {
	u.Email = authdto.NormalizeEmail(u.Email)
	if u.Email == "" {
		return "invalid email"
	}
	if u.Name = strings.TrimSpace(u.Name); u.Name == "" {
		u.Name, _, _ = strings.Cut(u.Email, "@")
	}
	if len(u.Name) > 200 {
		return "name must be at most 200 characters"
	}
	if detail := grantable(u.Roles); detail != "" {
		return detail
	}
	return utils.WeakPassword(u.Password)
}

// Disabled is the body that disables or enables a user.
type Disabled struct {
	Disabled *bool `json:"disabled"` // required: a missing field must not re-enable the user
}

// Validate returns the 422 detail for a missing field, or "".
func (d *Disabled) Validate() string {
	if d.Disabled == nil {
		return "disabled is required"
	}
	return ""
}

// Roles is the body that replaces a user's roles.
type Roles struct {
	Roles []string `json:"roles"`
}

// Validate: roles are a non-empty subset of user, dev (never admin).
func (r Roles) Validate() string { return grantable(r.Roles) }

// Password is the body an admin sets a user's password with.
type Password struct {
	Password string `json:"password"`
}

// Validate returns the 422 detail for a weak password, or "".
func (p *Password) Validate() string { return utils.WeakPassword(p.Password) }
