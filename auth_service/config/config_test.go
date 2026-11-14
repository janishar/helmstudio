package config

import (
	"strings"
	"testing"
)

// TestLoadTrustedProxies accepts addresses and CIDRs, and names the variable when an entry is bad.
func TestLoadTrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://scratch")
	t.Setenv("TRUSTED_PROXIES", "172.30.90.10, 10.0.0.0/8,")
	env, err := Load()
	if err != nil || len(env.TrustedProxies) != 2 {
		t.Fatalf("good entries: %v %v", env.TrustedProxies, err)
	}
	t.Setenv("TRUSTED_PROXIES", "172.30.90.10,not-an-ip")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("bad entry: %v", err)
	}
}
