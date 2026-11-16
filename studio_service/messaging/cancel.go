package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/helmstudio/helmgo/micro"
)

// NatsCancel writes runID as a key in the KV cancels (value: the request time, which runners ignore). A KV put is a
// JetStream publish to $KV.cancels.<key>; it is sent as a core request so the stream's ack, or its error, comes back.
func NatsCancel(client micro.NatsClient) func(ctx context.Context, runID string) error {
	return func(ctx context.Context, runID string) error {
		body, err := client.Request(ctx, "$KV.cancels."+runID, []byte(time.Now().UTC().Format(time.RFC3339)))
		if err != nil {
			return err
		}
		return pubAckError(body)
	}
}

// NatsKVPut writes value under key in KV bucket (a JetStream publish to $KV.<bucket>.<key>, sent as a core request
// so the stream's ack, or its error, comes back): studio's copy of host_settings (API 1.16).
func NatsKVPut(client micro.NatsClient, bucket string) func(ctx context.Context, key string, value []byte) error {
	return func(ctx context.Context, key string, value []byte) error {
		body, err := client.Request(ctx, "$KV."+bucket+"."+key, value)
		if err != nil {
			return err
		}
		return pubAckError(body)
	}
}

// pubAckError reads a JetStream publish ack: nil for a stored message, else the stream's error.
func pubAckError(body []byte) error {
	var ack struct {
		Stream string `json:"stream"`
		Error  *struct {
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		return err
	}
	if ack.Error != nil {
		return errors.New(ack.Error.Description)
	}
	if ack.Stream == "" {
		return errors.New("no stream stored the cancel")
	}
	return nil
}
