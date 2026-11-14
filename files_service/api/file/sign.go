// Package file: GET and HEAD /files/{key}?exp=&sig= and PUT /files/{key}?exp=&bytes=&ct=&sig=, HMAC-signed links (made by
// studio_service) served from and written to the object store.
package file

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Signer checks the links studio_service signs with the same secret (FILE_URL_SECRET, the file_url_secret compose
// secret).
type Signer struct {
	secret []byte
}

// NewSigner returns a signer for the shared secret.
func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret)}
}

// signature is the hex HMAC-SHA256 of key + "\n" + expiry: the format studio_service signs.
func (s *Signer) signature(key string, expiry int64) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(key + "\n" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// putSignature is the hex HMAC-SHA256 of "PUT\n" + key + "\n" + exp + "\n" + bytes + "\n" + ct: an upload link, which
// a GET signature (key + "\n" + exp) can never equal.
func (s *Signer) putSignature(key, expiry, size, contentType string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("PUT\n" + key + "\n" + expiry + "\n" + size + "\n" + contentType))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyPut checks an upload link's signature and expiry; the query values are checked as they were signed.
func (s *Signer) VerifyPut(key, expiryParam, sizeParam, contentType, signatureParam string) bool {
	expiry, err := strconv.ParseInt(expiryParam, 10, 64)
	if err != nil || signatureParam == "" || expiry < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(signatureParam), []byte(s.putSignature(key, expiryParam, sizeParam, contentType)))
}

// Verify checks a link's signature and expiry.
func (s *Signer) Verify(key, expiryParam, signatureParam string) bool {
	expiry, err := strconv.ParseInt(expiryParam, 10, 64)
	if err != nil || signatureParam == "" || expiry < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(signatureParam), []byte(s.signature(key, expiry)))
}
