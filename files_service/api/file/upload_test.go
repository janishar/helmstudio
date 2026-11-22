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
	owner, take := "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13", "9b2f4c6e-1a3d-4e5f-8a7b-0c1d2e3f4a51"
	wav, mp3, wav2 := "u/"+owner+"/uploads/"+take+".wav", "u/"+owner+"/uploads/"+take+".mp3", "u/"+owner+"/uploads/x"+take[1:]+".wav"
	m4a := "u/" + owner + "/uploads/y" + take[1:] + ".m4a"

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
		// API 1.19: an audio upload's key takes its own type only
		"wav":          {link(wav, 5, "audio/wav", later), "audio/wav", 5, 204},
		"mp3 as wav":   {link(mp3, 5, "audio/wav", later), "audio/wav", 5, 415},
		"audio as png": {link(key, 5, "audio/wav", later), "audio/wav", 5, 415},
		"png as audio": {link(wav2, 5, "image/png", later), "image/png", 5, 415},
		// API 1.38: an m4a's key takes audio/mp4 only
		"m4a":        {link(m4a, 5, "audio/mp4", later), "audio/mp4", 5, 204},
		"m4a as wav": {link(m4a, 5, "audio/wav", later), "audio/wav", 5, 415},
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

// A LoRA key takes application/octet-stream up to 2 GiB; it takes no image type, and no other key takes octet-stream.
func TestLoraUpload(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	store := storage.NewMemory()
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadControllers(NewController(store, signer))
	put := func(key string, size int64, contentType, body string) int {
		exp, bytes := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10), strconv.FormatInt(size, 10)
		target := "/files/" + key + "?exp=" + exp + "&bytes=" + bytes + "&ct=" + url.QueryEscape(contentType) + "&sig=" +
			signer.putSignature(key, exp, bytes, contentType)
		request := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
		request.ContentLength = size
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		router.Engine().ServeHTTP(recorder, request)
		return recorder.Code
	}
	owner, id := "7d1e0000-0000-4000-8000-000000000001", "2c4e0000-0000-4000-8000-000000000002"
	key := "u/" + owner + "/loras/" + id + ".safetensors"
	if got := put(key, MaxLoraUpload+1, "application/octet-stream", ""); got != 403 {
		t.Errorf("over 2 GiB: %d", got)
	}
	if got := put(key, 5, "image/png", "hello"); got != 415 {
		t.Errorf("image type on a LoRA key: %d", got)
	}
	if got := put("u/"+owner+"/uploads/x.png", 5, "application/octet-stream", "hello"); got != 415 {
		t.Errorf("octet-stream on an image key: %d", got)
	}
	if got := put("u/"+owner+"/loras/x.bin", MaxUpload+1, "application/octet-stream", ""); got != 403 {
		t.Errorf("another loras key past 50 MB: %d", got)
	}
	if got := put(key, 5, "application/octet-stream", "hello"); got != 204 {
		t.Fatalf("LoRA PUT: %d", got)
	}
	// an imported video (Import): video/mp4 up to 2 GiB on u/<owner>/imports/<id>.mp4 only
	video := "u/" + owner + "/imports/" + id + ".mp4"
	if got := put(video, MaxImportVideo+1, "video/mp4", ""); got != 403 {
		t.Errorf("video over 2 GiB: %d", got)
	}
	if got := put("u/"+owner+"/uploads/"+id+".mp4", 5, "video/mp4", "hello"); got != 415 {
		t.Errorf("video/mp4 outside imports: %d", got)
	}
	if got := put(video, 5, "video/mp4", "hello"); got != 204 {
		t.Fatalf("import video PUT: %d", got)
	}
}
