// Package model: the catalog_* rows and the JSON documents stored in them (never on the wire as rows; api/catalog
// maps them to the contract's shapes).
package model

import (
	"encoding/json"
	"time"
)

// Steps are the Input steps in flow order, the keys of Inputs.
var Steps = []string{"raw", "guidelines", "output", "poses", "models", "backgrounds", "details"}

// Item is one thing added to a step, stored as the client sent it (contract InputItem).
type Item struct {
	ID         string              `json:"id"`
	Option     string              `json:"option"`
	Label      string              `json:"label"`
	Detail     *string             `json:"detail,omitempty"`
	Reused     bool                `json:"reused,omitempty"`
	URL        string              `json:"url,omitempty"`
	Text       string              `json:"text,omitempty"`
	UploadIDs  []string            `json:"upload_ids,omitempty"`
	ImageCount *int                `json:"image_count,omitempty"`
	IDs        []string            `json:"ids,omitempty"`
	Randomize  bool                `json:"randomize,omitempty"`
	Resolution string              `json:"resolution,omitempty"`
	Ratio      string              `json:"ratio,omitempty"`
	Poses      map[string][]string `json:"poses,omitempty"`
	Briefs     map[string]string   `json:"briefs,omitempty"`
	// a poses brief's custom shots (their ids are in Poses) and which shot each reference photo (upload id) shows
	CustomPoses []CustomPose      `json:"custom_poses,omitempty"`
	PoseRefs    map[string]string `json:"pose_refs,omitempty"`
}

// CustomPose is a session shot the brand's brief asked for that the pose library lacks: rendered from its own prompt,
// never the anchor.
type CustomPose struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	OnModel  bool   `json:"on_model"`
}

// StepInput is one step's items and whether it was skipped.
type StepInput struct {
	Items   []Item `json:"items"`
	Skipped bool   `json:"skipped"`
}

// Inputs maps each of Steps to its input.
type Inputs map[string]StepInput

// EmptyInputs returns every step with no items, not skipped.
func EmptyInputs() Inputs {
	inputs := Inputs{}
	for _, step := range Steps {
		inputs[step] = StepInput{Items: []Item{}}
	}
	return inputs
}

// SessionCols selects a Session, in field order.
const SessionCols = `id::text, owner_id::text, project_name, stage, flow_position, inputs, messages, rules, expected_skus,
	created_at, updated_at`

