// Package file: GET and HEAD /files/{key}?exp=&sig=, HMAC-signed links (made by catalog_service) served from the
// object store.
package file

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Signer checks the links catalog_service signs with the same secret (FILE_URL_SECRET, the file_url_secret compose
// secret).
type Signer struct {
	secret []byte
}

// NewSigner returns a signer for the shared secret.
func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret)}
}

// signature is the hex HMAC-SHA256 of key + "\n" + expiry: the format catalog_service signs.
func (s *Signer) signature(key string, expiry int64) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(key + "\n" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a link's signature and expiry.
func (s *Signer) Verify(key, expiryParam, signatureParam string) bool {
	expiry, err := strconv.ParseInt(expiryParam, 10, 64)
	if err != nil || signatureParam == "" || expiry < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(signatureParam), []byte(s.signature(key, expiry)))
}
