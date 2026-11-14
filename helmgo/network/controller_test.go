package network

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

type stubAuthentication struct{}

func (stubAuthentication) Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Set("authenticated", true) }
}

type stubAuthorization struct {
	roles *[]string
}

func (s stubAuthorization) Middleware(roles ...string) gin.HandlerFunc {
	*s.roles = roles
	return func(*gin.Context) {}
}

func TestControllerDelegatesToProviders(t *testing.T) {
	var roles []string
	controller := NewController("/things", stubAuthentication{}, stubAuthorization{&roles})
	if controller.Path() != "/things" {
		t.Errorf("Path() = %q", controller.Path())
	}

	controller.Authorization("admin", "user")
	if !slices.Equal(roles, []string{"admin", "user"}) {
		t.Errorf("roles = %v", roles)
	}

	authenticated := false
	serve(t, httptest.NewRequest(http.MethodGet, "/", nil), controller.Authentication(), func(ctx *gin.Context) {
		authenticated = ctx.GetBool("authenticated")
	})
	if !authenticated {
		t.Error("Authentication() did not return the provider's middleware")
	}
}
