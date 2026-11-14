package messaging

import (
	"testing"
	"time"

	"github.com/ora/orago/micro"
)

// NatsJetStreamConsume updates a consumer to match its config: a change here changes the live consumer. Change it
// only on purpose (docs/ORAGO.md, section 6, "Durable consumers").
func TestProjectorConsumerKeepsTodaysSettings(t *testing.T) {
	want := micro.NatsJetStreamConsumer{Stream: "EVT", Durable: "catalog-projector", Subject: "evt.catalog.>",
		AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 2 * time.Second}
	if ProjectorConsumer != want {
		t.Errorf("ProjectorConsumer = %+v, want %+v", ProjectorConsumer, want)
	}
}
