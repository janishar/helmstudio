package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ora/orago/network"
)

const (
	testLimit  = 1024
	largeLimit = 4096
	largePath  = "/v1/large"
	secret     = "secret panic value"
	tokenValue = "Bearer token"
)

func requireToken(ctx *gin.Context) {
	if ctx.GetHeader("Authorization") == "" {
		network.SendError(ctx, http.StatusUnauthorized, "missing token")
	}
}

func limitFor(path string) int64 {
	if path == largePath {
		return largeLimit
	}
	return testLimit
}

// newStack builds the chain the services load, in their order.
func newStack(t *testing.T) *gin.Engine {
	t.Helper()
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(
		NewErrorCatcher(),
		NewNotFound(),
		NewBodyLimit(limitFor),
		NewPrefixGuard("/v1/", requireToken),
	)
	return router.Engine()
}

func serve(engine *gin.Engine, method, path string, authorized bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authorized {
		request.Header.Set("Authorization", tokenValue)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertDetail(t *testing.T, recorder *httptest.ResponseRecorder, status int, detail string) {
	t.Helper()
	if recorder.Code != status {
		t.Errorf("status %d, want %d", recorder.Code, status)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	want := fmt.Sprintf(`{"detail":%q}`, detail)
	if got := recorder.Body.String(); got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return buffer
}

func panicsWithSecret(*gin.Context) {
	panic(secret)
}

func TestPanicIsJSON500(t *testing.T) {
	captureLog(t)
	engine := newStack(t)
	engine.GET("/plain", panicsWithSecret)
	engine.GET("/typed", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/csv")
		ctx.Status(http.StatusCreated)
		panic(errors.New(secret))
	})

	for _, path := range []string{"/plain", "/typed"} {
		assertDetail(t, serve(engine, http.MethodGet, path, false), http.StatusInternalServerError, "internal error")
	}
}

func TestPanicIsLoggedWithStack(t *testing.T) {
	logged := captureLog(t)
	engine := newStack(t)
	engine.GET("/boom", panicsWithSecret)

	serve(engine, http.MethodGet, "/boom", false)

	if !strings.Contains(logged.String(), secret) {
		t.Errorf("log lacks the panic value: %q", logged)
	}
	if !strings.Contains(logged.String(), "middleware.panicsWithSecret") {
		t.Errorf("log lacks the stack: %q", logged)
	}
}

func TestAbortHandlerPanicPassesThrough(t *testing.T) {
	logged := captureLog(t)
	engine := newStack(t)
	engine.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", recovered)
		}
		if logged.Len() != 0 {
			t.Errorf("logged %q", logged)
		}
	}()
	serve(engine, http.MethodGet, "/abort", false)
	t.Error("the panic did not reach the server")
}

func writesThenPanics(ctx *gin.Context) {
	ctx.Writer.WriteString("partial")
	ctx.Writer.Flush()
	panicsWithSecret(ctx)
}

func TestPanicAfterWriteIsLoggedAndAborted(t *testing.T) {
	logged := captureLog(t)
	engine := newStack(t)
	engine.GET("/partial", writesThenPanics)

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", recovered)
		}
		if !strings.Contains(logged.String(), "middleware.panicsWithSecret") {
			t.Errorf("a late panic was not logged with its stack: %q", logged)
		}
	}()
	serve(engine, http.MethodGet, "/partial", false)
	t.Error("the panic did not reach the server")
}

func TestPanicAfterWriteLooksFailedToTheClient(t *testing.T) {
	captureLog(t)
	engine := newStack(t)
	engine.GET("/partial", writesThenPanics)
	server := httptest.NewServer(engine)
	defer server.Close()

	response, err := http.Get(server.URL + "/partial")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)

	if string(body) != "partial" {
		t.Errorf("body %q, want only what was written", body)
	}
	if err == nil {
		t.Error("a cut-off response looked complete")
	}
}

func TestPanicStopsLaterHandlers(t *testing.T) {
	captureLog(t)
	engine := newStack(t)
	reached := false
	engine.GET("/chain", panicsWithSecret, func(*gin.Context) { reached = true })

	assertDetail(t, serve(engine, http.MethodGet, "/chain", false), http.StatusInternalServerError, "internal error")
	if reached {
		t.Error("a handler after the panic ran")
	}
}

