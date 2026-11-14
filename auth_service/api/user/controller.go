package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/auth_service/api/user/dto"
	"github.com/helmstudio/auth_service/common"
)

// controller serves the current user's profile and the admin user routes.
type controller struct {
	micro.Controller
	service *Service
}

// NewController returns the controller.
func NewController(
	authentication network.AuthenticationProvider,
	authorization network.AuthorizationProvider,
	service *Service,
) micro.Controller {
	return &controller{
		Controller: micro.NewController("/auth", authentication, authorization),
		service:    service,
	}
}

// MountRoutes registers /me for a signed-in user and the /admin routes for the admin role.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/me", c.Authentication(), c.meHandler)

	admin := group.Group("/admin/users", c.Authentication(), c.Authorization("admin"))
	admin.GET("", c.listHandler)
	admin.POST("", c.createHandler)
	admin.PUT("/:id/disabled", c.disableHandler)
	admin.PUT("/:id/roles", c.rolesHandler)
	admin.PUT("/:id/password", c.passwordHandler)
}

// sendUser answers with the user's current state.
func (c *controller) sendUser(ctx *gin.Context, id string) {
	user, err := c.service.Auth.GetUser(ctx.Request.Context(), id)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, user)
}

// meHandler returns the caller's own user.
func (c *controller) meHandler(ctx *gin.Context) {
	c.sendUser(ctx, common.MustClaims(ctx).Subject)
}

// listHandler returns every user.
func (c *controller) listHandler(ctx *gin.Context) {
	users, err := c.service.Auth.ListUsers(ctx.Request.Context())
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, map[string]any{"users": users})
}

// createHandler makes an account (sign-up is closed) and answers 201 with it.
func (c *controller) createHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.NewUser](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	user, err := c.service.Create(ctx.Request.Context(), body)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusCreated, user)
}

// editable answers 409 for the ADMIN_EMAIL account (set by auth's .env) and reports whether to go on.
func (c *controller) editable(ctx *gin.Context, id string) bool {
	if err := c.service.Editable(ctx.Request.Context(), id); err != nil {
		network.SendMixedError(ctx, err)
		return false
	}
	return true
}

// disableHandler sets or clears a user's disabled state, and answers with the updated user.
func (c *controller) disableHandler(ctx *gin.Context) {
	id, err := network.PathUUID(ctx, "id")
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	body, err := network.ReqBody[dto.Disabled](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if !c.editable(ctx, id) {
		return
	}
	if err := c.service.SetDisabled(ctx.Request.Context(), id, *body.Disabled); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	c.sendUser(ctx, id)
}

// rolesHandler replaces a user's roles, and answers with the updated user.
func (c *controller) rolesHandler(ctx *gin.Context) {
	id, err := network.PathUUID(ctx, "id")
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	body, err := network.ReqBody[dto.Roles](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if !c.editable(ctx, id) {
		return
	}
	if err := c.service.SetRoles(ctx.Request.Context(), id, body.Roles); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	c.sendUser(ctx, id)
}

// passwordHandler sets a user's password without needing the old one.
func (c *controller) passwordHandler(ctx *gin.Context) {
	id, err := network.PathUUID(ctx, "id")
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	body, err := network.ReqBody[dto.Password](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if !c.editable(ctx, id) {
		return
	}
	if err := c.service.Auth.SetPassword(ctx.Request.Context(), id, body.Password, "admin"); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}
