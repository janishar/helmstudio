package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ora/orago/network"
)

// prefixGuard pairs a path prefix with the guard that protects it.
type prefixGuard struct {
	prefix string
	guard  gin.HandlerFunc
}

// NewPrefixGuard runs guard on every request under prefix, matched by a route or not, so an unknown path under
// prefix is denied before it can answer 404. The guard denies by aborting.
func NewPrefixGuard(prefix string, guard gin.HandlerFunc) network.RootMiddleware {
	return &prefixGuard{prefix: prefix, guard: guard}
}

// Attach runs the guard check on every request.
func (m *prefixGuard) Attach(engine *gin.Engine) {
	engine.Use(m.handle)
}

// handle checks URL.Path, the decoded path Gin routes on, so an encoded spelling can't reach a route around the
// guard.
func (m *prefixGuard) handle(ctx *gin.Context) {
	if strings.HasPrefix(ctx.Request.URL.Path, m.prefix) {
		m.guard(ctx)
	}
}
