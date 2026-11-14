// Package startup: config → database (migrations) → object store → NATS → router → relay, projector, watcher → serve.
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

	"github.com/ora/orago/micro"
	"github.com/ora/orago/network"
	"github.com/ora/orago/postgres"
	"github.com/ora/orago/storage"

	"github.com/ora/catalog_service/api/auth"
	authmiddleware "github.com/ora/catalog_service/api/auth/middleware"
	"github.com/ora/catalog_service/common"
	"github.com/ora/catalog_service/config"
	"github.com/ora/catalog_service/messaging"
	"github.com/ora/catalog_service/migrations"
	"github.com/ora/catalog_service/registry"
)

// Store builds the object store STORAGE_DRIVER names.
func Store(ctx context.Context, env config.Env) (storage.Store, error) {
	switch env.StorageDriver {
	case "memory":
		return storage.NewMemory(), nil
	case "s3":
		return storage.NewS3(ctx, storage.S3Config{Bucket: env.S3Bucket, Region: env.S3Region, Endpoint: env.S3Endpoint,
			AccessKeyID: env.S3AccessKey, SecretAccessKey: env.S3SecretKey,
			ForcePathStyle: env.S3PathStyle})
	}
	return nil, fmt.Errorf("STORAGE_DRIVER %q: use s3 or memory", env.StorageDriver)
}

// Run wires the service and serves until SIGINT or SIGTERM, then gives requests 10 s to finish. The registry watch,
// projector and relay run in the background on the same context, and Run waits for them after the server
// stops, because the deferred NATS disconnect and pool close would otherwise run under them.
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
	store, err := Store(ctx, env)
	if err != nil {
		return err
	}
	signer := common.NewSigner(env.FileURLSecret, env.PublicBaseURL, env.FileURLTTL)
	client, err := micro.NewNatsClient(micro.Config{
		URL: env.NatsURL, User: env.NatsUser, Password: env.NatsPassword, Name: "catalog_service",
		Timeout: 2 * time.Second, // RequestNats's limit on one token check
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

	module := &Module{
		db:             pool,
		store:          store,
		signer:         signer,
		authentication: authmiddleware.NewAuthenticationProvider(authService),
		agents:         client,
		shutdown:       ctx.Done(),
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
		maxRaw:  env.RawMaxMB << 20,
	}
	// A NATS micro service named catalog, as gomicro's services all are: it has no endpoints, but answers the $SRV
	// discovery verbs.
	router, err := micro.NewRouter(nil, client, micro.ServiceConfig{Name: "catalog", Version: "1.0.0", QueueGroup: "catalog"})
	if err != nil {
		return err
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	if err := router.LoadControllers(module.Controllers()...); err != nil {
		return err
	}

	var workers sync.WaitGroup
	workers.Go(func() { micro.NatsJetStreamWatchKV(ctx, client, "registry", registry.Set) })
	events := &micro.NatsJetStreamGroup{}
	module.Projector().MountNatsJetStream(events)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.ProjectorConsumer, events) })
	workers.Go(func() { messaging.Relay(ctx, pool, messaging.NatsJetStreamPublisher(client)) })

	log.Printf("catalog_service listening on :%s", env.Port)
	err = router.Start(ctx, ":"+env.Port)
	stop() // Start also returns on a failed listen, and ctx must end for the workers to.
	// ponytail: no time limit; a worker stuck in a call that ignores ctx holds the exit until Docker's kill
	workers.Wait()
	return err
}
