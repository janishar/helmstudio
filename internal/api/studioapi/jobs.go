package studioapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Jobs (R40, Q24, first review #2): a studio sees its own studio's jobs of
// every kind and changes only the task jobs it reported. The launcher's view
// is internal/api's /launcher/jobs.

const jobCols = `id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, last_error, created_at, started_at, finished_at, cancel_requested_at`

func scanJob(sc interface{ Scan(...any) error }) (*helm.Job, error) {
	var j helm.Job
	var kind, state string
	var studio, subjectKind, subjectID, lastErr sql.NullString
	var num, den sql.NullInt64
	var created int64
	var started, finished, cancel sql.NullInt64
	if err := sc.Scan(&j.ID, &kind, &studio, &state, &subjectKind, &subjectID, &num, &den, &lastErr, &created, &started, &finished, &cancel); err != nil {
		return nil, err
	}
	j.Kind, j.State = helm.JobKind(kind), helm.JobState(state)
	if studio.Valid {
		j.StudioID = &studio.String
	}
	if subjectKind.Valid {
		j.SubjectKind = &subjectKind.String
	}
	if subjectID.Valid {
		j.SubjectID = &subjectID.String
	}
	j.ProgressNum, j.ProgressDen = num.Int64, den.Int64
	if lastErr.Valid && lastErr.String != "" {
		var e helm.JobError
		if json.Unmarshal([]byte(lastErr.String), &e) == nil {
			j.LastError = &e
		}
	}
	j.CreatedAt = fromMS(created)
	j.StartedAt, j.FinishedAt, j.CancelRequestedAt = fromNullMS(started), fromNullMS(finished), fromNullMS(cancel)
	return &j, nil
}

// readJob reads a job with its build steps.
func (s *Service) readJob(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string, steps bool) (*helm.Job, error) {
	j, err := scanJob(q.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no job %s", id)
	}
	if err != nil || !steps {
		return j, err
	}
	rows, err := q.QueryContext(ctx, `SELECT step_index, step_name, command, state, exit_code, log_file_id, started_at, finished_at FROM step_runs WHERE job_id = ? ORDER BY step_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st helm.StepRun
		var state string
		var exit sql.NullInt64
		var logID sql.NullString
		var started, finished sql.NullInt64
		if err := rows.Scan(&st.StepIndex, &st.StepName, &st.Command, &state, &exit, &logID, &started, &finished); err != nil {
			return nil, err
		}
		st.State = state
		if exit.Valid {
			st.ExitCode = &exit.Int64
		}
		if logID.Valid {
			st.LogFileID = &logID.String
		}
		st.StartedAt, st.FinishedAt = fromNullMS(started), fromNullMS(finished)
		j.Steps = append(j.Steps, st)
	}
	return j, rows.Err()
}

// ownJob reads a job of the caller's studio; any other is not found.
func (s *Service) ownJob(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, p Principal, id string) (*helm.Job, error) {
	j, err := s.readJob(ctx, q, id, true)
	if err != nil {
		return nil, err
	}
	if j.StudioID == nil || *j.StudioID != p.StudioID {
		return nil, notFound("no job %s", id)
	}
	return j, nil
}

func finished(state helm.JobState) bool {
	switch state {
	case helm.JobStateQueued, helm.JobStateRunning:
		return false
	}
	return true
}

func (s *Service) JobsList(ctx context.Context, params helm.JobsListParams) (*helm.JobPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("jobs", p.StudioID)
	after, err := decodeCursor(params.Cursor, digest, 2)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + jobCols + ` FROM jobs WHERE studio_id = ?`
	args := []any{p.StudioID}
	if after != nil {
		at, ok1 := cursorInt(after[0])
		id, ok2 := cursorString(after[1])
		if !ok1 || !ok2 {
			return nil, badCursor()
		}
		q += ` AND (created_at, id) < (?, ?)`
		args = append(args, at, id)
	}
	n := pageLimit(params.Limit)
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, n+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &helm.JobPage{Items: []helm.Job{}}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == n {
			last := page.Items[n-1]
			page.NextCursor = encodeCursor(digest, ms(last.CreatedAt), last.ID)
			break
		}
		page.Items = append(page.Items, *j)
	}
	return page, rows.Err()
}

func (s *Service) JobsCreate(ctx context.Context, body *helm.TaskCreate) (*helm.Job, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	state := "running"
	if body.State != nil {
		state = *body.State
	}
	num, den := int64(0), int64(0)
	if body.ProgressNum != nil {
		num = *body.ProgressNum
	}
	if body.ProgressDen != nil {
		den = *body.ProgressDen
	}
	if den > 0 && num > den {
		return nil, badRequest("progress_num must not exceed progress_den")
	}
	var out *helm.Job
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		id, now := s.newID(), ms(s.now())
		var started any
		if state == "running" {
			started = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, created_at, started_at)
			VALUES (?, 'task', ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.StudioID, state, body.SubjectKind, body.SubjectID, num, den, now, started); err != nil {
			return err
		}
		var err error
		out, err = s.readJob(ctx, tx, id, false)
		return err
	})
	return out, err
}

// ownTask reads a task job the caller's studio reported.
func (s *Service) ownTask(ctx context.Context, tx *sql.Tx, p Principal, id string) (*helm.Job, error) {
	j, err := s.ownJob(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	if j.Kind != helm.JobKindTask {
		return nil, forbidden("not_a_task", "job %s is a %s job; a studio changes only the task jobs it reports, and the launcher cancels the rest", id, j.Kind)
	}
	return j, nil
}

func (s *Service) JobsGet(ctx context.Context, id string) (*helm.Job, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.ownJob(ctx, s.st.Reader(), p, id)
}

func (s *Service) JobsUpdate(ctx context.Context, id string, body map[string]any) (*helm.Job, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var out *helm.Job
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		j, err := s.ownTask(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if finished(j.State) {
			return conflict("job_finished", "job %s is already %s", id, j.State)
		}
		state, num, den := j.State, j.ProgressNum, j.ProgressDen
		if v, ok := body["state"]; ok {
			st, _ := v.(string)
			switch helm.JobState(st) {
			case helm.JobStateRunning, helm.JobStateSucceeded, helm.JobStateFailed, helm.JobStateCancelled:
				state = helm.JobState(st)
			default:
				return badRequest("state must be running, succeeded, failed or cancelled")
			}
		}
		intField := func(name string, cur int64) (int64, error) {
			v, ok := body[name]
			if !ok {
				return cur, nil
			}
			n, isInt := v.(int64)
			if !isInt || n < 0 {
				return 0, badRequest("%s must be a non-negative integer", name)
			}
			return n, nil
		}
		if num, err = intField("progress_num", num); err != nil {
			return err
		}
		if den, err = intField("progress_den", den); err != nil {
			return err
		}
		if den > 0 && num > den {
			return badRequest("progress_num must not exceed progress_den")
		}
		lastErr := sql.NullString{}
		if j.LastError != nil {
			b, _ := json.Marshal(j.LastError)
			lastErr = sql.NullString{String: string(b), Valid: true}
		}
		if v, ok := body["last_error"]; ok {
			switch t := v.(type) {
			case nil:
				lastErr = sql.NullString{}
			case map[string]any:
				cur := map[string]any{}
				if lastErr.Valid {
					_ = json.Unmarshal([]byte(lastErr.String), &cur)
				}
				merged := mergePatch(cur, t)
				code, okc := merged["code"].(string)
				msg, okm := merged["message"].(string)
				if !okc || !okm || len(code) > 64 || len(msg) > 4000 || len(merged) != 2 {
					return badRequest("last_error is {code, message}: strings of at most 64 and 4000 characters")
				}
				b, _ := json.Marshal(merged)
				lastErr = sql.NullString{String: string(b), Valid: true}
			default:
				return badRequest("last_error must be an object or null")
			}
		}
		now := ms(s.now())
		var startedSet, finishedSet any
		if j.StartedAt == nil && state != helm.JobStateQueued {
			startedSet = now
		}
		if finished(state) {
			finishedSet = now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, progress_num = ?, progress_den = ?, last_error = ?,
			started_at = COALESCE(started_at, ?), finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
			string(state), num, den, lastErr, startedSet, finishedSet, id); err != nil {
			return err
		}
		out, err = s.readJob(ctx, tx, id, false)
		return err
	})
	return out, err
}

func (s *Service) JobsCancel(ctx context.Context, id string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		j, err := s.ownTask(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if finished(j.State) {
			return conflict("job_finished", "job %s is already %s", id, j.State)
		}
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET cancel_requested_at = COALESCE(cancel_requested_at, ?) WHERE id = ?`, ms(s.now()), id)
		return err
	})
}

