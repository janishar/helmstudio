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

// A video streams by ranges: 206 with Content-Range and just those bytes (HEAD the headers only), 416 past the end,
// the whole file for no, several or malformed ranges; the stored Content-Type always. A link for one key never serves
// another (the poster's link is not the MP4's).
func TestServeRange(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	expiry := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	link := func(key string) string {
		exp, _ := strconv.ParseInt(expiry, 10, 64)
		return "/files/" + key + "?exp=" + expiry + "&sig=" + signer.signature(key, exp)
	}
	store := storage.NewMemory()
	video, poster := "u/a/runs/r/1/0.mp4", "u/a/runs/r/1/0.poster.png"
	_ = store.Put(context.Background(), video, strings.NewReader("0123456789"), 10, "video/mp4")
	_ = store.Put(context.Background(), poster, strings.NewReader("png"), 3, "image/png")
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadControllers(NewController(store, signer))
	serve := func(method, target, ranges string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(method, target, nil)
		if ranges != "" {
			request.Header.Set("Range", ranges)
		}
		router.Engine().ServeHTTP(w, request)
		return w
	}
	for ranges, want := range map[string]string{
		"bytes=0-3":     "206 0123 bytes 0-3/10",
		"bytes=4-":      "206 456789 bytes 4-9/10",
		"bytes=-3":      "206 789 bytes 7-9/10",
		"bytes=-30":     "206 0123456789 bytes 0-9/10",
		"bytes=8-100":   "206 89 bytes 8-9/10",
		"bytes=9-9":     "206 9 bytes 9-9/10",
		"bytes=10-":     "416  bytes */10",
		"bytes=-0":      "416  bytes */10",
		"":              "200 0123456789 ",
		"bytes=0-1,4-5": "200 0123456789 ",
		"bytes=5-2":     "200 0123456789 ",
		"items=0-3":     "200 0123456789 ",
		"bytes=x-3":     "200 0123456789 ",
	} {
		w := serve(http.MethodGet, link(video), ranges)
		body := w.Body.String()
		if w.Code == 416 {
			body = ""
			if !strings.Contains(w.Body.String(), `"detail"`) {
				t.Errorf("%q: 416 body %q", ranges, w.Body)
			}
		}
		got := strconv.Itoa(w.Code) + " " + body + " " + w.Header().Get("Content-Range")
		if got != want || w.Header().Get("Accept-Ranges") != "bytes" {
			t.Errorf("%q: %q, want %q (%v)", ranges, got, want, w.Header())
		}
		if w.Code != 416 && (w.Header().Get("Content-Type") != "video/mp4" || w.Header().Get("Content-Length") != strconv.Itoa(len(body))) {
			t.Errorf("%q: headers %v", ranges, w.Header())
		}
	}
	if w := serve(http.MethodHead, link(video), "bytes=2-5"); w.Code != 206 || w.Body.Len() != 0 ||
		w.Header().Get("Content-Range") != "bytes 2-5/10" || w.Header().Get("Content-Length") != "4" {
		t.Fatalf("HEAD: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := serve(http.MethodGet, link(poster), ""); w.Code != 200 || w.Body.String() != "png" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("poster: %d %v", w.Code, w.Header())
	}
	swapped := strings.Replace(link(poster), poster, video, 1) // the poster's signature on the MP4's key
	if w := serve(http.MethodGet, swapped, "bytes=0-3"); w.Code != 403 {
		t.Fatalf("one key's link served another: %d", w.Code)
	}
}
