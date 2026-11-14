package network

import "github.com/gin-gonic/gin"

// Controller is one feature's routes under a base path.
type Controller interface {
	Path() string
	Authentication() gin.HandlerFunc
	Authorization(roles ...string) gin.HandlerFunc
	MountRoutes(group *gin.RouterGroup)
}

// AuthenticationProvider supplies the middleware that authenticates a request.
type AuthenticationProvider interface {
	Middleware() gin.HandlerFunc
}

// AuthorizationProvider supplies the middleware that requires one of the given roles.
type AuthorizationProvider interface {
	Middleware(roles ...string) gin.HandlerFunc
}

// controller is the default Controller that NewController returns.
type controller struct {
	path           string
	authentication AuthenticationProvider
	authorization  AuthorizationProvider
}

// NewController returns the base that feature controllers embed, overriding MountRoutes. A provider may be nil when
// the controller never calls its method.
func NewController(
	path string,
	authentication AuthenticationProvider,
	authorization AuthorizationProvider,
) Controller {
	return &controller{path: path, authentication: authentication, authorization: authorization}
}

// Path returns the base path the controller's routes hang under.
func (c *controller) Path() string { return c.path }

// Authentication returns the provider's middleware. It panics on a nil provider.
func (c *controller) Authentication() gin.HandlerFunc { return c.authentication.Middleware() }

// Authorization returns the provider's middleware for roles. It panics on a nil provider.
func (c *controller) Authorization(roles ...string) gin.HandlerFunc {
	return c.authorization.Middleware(roles...)
}

// MountRoutes mounts nothing; feature controllers override it.
func (c *controller) MountRoutes(group *gin.RouterGroup) {}
