package platform

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// SecretStore keeps secrets out of helm.db (docs/design/01-prd.md R43). The
// database records only that a secret exists and when it was added.
type SecretStore interface {
	// Get returns the secret stored under name, or an error wrapping
	// ErrSecretNotFound.
	Get(ctx context.Context, name string) (string, error)
	// Set stores or replaces the secret under name.
	Set(ctx context.Context, name, secret string) error
	// Delete removes the secret under name. Deleting a missing secret
	// returns an error wrapping ErrSecretNotFound.
	Delete(ctx context.Context, name string) error
}

var (
	// ErrSecretNotFound means no secret is stored under the name.
	ErrSecretNotFound = errors.New("secret not found")
	// ErrNoSecretStore means this platform has no secret store. It is
	// deliberately an error rather than a plaintext file.
	ErrNoSecretStore = errors.New("no secret store on this platform")
)

const keychainService = "helmstudio"

// secretNamePattern restricts names so they can never be read as an option or
// need quoting on a command line.
var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func checkSecretName(name string) error {
	if !secretNamePattern.MatchString(name) {
		return fmt.Errorf("secret name %q is invalid: use letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	return nil
}

// checkSecretValue limits secrets to printable ASCII. A Hugging Face token is
// always within it, and it guarantees `security -w` prints the value itself
// rather than a hex dump it falls back to for unprintable bytes.
func checkSecretValue(secret string) error {
	if secret == "" {
		return errors.New("secret is empty")
	}
	for i := 0; i < len(secret); i++ {
		if c := secret[i]; c < 0x20 || c > 0x7e {
			return fmt.Errorf("secret contains a byte outside printable ASCII at offset %d", i)
		}
	}
	return nil
}

type unsupportedSecrets struct{ platform string }

func (u unsupportedSecrets) err() error {
	return fmt.Errorf("%w (%s): helmstudio stores secrets only in the macOS Keychain, and has no secret store for this platform yet", ErrNoSecretStore, u.platform)
}

func (u unsupportedSecrets) Get(context.Context, string) (string, error) { return "", u.err() }
func (u unsupportedSecrets) Set(context.Context, string, string) error   { return u.err() }
func (u unsupportedSecrets) Delete(context.Context, string) error        { return u.err() }
