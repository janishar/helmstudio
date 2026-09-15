//go:build darwin || linux

package platform

import (
	"os"
	"syscall"
)

// LinkCount reports how many directory entries name the file fi describes.
// ok is false when the platform does not say.
func LinkCount(fi os.FileInfo) (n uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false
	}
	return uint64(st.Nlink), true
}
