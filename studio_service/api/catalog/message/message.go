// Package message: the NATS bodies of Catalog Agent (contracts/messages/cmd.catalog.*, evt.catalog.*, req.catalog.*). Only
// orago/micro's helpers encode and decode them.
package message

import "encoding/json"

// Subjects. Commands go to CMD through the outbox; events arrive on evt.catalog.<session_id>.<type>; the two requests are
// core NATS request/reply answered by agents_service (queue group agents); agents.cancel is a core NATS broadcast to
// every agents replica whose body is the run id as plain text (not enveloped); agents.cancel.image likewise skips one
// image (shot) of a run, its body "<run_id> <image_id>".
const (
	SubjectProcess      = "cmd.catalog.process"
	SubjectRun          = "cmd.catalog.run"
	SubjectInterpret    = "catalog.interpret"
	SubjectPosesSuggest = "catalog.poses.suggest"
	SubjectCancel       = "agents.cancel"
	SubjectCancelImage  = "agents.cancel.image"
)

// ------------------------------------------------------------------ commands

// Process is cmd.catalog.process data: ingest new raw sources (none = re-run the checks), sort, describe, read documents.
type Process struct {
	SessionID    string   `json:"session_id"`
	Ingest       []Ingest `json:"ingest"`
	Guidelines   []DocRef `json:"guidelines"`
	Details      Details  `json:"details"`
	Choices      Choices  `json:"choices"`
	Skus         []SkuRef `json:"skus"`
	ExpectedSkus *int     `json:"expected_skus"`
	StartedSkus  int      `json:"started_skus"`       // of Skus, those of batches already started (before a Refresh)
	Families     []string `json:"families,omitempty"` // the families the brand is shooting; empty = every product
}

// Subject returns cmd.catalog.process.
func (Process) Subject() string { return SubjectProcess }

// Ingest is one raw source: stored files (keys), CSV rows (parsed here) or a link agents fetch.
type Ingest struct {
	Kind string   `json:"kind"` // files | csv | drive | website
	Keys []string `json:"keys,omitempty"`
	// Names are the uploaded files' names, one per key: the grouping hints keys (raw/<id>.<ext>) lose.
	Names []string `json:"names,omitempty"`
	Rows  []CsvRow `json:"rows,omitempty"`
	URL   string   `json:"url,omitempty"`
}

// CsvRow is one CSV line with an image URL.
type CsvRow struct {
	Sku      string `json:"sku"`
	Name     string `json:"name"`
	Category string `json:"category"`
	ImageURL string `json:"image_url"`
}

// DocRef is a guideline or brief document: a stored file (Key) or a link (URL).
type DocRef struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Details is the additional-details step: the notes and any brief documents.
type Details struct {
	Text string   `json:"text"`
	Docs []DocRef `json:"docs"`
}

// Choices are the output, pose, model and background inputs the checks compare against the library.
type Choices struct {
	Resolution  *string             `json:"resolution"`
	Ratio       *string             `json:"ratio"`
	Poses       map[string][]string `json:"poses"`
	Models      []string            `json:"models"`
	Backgrounds []string            `json:"backgrounds"`
}

