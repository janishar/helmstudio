//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// sZomb is SZOMB from <sys/proc.h>: exited, waiting to be reaped.
const sZomb = 5

// IdentifyProcess reads pid's start time and process group from the kernel
// (sysctl kern.proc.pid). It returns ErrNoProcess when the pid is not live.
func IdentifyProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// The kernel returns a zero-length record, which x/sys reports as
		// EIO, for a pid that exited between the probe above and this call.
		if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
		}
		return ProcessIdentity{}, fmt.Errorf("reading process %d from the kernel: %w", pid, err)
	}
	if int(k.Proc.P_pid) != pid || k.Proc.P_stat == sZomb {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	start := k.Proc.P_starttime.Sec*1_000_000 + int64(k.Proc.P_starttime.Usec)
	return ProcessIdentity{PID: pid, StartTime: start, PGID: int(k.Eproc.Pgid)}, nil
}

// HostMemoryBytes is the machine's physical (on Apple Silicon, unified) memory.
func HostMemoryBytes() (uint64, error) {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, fmt.Errorf("reading hw.memsize: %w", err)
	}
	return n, nil
}
