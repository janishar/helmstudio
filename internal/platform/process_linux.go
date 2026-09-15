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
	e, err := parseProcStat(pid, string(data))
	return e.ProcessIdentity, err
}

// ProcessTable is a snapshot of every live process under /proc, zombies left
// out. A process that exits while the table is read is skipped.
func ProcessTable() ([]ProcessEntry, error) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}
	var out []ProcessEntry
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || pid <= 0 {
			continue
		}
		data, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		if e, err := parseProcStat(pid, string(data)); err == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// parseProcStat parses proc(5)'s stat line. The command name is in
// parentheses and may itself contain spaces and parentheses, so fields are
// counted from the last ')'.
func parseProcStat(pid int, line string) (ProcessEntry, error) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return ProcessEntry{}, fmt.Errorf("parsing /proc/%d/stat: no command name", pid)
	}
	f := strings.Fields(line[i+1:])
	// f[0] is field 3 (state), f[1] field 4 (ppid), f[2] field 5 (pgrp),
	// f[19] field 22 (starttime).
	if len(f) < 20 {
		return ProcessEntry{}, fmt.Errorf("parsing /proc/%d/stat: %d fields after the command name", pid, len(f))
	}
	if f[0] == "Z" || f[0] == "X" {
		return ProcessEntry{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return ProcessEntry{}, fmt.Errorf("parsing /proc/%d/stat ppid: %w", pid, err)
	}
	pgid, err := strconv.Atoi(f[2])
	if err != nil {
		return ProcessEntry{}, fmt.Errorf("parsing /proc/%d/stat pgrp: %w", pid, err)
	}
	start, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return ProcessEntry{}, fmt.Errorf("parsing /proc/%d/stat starttime: %w", pid, err)
	}
	return ProcessEntry{ProcessIdentity: ProcessIdentity{PID: pid, StartTime: start, PGID: pgid}, PPID: ppid}, nil
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
