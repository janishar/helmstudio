package dto

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	gallery "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/magic"
)

// Magic boards (contracts/openapi.yaml MagicBoard*, MagicEdits, MagicEditResult, MagicRevision*, MagicSnapshot*,
// MagicAgentView; API 1.80, plan 39 phase C).

// MagicBoard is one board at its current revision.
type MagicBoard struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Revision  int       `json:"revision"`
	Doc       magic.Doc `json:"doc"`
	CanUndo   bool      `json:"can_undo"`
	CanRedo   bool      `json:"can_redo"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// API 1.81: the board's session, its blocks' runs, the document's takes and every block's status
	SessionID *string                `json:"session_id"`
	Runs      []MagicBlockRun        `json:"runs"`
	Takes     map[string]MagicTake   `json:"takes"`
	Status    map[string]MagicStatus `json:"status"`
	// API 1.83: the runs that make assets' views
	AssetRuns []MagicAssetRun `json:"asset_runs"`
	// API 1.91: who wrote the name, as a sequence's
	TitleSource string  `json:"title_source"`
	NamedBy     *string `json:"named_by"`
}

// MagicAssetRun is one run of Make views: an asset's view being made (API 1.83).
type MagicAssetRun struct {
	Asset     string    `json:"asset"`
	View      string    `json:"view"`
	RunID     string    `json:"run_id"`
	Status    string    `json:"status"`
	Progress  *int      `json:"progress,omitempty"`
	Error     *string   `json:"error"`
	Takes     []string  `json:"takes"`
	CreatedAt time.Time `json:"created_at"`
}

// MagicViewsCreate is POST …/assets/{asset_id}/views: the views to make on the board's revision.
type MagicViewsCreate struct {
	Revision int      `json:"revision"`
	Views    []string `json:"views"`
	Model    string   `json:"model,omitempty"`
	// API 1.98: every view's size and the model's own params, as a block request's
	Width    *int           `json:"width,omitempty"`
	Height   *int           `json:"height,omitempty"`
	Advanced map[string]any `json:"advanced,omitempty"`
}

// Validate requires the revision and 1-6 named views, each once.
func (b *MagicViewsCreate) Validate() string {
	if b.Revision < 1 {
		return "revision is required"
	}
	if len(b.Views) < 1 || len(b.Views) > 6 {
		return "views must be 1 to 6 names"
	}
	for i, v := range b.Views {
		if strings.TrimSpace(v) == "" || len([]rune(v)) > 40 || slices.Contains(b.Views[:i], v) {
			return "views are names of 1-40 characters, each once"
		}
	}
	for _, n := range []*int{b.Width, b.Height} {
		if n != nil && (*n < 16 || *n > 8192) {
			return "width and height must be 16 to 8192"
		}
	}
	return ""
}

// MagicViewStarted is a view's run; MagicViewsStarted is …/views' 202.
type MagicViewStarted struct {
	View  string `json:"view"`
	RunID string `json:"run_id"`
}
type MagicViewsStarted struct {
	Board   MagicBoard         `json:"board"`
	Started []MagicViewStarted `json:"started"`
}

// MagicStatus is a block's derived status (API 1.81).
type MagicStatus struct {
	Status   string `json:"status"` // draft | running | done | out_of_date | failed
	Progress *int   `json:"progress,omitempty"`
	Error    string `json:"error,omitempty"`
	// API 1.89, a dialogue only: each turn's own status and the take it plays
	Turns []MagicTurnStatus `json:"turns,omitempty"`
}

// MagicTurnStatus is one dialogue turn's status (API 1.89; the agent view's too, API 1.93).
type MagicTurnStatus = magic.TurnStatus

// MagicTake is a take the document names, as the board shows it.
type MagicTake struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	URL       string  `json:"url,omitempty"`
	ThumbURL  *string `json:"thumb_url"`
	PosterURL *string `json:"poster_url"`
	Seed      *int64  `json:"seed"`
	RunID     *string `json:"run_id"`
	Sound     *string `json:"sound,omitempty"` // API 1.85: an audio take's class
	// API 1.89: its seconds (a video or audio take) and a song's sections
	DurationS *float64               `json:"duration_s"`
	Sections  []gallery.AudioSection `json:"sections,omitempty"`
}

// MagicBlockRun is one run of a block (its run history).
type MagicBlockRun struct {
	Block      string    `json:"block"`
	RunID      string    `json:"run_id"`
	WorkflowID string    `json:"workflow_id"`
	Status     string    `json:"status"`
	Progress   *int      `json:"progress,omitempty"`
	Error      *string   `json:"error"`
	Takes      []string  `json:"takes"`
	Seed       *int64    `json:"seed"`
	Current    bool      `json:"current"`
	By         string    `json:"by"` // user | assistant
	CreatedAt  time.Time `json:"created_at"`
	RenderS    *float64  `json:"render_s"`
	// API 1.83: a board frame's style when it ran
	Style *string `json:"style,omitempty"`
	// API 1.89: a dialogue's run of this turn
	Turn *int `json:"turn,omitempty"`
	// API 1.94: the model variant it ran on, its title ("Turbo")
	Variant *string `json:"variant,omitempty"`
	// Tail is the seconds a music or sound block's run went past its block for a crossfade (API 1.106)
	Tail *float64 `json:"tail,omitempty"`
}

// MagicParam is one Advanced field of a model.
type MagicParam struct {
	Path  string `json:"path"`
	Param any    `json:"param"`
}

// MagicModel is a workflow that serves a kind.
type MagicModel struct {
	WorkflowID string            `json:"workflow_id"`
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	Env        string            `json:"env,omitempty"`    // API 1.89: Run on greys a host without it
	Family     string            `json:"family,omitempty"` // API 1.94: its model node's family (GET /models), absent without one
	Fields     []string          `json:"fields"`
	Greyed     map[string]string `json:"greyed"`
	Seconds    *float64          `json:"seconds"`
	Advanced   []MagicParam      `json:"advanced"`
	Video      *MagicVideo       `json:"video,omitempty"` // API 1.85: its workflow for a sound from a shot's video
}

// MagicVideo is a model's workflow for a sound made from a shot's video (MMAudio Sound from a video).
type MagicVideo struct {
	WorkflowID string       `json:"workflow_id"`
	Title      string       `json:"title"`
	Advanced   []MagicParam `json:"advanced"`
}

// MagicKindInfo is one kind: its shared fields and models.
type MagicKindInfo struct {
	Kind   string       `json:"kind"`
	Fields []string     `json:"fields"`
	Models []MagicModel `json:"models"`
	Off    string       `json:"off,omitempty"`
}

// MagicKinds is GET /magic/kinds.
type MagicKinds struct {
	Kinds []MagicKindInfo `json:"kinds"`
}

// MagicSettings is GET/PUT /magic/settings.
type MagicSettings struct {
	AskAboveGPUMinutes *float64 `json:"ask_above_gpu_minutes"`
}

// Validate requires 0..600 minutes.
func (b *MagicSettings) Validate() string {
	if b.AskAboveGPUMinutes == nil || *b.AskAboveGPUMinutes < 0 || *b.AskAboveGPUMinutes > 600 {
		return "ask_above_gpu_minutes must be 0 to 600"
	}
	return ""
}

// MagicPick names blocks: some, a lane's, or every one not done.
type MagicPick struct {
	Blocks []string `json:"blocks"`
	Lane   *string  `json:"lane"`
	Stale  *bool    `json:"stale"`
}

// Validate wants exactly one way to pick.
func (b *MagicPick) Validate() string {
	n := 0
	for _, set := range []bool{b.Blocks != nil, b.Lane != nil, b.Stale != nil} {
		if set {
			n++
		}
	}
	switch {
	case n != 1:
		return "give exactly one of blocks, lane, stale"
	case b.Blocks != nil && (len(b.Blocks) < 1 || len(b.Blocks) > 200):
		return "blocks: 1 to 200 ids"
	case b.Stale != nil && !*b.Stale:
		return "stale must be true"
	}
	return ""
}

// MagicRunCreate is POST …/run.
type MagicRunCreate struct {
	Revision int `json:"revision"`
	MagicPick
}

// Validate requires the revision and the pick.
func (b *MagicRunCreate) Validate() string {
	if b.Revision < 1 {
		return "revision is required"
	}
	return b.MagicPick.Validate()
}

// MagicStarted is a run started for a block; MagicSkipped one that could not.
type MagicStarted struct {
	Block string `json:"block"`
	RunID string `json:"run_id"`
}
type MagicSkipped struct {
	Block  string `json:"block"`
	Reason string `json:"reason"`
}

// MagicRunStarted is …/run's 202.
type MagicRunStarted struct {
	Board   MagicBoard     `json:"board"`
	Started []MagicStarted `json:"started"`
	Skipped []MagicSkipped `json:"skipped"`
}

// MagicPlanBlock is one block's estimate.
type MagicPlanBlock struct {
	Block   string   `json:"block"`
	Seconds *float64 `json:"seconds"`
	Source  string   `json:"source"` // yours | pack | unknown
	Host    *string  `json:"host"`
	PeakGB  *float64 `json:"peak_gb"`
	Reason  *string  `json:"reason"`
}

// MagicPlanLane is a lane's total.
type MagicPlanLane struct {
	Lane    string   `json:"lane"`
	Seconds *float64 `json:"seconds"`
	Blocks  int      `json:"blocks"`
}

// MagicPlan is …/plan.
type MagicPlan struct {
	Blocks       []MagicPlanBlock `json:"blocks"`
	Lanes        []MagicPlanLane  `json:"lanes"`
	Seconds      *float64         `json:"seconds"`
	Order        []string         `json:"order"`
	Ask          bool             `json:"ask"`
	AskText      string           `json:"ask_text"`
	LimitMinutes float64          `json:"limit_minutes"`
}

// MagicBoardSummary is a board in the list.
type MagicBoardSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Revision  int       `json:"revision"`
	Lanes     int       `json:"lanes"`
	Blocks    int       `json:"blocks"`
	Frames    int       `json:"frames"`
	FPS       int       `json:"fps"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MagicBoardList is GET /magic/boards.
