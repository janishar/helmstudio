package dto

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"
)

// Import (contracts/README.md "Import", API 1.16): helm import's takes.

// ImportKey is an import's idempotency key.
var ImportKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

// MaxVideoBytes is the largest imported video (2 GB); MaxLegacy the largest legacy snapshot as compact JSON.
const (
	MaxVideoBytes = 2147483648
	MaxLegacy     = 65536
)

// ImportTakeCreate is POST /admin/imports/takes.
type ImportTakeCreate struct {
	OwnerID     string          `json:"owner_id"`
	Key         string          `json:"key"`
	Kind        string          `json:"kind"`
	Filename    string          `json:"filename"`
	ContentType string          `json:"content_type"`
	Bytes       int64           `json:"bytes"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	CreatedAt   *time.Time      `json:"created_at"`
	Starred     *bool           `json:"starred"`
	Title       string          `json:"title"`
	Legacy      json.RawMessage `json:"legacy"`
	Parents     []string        `json:"parents"`
	Video       *ImportVideo    `json:"video"`
	unknown     string
}

// ImportVideo is an imported video's metadata and its poster image.
type ImportVideo struct {
	DurationS float64      `json:"duration_s"`
	FPS       float64      `json:"fps"`
	Frames    int          `json:"frames"`
	HasAudio  bool         `json:"has_audio"`
	Poster    *ImportImage `json:"poster"`
}

// ImportImage is a declared image file: its type, size and pixel size.
type ImportImage struct {
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// UnmarshalJSON decodes the fields and remembers an unknown one (additionalProperties: false).
func (b *ImportTakeCreate) UnmarshalJSON(data []byte) error {
	type plain ImportTakeCreate
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.unknown = unknownField(data, "owner_id", "key", "kind", "filename", "content_type", "bytes", "width", "height",
		"created_at", "starred", "title", "legacy", "parents", "video")
	return nil
}

// Validate checks the body against ImportTakeCreate.
func (b *ImportTakeCreate) Validate() string {
	switch {
	case b.unknown != "":
		return b.unknown + ": not a field of ImportTakeCreate"
	case !utility.IsUUID(b.OwnerID):
		return "owner_id must be a user id"
	case !ImportKey.MatchString(b.Key):
		return "key must be 1-200 letters, digits or ._:- (starting with a letter or digit)"
	case b.Kind != "image" && b.Kind != "video":
		return "kind must be image or video"
	case b.Filename == "" || utf8.RuneCountInString(b.Filename) > 255:
		return "filename must be 1-255 characters"
	case b.Width < 1 || b.Height < 1:
		return "width and height must be at least 1"
	case b.CreatedAt == nil:
		return "created_at is required"
	case b.Starred == nil:
		return "starred is required"
	case b.Title == "" || utf8.RuneCountInString(b.Title) > 200:
		return "title must be 1-200 characters"
	case len(bytes.TrimSpace(b.Legacy)) == 0 || bytes.TrimSpace(b.Legacy)[0] != '{':
		return "legacy must be an object"
	case b.Parents == nil:
		return "parents is required"
	case len(b.Parents) > 64:
		return "parents: at most 64"
	case b.Bytes < 1:
		return "bytes must be at least 1"
	}
	for _, p := range b.Parents {
		if !ImportKey.MatchString(p) {
			return "parents: each must be an import key"
		}
	}
	var compact bytes.Buffer
	if json.Compact(&compact, b.Legacy) != nil || compact.Len() > MaxLegacy {
		return "legacy is larger than 64 KB"
	}
	if b.Kind == "image" {
		switch {
		case b.Video != nil:
			return "video is only for kind video"
		case UploadTypes[b.ContentType] == "":
			return "an image must be png, jpeg or webp"
		case b.Bytes > MaxUploadBytes:
			return "file is larger than 50 MB"
		}
		return ""
	}
	switch v := b.Video; {
	case b.ContentType != "video/mp4":
		return "a video must be video/mp4"
	case b.Bytes > MaxVideoBytes:
		return "file is larger than 2 GB"
	case v == nil:
		return "video is required for kind video"
	case v.DurationS <= 0 || v.FPS <= 0 || v.Frames < 1:
		return "video: duration_s, fps and frames must be positive"
	case v.Poster == nil:
		return "video.poster is required"
	case UploadTypes[v.Poster.ContentType] == "":
		return "video.poster must be png, jpeg or webp"
	case v.Poster.Bytes < 1 || v.Poster.Bytes > MaxUploadBytes:
		return "video.poster: bytes must be 1 to 50 MB"
	case v.Poster.Width < 1 || v.Poster.Height < 1:
		return "video.poster: width and height must be at least 1"
	}
	return ""
}

// ImportTake is the answer: the take and, while it is pending, its signed PUT links.
type ImportTake struct {
	TakeID          string     `json:"take_id"`
	Key             string     `json:"key"`
	State           string     `json:"state"` // pending | ready
	UploadURL       *string    `json:"upload_url"`
	PosterUploadURL *string    `json:"poster_upload_url"`
	ExpiresAt       *time.Time `json:"expires_at"`
}
