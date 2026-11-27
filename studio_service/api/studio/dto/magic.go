package dto

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
	Boards []MagicBoardSummary `json:"boards"`
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
