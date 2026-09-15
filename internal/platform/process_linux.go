//go:build linux

package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// IdentifyProcess reads pid's start time and process group from
// /proc/<pid>/stat. It returns ErrNoProcess when the pid is not live.
func IdentifyProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("reading /proc/%d/stat: %w", pid, err)
	}
	return parseProcStat(pid, string(data))
}

// parseProcStat parses proc(5)'s stat line. The command name is in
// parentheses and may itself contain spaces and parentheses, so fields are
// counted from the last ')'.
func parseProcStat(pid int, line string) (ProcessIdentity, error) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return ProcessIdentity{}, fmt.Errorf("parsing /proc/%d/stat: no command name", pid)
	}
	f := strings.Fields(line[i+1:])
	// f[0] is field 3 (state), f[2] field 5 (pgrp), f[19] field 22 (starttime).
	if len(f) < 20 {
		return ProcessIdentity{}, fmt.Errorf("parsing /proc/%d/stat: %d fields after the command name", pid, len(f))
	}
	if f[0] == "Z" || f[0] == "X" {
		return ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	pgid, err := strconv.Atoi(f[2])
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("parsing /proc/%d/stat pgrp: %w", pid, err)
	}
	start, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("parsing /proc/%d/stat starttime: %w", pid, err)
	}
	return ProcessIdentity{PID: pid, StartTime: start, PGID: pgid}, nil
}

// HostMemoryBytes is MemTotal from /proc/meminfo.
func HostMemoryBytes() (uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("reading /proc/meminfo: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing MemTotal: %w", err)
			}
			return kb * 1024, nil
		}
	}
	return 0, errors.New("no MemTotal in /proc/meminfo")
}
