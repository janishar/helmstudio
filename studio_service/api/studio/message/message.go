// Package message: the NATS bodies studio sends and reads (contracts/messages): cmd.run.execute and evt.run.*.
package message

import "github.com/helmstudio/studio_service/registry"

// TypeExecute is the envelope type of a run command.
const TypeExecute = "cmd.run.execute"

// Execute is cmd.run.execute's data: the resolved graph, every param filled. Host and env pick the subject.
type Execute struct {
	RunID        string        `json:"run_id"`
	SessionID    string        `json:"session_id"`
	Attempt      int           `json:"attempt"`
	Workflow     GraphWorkflow `json:"workflow"`
	OutputPrefix string        `json:"output_prefix"`
	Host         string        `json:"-"`
	Env          string        `json:"-"`
}

// GraphWorkflow is the workflow a command carries.
type GraphWorkflow struct {
	ID    string                   `json:"id"`
	Nodes map[string]registry.Node `json:"nodes"`
}

// Type is the envelope type.
func (Execute) Type() string { return TypeExecute }

// Subject is cmd.run.<host>.<env>.
func (e Execute) Subject() string { return "cmd.run." + e.Host + "." + e.Env }

// Base is what every evt.run.* data has: seq restarts at 1 for each attempt; attempt absent is 1.
type Base struct {
	Seq     int `json:"seq"`
	Attempt int `json:"attempt"`
}

// Try is the event's attempt: 1 when absent.
func (b Base) Try() int { return max(b.Attempt, 1) }

// Common returns the base.
func (b Base) Common() Base { return b }

// Status is evt.run.status's data.
type Status struct {
	Base
	Status   string         `json:"status"` // running | succeeded | failed | cancelled
	Error    *string        `json:"error"`
	Host     *string        `json:"host"`
	Resolved map[string]any `json:"resolved"` // with succeeded: values the runner chose (a random seed)
}

// Progress is evt.run.progress's data.
type Progress struct {
	Base
	Node  string   `json:"node"`
	Step  int      `json:"step"`
	Of    int      `json:"of"`
	Phase *string  `json:"phase"`
	EtaS  *float64 `json:"eta_s"`
}

// Output is evt.run.output's data.
type Output struct {
	Base
	Index  int     `json:"index"`
	Key    string  `json:"key"`
	Kind   string  `json:"kind"`
	Width  *int    `json:"width"`
	Height *int    `json:"height"`
	Bytes  int64   `json:"bytes"`
	Etag   *string `json:"etag"`
	Seed   *int64  `json:"seed"`
}

// ------------------------------------------------------------------ batches (pipelines_service)

// SnapshotWorkflow is a registry workflow as cmd.pipeline.submit carries it (contracts defs workflowSnapshot).
type SnapshotWorkflow struct {
	ID     string                   `json:"id"`
	Title  string                   `json:"title"`
	Family string                   `json:"family"`
	Env    string                   `json:"env"`
	Nodes  map[string]registry.Node `json:"nodes"`
}

// Snapshot is what a batch runs: one workflow as it was at Submit.
type Snapshot struct {
	WorkflowID string           `json:"workflow_id"`
	Workflow   SnapshotWorkflow `json:"workflow"`
}

// SubmitRow is one row of cmd.pipeline.submit: its full inputs.
type SubmitRow struct {
	Row    int            `json:"row"`
	Inputs map[string]any `json:"inputs"`
}

// Submit is cmd.pipeline.submit's data.
type Submit struct {
	BatchID   string         `json:"batch_id"`
	SessionID string         `json:"session_id"`
	Snapshot  Snapshot       `json:"snapshot"`
	Inputs    map[string]any `json:"inputs"`
	Rows      []SubmitRow    `json:"rows"`
}

// Type is the envelope type.
func (Submit) Type() string { return "cmd.pipeline.submit" }

// Subject is cmd.pipeline.submit.
func (Submit) Subject() string { return "cmd.pipeline.submit" }

// Control is cmd.pipeline.pause, .resume, .stop (Row and Attempt nil) or .retry.
type Control struct {
	Kind    string `json:"-"` // pause | resume | stop | retry
	BatchID string `json:"batch_id"`
	Row     *int   `json:"row,omitempty"`
	Attempt *int   `json:"attempt,omitempty"`
}

// Type is cmd.pipeline.<kind>.
func (c Control) Type() string { return "cmd.pipeline." + c.Kind }

// Subject is the type.
func (c Control) Subject() string { return c.Type() }

// RunFinished is evt.studio.run_finished's data: a batch run reached a terminal status.
type RunFinished struct {
	RunID          string   `json:"run_id"`
	BatchID        string   `json:"batch_id"`
	Row            int      `json:"row"`
	IdempotencyKey string   `json:"idempotency_key"`
	Status         string   `json:"status"`
	TakeIDs        []string `json:"take_ids"`
	Error          *string  `json:"error"`
	SessionID      string   `json:"-"`
}

// Type is the envelope type.
func (RunFinished) Type() string { return "evt.studio.run_finished" }

// Subject is evt.studio.<session_id>.run_finished.
func (r RunFinished) Subject() string { return "evt.studio." + r.SessionID + ".run_finished" }

// PipelineBatch is evt.pipeline.batch's data.
type PipelineBatch struct {
	Seq    int64   `json:"seq"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// PipelineRow is evt.pipeline.row's data: the row's whole state.
type PipelineRow struct {
	Seq     int64    `json:"seq"`
	Row     int      `json:"row"`
	Attempt int      `json:"attempt"`
	Status  string   `json:"status"`
	RunID   *string  `json:"run_id"`
	TakeIDs []string `json:"take_ids"`
	Error   *string  `json:"error"`
}

// PipelineSeq is the event's seq (per batch, over both kinds).
func (b PipelineBatch) PipelineSeq() int64 { return b.Seq }

// PipelineSeq is the event's seq (per batch, over both kinds).
func (r PipelineRow) PipelineSeq() int64 { return r.Seq }

// RunsCreate is the studio.runs.create request (bare JSON).
type RunsCreate struct {
	OwnerID        string         `json:"owner_id"`
	SessionID      string         `json:"session_id"`
	WorkflowID     string         `json:"workflow_id"`
	Inputs         map[string]any `json:"inputs"`
	Lane           string         `json:"lane"`
	BatchID        string         `json:"batch_id"`
	Row            int            `json:"row"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// RunsCreated is studio.runs.create's reply for a run, new or already made under the key.
type RunsCreated struct {
	RunID   string   `json:"run_id"`
	Status  string   `json:"status"`
	Created bool     `json:"created"`
	TakeIDs []string `json:"take_ids"`
	Error   *string  `json:"error"`
}

// RunsCreateError is studio.runs.create's error reply; code is invalid | not_found | internal.
type RunsCreateError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}
