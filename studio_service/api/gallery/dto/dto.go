// Package dto: the HTTP bodies of the gallery and uploads (contracts/openapi.yaml: Take, TakePage, TakePatch,
// TakeReuse, UploadCreate, Upload). Hand-written; requests carry Validate (the 422 detail, "" when valid).
package dto

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// MadeWith is the snapshot of what made a run output, taken when its take was stored.
type MadeWith struct {
	WorkflowID    string `json:"workflow_id"`
	WorkflowTitle string `json:"workflow_title"`
	// a user workflow: the version that made it
	WorkflowVersion *int           `json:"workflow_version,omitempty"`
	Inputs          map[string]any `json:"inputs"`
	Prompt          *string        `json:"prompt"`
	Seed            *int64         `json:"seed"`
	Model           *Model         `json:"model"`
	// the elements its prompt mentioned, first mention first; typed: each param the expansion changed, as set
	Elements []MadeWithElement `json:"elements,omitempty"`
	Typed    map[string]any    `json:"typed,omitempty"`
	// every LoRA of every lora-stack node that ran, node id order then row order, as resolved when the run was made
	Loras []MadeWithLora `json:"loras,omitempty"`
	// the looks it was made with, in request order (repeats once), as they were when the run was made
	Looks []MadeWithLook `json:"looks,omitempty"`
	// a take made by a step of a pipeline batch
	Pipeline *MadeWithPipeline `json:"pipeline,omitempty"`
}

// MadeWithElement is an element a take's prompt mentioned, as it was expanded.
type MadeWithElement struct {
	ID      string `json:"id"`
	Handle  string `json:"handle"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// MadeWithLook is a look a take was made with, as it was applied.
type MadeWithLook struct {
	ID      string `json:"id"`
	Source  string `json:"source"` // mine | starter
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// LookStatus is how a look of made_with.looks compares now: current, changed, deleted (own) or missing (starter).
type LookStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// MadeWithLora is a LoRA a take was made with.
type MadeWithLora struct {
	Node     string  `json:"node"`
	ID       string  `json:"id"`
	Source   string  `json:"source"` // shared | mine
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
	Mode     string  `json:"mode"`
	SHA256   string  `json:"sha256"`
}

// LoraStatus is whether a LoRA of made_with.loras can be used now: current, missing (shared, on no live host) or
// deleted (own).
type LoraStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ElementStatus is how an element of made_with.elements compares now: current, changed or deleted.
type ElementStatus struct {
	ID            string  `json:"id"`
	Handle        string  `json:"handle"`
	Status        string  `json:"status"`
	CurrentHandle *string `json:"current_handle"`
}

// MadeWithPipeline is which pipeline, step node and visit made a take.
type MadeWithPipeline struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version int    `json:"version"`
	Node    string `json:"node"`
	Attempt int    `json:"attempt"` // the step's visit in the row, from 1
	Row     int    `json:"row"`
}

// Model is the workflow's <family>/model node as it ran.
type Model struct {
	ID        *string `json:"id"`
	Variant   *string `json:"variant"`
	Precision *string `json:"precision"`
}

// Take is one item of the caller's gallery: signed links only, never the key.
type Take struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Source      string     `json:"source"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	ThumbURL    string     `json:"thumb_url"`
	ContentType string     `json:"content_type"`
	Width       *int       `json:"width"`
	Height      *int       `json:"height"`
	Bytes       int64      `json:"bytes"`
	CreatedAt   time.Time  `json:"created_at"`
	Starred     bool       `json:"starred"`
	SessionID   *string    `json:"session_id"`
	RunID       *string    `json:"run_id"`
	MadeWith    *MadeWith  `json:"made_with"`
	Parents     []string   `json:"parents"`
	Children    *[]TakeRef `json:"children,omitempty"` // only GET /takes/{id}
	BatchID     *string    `json:"batch_id,omitempty"`
	Video       *VideoInfo `json:"video,omitempty"` // kind video only
	// only GET /takes/{id}: the judge decisions that weighed it, newest first (API 1.14)
	Decisions *[]TakeDecision `json:"decisions,omitempty"`
	// source import: the old settings, read-only (API 1.16)
	Legacy json.RawMessage `json:"legacy,omitempty"`
}

// TakeDecision is one judge decision about a take: kept (by the judge or the user's pick), passed or failed.
type TakeDecision struct {
	DecisionID  string  `json:"decision_id"`
	BatchID     string  `json:"batch_id"`
	Row         int     `json:"row"`
	Node        string  `json:"node"`
	Outcome     string  `json:"outcome"` // picked | passed | failed
	FirstFailed *string `json:"first_failed"`
}

// VideoInfo is a video take's or run output's metadata with its signed poster (contracts VideoInfo).
type VideoInfo struct {
	DurationS float64 `json:"duration_s"`
	FPS       float64 `json:"fps"`
	Frames    int     `json:"frames"`
	HasAudio  bool    `json:"has_audio"`
	PosterURL string  `json:"poster_url"`
}

// Video is a video's metadata as evt.run.output carries it and takes.video / run_outputs.video store it.
type Video struct {
	DurationS float64 `json:"duration_s"`
	FPS       float64 `json:"fps"`
	Frames    int     `json:"frames"`
	HasAudio  bool    `json:"has_audio"`
	Poster    Poster  `json:"poster"`
}

