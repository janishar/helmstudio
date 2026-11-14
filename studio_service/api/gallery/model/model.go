// Package model: the gallery's rows, as read (the service maps them to dto; the key never leaves it).
package model

import (
	"encoding/json"
	"time"
)

// Take is a takes row with its parents.
type Take struct {
	ID          string
	Kind        string
	Source      string
	Title       string
	Key         string
	ContentType string
	Width       *int
	Height      *int
	Bytes       int64
	Starred     bool
	SessionID   *string
	RunID       *string
	MadeWith    json.RawMessage // null for an upload
	Ready       bool
	CreatedAt   time.Time
	Parents     []string
	BatchID     *string
}

// TakeCols are the columns Take scans, in order, from takes aliased t.
const TakeCols = `t.id::text, t.kind, t.source, t.title, t.key, t.content_type, t.width, t.height, t.bytes, t.starred,
	t.session_id::text, t.run_id::text, t.made_with, t.ready, t.created_at,
	coalesce((SELECT array_agg(p.parent_id::text ORDER BY p.idx) FROM take_parents p
		JOIN takes pt ON pt.id = p.parent_id AND pt.deleted_at IS NULL WHERE p.take_id = t.id), '{}'),
	t.batch_id::text`