type MagicBoardList struct {
	Boards     []MagicBoardSummary `json:"boards"`
	NextCursor *string             `json:"next_cursor"` // API 1.92
}

// boardName is "" or why name is not a board's or snapshot's name.
func boardName(name *string) string {
	if name != nil && (strings.TrimSpace(*name) == "" || utf8.RuneCountInString(*name) > 80) {
		return "name must be 1-80 characters"
	}
	return ""
}

// MagicBoardCreate is POST /magic/boards.
type MagicBoardCreate struct {
	Name   *string       `json:"name"`
	Target *magic.Target `json:"target"`
	Doc    *magic.Doc    `json:"doc"`
}

// Validate checks the name, and that doc and target are not both given.
func (b *MagicBoardCreate) Validate() string {
	switch {
	case b.Doc != nil && b.Target != nil:
		return "doc and target are not allowed together"
	case b.Target != nil:
		return magic.CheckTarget(*b.Target)
	}
	return boardName(b.Name)
}

// MagicBoardPatch is PATCH /magic/boards/{id}.
type MagicBoardPatch struct {
	Name *string `json:"name"`
}

// Validate requires the name.
func (b *MagicBoardPatch) Validate() string {
	if b.Name == nil {
		return "name is required"
	}
	return boardName(b.Name)
}

