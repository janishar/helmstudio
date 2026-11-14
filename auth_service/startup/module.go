package startup

import (
	"context"
	"crypto/rsa"
	"errors"

	"github.com/helmstudio/orago/micro"
	oragomiddleware "github.com/helmstudio/orago/middleware"
	"github.com/helmstudio/orago/network"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/auth_service/api/apikey"
	"github.com/helmstudio/auth_service/api/auth"
	"github.com/helmstudio/auth_service/api/auth/middleware"
	"github.com/helmstudio/auth_service/api/user"
	"github.com/helmstudio/auth_service/config"
)

// Module holds the wired services and builds what the router loads.
type Module struct {
	Auth      *auth.Service
	APIKeys   *apikey.Service
	Users     *user.Service
	db        *pgxpool.Pool
	connected func() bool
}

// NewModule wires the services. publish and connected may be nil (tests, no NATS).
func NewModule(env config.Env, db *pgxpool.Pool, key *rsa.PrivateKey, publish func(string, []byte) error, connected func() bool) *Module {
	authService := auth.NewService(db, env, key, publish)
	return &Module{
		Auth:      authService,
		APIKeys:   apikey.NewService(db),
		Users:     user.NewService(authService, env.AdminEmail),
		db:        db,
		connected: connected,
	}
}

// RootMiddlewares returns the middlewares in load order. The error catcher comes first, so it covers the rest.
func (m *Module) RootMiddlewares() []network.RootMiddleware {
	return []network.RootMiddleware{oragomiddleware.NewErrorCatcher(), oragomiddleware.NewNotFound()}
}

// AuthenticationProvider returns the provider behind Controller.Authentication.
func (m *Module) AuthenticationProvider() network.AuthenticationProvider {
	return middleware.NewAuthenticationProvider(m.Auth)
}

// AuthorizationProvider returns the provider behind Controller.Authorization.
func (m *Module) AuthorizationProvider() network.AuthorizationProvider {
	return middleware.NewAuthorizationProvider()
}

// Controllers returns every controller, each a micro.Controller as gomicro's are: the auth controller mounts the
// auth.validate.* endpoints, the others serve HTTP only.
func (m *Module) Controllers() []micro.Controller {
	authentication, authorization := m.AuthenticationProvider(), m.AuthorizationProvider()
	return []micro.Controller{
		m.healthController(),
		auth.NewController(authentication, authorization, m.Auth),
		user.NewController(authentication, authorization, m.Users),
		apikey.NewController(authentication, authorization, m.APIKeys),
	}
}

// healthController checks the database and, when there is a connection to check, NATS.
func (m *Module) healthController() micro.Controller {
	checks := []network.HealthCheck{{Name: "database", Ping: m.db.Ping}}
	if m.connected != nil {
		checks = append(checks, network.HealthCheck{Name: "nats", Ping: func(context.Context) error {
			if !m.connected() {
				return errors.New("not connected")
			}
			return nil
		}})
	}
	return micro.NewHealthController([]string{"/auth/health"}, checks...)
}
