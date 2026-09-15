//go:build darwin && machine

package platform

// Machine-bound: this writes to, reads from and deletes an item in the login
// Keychain of whoever runs it, under the service "helmstudio.test". It is not
// part of `make gate` and is never reported as verified by it. Run by hand:
//
//	go test -tags machine -run TestKeychainRoundTrip -v ./internal/platform

import (
	"context"
	"errors"
	"testing"
)

func TestKeychainRoundTrip(t *testing.T) {
	ctx := context.Background()
	k := newKeychain("helmstudio.test")
	const name = "roundtrip"
	t.Cleanup(func() { _ = k.Delete(ctx, name) })

	if _, err := k.Get(ctx, name); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("before Set: err = %v, want ErrSecretNotFound (a stale item from an earlier run?)", err)
	}
	const first = `hf_first "quoted" \ value`
	if err := k.Set(ctx, name, first); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := k.Get(ctx, name); err != nil || got != first {
		t.Fatalf("Get = %q, %v; want %q", got, err, first)
	}
	const second = "hf_second"
	if err := k.Set(ctx, name, second); err != nil {
		t.Fatalf("Set (replace): %v", err)
	}
	if got, err := k.Get(ctx, name); err != nil || got != second {
		t.Fatalf("Get after replace = %q, %v; want %q", got, err, second)
	}
	if err := k.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get(ctx, name); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("after Delete: err = %v, want ErrSecretNotFound", err)
	}
}
