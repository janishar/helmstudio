package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Signer makes time-limited /files/{key}?exp=&sig= links to object keys. files_service serves them and checks the
// signature with the same secret (FILE_URL_SECRET, the file_url_secret compose secret).
type Signer struct {
	secret  []byte
	baseURL string        // public base URL without a trailing "/", e.g. http://localhost:8100
	ttl     time.Duration // how long a link stays valid
}

// NewSigner returns a signer for the secret, the public base URL and the link lifetime; a ttl <= 0 means 24 hours.
func NewSigner(secret, publicBaseURL string, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Signer{secret: []byte(secret), baseURL: strings.TrimRight(publicBaseURL, "/"), ttl: ttl}
}

// signature is the hex HMAC-SHA256 of key + "\n" + expiry: the format files_service verifies.
func (s *Signer) signature(key string, expiry int64) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(key + "\n" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Sign returns an absolute, time-limited link to an object key. Nothing checks ownership here: callers sign only keys
// read from rows RLS let them see. The expiry is rounded up to the next UTC day, so a key keeps the same link for up
// to a day (valid for ttl to ttl + 24 h): the browser caches by the whole URL, so files_service's long Cache-Control
// only pays off while the link stays the same.
func (s *Signer) Sign(key string) string {
	const day = 24 * 60 * 60
	expiry := (time.Now().Add(s.ttl).Unix()/day + 1) * day
	return s.baseURL + "/files/" + (&url.URL{Path: key}).EscapedPath() + "?exp=" + strconv.FormatInt(expiry, 10) + "&sig=" + s.signature(key, expiry)
}

// SignPtr signs a key that may be absent (nil/empty → nil).
func (s *Signer) SignPtr(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	link := s.Sign(*key)
	return &link
}
