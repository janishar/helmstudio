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
	Video       json.RawMessage // a video's metadata and poster (dto.Video); null for an image
	Legacy      json.RawMessage // source import: {settings, imported_at}; null otherwise (API 1.16)
	FrameOf     json.RawMessage // source frame: {take_id, at}; null otherwise (API 1.17)
	Export      json.RawMessage // source export: {timeline_id, timeline_name, revision, job_id, mode} (API 1.17)
	Audio       json.RawMessage // kind audio: {duration_s} (API 1.19)
	SourceMap   json.RawMessage // a video made from a media take: evt.run.output's source_map (API 1.19)
}

// TakeCols are the columns Take scans, in order, from takes aliased t.
const TakeCols = `t.id::text, t.kind, t.source, t.title, t.key, t.content_type, t.width, t.height, t.bytes, t.starred,
	t.session_id::text, t.run_id::text, t.made_with, t.ready, t.created_at,
	coalesce((SELECT array_agg(p.parent_id::text ORDER BY p.idx) FROM take_parents p
		JOIN takes pt ON pt.id = p.parent_id AND pt.deleted_at IS NULL WHERE p.take_id = t.id), '{}'),
	t.batch_id::text, t.video, t.legacy, t.frame_of, t.export, t.audio, t.source_map`

// ThumbKey is the key of a take's thumbnail, from takes aliased t: a video's poster, else the image itself.
const ThumbKey = `coalesce(t.video->'poster'->>'key', t.key)`
