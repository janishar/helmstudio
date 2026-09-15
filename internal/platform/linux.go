//go:build linux

package platform

import (
	"fmt"
	"os"
)

// Name is the platform this binary was built for.
const Name = "linux"

func currentDefaults(lookupEnv func(string) (string, bool)) (osDefaults, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return osDefaults{}, fmt.Errorf("finding the home directory: %w", err)
	}
	return linuxDefaults(home, lookupEnv), nil
}

// Secrets returns the explicit refusal: Linux has no secret store yet
// (docs/design/01-prd.md §14 lists it as outstanding work).
func Secrets() SecretStore { return unsupportedSecrets{platform: Name} }
