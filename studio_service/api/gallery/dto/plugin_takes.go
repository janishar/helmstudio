package dto

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"
)

// Plugins: takes (API 1.70; contracts/README.md "Plugins: takes"): what a `takes` plugin reads and makes.

// PluginTake is GET /plugin/takes/{take_id}.
type PluginTake struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	ContentType string   `json:"content_type"`
	Bytes       int64    `json:"bytes"`
	Width       *int     `json:"width"`
	Height      *int     `json:"height"`
	URL         string   `json:"url"`
	DurationS   *float64 `json:"duration_s,omitempty"`
	FPS         *float64 `json:"fps,omitempty"`
	HasAudio    *bool    `json:"has_audio,omitempty"`
}

// MaxToolInput is the largest tool input a take keeps, as compact JSON.
const MaxToolInput = 4096

var toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

// PluginTakeCreate is POST /plugin/takes.
type PluginTakeCreate struct {
	Parents     []string        `json:"parents"`
	Tool        string          `json:"tool"`
	Input       json.RawMessage `json:"input"`
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	ContentType string          `json:"content_type"`
	Bytes       int64           `json:"bytes"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	DurationS   float64         `json:"duration_s"`
	Sound       string          `json:"sound"` // API 1.79, an audio take's class (absent: voice)
	Video       *ImportVideo    `json:"video"`
	unknown     string
}

// UnmarshalJSON decodes the fields and remembers an unknown one (additionalProperties: false).
func (b *PluginTakeCreate) UnmarshalJSON(data []byte) error {
	type plain PluginTakeCreate
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.unknown = unknownField(data, "parents", "tool", "input", "title", "kind", "content_type", "bytes", "width",
		"height", "duration_s", "video", "sound")
	return nil
}

// Validate checks the body against PluginTakeCreate.
func (b *PluginTakeCreate) Validate() string {
	var compact bytes.Buffer
	switch {
	case b.unknown != "":
		return b.unknown + ": not a field of PluginTakeCreate"
	case b.Sound != "" && b.Kind != "audio": // API 1.79
		return "only audio has a sound class"
	case b.Sound != "" && !slices.Contains(SoundClasses, b.Sound):
		return "sound must be music, sound or voice"
	case len(b.Parents) < 1 || len(b.Parents) > 8:
		return "parents: 1 to 8 take ids"
	case !toolNameRe.MatchString(b.Tool):
		return "tool must be a tool's name"
	case len(bytes.TrimSpace(b.Input)) == 0 || bytes.TrimSpace(b.Input)[0] != '{':
		return "input must be an object"
	case json.Compact(&compact, b.Input) != nil || compact.Len() > MaxToolInput:
		return "input is larger than 4096 bytes"
	case b.Title == "" || utf8.RuneCountInString(b.Title) > 200:
		return "title must be 1-200 characters"
	case b.Bytes < 1:
		return "bytes must be at least 1"
	}
	seen := map[string]bool{}
	for _, p := range b.Parents {
		if !utility.IsUUID(p) || seen[p] {
			return "parents: each a take id, once"
		}
		seen[p] = true
	}
	b.Input = compact.Bytes()
	switch b.Kind {
	case "image":
		switch {
		case !strings.HasPrefix(b.ContentType, "image/") || UploadTypes[b.ContentType] == "":
			return "kind and content_type differ"
		case b.Width < 1 || b.Height < 1 || b.Width > 16384 || b.Height > 16384:
			return "an image or a video needs width and height"
		case b.Bytes > MaxUploadBytes:
			return "file is larger than 50 MB"
		case b.Video != nil || b.DurationS != 0:
			return "an image has no video or duration_s"
		}
	case "audio":
		switch {
		case !strings.HasPrefix(b.ContentType, "audio/") || UploadTypes[b.ContentType] == "":
			return "kind and content_type differ"
		case b.DurationS <= 0 || b.DurationS > 21600:
			return "an audio take needs duration_s"
		case b.Bytes > MaxUploadBytes:
			return "file is larger than 50 MB"
		case b.Video != nil || b.Width != 0 || b.Height != 0:
			return "an audio take has no video, width or height"
		}
	case "video":
		v := b.Video
		switch {
		case b.ContentType != "video/mp4":
			return "kind and content_type differ"
		case b.Width < 1 || b.Height < 1 || b.Width > 16384 || b.Height > 16384:
			return "an image or a video needs width and height"
		case b.Bytes > MaxVideoBytes:
			return "file is larger than 2 GB"
		case v == nil:
			return "a video needs video"
		case v.DurationS <= 0 || v.DurationS > 21600 || v.FPS <= 0 || v.FPS > 240 || v.Frames < 1:
			return "video: duration_s, fps and frames must be positive"
		case v.Poster == nil || !strings.HasPrefix(v.Poster.ContentType, "image/") || UploadTypes[v.Poster.ContentType] == "":
			return "video.poster must be png, jpeg or webp"
		case v.Poster.Bytes < 1 || v.Poster.Bytes > MaxUploadBytes || v.Poster.Width < 1 || v.Poster.Height < 1:
			return "video.poster needs bytes (at most 50 MB), width and height"
		}
	default:
		return "kind must be image, video or audio"
	}
	return ""
}

// PluginTakeUpload is POST /plugin/takes's answer.
type PluginTakeUpload struct {
	TakeID          string    `json:"take_id"`
	UploadURL       string    `json:"upload_url"`
	PosterUploadURL *string   `json:"poster_upload_url,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
}
