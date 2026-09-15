//go:build !darwin && !linux

package platform

import "errors"

// LockExclusive is not implemented on this platform.
func LockExclusive(path string) (release func() error, err error) {
	return nil, errors.New("exclusive file locks are not implemented on this operating system")
}