// SkuRef is a SKU as catalog_service knows it. Attributes pass through as agents published them.
type SkuRef struct {
	SkuID       string          `json:"sku_id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Subcategory *string         `json:"subcategory"`
	Attributes  json.RawMessage `json:"attributes"`
	RawKeys     []string        `json:"raw_keys"`
}

// Run is cmd.catalog.run data: render each shot (its image row exists, generating), review, save to the image's key.
type Run struct {
	SessionID  string          `json:"session_id"`
	Scope      string          `json:"scope"`
	Resolution string          `json:"resolution"`
	Ratio      string          `json:"ratio"`
	Rules      json.RawMessage `json:"rules"` // null until processing read documents
	Details    string          `json:"details"`
	Feedback   *string         `json:"feedback"`
	Skus       []RunSku        `json:"skus"`
}

// Subject returns cmd.catalog.run.
func (Run) Subject() string { return SubjectRun }

// RunSku is one SKU of a run with its resolved model (nil for a product without one), background, shots, render
// resolution, its live images outside the run that passed review (agents pick an anchor among them) and the poses of
// the live ones that did not (a gated product's other shots wait while its anchor pose is among them).
type RunSku struct {
	SkuRef
	Model      *ChoiceRef `json:"model"`
	Background *ChoiceRef `json:"background"` // nil when the background is only described
	// the brand's descriptions of the model and background (the brief's), absent when none
	ModelDescription      *string  `json:"model_description,omitempty"`
	BackgroundDescription *string  `json:"background_description,omitempty"`
	ShotsBrief            *string  `json:"shots_brief,omitempty"` // the brand's shots brief for this product
	Shots                 []Shot   `json:"shots"`
	Resolution            string   `json:"resolution"`
	Passed                []Passed `json:"passed"`
	NotPassed             []string `json:"not_passed"`
}

// Passed is an earlier image of a SKU that passed review or was approved.
type Passed struct {
	PoseID string `json:"pose_id"`
	Key    string `json:"key"`
}

// ChoiceRef is a resolved model or background: built-in (Key nil) or a session upload (Key).
type ChoiceRef struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Key  *string `json:"key"`
}

// Shot is one image to render; RegenerateOf names the image a regeneration fixes.
type Shot struct {
	ImageID      string    `json:"image_id"`
	PoseID       string    `json:"pose_id"`
	PoseName     string    `json:"pose_name"`
	Revision     int       `json:"revision"`
	RegenerateOf *ImageRef `json:"regenerate_of"`
	Ratio        string    `json:"ratio,omitempty"` // this shot's ratio; empty = the run's
	Notes        string    `json:"notes,omitempty"` // the brand's instruction for this pose
	// a custom shot's own pose (nil for a library pose) and the brand's framing reference photos for this shot
	Prompt     string   `json:"prompt,omitempty"`
	OnModel    *bool    `json:"on_model,omitempty"`
	References []string `json:"references,omitempty"`
}

// ImageRef is an earlier image and its object key.
type ImageRef struct {
	ImageID string `json:"image_id"`
	Key     string `json:"key"`
}

// ------------------------------------------------------------------ events (the data of evt.catalog.<session_id>.<type>)

// Base holds the fields every catalog event's data has.
type Base struct {
	Seq       int    `json:"seq"`
	Attempt   int    `json:"attempt"`
	SessionID string `json:"session_id"`
}

// Common returns the shared fields; every event data embeds Base, so each has it.
func (b Base) Common() Base { return b }

// Status is evt.catalog.status data: a process or run job started, finished or failed.
type Status struct {
	Base
	Work   string  `json:"work"`   // process | run
	Status string  `json:"status"` // running | succeeded | failed
	Error  *string `json:"error"`
}

// Question is evt.catalog.question data: a check hit with two options and the SKU changes each makes.
type Question struct {
	Base
	Key      string           `json:"key"`
	Check    string           `json:"check"`
	Text     string           `json:"text"`
	Options  []QuestionOption `json:"options"`
	Blocking bool             `json:"blocking"`
	SkuCodes []string         `json:"sku_codes"`
}

// QuestionOption is one answer and what choosing it changes.
type QuestionOption struct {
	Label string  `json:"label"`
	Apply []Apply `json:"apply"`
}

// Apply is one SKU change of an option: a move to another product and its subcategory (raw JSON: null clears it,
// absent leaves it), brief fields, some attributes, confirmation, or folding it into another SKU.
type Apply struct {
	SkuCode     string          `json:"sku_code"`
	Category    string          `json:"category,omitempty"`
	Subcategory json.RawMessage `json:"subcategory,omitempty"`
	Brief       *BriefPatch     `json:"brief,omitempty"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
	Confirmed   *bool           `json:"confirmed,omitempty"`
	MergeInto   string          `json:"merge_into,omitempty"`
}

// BriefPatch is the brief fields an answer or an edit sets on a SKU (absent = unchanged).
type BriefPatch struct {
	Model      string   `json:"model,omitempty"`
	Background string   `json:"background,omitempty"`
	Poses      []string `json:"poses,omitempty"`
	// set only by SKU edits ("" = none); never in a question's apply
	ModelDescription      *string `json:"model_description,omitempty"`
	BackgroundDescription *string `json:"background_description,omitempty"`
}

// Skus is evt.catalog.skus data: the SKUs found (upserted by code), counts and the rules read from documents.
type Skus struct {
	Base
	Skus     []FoundSku      `json:"skus"`
	Found    int             `json:"found"`
	Expected *int            `json:"expected"`
	Rules    json.RawMessage `json:"rules"`
}

// FoundSku is one SKU of evt.catalog.skus; VariantOf is a code.
type FoundSku struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Subcategory *string         `json:"subcategory"`
	GroupKey    *string         `json:"group_key"`
	GroupLabel  *string         `json:"group_label"`
	VariantOf   *string         `json:"variant_of"`
	Attributes  json.RawMessage `json:"attributes"`
	Confirmed   bool            `json:"confirmed"`
	RawKeys     []string        `json:"raw_keys"`
}

