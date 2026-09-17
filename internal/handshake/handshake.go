// Package handshake is how helmstudio.app finds its daemon.
//
// The shell spawns the daemon as a sidecar and reads one line of JSON from
// its stdout. That line is the signal that the daemon is listening, and it
// says where — the shell has no port to guess and no readiness to poll for.
// The same record is left in the data root as daemon.json, and that file is
// what makes adoption possible: a shell that was killed, or updated and
// relaunched, finds the daemon that outlived it instead of starting a second
// one that would only fail to bind the port
// (docs/agents/milestones/09-mac-app-and-release.md, docs/design/01-prd.md R69).
//
// stdout belongs to the handshake. Everything the daemon says to a person
// goes to stderr through log, so the line is never mixed with anything else.
package handshake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/janishar/helmstudio/internal/platform"
)

// Name is the record's file name inside the data root.
const Name = "daemon.json"

// Record is what a running daemon publishes about itself. It is written once,
// after the listener is open, and removed on a clean exit.
type Record struct {
	// Addr is where the daemon actually listens, which is what the listener
	// reports rather than what was asked for.
	Addr string `json:"addr"`
	// URL is what the shell loads. It is Addr spelled as the origin the
	// launcher is served from, so a shell never builds one by hand and can
	// never build a different one than the Origin check expects.
	URL string `json:"url"`
	// PID and StartTime together name the process. The pid alone is not an
	// identity: pids are reused, and a record left behind by a daemon that
	// was killed eventually names an unrelated process.
	PID       int   `json:"pid"`
	StartTime int64 `json:"start_time"`
	// Version is the daemon's own version, so a shell can tell whether the
	// daemon it adopted is the one it ships with.
	Version string `json:"version"`
	// Data is the data root this record was written in. A record is per tree,
	// and a shell that resolves a different tree than the daemon did should
	// be able to see that rather than infer it.
	Data string `json:"data"`
}

// ErrNoDaemon means no daemon is running for this data root: there is no
// record, or the record names a process that has gone.
var ErrNoDaemon = errors.New("no daemon is running")

// Current describes this process for a daemon listening on addr.
func Current(addr, version, data string) (Record, error) {
	id, err := platform.IdentifyProcess(os.Getpid())
	if err != nil {
		return Record{}, fmt.Errorf("identifying this process for the handshake: %w", err)
	}
	return Record{
		Addr:      addr,
		URL:       "http://" + addr,
		PID:       id.PID,
		StartTime: id.StartTime,
		Version:   version,
		Data:      data,
	}, nil
}

// Announce writes the record to w as one line of JSON and flushes nothing:
// the caller's w is stdout, which is unbuffered.
func Announce(w io.Writer, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding the handshake: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("writing the handshake: %w", err)
	}
	return nil
}

// Path is where the record lives for a given tree.
func Path(dirs *platform.Dirs) string { return filepath.Join(dirs.Data(), Name) }

// Publish writes the record into the data root, replacing whatever is there.
// The write is a rename onto the final name, so a shell reading it never sees
// half a record.
func Publish(dirs *platform.Dirs, r Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", Name, err)
	}
	final := Path(dirs)
	tmp, err := os.CreateTemp(filepath.Dir(final), Name+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", final, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", final, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", final, err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", final, err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return fmt.Errorf("writing %s: %w", final, err)
	}
	return nil
}

// Withdraw removes the record. A record that is already gone is not an error:
// a daemon shutting down should not fail because someone deleted it first.
func Withdraw(dirs *platform.Dirs) error {
	if err := os.Remove(Path(dirs)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", Path(dirs), err)
	}
	return nil
}

// Read returns the record as it is written, without asking whether the
// process it names is still alive. Use Adopt to decide whether to spawn.
func Read(dirs *platform.Dirs) (Record, error) {
	b, err := os.ReadFile(Path(dirs))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNoDaemon
	}
	if err != nil {
		return Record{}, fmt.Errorf("reading %s: %w", Path(dirs), err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, fmt.Errorf("reading %s: %w", Path(dirs), err)
	}
	return r, nil
}

// Adopt returns the record of a daemon that is still running, or ErrNoDaemon.
//
// Both the pid and the start time recorded beside it must match the live
// process, which is the same rule the supervisor re-adopts studios by
// (docs/decisions.md, 2026-09-15 supervision): checking the pid alone
// eventually signals whatever unrelated process inherited the number. The
// process group is deliberately not compared — a daemon started from a
// terminal and one started by the shell are in different groups and are
// equally adoptable.
//
// A stale record is left where it is. The next daemon replaces it when it
// listens, and a reader that deletes what it finds surprises the next reader.
func Adopt(dirs *platform.Dirs) (Record, error) {
	r, err := Read(dirs)
	if err != nil {
		return Record{}, err
	}
	if r.PID <= 0 {
		return Record{}, ErrNoDaemon
	}
	live, err := platform.IdentifyProcess(r.PID)
	if errors.Is(err, platform.ErrNoProcess) {
		return Record{}, ErrNoDaemon
	}
	if err != nil {
		return Record{}, fmt.Errorf("checking whether the daemon in %s is still running: %w", Path(dirs), err)
	}
	if !live.SameProcess(platform.ProcessIdentity{PID: r.PID, StartTime: r.StartTime}) {
		return Record{}, ErrNoDaemon
	}
	return r, nil
}
