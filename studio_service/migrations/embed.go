// Package migrations: goose SQL files (00001 platform: RLS function, outbox, projector dedupe; 00002 sessions, runs,
// run outputs and the per-session event log).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
