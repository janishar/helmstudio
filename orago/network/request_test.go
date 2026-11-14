package network

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type plainBody struct {
	Name string `json:"name"`
}

type pointerValidated struct {
	Name string `json:"name"`
}

func (b *pointerValidated) Validate() string {
	if b.Name == "" {
		return "name is required"
	}
	return ""
}

type valueValidated struct {
	Name string `json:"name"`
}

func (b valueValidated) Validate() string {
	if b.Name == "" {
		return "name is required"
	}
	return ""
}

// parse runs fn inside a handler, because the body cap and path parameters need a live gin.Context.
func parse(t *testing.T, method, target, body string, fn func(*gin.Context)) {
	t.Helper()
	serve(t, httptest.NewRequest(method, target, strings.NewReader(body)), fn)
}

func assertAPIError(t *testing.T, err error, code int, message string) {
	t.Helper()
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	if apiError.Code != code || apiError.Message != message {
		t.Errorf("got %d %q, want %d %q", apiError.Code, apiError.Message, code, message)
	}
}

// jsonOfSize is a valid {"name":"aaa"} body of exactly size bytes.
func jsonOfSize(size int) string {
	return `{"name":"` + strings.Repeat("a", size-len(`{"name":""}`)) + `"}`
}

func TestReqBody(t *testing.T) {
	parse(t, http.MethodPost, "/", `{"name":"ada"}`, func(ctx *gin.Context) {
		body, err := ReqBody[plainBody](ctx)
		if err != nil {
			t.Fatal(err)
		}
		if body.Name != "ada" {
			t.Errorf("Name = %q", body.Name)
		}
	})
}

func TestReqBodyInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"bad JSON":    `{"name":`,
		"wrong type":  `{"name":1}`,
		"empty":       "",
		"over 64 KiB": jsonOfSize(64<<10 + 1),
	} {
		t.Run(name, func(t *testing.T) {
			parse(t, http.MethodPost, "/", body, func(ctx *gin.Context) {
				parsed, err := ReqBody[plainBody](ctx)
				if parsed != nil {
					t.Error("body must be nil on failure")
				}
				assertAPIError(t, err, http.StatusUnprocessableEntity, "invalid JSON body")
			})
		})
	}
}

func TestReqBodyAtExactlyTheCap(t *testing.T) {
	parse(t, http.MethodPost, "/", jsonOfSize(64<<10), func(ctx *gin.Context) {
		if _, err := ReqBody[plainBody](ctx); err != nil {
			t.Errorf("a 64 KiB body must pass: %v", err)
		}
	})
}

func TestReqBodyCallsValidate(t *testing.T) {
	parse(t, http.MethodPost, "/", `{}`, func(ctx *gin.Context) {
		_, err := ReqBody[pointerValidated](ctx)
		assertAPIError(t, err, http.StatusUnprocessableEntity, "name is required")
	})
	parse(t, http.MethodPost, "/", `{}`, func(ctx *gin.Context) {
		_, err := ReqBody[valueValidated](ctx)
		assertAPIError(t, err, http.StatusUnprocessableEntity, "name is required")
	})
	parse(t, http.MethodPost, "/", `{"name":"ada"}`, func(ctx *gin.Context) {
		if _, err := ReqBody[pointerValidated](ctx); err != nil {
			t.Error(err)
		}
	})
}

func TestReqBodyOptionalEmptyGivesZeroValueUnvalidated(t *testing.T) {
	parse(t, http.MethodPost, "/", "", func(ctx *gin.Context) {
		body, err := ReqBodyOptional[pointerValidated](ctx)
		if err != nil {
			t.Fatal(err)
		}
		if body == nil || body.Name != "" {
			t.Errorf("body = %+v, want the zero value", body)
		}
	})
}

func TestReqBodyOptionalDecodesWithoutValidate(t *testing.T) {
	parse(t, http.MethodPost, "/", `{"name":""}`, func(ctx *gin.Context) {
		if _, err := ReqBodyOptional[pointerValidated](ctx); err != nil {
			t.Errorf("Validate must not run: %v", err)
		}
	})
	parse(t, http.MethodPost, "/", `{"name":"ada"}`, func(ctx *gin.Context) {
		body, err := ReqBodyOptional[plainBody](ctx)
		if err != nil || body.Name != "ada" {
			t.Errorf("body = %+v, err = %v", body, err)
		}
	})
}

func TestReqBodyOptionalInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"bad JSON":    `not json`,
		"truncated":   `{"name":`,
		"over 64 KiB": jsonOfSize(64<<10 + 1),
	} {
		t.Run(name, func(t *testing.T) {
			parse(t, http.MethodPost, "/", body, func(ctx *gin.Context) {
				_, err := ReqBodyOptional[plainBody](ctx)
				assertAPIError(t, err, http.StatusUnprocessableEntity, "invalid JSON body")
			})
		})
	}
}

func TestPathUUID(t *testing.T) {
	const id = "0194f0a2-7b3c-7d4e-8f90-123456789abc"
	reached := 0
	for _, target := range []string{"/things/" + id, "/things/not-a-uuid"} {
		router, err := NewRouter(nil)
		if err != nil {
			t.Fatal(err)
		}
		router.Engine().GET("/things/:id", func(ctx *gin.Context) {
			reached++
			got, err := PathUUID(ctx, "id")
			if target == "/things/"+id {
				if err != nil || got != id {
					t.Errorf("got %q, %v", got, err)
				}
				return
			}
			if got != "" {
				t.Errorf("got %q for a bad id", got)
			}
			assertAPIError(t, err, http.StatusNotFound, "not found")
		})
		router.Engine().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	if reached != 2 {
		t.Errorf("handler ran %d times, want 2", reached)
	}
}

func TestQueryInt(t *testing.T) {
	const message = "limit must be an integer from 1 to 100"
	for _, c := range []struct {
		target string
		want   int
		fails  bool
	}{
		{"/", 20, false},
		{"/?limit=", 20, false},
		{"/?limit=1", 1, false},
		{"/?limit=100", 100, false},
		{"/?limit=0", 0, true},
		{"/?limit=101", 0, true},
		{"/?limit=abc", 0, true},
	} {
		parse(t, http.MethodGet, c.target, "", func(ctx *gin.Context) {
			got, err := QueryInt(ctx, "limit", 20, 1, 100)
			if c.fails {
				assertAPIError(t, err, http.StatusUnprocessableEntity, message)
				return
			}
			if err != nil || got != c.want {
				t.Errorf("%s: got %d, %v, want %d", c.target, got, err, c.want)
			}
		})
	}
}

func TestQueryTime(t *testing.T) {
	parse(t, http.MethodGet, "/", "", func(ctx *gin.Context) {
		got, err := QueryTime(ctx, "before")
		if got != nil || err != nil {
			t.Errorf("absent: got %v, %v", got, err)
		}
	})
	parse(t, http.MethodGet, "/?before=2026-11-02T10:00:00.123456789Z", "", func(ctx *gin.Context) {
		got, err := QueryTime(ctx, "before")
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 11, 2, 10, 0, 0, 123456789, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	parse(t, http.MethodGet, "/?before=yesterday", "", func(ctx *gin.Context) {
		_, err := QueryTime(ctx, "before")
		assertAPIError(t, err, http.StatusUnprocessableEntity, "before must be an RFC 3339 date-time")
	})
}
