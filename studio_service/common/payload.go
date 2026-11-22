// Package common: what every feature shares: the request's caller, transactions and object keys.
package common

import "github.com/gin-gonic/gin"

// callerKey is the gin.Context key of the request's Caller.
const callerKey = "caller"

// Caller is the authenticated user of a request (from auth.validate.token) plus Kong's client id.
type Caller struct {
	ID       string // user id (JWT sub), a uuid
	Email    string
	Roles    []string
	ClientID string // X-Client-Id set by Kong's plugin; may be empty
	Via      string // API 1.49: where a write came from, by X-Helm-Client (ViaOf); a label, never a permission
}

// ViaOf is a timeline revision's via for an X-Helm-Client value (contracts/README.md, Timeline: edits by ops): web,
// mcp or assistant; helm, any other value or none is api.
func ViaOf(client string) string {
	switch client {
	case "web", "mcp", "assistant":
		return client
	}
	return "api"
}

// IsAdmin reports whether the caller has the admin role.
func (c Caller) IsAdmin() bool {
	for _, role := range c.Roles {
		if role == "admin" {
			return true
		}
	}
	return false
}

// SetCaller stores the authenticated caller for the handlers after the authentication middleware.
func SetCaller(ctx *gin.Context, caller Caller) {
	ctx.Set(callerKey, caller)
}

// MustCaller returns the caller SetCaller stored. It panics on a route outside the /v1/ guard, a programming error the
// error catcher answers as a 500.
func MustCaller(ctx *gin.Context) Caller {
	return ctx.MustGet(callerKey).(Caller)
}
