package network

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBearerToken(t *testing.T) {
	for _, c := range []struct{ header, want string }{
		{"Bearer abc", "abc"},
		{"bEaReR   abc  ", "abc"},
		{"Bearer ", ""},
		{"Basic abc", ""},
		{"", ""},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(AuthorizationHeader, c.header)
		serve(t, request, func(ctx *gin.Context) {
			if got := BearerToken(ctx); got != c.want {
				t.Errorf("%q: got %q, want %q", c.header, got, c.want)
			}
		})
	}
}
