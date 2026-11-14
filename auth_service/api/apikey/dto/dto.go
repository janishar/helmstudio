// Package dto: API key bodies.
package dto

import (
	"regexp"

	"github.com/helmstudio/auth_service/api/apikey/model"
)

// Create is the body that issues a key.
type Create struct {
	Client string   `json:"client"`
	Scopes []string `json:"scopes"`
}

// nameRe is the allowed form of client and scope names.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// Validate returns a 422 detail, or "". It also turns missing scopes into an empty list, as the column is NOT NULL.
func (c *Create) Validate() string {
	if !nameRe.MatchString(c.Client) {
		return "client must match " + nameRe.String()
	}
	for _, s := range c.Scopes {
		if !nameRe.MatchString(s) {
			return "scopes must match " + nameRe.String()
		}
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	return ""
}

// Created carries the raw key: returned once, never stored.
type Created struct {
	model.APIKey
	Key string `json:"key"`
}

// Verified is GET /verify/apikey's answer (read by the Kong plugin).
type Verified struct {
	ClientID string   `json:"client_id"`
	Client   string   `json:"client"`
	Scopes   []string `json:"scopes"`
}
