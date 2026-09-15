package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Process and group states, spelled as docs/design/01-prd.md §5 spells them.
const (
	stateQueued   = "queued"
	stateStarting = "starting"
	stateRunning  = "running"
	stateStopping = "stopping"
	stateExited   = "exited"
	stateFailed   = "failed"
)

// Exit reasons (docs/design/02-data-model.md §4). An empty reason is stored
// as NULL: a sibling stopped because another member failed carries none; the
// member that failed carries the reason.
const (
	reasonClean         = "clean"
	reasonCrashed       = "crashed"
	reasonHealthTimeout = "health_timeout"
	reasonKilledByUser  = "killed_by_user"
	reasonPreempted     = "preempted"
)

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

// write runs one write transaction. what names it for the log and for tests.
// Callers that depend on the write — the rows re-adoption reads — check the
// error; state that only drives the UI logs it and carries on.
func (s *Supervisor) write(what string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	if s.cfg.failWrite != nil {
		if err := s.cfg.failWrite(what); err != nil {
			s.logf("supervisor: writing %s: %v", what, err)
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.Update(ctx, fn); err != nil {
		s.logf("supervisor: writing %s: %v", what, err)
		return fmt.Errorf("recording %s: %w", what, err)
	}
	return nil
}

// insertQueued writes a launch's rows in one transaction, so a failure
// leaves no queued row behind for a later re-adoption to start.
func (s *Supervisor) insertQueued(procs []*proc) error {
	return s.write("queued processes", func(ctx context.Context, tx *sql.Tx) error {
		for _, p := range procs {
			cmd, _ := json.Marshal(p.command)
			if _, err := tx.ExecContext(ctx, `INSERT INTO processes
				(id, studio_id, group_run_id, spec_name, role, port, command, state, health_state)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				p.rowID, p.g.studioID, p.g.runID, p.name, p.role, nullInt(p.port), string(cmd), stateQueued, healthUnknown); err != nil {
				return err
			}
		}
		return nil
	})
}

// persistClaim marks a row starting, with no pid, before its process is
// started. A daemon killed between this write and persistSpawn leaves a row a
// restarted daemon must not start again: a process may be running from it
// that nobody recorded. Only a row still queued is known never to have run.
func (s *Supervisor) persistClaim(p *proc) error {
	return s.write("process claim", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE processes SET state = ?, pid = NULL, pid_start_time = NULL, pgid = NULL WHERE id = ?`, stateStarting, p.rowID)
		return err
	})
}

// persistSpawn records everything re-adoption will need, in one write, before
// the process can be health-gated. If it fails, the caller kills the process:
// one that re-adoption could never find must not be left running.
func (s *Supervisor) persistSpawn(p *proc, logRel string, newLog bool) error {
	return s.write("spawned process", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE processes SET pid = ?, pid_start_time = ?, pgid = ?,
			state = ?, health_state = ?, exit_reason = NULL, exit_code = NULL, started_at = ?, exited_at = NULL
			WHERE id = ?`,
			p.ident.PID, nullInt64(p.ident.StartTime), p.ident.PGID, stateStarting, healthUnknown, p.startedAt.UnixMilli(), p.rowID); err != nil {
			return err
		}
		if !newLog {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at)
			VALUES (?, ?, 'run', 'process', ?, ?, ?)`,
			newID(s.now()), logRel, p.rowID, p.g.studioID, s.now().UnixMilli())
		return err
	})
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Supervisor) persistState(p *proc, state, reason string, exitCode *int, exitedAt time.Time) {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	_ = s.write("process state", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE processes SET state = ?, exit_reason = ?, exit_code = ?, exited_at = ? WHERE id = ?`,
			state, nullString(reason), code, unixOrNil(exitedAt), p.rowID)
		return err
	})
}

func (s *Supervisor) persistHealth(p *proc, health string) {
	_ = s.write("health state", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE processes SET health_state = ? WHERE id = ?`, health, p.rowID)
		return err
	})
}

func (s *Supervisor) markLogTruncated(procRowID string) {
	_ = s.write("log truncation", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE log_files SET truncated = 1 WHERE owner_kind = 'process' AND owner_id = ?`, procRowID)
		return err
	})
}

// row is a processes row as read back.
type row struct {
	ID, StudioID, GroupRunID, SpecName, Role string
	PID, PGID, Port                          int
	StartTime                                int64
	Command                                  []string
	State, Health, ExitReason                string
	ExitCode                                 *int
	StartedAt, ExitedAt                      time.Time
	LogPath                                  string
}

const rowColumns = `p.id, p.studio_id, p.group_run_id, p.spec_name, p.role, p.pid, p.pid_start_time, p.pgid, p.port,
	p.command, p.state, p.health_state, p.exit_reason, p.exit_code, p.started_at, p.exited_at,
	(SELECT l.path FROM log_files l WHERE l.owner_kind = 'process' AND l.owner_id = p.id ORDER BY l.created_at DESC LIMIT 1)`

func scanRows(rows *sql.Rows) ([]row, error) {
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		var pid, start, pgid, port, code, started, exited sql.NullInt64
		var command string
		var health, reason, logPath sql.NullString
		if err := rows.Scan(&r.ID, &r.StudioID, &r.GroupRunID, &r.SpecName, &r.Role, &pid, &start, &pgid, &port,
			&command, &r.State, &health, &reason, &code, &started, &exited, &logPath); err != nil {
			return nil, err
		}
		r.PID, r.StartTime, r.PGID, r.Port = int(pid.Int64), start.Int64, int(pgid.Int64), int(port.Int64)
		_ = json.Unmarshal([]byte(command), &r.Command)
		r.Health, r.ExitReason, r.LogPath = health.String, reason.String, logPath.String
		if code.Valid {
			c := int(code.Int64)
			r.ExitCode = &c
		}
		if started.Valid {
			r.StartedAt = time.UnixMilli(started.Int64)
		}
		if exited.Valid {
			r.ExitedAt = time.UnixMilli(exited.Int64)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// liveRows returns every row the last daemon left unfinished, oldest run
// first. queued rows are included: a launch interrupted by the daemon dying
// has members that never spawned.
func (s *Supervisor) liveRows(ctx context.Context) ([]row, error) {
	rows, err := s.store.Reader().QueryContext(ctx, `SELECT `+rowColumns+` FROM processes p
		WHERE p.state IN ('queued','starting','running','stopping') ORDER BY p.rowid`)
	if err != nil {
		return nil, fmt.Errorf("reading unfinished processes: %w", err)
	}
	return scanRows(rows)
}

// lastRunRows returns the rows of a studio's most recent group run.
func (s *Supervisor) lastRunRows(ctx context.Context, studioID string) ([]row, error) {
	rows, err := s.store.Reader().QueryContext(ctx, `SELECT `+rowColumns+` FROM processes p
		WHERE p.group_run_id = (SELECT group_run_id FROM processes WHERE studio_id = ? ORDER BY rowid DESC LIMIT 1)
		ORDER BY p.rowid`, studioID)
	if err != nil {
		return nil, fmt.Errorf("reading the last run of %s: %w", studioID, err)
	}
	return scanRows(rows)
}
