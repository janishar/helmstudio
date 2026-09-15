//go:build !darwin && !linux

package platform

import "os"

// LinkCount is not known here.
func LinkCount(fi os.FileInfo) (n uint64, ok bool) { return 0, false }
