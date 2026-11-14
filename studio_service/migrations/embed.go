// Package migrations: goose SQL files (00001 platform: RLS function, outbox, projector dedupe; 00002 sessions, runs,
// run outputs and the per-session event log; 00003 what the runner resolved: a run's resolved values, an output's seed;
// 00004 the dispatcher: runs.lane, nodes, released_at, projector dedupe per attempt; 00005 the gallery: takes, their lineage edges, run_outputs.take_id; 00006 dispatch_owners, each owner's last release; 00007 batches; 00008 takes.etag, take_parents.idx).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
