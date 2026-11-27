package dto

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/timeline"
)

// Timeline bodies (contracts/openapi.yaml Timeline*, ExportPlan, MediaJob, TakeFrame; API 1.17).

// ClipID is TimelineClipId.
var ClipID = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// TrackName is an addressable track: V1 or A2…A9.
var TrackName = regexp.MustCompile(`^(V1|A[2-9])$`)

// TimelineTargetIn is TimelineCreate.target: any field left out comes from the first take.
type TimelineTargetIn struct {
	Width      *int     `json:"width"`
	Height     *int     `json:"height"`
	FPS        *float64 `json:"fps"`
	SampleRate *int     `json:"sample_rate"`
}

// TimelineDefaults is what New shot's composer starts with.
type TimelineDefaults struct {
	Cast  []string `json:"cast"`
	Looks []string `json:"looks"`
}

// Validate: at most 16 distinct cast element ids and 8 look ids.
func (d *TimelineDefaults) Validate() string {
	if len(d.Cast) > 16 {
		return "defaults.cast: at most 16 items"
	}
	seen := map[string]bool{}
	for i, id := range d.Cast {
		if !utility.IsUUID(id) {
			return fmt.Sprintf("defaults.cast[%d]: must be an element id", i)
		}
		if seen[id] {
			return "defaults.cast: items must be unique"
		}
		seen[id] = true
	}
	return CheckLookIDs("defaults.looks", d.Looks)
}

// Filled is d with empty lists for absent ones.
func (d *TimelineDefaults) Filled() TimelineDefaults {
	out := TimelineDefaults{Cast: []string{}, Looks: []string{}}
	if d != nil {
		out.Cast, out.Looks = append(out.Cast, d.Cast...), append(out.Looks, d.Looks...)
	}
	return out
}

// TimelineCreate is POST /timelines.
type TimelineCreate struct {
	Name     *string           `json:"name"`
	Target   *TimelineTargetIn `json:"target"`
	TakeIDs  []string          `json:"take_ids"`
	Defaults *TimelineDefaults `json:"defaults"`
	From     *string           `json:"from_timeline_id"` // 1.76: clone this sequence
}

// Validate checks the shapes; the takes are checked by the service.
func (b *TimelineCreate) Validate() string {
	if b.From != nil && (b.Target != nil || b.TakeIDs != nil) {
		return "from_timeline_id: target and take_ids are not allowed with it"
	}
	if b.Name != nil && utf8.RuneCountInString(*b.Name) > 80 {
		return "name must be 1-80 characters"
	}
	if t := b.Target; t != nil {
		full := timeline.Target{Width: 16, Height: 16, FPS: 24, SampleRate: 48000}
		if t.Width != nil {
			full.Width = *t.Width
		}
		if t.Height != nil {
			full.Height = *t.Height
		}
		if t.FPS != nil {
			full.FPS = *t.FPS
		}
		if t.SampleRate != nil {
			full.SampleRate = *t.SampleRate
		}
		if detail := CheckTarget(full); detail != "" {
			return detail
		}
	}
	if len(b.TakeIDs) > 100 {
		return "take_ids: at most 100 items"
	}
	for i, id := range b.TakeIDs {
		if !utility.IsUUID(id) {
			return fmt.Sprintf("take_ids[%d]: take %s not found", i, id)
		}
	}
	if b.Defaults != nil {
		return b.Defaults.Validate()
	}
	return ""
}

// NameOrDefault is the trimmed name, "Untitled sequence" when empty.
func (b *TimelineCreate) NameOrDefault() string {
	if b.Name == nil || strings.TrimSpace(*b.Name) == "" {
		return "Untitled sequence"
	}
	return strings.TrimSpace(*b.Name)
}

// CheckTarget checks TimelineTarget's ranges.
func CheckTarget(t timeline.Target) string {
	switch {
	case t.Width < 16 || t.Width > 4096 || t.Width%2 != 0:
		return "target.width must be an even number from 16 to 4096"
	case t.Height < 16 || t.Height > 4096 || t.Height%2 != 0:
		return "target.height must be an even number from 16 to 4096"
	case t.SampleRate != 44100 && t.SampleRate != 48000:
		return "target.sample_rate must be 44100 or 48000"
	}
	if _, _, ok := timeline.Rate(t.FPS); !ok {
		return "target.fps must be one of 23.976, 24, 25, 29.97, 30, 48, 50, 59.94, 60"
	}
	return ""
}

