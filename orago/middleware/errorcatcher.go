// Package middleware holds the root middlewares of orago services. They attach to the engine, not to a group,
// because Gin runs group middleware only on matched routes and these must also cover unmatched ones.
package middleware

import (
	"log"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/orago/network"
)

// errorCatcher is the panic-recovering middleware.
type errorCatcher struct{}

// NewErrorCatcher turns a panic into a logged 500. Load it first, so it also covers the middlewares after it.
func NewErrorCatcher() network.RootMiddleware {
	return &errorCatcher{}
}

// Attach runs the catcher around everything loaded after it.
func (m *errorCatcher) Attach(engine *gin.Engine) {
	engine.Use(m.handle)
}

// handle runs the rest of the chain with catch deferred.
func (m *errorCatcher) handle(ctx *gin.Context) {
	defer m.catch(ctx)
	ctx.Next()
}

// catch recovers a panic, so it must be the deferred function itself. It answers 500 when it still can.
func (m *errorCatcher) catch(ctx *gin.Context) {
	recovered := recover()
	if recovered == nil {
		return
	}

	// net/http compares this by identity and aborts the connection without logging, so it must pass through unchanged.
	if recovered == http.ErrAbortHandler {
		panic(recovered)
	}

	log.Printf("panic: %v\n%s", recovered, debug.Stack())

	// Once a response has started, as an SSE stream has, a 500 can't be sent. Dropping the connection lets the client
	// see the failure, where ending normally would make a cut-off response look complete.
	if ctx.Writer.Written() {
		panic(http.ErrAbortHandler)
	}

	network.SendError(ctx, http.StatusInternalServerError, "internal error")
}
