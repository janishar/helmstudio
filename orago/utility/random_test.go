package utility

import (
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"
)

func TestRandomToken(t *testing.T) {
	token := RandomToken(32)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("not base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(decoded))
	}
	if token == RandomToken(32) {
		t.Fatal("two tokens are equal")
	}
}

func TestRandomHex(t *testing.T) {
	value := RandomHex(16)
	if len(value) != 32 {
		t.Fatalf("length = %d, want 32", len(value))
	}
	if _, err := hex.DecodeString(value); err != nil {
		t.Fatalf("not hex: %v", err)
	}
	if value == RandomHex(16) {
		t.Fatal("two values are equal")
	}
}

func TestSHA256(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := hex.EncodeToString(SHA256("abc")); got != want {
		t.Fatalf("SHA256(abc) = %s, want %s", got, want)
	}
}

func TestNewID(t *testing.T) {
	v4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	id := NewID()
	if !v4.MatchString(id) {
		t.Fatalf("%q is not a v4 UUID", id)
	}
	if id == NewID() {
		t.Fatal("two ids are equal")
	}
}

func TestIsUUID(t *testing.T) {
	cases := map[string]bool{
		NewID():                                  true,
		"550E8400-E29B-41D4-A716-446655440000":   true,
		"":                                       false,
		"not-a-uuid":                             false,
		"550e8400e29b41d4a716446655440000":       false,
		"550e8400-e29b-41d4-a716-44665544000":    false,
		"550e8400-e29b-41d4-a716-4466554400000":  false,
		"550e8400-e29b-41d4-a716-44665544000g":   false,
		"550e8400-e29b-41d4-a716-446655440000\n": false,
		"x550e8400-e29b-41d4-a716-446655440000":  false,
	}
	for input, want := range cases {
		if got := IsUUID(input); got != want {
			t.Errorf("IsUUID(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestTraceparent(t *testing.T) {
	format := regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	value := Traceparent()
	if !format.MatchString(value) {
		t.Fatalf("%q is not a W3C traceparent", value)
	}
	if value == Traceparent() {
		t.Fatal("two traceparents are equal")
	}
}