// TimelineUpdate is PUT /timelines/{id}: the whole document at the revision it was made on.
type TimelineUpdate struct {
	Revision int               `json:"revision"`
	Target   *timeline.Target  `json:"target"`
	Tracks   []timeline.Track  `json:"tracks"`
	Markers  []timeline.Marker `json:"markers"` // 1.50: the whole list (absent = none)
}

// Validate checks the schema's shapes; the timeline rules are the service's.
func (b *TimelineUpdate) Validate() string {
	switch {
	case b.Revision < 1:
		return "revision is required"
	case b.Target == nil:
		return "target is required"
	case len(b.Tracks) < 1 || len(b.Tracks) > 9:
		return "tracks must have 1 to 9 items"
	}
	if detail := CheckTarget(*b.Target); detail != "" {
		return detail
	}
	for ti, t := range b.Tracks {
		at := fmt.Sprintf("tracks[%d]", ti)
		if t.Kind != "video" && t.Kind != "audio" {
			return at + ".kind must be video or audio"
		}
		if t.GainDB != nil && (*t.GainDB < -60 || *t.GainDB > 12) {
			return at + ".gain_db must be from -60 to 12"
		}
		if len(t.Clips) > 500 {
			return at + ".clips: at most 500 items"
		}
		for ci, c := range t.Clips {
			if detail := checkClip(c); detail != "" {
				return fmt.Sprintf("%s.clips[%d]%s", at, ci, detail)
			}
		}
	}
	for mi, m := range b.Markers { // 1.50; at most 200 is the rules' (too_many_markers)
		at := fmt.Sprintf("markers[%d]", mi)
		switch {
		case !ClipID.MatchString(m.ID):
			return at + ".id must be 1-16 lowercase letters or digits"
		case m.At < 0 || m.At > 86400:
			return at + ".at must be seconds from 0 to 86400"
		case m.Name != nil && (*m.Name == "" || utf8.RuneCountInString(*m.Name) > 80):
			return at + ".name must be 1-80 characters"
		case m.Color != nil && !slices.Contains(MarkerColors, *m.Color):
			return at + ".color must be one of " + strings.Join(MarkerColors, ", ")
		}
	}
	return ""
}

// checkClip checks one clip's shapes; the detail starts with the field (".gain_db …").
func checkClip(c timeline.Clip) string {
	seconds := func(p *float64) bool { return p == nil || (*p >= 0 && *p <= 86400) }
	switch {
	case !ClipID.MatchString(c.ID):
		return ".id must be 1-16 lowercase letters or digits"
	case c.TakeID != nil && !utility.IsUUID(*c.TakeID):
		return ".take_id must be a take id"
	case !seconds(c.In) || !seconds(c.Out) || !seconds(c.At):
		return ": in, out and at must be seconds from 0 to 86400"
	case c.Hold != nil && (*c.Hold <= 0 || *c.Hold > 3600):
		return ".hold must be more than 0 and at most 3600"
	case c.GainDB != nil && (*c.GainDB < -60 || *c.GainDB > 12):
		return ".gain_db must be from -60 to 12"
	case c.TransitionIn != nil && (c.TransitionIn.Type != "dissolve" || c.TransitionIn.Frames < 2 ||
		c.TransitionIn.Frames > 240 || c.TransitionIn.Frames%2 != 0):
		return ".transition_in must be a dissolve of an even 2 to 240 frames"
	case len(c.Alternatives) > 32:
		return ".alternatives: at most 32 items"
	}
	seen := map[string]bool{}
	for _, a := range c.Alternatives {
		if !utility.IsUUID(a) || seen[a] {
			return ".alternatives must be distinct take ids"
		}
		seen[a] = true
	}
	return ""
}

// TimelinePatch is PATCH /timelines/{id}.
type TimelinePatch struct {
	Name     *string           `json:"name"`
	Defaults *TimelineDefaults `json:"defaults"`
}