// Session is one catalog_sessions row. Rules is nil until processing read documents.
type Session struct {
	ID           string
	OwnerID      string
	ProjectName  string
	Stage        string // setup | ready
	FlowPosition string
	Inputs       Inputs
	Messages     json.RawMessage
	Rules        json.RawMessage
	ExpectedSkus *int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Process is the newest catalog_processes row of a session.
type Process struct {
	ID     string
	Status string // queued | running | succeeded | failed
	Error  *string
}

// UploadCols selects an Upload, in field order.
const UploadCols = `id::text, kind, name, content_type, size, category, key, created_at`

// Upload is one catalog_uploads row: a file stored for a session.
type Upload struct {
	ID          string
	Kind        string
	Name        string
	ContentType string
	Size        int64
	Category    *string
	Key         string
	CreatedAt   time.Time
}

// Brief is what a SKU renders with (stored JSON; nil fields are filled by Start).
type Brief struct {
	Model      *string  `json:"model"`
	Background *string  `json:"background"`
	Poses      []string `json:"poses"`
	Resolution *string  `json:"resolution"`
	Ratio      *string  `json:"ratio"`
	// PoseRatios and PoseNotes map a pose id to its own ratio (over Ratio) and the brand's instruction for it.
	PoseRatios map[string]string `json:"pose_ratios"`
	PoseNotes  map[string]string `json:"pose_notes"`
	// ModelDescription and BackgroundDescription are the brand's words for the model and background (the step text,
	// else the SKU's own): with Model or Background nil they are the whole choice, else they add to it.
	ModelDescription      *string `json:"model_description,omitempty"`
	BackgroundDescription *string `json:"background_description,omitempty"`
	// ShotsBrief is the poses step's brief for the SKU's product (links removed, one line), set by Start
	ShotsBrief *string `json:"shots_brief,omitempty"`
	// CustomPoses are the definitions of the custom shots this SKU has used, so they render after the poses step changes
	CustomPoses []CustomPose `json:"custom_poses,omitempty"`
}

// Overrides are the brief fields set by question answers and SKU edits; Start keeps them over inputs and defaults.
type Overrides struct {
	Model      string   `json:"model,omitempty"`
	Background string   `json:"background,omitempty"`
	Poses      []string `json:"poses,omitempty"`
	// a SKU's own model or background description; "" = none for this SKU (over the step text)
	ModelDescription      *string `json:"model_description,omitempty"`
	BackgroundDescription *string `json:"background_description,omitempty"`
}

// SkuCols selects a Sku, in field order.
const SkuCols = `id::text, code, name, category, subcategory, group_key, group_label, variant_of::text, attributes,
	confirmed, brief, overrides, raw_keys, updated_at`

// Sku is one catalog_skus row.
type Sku struct {
	ID          string
	Code        string
	Name        string
	Category    string
	Subcategory *string
	GroupKey    *string
	GroupLabel  *string
	VariantOf   *string
	Attributes  json.RawMessage
	Confirmed   bool
	Brief       Brief
	Overrides   Overrides
	RawKeys     []string
	UpdatedAt   time.Time
}

// QuestionCols selects a Question, in field order.
const QuestionCols = `id::text, key, check_id, text, options, blocking, sku_codes, answer, answered_at, created_at`

// Question is one catalog_questions row; Options is the stored [{label, apply}].
type Question struct {
	ID         string
	Key        string
	Check      string
	Text       string
	Options    json.RawMessage
	Blocking   bool
	SkuCodes   []string
	Answer     *int
	AnsweredAt *time.Time
	CreatedAt  time.Time
}

// RunCols selects a Run, in field order.
const RunCols = `id::text, scope, category, group_key, sku_ids::text[], image_ids::text[], credits_quoted, feedback, status,
	error, created_at, finished_at`

// Run is one catalog_runs row.
type Run struct {
	ID            string
	Scope         string
	Category      *string
	GroupKey      *string
	SkuIDs        []string
	ImageIDs      []string
	CreditsQuoted float64
	Feedback      *string
	Status        string // queued | running | succeeded | failed
	Error         *string
	CreatedAt     time.Time
	FinishedAt    *time.Time
}

// ImageCols selects an Image, in field order.
const ImageCols = `id::text, sku_id::text, run_id::text, shot, revision, status, key, feedback, review, error, created_at,
	updated_at, tries`

// Image is one catalog_images row (one revision of a SKU × shot).
type Image struct {
	ID        string
	SkuID     string
	RunID     string
	Shot      string
	Revision  int
	Status    string // generating | to_review | approved | rejected | failed
	Key       *string
	Feedback  *string
	Review    json.RawMessage
	Error     *string
	CreatedAt time.Time
	UpdatedAt time.Time
	Tries     []Try // every render of this revision, in order; nil before tries were kept
}

// Try is one render of an image as agents reported it (catalog_images.tries): its key under the owner's prefix;
// Picked marks the try the brand chose as the image over the reviewer's best.
type Try struct {
	N      int             `json:"n"`
	Key    string          `json:"key"`
	Review json.RawMessage `json:"review"`
	Best   bool            `json:"best"`
	Picked bool            `json:"picked,omitempty"`
}

// Event is one catalog_events row: Data is what the projector stored (see projector.go).
type Event struct {
	Seq    int64
	Type   string
	WorkID *string
	TS     time.Time
	Data   json.RawMessage
}

// JobCols selects a Job from the jobs union (see JobsFrom), in field order.
const JobCols = `j.id::text, j.kind, j.session_id::text, s.project_name, j.scope, j.status, j.error, j.created_at,
	coalesce((SELECT max(e.ts) FROM catalog_events e WHERE e.session_id = j.session_id AND e.work_id = j.id), j.created_at)`

// JobsFrom is every processing job and run with its session, as j and s.
const JobsFrom = `(SELECT id, 'process' AS kind, session_id, NULL::text AS scope, status, error, created_at FROM catalog_processes
	UNION ALL SELECT id, 'run', session_id, scope, status, error, created_at FROM catalog_runs) j
	JOIN catalog_sessions s ON s.id = j.session_id`

// Job is a processing job or a run, for the dev Jobs view; UpdatedAt is its last event's time.
type Job struct {
	ID          string
	Kind        string // process | run
	SessionID   string
	ProjectName string
	Scope       *string // nil for a process
	Status      string
	Error       *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ModelUsage is one model's calls in a job, summed from its usage events; CostUSD and Credits are nil when no call
// carried one.
type ModelUsage struct {
	Kind            string // llm | image
	Model           string
	Calls           int
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	CostUSD         *float64
	Credits         *float64
	DurationMS      int64
}
