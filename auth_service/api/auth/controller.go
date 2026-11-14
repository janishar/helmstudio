package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/auth_service/api/auth/dto"
	"github.com/helmstudio/auth_service/api/auth/message"
	"github.com/helmstudio/auth_service/common"
)

// validateTimeout bounds one token check over NATS.
const validateTimeout = 2 * time.Second

// controller serves sign-in, sessions, password change and the JWKS, and the token checks over NATS.
type controller struct {
	micro.Controller
	service *Service
}

// NewController returns the controller. The module passes the providers in, because their package imports this one.
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

// MountRoutes registers the routes; signout and password need a signed-in user. There is no sign-up: the root admin
// creates every account (POST /auth/admin/users).
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.POST("/signin", c.signinHandler)
	group.POST("/refresh", c.refreshHandler)
	group.DELETE("/signout", c.Authentication(), c.signoutHandler)
	group.PUT("/password", c.Authentication(), c.passwordHandler)
	group.GET("/.well-known/jwks.json", c.jwksHandler)
}

// MountNats registers subjects auth.validate.token and auth.validate.role.
func (c *controller) MountNats(group micro.NatsGroup) error {
	validate := group.AddGroup("validate")
	if err := validate.AddEndpoint("token", micro.NatsHandlerFunc(c.validateTokenHandler)); err != nil {
		return err
	}
	return validate.AddEndpoint("role", micro.NatsHandlerFunc(c.validateRoleHandler))
}

// validateTokenHandler answers auth.validate.token with the token's claims.
func (c *controller) validateTokenHandler(req micro.NatsRequest) {
	body, err := micro.JsonToMsg[message.TokenRequest](req.Data())
	if err != nil {
		micro.RespondNatsError(req, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	claims, err := c.service.Validate(ctx, body.Token)
	if err != nil {
		micro.RespondNatsError(req, err)
		return
	}

	micro.RespondNatsMessage(req, message.NewReply(claims))
}

// validateRoleHandler answers auth.validate.role with the token's claims and whether they hold the role.
func (c *controller) validateRoleHandler(req micro.NatsRequest) {
	body, err := micro.JsonToMsg[message.RoleRequest](req.Data())
	if err != nil {
		micro.RespondNatsError(req, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	claims, err := c.service.Validate(ctx, body.Token)
	if err != nil {
		micro.RespondNatsError(req, err)
		return
	}

	micro.RespondNatsMessage(req, message.NewRoleReply(claims, body.Role))
}

// signinHandler exchanges an email and password for a session. The client IP feeds the attempt limit.
func (c *controller) signinHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.SigninRequest](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	session, err := c.service.Signin(ctx.Request.Context(), body, ctx.ClientIP())
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, session)
}

// refreshHandler swaps a refresh token for a new session. An empty token is a 401, not a 422.
func (c *controller) refreshHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.RefreshRequest](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if body.RefreshToken == "" {
		network.SendMixedError(ctx, ErrUnauthorized)
		return
	}
	session, err := c.service.Refresh(ctx.Request.Context(), body.RefreshToken)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, http.StatusOK, session)
}

// signoutHandler ends the session of the caller's access token.
func (c *controller) signoutHandler(ctx *gin.Context) {
	if err := c.service.Signout(ctx.Request.Context(), common.MustClaims(ctx)); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// passwordHandler changes the caller's own password.
func (c *controller) passwordHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.PasswordChange](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	err = c.service.ChangePassword(ctx.Request.Context(), common.MustClaims(ctx), body.CurrentPassword, body.NewPassword)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// jwksHandler serves the public signing key; caches may keep it for five minutes.
func (c *controller) jwksHandler(ctx *gin.Context) {
	ctx.Header("Cache-Control", "public, max-age=300")
	network.SendJSON(ctx, http.StatusOK, c.service.JWKS())
}
