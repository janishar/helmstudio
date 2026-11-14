package common

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// vector is HMAC-SHA256("0123456789abcdef", "u/a/archives/z.zip\n2000000000"): files_service's sign_test.go checks the
// same value, so links made here verify there.
const vector = "0c7ad09bc5bf227101a830b6a5f18c6ee3a10c01d759357bf953bf686ce9caae"

// Sign makes /files/<escaped key>?exp=&sig= links whose signature is the shared format.
func TestSign(t *testing.T) {
	signer := NewSigner("0123456789abcdef", "http://localhost:8300/", time.Hour)
	if got := signer.signature("u/a/archives/z.zip", 2000000000); got != vector {
		t.Fatalf("signature = %s, want %s", got, vector)
	}
	key := "u/a/runs/r/front view.png"
	u, err := url.Parse(signer.Sign(key))
	if err != nil || !strings.HasPrefix(u.String(), "http://localhost:8300/files/u/a/runs/r/front%20view.png?exp=") {
		t.Fatal(u, err)
	}
	expiry, err := strconv.ParseInt(u.Query().Get("exp"), 10, 64)
	if err != nil || u.Query().Get("sig") != signer.signature(key, expiry) {
		t.Fatal(u)
	}
	if left := time.Until(time.Unix(expiry, 0)); expiry%86400 != 0 || left < time.Hour || left > 25*time.Hour {
		t.Fatalf("expiry %d: on a UTC day, valid for ttl to ttl + 24 h (%v left)", expiry, left)
	}
	if again := signer.Sign(key); again != u.String() && time.Now().Unix()%86400 != 0 {
		t.Fatalf("the same key within the day gets the same link: %s vs %s", again, u)
	}
	if signer.SignPtr(nil) != nil || signer.SignPtr(new(string)) != nil {
		t.Fatal("SignPtr of no key")
	}
}