func TestFlushWorksBehindAllMiddleware(t *testing.T) {
	engine := newStack(t)
	release := make(chan struct{})
	engine.GET("/v1/stream", func(ctx *gin.Context) {
		ctx.Writer.WriteString("one\n")
		ctx.Writer.Flush()
		<-release

		controller := http.NewResponseController(ctx.Writer)
		if err := controller.SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			t.Errorf("SetWriteDeadline: %v", err)
		}
		ctx.Writer.WriteString("two\n")
		if err := controller.Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		<-release
	})
	server := httptest.NewServer(engine)
	defer server.Close()
	defer close(release)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", tokenValue)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	// The handler stays blocked until each read returns, so a line arrives only if its flush worked.
	lines := bufio.NewReader(response.Body)
	for _, want := range []string{"one\n", "two\n"} {
		line, err := lines.ReadString('\n')
		if err != nil || line != want {
			t.Fatalf("read %q, %v, want %q", line, err, want)
		}
		release <- struct{}{}
	}
}

func TestPostWithoutLengthIs411(t *testing.T) {
	engine := newStack(t)
	reached := false
	engine.POST("/upload", func(*gin.Context) { reached = true })

	assertDetail(t, serve(engine, http.MethodPost, "/upload", false), http.StatusLengthRequired, "Content-Length required")
	if reached {
		t.Error("handler ran")
	}
}

func TestOnlyPostNeedsALength(t *testing.T) {
	engine := newStack(t)
	engine.PUT("/thing", network.SendNoContent)

	if recorder := serve(engine, http.MethodPut, "/thing", false); recorder.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204", recorder.Code)
	}
}

func TestDeclaredLengthOverLimitIs413(t *testing.T) {
	engine := newStack(t)
	reached := false
	engine.POST("/upload", func(*gin.Context) { reached = true })
	server := httptest.NewServer(engine)
	defer server.Close()

	response, err := http.Post(server.URL+"/upload", "text/plain", strings.NewReader(strings.Repeat("x", testLimit+1)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusRequestEntityTooLarge || string(body) != `{"detail":"request body too large"}` {
		t.Errorf("got %d %s", response.StatusCode, body)
	}
	if reached {
		t.Error("handler ran")
	}
}

func TestBodyOfExactlyTheLimitPasses(t *testing.T) {
	engine := newStack(t)
	engine.POST("/upload", network.SendNoContent)
	request := httptest.NewRequest(http.MethodPost, "/upload", nil)
	request.Header.Set("Content-Length", fmt.Sprint(testLimit))
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204 at the limit", recorder.Code)
	}
}

func TestMalformedLengthIs413(t *testing.T) {
	engine := newStack(t)
	engine.POST("/upload", network.SendNoContent)
	request := httptest.NewRequest(http.MethodPost, "/upload", nil)
	request.Header.Set("Content-Length", "many")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	assertDetail(t, recorder, http.StatusRequestEntityTooLarge, "request body too large")
}

func TestLimitDependsOnPath(t *testing.T) {
	engine := newStack(t)
	engine.POST(largePath, network.SendNoContent)
	request := httptest.NewRequest(http.MethodPost, largePath, nil)
	request.Header.Set("Authorization", tokenValue)
	request.Header.Set("Content-Length", fmt.Sprint(testLimit+1))
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204 under the larger limit", recorder.Code)
	}
}

func TestBodylessMethodsSkipTheLimit(t *testing.T) {
	engine := newStack(t)
	handler := func(ctx *gin.Context) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil || len(body) != testLimit+1 {
			t.Errorf("read %d bytes, %v", len(body), err)
		}
		network.SendNoContent(ctx)
	}
	engine.GET("/read", handler)
	engine.HEAD("/read", handler)
	engine.OPTIONS("/read", handler)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		request := httptest.NewRequest(method, "/read", strings.NewReader(strings.Repeat("x", testLimit+1)))
		request.Header.Set("Content-Length", fmt.Sprint(testLimit+1))
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", method, recorder.Code)
		}
	}
}