// MaxMagicOps is how many ops one /edits call may carry.
const MaxMagicOps = 200

// MagicEdits is POST /magic/boards/{id}/edits.
type MagicEdits struct {
	BaseRevision int        `json:"base_revision"`
	Ops          []magic.Op `json:"ops"`
	DryRun       bool       `json:"dry_run"`
}

// Validate checks the shape of every op (MagicOp); whether it applies is the ops'.
func (b *MagicEdits) Validate() string {
	switch {
	case b.BaseRevision < 1:
		return "base_revision is required"
	case len(b.Ops) == 0:
		return "ops must have 1 to 200 items"
	case len(b.Ops) > MaxMagicOps:
		return fmt.Sprintf("too_many_ops: at most %d ops per call", MaxMagicOps)
	}
	for i, op := range b.Ops {
		if why := magic.Shape(op); why != "" {
			return fmt.Sprintf("ops[%d]: %s", i, why)
		}
	}
	return ""
}

// MagicEditResult is /edits' answer.
type MagicEditResult struct {
	Revision int        `json:"revision"`
	DryRun   bool       `json:"dry_run"`
	Board    MagicBoard `json:"board"`
	Diff     magic.Diff `json:"diff"`
	Warnings []string   `json:"warnings"`
}

// MagicRevision is undo's and redo's body.
type MagicRevision struct {
	Revision int `json:"revision"`
}

