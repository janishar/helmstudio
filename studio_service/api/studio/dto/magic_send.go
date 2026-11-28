package dto

import (
	"time"

	"github.com/helmstudio/studio_service/api/magic"
)

// Magic: Send to Timeline (contracts/openapi.yaml MagicSend*; API 1.82, plan 39 phase G).

// MagicSendCreate is POST …/boards/{id}/send.
type MagicSendCreate struct {
	Revision int     `json:"revision"`
	Name     *string `json:"name"`
	DryRun   bool    `json:"dry_run"`
}

// Validate requires the revision; a name is 1-80 characters.
func (b *MagicSendCreate) Validate() string {
	if b.Revision < 1 {
		return "revision is required"
	}
	return boardName(b.Name)
}

// MagicSendPlan is what a send makes and lacks.
type MagicSendPlan struct {
	Name        string             `json:"name"`
	Revision    int                `json:"revision"`
	Goes        []magic.SendGoes   `json:"goes"`
	Joins       []magic.SendJoin   `json:"joins"`
	Effects     []magic.SendEffect `json:"effects"`
	Warnings    []string           `json:"warnings"`
	Bakes       int                `json:"bakes"`
	BakeSeconds *float64           `json:"bake_seconds"`
	Host        *string            `json:"host"`
}

// MagicSend is one send of a board.
type MagicSend struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Revision     int        `json:"revision"`
	Status       string     `json:"status"`
	TimelineID   *string    `json:"timeline_id"`
	TimelineGone bool       `json:"timeline_gone"`
	Bakes        int        `json:"bakes"`
	Baked        int        `json:"baked"`
	Jobs         []MediaJob `json:"jobs,omitempty"`
	Failure      *string    `json:"failure"`
	MadeAt       time.Time  `json:"made_at"`
}

// MagicSendResult is the send's answer: its plan, and the send unless it was a dry run.
type MagicSendResult struct {
	Plan MagicSendPlan `json:"plan"`
	Send *MagicSend    `json:"send,omitempty"`
}

// MagicSendList is GET …/sends.
type MagicSendList struct {
	Sends []MagicSend `json:"sends"`
}
