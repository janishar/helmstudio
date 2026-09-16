package studioapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/export"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Export (R46–R48, M8 Q13–Q17). An export is a Job, so progress, cancellation
// and the log are the ones that already exist. It writes where nothing looks
// for it, and becomes an asset only once ffmpeg has finished and the file has
// been probed against the target.

// exports is what this process is rendering, so a cancel reaches the ffmpeg it
// started. A render this process did not start is stopped by its recorded
// identity instead (M2's rule).
type exports struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func (e *exports) add(job string, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running == nil {
		e.running = map[string]context.CancelFunc{}
	}
	e.running[job] = cancel
}

func (e *exports) done(job string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.running, job)
}

func (e *exports) stop(job string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	cancel, ok := e.running[job]
	if ok {
		cancel()
	}
	return ok
}

// ffmpeg is the tool, found once and remembered. Without one, the operations
// that need it answer 501 and everything else still works (M8 Q5).
func (s *Service) ffmpeg() (*export.Tool, error) {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()
	if s.tool != nil {
		return s.tool, nil
	}
	find := s.cfg.FindFFmpeg
	if find == nil {
		find = export.Find
	}
	t, err := find()
	if err != nil {
		return nil, toolMissing(err)
	}
	s.tool = t
	return t, nil
}

func toolMissing(err error) *helm.Error {
	e := apiError(http.StatusNotImplemented, "unsupported", "%v", err)
	e.Details = map[string]any{"code": "tool_missing", "tool": "ffmpeg"}
	return e
}

// documentFiles is every clip's file and what ffprobe found in it. An asset
// the caller may no longer read, or one whose file is gone, refuses the export
// rather than rendering a sequence that is not the one on screen.
func (s *Service) documentFiles(ctx context.Context, p Principal, tool *export.Tool, r *timelineRow) (timeline.Sources, timeline.Probes, error) {
	ids := timeline.Assets(r.tracks)
	files := timeline.Sources{}
	probes := timeline.Probes{}
	var faults []timeline.Fault
	for _, id := range ids {
		a, err := s.readAsset(ctx, s.st.Reader(), p, id)
		if err != nil {
			faults = append(faults, timeline.Fault{Clip: -1, Code: "asset_not_found", Message: fmt.Sprintf("no asset %s you can use", id)})
			continue
		}
		path := s.media.BlobPath(a.blobPath)
		if _, err := os.Stat(path); err != nil {
			faults = append(faults, timeline.Fault{Clip: -1, Code: "asset_missing", Message: fmt.Sprintf("asset %s is recorded but its file is not where helmstudio left it", id)})
			continue
		}
		probe, err := tool.Probe(ctx, path)
		if err != nil {
			faults = append(faults, timeline.Fault{Clip: -1, Code: "not_probed", Message: fmt.Sprintf("asset %s could not be read: %v", id, err)})
			continue
		}
		files[id], probes[id] = path, probe
	}
	if len(faults) > 0 {
		return nil, nil, invalidTimeline(&timeline.Invalid{Faults: faults})
	}
	if err := timeline.CheckAgainstProbes(r.target, r.tracks, probes); err != nil {
		return nil, nil, invalidTimeline(err)
	}
	return files, probes, nil
}

func presetOf(p *helm.ExportPreset) helm.ExportPreset {
	if p == nil {
		return helm.ExportPresetH264
	}
	return *p
}

func (s *Service) TimelinePlan(ctx context.Context, id string, params helm.TimelinePlanParams) (*helm.ExportPlan, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.readTimeline(ctx, s.st.Reader(), p, id)
	if err != nil {
		return nil, err
	}
	tool, err := s.ffmpeg()
	if err != nil {
		return nil, err
	}
	_, probes, err := s.documentFiles(ctx, p, tool, r)
	if err != nil {
		return nil, err
	}
	mode, reasons := timeline.Plan(r.target, r.tracks, probes)
	if reasons == nil {
		reasons = []helm.ExportReason{}
	}
	return &helm.ExportPlan{
		Mode: mode, Preset: presetOf(params.Preset), Target: r.target,
		DurationS: timeline.Duration(r.target, r.tracks),
		Frames:    timeline.Frames(r.target, r.tracks),
		Reasons:   reasons,
	}, nil
}

