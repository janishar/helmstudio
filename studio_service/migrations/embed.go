// Package migrations: goose SQL files (00001 platform: RLS function, outbox, projector dedupe; 00002 sessions, runs,
// run outputs and the per-session event log; 00003 what the runner resolved: a run's resolved values, an output's seed).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
