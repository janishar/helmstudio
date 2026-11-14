// Package model: the studio tables' rows, as read (never sent as they are: the service maps them to dto).
package model

import (
	"encoding/json"
	"time"
)

// Session is a sessions row.
type Session struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionCols are the columns Session scans, in order.
const SessionCols = `id::text, title, created_at, updated_at`

// Run is a runs row.
type Run struct {
	ID         string
	OwnerID    string
	SessionID  string
	WorkflowID string
	Env        string
	Inputs     json.RawMessage
	Status     string
	Progress   json.RawMessage // null before the first progress event
	Error      *string
	Host       *string
	Cancelling bool // cancel_requested_at is set
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// RunCols are the columns Run scans, in order.
const RunCols = `id::text, owner_id::text, session_id::text, workflow_id, env, inputs, status, progress, error, host,
	cancel_requested_at IS NOT NULL, created_at, started_at, finished_at`

// Output is a run_outputs row.
type Output struct {
	RunID  string
	Index  int
	Key    string
	Kind   string
	Width  *int
	Height *int
}

// OutputCols are the columns Output scans, in order.
const OutputCols = `run_id::text, idx, key, kind, width, height`

// Event is a run_events row.
type Event struct {
	Seq   int64
	Type  string
	RunID string
	TS    time.Time
	Data  json.RawMessage
}