func (s *Service) TimelineExport(ctx context.Context, id string, body *helm.ExportRequest) (*helm.Job, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.readTimeline(ctx, s.st.Reader(), p, id)
	if err != nil {
		return nil, err
	}
	// What the document itself settles is settled before ffmpeg is looked for,
	// so a sequence with nothing to export answers the same on every machine.
	if i := timeline.TrackNamed(r.tracks, "V1"); i < 0 || len(r.tracks[i].Clips) == 0 {
		return nil, invalidTimeline(&timeline.Invalid{Faults: []timeline.Fault{{
			Clip: -1, Code: "no_video", Message: "the sequence has no video clip, so there is nothing to export"}}})
	}
	tool, err := s.ffmpeg()
	if err != nil {
		return nil, err
	}
	files, probes, err := s.documentFiles(ctx, p, tool, r)
	if err != nil {
		return nil, err
	}
	mode, _ := timeline.Plan(r.target, r.tracks, probes)

	var job *helm.Job
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var running int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE kind = 'export' AND subject_kind = 'timeline' AND subject_id = ?
			AND state IN ('queued','running')`, id).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return conflict("already_exporting", "sequence %s is being exported already; wait for that job or cancel it", id)
		}
		jobID, now := s.newID(), ms(s.now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, kind, studio_id, state, subject_kind, subject_id, progress_num, progress_den, created_at)
			VALUES (?, 'export', ?, 'queued', 'timeline', ?, 0, ?, ?)`,
			jobID, p.StudioID, id, timeline.Frames(r.target, r.tracks), now); err != nil {
			return err
		}
		var err error
		job, err = s.readJob(ctx, tx, jobID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	go s.runExport(p, job.ID, r, files, probes, mode, body.Preset, tool)
	return job, nil
}

// runExport renders one sequence. It is the daemon's own work, not the
// request's, so a client that hangs up does not stop it.
func (s *Service) runExport(p Principal, jobID string, r *timelineRow, files timeline.Sources, probes timeline.Probes, mode string, preset helm.ExportPreset, tool *export.Tool) {
	ctx, cancel := context.WithCancel(context.Background())
	s.exports.add(jobID, cancel)
	defer func() {
		cancel()
		s.exports.done(jobID)
	}()

	work := filepath.Join(s.media.Assets, "tmp", "export-"+jobID)
	outPath := filepath.Join(work, "export.mp4")
	listPath := filepath.Join(work, "concat.txt")
	fail := func(code string, err error) {
		s.cfg.Logf("export: sequence %s, job %s: %v", r.id, jobID, err)
		s.finishExport(jobID, helm.JobStateFailed, &helm.JobError{Code: code, Message: err.Error()})
		_ = os.RemoveAll(work)
	}

	if err := os.MkdirAll(work, 0o700); err != nil {
		fail("export_failed", fmt.Errorf("making room for the export: %w", err))
		return
	}
	render, err := timeline.Build(r.target, r.tracks, probes, files, mode, listPath)
	if err != nil {
		fail("export_failed", err)
		return
	}
	if render.ConcatList != "" {
		if err := os.WriteFile(listPath, []byte(render.ConcatList), 0o600); err != nil {
			fail("export_failed", fmt.Errorf("writing the clip list: %w", err))
			return
		}
	}
	args, err := render.EncodeArgs(r.target, preset, outPath)
	if err != nil {
		fail("export_failed", err)
		return
	}
	logFile, logRel, err := s.openExportLog(p.StudioID, jobID)
	if err != nil {
		fail("export_failed", err)
		return
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "%s %s\n", tool.FFmpeg, strings.Join(args, " "))

	if err := s.startExport(jobID, work); err != nil {
		fail("export_failed", err)
		return
	}
	var lastProgress time.Time
	run := export.Run{
		Tool: tool,
		Args: args,
		Log:  logFile,
		Started: func(id platform.ProcessIdentity) error {
			return s.recordExportProcess(jobID, id, outPath)
		},
		Progress: func(frames int64) {
			if time.Since(lastProgress) < 500*time.Millisecond {
				return
			}
			lastProgress = time.Now()
			s.progressExport(jobID, frames)
		},
	}
	switch err := run.Do(ctx); {
	case errors.Is(err, export.ErrCancelled):
		s.cfg.Logf("export: sequence %s, job %s was stopped", r.id, jobID)
		s.finishExport(jobID, helm.JobStateCancelled, &helm.JobError{Code: "cancelled", Message: "the export was stopped, and the file it was writing was removed"})
		_ = os.RemoveAll(work)
		return
	case err != nil:
		fmt.Fprintf(logFile, "==> %v\n", err)
		fail("export_failed", fmt.Errorf("%v; the export's log has what ffmpeg said", err))
		return
	}

	// Nothing becomes an asset before it has been read back and found to be
	// the target it declared (M8 Q16).
	made, err := tool.Probe(ctx, outPath)
	if err != nil {
		fail("export_failed", fmt.Errorf("reading back the export: %w", err))
		return
	}
	if err := checkRendered(r.target, r.tracks, made); err != nil {
		fail("export_failed", err)
		return
	}
	item, err := s.adoptExport(ctx, p, r, outPath, made, mode, preset, logRel)
	if err != nil {
		fail("export_failed", err)
		return
	}
	_ = os.RemoveAll(work)
	s.finishExport(jobID, helm.JobStateSucceeded, nil)
	s.publishItem("added", *item)
}

