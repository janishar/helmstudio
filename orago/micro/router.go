package micro

import (
	"log"

	natsmicro "github.com/nats-io/nats.go/micro"

	"github.com/ora/orago/network"
)

// ServiceConfig names the NATS micro service. Replicas in one QueueGroup share its requests.
type ServiceConfig struct {
	Name       string
	Version    string
	QueueGroup string
}

// Router is an HTTP router that also serves NATS endpoints.
type Router struct {
	*network.Router
	service natsmicro.Service
}

// NewRouter works while NATS is down: the service's subscriptions start once the connection does.
func NewRouter(trustedProxies []string, client NatsClient, service ServiceConfig) (*Router, error) {
	router, err := network.NewRouter(trustedProxies)
	if err != nil {
		return nil, err
	}

	natsService, err := natsmicro.AddService(client.Conn(), natsmicro.Config{
		Name:       service.Name,
		Version:    service.Version,
		QueueGroup: service.QueueGroup,
		ErrorHandler: func(_ natsmicro.Service, err *natsmicro.NATSError) {
			log.Printf("nats service error: %v", err)
		},
	})
	if err != nil {
		return nil, err
	}

	return &Router{Router: router, service: natsService}, nil
}

// LoadControllers mounts each controller's HTTP routes and its NATS endpoints, as goserve's micro router does.
// Subjects are rooted at the service name, not the controller's HTTP path: the message contract fixes them, and they
// must not move when a route does.
func (r *Router) LoadControllers(controllers ...Controller) error {
	group := r.service.AddGroup(r.service.Info().Name)
	for _, controller := range controllers {
		r.Router.LoadControllers(controller)
		if err := controller.MountNats(group); err != nil {
			return err
		}
	}
	return nil
}
