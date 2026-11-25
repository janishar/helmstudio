package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	baseURL string        // public base URL without a trailing "/", e.g. http://localhost:8300
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

// SignPut returns a link to PUT exactly size bytes of contentType to key until expiry, in the format files_service
// verifies: sig = HMAC("PUT\n" + key + "\n" + exp + "\n" + bytes + "\n" + ct), so a GET signature never passes.
func (s *Signer) SignPut(key string, size int64, contentType string, expiry time.Time) string {
	exp, bytes := strconv.FormatInt(expiry.Unix(), 10), strconv.FormatInt(size, 10)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("PUT\n" + key + "\n" + exp + "\n" + bytes + "\n" + contentType))
	return s.baseURL + "/files/" + (&url.URL{Path: key}).EscapedPath() + "?exp=" + exp + "&bytes=" + bytes +
		"&ct=" + url.QueryEscape(contentType) + "&sig=" + hex.EncodeToString(mac.Sum(nil))
}

// SignPtr signs a key that may be absent (nil/empty → nil).
func (s *Signer) SignPtr(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	link := s.Sign(*key)
	return &link
}

// SignUntil signs a GET link to key valid until expiry (API 1.70: a plugin's 10-minute read link).
func (s *Signer) SignUntil(key string, expiry time.Time) string {
	exp := expiry.Unix()
	return s.baseURL + "/files/" + (&url.URL{Path: key}).EscapedPath() + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.signature(key, exp)
}

// BaseURL is the public base URL links are signed under (a plugin call's X-Helm-Api, API 1.70).
func (s *Signer) BaseURL() string { return s.baseURL }

// passKey is the key of plugin call passes (API 1.70): derived from the file secret, so a /files signature never
// verifies as a pass and the secret itself never signs one.
func (s *Signer) passKey() []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("helmstudio plugin pass v1"))
	return mac.Sum(nil)
}

// Pass signs body: base64url(body) "." base64url(HMAC-SHA256(passKey, base64url(body))).
func (s *Signer) Pass(body []byte) string {
	head := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.passKey())
	mac.Write([]byte(head))
	return head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// PassBody is a pass's body when its signature holds (checked in constant time), else false.
func (s *Signer) PassBody(pass string) ([]byte, bool) {
	head, sig, ok := strings.Cut(pass, ".")
	if !ok || len(pass) > 1000 {
		return nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, s.passKey())
	mac.Write([]byte(head))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return nil, false
	}
	body, err := base64.RawURLEncoding.DecodeString(head)
	return body, err == nil
}
