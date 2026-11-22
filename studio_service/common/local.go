package common

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/middleware"
	"github.com/helmstudio/helmgo/network"
)

// The assistant's read tools (API 1.42) answer with the API's own shapes, so studio asks its own routes in process:
// LocalEngine mounts the same controllers on an engine that is never served, whose /v1/ guard takes the caller from
// the request's context (WithLocalCaller), never from a header.

type localKey struct{}

// WithLocalCaller returns ctx carrying caller for a LocalEngine request.
func WithLocalCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, localKey{}, caller)
}

// LocalEngine returns the in-process engine of controllers (their HTTP routes only).
func LocalEngine(controllers ...network.Controller) (http.Handler, error) {
	router, err := network.NewRouter(nil)
	if err != nil {
		return nil, err
	}
	router.LoadRootMiddlewares(middleware.NewErrorCatcher(), middleware.NewNotFound(),
		middleware.NewPrefixGuard("/v1/", func(ctx *gin.Context) {
			caller, ok := ctx.Request.Context().Value(localKey{}).(Caller)
			if !ok || caller.ID == "" {
				network.SendError(ctx, http.StatusUnauthorized, "invalid or missing bearer token")
				return
			}
			SetCaller(ctx, caller)
		}))
	router.LoadControllers(controllers...)
	return router.Engine(), nil
}
