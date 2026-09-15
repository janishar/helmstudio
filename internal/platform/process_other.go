//go:build !darwin && !linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
)

func unsupportedProcesses() error {
	return fmt.Errorf("process supervision is not implemented on %s: it needs process groups and POSIX signals", Name)
}

// StartInGroup refuses: there are no process groups here.
func StartInGroup(*exec.Cmd) error { return unsupportedProcesses() }

// RunInGroup refuses.
func RunInGroup(*exec.Cmd) error { return unsupportedProcesses() }

// TerminateGroup refuses.
func TerminateGroup(int) error { return unsupportedProcesses() }

// KillGroup refuses.
func KillGroup(int) error { return unsupportedProcesses() }

// GroupExists refuses.
func GroupExists(int) (bool, error) { return false, unsupportedProcesses() }

// PortHolder knows nothing here.
func PortHolder(int) string { return "" }

// IdentifyProcess refuses.
func IdentifyProcess(int) (ProcessIdentity, error) { return ProcessIdentity{}, unsupportedProcesses() }

// HostMemoryBytes refuses.
func HostMemoryBytes() (uint64, error) { return 0, unsupportedProcesses() }

// ExitCode is the process's exit code.
func ExitCode(ps *os.ProcessState) int { return ps.ExitCode() }
