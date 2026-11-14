// Package model: users and access-token claims.
package model

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ora/orago/network"
)

// ErrUnauthorized means an access or refresh token was refused.
var ErrUnauthorized = network.NewUnauthorizedError("invalid or expired token", nil)

// User is an account as the API shows it. It never holds the password.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Roles     []string  `json:"roles"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

// HasRole reports whether the user has role.
func (u User) HasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Claims of the RS256 access token: sub, email, roles, jti (+ fid: the refresh-token family it belongs to).
type Claims struct {
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	FamilyID string   `json:"fid"`
	jwt.RegisteredClaims
}

// HasRole reports whether the token carries role.
func (c *Claims) HasRole(role string) bool { return User{Roles: c.Roles}.HasRole(role) }