// checkRendered holds the file to the sequence it was made from.
func checkRendered(target helm.TimelineTarget, tracks []helm.TimelineTrack, made timeline.Stream) error {
	rate, _ := timeline.RateOf(target.FPS)
	want := timeline.Duration(target, tracks)
	switch {
	case made.Width != target.Width || made.Height != target.Height:
		return fmt.Errorf("the export came out %dx%d and the target is %dx%d", made.Width, made.Height, target.Width, target.Height)
	case !made.FrameRate.Equal(rate):
		return fmt.Errorf("the export runs at %s and the target at %s", made.FrameRate, rate)
	case made.SampleRate != 0 && made.SampleRate != target.SampleRate:
		return fmt.Errorf("the export's sound is %d Hz and the target is %d Hz", made.SampleRate, target.SampleRate)
	}
	if d := made.Duration - want; d > rate.Seconds(1) || d < -rate.Seconds(1) {
		return fmt.Errorf("the export is %.3fs and the sequence is %.3fs", made.Duration, want)
	}
	return nil
}

// adoptExport takes the finished file into the asset store and records it as a
// gallery item carrying its sequence and one clip input per asset (R48).
func (s *Service) adoptExport(ctx context.Context, p Principal, r *timelineRow, path string, made timeline.Stream, mode string, preset helm.ExportPreset, logRel string) (*helm.Item, error) {
	staged, err := s.media.Link(path)
	if err != nil {
		return nil, fmt.Errorf("taking the export into the asset store: %w", err)
	}
	duration := timeline.Duration(r.target, r.tracks)
	fps := r.target.FPS
	width, height := r.target.Width, r.target.Height
	asset, _, err := s.ingest(ctx, p, staged, helm.AssetKindVideo, exportFileName(r.name), "video/mp4",
		hints{width: &width, height: &height, duration: &duration, fps: &fps})
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"preset":      string(preset),
		"mode":        mode,
		"duration_s":  duration,
		"width":       r.target.Width,
		"height":      r.target.Height,
		"fps":         r.target.FPS,
		"sample_rate": r.target.SampleRate,
		"clips":       timeline.ClipCount(r.tracks),
	}
	var item *helm.Item
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		encoded, err := encodeDoc(params)
		if err != nil {
			return err
		}
		id, now := s.newID(), ms(s.now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO items (id, studio_id, session_id, kind, asset_id, timeline_id, title, params, created_at)
			VALUES (?, ?, NULL, 'video', ?, ?, ?, ?, ?)`, id, p.StudioID, asset.ID, r.id, r.name, encoded, now); err != nil {
			return err
		}
		refs := []any{asset.ID}
		for _, clipAsset := range timeline.Assets(r.tracks) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_inputs (item_id, asset_id, role) VALUES (?, ?, 'clip')`, id, clipAsset); err != nil {
				return err
			}
			refs = append(refs, clipAsset)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (item_id, title, prompt) VALUES (?, ?, '')`, id, r.name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET first_referenced_at = ? WHERE first_referenced_at IS NULL AND id IN (?`+strings.Repeat(",?", len(refs)-1)+`)`,
			append([]any{now}, refs...)...); err != nil {
			return err
		}
		item, err = s.loadItem(ctx, tx, p, id, true)
		return err
	})
	return item, err
}

