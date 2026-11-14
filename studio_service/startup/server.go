// Package startup: config → database (migrations) → NATS → router → relay, projector, registry and hosts watches,
// lease sweep → serve.
package startup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/postgres"

	"github.com/helmstudio/studio_service/api/auth"
	authmiddleware "github.com/helmstudio/studio_service/api/auth/middleware"
	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/config"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/migrations"
	"github.com/helmstudio/studio_service/registry"
)

// Run wires the service and serves until SIGINT or SIGTERM, then gives requests 10 s to finish. The background workers
// run on the same context, and Run waits for them after the server stops, because the deferred NATS disconnect and
// pool close would otherwise run under them.
func Run() error {
	env, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, env.DatabaseURL, migrations.FS)
	if err != nil {
		return err
	}
	defer pool.Close()
	client, err := micro.NewNatsClient(micro.Config{
		URL: env.NatsURL, User: env.NatsUser, Password: env.NatsPassword, Name: "studio_service", InboxPrefix: "_INBOX_studio",
		Timeout: 2 * time.Second, // RequestNats's limit on one token check, and on one cancel
	})
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer client.Disconnect()
	natsConn := client.Conn()

	authService := auth.NewService(client)
	if _, err := natsConn.Subscribe("evt.auth.revoked", func(message *nats.Msg) { authService.Revoked(message.Data) }); err != nil {
		return err
	}

	runners := &registry.Registry{}
	module := &Module{
		db:             pool,
		signer:         common.NewSigner(env.FileURLSecret, env.PublicBaseURL, env.FileURLTTL),
		registry:       runners,
		cancel:         messaging.NatsCancel(client),
		authentication: authmiddleware.NewAuthenticationProvider(authService),
		recheck: func(ctx context.Context, token string) error {
			_, err := authService.Validate(ctx, token)
			return err
		},
		shutdown: ctx.Done(),
		health: micro.NewHealthController(nil,
			network.HealthCheck{Name: "database", Ping: pool.Ping},
			network.HealthCheck{Name: "nats", Ping: func(context.Context) error {
				if !natsConn.IsConnected() {
					return errors.New("not connected")
				}
				return nil
			}},
		),
		maxBody: env.MaxBodyMB << 20,
	}
	// A NATS micro service named studio: it has no endpoints, but answers the $SRV discovery verbs.
	router, err := micro.NewRouter(nil, client, micro.ServiceConfig{Name: "studio", Version: "1.0.0", QueueGroup: "studio"})
	if err != nil {
		return err
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	if err := router.LoadControllers(module.Controllers()...); err != nil {
		return err
	}

	var workers sync.WaitGroup
	workers.Go(func() { micro.NatsJetStreamWatchKV(ctx, client, "registry", runners.SetRegistry) })
	workers.Go(func() { micro.NatsJetStreamWatchKV(ctx, client, "hosts", runners.SetHost) })
	events := &micro.NatsJetStreamGroup{}
	studio.NewProjector(pool).MountNatsJetStream(events)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.ProjectorConsumer, events) })
	workers.Go(func() { messaging.Relay(ctx, pool, messaging.NatsJetStreamPublisher(client)) })
	sweeper := studio.NewSweeper(pool, runners, func(ctx context.Context) (int, error) {
		return micro.NatsJetStreamBacklog(ctx, client, messaging.ProjectorConsumer)
	})
	workers.Go(func() { sweeper.Run(ctx) })

	log.Printf("studio_service listening on :%s", env.Port)
	err = router.Start(ctx, ":"+env.Port)
	stop() // Start also returns on a failed listen, and ctx must end for the workers to.
	// ponytail: no time limit; a worker stuck in a call that ignores ctx holds the exit until Docker's kill
	workers.Wait()
	return err
}
