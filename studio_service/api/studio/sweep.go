package studio

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Lease is how long an open run may show no sign of life (no event), or a cancel go unanswered, before the sweep
// looks at it.
const Lease = 30 * time.Second

// Confirm is how long a run must keep looking lost before the sweep ends it: one look at the hosts KV is not enough
// (a runner writes idle between two runs, and the view can lag).
const Confirm = 10 * time.Second

// Sweeper ends the runs whose lease is lost. lost holds when each run that looks lost was first seen so.
type Sweeper struct {
	pool  *pgxpool.Pool
	hosts *registry.Registry
	// backlog is how many events EVT still holds for the projector; while it has any, a run may only look stale.
	backlog func(ctx context.Context) (int, error)
	lost    map[string]time.Time
}

// NewSweeper returns a Sweeper; backlog nil means the projector never lags (tests).
func NewSweeper(pool *pgxpool.Pool, hosts *registry.Registry, backlog func(ctx context.Context) (int, error)) *Sweeper {
	return &Sweeper{pool: pool, hosts: hosts, backlog: backlog, lost: map[string]time.Time{}}
}

// Sweep ends, as System, every open run that has looked lost for Confirm up to now: no event within Lease (and, when
// a cancel was requested, no answer within Lease of it), and its host offline (or never live) or live but not on it
// (registry.Holds). A run waiting behind another on a live runner is left alone. A run with a cancel requested ends
// cancelled, any other failed. It does nothing while the projector has events to catch up on. Returns how many it
// ended.
func (s *Sweeper) Sweep(ctx context.Context, now time.Time) (int, error) {
	if s.backlog != nil {
		if pending, err := s.backlog(ctx); err != nil || pending > 0 {
			return 0, err
		}
	}
	ended := 0
	lost := map[string]time.Time{}
	err := common.System(ctx, s.pool, func(tx pgx.Tx) error {
		// ponytail: every stale open run in one pass (no LIMIT, so held ones can't starve the rest); page it if
		// thousands ever queue.
		runs, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE status IN ('queued', 'running')
			AND updated_at < clock_timestamp() - make_interval(secs => $1)
			AND (cancel_requested_at IS NULL OR cancel_requested_at < clock_timestamp() - make_interval(secs => $1))
			FOR UPDATE SKIP LOCKED`, Lease.Seconds())
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.Host != nil && s.hosts.Holds(*run.Host, run.Env, run.ID, run.Status == "queued", now) {
				continue // a long step, or a queue, on a live runner
			}
			first, seen := s.lost[run.ID]
			if !seen {
				first = now
			}
			if now.Sub(first) < Confirm {
				lost[run.ID] = first
				continue
			}
			status, failure := lostLease(run)
			if _, err := finish(ctx, tx, run, status, failure); err != nil {
				return err
			}
			ended++
		}
		return nil
	})
	if err == nil {
		s.lost = lost // runs no longer stale, or held again, start over
	}
	return ended, err
}

// lostLease is the status and error a run whose lease is lost ends with.
func lostLease(run model.Run) (string, *string) {
	if run.Cancelling {
		return "cancelled", nil
	}
	reason := "the runner stopped responding"
	if run.Status == "queued" {
		reason = "the runner lost the run before it started it"
	}
	return "failed", &reason
}

// Run sweeps every few seconds until ctx ends. It waits one Lease first, so the projector can catch up on the events
// that arrived while this service was down before any run looks stale.
func (s *Sweeper) Run(ctx context.Context) {
	wait := Lease
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 5 * time.Second
		if _, err := s.Sweep(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("lease sweep: %v", err)
		}
	}
}
