// Package middleware: bearer-token authentication and role checks for auth_service's own routes.
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/auth_service/api/auth"
	"github.com/helmstudio/auth_service/common"
)

// authentication checks access tokens with the auth service directly, where studio_service asks over NATS.
type authentication struct {
	service *auth.Service
}

// NewAuthenticationProvider returns the provider that requires a valid access token.
func NewAuthenticationProvider(service *auth.Service) network.AuthenticationProvider {
	return &authentication{service: service}
}

// Middleware returns the check as a handler.
func (m *authentication) Middleware() gin.HandlerFunc { return m.handle }

// handle hands the token's claims to the next handlers, or answers 401, or 500 when the check itself failed.
func (m *authentication) handle(ctx *gin.Context) {
	token := network.BearerToken(ctx)
	if token == "" {
		network.SendError(ctx, http.StatusUnauthorized, "missing bearer token")
		return
	}
	claims, err := m.service.Validate(ctx.Request.Context(), token)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	common.SetClaims(ctx, claims)
}
