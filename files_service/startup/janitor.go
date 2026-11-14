package startup

import (
	"context"
	"log"
	"regexp"
	"time"

	"github.com/ora/orago/storage"
)

// The key patterns the janitor may delete.
var (
	workKey    = regexp.MustCompile(`^u/[^/]+/(catalog/[^/]+/)?work/`) // intermediate files: u/<owner>/work/, a session's work/
	archiveKey = regexp.MustCompile(`^u/[^/]+/archives/`)              // download zips
)

// Sweep deletes work files older than workAge and archives older than a day, with List + Delete only (the same on
// SeaweedFS and S3). ponytail: one listing of the whole bucket per run; list per owner if the bucket gets huge.
func Sweep(ctx context.Context, store storage.Store, now time.Time, workAge time.Duration) (int, error) {
	var old []string
	for info, err := range store.List(ctx, "u/") {
		if err != nil {
			return 0, err
		}
		age := now.Sub(info.LastModified)
		if (workKey.MatchString(info.Key) && age > workAge) || (archiveKey.MatchString(info.Key) && age > 24*time.Hour) {
			old = append(old, info.Key)
		}
	}
	if len(old) == 0 {
		return 0, nil
	}
	return len(old), store.Delete(ctx, old...)
}

// Janitor sweeps at start and then hourly until ctx ends. ponytail: every replica sweeps (deletes are idempotent, the
// service has no database for a lock); add a lock (a NATS KV key) if it ever runs many replicas.
func Janitor(ctx context.Context, store storage.Store, workAge time.Duration) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		n, err := Sweep(ctx, store, time.Now(), workAge)
		if err != nil && ctx.Err() == nil {
			log.Printf("janitor: %v", err)
		} else if n > 0 {
			log.Printf("janitor: deleted %d expired object(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
