// Package dto: the HTTP bodies of /v1/studio (contracts/openapi.yaml, tag studio). Hand-written; requests carry
// Validate (the 422 detail, "" when valid).
package dto

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"

	gallery "github.com/helmstudio/studio_service/api/gallery/dto"
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

// WorkflowList is the templates by title, then the caller's own workflows, newest first.
type WorkflowList struct {
	Workflows []Workflow `json:"workflows"`
}

// RunRequest is POST /sessions/{id}/runs; inputs are checked against the workflow's node schemas.
type RunRequest struct {
	WorkflowID string         `json:"workflow_id"`
	Inputs     map[string]any `json:"inputs"`
	Looks      []string       `json:"looks"` // the chosen looks, every group together; a repeat counts once
}

// Validate wants a workflow id and at most 8 look ids.
func (b *RunRequest) Validate() string {
	if b.WorkflowID == "" {
		return "workflow_id is required"
	}
	return CheckLookIDs("looks", b.Looks)
}

// MaxRunLooks is the most look ids a run (a batch row, a step) asks for.
const MaxRunLooks = 8

// CheckLookIDs checks a list of look ids at where: at most 8, each an own (UUID) or starter look id.
func CheckLookIDs(where string, ids []string) string {
	if len(ids) > MaxRunLooks {
		return where + ": at most 8 looks"
	}
	for i, id := range ids {
		if !LookID.MatchString(id) {
			return fmt.Sprintf("%s[%d]: %q is not a look id", where, i+1, id)
		}
	}
	return ""
}

// RunProgress is the last step a run reported.
type RunProgress struct {
	Node  string   `json:"node"`
	Step  int      `json:"step"`
	Of    int      `json:"of"`
	Phase *string  `json:"phase,omitempty"`
	EtaS  *float64 `json:"eta_s,omitempty"`
}

// RunOutput is one stored output with its signed link.
type RunOutput struct {
	Index  int     `json:"index"`
	Key    string  `json:"key"`
	Kind   string  `json:"kind"`
	URL    string  `json:"url"`
	Width  *int    `json:"width"`
	Height *int    `json:"height"`
	Seed   *int64  `json:"seed,omitempty"`
	TakeID *string `json:"take_id,omitempty"`
	// kind video only: its metadata and signed poster
	Video *gallery.VideoInfo `json:"video,omitempty"`
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
	Host       *string        `json:"host"` // while queued: the host it is lined up for
	Lane       string         `json:"lane"`
	Attempt    int            `json:"attempt"`
	Position   *int           `json:"position,omitempty"` // only while queued
	Resolved   map[string]any `json:"resolved,omitempty"` // values the runner chose (a random seed), from succeeded
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
	BatchID    *string        `json:"batch_id,omitempty"`
	BatchRow   *int           `json:"batch_row,omitempty"`
	// a user workflow: the version it ran
	WorkflowVersion *int `json:"workflow_version,omitempty"`
	// the looks it was asked with, as requested
	Looks []string `json:"looks,omitempty"`
	// a text (ask) workflow's run: its answer, never a take (API 1.14)
	Answer *RunAnswer `json:"answer,omitempty"`
}

