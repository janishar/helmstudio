// Package network is the HTTP edge of orago services: a configured Gin router, the controller shape, request
// parsing, and the {"detail": string} error responses the API contract fixes.
package network

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Server timeouts: reading a request's headers, and waiting for requests in flight at shutdown.
const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// RootMiddleware applies to the whole engine. Load these before controllers: Gin's Use only covers later routes.
type RootMiddleware interface {
	Attach(engine *gin.Engine)
}

// Router owns the Gin engine and runs the HTTP server.
type Router struct {
	engine *gin.Engine
}

// NewRouter returns a Router with orago's Gin settings. It fails on a malformed trusted proxy.
// SetMode changes Gin's global state, so it applies to the whole process.
func NewRouter(trustedProxies []string) (*Router, error) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	// A trailing slash is a 404 today. Gin's default is a redirect.
	engine.RedirectTrailingSlash = false

	// Gin's default leaves ctx.Done() nil, so passing ctx would drop cancellation.
	engine.ContextWithFallback = true

	// Only X-Forwarded-For names the client, and only from trusted proxies.
	// Gin's default also reads X-Real-IP and trusts every proxy.
	engine.RemoteIPHeaders = []string{"X-Forwarded-For"}
	if err := engine.SetTrustedProxies(trustedProxies); err != nil {
		return nil, err
	}

	return &Router{engine: engine}, nil
}

// LoadRootMiddlewares attaches the middlewares in order, so the first one runs outermost.
func (r *Router) LoadRootMiddlewares(middlewares ...RootMiddleware) {
	for _, middleware := range middlewares {
		middleware.Attach(r.engine)
	}
}

// LoadControllers mounts each controller's routes on a group at its path.
func (r *Router) LoadControllers(controllers ...Controller) {
	for _, controller := range controllers {
		controller.MountRoutes(r.engine.Group(controller.Path()))
	}
}

// Engine returns the underlying Gin engine.
func (r *Router) Engine() *gin.Engine { return r.engine }

// newServer builds the HTTP server. There is no WriteTimeout: it would end uploads and SSE.
func (r *Router) newServer(address string) *http.Server {
	return &http.Server{Addr: address, Handler: r.engine, ReadHeaderTimeout: readHeaderTimeout}
}

// Start serves until ctx ends, then returns only once Shutdown has returned, so a caller that exits or closes the
// database next can't cut off requests that finish within shutdownTimeout.
func (r *Router) Start(ctx context.Context, address string) error {
	server := r.newServer(address)
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.ListenAndServe() }()

	select {
	case err := <-serveResult:
		return err // Nothing has called Shutdown yet, so this is a real failure, a taken port for one.
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	stopErr := server.Shutdown(shutdownCtx)

	// ctx may have ended just as listening failed, and that failure must not be lost.
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return stopErr
}
