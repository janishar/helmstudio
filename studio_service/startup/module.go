package startup

import (
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/middleware"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"

	"github.com/helmstudio/studio_service/api/catalog"
	"github.com/helmstudio/studio_service/common"
)

// rawPath is the raw image upload route, the one route with the larger body limit.
var rawPath = regexp.MustCompile(`^/v1/catalog/sessions/[^/]+/raw/?$`)

// Module is what the router is built from: Run fills it for production, tests pass their own parts.
type Module struct {
	db             *pgxpool.Pool
	store          storage.Store
	signer         *common.Signer
	authentication network.AuthenticationProvider
	agents         micro.NatsClient // answers catalog.interpret and catalog.poses.suggest; nil in tests (those routes 503)
	shutdown       <-chan struct{}  // closes when the service stops, which ends open SSE streams
	health         micro.Controller
	maxBody        int64 // bytes
	maxRaw         int64 // bytes, POST /v1/catalog/sessions/{id}/raw
}

// RootMiddlewares returns the middlewares in load order. The body limit comes before the guard, so 411 and 413 come
// before 401. The guard covers all of /v1/ as a root middleware, so an unknown /v1 path without a token is 401, not
// 404. /health needs no Bearer token.
func (m *Module) RootMiddlewares() []network.RootMiddleware {
	return []network.RootMiddleware{
		middleware.NewErrorCatcher(), // must be first
		middleware.NewNotFound(),
		middleware.NewBodyLimit(m.bodyLimit),
		middleware.NewPrefixGuard("/v1/", m.AuthenticationProvider().Middleware()),
	}
}

// AuthenticationProvider returns the provider behind the /v1/ guard.
func (m *Module) AuthenticationProvider() network.AuthenticationProvider { return m.authentication }

// Controllers returns every controller, each a micro.Controller as gomicro's are.
func (m *Module) Controllers() []micro.Controller {
	return []micro.Controller{
		m.health,
		catalog.NewController(catalog.NewService(m.db, m.store, m.signer, m.agents, catalog.NatsStopper(m.agents)), m.shutdown),
	}
}

// Projector returns the projector Run consumes evt.catalog.> with.
func (m *Module) Projector() *catalog.Projector {
	return catalog.NewProjector(m.db, catalog.NatsStopper(m.agents))
}

// bodyLimit returns the body size path may send: raw image uploads get their own, larger limit.
func (m *Module) bodyLimit(path string) int64 {
	if rawPath.MatchString(path) {
		return m.maxRaw
	}
	return m.maxBody
}
