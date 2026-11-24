// Package message: the NATS bodies studio sends and reads (contracts/messages): cmd.run.execute and evt.run.*.
package message

import (
	"encoding/json"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/registry"
)

// TypeExecute is the envelope type of a run command.
const TypeExecute = "cmd.run.execute"

// Execute is cmd.run.execute's data: the resolved graph, every param filled. Host and env pick the subject.
type Execute struct {
	RunID        string        `json:"run_id"`
	SessionID    string        `json:"session_id"`
	Attempt      int           `json:"attempt"`
	Workflow     GraphWorkflow `json:"workflow"`
	OutputPrefix string        `json:"output_prefix"`
	BudgetGB     *float64      `json:"budget_gb,omitempty"` // the host's budget minus what its other envs hold
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
	// a text (ask) workflow's run: defs answer, with succeeded and with failed on the schema (API 1.14)
	Answer json.RawMessage `json:"answer"`
}

// Answer is defs answer as studio reads it (Run.answer): the tries, the thumbnails seen, the tokens and time.
type Answer struct {
	Tries []struct {
		Text  string  `json:"text"`
		Error *string `json:"error"`
	} `json:"tries"`
	Seen      []json.RawMessage `json:"seen"`
	TokensIn  int               `json:"tokens_in"`
	TokensOut int               `json:"tokens_out"`
	Seconds   float64           `json:"seconds"`
}

// Progress is evt.run.progress's data.
type Progress struct {
	Base
	Node  string   `json:"node"`
	Step  int      `json:"step"`
	Of    int      `json:"of"`
	Phase *string  `json:"phase"`
	EtaS  *float64 `json:"eta_s"`
	// API 1.36: which of the node's phases it is in
	PhaseID *string `json:"phase_id"`
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
	// kind video: its metadata and poster (required then, absent for an image)
	Video *dto.Video `json:"video"`
	// the values the runner chose for params the run set to "auto"
	Resolved *Resolved `json:"resolved"`
	// API 1.19, a video made from a media take: how its time lines up with that take (Take.source_map)
	SourceMap *dto.SourceMap `json:"source_map"`
	// API 1.30, kind audio only (required then): the WAV's length and sample rate
	Audio *dto.AudioInfo `json:"audio"`
	// API 1.48, an image or a video: its thumbnail <output_prefix><index>.thumb.webp
	Thumb *dto.Poster `json:"thumb"`
}

// Resolved is an output's resolved: "<node>.<param>" → the value the runner chose for "auto" (a predicted length).
type Resolved struct {
	Params map[string]int64 `json:"params"`
}

// Unload is req.runner.unload's request: the env about to load and the run waiting (both for the runner's log), and the
// entries to unload in the dispatcher's victim order.
type Unload struct {
	ForEnv     string      `json:"for_env"`
	RunID      string      `json:"run_id,omitempty"`
	Components []Component `json:"components,omitempty"`
	By         string      `json:"by,omitempty"`          // API 1.16: dispatcher (absent) | admin
	CachedOnly bool        `json:"cached_only,omitempty"` // API 1.16, by admin without components: cached handles only
}

// Component names one resident entry of a hosts value (no LoRAs = the plain component).
type Component struct {
	Component string          `json:"component"`
	Model     string          `json:"model"`
	Variant   string          `json:"variant"`
	Precision string          `json:"precision"`
	Compute   string          `json:"compute,omitempty"` // API 1.39 (absent = float32)
	Loras     []ComponentLora `json:"loras,omitempty"`
}

// ComponentLora is one LoRA baked into a component.
type ComponentLora struct {
	LoraID   string  `json:"lora_id"`
	Strength float64 `json:"strength"`
}

