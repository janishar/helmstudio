// Package message: the bodies of the NATS responders auth.validate.token and auth.validate.role. The auth
// controller mounts them.
package message

import (
	"time"

	"github.com/helmstudio/auth_service/api/auth/model"
)

// NATS subjects this service answers.
const (
	SubjectToken = "auth.validate.token"
	SubjectRole  = "auth.validate.role"
)

// TokenRequest is the body of auth.validate.token: {"token": "<access token>"}.
type TokenRequest struct {
	Token string `json:"token"`
}

// Validate requires the token.
func (r *TokenRequest) Validate() string {
	if r.Token == "" {
		return `want {"token": ...}`
	}
	return ""
}

// RoleRequest is the body of auth.validate.role: {"token": ..., "role": ...}.
type RoleRequest struct {
	Token string `json:"token"`
	Role  string `json:"role"`
}

// Validate requires the token, then the role, with the texts callers have always read.
func (r *RoleRequest) Validate() string {
	if r.Token == "" {
		return `want {"token": ...}`
	}
	if r.Role == "" {
		return `want {"token": ..., "role": ...}`
	}
	return ""
}

// Reply is a valid token's claims; auth.validate.role adds "allowed". An error goes out through
// micro.RespondNatsError as {"error": "..."}.
type Reply struct {
	UserID    string    `json:"user_id,omitempty"`
	Email     string    `json:"email,omitempty"`
	Roles     []string  `json:"roles,omitempty"`
	JTI       string    `json:"jti,omitempty"`
	FamilyID  string    `json:"family_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Allowed   *bool     `json:"allowed,omitempty"`
}

// NewReply returns the claims of a valid token.
func NewReply(claims *model.Claims) *Reply {
	return &Reply{
		UserID: claims.Subject, Email: claims.Email, Roles: claims.Roles, JTI: claims.ID,
		FamilyID: claims.FamilyID, ExpiresAt: claims.ExpiresAt.Time,
	}
}

// NewRoleReply returns the claims of a valid token and whether they hold role.
func NewRoleReply(claims *model.Claims, role string) *Reply {
	reply := NewReply(claims)
	allowed := claims.HasRole(role)
	reply.Allowed = &allowed
	return reply
}
