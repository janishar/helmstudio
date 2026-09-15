//go:build !darwin && !linux

package platform

import "fmt"

// FreeDiskBytes refuses: the disk pre-check is not implemented here.
func FreeDiskBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("reading free disk space is not implemented on %s", Name)
}
