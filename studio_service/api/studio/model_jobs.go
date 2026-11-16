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
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// Model jobs (contracts/README.md "Models and model jobs", API 1.16): downloads and deletes of weights, deletes of
// shared LoRA files, each a row of model_jobs (admin only). Studio releases at most one job per host at a time, oldest
// first, re-checking its refusals (an outbox row: cmd.model.job on cmd.model.<host>.<env>); the projector applies the
// runner's evt.model.* to it.

// StaleJobKey is how old an env's hosts key may be before studio cancels a released download itself.
const StaleJobKey = 60 * time.Second

// JobSilence is how long a released job may go unheard (no evt.model.* since its release or its last event: the
// runner sends progress at least every 30 s) before studio fails it and purges its command.
var JobSilence = 10 * time.Minute

// jobRow is a model_jobs row.
type jobRow struct {
	ID, Kind, Host, Env            string
	Model, Variant, LoraID         *string
	Title, State                   string
	Progress, Failure, Result      json.RawMessage
	Keeps, Licence                 json.RawMessage
	RequestedBy                    string
	CreatedAt                      time.Time
	ReleasedAt, StartedAt, EndedAt *time.Time
}

const jobCols = `id::text, kind, host, env, model, variant, lora_id, title, state, progress, failure, result, keeps,
	licence, requested_by::text, created_at, released_at, started_at, ended_at`

var jobEnded = map[string]bool{"succeeded": true, "failed": true, "cancelled": true}

func (r jobRow) dto() dto.ModelJob {
	out := dto.ModelJob{ID: r.ID, Kind: r.Kind, Host: r.Host, Env: r.Env, Model: r.Model, Variant: r.Variant, LoraID: r.LoraID,
		Title: r.Title, State: r.State, Progress: r.Progress, Failure: r.Failure, Result: r.Result, Keeps: []dto.Keep{},
		RequestedBy: r.RequestedBy, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, EndedAt: r.EndedAt}
	_ = json.Unmarshal(r.Keeps, &out.Keeps)
	_ = json.Unmarshal(r.Licence, &out.Licence)
	if out.Keeps == nil {
		out.Keeps = []dto.Keep{}
	}
	return out
}

// runOnHost is a run not yet terminal that may load weights on a host: where it is placed (nil: not yet), its env,
// its model nodes and its LoRAs.
type runOnHost struct {
	host   *string
	env    string
	models []runModel
	loras  []lora.Used
}

// openRuns are the runs not yet terminal that are placed on host or not placed yet (every user's: tx is System).
func openRuns(ctx context.Context, tx pgx.Tx, host string) ([]runOnHost, error) {
	type row struct {
		Host  *string
		Env   string
		Nodes json.RawMessage
		Loras json.RawMessage
	}
	rows, err := collect[row](ctx, tx, `SELECT host, env, nodes, loras FROM runs
		WHERE status IN ('queued', 'running') AND (host = $1 OR host IS NULL)`, host)
	var out []runOnHost
	for _, r := range rows {
		var graph map[string]registry.Node
		_ = json.Unmarshal(r.Nodes, &graph)
		out = append(out, runOnHost{host: r.Host, env: r.Env, models: runModels(graph), loras: runUsed(r.Loras)})
	}
	return out, err
}

// lockHost serialises the job decisions of one host.
func lockHost(ctx context.Context, tx pgx.Tx, host string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('model_jobs:' || $1))`, host)
	return err
}

// unfinished is the open job of kind on host matching model, variant and lora (nil compares as null), or nil.
func unfinished(ctx context.Context, tx pgx.Tx, kind, host string, model, variant, loraID *string) (*jobRow, error) {
	row, err := one[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE kind = $1 AND host = $2
		AND model IS NOT DISTINCT FROM $3 AND variant IS NOT DISTINCT FROM $4 AND lora_id IS NOT DISTINCT FROM $5
		AND state IN ('queued', 'running') ORDER BY created_at LIMIT 1`, kind, host, model, variant, loraID)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return &row, err
}

