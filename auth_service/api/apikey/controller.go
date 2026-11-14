package apikey

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/auth_service/api/apikey/dto"
)

// controller serves the admin API key routes and the internal key check.
type controller struct {
	micro.Controller
	service *Service
}

// NewController returns the controller. Its path is the root because /verify sits outside /auth.
func NewController(
	authentication network.AuthenticationProvider,
	authorization network.AuthorizationProvider,
	service *Service,
) micro.Controller {
	return &controller{
		Controller: micro.NewController("/", authentication, authorization),
		service:    service,
	}
}

// MountRoutes registers the internal key check, never routed by Kong, and the routes that need the admin role.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/verify/apikey", c.verifyHandler)

	admin := group.Group("/auth/admin/apikeys", c.Authentication(), c.Authorization("admin"))
	admin.POST("", c.createHandler)
	admin.GET("", c.listHandler)
	admin.DELETE("/:id", c.revokeHandler)
}

// verifyHandler checks the x-api-key header for the Kong plugin: 200 with the client, or 401.
func (c *controller) verifyHandler(ctx *gin.Context) {
	verified, err := c.service.Verify(ctx.Request.Context(), ctx.GetHeader(network.APIKeyHeader))
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, verified)
}

// createHandler issues a key. no-store keeps the raw key, which is shown only here, out of caches.
func (c *controller) createHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.Create](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	created, err := c.service.Create(ctx.Request.Context(), body)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	network.SendJSON(ctx, http.StatusCreated, created)
}

// listHandler returns every key's metadata, never the raw key.
func (c *controller) listHandler(ctx *gin.Context) {
	keys, err := c.service.List(ctx.Request.Context())
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, map[string]any{"api_keys": keys})
}

// revokeHandler disables a key. A malformed, unknown or already revoked id is a 404.
func (c *controller) revokeHandler(ctx *gin.Context) {
	id, err := network.PathUUID(ctx, "id")
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if err := c.service.Revoke(ctx.Request.Context(), id); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}
