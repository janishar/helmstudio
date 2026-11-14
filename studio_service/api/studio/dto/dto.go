// Package dto: the HTTP bodies of /v1/studio (contracts/openapi.yaml, tag studio). Hand-written; requests carry
// Validate (the 422 detail, "" when valid).
package dto

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/studio_service/registry"
)

// Session is StudioSession.
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionList is the caller's sessions, newest first.
type SessionList struct {
	Sessions []Session `json:"sessions"`
}

// SessionCreate is POST /sessions; a missing or blank title is "Untitled".
type SessionCreate struct {
	Title *string `json:"title"`
}

// Validate checks the title's length.
func (b *SessionCreate) Validate() string {
	if b.Title != nil && utf8.RuneCountInString(strings.TrimSpace(*b.Title)) > 200 {
		return "title must be at most 200 characters"
	}
	return ""
}

// TitleOrDefault is the trimmed title, or "Untitled".
func (b *SessionCreate) TitleOrDefault() string {
	if b.Title == nil || strings.TrimSpace(*b.Title) == "" {
		return "Untitled"
	}
	return strings.TrimSpace(*b.Title)
}

// SessionPatch renames a session.
type SessionPatch struct {
	Title *string `json:"title"`
}

// Validate wants a title of 1-200 characters after trimming.
func (b *SessionPatch) Validate() string {
	if b.Title == nil {
		return "title is required"
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(*b.Title)); n == 0 || n > 200 {
		return "title must be 1-200 characters"
	}
	return ""
}

// WorkflowList is every runnable workflow, by id.
type WorkflowList struct {
	Workflows []registry.Workflow `json:"workflows"`
}

// RunRequest is POST /sessions/{id}/runs; inputs are checked against the workflow's node schemas.
type RunRequest struct {
	WorkflowID string         `json:"workflow_id"`
	Inputs     map[string]any `json:"inputs"`
}

// Validate wants a workflow id.
func (b *RunRequest) Validate() string {
	if b.WorkflowID == "" {
		return "workflow_id is required"
	}
	return ""
}

// RunProgress is the last step a run reported.
type RunProgress struct {
	Node string `json:"node"`
	Step int    `json:"step"`
	Of   int    `json:"of"`
}

// RunOutput is one stored output with its signed link.
type RunOutput struct {
	Index  int    `json:"index"`
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Width  *int   `json:"width"`
	Height *int   `json:"height"`
}

// Run is one run of a workflow.
type Run struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	WorkflowID string         `json:"workflow_id"`
	Inputs     map[string]any `json:"inputs"`
	Status     string         `json:"status"`
	Progress   *RunProgress   `json:"progress"`
	Error      *string        `json:"error"`
	Outputs    []RunOutput    `json:"outputs"`
	Host       *string        `json:"host"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

// RunList is a session's runs, newest first.
type RunList struct {
	Runs []Run `json:"runs"`
}

// SessionEvent is one stored event: type status, progress or output.
type SessionEvent struct {
	Seq   int64     `json:"seq"`
	Type  string    `json:"type"`
	RunID string    `json:"run_id"`
	TS    time.Time `json:"ts"`
	Data  any       `json:"data"`
}

// EventPage is the events after a cursor and the next cursor.
type EventPage struct {
	Events  []SessionEvent `json:"events"`
	LastSeq int64          `json:"last_seq"`
}

// Host is one runner as users see it: no run id, no owner.
type Host struct {
	Host      string    `json:"host"`
	Env       string    `json:"env"`
	State     string    `json:"state"` // idle | busy | offline
	UpdatedAt time.Time `json:"updated_at"`
}

// HostList is every runner, by key.
type HostList struct {
	Hosts []Host `json:"hosts"`
}
