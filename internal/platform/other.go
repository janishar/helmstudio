//go:build !darwin && !linux

package platform

import "fmt"

// Name is the platform this binary was built for.
const Name = "unsupported"

func currentDefaults(func(string) (string, bool)) (osDefaults, error) {
	return osDefaults{}, fmt.Errorf("helmstudio has no default directories on this operating system; set %s to an absolute directory", EnvHome)
}

// Secrets returns the explicit refusal.
func Secrets() SecretStore { return unsupportedSecrets{platform: Name} }