// Validate requires the revision.
func (b *MagicRevision) Validate() string {
	if b.Revision < 1 {
		return "revision is required"
	}
	return ""
}

// MagicRestore is POST …/restore.
type MagicRestore struct {
	Revision int `json:"revision"`
	To       int `json:"to"`
}

// Validate requires both revisions.
func (b *MagicRestore) Validate() string {
	if b.Revision < 1 || b.To < 1 {
		return "revision and to are required"
	}
	return ""
}

// MagicRevisionItem is one kept revision.
type MagicRevisionItem struct {
	Revision   int       `json:"revision"`
	By         string    `json:"by"`
	Via        *string   `json:"via"`
	SavedAt    time.Time `json:"saved_at"`
	OpsSummary string    `json:"ops_summary"`
	Blocks     int       `json:"blocks"`
}

// MagicRevisionList is GET …/revisions.
type MagicRevisionList struct {
	Revisions []MagicRevisionItem `json:"revisions"`
}

// MagicSnapshot is one named snapshot.
type MagicSnapshot struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Revision int       `json:"revision"`
	MadeAt   time.Time `json:"made_at"`
	Blocks   int       `json:"blocks"`
}

// MagicSnapshotList is GET …/snapshots.
type MagicSnapshotList struct {
	Snapshots []MagicSnapshot `json:"snapshots"`
}

// MagicSnapshotCreate is POST …/snapshots.
type MagicSnapshotCreate struct {
	Name     *string `json:"name"`
	Revision *int    `json:"revision"`
}

// Validate checks the name and revision.
func (b *MagicSnapshotCreate) Validate() string {
	if b.Revision != nil && *b.Revision < 1 {
		return "revision must be 1 or more"
	}
	return boardName(b.Name)
}

// MagicSnapshotPatch is PATCH …/snapshots/{id}.
type MagicSnapshotPatch struct {
	Name *string `json:"name"`
}

// Validate requires the name.
func (b *MagicSnapshotPatch) Validate() string {
	if b.Name == nil {
		return "name is required"
	}
	return boardName(b.Name)
}

// MagicAgentView is GET …/{id}?view=agent.
type MagicAgentView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
	magic.Agent
}

// MagicVoiceDesign is POST …/assets/{asset_id}/voice/design (API 1.102): design_voice's input as a route's.
type MagicVoiceDesign struct {
	Revision int     `json:"revision"`
	Design   string  `json:"design"`
	Line     string  `json:"line"`
	Language *string `json:"language,omitempty"`
	VoiceID  *string `json:"voice_id,omitempty"`
}

// MagicVoiceSave is POST …/assets/{asset_id}/voice (API 1.102): save_voice's input with the voice-rights OK.
type MagicVoiceSave struct {
	Revision int    `json:"revision"`
	TakeID   string `json:"take_id"`
	Consent  bool   `json:"consent"`
}

// Validate requires the revision (design and line are design_voice's own checks).
func (b *MagicVoiceDesign) Validate() string {
	if b.Revision < 1 {
		return "revision: required"
	}
	return ""
}

// Validate requires the revision and the take.
func (b *MagicVoiceSave) Validate() string {
	switch {
	case b.Revision < 1:
		return "revision: required"
	case b.TakeID == "":
		return "take_id: required"
	}
	return ""
}
