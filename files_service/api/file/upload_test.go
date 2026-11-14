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

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
)

// putVector is HMAC-SHA256("0123456789abcdef", "PUT\nu/a/uploads/t.png\n2000000000\n5\nimage/png"): studio_service's
// common/sign_test.go checks the same value, so its upload links verify here.
const putVector = "7e14c889185306277d7776b382be108a9458d8bc542f4640ebebe48a833223c1"

// VerifyPut accepts the shared format; a GET signature of the same key never passes as a PUT.
func TestVerifyPut(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	if !signer.VerifyPut("u/a/uploads/t.png", "2000000000", "5", "image/png", putVector) {
		t.Fatal("the shared vector was refused")
	}
	if signer.VerifyPut("u/a/uploads/t.png", "2000000000", "6", "image/png", putVector) ||
		signer.VerifyPut("u/a/uploads/t.png", "2000000000", "5", "image/webp", putVector) ||
		signer.VerifyPut("u/a/uploads/t.png", "2000000000", "5", "image/png", signer.signature("u/a/uploads/t.png", 2000000000)) {
		t.Fatal("a changed size, type or a GET signature passed")
	}
}

// PUT stores exactly the signed bytes and type once; every other request is refused before anything is stored.
func TestUpload(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	store := storage.NewMemory()
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadControllers(NewController(store, signer))
	link := func(key string, size int, contentType string, expiry int64) string {
		exp, bytes := strconv.FormatInt(expiry, 10), strconv.Itoa(size)
		return "/files/" + (&url.URL{Path: key}).EscapedPath() + "?exp=" + exp + "&bytes=" + bytes + "&ct=" +
			url.QueryEscape(contentType) + "&sig=" + signer.putSignature(key, exp, bytes, contentType)
	}
	put := func(target, body, contentType string, contentLength int64) int {
		request := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
		request.ContentLength = contentLength
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		router.Engine().ServeHTTP(recorder, request)
		return recorder.Code
	}
	later := time.Now().Add(time.Hour).Unix()
	key := "u/a/uploads/t.png"
	good := link(key, 5, "image/png", later)

	for name, test := range map[string]struct {
		target, contentType string
		length              int64
		want                int
	}{
		"expired":          {link(key, 5, "image/png", time.Now().Add(-time.Minute).Unix()), "image/png", 5, 403},
		"tampered size":    {strings.Replace(good, "bytes=5", "bytes=6", 1), "image/png", 6, 403},
		"outside u/":       {link("x/a.png", 5, "image/png", later), "image/png", 5, 403},
		"climbs":           {link("u/a/../b/t.png", 5, "image/png", later), "image/png", 5, 403},
		"over the cap":     {link(key, MaxUpload+1, "image/png", later), "image/png", MaxUpload + 1, 403},
		"chunked":          {good, "image/png", -1, 411},
		"other length":     {good, "image/png", 4, 411},
		"other type":       {good, "image/jpeg", 5, 415},
		"type not allowed": {link(key, 5, "text/html", later), "text/html", 5, 415},
	} {
		if got := put(test.target, "hello", test.contentType, test.length); got != test.want {
			t.Errorf("%s: %d, want %d", name, got, test.want)
		}
	}
	if _, err := store.Stat(context.Background(), key); err == nil {
		t.Fatal("a refused PUT stored something")
	}
	if got := put(good, "hel", "image/png", 5); got != 400 {
		t.Fatalf("short body: %d", got)
	}
	if _, err := store.Stat(context.Background(), key); err == nil {
		t.Fatal("a short body was kept")
	}
	if got := put(good, "hello", "image/png", 5); got != 204 {
		t.Fatalf("PUT: %d", got)
	}
	if info, err := store.Stat(context.Background(), key); err != nil || info.Size != 5 || info.ContentType != "image/png" {
		t.Fatal(info, err)
	}
	if got := put(good, "HELLO", "image/png", 5); got != 409 {
		t.Fatalf("second PUT: %d", got)
	}
}