// openOf reports whether a job of kind on host for model (any variant) other than except is queued or running.
func openOf(ctx context.Context, tx pgx.Tx, kind, host, model, except string) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM model_jobs WHERE kind = $1 AND host = $2 AND model = $3
		AND state IN ('queued', 'running') AND id::text <> $4)`, kind, host, model, except).Scan(&found)
	return found, err
}

// insertJob stores a queued job.
func insertJob(ctx context.Context, tx pgx.Tx, j jobRow) (jobRow, error) {
	if j.Keeps == nil {
		j.Keeps = json.RawMessage(`[]`)
	}
	return one[jobRow](ctx, tx, `INSERT INTO model_jobs (id, kind, host, env, model, variant, lora_id, title, keeps,
		licence, requested_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING `+jobCols,
		utility.NewID(), j.Kind, j.Host, j.Env, j.Model, j.Variant, j.LoraID, j.Title, j.Keeps, j.Licence, j.RequestedBy)
}

// downloadDB applies a download's refusals that need the jobs table: a delete of the model on the host unfinished.
func downloadDB(ctx context.Context, tx pgx.Tx, host string, d download, except string) error {
	if open, err := openOf(ctx, tx, "delete", host, d.model.ID, except); err != nil || !open {
		return err
	}
	return network.NewConflictError(fmt.Sprintf("a delete of %s on %s is unfinished", d.model.Title, host), nil)
}

// deleteDB applies a delete's refusals that need the database: a run not yet terminal uses it, a download of the
// model on the host is unfinished.
func deleteDB(ctx context.Context, tx pgx.Tx, w weights, host string, del removal) error {
	runs, err := openRuns(ctx, tx, host)
	if err != nil {
		return err
	}
	if w.usedBy(host, del, runs) {
		return network.NewConflictError(fmt.Sprintf("%s is in use by a queued or running run on %s", del.model.Title, host), nil)
	}
	if open, err := openOf(ctx, tx, "download", host, del.model.ID, ""); err != nil || !open {
		return err
	}
	return network.NewConflictError(fmt.Sprintf("a download of %s on %s is unfinished; cancel it first", del.model.Title, host), nil)
}

// loraDB applies a LoRA delete's refusal that needs the database: a run not yet terminal on the host names it.
func loraDB(ctx context.Context, tx pgx.Tx, w weights, host, id string, del loraDelete) error {
	runs, err := openRuns(ctx, tx, host)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.host == nil {
			if _, ok := w.key(host, run.env); !ok {
				continue
			}
		}
		if slices.ContainsFunc(run.loras, func(u lora.Used) bool { return u.ID == id }) {
			return network.NewConflictError(fmt.Sprintf("LoRA %s is in use by a queued or running run on %s", del.name, host), nil)
		}
	}
	return nil
}

// DownloadModel answers POST /admin/models/{model}/download: the new job (true: 202), or the same unfinished one.
func (s *Service) DownloadModel(ctx context.Context, caller common.Caller, model string, body *dto.ModelDownload) (dto.ModelJob, bool, error) {
	if !caller.IsAdmin() {
		return dto.ModelJob{}, false, errAdmin
	}
	if !s.registry.Ready() {
		return dto.ModelJob{}, false, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out jobRow
	created := false
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockHost(ctx, tx, body.Host); err != nil {
			return err
		}
		w := s.weights()
		if !w.known(model) {
			return errNotFound
		}
		variant := body.Variant
		if variant == nil { // the job names the variant it fetches
			if _, m, ok := w.envFor(body.Host, model); ok && len(m.Variants) > 0 {
				variant = &m.Variants[0].ID
			}
		}
		if variant != nil {
			if open, err := unfinished(ctx, tx, "download", body.Host, &model, variant, nil); err != nil || open != nil {
				if open != nil {
					out = *open
				}
				return err
			}
		}
		d, err := w.checkDownload(body.Host, model, variant, body.AcceptLicence)
		if err != nil {
			return err
		}
		if err := downloadDB(ctx, tx, body.Host, d, ""); err != nil {
			return err
		}
		licence, _ := json.Marshal(d.licence)
		out, err = insertJob(ctx, tx, jobRow{Kind: "download", Host: body.Host, Env: d.env, Model: &model, Variant: &d.variant.ID,
			Title: d.model.Title + " · " + firstNonEmpty(d.variant.Title, d.variant.ID), Licence: licence, RequestedBy: caller.ID})
		created = err == nil
		return err
	})
	return out.dto(), created, err
}

// DeleteModel answers DELETE /admin/models/{model}?host=&variant=.
func (s *Service) DeleteModel(ctx context.Context, caller common.Caller, model, host string, variant *string) (dto.ModelJob, bool, error) {
	if !caller.IsAdmin() {
		return dto.ModelJob{}, false, errAdmin
	}
	switch {
	case host == "":
		return dto.ModelJob{}, false, network.NewUnprocessableError("host is required", nil)
	case !dto.HostName.MatchString(host):
		return dto.ModelJob{}, false, network.NewUnprocessableError("host must be a host name", nil)
	case variant != nil && !dto.VariantID.MatchString(*variant):
		return dto.ModelJob{}, false, network.NewUnprocessableError("variant must be a variant id", nil)
	case !s.registry.Ready():
		return dto.ModelJob{}, false, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out jobRow
	created := false
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockHost(ctx, tx, host); err != nil {
			return err
		}
		if open, err := unfinished(ctx, tx, "delete", host, &model, variant, nil); err != nil || open != nil {
			if open != nil {
				out = *open
			}
			return err
		}
		w := s.weights()
		del, err := w.checkDelete(host, model, variant)
		if err != nil {
			return err
		}
		if err := deleteDB(ctx, tx, w, host, del); err != nil {
			return err
		}
		keeps, _ := json.Marshal(del.keeps)
		out, err = insertJob(ctx, tx, jobRow{Kind: "delete", Host: host, Env: del.env, Model: &model, Variant: variant,
			Title: del.title, Keeps: keeps, RequestedBy: caller.ID})
		created = err == nil
		return err
	})
	return out.dto(), created, err
}

// DeleteSharedLora answers DELETE /admin/loras/{lora_id}?host=.
func (s *Service) DeleteSharedLora(ctx context.Context, caller common.Caller, id, host string) (dto.ModelJob, bool, error) {
	if !caller.IsAdmin() {
		return dto.ModelJob{}, false, errAdmin
	}
	switch {
	case host == "":
		return dto.ModelJob{}, false, network.NewUnprocessableError("host is required", nil)
	case !dto.HostName.MatchString(host):
		return dto.ModelJob{}, false, network.NewUnprocessableError("host must be a host name", nil)
	case !lora.IsShared(id):
		return dto.ModelJob{}, false, errNotFound
	case !s.registry.Ready():
		return dto.ModelJob{}, false, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out jobRow
	created := false
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockHost(ctx, tx, host); err != nil {
			return err
		}
		if open, err := unfinished(ctx, tx, "lora_delete", host, nil, nil, &id); err != nil || open != nil {
			if open != nil {
				out = *open
			}
			return err
		}
		w := s.weights()
		del, err := w.checkLoraDelete(host, id)
		if err != nil {
			return err
		}
		if err := loraDB(ctx, tx, w, host, id, del); err != nil {
			return err
		}
		out, err = insertJob(ctx, tx, jobRow{Kind: "lora_delete", Host: host, Env: del.env, LoraID: &id, Title: del.name,
			RequestedBy: caller.ID})
		created = err == nil
		return err
	})
	return out.dto(), created, err
}

// ListModelJobs answers GET /admin/model-jobs?host=&active=: newest first, at most 200.
func (s *Service) ListModelJobs(ctx context.Context, caller common.Caller, host, active string) (dto.ModelJobList, error) {
	out := dto.ModelJobList{Jobs: []dto.ModelJob{}}
	switch {
	case !caller.IsAdmin():
		return out, errAdmin
	case host != "" && !dto.HostName.MatchString(host):
		return out, network.NewUnprocessableError("host must be a host name", nil)
	case active != "" && active != "true" && active != "false":
		return out, network.NewUnprocessableError("active must be true or false", nil)
	}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := collect[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE ($1 = '' OR host = $1)
			AND ($2 <> 'true' OR state IN ('queued', 'running')) ORDER BY created_at DESC, id LIMIT 200`, host, active)
		for _, r := range rows {
			out.Jobs = append(out.Jobs, r.dto())
		}
		return err
	})
	return out, err
}

