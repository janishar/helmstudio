package micro

import (
	"context"
	"errors"
	"log"

	"github.com/nats-io/nats.go/jetstream"
)

// NatsJetStreamPutKV sets key to value in a JetStream KV bucket. The bucket must exist; without a deadline on ctx,
// the JetStream default of 5 s applies.
func NatsJetStreamPutKV(ctx context.Context, client NatsClient, bucket, key string, value []byte) error {
	store, err := client.jetStream().KeyValue(ctx, bucket)
	if err != nil {
		return err
	}
	_, err = store.Put(ctx, key, value)
	return err
}

// NatsJetStreamWatchKV hands every value put in a JetStream KV bucket to handler, the current ones first, until ctx
// ends. It retries while NATS or the bucket is missing, and starts over if the watch ends on its own. A handler error
// is logged and the watch goes on: the next put replaces the value.
func NatsJetStreamWatchKV(
	ctx context.Context, client NatsClient, bucket string, handler func(key string, value []byte) error,
) {
	keepRunning(ctx, "watch "+bucket, func() error {
		return watchUntilStopped(ctx, client.jetStream(), bucket, handler)
	})
}

// watchUntilStopped runs one watch. It returns nil when ctx ends, and an error when the bucket can't be read or the
// watch ends on its own.
func watchUntilStopped(
	ctx context.Context, jetStream jetstream.JetStream, bucket string, handler func(key string, value []byte) error,
) error {
	store, err := jetStream.KeyValue(ctx, bucket)
	if err != nil {
		return err
	}
	watcher, err := store.WatchAll(ctx)
	if err != nil {
		return err
	}
	defer watcher.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, open := <-watcher.Updates():
			if !open {
				return errors.New("watch ended")
			}
			if entry == nil || entry.Operation() != jetstream.KeyValuePut {
				continue // nil marks the end of the current values
			}
			if err := handler(entry.Key(), entry.Value()); err != nil {
				log.Printf("watch %s: %s: %v", bucket, entry.Key(), err)
			}
		}
	}
}
