// Package startup: config → object store → router → janitor → serve.
package startup

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"sync"
	"syscall"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"

	"github.com/helmstudio/files_service/api/file"
	"github.com/helmstudio/files_service/config"
)

// Store builds the object store STORAGE_DRIVER names.
func Store(ctx context.Context, env config.Env) (storage.Store, error) {
	switch env.StorageDriver {
	case "memory":
		return storage.NewMemory(), nil
	case "s3":
		return storage.NewS3(ctx, storage.S3Config{Bucket: env.S3Bucket, Region: env.S3Region, Endpoint: env.S3Endpoint,
			AccessKeyID: env.S3AccessKey, SecretAccessKey: env.S3SecretKey, ForcePathStyle: env.S3PathStyle})
	}
	return nil, fmt.Errorf("STORAGE_DRIVER %q: use s3 or memory", env.StorageDriver)
}

// Run wires the service and serves until SIGINT or SIGTERM, then gives requests 10 s to finish. The janitor runs in
// the background on the same context, and Run waits for it after the server stops.
func Run() error {
	env, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := Store(ctx, env)
	if err != nil {
		return err
	}
	module := &Module{store: store, signer: file.NewSigner(env.FileURLSecret)}
	router, err := network.NewRouter(nil)
	if err != nil {
		return err
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	router.LoadControllers(module.Controllers()...)

	var workers sync.WaitGroup
	workers.Go(func() { Janitor(ctx, store, env.WorkRetention) })

	log.Printf("files_service listening on :%s", env.Port)
	err = router.Start(ctx, ":"+env.Port)
	stop() // Start also returns on a failed listen, and ctx must end for the janitor to.
	workers.Wait()
	return err
}
