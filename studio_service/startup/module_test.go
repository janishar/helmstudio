package startup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// denyAll is an authentication provider that refuses every request.
type denyAll struct{}

// Middleware answers 401.
func (denyAll) Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) { network.SendError(ctx, http.StatusUnauthorized, "no") }
}

// Every route mounts on one router without conflicts; /v1 needs auth, unknown paths under it included, and the body
// limit answers before it. /health needs no token; /files is files_service's.
func TestHandlerRoutes(t *testing.T) {
	module := &Module{
		signer:         common.NewSigner("0123456789abcdef", "", 0),
		registry:       &registry.Registry{},
		authentication: denyAll{},
		health: micro.NewHealthController(nil, network.HealthCheck{
			Name: "database", Ping: func(context.Context) error { return nil },
		}),
		maxBody: 1 << 10,
	}
	// The HTTP part of the micro router only: no controller mounts NATS endpoints, so no NATS is needed.
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	for _, controller := range module.Controllers() {
		router.LoadControllers(controller)
	}

	send := func(method, path, contentLength string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Content-Length", contentLength)
		router.Engine().ServeHTTP(recorder, request)
		return recorder.Code
	}
	for path, want := range map[string]int{"/health": 200, "/v1/studio/sessions": 401, "/v1/studio/hosts": 401,
		"/v1/studio/takes": 401, "/v1/studio/sessions/x/takes": 401,
		"/v1/nope": 401, "/files/u/x/y.png": 404, "/nope": 404} {
		if got := send("GET", path, ""); got != want {
			t.Errorf("GET %s: %d, want %d", path, got, want)
		}
	}
	for _, test := range []struct {
		path, contentLength string
		want                int
	}{
		{"/v1/studio/sessions", "", 411},
		{"/v1/studio/sessions", "2048", 413},
		{"/v1/nope", "2048", 413},
		{"/v1/studio/sessions", "100", 401},
	} {
		if got := send("POST", test.path, test.contentLength); got != test.want {
			t.Errorf("POST %s, %q bytes: %d, want %d", test.path, test.contentLength, got, test.want)
		}
	}
}
