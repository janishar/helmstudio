package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/ora/orago/utility"

	"github.com/ora/catalog_service/api/catalog/dto"
	"github.com/ora/catalog_service/api/catalog/model"
	"github.com/ora/catalog_service/common"
)

// Jobs returns the caller's newest 100 processing jobs and runs for the dev Jobs view. The caller transaction's
// row policy keeps it to the caller's own, whatever the role. ponytail: the last-event time scans each job's
// session events; an index on catalog_events (work_id) if the list gets slow.
func (s *Service) Jobs(ctx context.Context, caller common.Caller) (dto.JobList, error) {
	out := dto.JobList{Jobs: []dto.Job{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		jobs, err := collect[model.Job](ctx, tx, `SELECT `+model.JobCols+` FROM `+model.JobsFrom+`
			ORDER BY j.created_at DESC LIMIT 100`)
		for _, job := range jobs {
			out.Jobs = append(out.Jobs, jobDTO(job))
		}
		return err
	})
	return out, err
}

// Job returns one of the caller's jobs with its model usage (summed over all its usage events) and its events (at
// most 2000, oldest first), mapped as the session's event page maps them: an image event carries the image as it is
// now, with its tries and reviews.
func (s *Service) Job(ctx context.Context, caller common.Caller, jobID string) (dto.JobDetail, error) {
	var out dto.JobDetail
	if !utility.IsUUID(jobID) {
		return out, errNotFound
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT `+model.JobCols+` FROM `+model.JobsFrom+` WHERE j.id = $1`, jobID)
		job, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[model.Job])
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		st, err := loadState(ctx, tx, job.SessionID, false)
		if err != nil {
			return err
		}
		events, err := collect[model.Event](ctx, tx, `SELECT seq, type, work_id::text, ts, data FROM catalog_events
			WHERE session_id = $1 AND work_id = $2 ORDER BY seq LIMIT 2000`, job.SessionID, jobID)
		if err != nil {
			return err
		}
		models, err := collect[model.ModelUsage](ctx, tx, `SELECT data->>'kind', data->>'model', count(*)::int,
			coalesce(sum((data->>'input_tokens')::bigint), 0)::bigint, coalesce(sum((data->>'output_tokens')::bigint), 0)::bigint,
			coalesce(sum((data->>'cache_read_tokens')::bigint), 0)::bigint, sum((data->>'cost_usd')::float8),
			sum((data->>'credits')::float8), coalesce(sum((data->>'duration_ms')::bigint), 0)::bigint
			FROM catalog_events WHERE session_id = $1 AND work_id = $2 AND type = 'usage'
			GROUP BY 1, 2 ORDER BY 1 DESC, 2`, job.SessionID, jobID)
		out = dto.JobDetail{Job: jobDTO(job), Usage: usageDTO(models), Events: make([]dto.SessionEvent, 0, len(events))}
		for _, event := range events {
			out.Events = append(out.Events, s.eventDTO(st, event))
		}
		return err
	})
	return out, err
}

// usageDTO maps a job's per-model sums and adds the totals (LLM calls first, then image endpoints).
func usageDTO(models []model.ModelUsage) dto.JobUsage {
	out := dto.JobUsage{Models: make([]dto.ModelUsage, 0, len(models))}
	add := func(total **float64, v *float64) { // nil stays nil until a call carries a value
		if v == nil {
			return
		}
		if *total == nil {
			*total = new(0.0)
		}
		**total += *v
	}
	for _, m := range models {
		out.Models = append(out.Models, dto.ModelUsage{Kind: m.Kind, Model: m.Model, Calls: m.Calls,
			InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CacheReadTokens: m.CacheReadTokens,
			CostUSD: m.CostUSD, Credits: m.Credits, DurationMS: m.DurationMS})
		if m.Kind == "llm" {
			out.LLMCalls += m.Calls
		} else {
			out.ImageCalls += m.Calls
		}
		out.InputTokens += m.InputTokens
		out.OutputTokens += m.OutputTokens
		add(&out.CostUSD, m.CostUSD)
		add(&out.Credits, m.Credits)
	}
	return out
}

// jobDTO maps a job row to its wire form.
func jobDTO(job model.Job) dto.Job {
	return dto.Job{ID: job.ID, Kind: job.Kind, SessionID: job.SessionID, ProjectName: job.ProjectName, Scope: job.Scope,
		Status: job.Status, Error: job.Error, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt}
}
