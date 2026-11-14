// Package micro is the NATS side of orago services: a client that survives NATS being down, a router that adds a
// NATS micro service to the HTTP router, typed request and respond helpers over flat JSON bodies, and the JetStream
// helpers (publish, a durable consumer with handlers by subject, a KV watch) that reach JetStream through the client.
package micro

import (
	"context"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// drainTimeout is how long Disconnect waits for in-flight messages.
const drainTimeout = 5 * time.Second

// Config holds the settings for NewNatsClient. Password is sent only when User is set, and Timeout is the one
// Request (and so RequestNats) applies.
type Config struct {
	URL      string
	User     string
	Password string
	Name     string
	Timeout  time.Duration
	// InboxPrefix replaces _INBOX for this client's replies (requests and JetStream), so nats.conf can let each user
	// subscribe to its own inboxes only; empty keeps _INBOX.
	InboxPrefix string
}

// NatsClient is a connection to NATS, as goserve's micro.NatsClient is: RequestNats asks through it, NewRouter adds
// the service's NATS micro service to it, and the NatsJetStream helpers reach JetStream through it. A service keeps it
// in the service that calls other services. It is an interface so that service's tests can pass a fake: a struct that
// embeds NatsClient and overrides Request. Such a fake serves RequestNats only: its other methods reach the nil embedded
// value, so NewRouter or a NatsJetStream helper given it panics.
type NatsClient interface {
	// Conn is the connection, for core publish and subscribe and for the NATS micro service.
	Conn() *nats.Conn
	// Request sends data to subject and returns the reply's body, within ctx and the client's Timeout.
	Request(ctx context.Context, subject string, data []byte) ([]byte, error)
	// Disconnect waits for in-flight messages, then closes the connection.
	Disconnect()
	// jetStream is the connection's JetStream context. Unexported, so only this package's NatsJetStream helpers reach
	// JetStream (orago/Makefile check).
	jetStream() jetstream.JetStream
}

// natsClient is the NatsClient NewNatsClient makes: a connection, its JetStream context and the timeout Request applies.
type natsClient struct {
	conn             *nats.Conn
	timeout          time.Duration
	jetStreamContext jetstream.JetStream
	closed           chan struct{}
}

// NewNatsClient keeps retrying in the background, so a service can start, and report unhealthy, while NATS is down.
func NewNatsClient(config Config) (NatsClient, error) {
	closed := make(chan struct{})
	options := []nats.Option{
		nats.Name(config.Name),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.DrainTimeout(drainTimeout),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) { log.Printf("nats disconnected: %v", err) }),
		nats.ReconnectHandler(func(*nats.Conn) { log.Print("nats reconnected") }),
	}
	if config.User != "" {
		options = append(options, nats.UserInfo(config.User, config.Password))
	}
	if config.InboxPrefix != "" {
		options = append(options, nats.CustomInboxPrefix(config.InboxPrefix))
	}

	conn, err := nats.Connect(config.URL, options...)
	if err != nil {
		return nil, err
	}
	// jetstream.New sends nothing, so this works while NATS is down, and costs nothing in a service that never uses it.
	jetStream, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &natsClient{conn: conn, timeout: config.Timeout, jetStreamContext: jetStream, closed: closed}, nil
}

// Disconnect waits for in-flight messages, so a caller can close what their handlers use next. Drain itself returns
// early, and if NATS is down it closes the connection and reports that, so the wait is always on the close.
func (c *natsClient) Disconnect() {
	if err := c.conn.Drain(); err != nil {
		log.Printf("nats drain: %v", err)
	}
	<-c.closed
}

// Conn returns the connection.
func (c *natsClient) Conn() *nats.Conn { return c.conn }

// Request sends data to subject and returns the reply's body. The client's Timeout bounds it, inside ctx's own limit.
func (c *natsClient) Request(ctx context.Context, subject string, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	message, err := c.conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, err
	}
	return message.Data, nil
}

// jetStream returns the connection's JetStream context.
func (c *natsClient) jetStream() jetstream.JetStream { return c.jetStreamContext }