// exportFileName is what the library calls a sequence, which is what a person
// sees in Finder.
func exportFileName(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == ' ', r == '_':
			return r
		}
		return '-'
	}, name)
	safe = strings.TrimSpace(safe)
	if safe == "" {
		safe = "sequence"
	}
	return safe + ".mp4"
}

func (s *Service) openExportLog(studioID, jobID string) (*os.File, string, error) {
	rel := filepath.ToSlash(filepath.Join("studios", studioID, "export-"+jobID+".log"))
	err := s.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO log_files (id, path, kind, owner_kind, owner_id, studio_id, created_at)
			VALUES (?, ?, 'export', 'job', ?, ?, ?) ON CONFLICT (path) DO NOTHING`, s.newID(), rel, jobID, studioID, ms(s.now()))
		return err
	})
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(s.cfg.Paths.Logs())
	if err != nil {
		return nil, "", fmt.Errorf("opening the logs root: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(rel)), 0o700); err != nil {
		return nil, "", fmt.Errorf("making the export's log directory: %w", err)
	}
	f, err := root.OpenFile(filepath.FromSlash(rel), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("opening the export's log: %w", err)
	}
	return f, rel, nil
}

func (s *Service) startExport(jobID, work string) error {
	return s.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', started_at = COALESCE(started_at, ?), work_path = ? WHERE id = ?`,
			ms(s.now()), work, jobID)
		return err
	})
}

// recordExportProcess writes what the next daemon needs to stop this render if
// this one is killed. A render whose identity cannot be written is killed at
// once rather than left for nobody to find (M3 review #1).
func (s *Service) recordExportProcess(jobID string, id platform.ProcessIdentity, outPath string) error {
	return s.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		var start any
		if id.StartTime != 0 {
			start = id.StartTime
		}
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET pid = ?, pid_start_time = ?, pgid = ? WHERE id = ?`, id.PID, start, id.PGID, jobID)
		return err
	})
}

func (s *Service) progressExport(jobID string, frames int64) {
	_ = s.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET progress_num = ? WHERE id = ? AND state = 'running'`, frames, jobID)
		return err
	})
}

func (s *Service) finishExport(jobID string, state helm.JobState, jobErr *helm.JobError) {
	_ = s.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		var encoded any
		if jobErr != nil {
			b, _ := json.Marshal(jobErr)
			encoded = string(b)
		}
		num := any(nil)
		if state == helm.JobStateSucceeded {
			num = "progress_den"
		}
		q := `UPDATE jobs SET state = ?, finished_at = ?, last_error = ?, pid = NULL, pid_start_time = NULL, pgid = NULL, work_path = NULL WHERE id = ?`
		if num != nil {
			q = `UPDATE jobs SET state = ?, finished_at = ?, last_error = ?, progress_num = progress_den, pid = NULL, pid_start_time = NULL, pgid = NULL, work_path = NULL WHERE id = ?`
		}
		_, err := tx.ExecContext(ctx, q, string(state), ms(s.now()), encoded, jobID)
		return err
	})
}

