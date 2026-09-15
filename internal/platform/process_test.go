//go:build darwin || linux

package platform

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIdentifyProcessReadsAStableIdentity(t *testing.T) {
	a, err := IdentifyProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	b, err := IdentifyProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if a.PID != os.Getpid() || a.PGID != syscall.Getpgrp() || a.StartTime == 0 || !a.Matches(b) {
		t.Fatalf("identity %+v then %+v; want this process, its group, a non-zero start time, stable", a, b)
	}
	if _, err := IdentifyProcess(1 << 30); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("an impossible pid: err = %v, want ErrNoProcess", err)
	}
}

// StartInGroup puts the child in a group it leads; after the group is killed
// the group is empty and the child's identity is gone.
func TestStartInGroupAndKillGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & sleep 60")
	if err := StartInGroup(cmd); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	id, err := IdentifyProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if id.PGID != pid || id.PGID == syscall.Getpgrp() {
		t.Fatalf("child pgid %d, pid %d, ours %d; want the child leading its own group", id.PGID, pid, syscall.Getpgrp())
	}
	if ok, err := GroupExists(pid); err != nil || !ok {
		t.Fatalf("GroupExists = %v %v, want true", ok, err)
	}
	if err := KillGroup(pid); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, err := GroupExists(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the backgrounded sleep survived SIGKILL to the group")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := IdentifyProcess(pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("after kill: err = %v, want ErrNoProcess", err)
	}
	if code := ExitCode(cmd.ProcessState); code != 137 {
		t.Fatalf("exit code %d, want 137", code)
	}
}

// kill(2) reads 0 as the caller's own group and -1 as every process it may
// signal; neither may ever be reachable.
func TestGroupSignalsRefuseUnsafeIDs(t *testing.T) {
	for _, pgid := range []int{-1, 0, 1, syscall.Getpgrp()} {
		for name, f := range map[string]func(int) error{"terminate": TerminateGroup, "kill": KillGroup} {
			if err := f(pgid); !errors.Is(err, ErrUnsafeGroup) {
				t.Errorf("%s(%d) = %v, want ErrUnsafeGroup", name, pgid, err)
			}
		}
		if _, err := GroupExists(pgid); !errors.Is(err, ErrUnsafeGroup) {
			t.Errorf("GroupExists(%d) = %v, want ErrUnsafeGroup", pgid, err)
		}
	}
}

func TestPortHolderNamesTheListener(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		if _, statErr := os.Stat("/usr/sbin/lsof"); statErr != nil {
			t.Skip("lsof is not installed; PortHolder reports an unnamed holder here")
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := PortHolder(l.Addr().(*net.TCPAddr).Port)
	if !strings.Contains(got, "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("PortHolder = %q, want it to name pid %d", got, os.Getpid())
	}
}

func TestHostMemoryBytes(t *testing.T) {
	n, err := HostMemoryBytes()
	if err != nil || n < 1<<30 {
		t.Fatalf("HostMemoryBytes = %d, %v", n, err)
	}
}

// Every value comes back from the shell as exactly the one word it was.
func TestQuoteForShellRoundTrips(t *testing.T) {
	values := []string{
		"/Users/x/Library/Application Support/helmstudio/models/MiniMax-H3",
		"it's", `a"b`, "$HOME", "`id`", "a\\b", "semi;colon", "", "8710", "*", "new\nline",
	}
	for _, shell := range []string{"sh", "bash"} {
		script := "printf '%s\\0'"
		for _, v := range values {
			q, err := QuoteForShell(shell, v)
			if err != nil {
				t.Fatal(err)
			}
			script += " " + q
		}
		argv, err := ShellArgv(shell, script)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if len(got) != len(values) {
			t.Fatalf("%s: %d words back, want %d: %q", shell, len(got), len(values), got)
		}
		for i := range values {
			if got[i] != values[i] {
				t.Errorf("%s: word %d = %q, want %q", shell, i, got[i], values[i])
			}
		}
	}
	if q, _ := QuoteForShell("sh", "8710"); q != "8710" {
		t.Errorf("a plain port was quoted: %q", q)
	}
	if _, err := ShellArgv("powershell", "x"); err == nil {
		t.Error("powershell was accepted")
	}
}

// The process table shows a child that left its parent's group in its own
// session, still parented by the process that started it: that is how a
// teardown finds a render a studio detached (docs/decisions.md M5 Q11).
func TestProcessTableFindsADetachedChildByItsParent(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	id, err := IdentifyProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	table, err := ProcessTable()
	if err != nil {
		t.Fatal(err)
	}
	var self, child *ProcessEntry
	for i := range table {
		switch table[i].PID {
		case os.Getpid():
			self = &table[i]
		case cmd.Process.Pid:
			child = &table[i]
		}
	}
	if self == nil || self.PPID != os.Getppid() || self.PGID != syscall.Getpgrp() {
		t.Fatalf("this process in the table: %+v; want ppid %d, pgid %d", self, os.Getppid(), syscall.Getpgrp())
	}
	if child == nil || child.PPID != os.Getpid() || child.PGID == syscall.Getpgrp() || !child.ProcessIdentity.Matches(id) {
		t.Fatalf("the detached child in the table: %+v; want parent %d, its own group, identity %+v", child, os.Getpid(), id)
	}
}

// SameProcess ignores the group, and never matches an identity without a
// start time; KillProcess refuses init and this process.
func TestSameProcessAndSingleProcessSignals(t *testing.T) {
	a := ProcessIdentity{PID: 10, StartTime: 5, PGID: 10}
	if !a.SameProcess(ProcessIdentity{PID: 10, StartTime: 5, PGID: 99}) {
		t.Error("a process that changed group is not the same process")
	}
	if a.SameProcess(ProcessIdentity{PID: 10, StartTime: 6, PGID: 10}) || (ProcessIdentity{PID: 10}).SameProcess(ProcessIdentity{PID: 10}) {
		t.Error("a different start time, or none, matched")
	}
	for _, pid := range []int{0, 1, os.Getpid()} {
		if err := KillProcess(pid); !errors.Is(err, ErrUnsafeProcess) {
			t.Errorf("KillProcess(%d) = %v, want ErrUnsafeProcess", pid, err)
		}
	}
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := TerminateProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("sleep exited cleanly; want it ended by SIGTERM")
	}
	if err := TerminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("signalling a process already gone: %v, want nil", err)
	}
}
