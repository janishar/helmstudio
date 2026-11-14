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