// GetModelJob answers GET /admin/model-jobs/{job_id}.
func (s *Service) GetModelJob(ctx context.Context, caller common.Caller, id string) (dto.ModelJob, error) {
	if !caller.IsAdmin() {
		return dto.ModelJob{}, errAdmin
	}
	if !utility.IsUUID(id) {
		return dto.ModelJob{}, errNotFound
	}
	var out jobRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = one[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE id = $1`, id)
		return err
	})
	return out.dto(), err
}

// CancelModelJob answers POST /admin/model-jobs/{job_id}/cancel: a job not released yet ends at once; a released
// download is asked to stop through KV cancels (or ended here when its runner's key is gone or older than
// StaleJobKey, its command purged); a released delete cannot be cancelled.
func (s *Service) CancelModelJob(ctx context.Context, caller common.Caller, id string) (dto.ModelJob, error) {
	if !caller.IsAdmin() {
		return dto.ModelJob{}, errAdmin
	}
	if !utility.IsUUID(id) {
		return dto.ModelJob{}, errNotFound
	}
	var out jobRow
	ask := false
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		if out, err = one[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		switch {
		case jobEnded[out.State]:
			return network.NewConflictError("job already ended", nil)
		case out.ReleasedAt == nil:
			return endJob(ctx, tx, &out, "cancelled", nil, nil)
		case out.Kind != "download":
			return network.NewConflictError("a running delete cannot be cancelled", nil)
		}
		key, ok := s.weights().key(out.Host, out.Env)
		if ok && time.Since(key.UpdatedAt) <= StaleJobKey {
			ask = true
			return nil
		}
		if s.purge != nil {
			if err := s.purge(ctx, "cmd.model."+out.Host+"."+out.Env); err != nil {
				return network.NewServiceUnavailableError("could not reach the runner's queue; try again", err)
			}
		}
		return endJob(ctx, tx, &out, "cancelled", nil, nil)
	})
	if err == nil && ask {
		if err := s.cancel(ctx, id); err != nil {
			return dto.ModelJob{}, network.NewServiceUnavailableError("could not reach the runner; try again", err)
		}
	}
	return out.dto(), err
}

// endJob moves an open job to a terminal state.
func endJob(ctx context.Context, tx pgx.Tx, job *jobRow, state string, failure, result any) error {
	f, _ := json.Marshal(failure)
	r, _ := json.Marshal(result)
	row, err := one[jobRow](ctx, tx, `UPDATE model_jobs SET state = $2, failure = $3::jsonb, result = $4::jsonb,
		ended_at = clock_timestamp() WHERE id = $1 RETURNING `+jobCols, job.ID, state, string(f), string(r))
	*job = row
	return err
}

// ------------------------------------------------------------------ release

// ReleaseModelJobs fails the released jobs whose env's hosts key is gone or older than StaleJobKey (host_lost: the
// host stopped responding, a spot VM preempted; their command purged; queued jobs wait for their host) and those
// unheard for JobSilence (their command purged: a runner that crashed through max_deliver, or term'd it, never
// reports them), then releases the oldest queued job of every host with no
// released job open, once its env's key is live: its refusals are checked again (a refusal fails it, class in_use)
// and its cmd.model.job goes to the outbox. It holds the dispatcher's lock too, so a run is never released to a host
// in the same instant as a delete of its weights there.
func ReleaseModelJobs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry, purge func(ctx context.Context, subject string) error, now time.Time) error {
	if !reg.Ready() {
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('studio.model_jobs'))`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('studio.dispatcher'))`); err != nil {
			return err
		}
		ended := func(job jobRow, class, detail string) error {
			if purge != nil {
				if err := purge(ctx, "cmd.model."+job.Host+"."+job.Env); err != nil {
					log.Printf("model jobs: purge cmd.model.%s.%s: %v", job.Host, job.Env, err) // tried again next tick
					return nil
				}
			}
			log.Printf("model jobs: %s %s on %s: %s", job.Kind, job.ID, job.Host, detail)
			return endJob(ctx, tx, &job, "failed", map[string]string{"class": class, "detail": detail}, nil)
		}
		// only jobs unheard for StaleJobKey: a runner restarted cleanly deletes its key but reports again within
		// seconds and finishes the redelivered command, so a missing key alone is no host loss
		released, err := collect[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE state IN ('queued', 'running')
			AND released_at IS NOT NULL AND coalesce(heard_at, released_at) < $1 FOR UPDATE`, now.Add(-StaleJobKey))
		if err != nil {
			return err
		}
		lost := map[string]bool{}
		for _, job := range released {
			if key, ok := (weights{values: reg.Hosts()}).key(job.Host, job.Env); !ok || now.Sub(key.UpdatedAt) > StaleJobKey {
				lost[job.ID] = true
				if err := ended(job, "host_lost", "host "+job.Host+" stopped responding"); err != nil {
					return err
				}
			}
		}
		silent, err := collect[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE state IN ('queued', 'running')
			AND released_at IS NOT NULL AND coalesce(heard_at, released_at) < $1`, now.Add(-JobSilence))
		if err != nil {
			return err
		}
		for _, job := range silent {
			if !lost[job.ID] {
				if err := ended(job, "internal", "the runner stopped reporting this job"); err != nil {
					return err
				}
			}
		}
		next, err := collect[jobRow](ctx, tx, `SELECT DISTINCT ON (host) `+jobCols+` FROM model_jobs j
			WHERE state = 'queued' AND released_at IS NULL AND NOT EXISTS (SELECT 1 FROM model_jobs o
				WHERE o.host = j.host AND o.state IN ('queued', 'running') AND o.released_at IS NOT NULL)
			ORDER BY host, created_at, id`)
		if err != nil {
			return err
		}
		w := weights{now: now, values: reg.Hosts(), published: reg.Published()}
		for _, job := range next {
			if err := lockHost(ctx, tx, job.Host); err != nil {
				return err
			}
			command, refusal, err := recheck(ctx, tx, w, job)
			switch {
			case err != nil:
				return err
			case refusal != "":
				if err := endJob(ctx, tx, &job, "failed", map[string]string{"class": "in_use", "detail": refusal}, nil); err != nil {
					return err
				}
				continue
			case command == nil:
				continue // its env's key is not live: it waits
			}
			if _, err := tx.Exec(ctx, `UPDATE model_jobs SET released_at = clock_timestamp(), heard_at = clock_timestamp(), env = $2 WHERE id = $1`,
				job.ID, command.Env); err != nil {
				return err
			}
			if err := messaging.Enqueue(ctx, tx, messaging.Command{JobID: job.ID, OwnerID: job.RequestedBy,
				MsgID: job.ID + ":1", Data: *command}); err != nil {
				return err
			}
		}
		return nil
	})
}

// recheck applies a queued job's refusals again: its command, or the refusal's text; neither while the env's key is
// not live.
func recheck(ctx context.Context, tx pgx.Tx, w weights, job jobRow) (*message.ModelJob, string, error) {
	text := func(err error) (string, error) {
		var api *network.APIError
		if errors.As(err, &api) && api.Code < 500 {
			return api.Message, nil
		}
		return "", err
	}
	command := &message.ModelJob{Kind: job.Kind, Host: job.Host, Model: job.Model, Variant: job.Variant, LoraID: job.LoraID}
	var err error
	switch job.Kind {
	case "download":
		var d download
		if d, err = w.checkDownload(job.Host, deref(job.Model), job.Variant, true); err == nil {
			err = downloadDB(ctx, tx, job.Host, d, job.ID)
		}
		command.Env = d.env
		var accepted *message.JobLicence
		_ = json.Unmarshal(job.Licence, &accepted)
		command.Licence = accepted
	case "delete":
		var del removal
		if del, err = w.checkDelete(job.Host, deref(job.Model), job.Variant); err == nil {
			err = deleteDB(ctx, tx, w, job.Host, del)
		}
		command.Env = del.env
	case "lora_delete":
		var del loraDelete
		if del, err = w.checkLoraDelete(job.Host, deref(job.LoraID)); err == nil {
			err = loraDB(ctx, tx, w, job.Host, deref(job.LoraID), del)
		}
		command.Env = del.env
	}
	if err != nil {
		refusal, err := text(err)
		return nil, refusal, err
	}
	if key, ok := w.key(job.Host, command.Env); !ok || !key.Live(w.now) {
		return nil, "", nil
	}
	return command, "", nil
}

// RunModelJobs releases model jobs every Tick until ctx ends.
func RunModelJobs(ctx context.Context, pool *pgxpool.Pool, reg *registry.Registry, purge func(ctx context.Context, subject string) error) {
	tick := time.NewTicker(Tick)
	defer tick.Stop()
	for {
		if err := ReleaseModelJobs(ctx, pool, reg, purge, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("model jobs: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// ------------------------------------------------------------------ the projector of evt.model.*

// MountModels registers the handlers of evt.model.<host>.status and .progress (durable studio-models).
func (p *Projector) MountModels(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.model.*.status", modelHandler(p.pool, applyModelStatus))
	group.AddHandler("evt.model.*.progress", modelHandler(p.pool, applyModelProgress))
}

// modelApply projects one event onto its open job (locked).
type modelApply[D interface{ Base() message.ModelBase }] func(ctx context.Context, tx pgx.Tx, job jobRow, event *gen.Message[D]) error

// modelHandler decodes, projects once per (job_id, delivery, seq) and acks; a database failure naks.
func modelHandler[D interface{ Base() message.ModelBase }](pool *pgxpool.Pool, apply modelApply[D]) micro.NatsJetStreamHandlerFunc {
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
		if err := projectModel(ctx, pool, host, event, apply); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// projectModel applies the event when it is of an open job of the subject's host, asked by the envelope's owner, and
// new; anything else is logged (or silently, for an ended job) and skipped.
func projectModel[D interface{ Base() message.ModelBase }](ctx context.Context, pool *pgxpool.Pool, host string, event *gen.Message[D], apply modelApply[D]) error {
	base := event.Data.Base()
	if event.JobID == nil || !utility.IsUUID(*event.JobID) || base.Seq < 1 || base.Delivery < 1 {
		log.Printf("model projector: dropping %s without a job_id, seq or delivery", event.Type)
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		job, err := one[jobRow](ctx, tx, `SELECT `+jobCols+` FROM model_jobs WHERE id = $1 FOR UPDATE`, *event.JobID)
		if errors.Is(err, errNotFound) || (err == nil && (job.Host != host || job.RequestedBy != event.OwnerID)) {
			log.Printf("model projector: %s for unknown job %s (host %s)", event.Type, *event.JobID, host)
			return nil
		} else if err != nil || jobEnded[job.State] {
			return err
		}
		if first, err := messaging.FirstSeen(ctx, tx, job.ID, base.Delivery, base.Seq); err != nil || !first {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE model_jobs SET heard_at = clock_timestamp() WHERE id = $1`, job.ID); err != nil {
			return err
		}
		return apply(ctx, tx, job, event)
	})
}