// Validate wants a field, a non-blank name of at most 80 characters.
func (b *TimelinePatch) Validate() string {
	switch {
	case b.Name == nil && b.Defaults == nil:
		return "name or defaults is required"
	case b.Name != nil && (strings.TrimSpace(*b.Name) == "" || utf8.RuneCountInString(*b.Name) > 80):
		return "name must be 1-80 characters"
	case b.Defaults != nil:
		return b.Defaults.Validate()
	}
	return ""
}

// TimelineRevision is undo's and redo's body.
type TimelineRevision struct {
	Revision int `json:"revision"`
}

// Validate wants a revision.
func (b *TimelineRevision) Validate() string {
	if b.Revision < 1 {
		return "revision is required"
	}
	return ""
}

// TimelineAddClips is POST /timelines/{id}/clips.
type TimelineAddClips struct {
	TakeIDs []string `json:"take_ids"`
	Track   string   `json:"track"`
}

// Validate wants 1 to 100 take ids and a track name.
func (b *TimelineAddClips) Validate() string {
	if len(b.TakeIDs) < 1 || len(b.TakeIDs) > 100 {
		return "take_ids must have 1 to 100 items"
	}
	for i, id := range b.TakeIDs {
		if !utility.IsUUID(id) {
			return fmt.Sprintf("take_ids[%d]: take %s not found", i, id)
		}
	}
	if b.Track == "" {
		b.Track = "V1"
	}
	if !TrackName.MatchString(b.Track) {
		return "track must be V1 or A2-A9"
	}
	return ""
}

// TimelineSlot is where a New shot goes: an existing clip, a new empty slot at the cut nearest at, or (API 1.19, a
// take action's insert after) a new empty slot right after V1 clip After.
type TimelineSlot struct {
	Clip   string   `json:"clip"`
	At     *float64 `json:"at"`
	Length *float64 `json:"length"`
	ID     string   `json:"id"`
	After  string   `json:"after"`
	Exact  bool     `json:"exact"` // API 1.32 (Make shot): the place edit at exactly frames(at)
}

// TimelineShotCreate is POST /timelines/{id}/shots.
type TimelineShotCreate struct {
	Slot *TimelineSlot `json:"slot"`
	Run  *RunRequest   `json:"run"`
}

// Validate checks the slot's form and the run's.
func (b *TimelineShotCreate) Validate() string {
	switch s := b.Slot; {
	case s == nil:
		return "slot is required"
	case b.Run == nil:
		return "run is required"
	case (s.Clip != "" && (s.At != nil || s.Length != nil || s.ID != "" || s.After != "")) || (s.After != "" && s.At != nil):
		return "slot is {clip}, {at, length, id?} or {after, length, id?}"
	case s.Clip != "" && !ClipID.MatchString(s.Clip):
		return "unknown clip " + s.Clip
	case s.After != "" && !ClipID.MatchString(s.After):
		return "unknown clip " + s.After
	case s.Exact && (s.At == nil || s.After != ""):
		return "slot.exact needs {at, length}"
	case s.Clip == "" && s.At == nil && s.After == "":
		return "slot.clip, slot.at or slot.after is required"
	case s.Clip == "" && s.Length == nil:
		return "length is required to insert a slot"
	case s.Clip == "" && (*s.Length <= 0 || *s.Length > 3600 || (s.At != nil && (*s.At < 0 || *s.At > 86400))):
		return "slot.at must be seconds from 0 and slot.length more than 0 and at most 3600"
	case s.ID != "" && !ClipID.MatchString(s.ID):
		return "slot.id must be 1-16 lowercase letters or digits"
	}
	return b.Run.Validate()
}

// TimelineLineSlot is where a New line goes (API 1.32): a placeholder on audio track Track at At of Length seconds,
// or audio clip Clip to redo.
type TimelineLineSlot struct {
	Clip   string   `json:"clip"`
	Track  string   `json:"track"`
	At     *float64 `json:"at"`
	Length *float64 `json:"length"`
	ID     string   `json:"id"`
}

// TimelineLineCreate is POST /timelines/{id}/lines (API 1.32, New line).
type TimelineLineCreate struct {
	Slot *TimelineLineSlot `json:"slot"`
	Run  *RunRequest       `json:"run"`
}

