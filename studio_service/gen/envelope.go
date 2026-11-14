// Package gen: NATS message structs from contracts/messages (hand-kept; see Makefile gen).
package gen

import (
	"encoding/json"
	"time"
)

type Trace struct {
	Traceparent   string `json:"traceparent"`
	Tracestate    string `json:"tracestate,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// Message is contracts/messages/envelope.schema.json with its data typed as D.
type Message[D any] struct {
	Type       string    `json:"type"`
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	JobID      *string   `json:"job_id"`
	OwnerID    string    `json:"owner_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Trace      Trace     `json:"trace"`
	Data       D         `json:"data"`
}

// Envelope is a message whose data isn't decoded: the relay wraps stored commands in it, the SSE stream passes data on.
type Envelope = Message[json.RawMessage]
