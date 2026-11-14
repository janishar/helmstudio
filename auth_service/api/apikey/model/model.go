// Package model: client API keys (only the SHA-256 hash is stored; the key is shown once).
package model

import "time"

// APIKey is a key's metadata as the API shows it. Prefix is the key's first characters, so a person can recognise
// it.
type APIKey struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"client_id"`
	Client     string     `json:"client"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}
