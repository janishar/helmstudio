package network

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newRouter(t *testing.T, trustedProxies []string) *Router {
	t.Helper()
	router, err := NewRouter(trustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func TestNewRouterRejectsBadProxy(t *testing.T) {
	if _, err := NewRouter([]string{"not-an-address"}); err == nil {
		t.Error("want an error for a bad proxy")
	}
}

func TestTrailingSlashIs404NotRedirect(t *testing.T) {
	router := newRouter(t, nil)
	router.Engine().GET("/things", func(ctx *gin.Context) { SendNoContent(ctx) })
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/things/", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", recorder.Code)
	}
}

func TestWrongMethodIs404(t *testing.T) {
	router := newRouter(t, nil)
	router.Engine().GET("/things", func(ctx *gin.Context) { SendNoContent(ctx) })
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/things", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", recorder.Code)
	}
}

func TestContextDoneFollowsRequest(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestCtx)
	reached := false
	serve(t, request, func(ctx *gin.Context) {
		reached = true
		if ctx.Done() == nil {
			t.Fatal("ctx.Done() is nil")
		}
		cancel()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Error("ctx.Done() did not follow the request")
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("ctx.Err() = %v", ctx.Err())
		}
	})
	if !reached {
		t.Fatal("handler never ran")
	}
}

// clientIP answers what ctx.ClientIP() gives for this peer and these headers.
func clientIP(t *testing.T, trustedProxies []string, peer string, header http.Header) string {
	t.Helper()
	router := newRouter(t, trustedProxies)
	router.Engine().POST("/signin", func(ctx *gin.Context) { ctx.String(http.StatusOK, ctx.ClientIP()) })
	request := httptest.NewRequest(http.MethodPost, "/signin", nil)
	request.RemoteAddr = peer
	request.Header = header
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, request)
	return recorder.Body.String()
}

var kongAndWeb = []string{"172.30.0.10/32", "172.30.0.11/32"}

func TestClientIP(t *testing.T) {
	for _, c := range []struct{ peer, forwarded, want string }{
		{"203.0.113.9:1234", "", "203.0.113.9"},                           // direct: the peer
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},                    // an untrusted peer can't forward
		{"172.30.0.10:1234", "172.30.0.1", "172.30.0.1"},                  // Kong vouches for the client
		{"172.30.0.10:1234", "1.2.3.4, 172.30.0.1", "172.30.0.1"},         // a client's own XFF stays left of it
		{"172.30.0.10:1234", "198.51.100.7, 172.30.0.11", "198.51.100.7"}, // browser → web → Kong
		{"172.30.0.10:1234", "9.9.9.9, 198.51.100.7, 172.30.0.11", "198.51.100.7"},
	} {
		header := http.Header{}
		if c.forwarded != "" {
			header.Set("X-Forwarded-For", c.forwarded)
		}
		if got := clientIP(t, kongAndWeb, c.peer, header); got != c.want {
			t.Errorf("%s %q: got %s, want %s", c.peer, c.forwarded, got, c.want)
		}
	}
}

func TestClientIPTwoForwardedForLines(t *testing.T) {
	header := http.Header{"X-Forwarded-For": {"1.2.3.4", "198.51.100.7, 172.30.0.11"}}
	if got := clientIP(t, kongAndWeb, "172.30.0.10:1234", header); got != "198.51.100.7" {
		t.Errorf("got %s, want 198.51.100.7", got)
	}
}

func TestClientIPIgnoresLoneRealIP(t *testing.T) {
	header := http.Header{"X-Real-Ip": {"1.2.3.4"}}
	if got := clientIP(t, kongAndWeb, "172.30.0.10:1234", header); got != "172.30.0.10" {
		t.Errorf("got %s, want the peer", got)
	}
}

func TestNilTrustedProxiesTrustNoOne(t *testing.T) {
	header := http.Header{"X-Forwarded-For": {"1.2.3.4"}}
	if got := clientIP(t, nil, "127.0.0.1:1234", header); got != "127.0.0.1" {
		t.Errorf("got %s, want the peer", got)
	}
}

