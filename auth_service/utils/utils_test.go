package utils

import (
	"testing"
	"time"
)

// TestPassword covers hashing, verifying and the weak-password rules.
func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("salt must differ")
	}
	if !VerifyPassword("correct horse battery", h) || VerifyPassword("wrong horse battery", h) || VerifyPassword("x", "garbage") {
		t.Fatal("verify")
	}
	if WeakPassword("short") == "" || WeakPassword("Password123") == "" || WeakPassword("a fine long passphrase") != "" {
		t.Fatal("weak")
	}
}

// TestLimiter covers blocking after too many failures, and resetting.
func TestLimiter(t *testing.T) {
	l := NewLimiter(2, 50*time.Millisecond)
	l.Fail("a")
	if l.Blocked("a") {
		t.Fatal("blocked too early")
	}
	l.Fail("a")
	if !l.Blocked("a") || l.Blocked("b") {
		t.Fatal("limit")
	}
	time.Sleep(60 * time.Millisecond)
	if l.Blocked("a") {
		t.Fatal("window must expire")
	}
	l.Fail("a")
	l.Fail("a")
	l.Reset("a")
	if l.Blocked("a") {
		t.Fatal("reset")
	}
}

// TestKeys covers generating, loading and identifying the signing key.
func TestKeys(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadKey(dir)
	if err != nil || !k1.Equal(k2) || KeyID(&k1.PublicKey) != KeyID(&k2.PublicKey) {
		t.Fatal("reload must return the same key", err)
	}
}
