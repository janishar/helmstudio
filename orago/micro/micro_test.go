package micro

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	natsmicro "github.com/nats-io/nats.go/micro"

	"github.com/helmstudio/orago/network"
)

const quickly = 2 * time.Second

// deadURL is a local address nothing listens on.
func deadURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return "nats://" + listener.Addr().String()
}

func newDeadClient(t *testing.T, timeout time.Duration) NatsClient {
	t.Helper()
	client, err := NewNatsClient(Config{URL: deadURL(t), Name: "test", Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Conn().Close)
	return client
}

func newDeadRouter(t *testing.T) *Router {
	t.Helper()
	router, err := NewRouter(nil, newDeadClient(t, time.Second), ServiceConfig{
		Name: "auth", Version: "1.0.0", QueueGroup: "auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func TestNewNatsClientReturnsWhileNatsIsDown(t *testing.T) {
	start := time.Now()
	client, err := NewNatsClient(Config{URL: deadURL(t), Name: "test", Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("NewNatsClient: %v", err)
	}
	defer client.Conn().Close()

	if elapsed := time.Since(start); elapsed > quickly {
		t.Errorf("took %v", elapsed)
	}
	if client.Conn().IsConnected() {
		t.Error("connected to a dead port")
	}
	if client.(*natsClient).timeout != 3*time.Second {
		t.Errorf("timeout = %v", client.(*natsClient).timeout)
	}
}

func TestNewNatsClientOptions(t *testing.T) {
	client, err := NewNatsClient(Config{URL: deadURL(t), Name: "test", User: "someone", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Conn().Close()

	options := client.Conn().Opts
	if options.Name != "test" || options.User != "someone" || options.Password != "secret" {
		t.Errorf("name, user, password = %q, %q, %q", options.Name, options.User, options.Password)
	}
	if !options.RetryOnFailedConnect || options.MaxReconnect != -1 {
		t.Errorf("RetryOnFailedConnect = %v, MaxReconnect = %d", options.RetryOnFailedConnect, options.MaxReconnect)
	}
	if options.DisconnectedErrCB == nil || options.ReconnectedCB == nil {
		t.Error("disconnects and reconnects are not logged")
	}
}

func TestNewNatsClientSendsNoCredentialsWithoutUser(t *testing.T) {
	client := newDeadClient(t, time.Second)
	if client.Conn().Opts.User != "" || client.Conn().Opts.Password != "" {
		t.Errorf("user, password = %q, %q", client.Conn().Opts.User, client.Conn().Opts.Password)
	}
}

func TestDisconnectClosesWhileNatsIsDown(t *testing.T) {
	client := newDeadClient(t, time.Second)
	client.Disconnect()
	if !client.Conn().IsClosed() {
		t.Error("connection still open")
	}
}

func TestNewRouterReturnsWhileNatsIsDown(t *testing.T) {
	client := newDeadClient(t, time.Second)
	start := time.Now()
	router, err := NewRouter(nil, client, ServiceConfig{Name: "auth", Version: "1.0.0", QueueGroup: "auth"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if elapsed := time.Since(start); elapsed > quickly {
		t.Errorf("took %v", elapsed)
	}
	if client.Conn().IsConnected() {
		t.Error("connected to a dead port")
	}
	if router.Engine() == nil {
		t.Error("no HTTP engine")
	}
}

func TestNewRouterReturnsServiceErrors(t *testing.T) {
	_, err := NewRouter(nil, newDeadClient(t, time.Second), ServiceConfig{Name: "auth", Version: "not semver"})
	if !errors.Is(err, natsmicro.ErrConfigValidation) {
		t.Errorf("err = %v", err)
	}
}

func TestNewRouterReturnsTrustedProxyErrors(t *testing.T) {
	_, err := NewRouter([]string{"not an address"}, newDeadClient(t, time.Second), ServiceConfig{
		Name: "auth", Version: "1.0.0",
	})
	if err == nil {
		t.Error("no error")
	}
}

type validateController struct {
	Controller
}

func (c *validateController) MountRoutes(group *gin.RouterGroup) {
	group.GET("/ping", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
}

func (c *validateController) MountNats(group NatsGroup) error {
	validate := group.AddGroup("validate")
	if err := validate.AddEndpoint("token", NatsHandlerFunc(func(NatsRequest) {})); err != nil {
		return err
	}
	return validate.AddEndpoint("role", NatsHandlerFunc(func(NatsRequest) {}))
}

func TestLoadControllersRootsSubjectsAtServiceName(t *testing.T) {
	router := newDeadRouter(t)
	controller := &validateController{Controller: NewController("/v1/elsewhere", nil, nil)}
	if err := router.LoadControllers(controller); err != nil {
		t.Fatalf("LoadControllers: %v", err)
	}

	var subjects []string
	for _, endpoint := range router.service.Info().Endpoints {
		subjects = append(subjects, endpoint.Subject)
		if endpoint.QueueGroup != "auth" {
			t.Errorf("%s: queue group = %q", endpoint.Subject, endpoint.QueueGroup)
		}
	}
	slices.Sort(subjects)
	if want := []string{"auth.validate.role", "auth.validate.token"}; !slices.Equal(subjects, want) {
		t.Errorf("subjects = %v, want %v", subjects, want)
	}
}

func TestSubjectsFollowTheServiceName(t *testing.T) {
	router, err := NewRouter(nil, newDeadClient(t, time.Second), ServiceConfig{Name: "billing", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &validateController{Controller: NewController("/", nil, nil)}
	if err := router.LoadControllers(controller); err != nil {
		t.Fatalf("LoadControllers: %v", err)
	}

	for _, endpoint := range router.service.Info().Endpoints {
		if !strings.HasPrefix(endpoint.Subject, "billing.validate.") {
			t.Errorf("subject %q is not rooted at the service name", endpoint.Subject)
		}
	}
}

func TestLoadControllersMountsHTTPRoutes(t *testing.T) {
	router := newDeadRouter(t)
	controller := &validateController{Controller: NewController("/v1/elsewhere", nil, nil)}
	if err := router.LoadControllers(controller); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/elsewhere/ping", nil))
	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d", recorder.Code)
	}
}

func TestBaseControllerAddsNoEndpoints(t *testing.T) {
	router := newDeadRouter(t)
	if err := router.LoadControllers(NewController("/things", nil, nil)); err != nil {
		t.Fatalf("LoadControllers: %v", err)
	}
	if endpoints := router.service.Info().Endpoints; len(endpoints) != 0 {
		t.Errorf("endpoints = %v", endpoints)
	}
}

type failingController struct {
	Controller
	err     error
	mounted *int
}

func (c *failingController) MountNats(NatsGroup) error {
	*c.mounted++
	return c.err
}

func TestLoadControllersMountsHTTPOnlyControllers(t *testing.T) {
	router := newDeadRouter(t)
	ok := func(context.Context) error { return nil }
	health := NewHealthController(nil, network.HealthCheck{Name: "database", Ping: ok})
	if err := router.LoadControllers(NewController("/things", nil, nil), health); err != nil {
		t.Fatalf("LoadControllers: %v", err)
	}
	if endpoints := router.service.Info().Endpoints; len(endpoints) != 0 {
		t.Errorf("endpoints = %v, want none", endpoints)
	}
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", recorder.Code)
	}
}

func TestLoadControllersStopsAtFirstError(t *testing.T) {
	first := errors.New("first")
	mounted := 0
	err := newDeadRouter(t).LoadControllers(
		&failingController{NewController("/a", nil, nil), first, &mounted},
		&failingController{NewController("/b", nil, nil), errors.New("second"), &mounted},
	)
	if !errors.Is(err, first) {
		t.Errorf("err = %v", err)
	}
	if mounted != 1 {
		t.Errorf("MountNats ran %d times", mounted)
	}
}

type tokenRequest struct {
	Token string `json:"token"`
}

type tokenReply struct {
	Valid  bool   `json:"valid"`
	UserID string `json:"user_id"`
}

func TestRequestFailsWithinTimeoutWhenNoServerAnswers(t *testing.T) {
	client := newDeadClient(t, 50*time.Millisecond)

	start := time.Now()
	reply, err := RequestNats[tokenRequest, tokenReply](context.Background(), client, "auth.validate.token", &tokenRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if reply != nil {
		t.Errorf("reply = %+v", reply)
	}
	if elapsed := time.Since(start); elapsed > quickly {
		t.Errorf("took %v", elapsed)
	}
}

func TestRequestFollowsCallerCancellation(t *testing.T) {
	client := newDeadClient(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RequestNats[tokenRequest, tokenReply](ctx, client, "auth.validate.token", &tokenRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

type recordingRequest struct {
	NatsRequest
	body []byte
}

func (r *recordingRequest) Respond(body []byte, _ ...natsmicro.RespondOpt) error {
	r.body = body
	return nil
}

func (r *recordingRequest) Subject() string { return "test.subject" }

func TestBodiesAreFlat(t *testing.T) {
	request := &recordingRequest{}
	RespondNatsMessage(request, &tokenReply{Valid: true, UserID: "u1"})
	if want := `{"valid":true,"user_id":"u1"}`; string(request.body) != want {
		t.Errorf("body = %s, want %s", request.body, want)
	}

	reply, err := JsonToMsg[tokenReply](request.body)
	if err != nil {
		t.Fatalf("JsonToMsg: %v", err)
	}
	if *reply != (tokenReply{Valid: true, UserID: "u1"}) {
		t.Errorf("reply = %+v", reply)
	}
}

// validated is a body with a Validate method, as a service's message types have.
type validated struct {
	Token string `json:"token"`
}

func (v *validated) Validate() string {
	if v.Token == "" {
		return `want {"token": ...}`
	}
	return ""
}

func TestJsonToMsgDecodesAndValidates(t *testing.T) {
	if body, err := JsonToMsg[validated]([]byte(`{"token": "t"}`)); err != nil || body.Token != "t" {
		t.Errorf("body %+v, err %v", body, err)
	}
	for input, want := range map[string]string{
		`not json`:      "bad request: invalid JSON",
		`{"token": 5}`:  "bad request: invalid JSON",
		`{}`:            `bad request: want {"token": ...}`,
		`{"token": ""}`: `bad request: want {"token": ...}`,
	} {
		_, err := JsonToMsg[validated]([]byte(input))
		var apiError *network.APIError
		if !errors.As(err, &apiError) || apiError.Message != want {
			t.Errorf("%s: %v, want %q", input, err, want)
		}
	}
}

func TestRespondLogsEncodingErrors(t *testing.T) {
	request := &recordingRequest{}
	RespondNatsMessage(request, &struct{ Bad chan int }{})
	if request.body != nil {
		t.Errorf("body = %s", request.body)
	}
}

// RespondNatsError sends an APIError's message as today's {"error"} reply, and hides any other error.
func TestRespondNatsErrorIsFlat(t *testing.T) {
	for err, want := range map[error]string{
		network.NewUnauthorizedError("invalid or expired token", nil):       `{"error":"invalid or expired token"}`,
		network.NewBadRequestError(`bad request: want {"token": ...}`, nil): `{"error":"bad request: want {\"token\": ...}"}`,
		errors.New("pool closed"): `{"error":"internal error"}`,
	} {
		request := &recordingRequest{}
		RespondNatsError(request, err)
		if string(request.body) != want {
			t.Errorf("%v: body %s, want %s", err, request.body, want)
		}
	}
}

// fakeClient answers requests without NATS, as a service's tests fake the client its service keeps: it embeds
// NatsClient for the methods it doesn't use and overrides Request.
type fakeClient struct {
	NatsClient
	subject string
	reply   string
	err     error
}

// Request records the subject and answers with the canned reply or error.
func (f *fakeClient) Request(_ context.Context, subject string, _ []byte) ([]byte, error) {
	f.subject = subject
	return []byte(f.reply), f.err
}

func TestRequestNatsThroughAFakeClient(t *testing.T) {
	client := &fakeClient{reply: `{"valid": true, "user_id": "u1"}`}
	reply, err := RequestNats[tokenRequest, tokenReply](context.Background(), client, "auth.validate.token", &tokenRequest{})
	if err != nil || *reply != (tokenReply{Valid: true, UserID: "u1"}) || client.subject != "auth.validate.token" {
		t.Errorf("reply %+v, err %v, subject %q", reply, err, client.subject)
	}
	client.reply = "not json"
	if _, err := RequestNats[tokenRequest, tokenReply](context.Background(), client, "s", &tokenRequest{}); err == nil {
		t.Error("an undecodable reply gave no error")
	}
}
