package startup

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/middleware"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Module is what the router is built from: Run fills it for production, tests pass their own parts.
type Module struct {
	db              *pgxpool.Pool
	signer          *common.Signer
	registry        *registry.Registry
	cancel          studio.Cancel
	authentication  network.AuthenticationProvider
	recheck         studio.Recheck  // the token check an open event stream repeats
	shutdown        <-chan struct{} // closes when the service stops, which ends open SSE streams
	health          micro.Controller
	maxBody         int64                                                      // bytes
	store           storage.Store                                              // read to check uploads and reference images
	stateGet        studio.StateGet                                            // pipelines.state.get (a row's checkpoint state)
	memory          studio.MemoryAsk                                           // pipelines.memory (a pipeline's memory)
	dispatcher      *studio.Dispatcher                                         // positions use its skip counts; nil in tests
	remoteModels    bool                                                       // the deployment's remote switch (API 1.15)
	providerTest    studio.ProviderTestAsk                                     // pipelines.providers.test
	judgeTest       studio.JudgeTestAsk                                        // pipelines.judge.test (API 1.28)
	assistantMemory studio.AssistantMemoryAsk                                  // pipelines.assistant.memory (API 1.44)
	lookup          studio.Lookup                                              // a server provider's host name (nil: the system resolver)
	purge           func(ctx context.Context, subject string) error            // a CMD subject's messages (API 1.16)
	putSettings     func(ctx context.Context, host string, value []byte) error // KV host_settings (API 1.16)
	settings        *studio.Service                                            // the service Controllers built (it publishes the host settings at start)
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
	service := studio.NewService(m.db, m.signer, m.registry, m.cancel, m.store)
	service.SetStateGet(m.stateGet)
	service.SetMemory(m.memory)
	service.SetDispatcher(m.dispatcher)
	service.SetRemote(m.remoteModels)
	service.SetProviderTest(m.providerTest)
	service.SetJudgeTest(m.judgeTest)
	service.SetLookup(m.lookup)
	service.SetModelOps(m.purge, m.putSettings)
	m.settings = service
	service.SetAssistantMemory(m.assistantMemory)
	galleryService := gallery.NewService(m.db, m.signer, m.store, m.registry, service.FindWorkflow)
	galleryService.SetOffers(service.Offerable)
	api := []micro.Controller{
		studio.NewController(service, m.recheck, m.shutdown),
		gallery.NewController(galleryService),
		lora.NewController(lora.NewService(m.db, m.signer, m.store, m.registry)),
	}
	// the assistant's read tools ask these routes in process (API 1.44)
	local, err := common.LocalEngine(api[0], api[1], api[2])
	if err != nil {
		panic(err) // network.NewRouter fails only on a malformed trusted proxy; LocalEngine passes none
	}
	service.SetLocal(local)
	return append([]micro.Controller{m.health}, api...)
}
