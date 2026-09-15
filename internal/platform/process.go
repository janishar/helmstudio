package platform

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoProcess means no live process has the pid asked about. A zombie counts
// as gone: it has exited and only waits to be reaped.
var ErrNoProcess = errors.New("no such process")

// ErrUnsafeGroup means a group signal was refused because the process group
// id could address more than one studio's processes: kill(2) reads 0 as "my
// own group" and -1 as "every process I may signal".
var ErrUnsafeGroup = errors.New("refusing to signal an unsafe process group id")

// ProcessIdentity is what distinguishes one process from a later one that
// inherited its pid. All three fields are compared before a process is
// adopted or signalled after a daemon restart
// (docs/design/02-data-model.md §4, processes).
type ProcessIdentity struct {
	PID int
	// StartTime is an opaque, monotonic-per-boot value from the kernel: on
	// darwin microseconds since the epoch, on linux clock ticks since boot.
	// Compare it only with another value from IdentifyProcess on the same
	// machine.
	StartTime int64
	PGID      int
}

// Matches reports whether two identities name the same process.
func (a ProcessIdentity) Matches(b ProcessIdentity) bool {
	return a.PID == b.PID && a.StartTime == b.StartTime && a.PGID == b.PGID
}

// ShellArgv returns the argv that runs script in the manifest-declared shell
// (schema/manifest.json: sh, bash, powershell, cmd). Only the POSIX shells are
// available on the platforms helmstudio builds for today.
func ShellArgv(shell, script string) ([]string, error) {
	switch shell {
	case "", "sh":
		return []string{"/bin/sh", "-c", script}, nil
	case "bash":
		return []string{"/bin/bash", "-c", script}, nil
	default:
		return nil, fmt.Errorf("shell %q is not supported on %s; use sh or bash", shell, Name)
	}
}

// QuoteForShell quotes value so the named shell reads it back as exactly one
// word, whatever it contains — a space in "Application Support", a quote, a
// dollar sign. A value made only of characters no POSIX shell treats
// specially is returned unchanged, so a port number stays readable in a
// recorded command.
func QuoteForShell(shell, value string) (string, error) {
	switch shell {
	case "", "sh", "bash":
	default:
		return "", fmt.Errorf("shell %q is not supported on %s; use sh or bash", shell, Name)
	}
	if value != "" && strings.Trim(value, shellSafe) == "" {
		return value, nil
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'", nil
}

const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"
