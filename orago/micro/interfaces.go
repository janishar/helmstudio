package micro

import natsmicro "github.com/nats-io/nats.go/micro"

// Aliases, so a service's controllers need only this package.
type (
	NatsGroup       = natsmicro.Group
	NatsHandlerFunc = natsmicro.HandlerFunc
	NatsRequest     = natsmicro.Request
)
