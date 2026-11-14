package messaging

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/helmstudio/orago/micro"
)

// NatsPurge removes every message of subject from the stream CMD: the dispatcher drops a lost run's command before it
// queues the run again (it holds at most that command, since a host has one run at a time). A core request to the
// JetStream API, as NatsCancel's KV put is; nats.conf must let studio publish $JS.API.STREAM.PURGE.CMD.
func NatsPurge(client micro.NatsClient) func(ctx context.Context, subject string) error {
	return func(ctx context.Context, subject string) error {
		request, _ := json.Marshal(map[string]string{"filter": subject})
		body, err := client.Request(ctx, "$JS.API.STREAM.PURGE.CMD", request)
		if err != nil {
			return err
		}
		return purgeError(body)
	}
}

// purgeError reads a stream purge answer: nil on success, else the stream's error.
func purgeError(body []byte) error {
	var answer struct {
		Success bool `json:"success"`
		Error   *struct {
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return err
	}
	if answer.Error != nil {
		return errors.New(answer.Error.Description)
	}
	if !answer.Success {
		return errors.New("the purge was not done")
	}
	return nil
}
