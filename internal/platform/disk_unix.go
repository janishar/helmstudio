//go:build darwin || linux

package platform

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// FreeDiskBytes is the space an unprivileged process can still write on the
// volume holding path (statfs f_bavail), which is what a download can use.
func FreeDiskBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("reading free space on the volume holding %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
