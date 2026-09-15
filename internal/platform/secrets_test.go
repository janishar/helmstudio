package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSecretNames(t *testing.T) {
	for _, ok := range []string{"hf_token", "huggingface.token", "a", "A-1"} {
		if err := checkSecretName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-w", "has space", "semi;colon", "quote\"", "new\nline", strings.Repeat("a", 129)} {
		if err := checkSecretName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSecretValues(t *testing.T) {
	if err := checkSecretValue("hf_AbCdEf0123456789 ~!@#$%^&*()\"'\\"); err != nil {
		t.Errorf("printable ASCII rejected: %v", err)
	}
	for _, bad := range []string{"", "line\n", "tab\t", "café", "nul\x00"} {
		if err := checkSecretValue(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUnsupportedSecretsRefuseExplicitly(t *testing.T) {
	s := unsupportedSecrets{platform: "linux"}
	ctx := context.Background()
	if _, err := s.Get(ctx, "hf_token"); !errors.Is(err, ErrNoSecretStore) {
		t.Errorf("Get err = %v, want ErrNoSecretStore", err)
	}
	if err := s.Set(ctx, "hf_token", "hf_x"); !errors.Is(err, ErrNoSecretStore) {
		t.Errorf("Set err = %v, want ErrNoSecretStore", err)
	}
	if err := s.Delete(ctx, "hf_token"); !errors.Is(err, ErrNoSecretStore) {
		t.Errorf("Delete err = %v, want ErrNoSecretStore", err)
	}
}
