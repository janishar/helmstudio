//go:build linux

package platform

// Name is the platform this binary was built for.
const Name = "linux"

func defaultData() (string, error) { return homeDefault() }

// Secrets returns the explicit refusal: Linux has no secret store yet
// (docs/design/01-prd.md §14 lists it as outstanding work).
func Secrets() SecretStore { return unsupportedSecrets{platform: Name} }
