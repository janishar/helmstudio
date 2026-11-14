package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func auth(t *testing.T, calls *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.Header.Get("x-api-key") {
		case "good":
			w.Write([]byte(`{"client_id":"c1","scopes":["a","b"]}`))
		case "slow":
			time.Sleep(300 * time.Millisecond)
		default:
			w.WriteHeader(401)
			w.Write([]byte(`{"detail":"invalid api key"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck(t *testing.T) {
	cache.Clear()
	var calls atomic.Int32
	srv := auth(t, &calls)
	c := &Config{VerificationUrls: []string{"http://127.0.0.1:1/dead", srv.URL}, TimeoutMs: 100}

	for _, p := range []string{"/health", "/auth/health", "/auth/.well-known/jwks.json", "/_next/x.js"} {
		if r := c.check(p, ""); r.status != 0 || r.clientID != "" {
			t.Fatalf("bypass %s: %+v", p, r)
		}
	}
	if r := c.check("/v1/studio/sessions", ""); r.status != 401 || r.body != msgMissing {
		t.Fatalf("missing: %+v", r)
	}
	r := c.check("/v1/studio/sessions", "good")
	if r.status != 0 || r.clientID != "c1" || len(r.scopes) != 2 {
		t.Fatalf("good: %+v", r)
	}
	c.check("/v1/studio/sessions", "good") // cached
	if calls.Load() != 1 {
		t.Fatalf("positive cache: %d calls", calls.Load())
	}
	if r := c.check("/v1/studio/sessions", "bad"); r.status != 401 || r.body != msgInvalid {
		t.Fatalf("bad: %+v", r)
	}
	c.check("/v1/studio/sessions", "bad") // negative cache
	if calls.Load() != 2 {
		t.Fatalf("negative cache: %d calls", calls.Load())
	}
	if r := c.check("/v1/studio/sessions", "slow"); r.status != 503 {
		t.Fatalf("timeout must fail closed: %+v", r)
	}
	if _, ok := cache.Load(sha("slow")); ok {
		t.Fatal("unavailable must not be cached")
	}

	// cache expiry
	c.PositiveTTL = 1
	cache.Clear()
	c.check("/v1/studio/sessions", "good")
	v, _ := cache.Load(sha("good"))
	if d := time.Until(v.(verdict).until); d > time.Second {
		t.Fatalf("ttl %v", d)
	}
}

func TestAuthDown(t *testing.T) {
	cache.Clear()
	c := &Config{VerificationUrls: []string{"http://127.0.0.1:1/verify/apikey"}}
	if r := c.check("/v1/studio/sessions", "good"); r.status != 503 {
		t.Fatalf("auth down: %+v", r)
	}
	if r := (&Config{}).check("/v1/studio/sessions", "good"); r.status != 503 {
		t.Fatalf("no urls: %+v", r)
	}
}

func TestCustomBypass(t *testing.T) {
	c := &Config{BypassPaths: []string{`^/public/`}}
	if r := c.check("/public/a", ""); r.status != 0 {
		t.Fatal("custom bypass")
	}
	if r := c.check("/health", ""); r.status != 401 {
		t.Fatal("custom list replaces defaults")
	}
}

func sha(k string) string {
	s := sha256.Sum256([]byte(k))
	return hex.EncodeToString(s[:])
}
