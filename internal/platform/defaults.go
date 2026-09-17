package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultDir is the directory in the user's home that holds every root unless
// something moves it (docs/design/06-storage.md §4, amended 2026-09-17).
const DefaultDir = ".helmstudio"

// homeDefault is the default data root, DefaultDir in the home directory. The
// build-constrained files choose whether a platform has one.
func homeDefault() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory: %w", err)
	}
	return defaultDataRoot(home), nil
}

// defaultDataRoot is homeDefault's rule as a pure function, so it is tested on
// whichever machine runs the tests.
func defaultDataRoot(home string) string { return filepath.Join(home, DefaultDir) }