// UnloadReply is req.runner.unload's reply: {unloaded_gb, freed, resident}, or {error, code: busy | internal}.
type UnloadReply struct {
	UnloadedGB *float64             `json:"unloaded_gb"`
	Resident   *[]registry.Resident `json:"resident"` // what remains, as the hosts key the runner rewrote
	Freed      []Component          `json:"freed"`
	Error      string               `json:"error"`
	Code       string               `json:"code"`
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

// Snapshot is what a batch runs: one workflow as it was at Submit, or with Pipeline a saved pipeline (then the
// workflow is its first step's).
type Snapshot struct {
	WorkflowID string            `json:"workflow_id"`
	Workflow   SnapshotWorkflow  `json:"workflow"`
	Pipeline   *PipelineSnapshot `json:"pipeline,omitempty"`
}

// PipelineSnapshot is a saved pipeline as it was at Submit (contracts defs pipelineSnapshot): each node as a JSON
// object (a step's inputs merged over its preset, plus seed_param), the edges, every workflow a step uses.
type PipelineSnapshot struct {
	ID        string                      `json:"id"`
	Title     string                      `json:"title"`
	Version   int                         `json:"version"`
	Nodes     map[string]map[string]any   `json:"nodes"`
	Edges     json.RawMessage             `json:"edges"`
	Workflows map[string]SnapshotWorkflow `json:"workflows"`
	// API 1.15: every provider a node's source names, as it was at Submit; the pipeline's remote switch
	Providers map[string]map[string]any `json:"providers,omitempty"`
	Remote    any                       `json:"remote,omitempty"`
}

// SubmitRow is one row of cmd.pipeline.submit: its full inputs; a pipeline batch's also its base seed per step.
type SubmitRow struct {
	Row    int              `json:"row"`
	Inputs map[string]any   `json:"inputs"`
	Seeds  map[string]int64 `json:"seeds,omitempty"`
}

// Submit is cmd.pipeline.submit's data.
type Submit struct {
	BatchID   string         `json:"batch_id"`
	SessionID string         `json:"session_id"`
	Snapshot  Snapshot       `json:"snapshot"`
	Inputs    map[string]any `json:"inputs"`
	Rows      []SubmitRow    `json:"rows"`
	// Run again with the same decisions: the batch whose decisions are replayed (API 1.14)
	ReplayFrom *string `json:"replay_from,omitempty"`
}

// Type is the envelope type.
func (Submit) Type() string { return "cmd.pipeline.submit" }

// Subject is cmd.pipeline.submit.
func (Submit) Subject() string { return "cmd.pipeline.submit" }

// Control is cmd.pipeline.pause, .resume, .stop (Row and Attempt nil) or .retry (FromStep and CheckpointID: Retry row
// from here).
type Control struct {
	Kind         string  `json:"-"` // pause | resume | stop | retry
	BatchID      string  `json:"batch_id"`
	Row          *int    `json:"row,omitempty"`
	Attempt      *int    `json:"attempt,omitempty"`
	FromStep     *int    `json:"from_step,omitempty"`
	CheckpointID *string `json:"checkpoint_id,omitempty"`
}

// Type is cmd.pipeline.<kind>.
func (c Control) Type() string { return "cmd.pipeline." + c.Kind }

// Subject is the type.
func (c Control) Subject() string { return c.Type() }

// Pick is cmd.pipeline.pick's data: a gate's answer for one parked row (TakeIDs, or Reject).
type Pick struct {
	BatchID string   `json:"batch_id"`
	Row     int      `json:"row"`
	Attempt int      `json:"attempt"`
	Gate    string   `json:"gate"`
	Visit   int      `json:"visit"`
	TakeIDs []string `json:"take_ids,omitempty"`
	Reject  *bool    `json:"reject,omitempty"`
	Agreed  *bool    `json:"agreed,omitempty"` // a judge: the pick equals its suggestion (API 1.14)
	Note    *string  `json:"note,omitempty"`   // a judge: the user's note (a judge example)
}

// Type is the envelope type.
func (Pick) Type() string { return "cmd.pipeline.pick" }

// Subject is the type.
func (Pick) Subject() string { return "cmd.pipeline.pick" }

// RunFinished is evt.studio.run_finished's data: a batch run (API 1.43: or a session's naming run, BatchID and Row
// nil) reached a terminal status.
type RunFinished struct {
	RunID          string          `json:"run_id"`
	BatchID        *string         `json:"batch_id"`
	Row            *int            `json:"row"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	TakeIDs        []string        `json:"take_ids"`
	Error          *string         `json:"error"`
	Answer         json.RawMessage `json:"answer,omitempty"` // a text workflow's run: its answer (API 1.14)
	SessionID      string          `json:"-"`
}

// Type is the envelope type.
func (RunFinished) Type() string { return "evt.studio.run_finished" }

// Subject is evt.studio.<session_id>.run_finished.
func (r RunFinished) Subject() string { return "evt.studio." + r.SessionID + ".run_finished" }

// PipelineBatch is evt.pipeline.batch's data.
type PipelineBatch struct {
	Seq    int64           `json:"seq"`
	Status string          `json:"status"`
	Reason *string         `json:"reason"`
	Memory json.RawMessage `json:"memory"` // a pipeline batch's memory snapshot (API 1.14)
	// API 1.15: the director chat's state, and remote calls so far against the caps
	Director json.RawMessage `json:"director"`
	Remote   json.RawMessage `json:"remote"`
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
	// pipeline batches
	Node   *string         `json:"node"`
	Parked json.RawMessage `json:"parked"` // {gate, visit, keep, candidates} with waiting
	Cells  json.RawMessage `json:"cells"`  // [{node, kind, attempts: [{visit, status, run_ids, take_ids, …}]}]
}

// PipelineStep is evt.pipeline.step's data: one graph step of a pipeline row.
type PipelineStep struct {
	Seq          int64           `json:"seq"`
	Row          int             `json:"row"`
	Attempt      int             `json:"attempt"`
	Step         int             `json:"step"`
	Node         string          `json:"node"`
	Part         string          `json:"part"`
	Visit        int             `json:"visit"`
	Status       string          `json:"status"`
	What         string          `json:"what"`
	Changed      []string        `json:"changed"`
	Edge         json.RawMessage `json:"edge"`
	Loops        map[string]int  `json:"loops"`
	RunIDs       []string        `json:"run_ids"`
	TakeIDs      []string        `json:"take_ids"`
	Error        *string         `json:"error"`
	CheckpointID *string         `json:"checkpoint_id"`
	Decision     json.RawMessage `json:"decision"` // API 1.14: defs decision, on the part that derived it
	Memory       json.RawMessage `json:"memory"`   // API 1.14: {read, wrote}
	// API 1.15: a fanned-out step's branch; an agent's turn and, on tools, its calls with results
	Branch    *int            `json:"branch"`
	Turn      *int            `json:"turn"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

// PipelineSeq is the event's seq (per batch, over every kind).
func (s PipelineStep) PipelineSeq() int64 { return s.Seq }

// SubjectStateGet is pipelines' request/reply subject for a row's state at a checkpoint.
const SubjectStateGet = "pipelines.state.get"

// StateGet is the pipelines.state.get request (bare JSON).
type StateGet struct {
	OwnerID      string `json:"owner_id"`
	BatchID      string `json:"batch_id"`
	Row          int    `json:"row"`
	CheckpointID string `json:"checkpoint_id"`
}

// StateReply is its reply: checkpoint_id and state, or error and code (not_found | internal).
type StateReply struct {
	CheckpointID string         `json:"checkpoint_id"`
	State        map[string]any `json:"state"`
	Error        string         `json:"error"`
	Code         string         `json:"code"`
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
	Purpose        string         `json:"purpose"` // API 1.43: name = a session's hidden naming run (no batch)
}

// RunsCreated is studio.runs.create's reply for a run, new or already made under the key.
type RunsCreated struct {
	RunID   string          `json:"run_id"`
	Status  string          `json:"status"`
	Created bool            `json:"created"`
	TakeIDs []string        `json:"take_ids"`
	Error   *string         `json:"error"`
	Answer  json.RawMessage `json:"answer,omitempty"` // a text workflow's terminal run: its answer (defs answer)
}

// RunsCreateError is studio.runs.create's error reply; code is invalid | not_found | internal.
type RunsCreateError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// SubjectMemory is pipelines' request/reply subject for a pipeline's memory (API 1.14).
const SubjectMemory = "pipelines.memory"

// MemoryRequest is the pipelines.memory request (bare JSON): list, create, delete or clear, in the owner's namespace.
type MemoryRequest struct {
	OwnerID    string  `json:"owner_id"`
	Op         string  `json:"op"` // list | create | delete | clear
	PipelineID string  `json:"pipeline_id"`
	Kind       *string `json:"kind,omitempty"`    // list
	ItemID     *string `json:"item_id,omitempty"` // delete
	Text       *string `json:"text,omitempty"`    // create
	Key        *string `json:"key,omitempty"`     // create
}

// MemoryItem is defs memoryItem: its takes by id.
type MemoryItem struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	PipelineID string          `json:"pipeline_id"`
	Key        *string         `json:"key"`
	Text       string          `json:"text"`
	Kept       []string        `json:"kept"`
	Rejected   []string        `json:"rejected"`
	Agreed     *bool           `json:"agreed"`
	Source     json.RawMessage `json:"source"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

// MemoryReply is its reply: items (list), item (create), deleted (delete, clear), or error and code (not_found | full |
// invalid | internal).
type MemoryReply struct {
	Items   []MemoryItem `json:"items"`
	Item    *MemoryItem  `json:"item"`
	Deleted *int         `json:"deleted"`
	// Truncated: list only, older items left out to keep the reply under NATS max_payload
	Truncated bool   `json:"truncated"`
	Error     string `json:"error"`
	Code      string `json:"code"`
}

// ------------------------------------------------------------------ API 1.15: providers, tools, the director's chat

// SubjectProviderTest is pipelines' request/reply subject for testing a model provider.
const SubjectProviderTest = "pipelines.providers.test"

// ProviderTestRequest is the pipelines.providers.test request: the provider as a batch would call it (defs
// providerSnapshot) and, optionally, a model for one tiny completion.
type ProviderTestRequest struct {
	Name     string         `json:"name"`
	Provider map[string]any `json:"provider"`
	Model    *string        `json:"model,omitempty"`
}

// ProviderTestReply is its reply: {models, answer?, seconds} or {error, code: network | answer | refused | off | internal}.
type ProviderTestReply struct {
	Models  []string `json:"models"`
	Answer  *string  `json:"answer"`
	Seconds float64  `json:"seconds"`
	Error   string   `json:"error"`
	Code    string   `json:"code"`
	// API 1.53: per listed model, what its /models entry said of reasoning (OpenRouter's)
	Reasoning map[string]ModelReasoning `json:"reasoning"`
}

// ModelReasoning is one model's reasoning in a test reply: the efforts it lists, and whether it cannot be off.
type ModelReasoning struct {
	Efforts   []string `json:"efforts"`
	Mandatory bool     `json:"mandatory"`
}

// SubjectProviderKey is pipelines' request/reply subject for a key file's status (API 1.51).
const SubjectProviderKey = "pipelines.providers.key"

// ProviderKeyRequest is the pipelines.providers.key request.
type ProviderKeyRequest struct {
	KeySecret string `json:"key_secret"`
}

// ProviderKeyReply is its reply: {found, writable (API 1.52)} or {error, code: internal}.
type ProviderKeyReply struct {
	Found    bool   `json:"found"`
	Writable bool   `json:"writable"`
	Error    string `json:"error"`
	Code     string `json:"code"`
}

// SubjectProviderKeyWrite is pipelines' request/reply subject for saving or removing a key file (API 1.52): the only
// message that carries a key (core request/reply only, never in a stream).
const SubjectProviderKeyWrite = "pipelines.providers.key.write"

// ProviderKeyWriteRequest is its request: op save (Key, Provider: a snapshot without key_secret, Force) or remove.
// Never printed or logged.
type ProviderKeyWriteRequest struct {
	Op        string         `json:"op"`
	KeySecret string         `json:"key_secret"`
	Key       string         `json:"key,omitempty"`
	Provider  map[string]any `json:"provider,omitempty"`
	Force     bool           `json:"force,omitempty"`
}

// ProviderKeyWriteReply is its reply: {found, writable, saved?, check?, detail?} or {error, code: readonly | not_file |
// internal}.
type ProviderKeyWriteReply struct {
	Found    bool   `json:"found"`
	Writable bool   `json:"writable"`
	Saved    bool   `json:"saved"`
	Check    string `json:"check"`
	Detail   string `json:"detail"`
	Error    string `json:"error"`
	Code     string `json:"code"`
}

// ToolsRequest is the studio.tools request (pipelines → studio): one agent tool call, as the batch's owner; from API
// 1.44 an assistant chat's call, with ThreadID in place of BatchID, Row and Node.
type ToolsRequest struct {
	OwnerID  string          `json:"owner_id"`
	ThreadID string          `json:"thread_id"`
	BatchID  string          `json:"batch_id"`
	Row      int             `json:"row"`
	Node     string          `json:"node"`
	CallID   string          `json:"call_id"`
	Tool     string          `json:"tool"`
	Args     json.RawMessage `json:"args"`
}

// ToolsReply is its reply: {result, row?} or {error, code: invalid | conflict | cap | not_found | internal}.
type ToolsReply struct {
	Tool   string    `json:"tool,omitempty"` // API 1.44: an assistant call's ok reply names its tool
	Result any       `json:"result,omitempty"`
	Row    *ToolsRow `json:"row,omitempty"`
	Error  string    `json:"error,omitempty"`
	Code   string    `json:"code,omitempty"`
}

// ToolsRow is the stored row of a rows.add / rows.edit reply, as cmd.pipeline.submit carries rows but with seeds
// always present (the reply schema requires them; {} when no step has a seed param).
type ToolsRow struct {
	Row    int              `json:"row"`
	Inputs map[string]any   `json:"inputs"`
	Seeds  map[string]int64 `json:"seeds"`
}

// Chat is cmd.pipeline.chat's data: a user's message to the batch's director.
type Chat struct {
	BatchID   string `json:"batch_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	At        string `json:"at"`
}

// Type is the envelope type.
func (Chat) Type() string { return "cmd.pipeline.chat" }

// Subject is the type.
func (Chat) Subject() string { return "cmd.pipeline.chat" }

// PipelineChat is evt.pipeline.chat's data: one message of the director.
type PipelineChat struct {
	Seq     int64 `json:"seq"`
	Message struct {
		ID        string            `json:"id"`
		Role      string            `json:"role"` // director | notice
		Text      string            `json:"text"`
		Wake      int               `json:"wake"`
		ToolCalls []json.RawMessage `json:"tool_calls"`
		Rows      []int             `json:"rows"`
		At        string            `json:"at"`
	} `json:"message"`
}

// PipelineSeq is the event's seq (per batch, over every kind).
func (c PipelineChat) PipelineSeq() int64 { return c.Seq }

// ------------------------------------------------------------------ model jobs (API 1.16)

// ModelJob is cmd.model.job's data: one job for the runner of Host and Env (never a token, URL, path or bytes).
type ModelJob struct {
	Kind    string      `json:"kind"` // download | delete | lora_delete
	Model   *string     `json:"model,omitempty"`
	Variant *string     `json:"variant,omitempty"`
	Addon   *string     `json:"addon,omitempty"` // API 1.36: an optional add-on instead of a variant
	Licence *JobLicence `json:"licence,omitempty"`
	LoraID  *string     `json:"lora_id,omitempty"`
	Host    string      `json:"-"`
	Env     string      `json:"-"`
}

// JobLicence is the licence a download's admin accepted and the SHA-256 of its text.
type JobLicence struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// Type is the envelope type.
func (ModelJob) Type() string { return "cmd.model.job" }

// Subject is cmd.model.<host>.<env>.
func (j ModelJob) Subject() string { return "cmd.model." + j.Host + "." + j.Env }

// ModelBase is what every evt.model.* has: seq per delivery and the command's delivery count.
type ModelBase struct {
	Seq      int `json:"seq"`
	Delivery int `json:"delivery"`
}

// Base returns the base.
func (b ModelBase) Base() ModelBase { return b }

// ModelStatus is evt.model.status's data.
type ModelStatus struct {
	ModelBase
	Status  string          `json:"status"` // running | succeeded | failed | cancelled
	Failure json.RawMessage `json:"failure"`
	Result  json.RawMessage `json:"result"`
}

// ModelProgress is evt.model.progress's data (absolute counts).
type ModelProgress struct {
	ModelBase
	DoneBytes  int64    `json:"done_bytes"`
	TotalBytes *int64   `json:"total_bytes"`
	FilesDone  int      `json:"files_done"`
	FilesTotal *int     `json:"files_total"`
	File       *string  `json:"file"`
	EtaS       *float64 `json:"eta_s"`
}

// ------------------------------------------------------------------ API 1.28: a judge test

// SubjectJudgeTest is pipelines' request/reply subject for a judge test's template and verdict.
const SubjectJudgeTest = "pipelines.judge.test"

// JudgeTestMemory is the judge's memory read node as the prompt reads it.
type JudgeTestMemory struct {
	Key   *string `json:"key,omitempty"`
	Limit int     `json:"limit"`
}

// JudgeTestRequest is the pipelines.judge.test request: op prompt {owner_id, pipeline_id, node, criteria, n, brief,
// memory?} or op verdict {criteria, keep, take_ids, text}.
type JudgeTestRequest struct {
	Op         string           `json:"op"`
	OwnerID    string           `json:"owner_id,omitempty"`
	PipelineID string           `json:"pipeline_id,omitempty"`
	Node       string           `json:"node,omitempty"`
	Criteria   json.RawMessage  `json:"criteria"`
	N          int              `json:"n,omitempty"`
	Brief      *string          `json:"brief,omitempty"`
	Memory     *JudgeTestMemory `json:"memory,omitempty"`
	Keep       int              `json:"keep,omitempty"`
	TakeIDs    []string         `json:"take_ids,omitempty"`
	Text       *string          `json:"text,omitempty"`
}

// JudgeTestTake is one candidate's verdict in a verdict reply.
type JudgeTestTake struct {
	TakeID string `json:"take_id"`
	Checks []bool `json:"checks"`
	Reason string `json:"reason"`
	Passes bool   `json:"passes"`
	Rank   int    `json:"rank"`
}

// JudgeTestReply is its reply: {system, prompt, schema, max_tokens, template} (prompt), {verdict, first_failed,
// picked, takes} (verdict) or {error, code: answer | invalid | internal}.
type JudgeTestReply struct {
	System      string          `json:"system"`
	Prompt      string          `json:"prompt"`
	Schema      string          `json:"schema"`
	MaxTokens   int             `json:"max_tokens"`
	Verdict     string          `json:"verdict"`
	FirstFailed *string         `json:"first_failed"`
	Picked      []string        `json:"picked"`
	Takes       []JudgeTestTake `json:"takes"`
	Error       string          `json:"error"`
	Code        string          `json:"code"`
}

// Name is cmd.pipeline.name's data (API 1.43): name an untitled session from its first succeeded run's prompt.
type Name struct {
	SessionID string        `json:"session_id"`
	RunID     string        `json:"run_id"`
	Prompt    string        `json:"prompt"`
	Mode      string        `json:"mode"` // local | remote
	NamedBy   string        `json:"named_by"`
	Provider  *NameProvider `json:"provider,omitempty"` // remote only
}

// NameProvider is the provider a remote name is asked of, as it is now.
type NameProvider struct {
	Name     string         `json:"name"`
	Model    string         `json:"model"`
	Snapshot map[string]any `json:"snapshot"`
}

// Type is the envelope type.
func (Name) Type() string { return "cmd.pipeline.name" }

// Subject is the type.
func (Name) Subject() string { return "cmd.pipeline.name" }

// Named is evt.pipeline.named's data: a title for the session (API 1.43).
type Named struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Title     string `json:"title"`
	NamedBy   string `json:"named_by"`
}

