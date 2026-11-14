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

// Base is what every evt.run.* data has.
type Base struct {
	Seq int `json:"seq"`
}

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
	Index  int    `json:"index"`
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Width  *int   `json:"width"`
	Height *int   `json:"height"`
	Bytes  int64  `json:"bytes"`
	Seed   *int64 `json:"seed"`
}
