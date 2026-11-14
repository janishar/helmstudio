package network

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// healthTimeout keeps the probe and the checks inside Docker's 3 second health check.
const healthTimeout = 2 * time.Second

// HealthCheck is one dependency a service needs. Name appears in the 503 detail.
type HealthCheck struct {
	Name string
	Ping func(context.Context) error
}

// healthController is the Controller behind the health routes.
type healthController struct {
	Controller
	extraPaths []string
	checks     []HealthCheck
}

// NewHealthController answers on /health and on extraPaths, such as a path a gateway routes to this service.
func NewHealthController(extraPaths []string, checks ...HealthCheck) Controller {
	return &healthController{
		Controller: NewController("/", nil, nil),
		extraPaths: extraPaths,
		checks:     checks,
	}
}

// MountRoutes serves /health on GET and HEAD, and extraPaths on GET only.
func (c *healthController) MountRoutes(group *gin.RouterGroup) {
	group.GET("/health", c.healthHandler)
	group.HEAD("/health", c.healthHandler)
	for _, path := range c.extraPaths {
		group.GET(path, c.healthHandler)
	}
}

// healthHandler answers 503 naming the first failed check, or 200. All checks share one healthTimeout.
func (c *healthController) healthHandler(ctx *gin.Context) {
	checkCtx, cancel := context.WithTimeout(ctx.Request.Context(), healthTimeout)
	defer cancel()
	for _, check := range c.checks {
		if err := check.Ping(checkCtx); err != nil {
			SendError(ctx, http.StatusServiceUnavailable, check.Name+" unavailable")
			return
		}
	}
	SendJSON(ctx, http.StatusOK, map[string]string{"status": "ok"})
}

// HealthProbe calls this service's own /health, for the container health check.
func HealthProbe() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := http.Client{Timeout: healthTimeout}
	response, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: status %d", response.StatusCode)
	}
	return nil
}
