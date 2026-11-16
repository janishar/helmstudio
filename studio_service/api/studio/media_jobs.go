package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// Media jobs (contracts/README.md "Timeline: export", "Media jobs", API 1.17): a sequence's export and a video take's
// frame, each a row of media_jobs sent at once as cmd.media.job to an env whose hosts key has `media`. They hold no
// model and take no GPU admission: the dispatcher never sees them. The projector applies evt.media.* and stores the
// take; a job whose env went silent is ended by RunMediaJobs.

// noHost is the refusal when no live env can run a media job.
const noHost = "no live host can export (ffmpeg)"

// MaxOpenMediaJobs is how many unfinished media jobs (exports and frames) one user may have: every user's jobs share
// each env's one media worker.
const MaxOpenMediaJobs = 4

type mediaRow struct {
	ID, OwnerID, Kind, Status, Host, Env string
	TimelineID, TimelineName             *string
	Revision                             *int
	Parents                              json.RawMessage
	SourceTakeID, At, Mode, Reason       *string
	Frames                               *int
	Progress                             json.RawMessage
	TakeID                               *string
	Failure                              json.RawMessage
	CreatedAt, HeardAt                   time.Time
	StartedAt, FinishedAt                *time.Time
}

const mediaCols = `id::text, owner_id::text, kind, status, host, env, timeline_id::text, timeline_name, revision, parents,
	source_take_id::text, at, mode, reason, frames, progress, take_id::text, failure, created_at, heard_at, started_at,
	finished_at`

func (r mediaRow) dto() dto.MediaJob {
	return dto.MediaJob{ID: r.ID, Kind: r.Kind, Status: r.Status, Host: r.Host, Env: r.Env, TimelineID: r.TimelineID,
		Revision: r.Revision, SourceTakeID: r.SourceTakeID, Mode: r.Mode, Reason: r.Reason, Progress: nullable(r.Progress),
		TakeID: r.TakeID, Failure: nullable(r.Failure), CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

// mediaCommand is cmd.media.job's data.
type mediaCommand struct {
	Kind         string                `json:"kind"`
	Inputs       []timeline.MediaInput `json:"inputs"`
	OutputPrefix string                `json:"output_prefix"`
	Export       *mediaExport          `json:"export,omitempty"`
	Frame        *mediaFrame           `json:"frame,omitempty"`
	host, env    string
}

type mediaExport struct {
	TimelineID   string                `json:"timeline_id"`
	Revision     int                   `json:"revision"`
	Mode         string                `json:"mode"`
	Target       timeline.ExportTarget `json:"target"`
	Frames       int                   `json:"frames"`
	Samples      int                   `json:"samples"`
	VideoBitrate int                   `json:"video_bitrate"`
	Args         []string              `json:"args"`
	Concat       []int                 `json:"concat,omitempty"`
	Expect       []timeline.Expect     `json:"expect,omitempty"`
	ConformArgs  []string              `json:"conform_args,omitempty"`
}

type mediaFrame struct {
	At    string `json:"at"`
	Index int    `json:"index"`
}

func (mediaCommand) Type() string      { return "cmd.media.job" }
func (c mediaCommand) Subject() string { return "cmd.media." + c.host + "." + c.env }

// mediaEnv is the env that would run a media job now: the named host's media envs, else every live one;
// VideoToolbox first, then host, env by name; never a server env.
func (s *Service) mediaEnv(host *string) (registry.Host, bool) {
	now := time.Now()
	var envs []registry.Host
	for _, h := range s.registry.Hosts() {
		if h.Media != nil && len(h.Media.Encoders) > 0 && h.Live(now) && h.Env != "server" && (host == nil || h.Host == *host) {
			envs = append(envs, h)
		}
	}
	slices.SortFunc(envs, func(a, b registry.Host) int {
		va, vb := a.Media.Encoders[0] == "h264_videotoolbox", b.Media.Encoders[0] == "h264_videotoolbox"
		switch {
		case va != vb && va:
			return -1
		case va != vb:
			return 1
		}
		return strings.Compare(a.Host+"."+a.Env, b.Host+"."+b.Env)
	})
	if len(envs) == 0 {
		return registry.Host{}, false
	}
	return envs[0], true
}

// insertMediaJob stores a queued job and sends its command; past MaxOpenMediaJobs open ones of the owner it is 409
// (counted under a per-user lock, so concurrent requests cannot pass it together).
func insertMediaJob(ctx context.Context, tx pgx.Tx, j mediaRow, command mediaCommand) (mediaRow, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('media:' || $1::text))`, j.OwnerID); err != nil {
		return mediaRow{}, err
	}
	var open int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM media_jobs WHERE owner_id = $1 AND status IN ('queued', 'running')`,
		j.OwnerID).Scan(&open); err != nil {
		return mediaRow{}, err
	} else if open >= MaxOpenMediaJobs {
		return mediaRow{}, network.NewConflictError(fmt.Sprintf("You have %d unfinished exports or frames; wait for one to finish.",
			MaxOpenMediaJobs), nil)
	}
	row, err := one[mediaRow](ctx, tx, `INSERT INTO media_jobs (id, owner_id, kind, host, env, timeline_id, timeline_name,
		revision, parents, source_take_id, at, mode, reason, frames) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		$12, $13, $14) RETURNING `+mediaCols, j.ID, j.OwnerID, j.Kind, j.Host, j.Env, j.TimelineID, j.TimelineName,
		j.Revision, j.Parents, j.SourceTakeID, j.At, j.Mode, j.Reason, j.Frames)
	if err != nil {
		return row, err
	}
	command.host, command.env = j.Host, j.Env
	return row, messaging.Enqueue(ctx, tx, messaging.Command{JobID: j.ID, OwnerID: j.OwnerID, MsgID: j.ID + ":1", Data: command})
}

