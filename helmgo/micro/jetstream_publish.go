package micro

import (
	"context"
	"encoding/json"

	"github.com/nats-io/nats.go/jetstream"
)

// NatsJetStreamPublish stores data as flat JSON in the stream that holds subject and waits for the stream's ack. A
// non-empty msgID becomes Nats-Msg-Id, so a message published twice within the stream's duplicate window is stored
// once. Without a deadline on ctx, the JetStream default of 5 s applies.
func NatsJetStreamPublish[T any](ctx context.Context, client NatsClient, subject string, data *T, msgID string) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var options []jetstream.PublishOpt
	if msgID != "" {
		options = append(options, jetstream.WithMsgID(msgID))
	}
	_, err = client.jetStream().Publish(ctx, subject, body, options...)
	return err
}
