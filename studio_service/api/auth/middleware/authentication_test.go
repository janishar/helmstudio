package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/studio_service/api/auth"
	"github.com/helmstudio/studio_service/common"
)

// fakeNats answers every request with reply or err, without NATS: it embeds micro.NatsClient for what it doesn't use.
type fakeNats struct {
	micro.NatsClient
	reply string
	err   error
}

// Request answers every token check with the canned reply or error.
func (f *fakeNats) Request(context.Context, string, []byte) ([]byte, error) {
	return []byte(f.reply), f.err
}

func TestMiddleware(t *testing.T) {
	cases := []struct {
		reply  string
		err    error
		header string
		want   int
		body   string
	}{
		{`{"error":"invalid or expired token"}`, nil, "Bearer x", 401, `{"detail":"invalid or missing bearer token"}`},
		{"", nil, "", 401, `{"detail":"invalid or missing bearer token"}`},
		{"", errors.New("no responders"), "Bearer x", 503, `{"detail":"auth unavailable"}`},
		{"not json", nil, "Bearer x", 503, `{"detail":"auth unavailable"}`},
		{`{"user_id":"u1","expires_at":"` + time.Now().Add(time.Minute).Format(time.RFC3339) + `"}`, nil, "Bearer x", 200, "u1 kong"},
	}
	for _, c := range cases {
		service := auth.NewService(&fakeNats{reply: c.reply, err: c.err})
		router, err := network.NewRouter(nil)
		if err != nil {
			t.Fatal(err)
		}
		router.Engine().GET("/v1/studio/sessions", NewAuthenticationProvider(service).Middleware(), func(ctx *gin.Context) {
			caller := common.MustCaller(ctx)
			ctx.String(http.StatusOK, caller.ID+" "+caller.ClientID)
		})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/studio/sessions", nil)
		r.Header.Set("X-Client-Id", "kong")
		if c.header != "" {
			r.Header.Set("Authorization", c.header)
		}
		router.Engine().ServeHTTP(w, r)
		if w.Code != c.want || w.Body.String() != c.body {
			t.Errorf("%+v: %d %s", c, w.Code, w.Body)
		}
	}
}
