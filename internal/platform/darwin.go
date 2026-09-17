//go:build darwin

package platform

// Name is the platform this binary was built for.
const Name = "darwin"

func defaultData() (string, error) { return homeDefault() }

// Secrets returns the macOS Keychain, under the "helmstudio" service.
func Secrets() SecretStore { return newKeychain(keychainService) }