// ------------------------------------------------------------------ export plan

// planned is a plan with what Export needs from it.
type planned struct {
	out   dto.ExportPlan
	rules timeline.Plan
	doc   timeline.Doc
	takes map[string]timeline.Take
	rows  map[string]gallerymodel.Take
	env   *registry.Host
}

// plan is the export plan of doc (a stored revision of row): normalized against the takes now, with the env that
// would run it.
func (s *Service) plan(ctx context.Context, tx pgx.Tx, row timelineRow, revision int, doc timeline.Doc, host *string) (planned, error) {
	rules, rows, err := loadTakes(ctx, tx, row.OwnerID, docTakeIDs(doc))
	if err != nil {
		return planned{}, err
	}
	if n, probs := timeline.Normalize(doc, rules, &doc); probs == nil {
		doc = *n
	}
	p := timeline.ExportPlan(doc, rules)
	g := timeline.NewGrid(doc.Target)
	out := dto.ExportPlan{Revision: revision, StreamCopy: p.StreamCopy, Reasons: []dto.ExportReason{}, Blocked: []dto.ExportReason{},
		Frames: doc.Frames, DurationS: g.S(doc.Frames), EstimatedBytes: p.EstimatedBytes,
		Output: dto.ExportOutput{Width: doc.Target.Width, Height: doc.Target.Height, FPS: doc.Target.FPS,
			SampleRate: doc.Target.SampleRate, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", VideoBitrate: p.VideoBitrate}}
	for _, r := range p.Reasons {
		out.Reasons = append(out.Reasons, exportReason(r, doc, rules))
	}
	for _, r := range p.Blocked {
		out.Blocked = append(out.Blocked, exportReason(r, doc, rules))
	}
	result := planned{rules: p, doc: doc, takes: rules, rows: rows}
	if env, ok := s.mediaEnv(host); ok {
		hardware := env.Media.Encoders[0] == "h264_videotoolbox"
		out.Host, out.Env, out.Hardware, result.env = &env.Host, &env.Env, &hardware, &env
	} else {
		out.Blocked = append(out.Blocked, dto.ExportReason{Code: "no_host", Message: noHost})
	}
	result.out = out
	return result, nil
}

// exportReason is a reason with its message (Shot n is the n-th V1 clip).
func exportReason(r timeline.Reason, doc timeline.Doc, takes map[string]timeline.Take) dto.ExportReason {
	out := dto.ExportReason{Code: r.Code}
	shot, n := "", 0
	var stream, first *timeline.Stream
	for i, c := range doc.Tracks[0].Clips {
		if i == 0 {
			first = takes[c.Take()].Stream
		}
		if c.ID == r.Clip {
			n, stream = i+1, takes[c.Take()].Stream
		}
	}
	if r.Clip != "" {
		clip := r.Clip
		out.Clip = &clip
		shot = fmt.Sprintf("Shot %d", n)
	}
	t := doc.Target
	switch r.Code {
	case "offline":
		out.Message = "clip " + r.Clip + " is offline"
	case "empty":
		out.Message = "the sequence has no clip on V1"
	case "too_large":
		out.Message = "the export is too large: at most 2 hours, an estimated 16 GB file and 32 GB of clips to read"
	case "no_host":
		out.Message = noHost
	case "gap":
		out.Message = shot + " is an empty slot"
	case "image":
		out.Message = shot + " is an image"
	case "transition":
		out.Message = shot + " dissolves in from the shot before"
	case "not_probed":
		out.Message = shot + " has no stream facts"
	case "trimmed":
		out.Message = shot + " is trimmed"
	case "size":
		out.Message = fmt.Sprintf("%s is %d × %d; the sequence is %d × %d", shot, stream.Width, stream.Height, t.Width, t.Height)
	case "frame_rate":
		out.Message = fmt.Sprintf("%s runs at %s fps; the sequence at %v", shot, stream.FrameRate, t.FPS)
	case "container", "codec", "pixel_format", "aspect", "field_order":
		out.Message = fmt.Sprintf("%s is not H.264 yuv420p with square, progressive pixels in MP4 or MOV (%s)", shot, r.Code)
	default: // profile, level, time_base, colour, parameter_sets: against the first clip
		_ = first
		out.Message = fmt.Sprintf("%s differs from shot 1 in its %s", shot, strings.ReplaceAll(r.Code, "_", " "))
	}
	return out
}

// planRevision is the document of revision (nil: the current one); one not kept is 404.
func planRevision(ctx context.Context, tx pgx.Tx, row timelineRow, revision *int) (int, timeline.Doc, error) {
	if revision == nil || *revision == row.Revision {
		return row.Revision, row.doc(), nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT doc FROM timeline_revisions WHERE timeline_id = $1 AND revision = $2`, row.ID, *revision).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, timeline.Doc{}, errNotFound
	}
	var doc timeline.Doc
	_ = json.Unmarshal(raw, &doc)
	return *revision, doc, err
}

// PlanExport is the export plan of a revision of the sequence (nothing is created).
func (s *Service) PlanExport(ctx context.Context, caller common.Caller, id string, body *dto.TimelineExportCreate) (dto.ExportPlan, error) {
	if body == nil {
		body = &dto.TimelineExportCreate{}
	}
	var out dto.ExportPlan
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, false)
		if err != nil {
			return err
		}
		revision, doc, err := planRevision(ctx, tx, row, body.Revision)
		if err != nil {
			return err
		}
		p, err := s.plan(ctx, tx, row, revision, doc, body.Host)
		out = p.out
		return err
	})
	return out, err
}

// ExportTimeline plans the current revision, builds the ffmpeg arguments and sends the job.
func (s *Service) ExportTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineExportCreate) (dto.MediaJob, error) {
	if body.Revision == nil {
		return dto.MediaJob{}, network.NewUnprocessableError("revision is required", nil)
	}
	var out mediaRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readTimeline(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if *body.Revision != row.Revision {
			return staleRevision(row)
		}
		if body.Host != nil {
			if _, ok := s.mediaEnv(body.Host); !ok {
				return network.NewUnprocessableError("host "+*body.Host+" cannot export", nil)
			}
		}
		p, err := s.plan(ctx, tx, row, row.Revision, row.doc(), body.Host)
		if err != nil {
			return err
		}
		if len(p.out.Blocked) > 0 {
			return network.NewConflictError(p.out.Blocked[0].Message, nil)
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_jobs WHERE timeline_id = $1
			AND status IN ('queued', 'running'))`, row.ID).Scan(&open); err != nil {
			return err
		} else if open {
			return network.NewConflictError("an export of this sequence is already running", nil)
		}
		keys, parents := map[string]string{}, []string{}
		for _, t := range p.doc.Tracks {
			for _, c := range t.Clips {
				if take, ok := p.rows[c.Take()]; ok {
					keys[take.ID] = take.Key
					if !slices.Contains(parents, take.ID) {
						parents = append(parents, take.ID)
					}
				}
			}
		}
		job, err := timeline.BuildExport(p.doc, p.takes, keys, p.rules)
		if err != nil {
			return network.NewConflictError(err.Error(), nil)
		}
		jobID := utility.NewID()
		parentsJSON, _ := json.Marshal(parents)
		out, err = insertMediaJob(ctx, tx, mediaRow{ID: jobID, OwnerID: caller.ID, Kind: "export", Host: p.env.Host,
			Env: p.env.Env, TimelineID: &row.ID, TimelineName: &row.Name, Revision: &row.Revision, Parents: parentsJSON,
			Mode: &job.Mode, Frames: &job.Frames},
			mediaCommand{Kind: "export", Inputs: job.Inputs, OutputPrefix: common.OwnerPrefix(caller.ID) + "exports/" + jobID + "/",
				Export: &mediaExport{TimelineID: row.ID, Revision: row.Revision, Mode: job.Mode, Target: job.Target,
					Frames: job.Frames, Samples: job.Samples, VideoBitrate: job.VideoBitrate, Args: job.Args, Concat: job.Concat,
					Expect: job.Expect, ConformArgs: job.ConformArgs}})
		return err
	})
	return out.dto(), err
}