// Validate checks the slot's form and the run's.
func (b *TimelineLineCreate) Validate() string {
	switch s := b.Slot; {
	case s == nil:
		return "slot is required"
	case b.Run == nil:
		return "run is required"
	case s.Clip != "" && (s.Track != "" || s.At != nil || s.Length != nil || s.ID != ""):
		return "slot is {clip} or {track, at, length, id?}"
	case s.Clip != "" && !ClipID.MatchString(s.Clip):
		return "unknown clip " + s.Clip
	case s.Clip == "" && (s.Track == "" || s.At == nil || s.Length == nil):
		return "slot.clip or slot.track, slot.at and slot.length are required"
	case s.Clip == "" && s.Track == "V1":
		return "New line needs an audio track"
	case s.Clip == "" && !TrackName.MatchString(s.Track):
		return "slot.track must be A2-A9"
	case s.Clip == "" && (*s.Length <= 0 || *s.Length > 600 || *s.At < 0 || *s.At > 86400):
		return "slot.at must be seconds from 0 and slot.length more than 0 and at most 600"
	case s.ID != "" && !ClipID.MatchString(s.ID):
		return "slot.id must be 1-16 lowercase letters or digits"
	}
	return b.Run.Validate()
}

// TimelineRerender is POST /timelines/{id}/rerender.
type TimelineRerender struct {
	Revision int      `json:"revision"`
	Clips    []string `json:"clips"`
	Preset   *string  `json:"preset"`
}

// Validate checks the shapes.
func (b *TimelineRerender) Validate() string {
	switch {
	case b.Revision < 1:
		return "revision is required"
	case b.Clips != nil && (len(b.Clips) < 1 || len(b.Clips) > timeline.MaxRerender):
		return fmt.Sprintf("at most %d clips per re-render", timeline.MaxRerender)
	case b.Preset != nil && (*b.Preset == "" || utf8.RuneCountInString(*b.Preset) > 40):
		return "preset must be 1-40 characters"
	}
	seen := map[string]bool{}
	for _, id := range b.Clips {
		if !ClipID.MatchString(id) {
			return "unknown clip " + id
		}
		if seen[id] {
			return "clips must be unique"
		}
		seen[id] = true
	}
	return ""
}

// TimelineExportCreate is the export's and the plan's body.
type TimelineExportCreate struct {
	Revision *int    `json:"revision"`
	Host     *string `json:"host"`
}

// Validate checks the shapes.
func (b *TimelineExportCreate) Validate() string {
	switch {
	case b.Revision != nil && *b.Revision < 1:
		return "revision must be at least 1"
	case b.Host != nil && !HostName.MatchString(*b.Host):
		return "host must be a host name"
	}
	return ""
}

// TakeFrameRequest is POST /takes/{id}/frame.
type TakeFrameRequest struct {
	At string `json:"at"`
}

// Validate wants first or last.
func (b *TakeFrameRequest) Validate() string {
	if b.At != "first" && b.At != "last" {
		return "at must be first or last"
	}
	return ""
}

// TakeMuxRequest is POST /takes/{id}/mux (API 1.79): the video take whose picture keeps this audio take as its sound.
type TakeMuxRequest struct {
	VideoTakeID string `json:"video_take_id"`
}

// Validate wants a take id.
func (b *TakeMuxRequest) Validate() string {
	if !utility.IsUUID(b.VideoTakeID) {
		return "video_take_id must be a take id"
	}
	return ""
}

// TakeMux is its 202: the mux job.
type TakeMux struct {
	Job MediaJob `json:"job"`
}

// ------------------------------------------------------------------ answers

// TimelineTake is a take the document names, signed.
type TimelineTake struct {
	ID            string                `json:"id"`
	Kind          string                `json:"kind"`
	Source        string                `json:"source"`
	Title         string                `json:"title"`
	URL           string                `json:"url"`
	ThumbURL      string                `json:"thumb_url"`
	Width         *int                  `json:"width"`
	Height        *int                  `json:"height"`
	Video         *gallerydto.VideoInfo `json:"video,omitempty"`
	Audio         *gallerydto.AudioInfo `json:"audio,omitempty"` // API 1.32: an audio take's length, rate and cues
	Seed          *int64                `json:"seed"`
	WorkflowID    *string               `json:"workflow_id"`
	WorkflowTitle *string               `json:"workflow_title"`
	RenderS       *float64              `json:"render_s,omitempty"` // API 1.60: made_with.render_s
}

