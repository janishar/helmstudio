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
	Resolved   json.RawMessage // null until succeeded brings one
	Lane       string          // interactive | batch
	Attempt    int             // 1, or 2 after a lost lease
	Nodes      json.RawMessage // the graph resolved at create, sent at release
	ReleasedAt *time.Time      // when the dispatcher wrote this attempt's command; nil while waiting
	BatchID    *string         // the batch it runs a row of (lane batch)
	BatchRow   *int
	Key        *string // its studio.runs.create idempotency key
	// a user workflow's run: the version it ran and the workflow's title then (nil for a template)
	WorkflowVersion *int
	WorkflowTitle   *string
	Mentions        json.RawMessage // {typed, elements}: what the mention expansion did; null when nothing
	Loras           json.RawMessage // its LoRA values as resolved when it was made ([]lora.Used); null when none
	Looks           json.RawMessage // the look ids it was asked with (Run.looks); null when none
}

// RunCols are the columns Run scans, in order.
const RunCols = `id::text, owner_id::text, session_id::text, workflow_id, env, inputs, status, progress, error, host,
	cancel_requested_at IS NOT NULL, created_at, started_at, finished_at, resolved, lane, attempt, nodes, released_at,
	batch_id::text, batch_row, idempotency_key, workflow_version, workflow_title, mentions, loras, looks`

// Output is a run_outputs row.
type Output struct {
	RunID  string
	Index  int
	Key    string
	Kind   string
	Width  *int
	Height *int
	Seed   *int64
	TakeID *string // the take it became; nil only for outputs stored before the gallery
}

// OutputCols are the columns Output scans, in order.
const OutputCols = `run_id::text, idx, key, kind, width, height, seed, take_id::text`

// Event is a run_events row.
type Event struct {
	Seq   int64
	Type  string
	RunID string
	TS    time.Time
	Data  json.RawMessage
}

// Batch is a batches row with its rows counted by their shown status.
type Batch struct {
	ID            string
	OwnerID       string
	SessionID     *string
	Title         string
	WorkflowID    string
	WorkflowTitle string
	Status        string
	Control       string
	Reason        *string
	Commands      int
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	// a batch of a pipeline
	PipelineID      *string
	PipelineTitle   *string
	PipelineVersion *int
	Collection      *string
	Total           int
	Queued          int
	Running         int
	Waiting         int
	Succeeded       int
	Failed          int
	Cancelled       int
}

// RowShown is a row's status as the API shows it (br: batch_rows, r: its run): its end once pipelines reported one,
// waiting while parked on a gate, else running once its run left the queue, else queued.
const RowShown = `CASE WHEN br.status IN ('succeeded', 'failed', 'cancelled', 'waiting') THEN br.status
	WHEN r.status IS NOT NULL AND r.status <> 'queued' THEN 'running' ELSE 'queued' END`

// BatchFrom selects Batch's columns, in order, from batches aliased b; append WHERE … to it.
const BatchFrom = `SELECT b.id::text, b.owner_id::text, b.session_id::text, b.title, b.workflow_id, b.workflow_title, b.status,
	b.control, b.reason, b.commands, b.created_at, b.started_at, b.finished_at, b.pipeline_id::text, b.pipeline_title,
	b.pipeline_version, b.collection, c.* FROM batches b, LATERAL (
	SELECT count(*)::int, count(*) FILTER (WHERE s = 'queued')::int, count(*) FILTER (WHERE s = 'running')::int,
	count(*) FILTER (WHERE s = 'waiting')::int, count(*) FILTER (WHERE s = 'succeeded')::int, count(*) FILTER (WHERE s = 'failed')::int,
	count(*) FILTER (WHERE s = 'cancelled')::int
	FROM (SELECT ` + RowShown + ` AS s FROM batch_rows br LEFT JOIN runs r ON r.id = br.run_id WHERE br.batch_id = b.id) x) c `

// BatchRow is a batch_rows row as shown: status, the takes not deleted (ids and keys, in order), the run's start.
type BatchRow struct {
	Row        int
	Attempt    int
	Inputs     json.RawMessage
	Status     string
	RunID      *string
	TakeIDs    []string
	TakeKeys   []string
	Error      *string
	StartedAt  *time.Time
	FinishedAt *time.Time
	Pipeline   bool            // a row of a pipeline batch (its cells are never null)
	Node       *string         // pipeline rows: where it is now
	Parked     json.RawMessage // pipeline rows: {gate, visit, keep, candidates} while waiting
	Cells      json.RawMessage // pipeline rows: pipelines' cells
	Looks      json.RawMessage // a workflow batch's row: its look ids; null when none
}

// BatchRowFrom selects BatchRow's columns, in order, from batch_rows aliased br; append WHERE … to it.
const BatchRowFrom = `SELECT br.n, br.attempt, br.inputs, ` + RowShown + `, br.run_id::text, coalesce(tk.ids, '{}'),
	coalesce(tk.keys, '{}'), br.error, r.started_at, br.finished_at,
	br.cells IS NOT NULL, br.node, br.parked, br.cells, br.looks
	FROM batch_rows br LEFT JOIN runs r ON r.id = br.run_id
	LEFT JOIN LATERAL (SELECT array_agg(t.id::text ORDER BY u.ord) ids, array_agg(t.key ORDER BY u.ord) keys
		FROM unnest(br.take_ids) WITH ORDINALITY u(id, ord) JOIN takes t ON t.id = u.id AND t.deleted_at IS NULL) tk ON true `

// UserWorkflow is a user_workflows row.
type UserWorkflow struct {
	ID        string
	Title     string
	Family    string
	Env       string
	Nodes     json.RawMessage
	Studio    json.RawMessage // null: the derived form
	BasedOn   *string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserWorkflowCols are the columns UserWorkflow scans, in order.
const UserWorkflowCols = `id::text, title, family, env, nodes, studio, based_on, version, created_at, updated_at`

// UserPipeline is a user_pipelines row.
type UserPipeline struct {
	ID        string
	Title     string
	Nodes     json.RawMessage
	Edges     json.RawMessage
	BasedOn   *string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserPipelineCols are the columns UserPipeline scans, in order.
const UserPipelineCols = `id::text, title, nodes, edges, based_on, version, created_at, updated_at`

// BatchStep is a batch_steps row: one graph step of a pipeline row attempt.
type BatchStep struct {
	Step         int
	Node         string
	Part         string
	Visit        int
	Status       string
	What         string
	Changed      json.RawMessage
	Edge         json.RawMessage
	Loops        json.RawMessage
	RunIDs       []string
	TakeIDs      []string
	Error        *string
	CheckpointID *string
	At           time.Time
}

// BatchStepCols are the columns BatchStep scans, in order.
const BatchStepCols = `step, node, part, visit, status, what, changed, edge, loops, run_ids::text[], take_ids::text[], error,
	checkpoint_id, at`