// ListExports is the sequence's exports, newest first.
func (s *Service) ListExports(ctx context.Context, caller common.Caller, id string) (dto.MediaJobList, error) {
	out := dto.MediaJobList{Jobs: []dto.MediaJob{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readTimeline(ctx, tx, id, false); err != nil {
			return err
		}
		rows, err := collect[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE timeline_id = $1 AND kind = 'export'
			ORDER BY created_at DESC, id LIMIT 20`, id)
		for _, r := range rows {
			out.Jobs = append(out.Jobs, r.dto())
		}
		return err
	})
	return out, err
}

// GetMediaJob is one of the caller's media jobs.
func (s *Service) GetMediaJob(ctx context.Context, caller common.Caller, id string) (dto.MediaJob, error) {
	if !utility.IsUUID(id) {
		return dto.MediaJob{}, errNotFound
	}
	var out mediaRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		out, err = one[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE id = $1`, id)
		return err
	})
	return out.dto(), err
}

// CancelMediaJob asks the runner to stop the job (KV cancels); one whose env went silent is ended here with its
// command purged, so a runner that comes back never runs it.
func (s *Service) CancelMediaJob(ctx context.Context, caller common.Caller, id string) (dto.MediaJob, error) {
	if !utility.IsUUID(id) {
		return dto.MediaJob{}, errNotFound
	}
	var out mediaRow
	ask := false
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		if out, err = one[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		if jobEnded[out.Status] {
			return network.NewConflictError("job already ended", nil)
		}
		if s.mediaKeyLive(out, time.Now()) {
			ask = true
			return nil
		}
		if err := purgeMedia(ctx, s.purge, out); err != nil {
			return network.NewServiceUnavailableError("could not reach the runner's queue; try again", err)
		}
		return endMedia(ctx, tx, &out, "cancelled", nil)
	})
	if err == nil && ask {
		if err := s.cancel(ctx, id); err != nil {
			return dto.MediaJob{}, network.NewServiceUnavailableError("could not reach the runner; try again", err)
		}
	}
	return out.dto(), err
}

