package messaging

import "testing"

// A stored put acks with its stream; a refused one carries the stream's error.
func TestPubAckError(t *testing.T) {
	if err := pubAckError([]byte(`{"stream":"KV_cancels","seq":3}`)); err != nil {
		t.Fatal(err)
	}
	if err := pubAckError([]byte(`{"error":{"code":503,"description":"no responders"}}`)); err == nil || err.Error() != "no responders" {
		t.Fatal(err)
	}
	if pubAckError([]byte(`{}`)) == nil || pubAckError([]byte(`nope`)) == nil {
		t.Fatal("an answer without a stream is not an ack")
	}
}
