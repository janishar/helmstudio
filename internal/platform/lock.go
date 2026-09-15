package platform

import "errors"

// ErrLocked means another holder already has the exclusive lock.
var ErrLocked = errors.New("already locked by another holder")