// mediaKeyLive reports whether the job's env's hosts key is there and fresh.
func (s *Service) mediaKeyLive(job mediaRow, now time.Time) bool {
	return mediaKeyLive(s.registry, job, now)
}

func mediaKeyLive(reg *registry.Registry, job mediaRow, now time.Time) bool {
	for _, h := range reg.Hosts() {
		if h.Host == job.Host && h.Env == job.Env {
			return now.Sub(h.UpdatedAt) <= StaleJobKey
		}
	}
	return false
}

// endMedia moves an open job to a terminal status.
func endMedia(ctx context.Context, tx pgx.Tx, job *mediaRow, status string, failure any) error {
	f, _ := json.Marshal(failure)
	row, err := one[mediaRow](ctx, tx, `UPDATE media_jobs SET status = $2, failure = $3::jsonb, finished_at = clock_timestamp()
		WHERE id = $1 RETURNING `+mediaCols, job.ID, status, string(f))
	*job = row
	return err
}

// purgeMedia drops the commands queued for job's env (cmd.media.<host>.<env>), for a job whose env's key is stale: no
// job is sent to an env that stale, and every open one on it is ended too. nil purge: not reachable (tests).
func purgeMedia(ctx context.Context, purge func(ctx context.Context, subject string) error, job mediaRow) error {
	if purge == nil {
		return nil
	}
	return purge(ctx, "cmd.media."+job.Host+"."+job.Env)
}