// applyModelStatus: running starts the job (started_at the first time), a terminal status ends it.
func applyModelStatus(ctx context.Context, tx pgx.Tx, job jobRow, event *gen.Message[message.ModelStatus]) error {
	data := event.Data
	switch data.Status {
	case "running":
		_, err := tx.Exec(ctx, `UPDATE model_jobs SET state = 'running', started_at = coalesce(started_at, $2) WHERE id = $1`,
			job.ID, eventTime(event.OccurredAt))
		return err
	case "succeeded":
		return endJob(ctx, tx, &job, "succeeded", nil, nullable(data.Result))
	case "failed":
		var failure struct {
			Class  string `json:"class"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data.Failure, &failure) != nil || failure.Class == "" || failure.Detail == "" {
			failure.Class, failure.Detail = "internal", "the runner reported no reason"
		}
		return endJob(ctx, tx, &job, "failed", failure, nil)
	case "cancelled":
		return endJob(ctx, tx, &job, "cancelled", nil, nil)
	}
	log.Printf("model projector: job %s: unknown status %q", job.ID, data.Status)
	return nil
}

// applyModelProgress keeps a download's latest counts.
func applyModelProgress(ctx context.Context, tx pgx.Tx, job jobRow, event *gen.Message[message.ModelProgress]) error {
	d := event.Data
	progress := map[string]any{"done_bytes": d.DoneBytes, "total_bytes": d.TotalBytes, "files_done": d.FilesDone,
		"files_total": d.FilesTotal, "file": d.File, "eta_s": d.EtaS}
	_, err := tx.Exec(ctx, `UPDATE model_jobs SET progress = $2 WHERE id = $1`, job.ID, progress)
	return err
}

// nullable is raw as a value to store, nil for absent or JSON null.
func nullable(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// firstNonEmpty is the first of values that is not "".
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
