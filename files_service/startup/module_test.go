package startup

import (
	"net/http/httptest"
	"testing"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"

	"github.com/helmstudio/files_service/api/file"
)

// The routes mount on one router; /health answers, an unsigned /files link is 403 and anything else 404.
func TestHandlerRoutes(t *testing.T) {
	module := &Module{store: storage.NewMemory(), signer: file.NewSigner("0123456789abcdef")}
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	router.LoadControllers(module.Controllers()...)
	for path, want := range map[string]int{"/health": 200, "/files/u/x/y.png": 403, "/v1/studio/sessions": 404, "/nope": 404} {
		recorder := httptest.NewRecorder()
		router.Engine().ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != want {
			t.Errorf("GET %s: %d, want %d", path, recorder.Code, want)
		}
	}
}
