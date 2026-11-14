// Package dto: the /v1/catalog request and response bodies (contracts/openapi.yaml, tag catalog). Request DTOs
// validate themselves (Validate returns the 422 detail); responses are built by api/catalog from model rows.
package dto

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ora/orago/utility"

	"github.com/ora/catalog_service/api/catalog/model"
	"github.com/ora/catalog_service/registry"
)

// Patterns of the contract's ids and values.
var (
	LibraryID  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	choiceID   = regexp.MustCompile(`^([a-z0-9][a-z0-9_-]{0,63}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	resolution = regexp.MustCompile(`^[0-9]+K$`)
	ratio      = regexp.MustCompile(`^[0-9]+:[0-9]+$`)
	option     = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	// CustomPoseID is a session custom shot's id (custom-<product singular>-<slug>)
	CustomPoseID = regexp.MustCompile(`^custom-[a-z0-9][a-z0-9-]{0,55}$`)
)

// FlowPositions are the values of Session.flow_position.
var FlowPositions = []string{"raw", "guidelines", "processing", "output", "poses", "models", "backgrounds", "details", "summary", "done"}

// IsChoiceID reports whether id is a library id or an upload UUID.
func IsChoiceID(id string) bool { return choiceID.MatchString(id) }

// IsLink reports whether value is an absolute http(s) URL of at most 2048 characters.
func IsLink(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// allUUIDs reports whether every id is a UUID.
func allUUIDs(ids []string) bool {
	for _, id := range ids {
		if !utility.IsUUID(id) {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------ session

// SessionCreate is the optional POST /sessions body.
type SessionCreate struct {
	ProjectName *string `json:"project_name"`
}

// Validate checks the project name's length.
func (b *SessionCreate) Validate() string {
	if b.ProjectName != nil && (len(*b.ProjectName) < 1 || len(*b.ProjectName) > 200) {
		return "project_name must be 1-200 characters"
	}
	return ""
}

// StepInputPatch is one step of a PATCH: the whole new item list and/or the skipped flag.
type StepInputPatch struct {
	Items   *[]model.Item `json:"items"`
	Skipped *bool         `json:"skipped"`
}

// SessionPatch is the PATCH /sessions/{id} body; only the fields sent change.
type SessionPatch struct {
	ProjectName  *string                   `json:"project_name"`
	FlowPosition *string                   `json:"flow_position"`
	Messages     *[]json.RawMessage        `json:"messages"`
	Inputs       map[string]StepInputPatch `json:"inputs"`
}

// Validate checks every field the contract bounds; upload ownership is checked by the service.
func (b *SessionPatch) Validate() string {
	if b.ProjectName == nil && b.FlowPosition == nil && b.Messages == nil && b.Inputs == nil {
		return "send at least one of project_name, flow_position, messages, inputs"
	}
	if b.ProjectName != nil && (len(*b.ProjectName) < 1 || len(*b.ProjectName) > 200) {
		return "project_name must be 1-200 characters"
	}
	if b.FlowPosition != nil && !contains(FlowPositions, *b.FlowPosition) {
		return "flow_position must be one of " + strings.Join(FlowPositions, ", ")
	}
	if b.Messages != nil {
		if len(*b.Messages) > 2000 {
			return "messages: at most 2000"
		}
		size := 0
		for _, raw := range *b.Messages {
			size += len(raw)
			var message struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &message) != nil || len(message.ID) < 1 || len(message.ID) > 64 || len(message.Kind) < 1 || len(message.Kind) > 32 {
				return "every message needs an id (1-64 characters) and a kind (1-32 characters)"
			}
		}
		if size > 2<<20 {
			return "messages: at most 2 MiB"
		}
	}
	if b.Inputs != nil && len(b.Inputs) == 0 {
		return "inputs: send at least one step"
	}
	for step, patch := range b.Inputs {
		if !contains(model.Steps, step) {
			return "inputs: unknown step " + step
		}
		if patch.Items == nil && patch.Skipped == nil {
			return "inputs." + step + ": send items or skipped"
		}
		if patch.Items == nil {
			continue
		}
		if len(*patch.Items) > 100 {
			return "inputs." + step + ": at most 100 items"
		}
		for _, item := range *patch.Items {
			if detail := ValidateItem(item); detail != "" {
				return "inputs." + step + ": " + detail
			}
		}
	}
	return ""
}

// ValidateItem checks one InputItem against the contract's bounds.
func ValidateItem(item model.Item) string {
	switch {
	case len(item.ID) < 1 || len(item.ID) > 64:
		return "item id must be 1-64 characters"
	case !option.MatchString(item.Option):
		return "item option must match ^[a-z][a-z-]{0,31}$"
	case utf8.RuneCountInString(item.Label) > 200 || (item.Detail != nil && utf8.RuneCountInString(*item.Detail) > 200):
		return "item label and detail are at most 200 characters"
	case item.URL != "" && !IsLink(item.URL):
		return "item url must be an http(s) link"
	case utf8.RuneCountInString(item.Text) > 20000:
		return "item text is at most 20000 characters"
	case len(item.UploadIDs) > 500 || !allUUIDs(item.UploadIDs):
		return "item upload_ids must be at most 500 upload ids"
	case item.ImageCount != nil && *item.ImageCount < 0:
		return "item image_count must be >= 0"
	case len(item.IDs) > 50:
		return "item ids: at most 50"
	case item.Resolution != "" && !resolution.MatchString(item.Resolution):
		return "item resolution must look like 2K"
	case item.Ratio != "" && !ratio.MatchString(item.Ratio):
		return "item ratio must look like 1:1"
	}
	for _, id := range item.IDs {
		if !IsChoiceID(id) {
			return "item ids must be library ids or upload ids"
		}
	}
	for category, poses := range item.Poses {
		if !LibraryID.MatchString(category) || len(poses) > 30 || !allLibraryIDs(poses) {
			return "item poses must map category ids to at most 30 pose ids"
		}
	}
	for category, brief := range item.Briefs {
		if !LibraryID.MatchString(category) || len(brief) > 5000 {
			return "item briefs must map category ids to at most 5000 characters"
		}
	}
	if len(item.CustomPoses) > 40 || len(item.PoseRefs) > 20 {
		return "item custom_poses: at most 40; pose_refs: at most 20"
	}
	for _, c := range item.CustomPoses {
		if !CustomPoseID.MatchString(c.ID) || !LibraryID.MatchString(c.Category) || strings.TrimSpace(c.Name) == "" ||
			utf8.RuneCountInString(c.Name) > 60 || strings.TrimSpace(c.Prompt) == "" || utf8.RuneCountInString(c.Prompt) > 500 {
			return "item custom_poses need a custom-… id, a category, a 1-60 character name and a 1-500 character prompt"
		}
		// one line each: a newline in a shot's words could pose as a line of the image brief
		if strings.ContainsFunc(c.Name+c.Prompt, unicode.IsControl) {
			return "item custom_poses: name and prompt must be one line"
		}
	}
	for upload, pose := range item.PoseRefs {
		if !utility.IsUUID(upload) || !LibraryID.MatchString(pose) {
			return "item pose_refs must map upload ids to pose ids"
		}
	}
	return ""
}

// allLibraryIDs reports whether every id matches the library id pattern.
func allLibraryIDs(ids []string) bool {
	for _, id := range ids {
		if !LibraryID.MatchString(id) {
			return false
		}
	}
	return true
}

// contains reports whether list holds value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Rules is what processing read from the documents, with reference images as signed links.
type Rules struct {
	Background      *string        `json:"background"`
	Styling         *string        `json:"styling"`
	Mood            *string        `json:"mood"`
	MetalReferences []MetalRefLink `json:"metal_references"`
	WornReferences  []string       `json:"worn_references"`
	Dimensions      *string        `json:"dimensions"`
	Notes           []string       `json:"notes"`
}

// MetalRefLink is a metal finish and its reference image link.
type MetalRefLink struct {
	Finish string `json:"finish"`
	URL    string `json:"url"`
}

// Processing is the session's newest processing job.
type Processing struct {
	Status    string  `json:"status"` // idle | queued | running | succeeded | failed
	ProcessID *string `json:"process_id"`
	Error     *string `json:"error"`
}

// Upload is a file stored for the session.
type Upload struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Category    *string   `json:"category"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session is the contract's CatalogSession.
type Session struct {
	ID           string          `json:"id"`
	ProjectName  string          `json:"project_name"`
	Stage        string          `json:"stage"`
	FlowPosition string          `json:"flow_position"`
	Inputs       model.Inputs    `json:"inputs"`
	Messages     json.RawMessage `json:"messages"`
	Rules        *Rules          `json:"rules"`
	Processing   Processing      `json:"processing"`
	Uploads      []Upload        `json:"uploads"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// SessionSummary is one row of GET /sessions.
type SessionSummary struct {
	ID          string    `json:"id"`
	ProjectName string    `json:"project_name"`
	Stage       string    `json:"stage"`
	SkuCount    int       `json:"sku_count"`
	Counts      Counts    `json:"counts"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Counts are a session's SKUs by status (running: Queued or Running; to_review: To review or Redo) and approved images.
type Counts struct {
	Skus           int `json:"skus"`
	NotRun         int `json:"not_run"`
	Running        int `json:"running"`
	ToReview       int `json:"to_review"`
	Approved       int `json:"approved"`
	ImagesApproved int `json:"images_approved"`
}

// Stats is GET /stats: the caller's totals for the account page.
type Stats struct {
	Sessions       int     `json:"sessions"`
	Skus           int     `json:"skus"`
	ImagesApproved int     `json:"images_approved"`
	CreditsQuoted  float64 `json:"credits_quoted"`
}

// Metrics is GET /metrics: render quality per product.
type Metrics struct {
	Products []ProductMetrics `json:"products"`
}

// ProductMetrics is one product's images that finished rendering; the rates and averages are nil when no image has
// the data (rendered before metrics were kept, or nothing approved yet).
type ProductMetrics struct {
	Product            string   `json:"product"`
	Images             int      `json:"images"`
	FirstPassRate      *float64 `json:"first_pass_rate"` // share whose first render passed review
	AvgTries           *float64 `json:"avg_tries"`       // renders per image
	Approved           int      `json:"approved"`
	Rejected           int      `json:"rejected"`
	FeedbackCount      int      `json:"feedback_count"`       // regenerations the brand asked for with feedback
	CreditsPerApproved *float64 `json:"credits_per_approved"` // credits × renders of every image over the approved ones
}

// SessionList is GET /sessions.
type SessionList struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionDetail is a session with everything hanging off it.
type SessionDetail struct {
	Session   Session    `json:"session"`
	Skus      []Sku      `json:"skus"`
	Questions []Question `json:"questions"`
	Runs      []Run      `json:"runs"`
	Images    []Image    `json:"images"`
}

// ------------------------------------------------------------------ memory, library, favorites, estimate

// MemoryEntry is the last-used items of one step.
type MemoryEntry struct {
	Step        string       `json:"step"`
	Items       []model.Item `json:"items"`
	ProjectName string       `json:"project_name"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Memory is GET /memory.
type Memory struct {
	Memory []MemoryEntry `json:"memory"`
}

// ResolutionOption is a resolution with its credits per image.
type ResolutionOption struct {
	ID              string  `json:"id"`
	Label           string  `json:"label"`
	CreditsPerImage float64 `json:"credits_per_image"`
}

// Favorites are the caller's favorite library ids by kind.
type Favorites struct {
	Models      []string `json:"models"`
	Backgrounds []string `json:"backgrounds"`
	Poses       []string `json:"poses"`
}

// Library is GET /library: the registry slice plus the caller's favorites. MaxAttempts is the renders per image at
// most, so the web can show the worst case next to every quote.
type Library struct {
	Families    []registry.Family     `json:"families"`
	Categories  []registry.Category   `json:"categories"`
	Poses       []registry.Pose       `json:"poses"`
	Models      []registry.Model      `json:"models"`
	Backgrounds []registry.Background `json:"backgrounds"`
	Resolutions []ResolutionOption    `json:"resolutions"`
	Ratios      []registry.Ratio      `json:"ratios"`
	Defaults    registry.Defaults     `json:"defaults"`
	Favorites   Favorites             `json:"favorites"`
	MaxAttempts int                   `json:"max_attempts"`
}

// Estimate is GET /estimate.
type Estimate struct {
	Images          int     `json:"images"`
	Resolution      string  `json:"resolution"`
	CreditsPerImage float64 `json:"credits_per_image"`
	Credits         float64 `json:"credits"`
}

// ------------------------------------------------------------------ inputs

// RawLink is the JSON form of POST /raw: a Drive folder, a website or CSV text.
type RawLink struct {
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	CsvText  string   `json:"csv_text"`
	Families []string `json:"families"`
}

// Validate checks the kind and its field; a bad link is the demo's "That doesn't look like a link."
func (b *RawLink) Validate() string {
	if msg := CheckFamilies(b.Families); msg != "" {
		return msg
	}
	switch b.Kind {
	case "drive", "website":
		if !IsLink(b.URL) {
			return "That doesn't look like a link."
		}
	case "csv":
		if strings.TrimSpace(b.CsvText) == "" || len(b.CsvText) > 1<<20 {
			return "csv_text must be 1 byte to 1 MiB"
		}
	default:
		return "kind must be drive, website or csv"
	}
	return ""
}

// CheckFamilies checks the shape of a raw upload's families (at most 20 library ids); the service checks the library.
func CheckFamilies(families []string) string {
	if len(families) > 20 || !allLibraryIDs(families) {
		return "families must be at most 20 family ids"
	}
	return ""
}

// RawAccepted is the 202 of POST /raw.
type RawAccepted struct {
	Session      Session    `json:"session"`
	Item         model.Item `json:"item"`
	ImageCount   *int       `json:"image_count"`
	ExpectedSkus *int       `json:"expected_skus"`
	ProcessID    string     `json:"process_id"`
}

// GuidelineLink is the JSON form of POST /guidelines.
type GuidelineLink struct {
	URL    string `json:"url"`
	Target string `json:"target"`
}

// Validate checks the link and the target (default guidelines).
func (b *GuidelineLink) Validate() string {
	if !IsLink(b.URL) {
		return "That doesn't look like a link."
	}
	if b.Target == "" {
		b.Target = "guidelines"
	}
	if b.Target != "guidelines" && b.Target != "details" {
		return "target must be guidelines or details"
	}
	return ""
}

// InputAdded is the 201 of POST /guidelines.
type InputAdded struct {
	Session Session    `json:"session"`
	Item    model.Item `json:"item"`
}

// PoseBrief is one category of a pose suggestion request.
type PoseBrief struct {
	Category  string   `json:"category"`
	Text      string   `json:"text"`
	UploadIDs []string `json:"upload_ids"`
	Exact     bool     `json:"exact"` // the text is the brand's exact shot list ("Describe your shots")
}

// PoseSuggestRequest is POST /poses/suggest.
type PoseSuggestRequest struct {
	Briefs []PoseBrief `json:"briefs"`
}

// Validate checks the briefs' bounds.
func (b *PoseSuggestRequest) Validate() string {
	if len(b.Briefs) < 1 || len(b.Briefs) > 50 {
		return "briefs: 1 to 50"
	}
	for _, brief := range b.Briefs {
		if !LibraryID.MatchString(brief.Category) || utf8.RuneCountInString(brief.Text) > 5000 || len(brief.UploadIDs) > 20 || !allUUIDs(brief.UploadIDs) {
			return "each brief needs a category id, text of at most 5000 characters and at most 20 upload ids"
		}
	}
	return ""
}

// PoseSuggestion is the answer of POST /poses/suggest.
type PoseSuggestion struct {
	Poses       map[string][]string `json:"poses"`
	CustomPoses []model.CustomPose  `json:"custom_poses"`
	Refs        map[string]string   `json:"refs"` // upload id -> the pose id that reference photo shows
	Reply       string              `json:"reply"`
}

// ------------------------------------------------------------------ SKUs, questions

// Sku is one SKU with its derived status and raw photo links.
type Sku struct {
	ID          string          `json:"id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Subcategory *string         `json:"subcategory"`
	GroupKey    *string         `json:"group_key"`
	GroupLabel  *string         `json:"group_label"`
	VariantOf   *string         `json:"variant_of"`
	Attributes  json.RawMessage `json:"attributes"`
	Confirmed   bool            `json:"confirmed"`
	Brief       model.Brief     `json:"brief"`
	Status      string          `json:"status"`
	RawURLs     []string        `json:"raw_urls"`
	RawViews    []*string       `json:"raw_views"` // the view of each raw photo (attributes.sides), nil = untagged
	UpdatedAt   time.Time       `json:"updated_at"`
}

// SkuList is a list of SKUs.
type SkuList struct {
	Skus []Sku `json:"skus"`
}

// SkuPatch is PATCH /skus/{id}.
type SkuPatch struct {
	Model           *string           `json:"model"`
	Background      *string           `json:"background"`
	Poses           []string          `json:"poses"`
	PoseRatios      map[string]string `json:"pose_ratios"` // replaces the brief's map
	PoseNotes       map[string]string `json:"pose_notes"`  // replaces the brief's map
	Category        *string           `json:"category"`    // move to this product
	Subcategory     Nullable          `json:"subcategory"` // null clears it
	Wearer          Nullable          `json:"wearer"`      // "women", "men"; null clears it
	Confirmed       *bool             `json:"confirmed"`
	ApplyToCategory bool              `json:"apply_to_category"`
	// the SKU's own descriptions (null or blank = none for this SKU)
	ModelDescription      Nullable `json:"model_description"`
	BackgroundDescription Nullable `json:"background_description"`
}

// Nullable is an optional string field that may be sent as null: Set tells a null (Set, Value nil) from an absent field.
type Nullable struct {
	Set   bool
	Value *string
}

// UnmarshalJSON runs only for a field that is present, null included, so it marks the field Set.
func (n *Nullable) UnmarshalJSON(data []byte) error {
	n.Set = true
	return json.Unmarshal(data, &n.Value)
}

// Trimmed is a sent field's trimmed text ("" for null), nil when it was not sent.
func (n Nullable) Trimmed() *string {
	if !n.Set {
		return nil
	}
	s := ""
	if n.Value != nil {
		s = strings.TrimSpace(*n.Value)
	}
	return &s
}

// Validate checks the ids' shapes; the service checks them against the library.
func (b *SkuPatch) Validate() string {
	if b.Model == nil && b.Background == nil && b.Poses == nil && b.PoseRatios == nil && b.PoseNotes == nil &&
		b.Category == nil && !b.Subcategory.Set && !b.Wearer.Set && b.Confirmed == nil && !b.ModelDescription.Set &&
		!b.BackgroundDescription.Set {
		return "send at least one of model, background, poses, pose_ratios, pose_notes, category, subcategory, wearer, " +
			"confirmed, model_description, background_description"
	}
	if w := b.Wearer.Value; w != nil && *w != "women" && *w != "men" {
		return "wearer must be women, men or null"
	}
	for _, d := range []Nullable{b.ModelDescription, b.BackgroundDescription} {
		if t := d.Trimmed(); t != nil && utf8.RuneCountInString(*t) > 500 {
			return "model_description and background_description: at most 500 characters"
		}
	}
	if len(b.PoseRatios) > 30 || len(b.PoseNotes) > 30 {
		return "pose_ratios and pose_notes: at most 30 poses"
	}
	for pose, value := range b.PoseRatios {
		if !LibraryID.MatchString(pose) || !ratio.MatchString(value) {
			return "pose_ratios must map pose ids to ratios like 4:5"
		}
	}
	for pose, note := range b.PoseNotes {
		if n := utf8.RuneCountInString(note); !LibraryID.MatchString(pose) || strings.TrimSpace(note) == "" || n > 500 {
			return "pose_notes must map pose ids to 1-500 characters, not blank"
		}
	}
	if b.Category != nil && !LibraryID.MatchString(*b.Category) {
		return "category must be a category id"
	}
	if b.Subcategory.Value != nil && !LibraryID.MatchString(*b.Subcategory.Value) {
		return "subcategory must be a subcategory id or null"
	}
	if (b.Model != nil && !IsChoiceID(*b.Model)) || (b.Background != nil && !IsChoiceID(*b.Background)) {
		return "model and background must be library ids or upload ids"
	}
	if b.Poses != nil && (len(b.Poses) < 1 || len(b.Poses) > 30 || !allLibraryIDs(b.Poses)) {
		return "poses must be 1 to 30 pose ids"
	}
	return ""
}

// Question is a processing question; Options are the two labels, Answer nil while open.
type Question struct {
	ID         string     `json:"id"`
	Check      string     `json:"check"`
	Text       string     `json:"text"`
	Options    []string   `json:"options"`
	Blocking   bool       `json:"blocking"`
	Answer     *int       `json:"answer"`
	AnsweredAt *time.Time `json:"answered_at"`
	SkuIDs     []string   `json:"sku_ids"`
	CreatedAt  time.Time  `json:"created_at"`
}

// AnswerRequest is POST /questions/{id}/answer.
type AnswerRequest struct {
	Option *int `json:"option"`
}

// Validate checks the option is 0 or 1.
func (b *AnswerRequest) Validate() string {
	if b.Option == nil || (*b.Option != 0 && *b.Option != 1) {
		return "option must be 0 or 1"
	}
	return ""
}

// Answered is the answer's result.
type Answered struct {
	Question Question `json:"question"`
	Skus     []Sku    `json:"skus"`
}

// CategoryCount is one category of the Start summary.
type CategoryCount struct {
	Category  string `json:"category"`
	Count     int    `json:"count"`
	FirstCode string `json:"first_code"`
	LastCode  string `json:"last_code"`
}

// Summary is the "{n} SKUs found" card.
type Summary struct {
	Total       int             `json:"total"`
	ByCategory  []CategoryCount `json:"by_category"`
	SampleSkuID *string         `json:"sample_sku_id"`
}

// Started is POST /start.
type Started struct {
	Session Session `json:"session"`
	Skus    []Sku   `json:"skus"`
	Summary Summary `json:"summary"`
}

// ------------------------------------------------------------------ runs, images

// Image is one image revision.
type Image struct {
	ID        string          `json:"id"`
	SkuID     string          `json:"sku_id"`
	RunID     string          `json:"run_id"`
	Shot      string          `json:"shot"`
	Revision  int             `json:"revision"`
	Status    string          `json:"status"`
	URL       *string         `json:"url"`
	Feedback  *string         `json:"feedback"`
	Review    json.RawMessage `json:"review"`
	Attempts  []ImageAttempt  `json:"attempts"`
	Error     *string         `json:"error"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ImageAttempt is one render try of an image, signed; Best is the one in Image.URL.
type ImageAttempt struct {
	Index  int             `json:"index"`
	URL    string          `json:"url"`
	Review json.RawMessage `json:"review"`
	Best   bool            `json:"best"`
	Picked bool            `json:"picked,omitempty"` // the brand chose it over the reviewer's best
}

// Run is one run.
type Run struct {
	ID            string     `json:"id"`
	Scope         string     `json:"scope"`
	Category      *string    `json:"category"`
	GroupKey      *string    `json:"group_key"`
	SkuIDs        []string   `json:"sku_ids"`
	ImageIDs      []string   `json:"image_ids"`
	CreditsQuoted float64    `json:"credits_quoted"`
	Feedback      *string    `json:"feedback"`
	Status        string     `json:"status"`
	Error         *string    `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// RunRequest is POST /runs.
type RunRequest struct {
	Scope       string   `json:"scope"`
	Category    string   `json:"category"`
	GroupKey    string   `json:"group_key"`
	SkuIDs      []string `json:"sku_ids"`
	MissingOnly bool     `json:"missing_only"`
}

// Validate checks the scope and the fields it needs.
func (b *RunRequest) Validate() string {
	if b.Category != "" && !LibraryID.MatchString(b.Category) {
		return "category must be a category id"
	}
	switch b.Scope {
	case "sample":
		if b.Category == "" {
			return "sample needs a category"
		}
	case "batch":
		if b.Category == "" || b.GroupKey == "" || len(b.GroupKey) > 64 {
			return "batch needs a category and a group_key (at most 64 characters)"
		}
	case "all":
	case "skus":
		if len(b.SkuIDs) < 1 || len(b.SkuIDs) > 500 || !allUUIDs(b.SkuIDs) {
			return "skus needs 1 to 500 sku_ids"
		}
	default:
		return "scope must be sample, batch, all or skus"
	}
	return ""
}

// RegenerateRequest is POST /regenerate.
type RegenerateRequest struct {
	ImageIDs []string `json:"image_ids"`
	Feedback string   `json:"feedback"`
	Ratio    string   `json:"ratio"` // render the new revisions at this ratio; empty = each shot's own
}

// Validate checks the ids, the feedback's length and the ratio's shape (the service checks it is in the library).
func (b *RegenerateRequest) Validate() string {
	if len(b.ImageIDs) < 1 || len(b.ImageIDs) > 500 || !allUUIDs(b.ImageIDs) {
		return "image_ids must be 1 to 500 image ids"
	}
	if feedback := strings.TrimSpace(b.Feedback); feedback == "" || utf8.RuneCountInString(b.Feedback) > 2000 {
		return "feedback must be 1-2000 characters"
	}
	if b.Ratio != "" && !ratio.MatchString(b.Ratio) {
		return "ratio must look like 4:5"
	}
	return ""
}

// RunAccepted is the 202 of a run or regeneration.
type RunAccepted struct {
	Run    Run     `json:"run"`
	Images []Image `json:"images"`
	Skus   []Sku   `json:"skus"`
}

// RunStopped is the answer of POST /sessions/{id}/runs/{run_id}/stop: the stopped run, its SKUs as they are now and
// the generating images it deleted (the web drops them).
type RunStopped struct {
	Run             Run      `json:"run"`
	Skus            []Sku    `json:"skus"`
	RemovedImageIDs []string `json:"removed_image_ids"`
}

// ImagesStop is POST /sessions/{id}/images/stop: the images to stop (a tile's one, a SKU row's generating ones).
type ImagesStop struct {
	ImageIDs []string `json:"image_ids"`
}

// Validate checks the ids.
func (b *ImagesStop) Validate() string {
	if len(b.ImageIDs) < 1 || len(b.ImageIDs) > 500 || !allUUIDs(b.ImageIDs) {
		return "image_ids must be 1 to 500 image ids"
	}
	return ""
}

// ImagesStopped is the answer of POST /sessions/{id}/images/stop: their SKUs as they are now, the runs it changed and
// the generating images it deleted.
type ImagesStopped struct {
	Skus            []Sku    `json:"skus"`
	Runs            []Run    `json:"runs"`
	RemovedImageIDs []string `json:"removed_image_ids"`
}

// ImageReview is PATCH /images/{id}.
type ImageReview struct {
	Status string `json:"status"`
}

// Validate checks the status.
func (b *ImageReview) Validate() string {
	if b.Status != "approved" && b.Status != "rejected" {
		return "status must be approved or rejected"
	}
	return ""
}

// ImagePick is POST /images/{id}/pick: the try (attempts[].index) to keep as the image.
type ImagePick struct {
	Attempt int `json:"attempt"`
}

// Validate checks the attempt is an index.
func (b *ImagePick) Validate() string {
	if b.Attempt < 1 {
		return "attempt must be an attempts index (1 or more)"
	}
	return ""
}

// Reviewed is the image and its SKU after a review.
type Reviewed struct {
	Image Image `json:"image"`
	Sku   Sku   `json:"sku"`
}

// ApproveRequest is POST /approve: exactly one of the three.
type ApproveRequest struct {
	ImageIDs []string `json:"image_ids"`
	SkuIDs   []string `json:"sku_ids"`
	Category string   `json:"category"`
}

// Validate checks exactly one selector is sent, with valid ids.
func (b *ApproveRequest) Validate() string {
	sent := 0
	for _, set := range []bool{b.ImageIDs != nil, b.SkuIDs != nil, b.Category != ""} {
		if set {
			sent++
		}
	}
	switch {
	case sent != 1:
		return "send exactly one of image_ids, sku_ids, category"
	case b.ImageIDs != nil && (len(b.ImageIDs) < 1 || len(b.ImageIDs) > 1000 || !allUUIDs(b.ImageIDs)):
		return "image_ids must be 1 to 1000 image ids"
	case b.SkuIDs != nil && (len(b.SkuIDs) < 1 || len(b.SkuIDs) > 500 || !allUUIDs(b.SkuIDs)):
		return "sku_ids must be 1 to 500 SKU ids"
	case b.Category != "" && !LibraryID.MatchString(b.Category):
		return "category must be a category id"
	}
	return ""
}

// Approved is what POST /approve changed.
type Approved struct {
	Approved int     `json:"approved"`
	Images   []Image `json:"images"`
	Skus     []Sku   `json:"skus"`
}

// NextAction is what a Next step button does.
type NextAction struct {
	Type     string `json:"type"` // run | download
	Scope    string `json:"scope,omitempty"`
	Category string `json:"category,omitempty"`
	GroupKey string `json:"group_key,omitempty"`
}

// NextOption is one button of the Next step card.
type NextOption struct {
	Label   string     `json:"label"`
	Action  NextAction `json:"action"`
	Credits *float64   `json:"credits"`
	// WorstCredits is Credits when every review fails: each image rendered its product's max attempts.
	WorstCredits *float64 `json:"worst_credits"`
}

// NextStep is the Next step card.
type NextStep struct {
	Heading string       `json:"heading"`
	State   string       `json:"state"` // next | in_progress | review | done
	Line    string       `json:"line"`
	Options []NextOption `json:"options"`
}

// ------------------------------------------------------------------ typed requests, events, download

// TypedRequest is POST /requests.
type TypedRequest struct {
	Text           string   `json:"text"`
	ImageIDs       []string `json:"image_ids"`
	ConfirmCredits *float64 `json:"confirm_credits"` // the quote the client showed; a run is queued only when it matches
}

// Validate checks the text and the selected ids.
func (b *TypedRequest) Validate() string {
	if strings.TrimSpace(b.Text) == "" || utf8.RuneCountInString(b.Text) > 2000 {
		return "text must be 1-2000 characters"
	}
	if len(b.ImageIDs) > 500 || !allUUIDs(b.ImageIDs) {
		return "image_ids must be at most 500 image ids"
	}
	if b.ConfirmCredits != nil && *b.ConfirmCredits < 0 {
		return "confirm_credits must be >= 0"
	}
	return ""
}

// PoseEditor tells the client to open the pose editor with the current poses ticked.
type PoseEditor struct {
	Category string   `json:"category"`
	Poses    []string `json:"poses"`
}

// Update holds the slices a typed request changed (absent = unchanged).
type Update struct {
	Run        *Run        `json:"run,omitempty"`
	Images     []Image     `json:"images,omitempty"`
	Skus       []Sku       `json:"skus,omitempty"`
	Download   *Download   `json:"download,omitempty"`
	PoseEditor *PoseEditor `json:"pose_editor,omitempty"`
	Quote      *Quote      `json:"quote,omitempty"`
}

// Quote is what a typed request would render and cost; nothing is queued until the client confirms it.
type Quote struct {
	Images       int     `json:"images"`
	Credits      float64 `json:"credits"`
	WorstCredits float64 `json:"worst_credits"` // every review fails: each image rendered its product's max attempts
}

// TypedResponse is what a typed request did.
type TypedResponse struct {
	Action string `json:"action"`
	Reply  string `json:"reply"`
	Update Update `json:"update"`
}

// SessionEvent is one stored event, its data with object keys turned into signed links and ids.
type SessionEvent struct {
	Seq    int64     `json:"seq"`
	Type   string    `json:"type"`
	WorkID *string   `json:"work_id"`
	TS     time.Time `json:"ts"`
	Data   any       `json:"data"`
}

// EventPage is the JSON form of GET /events.
type EventPage struct {
	Events  []SessionEvent `json:"events"`
	LastSeq int64          `json:"last_seq"`
}

// Download is the signed zip of the approved images.
type Download struct {
	URL      string `json:"url"`
	FileName string `json:"file_name"`
	Skus     int    `json:"skus"`
	Images   int    `json:"images"`
}

// ------------------------------------------------------------------ jobs (dev view)

// Job is a processing job or a run of the caller's.
type Job struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	SessionID   string    `json:"session_id"`
	ProjectName string    `json:"project_name"`
	Scope       *string   `json:"scope"`
	Status      string    `json:"status"`
	Error       *string   `json:"error"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// JobList is GET /jobs.
type JobList struct {
	Jobs []Job `json:"jobs"`
}

// JobDetail is GET /jobs/{id}: the job, its model usage and the events it produced, oldest first.
type JobDetail struct {
	Job    Job            `json:"job"`
	Usage  JobUsage       `json:"usage"`
	Events []SessionEvent `json:"events"`
}

// JobUsage is a job's model calls by model, with the totals; CostUSD and Credits are nil when none is known.
type JobUsage struct {
	Models       []ModelUsage `json:"models"`
	LLMCalls     int          `json:"llm_calls"`
	ImageCalls   int          `json:"image_calls"`
	InputTokens  int64        `json:"input_tokens"`
	OutputTokens int64        `json:"output_tokens"`
	CostUSD      *float64     `json:"cost_usd"`
	Credits      *float64     `json:"credits"`
}

// ModelUsage is one model's calls in a job.
type ModelUsage struct {
	Kind            string   `json:"kind"`
	Model           string   `json:"model"`
	Calls           int      `json:"calls"`
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	CacheReadTokens int64    `json:"cache_read_tokens"`
	CostUSD         *float64 `json:"cost_usd"`
	Credits         *float64 `json:"credits"`
	DurationMS      int64    `json:"duration_ms"`
}
