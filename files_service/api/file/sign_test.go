package file

import (
	"strconv"
	"testing"
	"time"
)

// vector is HMAC-SHA256("0123456789abcdef", "u/a/archives/z.zip\n2000000000"): catalog_service's common/sign_test.go
// checks the same value, so its links verify here.
const vector = "0c7ad09bc5bf227101a830b6a5f18c6ee3a10c01d759357bf953bf686ce9caae"

// Verify accepts the shared format and refuses a changed key, an expired link and a missing signature.
func TestVerify(t *testing.T) {
	signer := NewSigner("0123456789abcdef")
	if !signer.Verify("u/a/archives/z.zip", "2000000000", vector) {
		t.Fatal("the shared vector was refused")
	}
	expired := time.Now().Add(-time.Minute).Unix()
	if signer.Verify("u/a/archives/y.zip", "2000000000", vector) || signer.Verify("u/a/archives/z.zip", "x", vector) ||
		signer.Verify("u/a/archives/z.zip", "2000000000", "") ||
		signer.Verify("u/a/x", strconv.FormatInt(expired, 10), signer.signature("u/a/x", expired)) {
		t.Fatal("bad link accepted")
	}
}
