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

// Lease is how long an open run may show no sign of life (no event) before the sweep looks at it.
const Lease = 30 * time.Second

// Sweep ends the runs whose lease is lost, as System: an open run with no event within Lease whose host is offline (or
// was never live) or live but not on it (registry.Holds), or that was asked to cancel and did not answer. A run
// waiting behind another on a live runner is left alone. A run with a cancel requested ends cancelled, any other failed. Returns how many it ended.
func Sweep(ctx context.Context, pool *pgxpool.Pool, hosts *registry.Registry) (int, error) {
	ended := 0
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		runs, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE status IN ('queued', 'running')
			AND updated_at < clock_timestamp() - make_interval(secs => $1) ORDER BY updated_at LIMIT 100 FOR UPDATE SKIP LOCKED`,
			Lease.Seconds())
		if err != nil {
			return err
		}
		now := time.Now()
		for _, run := range runs {
			if !run.Cancelling && run.Host != nil && hosts.Holds(*run.Host, run.Env, run.ID, run.Status == "queued", now) {
				continue // a long step, or a queue, on a live runner
			}
			status, failure := lostLease(run)
			if _, err := finish(ctx, tx, run, status, failure); err != nil {
				return err
			}
			ended++
		}
		return nil
	})
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

// Sweeper runs Sweep every few seconds until ctx ends. It waits one Lease first, so the projector can catch up on the
// events that arrived while this service was down before any run looks stale.
func Sweeper(ctx context.Context, pool *pgxpool.Pool, hosts *registry.Registry) {
	wait := Lease
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 5 * time.Second
		if _, err := Sweep(ctx, pool, hosts); err != nil && ctx.Err() == nil {
			log.Printf("lease sweep: %v", err)
		}
	}
}
