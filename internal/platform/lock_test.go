//go:build darwin || linux

package platform

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockExclusiveRefusesASecondHolderUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release, err := LockExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	// flock is per open file, so a second open in the same process conflicts
	// exactly as another process would.
	if _, err := LockExclusive(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock err = %v, want ErrLocked", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := LockExclusive(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again()
}