// Poster is a video's frame 0 as PNG: the take's thumbnail.
type Poster struct {
	Key    string `json:"key"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int64  `json:"bytes"`
}

// Info is the stored video (JSON, null or empty for an image) with its poster signed by sign; nil for an image.
func Info(stored []byte, sign func(key string) string) *VideoInfo {
	var video *Video
	if json.Unmarshal(stored, &video) != nil || video == nil {
		return nil
	}
	return &VideoInfo{DurationS: video.DurationS, FPS: video.FPS, Frames: video.Frames, HasAudio: video.HasAudio,
		PosterURL: sign(video.Poster.Key)}
}

// TakeRef is a take in a take's children ("Used in").
type TakeRef struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	ThumbURL string `json:"thumb_url"`
}

// TakeUse is POST /takes/{id}/use: the workflow, and which of its image inputs (absent: the only one).
type TakeUse struct {
	WorkflowID string  `json:"workflow_id"`
	Param      *string `json:"param"`
	unknown    string
}

// UnmarshalJSON decodes the fields and remembers an unknown one (additionalProperties: false).
func (b *TakeUse) UnmarshalJSON(data []byte) error {
	type plain TakeUse
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.unknown = unknownField(data, "workflow_id", "param")
	return nil
}

// paramKey is a "<node id>.<param>" input key.
var paramKey = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Validate wants a workflow_id and a param of the form <node>.<param>.
func (b *TakeUse) Validate() string {
	switch {
	case b.unknown != "":
		return b.unknown + ": not a field of TakeUse"
	case b.WorkflowID == "":
		return "workflow_id is required"
	case b.Param != nil && !paramKey.MatchString(*b.Param):
		return "param must be <node>.<param>"
	}
	return ""
}

// TakePage is a page of takes, newest first.
type TakePage struct {
	Takes      []Take  `json:"takes"`
	NextCursor *string `json:"next_cursor"`
}

// TakeReuse is the settings that made a take, as a RunRequest.
type TakeReuse struct {
	WorkflowID string         `json:"workflow_id"`
	Inputs     map[string]any `json:"inputs"`
	// reuse only: current | changed | deleted, how the workflow now compares with the one that made the take
	WorkflowStatus string `json:"workflow_status,omitempty"`
	// reuse only: one per made_with.elements entry
	ElementsStatus []ElementStatus `json:"elements_status,omitempty"`
	// reuse only: one per made_with.loras entry
	LorasStatus []LoraStatus `json:"loras_status,omitempty"`
	// reuse only: the ids of made_with.looks (send them back as RunRequest.looks), and how each compares now
	Looks       []string     `json:"looks,omitempty"`
	LooksStatus []LookStatus `json:"looks_status,omitempty"`
}

// TakePatch renames or stars a take: at least one field, no others.
type TakePatch struct {
	Title   *string `json:"title"`
	Starred *bool   `json:"starred"`
	unknown string  // the first field the schema does not allow
}

// UnmarshalJSON decodes the fields and remembers an unknown one (additionalProperties: false).
func (b *TakePatch) UnmarshalJSON(data []byte) error {
	type plain TakePatch
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.unknown = unknownField(data, "title", "starred")
	return nil
}

// Validate wants title or starred, and a title of 1-200 characters after trimming.
func (b *TakePatch) Validate() string {
	switch {
	case b.unknown != "":
		return b.unknown + ": not a field of TakePatch"
	case b.Title == nil && b.Starred == nil:
		return "title or starred is required"
	case b.Title != nil && (strings.TrimSpace(*b.Title) == "" || utf8.RuneCountInString(strings.TrimSpace(*b.Title)) > 200):
		return "title must be 1-200 characters"
	}
	return ""
}

// MaxUploadBytes is the largest upload (50 MB).
const MaxUploadBytes = 52428800

// UploadTypes maps each content type an upload may have to its key's extension.
var UploadTypes = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp"}

// UploadCreate is POST /uploads.
type UploadCreate struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	unknown     string
}

// UnmarshalJSON decodes the fields and remembers an unknown one (additionalProperties: false).
func (b *UploadCreate) UnmarshalJSON(data []byte) error {
	type plain UploadCreate
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.unknown = unknownField(data, "filename", "content_type", "bytes")
	return nil
}

// Validate checks the name, the type and the size.
func (b *UploadCreate) Validate() string {
	switch {
	case b.unknown != "":
		return b.unknown + ": not a field of UploadCreate"
	case b.Filename == "" || utf8.RuneCountInString(b.Filename) > 255:
		return "filename must be 1-255 characters"
	case UploadTypes[b.ContentType] == "":
		return "only png, jpeg and webp images can be uploaded"
	case b.Bytes < 1:
		return "bytes must be at least 1"
	case b.Bytes > MaxUploadBytes:
		return "file is larger than 50 MB"
	}
	return ""
}

// Title is the file's base name without its extension (at most 200 characters), or "Upload".
func (b *UploadCreate) Title() string {
	name := b.Filename[strings.LastIndexAny(b.Filename, `/\`)+1:]
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 200 {
		name = string([]rune(name)[:200])
	}
	if name == "" {
		return "Upload"
	}
	return name
}

// Upload is the answer to POST /uploads: where to PUT the bytes.
type Upload struct {
	TakeID      string    `json:"take_id"`
	UploadURL   string    `json:"upload_url"`
	ContentType string    `json:"content_type"`
	Bytes       int64     `json:"bytes"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// unknownField returns the first key of the JSON object data that is not allowed, or "".
func unknownField(data []byte, allowed ...string) string {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return name
		}
	}
	return ""
}
