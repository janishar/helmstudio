package file

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
)

// A signed link serves its object on GET and HEAD with the safety headers; a key the signature doesn't cover is a 403.
func TestServe(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	expiry := time.Now().Add(time.Hour).Unix()
	link := func(key string) string { // what studio_service's Signer.Sign makes, without the base URL
		return "/files/" + (&url.URL{Path: key}).EscapedPath() + "?exp=" + strconv.FormatInt(expiry, 10) + "&sig=" + signer.signature(key, expiry)
	}
	store := storage.NewMemory()
	key := "u/a/front view.txt"
	if err := store.Put(context.Background(), key, strings.NewReader("hello"), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadControllers(NewController(store, signer))
	serve := func(method, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.Engine().ServeHTTP(w, httptest.NewRequest(method, target, nil))
		return w
	}

	w := serve(http.MethodGet, link(key))
	if w.Code != 200 || w.Body.String() != "hello" || w.Header().Get("Content-Length") != "5" ||
		w.Header().Get("Content-Type") != "text/plain" || w.Header().Get("Content-Security-Policy") != "sandbox" ||
		w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("GET: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := serve(http.MethodHead, link(key)); w.Code != 200 {
		t.Fatalf("HEAD: %d", w.Code)
	}
	if w := serve(http.MethodGet, strings.Replace(link(key), "front", "back", 1)); w.Code != 403 || w.Header().Get("Cache-Control") != "" {
		t.Fatalf("tampered: %d %v", w.Code, w.Header()) // a refusal is never cached
	}
	if w := serve(http.MethodPost, link(key)); w.Code != 404 {
		t.Fatalf("POST: %d", w.Code)
	}
	// a starter look's thumbnail (packs/<family>/looks/<id>-<16 hex>.<ext>) is served; any other key outside u/ never is, even signed
	for k, want := range map[string]int{"packs/echo/looks/grain-0123456789abcdef.png": 200, "x/a.png": 403,
		"packs/echo/other.bin": 403, "packs/echo/looks/grain.bin": 403} {
		if err := store.Put(context.Background(), k, strings.NewReader("png"), 3, "image/png"); err != nil {
			t.Fatal(err)
		}
		if w := serve(http.MethodGet, link(k)); w.Code != want {
			t.Fatalf("%s: %d, want %d", k, w.Code, want)
		}
	}
}
