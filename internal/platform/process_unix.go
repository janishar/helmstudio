//go:build darwin || linux

package platform

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartInGroup starts cmd as the leader of a new process group, so the group
// id equals its pid and every child it forks can be signalled together. It
// is the only way helmstudio starts a process: a studio's children must die
// with it, and a process group is the only way to be sure they do.
func StartInGroup(cmd *exec.Cmd) error {
	setGroup(cmd)
	return cmd.Start()
}

// RunInGroup runs cmd to completion as the leader of its own process group.
// cmd should come from exec.CommandContext: when its context ends, the whole
// group is killed, not only the leader.
func RunInGroup(cmd *exec.Cmd) error {
	setGroup(cmd)
	cmd.Cancel = func() error { return KillGroup(cmd.Process.Pid) }
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

func setGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

func checkGroup(pgid int) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return fmt.Errorf("process group %d: %w", pgid, ErrUnsafeGroup)
	}
	return nil
}

// TerminateGroup sends SIGTERM to every process in the group.
func TerminateGroup(pgid int) error { return signalGroup(pgid, syscall.SIGTERM) }

// KillGroup sends SIGKILL to every process in the group.
func KillGroup(pgid int) error { return signalGroup(pgid, syscall.SIGKILL) }

func signalGroup(pgid int, sig syscall.Signal) error {
	if err := checkGroup(pgid); err != nil {
		return err
	}
	err := syscall.Kill(-pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil // nobody left to signal
	}
	if err != nil {
		return fmt.Errorf("sending %v to process group %d: %w", sig, pgid, err)
	}
	return nil
}

// TerminateProcess sends SIGTERM to one process.
func TerminateProcess(pid int) error { return signalProcess(pid, syscall.SIGTERM) }

// KillProcess sends SIGKILL to one process.
func KillProcess(pid int) error { return signalProcess(pid, syscall.SIGKILL) }

func signalProcess(pid int, sig syscall.Signal) error {
	if pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("process %d: %w", pid, ErrUnsafeProcess)
	}
	err := syscall.Kill(pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil // already gone
	}
	if err != nil {
		return fmt.Errorf("sending %v to process %d: %w", sig, pid, err)
	}
	return nil
}

// GroupExists reports whether any process — a zombie included — is still in
// the group. A teardown is complete when this is false.
func GroupExists(pgid int) (bool, error) {
	if err := checkGroup(pgid); err != nil {
		return false, err
	}
	err := syscall.Kill(-pgid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("probing process group %d: %w", pgid, err)
	}
}

// PortHolder names the process listening on a TCP port, as "pid 812
// (python3)", using lsof. It returns "" when lsof is missing or finds no
// listener; the caller still reports the port as taken.
func PortHolder(port int) string {
	path, err := exec.LookPath("lsof")
	if err != nil {
		for _, p := range []string{"/usr/sbin/lsof", "/usr/bin/lsof"} {
			if _, statErr := os.Stat(p); statErr == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc")
	cmd.Stdout = &out
	_ = RunInGroup(cmd)
	var holders []string
	var pid string
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "p"):
			pid = line[1:]
		case strings.HasPrefix(line, "c") && pid != "":
			holders = append(holders, fmt.Sprintf("pid %s (%s)", pid, line[1:]))
			pid = ""
		}
	}
	return strings.Join(holders, ", ")
}

// ExitCode is a finished process's exit status as a shell reports it: the
// exit code, or 128 plus the signal number when a signal ended it (a SIGKILL
// reads as 137, the number the UI shows).
func ExitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
