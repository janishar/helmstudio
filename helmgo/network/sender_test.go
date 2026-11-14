package network

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// serve runs one request through a real router, so a test sees what a client sees.
func serve(t *testing.T, request *http.Request, handlers ...gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	router, err := NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.Engine().Handle(request.Method, request.URL.Path, handlers...)
	recorder := httptest.NewRecorder()
	router.Engine().ServeHTTP(recorder, request)
	return recorder
}

// captureLog returns the buffer that holds what the standard logger writes during the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

func assertResponse(t *testing.T, response *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if response.Code != status {
		t.Errorf("status %d, want %d", response.Code, status)
	}
	if got := response.Body.String(); got != body {
		t.Errorf("body %q, want %q", got, body)
	}
}

func assertContentType(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want exactly application/json", got)
	}
}

// failIfReached is the handler after the one under test: every send must abort the chain.
func failIfReached(t *testing.T) gin.HandlerFunc {
	return func(*gin.Context) { t.Error("the chain was not aborted") }
}

func TestSendJSON(t *testing.T) {
	response := serve(t, httptest.NewRequest(http.MethodGet, "/", nil), func(ctx *gin.Context) {
		SendJSON(ctx, http.StatusCreated, map[string]int{"count": 2})
	}, failIfReached(t))
	assertResponse(t, response, http.StatusCreated, `{"count":2}`)
	assertContentType(t, response)
}

func TestSendJSONMarshalFailureAnswers500(t *testing.T) {
	logged := captureLog(t)
	response := serve(t, httptest.NewRequest(http.MethodGet, "/", nil), func(ctx *gin.Context) {
		SendJSON(ctx, http.StatusOK, make(chan int))
	}, failIfReached(t))
	assertResponse(t, response, http.StatusInternalServerError, `{"detail":"internal error"}`)
	assertContentType(t, response)
	if !strings.Contains(logged.String(), "chan int") {
		t.Errorf("marshal failure not logged: %q", logged)
	}
}

func TestSendError(t *testing.T) {
	response := serve(t, httptest.NewRequest(http.MethodGet, "/", nil), func(ctx *gin.Context) {
		SendError(ctx, http.StatusNotFound, "not found")
	}, failIfReached(t))
	assertResponse(t, response, http.StatusNotFound, `{"detail":"not found"}`)
	assertContentType(t, response)
}

func TestSendNoContent(t *testing.T) {
	response := serve(t, httptest.NewRequest(http.MethodDelete, "/", nil), func(ctx *gin.Context) {
		SendNoContent(ctx)
		if !ctx.Writer.Written() {
			t.Error("204 not written yet: a later catcher could overwrite it")
		}
	}, failIfReached(t))
	assertResponse(t, response, http.StatusNoContent, "")
}

func TestSendMixedError(t *testing.T) {
	const secret = "pq: password wrong for 10.0.0.5"
	for _, c := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
		wantLogged string // "" means nothing may be logged
	}{
		{
			"client error answers its code",
			NewConflictError("email already registered", nil),
			http.StatusConflict, `{"detail":"email already registered"}`, "",
		},
		{
			"client error does not log its cause",
			NewBadRequestError("bad", errors.New(secret)),
			http.StatusBadRequest, `{"detail":"bad"}`, "",
		},
		{
			"wrapped APIError is found",
			fmt.Errorf("get: %w", NewNotFoundError("not found", nil)),
			http.StatusNotFound, `{"detail":"not found"}`, "",
		},
		{
			"server error logs its cause and hides it",
			NewServiceUnavailableError("auth unavailable", errors.New(secret)),
			http.StatusServiceUnavailable, `{"detail":"auth unavailable"}`, secret,
		},
		{
			"server error with no cause logs nothing",
			NewServiceUnavailableError("auth unavailable", nil),
			http.StatusServiceUnavailable, `{"detail":"auth unavailable"}`, "",
		},
		{
			"plain error is logged and hidden",
			errors.New(secret),
			http.StatusInternalServerError, `{"detail":"internal error"}`, secret,
		},
		{
			"nil is logged and answers 500",
			nil,
			http.StatusInternalServerError, `{"detail":"internal error"}`, "<nil>",
		},
		{
			"nil APIError in an error is treated as nil",
			(*APIError)(nil),
			http.StatusInternalServerError, `{"detail":"internal error"}`, "<nil>",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logged := captureLog(t)
			response := serve(t, httptest.NewRequest(http.MethodGet, "/", nil), func(ctx *gin.Context) {
				SendMixedError(ctx, c.err)
			}, failIfReached(t))
			assertResponse(t, response, c.wantStatus, c.wantBody)
			assertContentType(t, response)
			if c.wantLogged == "" && logged.Len() != 0 {
				t.Errorf("unexpected log: %q", logged)
			}
			if !strings.Contains(logged.String(), c.wantLogged) {
				t.Errorf("log %q does not hold %q", logged, c.wantLogged)
			}
		})
	}
}

func TestSendJSONReplacesAHandlersContentType(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := serve(t, request, func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/event-stream")
		SendError(ctx, http.StatusServiceUnavailable, "event stream unavailable")
	})

	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
}
