//go:build !darwin && !linux

package platform

import "fmt"

// Name is the platform this binary was built for.
const Name = "unsupported"

func defaultData() (string, error) {
	return "", fmt.Errorf("helmstudio has no default directory on this operating system; set %s to an absolute directory", EnvHome)
}

// Secrets returns the explicit refusal.
func Secrets() SecretStore { return unsupportedSecrets{platform: Name} }
