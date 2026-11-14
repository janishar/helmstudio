// Package auth: Bearer access tokens validated by auth_service over NATS (auth.validate.token), cached until they
// expire and dropped on evt.auth.revoked.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/ora/orago/micro"

	"github.com/ora/catalog_service/api/auth/message"
	"github.com/ora/catalog_service/common"
)

// ErrInvalid means a bad or expired token (401); ErrUnavailable means auth_service could not be reached (503).
var (
	ErrInvalid     = errors.New("invalid or expired token")
	ErrUnavailable = errors.New("auth unavailable")
)

// entry is one cached token; family lets a family revocation find it.
type entry struct {
	caller  common.Caller
	family  string
	expires time.Time
}

// Service checks Bearer tokens with auth_service and caches the answers, keyed by the token's SHA-256. It keeps the NATS
// client and asks through micro.RequestNats, as goserve's services do; tests pass a fake client.
type Service struct {
	natsClient micro.NatsClient
	mu         sync.Mutex
	cache      map[[32]byte]entry
}

const maxEntries = 10_000 // ponytail: bounded by a full sweep, an LRU if it ever matters

// NewService returns a Service with an empty cache that asks auth_service through natsClient.
func NewService(natsClient micro.NatsClient) *Service {
	return &Service{natsClient: natsClient, cache: map[[32]byte]entry{}}
}

// Validate returns the token's caller: from the cache until the token expires, else from auth_service, within the NATS
// client's Timeout (2 s, set in startup). ErrInvalid: empty, oversized, refused or expired token. ErrUnavailable: the
// request failed or the reply couldn't be read. A full cache drops its expired entries first, and everything if that
// frees nothing.
func (s *Service) Validate(ctx context.Context, token string) (common.Caller, error) {
	if token == "" || len(token) > 8192 {
		return common.Caller{}, ErrInvalid
	}
	key := sha256.Sum256([]byte(token))
	now := time.Now()
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok && now.Before(cached.expires) {
		return cached.caller, nil
	}
	reply, err := micro.RequestNats[message.Request, message.Reply](ctx, s.natsClient, message.SubjectToken, message.NewRequest(token))
	if err != nil {
		return common.Caller{}, ErrUnavailable
	}
	if reply.Error != "" || reply.UserID == "" || !now.Before(reply.ExpiresAt) {
		return common.Caller{}, ErrInvalid
	}
	caller := common.Caller{ID: reply.UserID, Email: reply.Email, Roles: reply.Roles}
	s.mu.Lock()
	if len(s.cache) >= maxEntries {
		for key, cached := range s.cache {
			if !now.Before(cached.expires) {
				delete(s.cache, key)
			}
		}
		if len(s.cache) >= maxEntries {
			clear(s.cache)
		}
	}
	s.cache[key] = entry{caller, reply.FamilyID, reply.ExpiresAt}
	s.mu.Unlock()
	return caller, nil
}

// Revoked handles one evt.auth.revoked envelope: scope family drops that family, scope user every entry of the user.
func (s *Service) Revoked(raw []byte) {
	var envelope struct {
		Data struct {
			Scope    string `json:"scope"`
			UserID   string `json:"user_id"`
			FamilyID string `json:"family_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	revoked := envelope.Data
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cached := range s.cache {
		if (revoked.Scope == "family" && revoked.FamilyID != "" && cached.family == revoked.FamilyID) ||
			(revoked.Scope != "family" && cached.caller.ID == revoked.UserID) {
			delete(s.cache, key)
		}
	}
}
