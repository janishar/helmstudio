//go:build darwin

package platform

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// call records one invocation of the security tool, without running it.
type call struct {
	stdin string
	args  []string
}

func fakeKeychain(exit int, stdout, stderr string) (*keychain, *[]call) {
	var calls []call
	k := &keychain{service: "helmstudio.test", run: func(_ context.Context, stdin string, args ...string) (string, string, int, error) {
		calls = append(calls, call{stdin: stdin, args: args})
		return stdout, stderr, exit, nil
	}}
	return k, &calls
}

func TestKeychainSetKeepsTheSecretOutOfArgv(t *testing.T) {
	const secret = "hf_SuperSecretValue123"
	k, calls := fakeKeychain(0, "", "")
	if err := k.Set(context.Background(), "hf_token", secret); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("security ran %d times, want 1", len(*calls))
	}
	c := (*calls)[0]
	encoded := hex.EncodeToString([]byte(secret))
	for _, a := range c.args {
		if strings.Contains(a, secret) || strings.Contains(a, encoded) {
			t.Fatalf("secret visible in argv %q", c.args)
		}
	}
	want := "add-generic-password -U -s helmstudio.test -a hf_token -X " + encoded + "\n"
	if len(c.args) != 1 || c.args[0] != "-i" || c.stdin != want {
		t.Fatalf("security %q with stdin %q, want -i with %q", c.args, c.stdin, want)
	}
}

func TestKeychainGetTrimsOnlyTheTrailingNewline(t *testing.T) {
	k, calls := fakeKeychain(0, "hf_abc \n", "")
	got, err := k.Get(context.Background(), "hf_token")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hf_abc " {
		t.Errorf("Get = %q, want %q", got, "hf_abc ")
	}
	want := []string{"find-generic-password", "-s", "helmstudio.test", "-a", "hf_token", "-w"}
	if strings.Join((*calls)[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %q, want %q", (*calls)[0].args, want)
	}
}

func TestKeychainMissingItemIsErrSecretNotFound(t *testing.T) {
	k, _ := fakeKeychain(errSecItemNotFound, "", "could not be found")
	if _, err := k.Get(context.Background(), "hf_token"); !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("Get err = %v, want ErrSecretNotFound", err)
	}
	if err := k.Delete(context.Background(), "hf_token"); !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("Delete err = %v, want ErrSecretNotFound", err)
	}
}

func TestKeychainOtherFailuresCarryTheToolsMessage(t *testing.T) {
	k, _ := fakeKeychain(51, "", "User interaction is not allowed.\n")
	err := k.Set(context.Background(), "hf_token", "hf_x")
	if err == nil || errors.Is(err, ErrSecretNotFound) || !strings.Contains(err.Error(), "User interaction is not allowed.") {
		t.Fatalf("err = %v, want the tool's stderr and not ErrSecretNotFound", err)
	}
}

func TestKeychainRejectsBadInputBeforeRunningAnything(t *testing.T) {
	k, calls := fakeKeychain(0, "", "")
	ctx := context.Background()
	_ = k.Set(ctx, "-a evil", "hf_x")
	_ = k.Set(ctx, "hf_token", "new\nline")
	_ = k.Set(ctx, "hf_token", strings.Repeat("x", maxSecretBytes+1))
	_, _ = k.Get(ctx, "bad name")
	_ = k.Delete(ctx, "")
	if len(*calls) != 0 {
		t.Fatalf("security ran %d times on invalid input", len(*calls))
	}
}