type attachFunc func(engine *gin.Engine)

func (f attachFunc) Attach(engine *gin.Engine) { f(engine) }

type mountedController struct {
	Controller
}

func (c *mountedController) MountRoutes(group *gin.RouterGroup) {
	group.GET("/ping", func(ctx *gin.Context) { SendJSON(ctx, http.StatusOK, "pong") })
}

func TestLoadControllersUsesPathAndRootMiddleware(t *testing.T) {
	router := newRouter(t, nil)
	router.LoadRootMiddlewares(attachFunc(func(engine *gin.Engine) {
		engine.Use(func(ctx *gin.Context) { ctx.Header("X-Root", "ran") })
	}))
	router.LoadControllers(
		&mountedController{Controller: NewController("/a", nil, nil)},
		NewController("/empty", nil, nil), // the base MountRoutes adds nothing
	)
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/a/ping", nil))
	assertResponse(t, recorder, http.StatusOK, `"pong"`)
	if recorder.Header().Get("X-Root") != "ran" {
		t.Error("root middleware did not run")
	}
}

// freeAddress reserves a port and releases it for Start, which takes an address rather than a listener.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

// startWithBlockedRequest starts a server and sends a request that stays in flight until release closes.
func startWithBlockedRequest(t *testing.T, release <-chan struct{}) (cancel func(), started, clientDone <-chan error) {
	t.Helper()
	inHandler := make(chan struct{})
	router := newRouter(t, nil)
	router.Engine().GET("/slow", func(ctx *gin.Context) {
		close(inHandler)
		<-release
		SendJSON(ctx, http.StatusOK, "done")
	})
	address := freeAddress(t)
	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)

	startResult := make(chan error, 1)
	go func() { startResult <- router.Start(ctx, address) }()

	clientResult := make(chan error, 1)
	go func() {
		var response *http.Response
		var err error
		// Start has no ready signal, so retry until the port accepts.
		for range 100 {
			response, err = http.Get("http://" + address + "/slow")
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			clientResult <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && string(body) != `"done"` {
			err = errors.New("unexpected body: " + string(body))
		}
		clientResult <- err
	}()

	select {
	case <-inHandler:
	case err := <-startResult:
		t.Fatalf("Start returned early: %v", err)
	case err := <-clientResult:
		t.Fatalf("request failed: %v", err)
	}
	return cancelCtx, startResult, clientResult
}

func TestServerSettings(t *testing.T) {
	server := newRouter(t, nil).newServer(":0")
	if server.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v", server.ReadHeaderTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v would end uploads and SSE", server.WriteTimeout)
	}
}

func TestStartWaitsForInFlightRequest(t *testing.T) {
	release := make(chan struct{})
	cancel, startResult, clientResult := startWithBlockedRequest(t, release)

	cancel()
	select {
	case err := <-startResult:
		close(release)
		t.Fatalf("Start returned with a request in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-startResult:
		if err != nil {
			t.Errorf("Start = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the request finished")
	}
	if err := <-clientResult; err != nil {
		t.Errorf("the in-flight request was cut off: %v", err)
	}
}

func TestStartPassesOnShutdownError(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 10 second shutdown limit")
	}
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	cancel, startResult, _ := startWithBlockedRequest(t, release)

	cancel()
	select {
	case err := <-startResult:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Start = %v, want the deadline error", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("Start did not honour the shutdown limit")
	}
}

func TestStartReturnsListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	router := newRouter(t, nil)
	result := make(chan error, 1)
	go func() { result <- router.Start(context.Background(), listener.Addr().String()) }()
	select {
	case err := <-result:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Start = %v, want the listen error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return the listen error")
	}
}

func TestStartReturnsWhenCtxAlreadyEnded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Listening and shutdown race here, so either result is fine; Start only must not hang.
	router := newRouter(t, nil)
	result := make(chan error, 1)
	go func() { result <- router.Start(ctx, listener.Addr().String()) }()
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("Start hung")
	}
}
