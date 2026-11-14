package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/helmgo/network"
)

// notFound is the handler for unmatched routes.
type notFound struct{}

// NewNotFound answers unmatched routes in the contract's error shape. Gin's default is a plain-text body.
func NewNotFound() network.RootMiddleware {
	return &notFound{}
}

// Attach registers it as Gin's NoRoute handler.
func (m *notFound) Attach(engine *gin.Engine) {
	engine.NoRoute(m.handle)
}

// handle writes the 404.
func (m *notFound) handle(ctx *gin.Context) {
	network.SendError(ctx, http.StatusNotFound, "Not Found")
}
