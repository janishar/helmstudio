// Package utils: password hashing, RSA keys, attempt limits.
package utils

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id, OWASP minimum profile (m=19 MiB, t=2, p=1), PHC string encoding.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

// b64 is the base64 form the PHC string uses.
var b64 = base64.RawStdEncoding

// HashPassword hashes password with a random salt and returns the PHC string to store.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC string in constant time, using the parameters stored in it, so hashes keep
// working after the constants change. A string that doesn't parse is false.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is the hash DummyVerify checks against.
var dummyHash, _ = HashPassword("dummy-password-for-timing")

// DummyVerify burns the same time as a real check (unknown email must not be faster).
func DummyVerify(password string) { VerifyPassword(password, dummyHash) }

// denied holds common passwords refused at sign-up / change (length >= 10 already enforced), lower-cased.
var denied = map[string]bool{}

// init fills denied.
func init() {
	for _, p := range strings.Fields(`1234567890 12345678910 0123456789 1q2w3e4r5t 1qaz2wsx3edc qwertyuiop
		password12 password123 password1234 passw0rd123 iloveyou123 qwerty1234 qwerty12345 abc1234567
		abcdefghij aaaaaaaaaa 1111111111 0000000000 letmein123 welcome123 admin12345 administrator
		football123 baseball123 sunshine123 princess123 trustno1234 changeme123 qwertyuiop123`) {
		denied[p] = true
	}
}

// WeakPassword returns the 422 detail for a password that breaks the rules, or "" when it is acceptable.
func WeakPassword(password string) string {
	switch {
	case len(password) < 10:
		return "password must be at least 10 characters"
	case len(password) > 256:
		return "password must be at most 256 characters"
	case denied[strings.ToLower(password)]:
		return "password is too common"
	}
	return ""
}