func (s *Service) TimelineExports(ctx context.Context, id string, params helm.TimelineExportsParams) (*helm.JobPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.readTimeline(ctx, s.st.Reader(), p, id); err != nil {
		return nil, err
	}
	digest := filterDigest("timeline-exports", id)
	after, err := decodeCursor(params.Cursor, digest, 2)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + jobCols + ` FROM jobs WHERE kind = 'export' AND subject_kind = 'timeline' AND subject_id = ?`
	args := []any{id}
	if after != nil {
		at, ok1 := cursorInt(after[0])
		jobID, ok2 := cursorString(after[1])
		if !ok1 || !ok2 {
			return nil, badCursor()
		}
		q += ` AND (created_at, id) < (?, ?)`
		args = append(args, at, jobID)
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
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
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = encodeCursor(digest, ms(last.CreatedAt), last.ID)
			break
		}
		page.Items = append(page.Items, *j)
	}
	return page, rows.Err()
}

// TimelineCancelExport stops a render and leaves nothing behind. It reaches a
// render this process started through its context, and one a previous daemon
// started through its recorded identity.
func (s *Service) TimelineCancelExport(ctx context.Context, id, job string) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	if _, err := s.readTimeline(ctx, s.st.Reader(), p, id); err != nil {
		return err
	}
	var identity platform.ProcessIdentity
	var work string
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var state, subjectID string
		var pid, start, pgid sql.NullInt64
		var workPath sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT state, COALESCE(subject_id, ''), pid, pid_start_time, pgid, work_path FROM jobs
			WHERE id = ? AND kind = 'export'`, job).Scan(&state, &subjectID, &pid, &start, &pgid, &workPath)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && subjectID != id) {
			return notFound("sequence %s has no export %s", id, job)
		}
		if err != nil {
			return err
		}
		if finished(helm.JobState(state)) {
			return conflict("job_finished", "export %s is already %s", job, state)
		}
		identity = platform.ProcessIdentity{PID: int(pid.Int64), StartTime: start.Int64, PGID: int(pgid.Int64)}
		work = workPath.String
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET cancel_requested_at = COALESCE(cancel_requested_at, ?) WHERE id = ?`, ms(s.now()), job)
		return err
	})
	if err != nil {
		return err
	}
	if s.exports.stop(job) {
		return nil // its own goroutine ends the job and removes the file
	}
	// A render this process did not start: stop it by identity, then finish the
	// job here, since nobody else will.
	export.StopSurvivor(identity, export.DefaultGrace, s.cfg.Logf)
	if work != "" {
		_ = os.RemoveAll(work)
	}
	s.finishExport(job, helm.JobStateCancelled, &helm.JobError{Code: "cancelled", Message: "the export was stopped, and the file it was writing was removed"})
	return nil
}

// SweepExports stops renders a killed daemon left running and clears what they
// were writing, before the API serves anything. A job with no verifiable
// identity is never signalled; it is recorded interrupted and its file removed
// (M3 review #1's rule).
func (s *Service) SweepExports(ctx context.Context) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT id, pid, pid_start_time, pgid, work_path FROM jobs
		WHERE kind = 'export' AND state IN ('queued','running')`)
	if err != nil {
		s.cfg.Logf("export: reading the exports left running: %v", err)
		return
	}
	type left struct {
		id    string
		ident platform.ProcessIdentity
		work  string
	}
	var found []left
	for rows.Next() {
		var l left
		var pid, start, pgid sql.NullInt64
		var work sql.NullString
		if err := rows.Scan(&l.id, &pid, &start, &pgid, &work); err != nil {
			continue
		}
		l.ident = platform.ProcessIdentity{PID: int(pid.Int64), StartTime: start.Int64, PGID: int(pgid.Int64)}
		l.work = work.String
		found = append(found, l)
	}
	rows.Close()
	for _, l := range found {
		if l.ident.PID > 0 {
			export.StopSurvivor(l.ident, export.DefaultGrace, s.cfg.Logf)
		}
		if l.work != "" {
			_ = os.RemoveAll(l.work)
		}
		s.finishExport(l.id, helm.JobStateInterrupted, &helm.JobError{
			Code:    "interrupted",
			Message: "helmstudio stopped while this export was rendering; nothing was kept. Export the sequence again",
		})
		s.cfg.Logf("export: job %s was interrupted by a helmstudio that stopped; its partial file was removed", l.id)
	}
}
