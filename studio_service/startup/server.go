// Package startup: config → database (migrations) → NATS → router → relay, projector, registry and hosts watches,
// dispatcher → serve.
package startup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/postgres"

	"github.com/helmstudio/studio_service/api/auth"
	authmiddleware "github.com/helmstudio/studio_service/api/auth/middleware"
	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio"
	"github.com/helmstudio/studio_service/api/studio/message"
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
	dispatcher := studio.NewDispatcher(pool, runners, messaging.NatsPurge(client), func(ctx context.Context) (int, error) {
		return micro.NatsJetStreamBacklog(ctx, client, messaging.ProjectorConsumer)
	})
	dispatcher.Unload = client.Request // runner.unload.<host>.<env>: the hand-over between envs of one host
	module := &Module{
		dispatcher:     dispatcher,
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
		// pipelines answers within 5 s or the state endpoint is a 503 (the client's 2 s Timeout is for token checks)
		stateGet: func(ctx context.Context, req message.StateGet) (message.StateReply, error) {
			var reply message.StateReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectStateGet, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// the same 5 s for a pipeline's memory (API 1.14)
		memory: func(ctx context.Context, req message.MemoryRequest) (message.MemoryReply, error) {
			var reply message.MemoryReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectMemory, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// a judge test's template or verdict: the same 5 s (API 1.28)
		judgeTest: func(ctx context.Context, req message.JudgeTestRequest) (message.JudgeTestReply, error) {
			var reply message.JudgeTestReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectJudgeTest, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// the assistant's memory: the same 5 s (API 1.44)
		assistantMemory: func(ctx context.Context, req message.AssistantMemoryRequest) (message.AssistantMemoryReply, error) {
			var reply message.AssistantMemoryReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectAssistantMemory, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// the assistant's prompt cache hits: the same 5 s (API 1.66)
		assistantCache: func(ctx context.Context, req message.AssistantCacheRequest) (message.AssistantCacheReply, error) {
			var reply message.AssistantCacheReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectAssistantCache, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		remoteModels:  env.RemoteModels,
		pluginDirs:    env.PluginDirs,
		userPluginDir: env.UserPluginDir,
		// a provider test makes one or two HTTP calls in pipelines: 30 s (API 1.15)
		providerTest: func(ctx context.Context, req message.ProviderTestRequest) (message.ProviderTestReply, error) {
			var reply message.ProviderTestReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectProviderTest, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// a key file's status: pipelines stats it (API 1.51): 5 s
		providerKey: func(ctx context.Context, req message.ProviderKeyRequest) (message.ProviderKeyReply, error) {
			var reply message.ProviderKeyReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectProviderKey, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// a key to save or remove (API 1.52): save checks the key with the provider (15 s in pipelines): 30 s; remove 5 s.
		// The request holds the key: it is never logged here
		providerKeyWrite: func(ctx context.Context, req message.ProviderKeyWriteRequest) (message.ProviderKeyWriteReply, error) {
			var reply message.ProviderKeyWriteReply
			body, _ := json.Marshal(req)
			wait := 5 * time.Second
			if req.Op == "save" {
				wait = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectProviderKeyWrite, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// a plugin's key from its host (API 1.73): 5 s; the reply holds the key: it is never logged here
		pluginKey: func(ctx context.Context, host string, req message.PluginKeyRequest) (message.PluginKeyReply, error) {
			var reply message.PluginKeyReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectPluginKey+host, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
		// what each model folder path is on its host (API 1.78): 5 s
		foldersCheck: func(ctx context.Context, host string, req message.FoldersCheckRequest) (message.FoldersCheckReply, error) {
			var reply message.FoldersCheckReply
			body, _ := json.Marshal(req)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			answer, err := natsConn.RequestWithContext(ctx, message.SubjectFoldersCheck+host, body)
			if err == nil {
				err = json.Unmarshal(answer.Data, &reply)
			}
			return reply, err
		},
	}
	module.purge, module.putSettings = messaging.NatsPurge(client), messaging.NatsKVPut(client, "host_settings")
	if module.store, err = objectStore(ctx, env); err != nil {
		return err
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
	if added, err := module.settings.ScanPlugins(ctx); err != nil { // API 1.61: plugins/ on start (new ones stay off)
		log.Printf("plugins: scan failed: %v", err)
	} else if len(added) > 0 {
		log.Printf("plugins: found %v (off)", added)
	}
	go module.settings.WatchPlugins(ctx, 10*time.Second) // API 1.64: ~/.helm/plugins changed

	var workers sync.WaitGroup
	runners.Await("registry", "hosts") // the dispatcher fails no run on a view not loaded yet
	workers.Go(func() {
		micro.NatsJetStreamWatchKV(ctx, client, "registry", runners.SetRegistry, func() { runners.Loaded("registry") })
	})
	workers.Go(func() {
		micro.NatsJetStreamWatchKVAt(ctx, client, "hosts", runners.SetHostAt, func() { runners.Loaded("hosts") })
	})
	events := &micro.NatsJetStreamGroup{}
	projector := studio.NewProjector(pool)
	projector.Workflows = runners // names a take's workflow and model
	projector.RemoteOn = env.RemoteModels
	projector.Store = module.store
	projector.MountNatsJetStream(events)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.ProjectorConsumer, events) })
	pipelineEvents := &micro.NatsJetStreamGroup{}
	projector.MountPipeline(pipelineEvents)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.PipelineConsumer, pipelineEvents) })
	workers.Go(func() {
		// outputs stored before the gallery become takes; ponytail: a fixed wait for the registry watch to fill (it
		// names the workflow and model); a take made without it keeps the workflow id as its title
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		if made, err := projector.Backfill(ctx); err != nil {
			log.Printf("take backfill: %v", err)
		} else if made > 0 {
			log.Printf("take backfill: %d run outputs became takes", made)
		}
	})
	modelEvents := &micro.NatsJetStreamGroup{}
	projector.MountModels(modelEvents)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.ModelsConsumer, modelEvents) })
	workers.Go(func() { studio.RunModelJobs(ctx, pool, runners, module.purge) })
	mediaEvents := &micro.NatsJetStreamGroup{}
	projector.MountMedia(mediaEvents)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.MediaConsumer, mediaEvents) })
	workers.Go(func() { studio.RunMediaJobs(ctx, pool, runners, module.purge, module.cancel, module.store) })
	workers.Go(func() { studio.RunThumbs(ctx, pool, runners) }) // API 1.48: thumbnails of takes without one
	workers.Go(func() {
		// KV host_settings is a copy of studio's table: written at every start (API 1.16)
		if err := module.settings.PublishSettings(ctx); err != nil && ctx.Err() == nil {
			log.Printf("host settings: %v", err)
		}
	})
	assistantEvents := &micro.NatsJetStreamGroup{}
	projector.MountAssistant(assistantEvents)
	workers.Go(func() { micro.NatsJetStreamConsume(ctx, client, messaging.AssistantConsumer, assistantEvents) })
	workers.Go(func() { module.settings.RunAssistantSweep(ctx) })
	workers.Go(func() { messaging.Relay(ctx, pool, messaging.NatsJetStreamPublisher(client)) })
	workers.Go(func() { dispatcher.Run(ctx) })
	workers.Go(func() { gallery.NewSweeper(pool, module.store).Run(ctx) })
	workers.Go(func() { lora.RunSweep(ctx, pool, module.store) })

	log.Printf("studio_service listening on :%s", env.Port)
	err = router.Start(ctx, ":"+env.Port)
	stop() // Start also returns on a failed listen, and ctx must end for the workers to.
	// ponytail: no time limit; a worker stuck in a call that ignores ctx holds the exit until Docker's kill
	workers.Wait()
	return err
}