// RunAnswer is an ask run's answer: the last try's text, the tokens and the time.
type RunAnswer struct {
	Text      string  `json:"text"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	Seconds   float64 `json:"seconds"`
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
	Host      string           `json:"host"`
	Env       string           `json:"env"`
	State     string           `json:"state"` // idle | busy | offline
	UpdatedAt time.Time        `json:"updated_at"`
	Memory    *registry.Memory `json:"memory,omitempty"`
	// the host's shared budget and what all its envs hold (the same on each of its envs); nil: no key reports memory
	HostMemory *registry.Memory    `json:"host_memory,omitempty"`
	Resident   []registry.Resident `json:"resident,omitempty"`
}

// HostList is every runner, by key.
type HostList struct {
	Hosts []Host `json:"hosts"`
}

// Queue is the caller's queued and running runs, and per host how many runs of others wait.
type Queue struct {
	Runs  []Run       `json:"runs"`
	Hosts []QueueHost `json:"hosts"`
}

// QueueHost is one host name (its envs merged), redacted: a count of other users' queued runs, never whose.
type QueueHost struct {
	Host         string `json:"host"`
	State        string `json:"state"` // idle | busy | offline
	OthersQueued int    `json:"others_queued"`
}

// BatchCounts is a batch's rows by shown status.
type BatchCounts struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Waiting   int `json:"waiting"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// Batch is one batch: one workflow run once per row.
type Batch struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	WorkflowID    string      `json:"workflow_id"`
	WorkflowTitle string      `json:"workflow_title"`
	SessionID     *string     `json:"session_id"`
	Status        string      `json:"status"`
	Control       string      `json:"control"`
	Reason        *string     `json:"reason"`
	Counts        BatchCounts `json:"counts"`
	CreatedAt     time.Time   `json:"created_at"`
	StartedAt     *time.Time  `json:"started_at"`
	FinishedAt    *time.Time  `json:"finished_at"`
	// a batch of a pipeline (absent for a batch of one workflow)
	PipelineID      *string `json:"pipeline_id,omitempty"`
	PipelineTitle   *string `json:"pipeline_title,omitempty"`
	PipelineVersion *int    `json:"pipeline_version,omitempty"`
	Collection      *string `json:"collection,omitempty"`
	// API 1.14, a pipeline batch: its worst case, its ask workflows, its memory snapshot; Run again's source
	Bounds        *PipelineBounds   `json:"bounds,omitempty"`
	Models        map[string]string `json:"models,omitempty"`
	Memory        json.RawMessage   `json:"memory,omitempty"`
	AgainOf       *string           `json:"again_of,omitempty"`
	SameDecisions *bool             `json:"same_decisions,omitempty"`
	// API 1.15, a pipeline batch: what leaves the machine, servers' reserved memory, the director's chat (null
	// without one), the remote calls so far (null when off)
	Egress   *[]PipelineEgress   `json:"egress,omitempty"`
	Reserved *[]PipelineReserved `json:"reserved,omitempty"`
	Director json.RawMessage     `json:"director,omitempty"`
	Remote   json.RawMessage     `json:"remote,omitempty"`
}

