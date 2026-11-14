// Package middleware: bearer-token authentication for studio_service's /v1/ routes.
package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/auth"
	"github.com/helmstudio/studio_service/common"
)

// authentication checks access tokens through auth.Service, which asks auth_service over NATS.
type authentication struct {
	service *auth.Service
}

// NewAuthenticationProvider returns the provider that requires a valid access token.
func NewAuthenticationProvider(service *auth.Service) network.AuthenticationProvider {
	return &authentication{service: service}
}

// Middleware returns the check as a handler.
func (m *authentication) Middleware() gin.HandlerFunc { return m.handle }

// handle hands the token's caller and Kong's client id to the next handlers. It answers 503 when auth_service can't
// be reached and 401 for any other failure.
func (m *authentication) handle(ctx *gin.Context) {
	caller, err := m.service.Validate(ctx.Request.Context(), network.BearerToken(ctx))
	switch {
	case errors.Is(err, auth.ErrUnavailable):
		network.SendError(ctx, http.StatusServiceUnavailable, "auth unavailable")
		return
	case err != nil:
		network.SendError(ctx, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}
	caller.ClientID = ctx.GetHeader("X-Client-Id")
	common.SetCaller(ctx, caller)
}