// TimelineProblem is one broken rule.
type TimelineProblem struct {
	Code    string  `json:"code"`
	Track   *string `json:"track"`
	Clip    *string `json:"clip"`
	Message string  `json:"message"`
}

// Timeline is a sequence at its current revision.
type Timeline struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	SessionID *string                 `json:"session_id"`
	Revision  int                     `json:"revision"`
	Target    timeline.Target         `json:"target"`
	Tracks    []timeline.Track        `json:"tracks"`
	DurationS float64                 `json:"duration_s"`
	Frames    int                     `json:"frames"`
	Markers   []timeline.Marker       `json:"markers,omitempty"` // 1.50
	Defaults  TimelineDefaults        `json:"defaults"`
	CanUndo   bool                    `json:"can_undo"`
	CanRedo   bool                    `json:"can_redo"`
	Takes     map[string]TimelineTake `json:"takes"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
	// API 1.76: who wrote the name, as a session's title_source; named_by while it is model
	TitleSource string  `json:"title_source"`
	NamedBy     *string `json:"named_by"`
}

// TimelineSummary is a list item.
type TimelineSummary struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Target    timeline.Target `json:"target"`
	Revision  int             `json:"revision"`
	DurationS float64         `json:"duration_s"`
	Clips     int             `json:"clips"`
	ThumbURL  *string         `json:"thumb_url"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TimelineList is GET /timelines.
type TimelineList struct {
	Timelines []TimelineSummary `json:"timelines"`
}

// TimelineShot is New shot's 202.
type TimelineShot struct {
	Timeline Timeline `json:"timeline"`
	Run      Run      `json:"run"`
}

// TimelineRerenderAnswer is Re-render's 202.
type TimelineRerenderAnswer struct {
	Timeline Timeline `json:"timeline"`
	Batch    Batch    `json:"batch"`
}

// ExportReason is why the picture is re-encoded, or why it cannot export.
type ExportReason struct {
	Code    string  `json:"code"`
	Clip    *string `json:"clip"`
	Message string  `json:"message"`
}

// ExportOutput is the plan's output.
type ExportOutput struct {
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	SampleRate   int     `json:"sample_rate"`
	Container    string  `json:"container"`
	VideoCodec   string  `json:"video_codec"`
	AudioCodec   string  `json:"audio_codec"`
	VideoBitrate *int    `json:"video_bitrate"`
}

// ExportPlan is POST …/export/plan.
type ExportPlan struct {
	Revision       int                  `json:"revision"`
	StreamCopy     bool                 `json:"stream_copy"`
	Reasons        []ExportReason       `json:"reasons"`
	Blocked        []ExportReason       `json:"blocked"`
	Output         ExportOutput         `json:"output"`
	Frames         int                  `json:"frames"`
	DurationS      float64              `json:"duration_s"`
	EstimatedBytes int                  `json:"estimated_bytes"`
	Sounds         []timeline.PlanSound `json:"sounds"` // API 1.32: always sent
	Host           *string              `json:"host"`
	Env            *string              `json:"env"`
	Hardware       *bool                `json:"hardware"`
}

// MediaJob is an export or frame job.
type MediaJob struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	Host         string     `json:"host"`
	Env          string     `json:"env"`
	TimelineID   *string    `json:"timeline_id"`
	Revision     *int       `json:"revision"`
	SourceTakeID *string    `json:"source_take_id"`
	Mode         *string    `json:"mode"`
	Reason       *string    `json:"reason"`
	Progress     any        `json:"progress"`
	TakeID       *string    `json:"take_id"`
	Failure      any        `json:"failure"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	VideoTakeID  *string    `json:"video_take_id,omitempty"` // API 1.79, mux only
	SendID       *string    `json:"send_id,omitempty"`       // API 1.82, bake only
}

// MediaJobList is GET …/exports.
type MediaJobList struct {
	Jobs []MediaJob `json:"jobs"`
}

// TakeFrame is POST /takes/{id}/frame: the take (200) or the job (202).
type TakeFrame struct {
	Take *gallerydto.Take `json:"take,omitempty"`
	Job  *MediaJob        `json:"job,omitempty"`
}

// ClipActions is GET /timelines/{id}/clips/{clip_id}/actions (API 1.19).
type ClipActions struct {
	Actions []timeline.Action `json:"actions"` // API 1.32: an audio clip's carry a slot (Make shot)
}