// Image is evt.catalog.image data: one image rendering, ready for review (Key) or failed.
type Image struct {
	Base
	ImageID  string          `json:"image_id"`
	SkuID    string          `json:"sku_id"`
	Status   string          `json:"status"` // generating | to_review | failed
	Key      *string         `json:"key"`
	Review   json.RawMessage `json:"review"`
	Attempts int             `json:"attempts"` // renders made for this image
	// AttemptsDetail is every render with its key (images/<image_id>/attempt_<n>.<ext>) and review; absent = not kept.
	AttemptsDetail []Try   `json:"attempts_detail"`
	FirstPass      bool    `json:"first_pass"` // the first render passed review
	Error          *string `json:"error"`
}

// Try is one render of an image: its key, the review of it and whether it is the one kept (failed_goals is ignored).
type Try struct {
	N      int             `json:"n"`
	Key    string          `json:"key"`
	Review json.RawMessage `json:"review"`
	Best   bool            `json:"best"`
}

// RunProgress is evt.catalog.run data: per-SKU progress of a run.
type RunProgress struct {
	Base
	SkuID       string `json:"sku_id"`
	SkuStatus   string `json:"sku_status"`
	ImagesDone  int    `json:"images_done"`
	ImagesTotal int    `json:"images_total"`
	Line        string `json:"line"`
}

// ------------------------------------------------------------------ request/reply

// InterpretRequest asks agents to map a typed request to one action, given the session's state.
type InterpretRequest struct {
	OwnerID          string         `json:"owner_id"`
	SessionID        string         `json:"session_id"`
	Text             string         `json:"text"`
	SelectedImageIDs []string       `json:"selected_image_ids"`
	State            InterpretState `json:"state"`
}

// InterpretState is what the interpreter may refer to: categories with their poses, SKUs and the images waiting.
type InterpretState struct {
	Categories        []CategoryState `json:"categories"`
	Skus              []SkuState      `json:"skus"`
	LatestRunImageIDs []string        `json:"latest_run_image_ids"`
}

// CategoryState is one category with its current poses.
type CategoryState struct {
	Category    string       `json:"category"`
	Name        string       `json:"name"`
	Poses       []string     `json:"poses"`
	CustomPoses []CustomPose `json:"custom_poses,omitempty"` // the custom shots among Poses (their names)
}

// SkuState is one SKU as the interpreter sees it.
type SkuState struct {
	SkuID    string  `json:"sku_id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	GroupKey *string `json:"group_key"`
	Status   string  `json:"status"`
}

// InterpretReply is the action and reply, or Error.
type InterpretReply struct {
	Action string        `json:"action"`
	Reply  string        `json:"reply"`
	Args   InterpretArgs `json:"args"`
	Error  string        `json:"error"`
}

// InterpretArgs are the action's arguments (which ones depends on the action).
type InterpretArgs struct {
	Scope    string   `json:"scope"`
	Category string   `json:"category"`
	GroupKey string   `json:"group_key"`
	SkuIDs   []string `json:"sku_ids"`
	ImageIDs []string `json:"image_ids"`
	PoseID   string   `json:"pose_id"`
	Poses    []string `json:"poses"`
	Feedback string   `json:"feedback"`
}

// PosesSuggestRequest asks agents for pose ids per category from briefs, links or reference photos.
type PosesSuggestRequest struct {
	OwnerID   string      `json:"owner_id"`
	SessionID string      `json:"session_id"`
	Briefs    []PoseBrief `json:"briefs"`
}

// PoseBrief is one category's brief and reference photo keys.
type PoseBrief struct {
	Category      string   `json:"category"`
	Text          string   `json:"text"`
	ReferenceKeys []string `json:"reference_keys"`
	Exact         bool     `json:"exact,omitempty"` // the text is the brand's exact shot list
}

// PosesSuggestReply is the suggested poses, the custom shots among them, which shot each reference key shows and a
// reply, or Error.
type PosesSuggestReply struct {
	Poses       map[string][]string `json:"poses"`
	CustomPoses []CustomPose        `json:"custom_poses"`
	Refs        map[string]string   `json:"refs"`
	Reply       string              `json:"reply"`
	Error       string              `json:"error"`
}

// CustomPose is a custom shot agents suggest (contracts defs customPose).
type CustomPose struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	OnModel  bool   `json:"on_model"`
}
