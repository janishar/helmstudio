package install

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Job states (docs/design/02-data-model.md §4, jobs).
const (
	JobQueued      = "queued"
	JobRunning     = "running"
	JobSucceeded   = "succeeded"
	JobFailed      = "failed"
	JobCancelled   = "cancelled"
	JobInterrupted = "interrupted"
)

// Failure is installations.last_failure and jobs.last_error:
// {phase, step_index, exit_code, log_file_id, message} from 02 §4, plus a
// code a UI can choose a sentence from without parsing the message.
type Failure struct {
	Phase     string `json:"phase"`
	StepIndex *int   `json:"step_index,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	LogFileID string `json:"log_file_id,omitempty"`
	Message   string `json:"message"`
	// Code is one of: clone_failed, tool_missing, step_failed, step_timeout,
	// cancelled, interrupted, auth_required, disk_space, weights_failed,
	// linked_missing, uninstall_failed.
	Code string `json:"code"`
}

func (f *Failure) Error() string { return f.Message }

// StepRun is one executed build step.
type StepRun struct {
	Index      int        `json:"step_index"`
	Name       string     `json:"step_name"`
	Command    string     `json:"command"`
	State      string     `json:"state"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	LogFileID  string     `json:"log_file_id,omitempty"`
	LogPath    string     `json:"-"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Job is long work with progress, cancellation and logs (R40).
type Job struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	StudioID    string     `json:"studio_id,omitempty"`
	State       string     `json:"state"`
	SubjectKind string     `json:"subject_kind,omitempty"`
	SubjectID   string     `json:"subject_id,omitempty"`
	ProgressNum int64      `json:"progress_num"`
	ProgressDen int64      `json:"progress_den"`
	LastError   *Failure   `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	// Steps are an install job's build steps, in order.
	Steps []StepRun `json:"steps,omitempty"`
	// Warnings are host shortfalls that did not block the install (R5).
	Warnings []string `json:"warnings,omitempty"`
}

// ErrNoJob means no job has the id.
var ErrNoJob = errors.New("no such job")

func msPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

func (in *Installer) insertJob(ctx context.Context, tx *sql.Tx, kind, studioID, subjectKind, subjectID string) (string, error) {
	id := in.newID()
	_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, subject_kind, subject_id, created_at, started_at)
		VALUES (?, ?, NULLIF(?, ''), 'running', NULLIF(?, ''), NULLIF(?, ''), ?, ?)`, id, kind, studioID, subjectKind, subjectID, in.nowMs(), in.nowMs())
	return id, err
}

func (in *Installer) finishJob(ctx context.Context, id, state string, failure *Failure) {
	var lastErr any
	if failure != nil {
		b, _ := json.Marshal(failure)
		lastErr = string(b)
	}
	err := in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, last_error = ?, finished_at = ? WHERE id = ?`, state, lastErr, in.nowMs(), id)
		return err
	})
	if err != nil {
		in.cfg.Logf("install: recording job %s as %s: %v", id, state, err)
	}
}

func (in *Installer) setProgress(ctx context.Context, id string, num, den int64) {
	_ = in.cfg.Store.Update(context.WithoutCancel(ctx), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET progress_num = ?, progress_den = ? WHERE id = ?`, num, den, id)
		return err
	})
}

// Job returns one job. A download's progress is read from the partial files
// on disk, never from a stored counter.
func (in *Installer) Job(ctx context.Context, id string) (Job, error) {
	r := in.cfg.Store.Reader()
	var j Job
	var studio, subjectKind, subjectID, lastErr sql.NullString
	var num, den, started, finished sql.NullInt64
	var created int64
	err := r.QueryRowContext(ctx, `SELECT id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, last_error, created_at, started_at, finished_at
		FROM jobs WHERE id = ?`, id).Scan(&j.ID, &j.Kind, &studio, &j.State, &subjectKind, &subjectID, &num, &den, &lastErr, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNoJob)
	}
	if err != nil {
		return Job{}, err
	}
	j.StudioID, j.SubjectKind, j.SubjectID = studio.String, subjectKind.String, subjectID.String
	j.ProgressNum, j.ProgressDen = num.Int64, den.Int64
	j.CreatedAt, j.StartedAt, j.FinishedAt = time.UnixMilli(created), msPtr(started), msPtr(finished)
	if lastErr.Valid {
		j.LastError = new(Failure)
		_ = json.Unmarshal([]byte(lastErr.String), j.LastError)
	}
	if j.Kind == "download" && j.SubjectID != "" {
		if done, total, err := in.cfg.Weights.ArtifactProgress(ctx, j.SubjectID); err == nil {
			j.ProgressNum, j.ProgressDen = done, total
		}
	}
	if j.Kind == "install" {
		if j.Steps, err = in.stepRuns(ctx, j.ID); err != nil {
			return Job{}, err
		}
	}
	in.mu.Lock()
	if a := in.active[j.ID]; a != nil {
		j.Warnings = a.warnings
	}
	in.mu.Unlock()
	return j, nil
}

// Jobs lists the most recent jobs, newest first, optionally for one studio.
func (in *Installer) Jobs(ctx context.Context, studioID string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := in.cfg.Store.Reader().QueryContext(ctx, `SELECT id FROM jobs WHERE (?1 = '' OR studio_id = ?1) ORDER BY created_at DESC, id DESC LIMIT ?2`, studioID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Job{}
	for _, id := range ids {
		j, err := in.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func (in *Installer) stepRuns(ctx context.Context, jobID string) ([]StepRun, error) {
	rows, err := in.cfg.Store.Reader().QueryContext(ctx, `SELECT s.step_index, s.step_name, s.command, s.state, s.exit_code, COALESCE(s.log_file_id,''), COALESCE(l.path,''), s.started_at, s.finished_at
		FROM step_runs s LEFT JOIN log_files l ON l.id = s.log_file_id WHERE s.job_id = ? ORDER BY s.step_index`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepRun
	for rows.Next() {
		var s StepRun
		var code, started, finished sql.NullInt64
		if err := rows.Scan(&s.Index, &s.Name, &s.Command, &s.State, &code, &s.LogFileID, &s.LogPath, &started, &finished); err != nil {
			return nil, err
		}
		if code.Valid {
			c := int(code.Int64)
			s.ExitCode = &c
		}
		s.StartedAt, s.FinishedAt = msPtr(started), msPtr(finished)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Cancel stops a running job (R13): a running build step's process group is
// killed, the phase is recorded failed, and completed work is kept. It
// returns once the job has stopped.
func (in *Installer) Cancel(ctx context.Context, id string) error {
	in.mu.Lock()
	a := in.active[id]
	in.mu.Unlock()
	if a == nil {
		if _, err := in.Job(ctx, id); err != nil {
			return err
		}
		return fmt.Errorf("job %s is not running: %w", id, ErrNotRunning)
	}
	a.cancel(errCancelledByUser)
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrNotRunning refuses cancelling a job that has finished.
var ErrNotRunning = errors.New("not running")

var (
	errCancelledByUser = errors.New("cancelled")
	errShutdown        = errors.New("helmstudio is shutting down")
)

// active is a job this daemon is running.
type active struct {
	id       string
	studioID string
	cancel   context.CancelCauseFunc
	done     chan struct{}
	warnings []string
}

// jobEnd maps how a job's context ended to its final state.
func jobEnd(ctx context.Context) (string, string) {
	switch context.Cause(ctx) {
	case errShutdown:
		return JobInterrupted, "interrupted"
	default:
		return JobCancelled, "cancelled"
	}
}
