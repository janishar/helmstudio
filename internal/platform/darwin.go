//go:build darwin

package platform

import (
	"fmt"
	"os"
)

// Name is the platform this binary was built for.
const Name = "darwin"

func currentDefaults(func(string) (string, bool)) (osDefaults, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return osDefaults{}, fmt.Errorf("finding the home directory: %w", err)
	}
	return darwinDefaults(home), nil
}

// Secrets returns the macOS Keychain, under the "helmstudio" service.
func Secrets() SecretStore { return newKeychain(keychainService) }
