package common

import (
	"strings"
	"testing"
	"time"
)

// A pass (API 1.70) verifies with its signer only, never once changed, and a /files signature is never a pass.
func TestPass(t *testing.T) {
	signer, other := NewSigner("0123456789abcdef", "http://localhost:8300", time.Hour), NewSigner("fedcba9876543210", "x", time.Hour)
	pass := signer.Pass([]byte(`{"owner":"o"}`))
	if body, ok := signer.PassBody(pass); !ok || string(body) != `{"owner":"o"}` {
		t.Fatal(string(body), ok)
	}
	head, sig, _ := strings.Cut(pass, ".")
	for _, bad := range []string{"", pass + "x", head, "e30." + sig, head + "." + signer.signature(head, 0),
		strings.Repeat("a", 1001) + "." + sig} {
		if _, ok := signer.PassBody(bad); ok {
			t.Fatal("verified", bad)
		}
	}
	if _, ok := other.PassBody(pass); ok {
		t.Fatal("another key verified it")
	}
	if signer.BaseURL() != "http://localhost:8300" {
		t.Fatal(signer.BaseURL())
	}
}
