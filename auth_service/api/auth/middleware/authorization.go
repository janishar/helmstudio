package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/auth_service/common"
)

// authorization checks the roles in the claims that authentication stored.
type authorization struct{}

// NewAuthorizationProvider returns the provider that requires a role. Mount it after authentication.
func NewAuthorizationProvider() network.AuthorizationProvider {
	return &authorization{}
}

// Middleware returns a handler that passes when the token carries one of roles, and answers 403 otherwise.
func (m *authorization) Middleware(roles ...string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		claims := common.MustClaims(ctx)
		for _, role := range roles {
			if claims.HasRole(role) {
				return
			}
		}
		network.SendError(ctx, http.StatusForbidden, strings.Join(roles, " or ")+" role required")
	}
}
