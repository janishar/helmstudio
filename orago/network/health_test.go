package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func healthRouter(t *testing.T, extraPaths []string, checks ...HealthCheck) http.Handler {
	t.Helper()
	router := newRouter(t, nil)
	router.LoadControllers(NewHealthController(extraPaths, checks...))
	return router.Engine()
}

func ok(context.Context) error { return nil }

func fail(context.Context) error { return errors.New("connection refused to 10.0.0.5") }

func call(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestHealthOK(t *testing.T) {
	handler := healthRouter(t, nil, HealthCheck{"database", ok})
	response := call(handler, http.MethodGet, "/health")
	assertResponse(t, response, http.StatusOK, `{"status":"ok"}`)
	assertContentType(t, response)
	if got := call(handler, http.MethodHead, "/health").Code; got != http.StatusOK {
		t.Errorf("HEAD status %d", got)
	}
}

func TestHealthFirstFailureAnswers503(t *testing.T) {
	reachedLast := false
	handler := healthRouter(t, nil,
		HealthCheck{"database", ok},
		HealthCheck{"nats", fail},
		HealthCheck{"later", func(context.Context) error { reachedLast = true; return errors.New("later") }},
	)
	response := call(handler, http.MethodGet, "/health")
	assertResponse(t, response, http.StatusServiceUnavailable, `{"detail":"nats unavailable"}`)
	if reachedLast {
		t.Error("checks must stop at the first failure")
	}
	if got := call(handler, http.MethodHead, "/health").Code; got != http.StatusServiceUnavailable {
		t.Errorf("HEAD status %d", got)
	}
}

func TestHealthExtraPaths(t *testing.T) {
	handler := healthRouter(t, []string{"/auth/health"}, HealthCheck{"database", ok})
	assertResponse(t, call(handler, http.MethodGet, "/auth/health"), http.StatusOK, `{"status":"ok"}`)
	if got := call(handler, http.MethodPost, "/health").Code; got != http.StatusNotFound {
		t.Errorf("POST /health: status %d, want 404", got)
	}
	if got := call(handler, http.MethodHead, "/auth/health").Code; got != http.StatusNotFound {
		t.Errorf("HEAD on an extra path: status %d, want 404", got)
	}
}

func TestHealthChecksGetTimeoutFromRequest(t *testing.T) {
	type key struct{}
	reached := false
	handler := healthRouter(t, nil, HealthCheck{"database", func(ctx context.Context) error {
		reached = true
		if ctx.Value(key{}) != "request" {
			t.Error("check context is not derived from the request")
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("check context has no deadline")
		}
		if left := time.Until(deadline); left > 2*time.Second || left < time.Second {
			t.Errorf("deadline in %v, want about 2s", left)
		}
		return nil
	}})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request.WithContext(context.WithValue(request.Context(), key{}, "request")))
	if !reached {
		t.Fatal("check never ran")
	}
}

func portOf(t *testing.T, server *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestHealthProbe(t *testing.T) {
	status := http.StatusOK
	path := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer server.Close()
	t.Setenv("PORT", portOf(t, server))

	if err := HealthProbe(); err != nil {
		t.Errorf("healthy: %v", err)
	}
	if path != "/health" {
		t.Errorf("probed %q", path)
	}

	for _, status = range []int{http.StatusServiceUnavailable, http.StatusNotFound} {
		if err := HealthProbe(); err == nil {
			t.Errorf("status %d must be an error", status)
		}
	}
}

func TestHealthProbeGivesUpOnAHangingServer(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 2 second timeout")
	}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	t.Setenv("PORT", portOf(t, server))

	start := time.Now()
	if err := HealthProbe(); err == nil {
		t.Error("want an error from a server that never answers")
	}
	if elapsed := time.Since(start); elapsed > 2*healthTimeout {
		t.Errorf("took %v", elapsed)
	}
}

func TestHealthProbeNothingListening(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	port := portOf(t, server)
	server.Close()
	t.Setenv("PORT", port)
	if err := HealthProbe(); err == nil {
		t.Error("want an error when nothing listens")
	}
}

func TestHealthProbeDefaultPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("port 8080 is taken here:", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	t.Setenv("PORT", "")
	if err := HealthProbe(); err != nil {
		t.Errorf("default port: %v", err)
	}
}
