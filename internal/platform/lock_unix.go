//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// LockExclusive takes a non-blocking exclusive lock on path, creating the file
// if needed, and returns a function that releases it. A second holder — in
// another process or this one — gets an error wrapping ErrLocked immediately
// rather than waiting. The lock dies with the process, so a crash never leaves
// it stale.
func LockExclusive(path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return f.Close, nil
}