// answerRead reports reading the body the way the upload handlers do: the limit is theirs to turn into a 413.
func answerRead(ctx *gin.Context, read func() error) {
	var tooLarge *http.MaxBytesError
	err := read()
	if errors.As(err, &tooLarge) {
		network.SendError(ctx, http.StatusRequestEntityTooLarge, "over the limit")
		return
	}
	if err != nil {
		network.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	network.SendNoContent(ctx)
}

// chunked hides the body's length from the client, which then sends Transfer-Encoding: chunked.
func chunked(body []byte) io.Reader {
	return struct{ io.Reader }{bytes.NewReader(body)}
}

func TestChunkedBodyIsCappedWhileRead(t *testing.T) {
	engine := newStack(t)
	engine.POST("/upload", func(ctx *gin.Context) {
		answerRead(ctx, func() error {
			_, err := io.ReadAll(ctx.Request.Body)
			return err
		})
	})
	server := httptest.NewServer(engine)
	defer server.Close()

	cases := []struct {
		name   string
		size   int
		status int
	}{
		{"at the limit", testLimit, http.StatusNoContent},
		{"over the limit", testLimit + 1, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			response, err := http.Post(server.URL+"/upload", "text/plain", chunked(make([]byte, c.size)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != c.status {
				t.Errorf("status %d, want %d", response.StatusCode, c.status)
			}
		})
	}
}

func TestMultipartReadsThroughRequest(t *testing.T) {
	engine := newStack(t)
	var received []byte
	engine.POST("/upload", func(ctx *gin.Context) {
		answerRead(ctx, func() error {
			reader, err := ctx.Request.MultipartReader()
			if err != nil {
				return err
			}
			part, err := reader.NextPart()
			if err != nil {
				return err
			}
			received, err = io.ReadAll(part)
			return err
		})
	})
	server := httptest.NewServer(engine)
	defer server.Close()

	form := func(content string) (*bytes.Buffer, string) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "a.txt")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(part, content)
		writer.Close()
		return body, writer.FormDataContentType()
	}

	body, contentType := form("hello")
	response, err := http.Post(server.URL+"/upload", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || string(received) != "hello" {
		t.Errorf("got %d, read %q", response.StatusCode, received)
	}

	body, contentType = form(strings.Repeat("x", testLimit))
	response, err = http.Post(server.URL+"/upload", contentType, chunked(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413 when a chunked form overflows", response.StatusCode)
	}
}

func TestGuardCoversKnownAndUnknownPaths(t *testing.T) {
	engine := newStack(t)
	engine.GET("/v1/brands", network.SendNoContent)

	for _, path := range []string{"/v1/brands", "/v1/unknown"} {
		assertDetail(t, serve(engine, http.MethodGet, path, false), http.StatusUnauthorized, "missing token")
	}

	if recorder := serve(engine, http.MethodGet, "/v1/brands", true); recorder.Code != http.StatusNoContent {
		t.Errorf("with a token: status %d, want 204", recorder.Code)
	}
	assertDetail(t, serve(engine, http.MethodGet, "/v1/unknown", true), http.StatusNotFound, "Not Found")
}

func TestGuardLeavesOtherPathsAlone(t *testing.T) {
	engine := newStack(t)
	engine.GET("/health", network.SendNoContent)

	if recorder := serve(engine, http.MethodGet, "/health", false); recorder.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204", recorder.Code)
	}
	assertDetail(t, serve(engine, http.MethodGet, "/v1", false), http.StatusNotFound, "Not Found")
	assertDetail(t, serve(engine, http.MethodGet, "/v1x/brands", false), http.StatusNotFound, "Not Found")
	assertDetail(t, serve(engine, http.MethodGet, "/x/v1/brands", false), http.StatusNotFound, "Not Found")
}

func TestRootMiddlewareRunsOnNoRoute(t *testing.T) {
	logged := captureLog(t)
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(
		NewErrorCatcher(),
		NewNotFound(),
		NewBodyLimit(limitFor),
		NewPrefixGuard("/panics/", panicsWithSecret),
	)
	engine := router.Engine()

	assertDetail(t, serve(engine, http.MethodGet, "/nowhere", false), http.StatusNotFound, "Not Found")
	assertDetail(t, serve(engine, http.MethodPost, "/nowhere", false), http.StatusLengthRequired, "Content-Length required")
	assertDetail(t, serve(engine, http.MethodGet, "/panics/x", false), http.StatusInternalServerError, "internal error")
	if !strings.Contains(logged.String(), secret) {
		t.Errorf("panic on an unmatched route not logged: %q", logged)
	}
}

func TestEncodedPathsCannotPassTheGuard(t *testing.T) {
	engine := newStack(t)
	reached := false
	handler := func(ctx *gin.Context) {
		reached = true
		network.SendNoContent(ctx)
	}
	engine.GET("/v1/brands", handler)
	engine.GET("/v1/brands/:id", handler)

	spellings := []string{
		"/v1%2Fbrands",
		"/v1%2fbrands",
		"/v1/brands%2Fx",
		"/%76%31/brands",
		"/v1/%62rands",
		"//v1/brands",
		"/v1//brands",
		"/./v1/brands",
		"/x/../v1/brands",
		"/V1/brands",
		"/v1/brands/",
		"/v1/brands%00",
		"/v1%5Cbrands",
	}
	for _, path := range spellings {
		reached = false
		recorder := serve(engine, http.MethodGet, path, false)
		if reached {
			t.Errorf("%s reached a handler without a token (status %d)", path, recorder.Code)
		}
	}

	assertDetail(t, serve(engine, http.MethodGet, "/v1%2Fbrands", false), http.StatusUnauthorized, "missing token")
	if serve(engine, http.MethodGet, "/v1%2Fbrands", true); !reached {
		t.Error("/v1%2Fbrands with a token did not reach its handler")
	}
}
