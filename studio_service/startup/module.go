package startup

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/middleware"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Module is what the router is built from: Run fills it for production, tests pass their own parts.
type Module struct {
	db             *pgxpool.Pool
	signer         *common.Signer
	registry       *registry.Registry
	cancel         studio.Cancel
	authentication network.AuthenticationProvider
	recheck        studio.Recheck  // the token check an open event stream repeats
	shutdown       <-chan struct{} // closes when the service stops, which ends open SSE streams
	health         micro.Controller
	maxBody        int64         // bytes
	store          storage.Store // read to check uploads and reference images
}

// RootMiddlewares returns the middlewares in load order. The body limit comes before the guard, so 411 and 413 come
// before 401. The guard covers all of /v1/ as a root middleware, so an unknown /v1 path without a token is 401, not
// 404. /health needs no Bearer token.
func (m *Module) RootMiddlewares() []network.RootMiddleware {
	return []network.RootMiddleware{
		middleware.NewErrorCatcher(), // must be first
		middleware.NewNotFound(),
		middleware.NewBodyLimit(func(string) int64 { return m.maxBody }),
		middleware.NewPrefixGuard("/v1/", m.authentication.Middleware()),
	}
}

// Controllers returns every controller, each a micro.Controller as gomicro's are.
func (m *Module) Controllers() []micro.Controller {
	return []micro.Controller{
		m.health,
		studio.NewController(studio.NewService(m.db, m.signer, m.registry, m.cancel, m.store), m.recheck, m.shutdown),
		gallery.NewController(gallery.NewService(m.db, m.signer, m.store, m.registry)),
	}
}
