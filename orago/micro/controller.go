package micro

import "github.com/helmstudio/orago/network"

// Controller is a network.Controller that can also serve NATS endpoints.
type Controller interface {
	network.Controller
	MountNats(group NatsGroup) error
}

// controller adds a no-op MountNats to the HTTP base controller.
type controller struct {
	network.Controller
}

// NewController returns the base that feature controllers embed, overriding MountRoutes and MountNats as they need.
func NewController(
	path string,
	authentication network.AuthenticationProvider,
	authorization network.AuthorizationProvider,
) Controller {
	return &controller{Controller: network.NewController(path, authentication, authorization)}
}

// MountNats mounts nothing, for controllers that serve HTTP only.
func (c *controller) MountNats(group NatsGroup) error { return nil }

// NewHealthController is network.NewHealthController as a Controller, so a micro service lists its health routes with
// its other controllers, as gomicro's health controller is one. It mounts no NATS endpoints.
func NewHealthController(extraPaths []string, checks ...network.HealthCheck) Controller {
	return &controller{Controller: network.NewHealthController(extraPaths, checks...)}
}
