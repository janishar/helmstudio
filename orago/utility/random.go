// Package utility holds small helpers for random values, hashes and ids.
package utility

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
)

// uuidPattern matches the 8-4-4-4-12 hex form in either case.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// RandomToken returns size random bytes, base64url-encoded.
func RandomToken(size int) string {
	random := make([]byte, size)
	_, _ = rand.Read(random) // never fails: crypto/rand panics on failure since go1.24
	return base64.RawURLEncoding.EncodeToString(random)
}

// RandomHex returns size random bytes, hex-encoded.
func RandomHex(size int) string {
	random := make([]byte, size)
	_, _ = rand.Read(random)
	return hex.EncodeToString(random)
}

// SHA256 returns the SHA-256 digest of text.
func SHA256(text string) []byte {
	sum := sha256.Sum256([]byte(text))
	return sum[:]
}

// NewID returns a random (v4) UUID.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// IsUUID reports whether value has the 8-4-4-4-12 hex form. It doesn't check the version bits.
func IsUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

// Traceparent returns a new sampled W3C trace context, for messages that have no
// incoming trace to continue.
func Traceparent() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("00-%x-%x-01", b[:16], b[16:])
}