// taskLogRel is a task job's log file, relative to the logs root.
func taskLogRel(studioID, jobID string) string {
	return filepath.ToSlash(filepath.Join("studios", studioID, "task-"+jobID+".log"))
}

func (s *Service) JobsAppendLog(ctx context.Context, id string, body *helm.LogAppend) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	rel := taskLogRel(p.StudioID, id)
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		j, err := s.ownTask(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if finished(j.State) {
			return conflict("job_finished", "job %s is already %s", id, j.State)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at) VALUES (?, ?, 'task', 'job', ?, ?, ?) ON CONFLICT (path) DO NOTHING`,
			s.newID(), rel, id, p.StudioID, ms(s.now())); err != nil {
			return err
		}
		root, err := os.OpenRoot(s.cfg.Paths.Logs())
		if err != nil {
			return fmt.Errorf("opening the logs root: %w", err)
		}
		defer root.Close()
		if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(rel)), 0o700); err != nil {
			return fmt.Errorf("creating the task log directory: %w", err)
		}
		f, err := root.OpenFile(filepath.FromSlash(rel), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("opening the task log: %w", err)
		}
		defer f.Close()
		w := bufio.NewWriter(f)
		for _, line := range body.Lines {
			w.WriteString(strings.ReplaceAll(line, "\n", " ") + "\n")
		}
		return w.Flush()
	})
}

// jobLogs lists a job's log files in order: its steps', or its task log.
func (s *Service) jobLogs(ctx context.Context, j *helm.Job) ([]helm.JobLogStep, []string, error) {
	if j.Kind == helm.JobKindTask {
		var path string
		err := s.st.Reader().QueryRowContext(ctx, `SELECT path FROM log_files WHERE owner_kind = 'job' AND owner_id = ?`, j.ID).Scan(&path)
		if errors.Is(err, sql.ErrNoRows) {
			return []helm.JobLogStep{{StepIndex: -1}}, []string{""}, nil
		}
		return []helm.JobLogStep{{StepIndex: -1}}, []string{path}, err
	}
	var steps []helm.JobLogStep
	var paths []string
	for _, st := range j.Steps {
		path := ""
		if st.LogFileID != nil {
			_ = s.st.Reader().QueryRowContext(ctx, `SELECT path FROM log_files WHERE id = ?`, *st.LogFileID).Scan(&path)
		}
		steps = append(steps, helm.JobLogStep{StepIndex: st.StepIndex, StepName: st.StepName, Command: st.Command})
		paths = append(paths, path)
	}
	return steps, paths, nil
}

func (s *Service) JobsLogs(ctx context.Context, w http.ResponseWriter, r *http.Request, id string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	if _, err := s.ownJob(ctx, s.st.Reader(), p, id); err != nil {
		return err
	}
	return StreamJobLog(ctx, w, s.cfg.Paths.Logs(), func(ctx context.Context) (*helm.Job, []helm.JobLogStep, []string, error) {
		j, err := s.readJob(ctx, s.st.Reader(), id, true)
		if err != nil {
			return nil, nil, nil, err
		}
		steps, paths, err := s.jobLogs(ctx, j)
		return j, steps, paths, err
	})
}

// StreamJobLog writes a job's log as server-sent events, from the start, then
// live until the job has finished and every line is sent. The launcher's
// /launcher/jobs/{id}/logs uses it too.
func StreamJobLog(ctx context.Context, w http.ResponseWriter, logsRoot string, read func(context.Context) (*helm.Job, []helm.JobLogStep, []string, error)) error {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	send := func(name string, v any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, mustJSON(v))
		return err == nil
	}
	type cur struct {
		offset  int64
		partial string
		started bool
	}
	curs := map[int]*cur{}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		j, steps, paths, err := read(ctx)
		if err != nil {
			return nil
		}
		done := finished(j.State)
		for i, st := range steps {
			c := curs[i]
			if c == nil {
				c = &cur{}
				curs[i] = c
			}
			if paths[i] == "" || !filepath.IsLocal(filepath.FromSlash(paths[i])) {
				continue
			}
			if !c.started && st.StepIndex >= 0 {
				if !send("step", st) {
					return nil
				}
			}
			c.started = true
			f, err := os.Open(filepath.Join(logsRoot, filepath.FromSlash(paths[i])))
			if err != nil {
				continue
			}
			_, _ = f.Seek(c.offset, io.SeekStart)
			br := bufio.NewReaderSize(f, 64<<10)
			for {
				chunk, err := br.ReadString('\n')
				c.offset += int64(len(chunk))
				if strings.HasSuffix(chunk, "\n") {
					line := helm.JobLogLine{Text: c.partial + strings.TrimSuffix(chunk, "\n")}
					if st.StepIndex >= 0 {
						idx := st.StepIndex
						line.StepIndex = &idx
					}
					if !send("line", line) {
						f.Close()
						return nil
					}
					c.partial = ""
				} else {
					c.partial += chunk
				}
				if err != nil {
					break
				}
			}
			f.Close()
		}
		if done {
			send("end", helm.JobLogEnd{State: j.State, LastError: j.LastError})
			_ = rc.Flush()
			return nil
		}
		if rc.Flush() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
