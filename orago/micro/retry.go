package micro

import (
	"context"
	"log"
	"time"
)

// retryDelay is how long NatsJetStreamConsume and NatsJetStreamWatchKV wait before trying again while NATS, the stream
// or the bucket is missing.
const retryDelay = 3 * time.Second

// keepRunning calls run until ctx ends, logging each error and waiting retryDelay before the next call. run returns nil
// only when ctx has ended; any error means it stopped on its own and should start over.
func keepRunning(ctx context.Context, what string, run func() error) {
	for {
		err := run()
		if ctx.Err() != nil {
			return
		}
		log.Printf("%s: %v (retrying)", what, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}