// SweepMediaJobs ends the open jobs studio can no longer hear: an env's key gone or older than StaleJobKey is
// cancelled with its command purged; a job unheard for JobSilence fails (internal) and its runner is asked to stop
// it (KV cancels), so a hung ffmpeg never holds the env's media worker.
func SweepMediaJobs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry, purge func(ctx context.Context, subject string) error,
	cancel Cancel, now time.Time) error {
	if !reg.Ready() {
		return nil
	}
	var silent []string
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		open, err := collect[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE status IN ('queued', 'running')
			FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		for _, job := range open {
			switch {
			case !mediaKeyLive(reg, job, now):
				if err := purgeMedia(ctx, purge, job); err != nil {
					log.Printf("media jobs: purge cmd.media.%s.%s: %v", job.Host, job.Env, err) // tried again next tick
					continue
				}
				err = endMedia(ctx, tx, &job, "cancelled", nil)
			case now.Sub(job.HeardAt) > JobSilence:
				silent = append(silent, job.ID)
				err = endMedia(ctx, tx, &job, "failed", map[string]string{"class": "internal", "detail": "the runner stopped reporting this job"})
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	for _, id := range silent {
		if cancel == nil {
			break
		}
		if err := cancel(ctx, id); err != nil {
			log.Printf("media jobs: cancel %s: %v", id, err) // ended here; the runner's events are ignored
		}
	}
	return err
}

// RunMediaJobs sweeps media jobs every Tick until ctx ends.
func RunMediaJobs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry, purge func(ctx context.Context, subject string) error, cancel Cancel) {
	tick := time.NewTicker(Tick)
	defer tick.Stop()
	for {
		if err := SweepMediaJobs(ctx, pool, reg, purge, cancel, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("media jobs: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// ------------------------------------------------------------------ frames

// TakeFrame answers POST /takes/{id}/frame: the frame take (made at once by a copy), or the job that makes it.
func (s *Service) TakeFrame(ctx context.Context, caller common.Caller, id string, body *dto.TakeFrameRequest) (dto.TakeFrame, error) {
	if !utility.IsUUID(id) {
		return dto.TakeFrame{}, errNotFound
	}
	var out dto.TakeFrame
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, rows, err := loadTakes(ctx, tx, caller.ID, []string{id})
		if err != nil {
			return err
		}
		video, ok := rows[id]
		if !ok {
			return errNotFound
		}
		take, job, err := s.frameOf(ctx, tx, caller.ID, video, body.At)
		if err != nil {
			return err
		}
		if take != nil {
			t := gallery.TakeDTO(s.signer, *take)
			out.Take = &t
		} else {
			j := job.dto()
			out.Job = &j
		}
		return nil
	})
	return out, err
}

// frameOf is video's frame take at at: one made before, else a copy of its poster (first) or stored last frame; with
// no stored last frame, the open frame job (or a new one).
func (s *Service) frameOf(ctx context.Context, tx pgx.Tx, ownerID string, video gallerymodel.Take, at string) (*gallerymodel.Take, *mediaRow, error) {
	if video.Kind != "video" {
		return nil, nil, network.NewUnprocessableError("take "+video.ID+" is not a video", nil)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('frame:' || $1 || ':' || $2))`, video.ID, at); err != nil {
		return nil, nil, err
	}
	made, err := one[gallerymodel.Take](ctx, tx, `SELECT `+gallerymodel.TakeCols+` FROM takes t WHERE t.owner_id = $1
		AND t.frame_of->>'take_id' = $2 AND t.frame_of->>'at' = $3 AND t.deleted_at IS NULL`, ownerID, video.ID, at)
	if err == nil {
		return &made, nil, nil
	} else if !errors.Is(err, errNotFound) {
		return nil, nil, err
	}
	var v gallerydto.Video
	_ = json.Unmarshal(video.Video, &v)
	var src *gallerydto.Poster
	switch {
	case at == "last":
		src = v.Last
	case video.Source == "create" || video.Source == "pipeline": // a run's poster is its frame 0; an export's is a
		// tenth in, an upload's or import's is the client's
		src = &v.Poster
	}
	if src != nil && !common.Owns(ownerID, src.Key) {
		src = nil // never copy another user's object
	}
	if src != nil && src.Key != "" {
		frameID := utility.NewID()
		key := common.OwnerPrefix(ownerID) + "frames/" + frameID + ".png"
		err := s.store.Copy(ctx, src.Key, key)
		if err == nil {
			take, err := insertFrameTake(ctx, tx, ownerID, frameID, video, at, key, *src)
			return &take, nil, err
		} else if !errors.Is(err, storage.ErrNotFound) {
			return nil, nil, network.NewServiceUnavailableError("could not copy the frame; try again", err)
		}
	}
	job, err := one[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE kind = 'frame' AND source_take_id = $1
		AND at = $2 AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1`, video.ID, at)
	if err == nil {
		return nil, &job, nil
	} else if !errors.Is(err, errNotFound) {
		return nil, nil, err
	}
	env, ok := s.mediaEnv(nil)
	if !ok {
		return nil, nil, network.NewConflictError(noHost, nil)
	}
	index := 0
	if at == "last" {
		index = max(v.Frames-1, 0)
	}
	jobID := utility.NewID()
	job, err = insertMediaJob(ctx, tx, mediaRow{ID: jobID, OwnerID: ownerID, Kind: "frame", Host: env.Host, Env: env.Env,
		SourceTakeID: &video.ID, At: &at},
		mediaCommand{Kind: "frame", Inputs: []timeline.MediaInput{{Key: video.Key, Kind: "video"}},
			OutputPrefix: common.OwnerPrefix(ownerID) + "frames/" + jobID + "/", Frame: &mediaFrame{At: at, Index: index}})
	return nil, &job, err
}

// insertFrameTake stores the image take of video's frame at at.
func insertFrameTake(ctx context.Context, tx pgx.Tx, ownerID, id string, video gallerymodel.Take, at, key string, frame gallerydto.Poster) (gallerymodel.Take, error) {
	title := video.Title + " · " + at + " frame"
	frameOf, _ := json.Marshal(timeline.FrameOf{TakeID: video.ID, At: at})
	if _, err := tx.Exec(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes,
		ready, frame_of) VALUES ($1, $2, 'image', 'frame', $3, $4, 'image/png', $5, $6, $7, true, $8)`,
		id, ownerID, title, key, frame.Width, frame.Height, frame.Bytes, frameOf); err != nil {
		return gallerymodel.Take{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx) VALUES ($1, $2, $3, 0)`,
		id, video.ID, ownerID); err != nil {
		return gallerymodel.Take{}, err
	}
	return one[gallerymodel.Take](ctx, tx, `SELECT `+gallerymodel.TakeCols+` FROM takes t WHERE t.id = $1`, id)
}

// ------------------------------------------------------------------ the projector of evt.media.*

type mediaStatus struct {
	message.ModelBase
	Status  string          `json:"status"`
	Encoder *string         `json:"encoder"`
	Failure json.RawMessage `json:"failure"`
	Result  *mediaResult    `json:"result"`
}

type mediaResult struct {
	Key    string            `json:"key"`
	Width  int               `json:"width"`
	Height int               `json:"height"`
	Bytes  int64             `json:"bytes"`
	Etag   *string           `json:"etag"`
	Mode   *string           `json:"mode"`
	Reason *string           `json:"reason"`
	Video  *gallerydto.Video `json:"video"`
}

type mediaProgress struct {
	message.ModelBase
	Frames int `json:"frames"`
	Total  int `json:"total"`
}

// MountMedia registers the handlers of evt.media.<host>.status and .progress (durable studio-media).
func (p *Projector) MountMedia(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.media.*.status", mediaHandler(p.pool, applyMediaStatus))
	group.AddHandler("evt.media.*.progress", mediaHandler(p.pool, applyMediaProgress))
}

// mediaHandler decodes, projects once per (job_id, delivery, seq) onto an open job of the subject's host and the
// envelope's owner, and acks; a database failure naks.
func mediaHandler[D interface{ Base() message.ModelBase }](pool *pgxpool.Pool,
	apply func(ctx context.Context, tx pgx.Tx, job mediaRow, data D) error) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		parts := strings.Split(msg.Subject(), ".")
		host := ""
		if len(parts) == 4 {
			host = parts[2]
		}
		if err := projectMedia(ctx, pool, host, event, apply); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

func projectMedia[D interface{ Base() message.ModelBase }](ctx context.Context, pool *pgxpool.Pool, host string, event *gen.Message[D],
	apply func(ctx context.Context, tx pgx.Tx, job mediaRow, data D) error) error {
	base := event.Data.Base()
	if event.JobID == nil || !utility.IsUUID(*event.JobID) || base.Seq < 1 || base.Delivery < 1 {
		log.Printf("media projector: dropping %s without a job_id, seq or delivery", event.Type)
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		job, err := one[mediaRow](ctx, tx, `SELECT `+mediaCols+` FROM media_jobs WHERE id = $1 FOR UPDATE`, *event.JobID)
		if errors.Is(err, errNotFound) || (err == nil && (job.Host != host || job.OwnerID != event.OwnerID)) {
			log.Printf("media projector: %s for unknown job %s (host %s)", event.Type, *event.JobID, host)
			return nil
		} else if err != nil || jobEnded[job.Status] {
			return err
		}
		if first, err := messaging.FirstSeen(ctx, tx, job.ID, base.Delivery, base.Seq); err != nil || !first {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE media_jobs SET heard_at = clock_timestamp() WHERE id = $1`, job.ID); err != nil {
			return err
		}
		return apply(ctx, tx, job, event.Data)
	})
}

// applyMediaProgress keeps an export's latest frame count.
func applyMediaProgress(ctx context.Context, tx pgx.Tx, job mediaRow, data mediaProgress) error {
	if job.Kind != "export" || data.Total < 1 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE media_jobs SET progress = $2 WHERE id = $1`, job.ID,
		map[string]int{"frames": min(max(data.Frames, 0), data.Total), "total": data.Total})
	return err
}

// applyMediaStatus: running starts the job; succeeded stores its take in the same transaction; failed, cancelled end it.
func applyMediaStatus(ctx context.Context, tx pgx.Tx, job mediaRow, data mediaStatus) error {
	switch data.Status {
	case "running":
		_, err := tx.Exec(ctx, `UPDATE media_jobs SET status = 'running', started_at = coalesce(started_at, clock_timestamp())
			WHERE id = $1`, job.ID)
		return err
	case "failed":
		var failure struct {
			Class  string `json:"class"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data.Failure, &failure) != nil || failure.Class == "" || failure.Detail == "" {
			failure.Class, failure.Detail = "internal", "the runner reported no reason"
		}
		return endMedia(ctx, tx, &job, "failed", failure)
	case "cancelled":
		return endMedia(ctx, tx, &job, "cancelled", nil)
	case "succeeded":
		takeID, bad, err := storeMediaTake(ctx, tx, job, data.Result)
		if err != nil {
			return err
		}
		if bad != "" {
			log.Printf("media projector: job %s: %s", job.ID, bad)
			return endMedia(ctx, tx, &job, "failed", map[string]string{"class": "internal", "detail": bad})
		}
		mode, reason := job.Mode, job.Reason
		if r := data.Result; job.Kind == "export" && r.Mode != nil {
			mode, reason = r.Mode, r.Reason
		}
		// a finished export has written every frame, even one too short to report progress
		_, err = tx.Exec(ctx, `UPDATE media_jobs SET status = 'succeeded', take_id = $2, mode = $3, reason = $4,
			progress = CASE WHEN kind = 'export' THEN jsonb_build_object('frames', frames, 'total', frames) ELSE progress END,
			finished_at = clock_timestamp(), started_at = coalesce(started_at, clock_timestamp()) WHERE id = $1`,
			job.ID, takeID, mode, reason)
		return err
	}
	log.Printf("media projector: job %s: unknown status %q", job.ID, data.Status)
	return nil
}

// storeMediaTake makes a succeeded job's take: an export's video take (source export) or a frame's image take; a
// result outside the job's prefix is the second result.
func storeMediaTake(ctx context.Context, tx pgx.Tx, job mediaRow, r *mediaResult) (string, string, error) {
	inside := func(key, prefix, suffix string) bool {
		return common.Owns(job.OwnerID, key) && strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
	}
	if r == nil {
		return "", "the runner sent no result", nil
	}
	id := utility.NewID()
	if job.Kind == "frame" {
		prefix := common.OwnerPrefix(job.OwnerID) + "frames/" + job.ID + "/"
		if !inside(r.Key, prefix, "frame.png") {
			return "", "the result is outside the job's prefix", nil
		}
		video, err := one[gallerymodel.Take](ctx, tx, `SELECT `+gallerymodel.TakeCols+` FROM takes t WHERE t.id = $1`, *job.SourceTakeID)
		if err != nil {
			return "", "", err
		}
		frame := gallerydto.Poster{Key: r.Key, Width: r.Width, Height: r.Height, Bytes: r.Bytes}
		if _, err := insertFrameTake(ctx, tx, job.OwnerID, id, video, *job.At, r.Key, frame); err != nil {
			return "", "", err
		}
		if *job.At == "last" { // that video's last frame from now on
			_, err = tx.Exec(ctx, `UPDATE takes SET video = jsonb_set(video, '{last}', $2) WHERE id = $1 AND video IS NOT NULL`,
				video.ID, frame)
		}
		return id, "", err
	}
	prefix := common.OwnerPrefix(job.OwnerID) + "exports/" + job.ID + "/"
	v := r.Video
	if !inside(r.Key, prefix, "export.mp4") || v == nil || !inside(v.Poster.Key, prefix, ".poster.png") ||
		v.Last == nil || !inside(v.Last.Key, prefix, ".last.png") {
		return "", "the result is outside the job's prefix", nil
	}
	mode := deref(job.Mode)
	if r.Mode != nil {
		mode = *r.Mode
	}
	export, _ := json.Marshal(map[string]any{"timeline_id": job.TimelineID, "timeline_name": job.TimelineName,
		"revision": job.Revision, "job_id": job.ID, "mode": mode})
	if _, err := tx.Exec(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes,
		etag, video, ready, export) VALUES ($1, $2, 'video', 'export', $3, $4, 'video/mp4', $5, $6, $7, $8, $9, true, $10)`,
		id, job.OwnerID, deref(job.TimelineName), r.Key, r.Width, r.Height, r.Bytes, r.Etag, v, export); err != nil {
		return "", "", err
	}
	_, err := tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx)
		SELECT $1, parent::uuid, $2, idx - 1 FROM jsonb_array_elements_text(coalesce($3::jsonb, '[]')) WITH ORDINALITY AS u(parent, idx)
		ON CONFLICT DO NOTHING`, id, job.OwnerID, string(job.Parents))
	return id, "", err
}

// resolveFrames replaces every frameRef {frame_of, at} in inputs (a re-render step's frame source, API 1.17) with the
// frame take {take_id}; the second result is why one cannot be made now.
func (s *Service) resolveFrames(ctx context.Context, tx pgx.Tx, ownerID string, inputs map[string]any) (string, error) {
	var walk func(v any) (any, string, error)
	walk = func(v any) (any, string, error) {
		switch x := v.(type) {
		case map[string]any:
			if of, ok := x["frame_of"].(string); ok && len(x) == 2 {
				at, _ := x["at"].(string)
				if !utility.IsUUID(of) || (at != "first" && at != "last") {
					return v, "frame_of must be a take id and at first or last", nil
				}
				_, rows, err := loadTakes(ctx, tx, ownerID, []string{of})
				if err != nil || rows[of].ID == "" {
					return v, "take " + of + " not found", err
				}
				take, _, err := s.frameOf(ctx, tx, ownerID, rows[of], at)
				var api *network.APIError
				if errors.As(err, &api) && api.Code < 500 {
					return v, api.Message, nil
				} else if err != nil || take == nil {
					return v, "the " + at + " frame of take " + of + " is being made; try again", err
				}
				return map[string]any{"take_id": take.ID}, "", nil
			}
			for k, item := range x {
				out, detail, err := walk(item)
				if err != nil || detail != "" {
					return v, detail, err
				}
				x[k] = out
			}
		case []any:
			for i, item := range x {
				out, detail, err := walk(item)
				if err != nil || detail != "" {
					return v, detail, err
				}
				x[i] = out
			}
		}
		return v, "", nil
	}
	_, detail, err := walk(map[string]any(inputs))
	return detail, err
}
