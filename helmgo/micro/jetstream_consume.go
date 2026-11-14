package micro

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NatsJetStreamConsumer names a durable pull consumer. NatsJetStreamConsume creates it or updates it to match, so the
// service that reads a stream owns its consumer's settings, not the setup script. Acks are always explicit.
type NatsJetStreamConsumer struct {
	Stream        string
	Durable       string
	Subject       string        // the one filter subject: it keeps the create call on the subject nats.conf permits
	AckWait       time.Duration // redelivered when a handler takes longer; 0 is the server's 30 s
	MaxAckPending int           // 1 handles one message at a time, in stream order
	NakDelay      time.Duration // wait before a nak'd message comes back
}

// NatsJetStreamConsume hands each message of the consumer to its handler in group until ctx ends. A message no handler
// takes is dropped, and one its handler left unfinished is nak'd. It retries every few seconds while NATS or the
// stream is missing and recreates the consumer when the server no longer has it (deleted, or lost with the server's
// store while the client was away), so a service can start before the streams exist. It returns only after the
// message in hand is finished, so the caller can then close what the handlers use.
func NatsJetStreamConsume(
	ctx context.Context, client NatsClient, consumer NatsJetStreamConsumer, group *NatsJetStreamGroup,
) {
	keepRunning(ctx, "consumer "+consumer.Durable, func() error {
		return consumeUntilStopped(ctx, client.jetStream(), consumer, group)
	})
}

// NatsJetStreamBacklog returns how many messages the durable consumer has yet to deliver or is waiting to have acked.
func NatsJetStreamBacklog(ctx context.Context, client NatsClient, consumer NatsJetStreamConsumer) (int, error) {
	durable, err := client.jetStream().Consumer(ctx, consumer.Stream, consumer.Durable)
	if err != nil {
		return 0, err
	}
	info, err := durable.Info(ctx)
	if err != nil {
		return 0, err
	}
	return int(info.NumPending) + info.NumAckPending, nil
}

// consumeUntilStopped runs one consumption. It returns nil when ctx ends, and an error when the consumer can't be
// created or the server stopped it.
func consumeUntilStopped(
	ctx context.Context, jetStream jetstream.JetStream, consumer NatsJetStreamConsumer, group *NatsJetStreamGroup,
) error {
	durable, err := jetStream.CreateOrUpdateConsumer(ctx, consumer.Stream, jetstream.ConsumerConfig{
		Durable: consumer.Durable, FilterSubject: consumer.Subject, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: consumer.AckWait, MaxAckPending: consumer.MaxAckPending,
	})
	if err != nil {
		return err
	}
	consuming, err := durable.Consume(func(delivery jetstream.Msg) {
		group.run(ctx, &NatsJetStreamMessage{subject: delivery.Subject(), data: delivery.Data(), delivery: delivery,
			nakDelay: consumer.NakDelay, durable: consumer.Durable})
	}, jetstream.ConsumeErrHandler(func(consuming jetstream.ConsumeContext, err error) {
		if ctx.Err() != nil {
			return // shutting down: Stop's own errors are no news
		}
		log.Printf("consumer %s: %v", consumer.Durable, err)
		if consumerLost(ctx, durable, err) {
			consuming.Stop() // Closed fires below, and keepRunning creates the consumer again
		}
	}))
	if err != nil {
		return err
	}
	// Closed must be asked before Stop: asked after, it can report closed while the handler is still running.
	stopped := consuming.Closed()
	select {
	case <-ctx.Done():
		consuming.Stop()
		<-stopped
		return nil
	case <-stopped:
		return errors.New("the consumer stopped") // deleted or lost on the server: keepRunning recreates it
	}
}

// consumerLost reports whether a consume error means the server no longer has the consumer: a pull answered with "no
// responders", or heartbeats stopped and the server says the consumer or its stream is not found. A consumer lost
// while the connection was down, such as with a server restarted on an empty store, shows up only this way: no
// "consumer deleted" status reaches the client then, and nats.go would keep pulling from it forever.
func consumerLost(ctx context.Context, durable jetstream.Consumer, err error) bool {
	if errors.Is(err, nats.ErrNoResponders) {
		return true
	}
	if !errors.Is(err, jetstream.ErrNoHeartbeat) {
		return false
	}
	infoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, infoErr := durable.Info(infoCtx)
	return errors.Is(infoErr, jetstream.ErrConsumerNotFound) || errors.Is(infoErr, jetstream.ErrStreamNotFound)
}
