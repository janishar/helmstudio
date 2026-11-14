package micro

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// NatsJetStreamHandlerFunc handles one message of a consumer. It decodes the body with JsonToMsg and finishes the
// message with AckNatsJetStreamMessage, NakNatsJetStreamMessage or TermNatsJetStreamMessage, as a NATS handler answers
// with RespondNatsMessage or RespondNatsError. It must finish the message before it returns and not keep msg after:
// the consumer naks a message left unfinished. Unlike a NatsHandlerFunc it takes a ctx, so a shutdown can cancel its
// work.
type NatsJetStreamHandlerFunc func(ctx context.Context, msg *NatsJetStreamMessage)

// NatsJetStreamGroup routes the messages of one consumer to handlers by subject, as a NatsGroup routes requests to its
// endpoints. A service fills it in MountNatsJetStream.
type NatsJetStreamGroup struct {
	handlers []natsJetStreamHandler
}

// natsJetStreamHandler is one handler and the subject it takes.
type natsJetStreamHandler struct {
	subject string
	handle  NatsJetStreamHandlerFunc
}

// AddHandler adds handler for the messages whose subject matches subject: * matches one token, > the rest.
func (g *NatsJetStreamGroup) AddHandler(subject string, handler NatsJetStreamHandlerFunc) {
	g.handlers = append(g.handlers, natsJetStreamHandler{subject: subject, handle: handler})
}

// ErrNatsJetStreamMessageTerminated marks the error Handle returns for a dropped message, so a test can tell a drop
// (never redelivered) from a nak (redelivered after NakDelay).
var ErrNatsJetStreamMessageTerminated = errors.New("terminated")

// Handle runs the handler for subject on body with no NATS involved, so tests can call it directly. It returns nil
// when the handler acked the message, and otherwise the error it nak'd or dropped the message with (never nil); a
// drop's error also matches ErrNatsJetStreamMessageTerminated.
func (g *NatsJetStreamGroup) Handle(ctx context.Context, subject string, body []byte) error {
	msg := &NatsJetStreamMessage{subject: subject, data: body, durable: "test"}
	g.run(ctx, msg)
	if msg.terminated {
		return fmt.Errorf("%w: %w", ErrNatsJetStreamMessageTerminated, msg.err)
	}
	return msg.err
}

// run hands msg to the first handler whose subject matches. A message no handler takes is dropped; one the handler
// left unfinished is nak'd, so the consumer doesn't wait out AckWait for it.
func (g *NatsJetStreamGroup) run(ctx context.Context, msg *NatsJetStreamMessage) {
	for _, handler := range g.handlers {
		if subjectMatches(handler.subject, msg.subject) {
			handler.handle(ctx, msg)
			if !msg.finished {
				NakNatsJetStreamMessage(ctx, msg, errors.New("the handler did not finish the message"))
			}
			return
		}
	}
	TermNatsJetStreamMessage(msg, errors.New("no handler for its subject"))
}

// subjectMatches reports whether subject matches pattern, token by token: * matches one token, > the rest.
func subjectMatches(pattern, subject string) bool {
	patternTokens, subjectTokens := strings.Split(pattern, "."), strings.Split(subject, ".")
	for i, token := range patternTokens {
		if token == ">" {
			return len(subjectTokens) > i
		}
		if i >= len(subjectTokens) || (token != "*" && token != subjectTokens[i]) {
			return false
		}
	}
	return len(patternTokens) == len(subjectTokens)
}

// NatsJetStreamMessage is one message as a handler sees it. One of AckNatsJetStreamMessage, NakNatsJetStreamMessage
// and TermNatsJetStreamMessage finishes it.
type NatsJetStreamMessage struct {
	subject    string
	data       []byte
	delivery   jetstream.Msg // nil when Handle runs a handler without NATS
	nakDelay   time.Duration
	durable    string
	finished   bool
	terminated bool  // dropped rather than nak'd
	err        error // why it was nak'd or dropped
}

// Subject returns the subject the message was published on.
func (m *NatsJetStreamMessage) Subject() string { return m.subject }

// Data returns the message body, for JsonToMsg.
func (m *NatsJetStreamMessage) Data() []byte { return m.data }

// finish records how the message ended: err is nil for an ack, the reason for a nak or a drop. It reports false when
// a helper already finished it, so a second call sends nothing.
func (m *NatsJetStreamMessage) finish(err error) bool {
	if m.finished {
		return false
	}
	m.finished, m.err = true, err
	return true
}

// AckNatsJetStreamMessage marks msg done: the stream never redelivers it.
func AckNatsJetStreamMessage(msg *NatsJetStreamMessage) {
	if msg.finish(nil) && msg.delivery != nil {
		_ = msg.delivery.Ack()
	}
}

// NakNatsJetStreamMessage gives msg back for another try after the consumer's NakDelay. It logs err unless ctx has
// ended: a shutdown is not a failure.
func NakNatsJetStreamMessage(ctx context.Context, msg *NatsJetStreamMessage, err error) {
	if err == nil {
		err = errors.New("nak'd with no reason given")
	}
	if !msg.finish(err) {
		return
	}
	if ctx.Err() == nil {
		log.Printf("consumer %s: %s: %s", msg.durable, msg.subject, withCause(err))
	}
	if msg.delivery != nil {
		_ = msg.delivery.NakWithDelay(msg.nakDelay)
	}
}

// TermNatsJetStreamMessage logs err and drops msg: the stream never redelivers it. It is for a message that can never
// succeed, such as one JsonToMsg can't decode.
func TermNatsJetStreamMessage(msg *NatsJetStreamMessage, err error) {
	if err == nil {
		err = errors.New("dropped with no reason given")
	}
	if !msg.finish(err) {
		return
	}
	msg.terminated = true
	log.Printf("consumer %s: dropping %s: %s", msg.durable, msg.subject, withCause(err))
	if msg.delivery != nil {
		_ = msg.delivery.Term()
	}
}

// withCause is err's text and, when err wraps a cause with more to say, the cause's too: JsonToMsg's errors are
// APIErrors whose text is only "bad request: invalid JSON", and the log should say which field broke.
func withCause(err error) string {
	cause := errors.Unwrap(err)
	if cause == nil || cause.Error() == err.Error() {
		return err.Error()
	}
	return fmt.Sprintf("%v (%v)", err, cause)
}
