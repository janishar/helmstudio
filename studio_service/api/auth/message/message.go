// Package message: the bodies of the auth.validate.token request this service sends to auth_service.
package message

import "time"

// SubjectToken is the NATS subject that validates an access token.
const SubjectToken = "auth.validate.token"

// Request asks auth_service about one access token.
type Request struct {
	Token string `json:"token"`
}

// NewRequest returns the request for one access token.
func NewRequest(token string) *Request {
	return &Request{Token: token}
}

// Reply is auth_service's answer; Error is set when it refuses the token.
type Reply struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	FamilyID  string    `json:"family_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Error     string    `json:"error"`
}