// ------------------------------------------------------------------ the assistant (API 1.44)

// AssistantTurn is cmd.assistant.turn's data: one input to a chat.
type AssistantTurn struct {
	ThreadID string            `json:"thread_id"`
	Turn     int               `json:"turn"`
	ClientID string            `json:"client_id"`
	Roles    []string          `json:"roles"`
	Tools    []string          `json:"tools"`
	Actions  []string          `json:"actions"`
	Settings AssistantSettings `json:"settings"`
	Input    map[string]any    `json:"input"`
	Plugins  []TurnPlugin      `json:"plugins,omitempty"` // API 1.61: the assistant-tools plugins that are on
	Skills   []TurnSkill       `json:"skills,omitempty"`  // API 1.64: the skills that are on
}

// TurnSkill is one skill that is on, as a turn's prompt lists it (API 1.64).
type TurnSkill struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	When  string `json:"when"`
}

// TurnPlugin is one assistant-tools plugin as a turn offers it (API 1.61): its tools as in its manifest.
type TurnPlugin struct {
	Name  string          `json:"name"`
	Title string          `json:"title"`
	URL   string          `json:"url"`
	Tools json.RawMessage `json:"tools"`
}

// AssistantSettings is the admin's settings at an input: the model's source, the providers it names, search, caps.
type AssistantSettings struct {
	Source    map[string]any            `json:"source"`
	Providers map[string]map[string]any `json:"providers"`
	Search    *string                   `json:"search"`
	Limits    json.RawMessage           `json:"limits"`
	Sees      bool                      `json:"sees,omitempty"` // API 1.65: the model sees images
	// WindowTokens is the source model's chat window (API 1.66); 0 (absent) = pipelines' default
	WindowTokens int `json:"window_tokens,omitempty"`
}

