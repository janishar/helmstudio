package startup

import (
	"github.com/ora/orago/middleware"
	"github.com/ora/orago/network"
	"github.com/ora/orago/storage"

	"github.com/ora/files_service/api/file"
)

// Module is what the router is built from: Run fills it for production, tests pass their own parts.
type Module struct {
	store  storage.Store
	signer *file.Signer
}

// RootMiddlewares returns the middlewares in load order. No route needs a token: the signature is the auth for /files.
func (m *Module) RootMiddlewares() []network.RootMiddleware {
	return []network.RootMiddleware{
		middleware.NewErrorCatcher(), // must be first
		middleware.NewNotFound(),
	}
}

// Controllers returns every controller: /health and /files.
func (m *Module) Controllers() []network.Controller {
	return []network.Controller{network.NewHealthController(nil), file.NewController(m.store, m.signer)}
}
