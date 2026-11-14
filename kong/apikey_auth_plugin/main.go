// Kong plugin (go-pdk, access phase): checks x-api-key against auth_service's GET /verify/apikey.
// Fail closed (503 when auth is unreachable), TTL cache keyed by SHA-256 of the key, never logs the key.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Kong/go-pdk"
	"github.com/Kong/go-pdk/server"
)

// Version and Priority are what Kong reads when it loads the plugin.
const (
	Version  = "1.0.0"
	Priority = 1250 // before rate limiting (910), like key-auth
)

// defaultBypass lists the paths that need no key, used when Config.BypassPaths is empty.
var defaultBypass = []string{`^/health$`, `^/[^/]+/health$`, `^/auth/\.well-known/jwks\.json$`, `^/_next/`, `^/static/`, `^/favicon\.ico$`}

// Config is the plugin's Kong configuration. Its methods do the work for each request.
type Config struct {
	VerificationUrls []string `json:"verification_urls"`
	BypassPaths      []string `json:"bypass_paths"` // regexes; empty = defaults
	TimeoutMs        int      `json:"timeout_ms"`   // default 2000
	PositiveTTL      int      `json:"positive_ttl"` // seconds, default 60
	NegativeTTL      int      `json:"negative_ttl"` // seconds, default 10
	StripKey         *bool    `json:"strip_key"`    // default true; false for upstreams that check the key themselves
}

// New returns an empty Config for Kong to fill.
func New() interface{} { return &Config{} }

// main serves the plugin to Kong over go-pdk.
func main() { _ = server.StartServer(New, Version, Priority) }

// verdict is one cached answer from auth_service, valid until the time in until.
type verdict struct {
	ok       bool
	clientID string
	scopes   []string
	until    time.Time
}

// Process-wide state: the verdict and regexp caches, the round-robin counter for verification URLs, the HTTP client.
var (
	cache   sync.Map // sha256 hex -> verdict  // ponytail: unbounded per-process map, add LRU if key churn grows
	next    atomic.Uint64
	reCache sync.Map // pattern -> *regexp.Regexp
	client  = &http.Client{}
)

// Response bodies for refused requests, in the contract's error shape.
const (
	msgMissing     = `{"detail":"x-api-key header is missing"}`
	msgInvalid     = `{"detail":"invalid api key"}`
	msgUnavailable = `{"detail":"auth service unavailable"}`
)

// result of a check: status 0 = let the request through.
type result struct {
	status   int
	body     string
	clientID string
	scopes   []string
}

// bypass reports whether path skips the key check. Patterns are compiled once, and one that won't compile is
// skipped.
func (c *Config) bypass(path string) bool {
	pats := c.BypassPaths
	if len(pats) == 0 {
		pats = defaultBypass
	}
	for _, p := range pats {
		re, ok := reCache.Load(p)
		if !ok {
			r, err := regexp.Compile(p)
			if err != nil {
				continue
			}
			re, _ = reCache.LoadOrStore(p, r)
		}
		if re.(*regexp.Regexp).MatchString(path) {
			return true
		}
	}
	return false
}

// ttl turns seconds into a duration, using def when v is not positive.
func ttl(v, def int) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(v) * time.Second
}

// check decides one request: bypass, then cache, then auth_service. Valid and invalid answers are cached for their
// own TTLs.
func (c *Config) check(path, key string) result {
	if c.bypass(path) {
		return result{}
	}
	if key == "" {
		return result{status: 401, body: msgMissing}
	}
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	if v, ok := cache.Load(h); ok && time.Now().Before(v.(verdict).until) {
		return v.(verdict).result()
	}
	v, ok := c.verify(key)
	if !ok {
		return result{status: 503, body: msgUnavailable} // fail closed, not cached
	}
	if v.ok {
		v.until = time.Now().Add(ttl(c.PositiveTTL, 60))
	} else {
		v.until = time.Now().Add(ttl(c.NegativeTTL, 10))
	}
	cache.Store(h, v)
	return v.result()
}

// result turns a verdict into a check result: a 401 or the client's identity.
func (v verdict) result() result {
	if !v.ok {
		return result{status: 401, body: msgInvalid}
	}
	return result{clientID: v.clientID, scopes: v.scopes}
}

// verify asks auth_service, starting at the next url in turn and moving on after an error or an unexpected status.
// A 401 or 403 is a definite no. ok=false means no usable answer from any url.
func (c *Config) verify(key string) (verdict, bool) {
	timeout := time.Duration(c.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	n := len(c.VerificationUrls)
	start := int(next.Add(1))
	for i := 0; i < n; i++ {
		url := c.VerificationUrls[(start+i)%n]
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("x-api-key", key)
		cl := *client
		cl.Timeout = timeout
		resp, err := cl.Do(req)
		if err != nil {
			continue
		}
		var body struct {
			ClientID string   `json:"client_id"`
			Scopes   []string `json:"scopes"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == 200 && err == nil && body.ClientID != "":
			return verdict{ok: true, clientID: body.ClientID, scopes: body.Scopes}, true
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return verdict{}, true
		}
	}
	return verdict{}, false
}

// jsonHeaders goes on every refusal.
var jsonHeaders = map[string][]string{"Content-Type": {"application/json"}}

// Access is Kong's access-phase hook. It refuses the request or passes it on. A request with a valid key reaches the
// upstream with X-Client-Id and X-Key-Scopes set, and the key removed unless StripKey is false.
func (c *Config) Access(kong *pdk.PDK) {
	// never trust client-sent identity headers
	_ = kong.ServiceRequest.ClearHeader("X-Client-Id")
	_ = kong.ServiceRequest.ClearHeader("X-Key-Scopes")
	path, _ := kong.Request.GetPath()
	key, _ := kong.Request.GetHeader("x-api-key")
	r := c.check(path, key)
	if r.status != 0 {
		if r.status == 503 {
			_ = kong.Log.Err("apikey-auth: auth service unavailable")
		}
		kong.Response.Exit(r.status, []byte(r.body), jsonHeaders)
		return
	}
	if r.clientID == "" { // bypassed path
		return
	}
	_ = kong.ServiceRequest.SetHeader("X-Client-Id", r.clientID)
	_ = kong.ServiceRequest.SetHeader("X-Key-Scopes", strings.Join(r.scopes, ","))
	if c.StripKey == nil || *c.StripKey {
		_ = kong.ServiceRequest.ClearHeader("x-api-key")
	}
}