// Type is cmd.assistant.turn.
func (AssistantTurn) Type() string { return "cmd.assistant.turn" }

// Subject is the type.
func (AssistantTurn) Subject() string { return "cmd.assistant.turn" }

// AssistantDelete is cmd.assistant.delete's data: a deleted chat.
type AssistantDelete struct {
	ThreadID string `json:"thread_id"`
}

// Type is cmd.assistant.delete.
func (AssistantDelete) Type() string { return "cmd.assistant.delete" }

// Subject is the type.
func (AssistantDelete) Subject() string { return "cmd.assistant.delete" }

// AssistantSteer is cmd.assistant.steer's data (API 1.59): a message sent while the turn works, or Stop.
type AssistantSteer struct {
	ThreadID string `json:"thread_id"`
	Turn     int    `json:"turn"`
	SteerID  string `json:"steer_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Stop     bool   `json:"stop,omitempty"`
	At       string `json:"at"`
}

// Type is cmd.assistant.steer.
func (AssistantSteer) Type() string { return "cmd.assistant.steer" }

// Subject is the type.
func (AssistantSteer) Subject() string { return "cmd.assistant.steer" }

// AssistantSteered is evt.assistant.steered's data (API 1.59): the steers a turn read, in order.
type AssistantSteered struct {
	ThreadID string   `json:"thread_id"`
	Turn     int      `json:"turn"`
	SteerIDs []string `json:"steer_ids"`
}

// AssistantStep is evt.assistant.step's data: one step of a turn.
type AssistantStep struct {
	ThreadID string            `json:"thread_id"`
	Turn     int               `json:"turn"`
	Seq      int               `json:"seq"`
	Text     string            `json:"text"`
	Lines    []json.RawMessage `json:"lines"`
	Actions  []json.RawMessage `json:"actions"`
	Cards    []json.RawMessage `json:"cards"`
	Done     bool              `json:"done"`
	Caps     json.RawMessage   `json:"caps"`
	Error    json.RawMessage   `json:"error"`
}

// SubjectAssistantMemory is pipelines' request/reply subject for the assistant's memory (the Memory list).
const SubjectAssistantMemory = "pipelines.assistant.memory"

// AssistantMemoryRequest is a pipelines.assistant.memory request, in owner_id's namespace only.
type AssistantMemoryRequest struct {
	OwnerID string `json:"owner_id"`
	Op      string `json:"op"` // list | edit | delete | clear
	Kind    string `json:"kind,omitempty"`
	ItemID  string `json:"item_id,omitempty"`
	Text    string `json:"text,omitempty"`
}

// AssistantMemoryItem is assistant.schema.json memoryItem.
type AssistantMemoryItem struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Text      string  `json:"text"`
	ThreadID  *string `json:"thread_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// SubjectAssistantCache is pipelines' request/reply subject for the assistant's prompt cache hits (API 1.66).
const SubjectAssistantCache = "pipelines.assistant.cache"

// AssistantCacheRequest is a pipelines.assistant.cache request.
type AssistantCacheRequest struct {
	Days int `json:"days"`
}

// AssistantCacheModel is one provider and model's model calls that reported their hits.
type AssistantCacheModel struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Calls        int64  `json:"calls"`
	PromptTokens int64  `json:"prompt_tokens"`
	HitTokens    int64  `json:"hit_tokens"`
}

// AssistantCacheReply is its reply: models, or {error, code}.
type AssistantCacheReply struct {
	Models []AssistantCacheModel `json:"models"`
	Error  string                `json:"error"`
	Code   string                `json:"code"`
}

// AssistantMemoryReply is its reply: items and counts, item, deleted, or {error, code}.
type AssistantMemoryReply struct {
	Items   []AssistantMemoryItem `json:"items"`
	Counts  map[string]int        `json:"counts"`
	Item    *AssistantMemoryItem  `json:"item"`
	Deleted *int                  `json:"deleted"`
	Error   string                `json:"error"`
	Code    string                `json:"code"`
}