// BatchRow is one row of a batch.
type BatchRow struct {
	Row        int            `json:"row"`
	Attempt    int            `json:"attempt"`
	Inputs     map[string]any `json:"inputs"`
	Status     string         `json:"status"`
	RunID      *string        `json:"run_id"`
	TakeIDs    []string       `json:"take_ids"`
	ThumbURL   *string        `json:"thumb_url"`
	Error      *string        `json:"error"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
	// a batch of one workflow: the row's looks
	Looks []string `json:"looks,omitempty"`
	// pipeline batches only (a nil interface is left out; a typed nil is null): *string, *BatchRowParked, []BatchCell
	Node   any `json:"node,omitempty"`
	Parked any `json:"parked,omitempty"`
	Cells  any `json:"cells,omitempty"`
}

// BatchPage is a page of the caller's batches, newest first.
type BatchPage struct {
	Batches    []Batch `json:"batches"`
	NextCursor *string `json:"next_cursor"`
}

// BatchRowPage is a page of a batch's rows by number.
type BatchRowPage struct {
	Rows       []BatchRow `json:"rows"`
	NextCursor *string    `json:"next_cursor"`
}

// BatchEvent is one stored batch event: data is the whole Batch (type batch) or BatchRow (type row) as it is now.
type BatchEvent struct {
	Seq  int64     `json:"seq"`
	Type string    `json:"type"`
	TS   time.Time `json:"ts"`
	Data any       `json:"data"`
}

// BatchEventPage is the batch events after a cursor and the next cursor.
type BatchEventPage struct {
	Events  []BatchEvent `json:"events"`
	LastSeq int64        `json:"last_seq"`
}

// BatchLines is one row per pasted value of one text, string or enum param.
type BatchLines struct {
	Param  string   `json:"param"`
	Values []string `json:"values"`
}

// BatchTable is a parsed CSV: a typed cell per column; a null cell leaves that input unset.
type BatchTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// BatchAxis is one axis of a grid.
type BatchAxis struct {
	Param  string `json:"param"`
	Values []any  `json:"values"`
}

// BatchGrid is the cartesian product of its axes, the first varying slowest.
type BatchGrid struct {
	Axes []BatchAxis `json:"axes"`
}

// BatchRowsIn is exactly one of lines, table, grid.
type BatchRowsIn struct {
	Lines *BatchLines `json:"lines"`
	Table *BatchTable `json:"table"`
	Grid  *BatchGrid  `json:"grid"`
}

// BatchSeeds repeats each row count times with consecutive seeds from start (absent: drawn at Submit).
type BatchSeeds struct {
	Count int    `json:"count"`
	Start *int64 `json:"start"`
}

// BatchCreate is POST /batches.
type BatchCreate struct {
	WorkflowID string         `json:"workflow_id"`
	PipelineID string         `json:"pipeline_id"`
	SessionID  *string        `json:"session_id"`
	Title      *string        `json:"title"`
	Inputs     map[string]any `json:"inputs"`
	Rows       BatchRowsIn    `json:"rows"`
	Seeds      *BatchSeeds    `json:"seeds"`
	Looks      []string       `json:"looks"` // a batch of one workflow: the looks of every row without its own
}

// rowLooks reports whether a table column or grid axis is looks (each row's own looks).
func (b *BatchCreate) rowLooks() bool {
	if t := b.Rows.Table; t != nil && slices.Contains(t.Columns, "looks") {
		return true
	}
	return b.Rows.Grid != nil && slices.ContainsFunc(b.Rows.Grid.Axes, func(a BatchAxis) bool { return a.Param == "looks" })
}

// paramKey is "<node id>.<param>" (a pipeline batch: "<inputs node>.<input>").
var paramKey = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Validate checks the body's shape; the rows against the workflow are checked at Submit.
func (b *BatchCreate) Validate() string {
	rows := b.Rows
	given := 0
	for _, set := range []bool{rows.Lines != nil, rows.Table != nil, rows.Grid != nil} {
		if set {
			given++
		}
	}
	switch {
	case (b.WorkflowID == "") == (b.PipelineID == ""):
		return "give exactly one of workflow_id or pipeline_id"
	case b.PipelineID != "" && !utility.IsUUID(b.PipelineID):
		return "pipeline_id must be a uuid"
	case b.SessionID != nil && !utility.IsUUID(*b.SessionID):
		return "session_id must be a uuid"
	case b.Title != nil && utf8.RuneCountInString(strings.TrimSpace(*b.Title)) > 200:
		return "title must be at most 200 characters"
	case given != 1:
		return "rows needs exactly one of lines, table, grid"
	case b.Seeds != nil && (b.Seeds.Count < 1 || b.Seeds.Count > 8):
		return "seeds.count must be 1 to 8"
	case b.Seeds != nil && b.Seeds.Start != nil && (*b.Seeds.Start < 0 || *b.Seeds.Start > 1<<53-1):
		return "seeds.start must be from 0 to 9007199254740991"
	case b.PipelineID != "" && (b.Looks != nil || b.rowLooks()):
		return "looks: a pipeline batch takes its looks from its steps"
	}
	if detail := CheckLookIDs("looks", b.Looks); detail != "" {
		return detail
	}
	switch {
	case rows.Lines != nil:
		if !paramKey.MatchString(rows.Lines.Param) {
			return "rows.lines.param must be <node>.<param>"
		}
		if n := len(rows.Lines.Values); n < 1 || n > 500 {
			return "rows.lines.values must have 1 to 500 values"
		}
	case rows.Table != nil:
		seen := map[string]bool{}
		for _, column := range rows.Table.Columns {
			if !(paramKey.MatchString(column) || column == "looks") || seen[column] {
				return "rows.table.columns must be distinct <node>.<param> names"
			}
			seen[column] = true
		}
		if len(seen) == 0 {
			return "rows.table.columns must not be empty"
		}
		if n := len(rows.Table.Rows); n < 1 || n > 500 {
			return "rows.table.rows must have 1 to 500 rows"
		}
	default:
		if n := len(rows.Grid.Axes); n < 1 || n > 2 {
			return "rows.grid.axes must have 1 or 2 axes"
		}
		for _, axis := range rows.Grid.Axes {
			if !paramKey.MatchString(axis.Param) && axis.Param != "looks" {
				return "rows.grid.axes[].param must be <node>.<param>"
			}
			if n := len(axis.Values); n < 1 || n > 500 {
				return "rows.grid.axes[].values must have 1 to 500 values"
			}
		}
	}
	return ""
}
