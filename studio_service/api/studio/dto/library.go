package dto

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"
)

// LibraryItem is a library item: one of the caller's elements or looks, or a starter look, its images signed.
type LibraryItem struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Group      string          `json:"group"`
	Name       string          `json:"name"`
	Handle     *string         `json:"handle"`
	Text       string          `json:"text"`
	Images     []LibraryImage  `json:"images"`
	ThumbURL   *string         `json:"thumb_url"`
	Version    int             `json:"version"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Source     string          `json:"source"` // mine | starter
	Family     *string         `json:"family"` // a starter look's pack family; null for own items
	Loras      []LookLora      `json:"loras"`
	Needs      []string        `json:"needs"`
	FromTakeID *string         `json:"from_take_id"`
	Fits       *bool           `json:"fits,omitempty"`   // only with ?workflow=
	Reason     json.RawMessage `json:"reason,omitempty"` // with fits: a string, or null when it fits
}

// HandleOr is the handle, "" for a look.
func (i LibraryItem) HandleOr() string {
	if i.Handle == nil {
		return ""
	}
	return *i.Handle
}

// LookLora is one LoRA of a look: the LoRA (null: a starter file no live host has), its name and strength.
type LookLora struct {
	Lora     *LoraRef `json:"lora"`
	Name     string   `json:"name"`
	Strength float64  `json:"strength"`
}

// LoraRef names a LoRA.
type LoraRef struct {
	LoraID string `json:"lora_id"`
}

// LookLoraRef is a LoRA a look is saved with.
type LookLoraRef struct {
	Lora     *LoraRef `json:"lora"`
	Strength *float64 `json:"strength"`
}

// LibraryFromTake is "Save as look": fill what the body leaves out from a take the caller made with a run.
type LibraryFromTake struct {
	TakeID string `json:"take_id"`
	Prompt *bool  `json:"prompt"`
	Loras  *bool  `json:"loras"`
	// set by Validate: the body left text / loras out, so the take fills them (unless its option is false)
	FillText  bool `json:"-"`
	FillLoras bool `json:"-"`
}

// LibraryImage is one image of an item: its take, signed links and size.
type LibraryImage struct {
	TakeID   string `json:"take_id"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// LibraryItemList is GET /library.
type LibraryItemList struct {
	Items []LibraryItem `json:"items"`
}

// LibraryImageRef names a take.
type LibraryImageRef struct {
	TakeID string `json:"take_id"`
}

// LibraryFields are what a create and a replace both set; check trims them and says what is wrong for kind.
type LibraryFields struct {
	Group  string            `json:"group"`
	Name   string            `json:"name"`
	Handle *string           `json:"handle"`
	Text   *string           `json:"text"`
	Images []LibraryImageRef `json:"images"`
	Loras  []LookLoraRef     `json:"loras"`
}

// LibraryGroups are an element's groups.
var LibraryGroups = []string{"character", "location", "prop"}

// Handle is an element's handle: what @ mentions.
var Handle = regexp.MustCompile(`^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$`)

// LookID is a look's id: one of the caller's own (a UUID) or a starter look <family>/<pack look id>.
var LookID = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9][a-z0-9_-]{0,63}/[a-z0-9][a-z0-9-]{0,63})$`)

// MaxElementImages is the most images an element (or a look) has; MaxLookLoras the most LoRAs a look has.
const (
	MaxElementImages = 4
	MaxLookLoras     = 4
)

// ClampStrength is a look LoRA's strength in 0..2 (API 1.23; looks saved before held -4..4 and are clamped when read).
func ClampStrength(v float64) float64 { return min(max(v, 0), 2) }

// HandleOrEmpty is the handle, "" when absent.
func (f *LibraryFields) HandleOrEmpty() string {
	if f.Handle == nil {
		return ""
	}
	return *f.Handle
}

// Check trims the fields and returns the 422 detail for an item of kind, "" when valid; a look's need (text, an
// image or a LoRA) is checked by the service once from_take has filled what it fills.
func (f *LibraryFields) Check(kind string) string {
	f.Name = strings.TrimSpace(f.Name)
	if f.Text == nil {
		f.Text = new(string)
	}
	*f.Text = strings.TrimSpace(*f.Text)
	look := kind == "look"
	switch n := utf8.RuneCountInString(f.Name); {
	case !slices.Contains(LibraryGroups, f.Group) && !slices.Contains(LookGroups, f.Group):
		return "group must be one of " + strings.Join(append(slices.Clone(LibraryGroups), LookGroups...), ", ")
	case look && !slices.Contains(LookGroups, f.Group):
		return "group " + f.Group + " is not a look group"
	case !look && !slices.Contains(LibraryGroups, f.Group):
		return "group " + f.Group + " is not an element group"
	case n < 1 || n > 80:
		return "name must be 1-80 characters"
	case look && f.Handle != nil:
		return "a look has no handle"
	case !look && !Handle.MatchString(f.HandleOrEmpty()):
		return "handle must be a slug: a-z, 0-9, - and _, starting with a letter, ending with a letter or digit, at most 40 characters"
	case utf8.RuneCountInString(*f.Text) > 2000:
		return "text must be at most 2000 characters"
	case strings.ContainsFunc(f.Name, unicode.IsControl):
		return "name must not contain control characters"
	case strings.ContainsFunc(*f.Text, func(r rune) bool { return unicode.IsControl(r) && !strings.ContainsRune("\n\r\t", r) }):
		return "text must not contain control characters other than newlines and tabs"
	case !look && (len(f.Images) < 1 || len(f.Images) > MaxElementImages):
		return "images: an element has 1 to 4 images"
	case look && len(f.Images) > MaxElementImages:
		return "images: a look has at most 4 images"
	case !look && len(f.Loras) > 0:
		return "loras: an element has no LoRAs"
	}
	for i, image := range f.Images {
		if !utility.IsUUID(image.TakeID) {
			return fmt.Sprintf("images[%d]: take_id must be a uuid", i+1)
		}
		f.Images[i].TakeID = strings.ToLower(image.TakeID)
		for _, earlier := range f.Images[:i] {
			if earlier.TakeID == f.Images[i].TakeID {
				return fmt.Sprintf("images[%d]: take %s is listed twice", i+1, f.Images[i].TakeID)
			}
		}
	}
	return f.checkLoras()
}

// checkLoras checks the LoRAs' shape: at most 4, each a {lora_id} and a strength 0..2.
func (f *LibraryFields) checkLoras() string {
	if len(f.Loras) > MaxLookLoras {
		return "loras: a look has at most 4 LoRAs"
	}
	for i, l := range f.Loras {
		switch {
		case l.Lora == nil || l.Lora.LoraID == "":
			return fmt.Sprintf("loras[%d].lora: a {lora_id} is required", i+1)
		case l.Strength == nil || *l.Strength < 0 || *l.Strength > 2:
			return fmt.Sprintf("loras[%d].strength: must be between 0 and 2", i+1)
		}
	}
	return ""
}

// LibraryItemCreate is POST /library.
type LibraryItemCreate struct {
	Kind string `json:"kind"`
	LibraryFields
	FromTake *LibraryFromTake `json:"from_take"`
}

// Validate trims the body and returns the 422 detail, "" when valid.
func (b *LibraryItemCreate) Validate() string {
	switch {
	case b.Kind != "element" && b.Kind != "look":
		return "kind must be element or look"
	case b.FromTake != nil && b.Kind != "look":
		return "from_take makes looks only"
	case b.FromTake != nil && !utility.IsUUID(b.FromTake.TakeID):
		return "from_take.take_id must be a uuid"
	}
	if b.FromTake != nil {
		b.FromTake.TakeID = strings.ToLower(b.FromTake.TakeID)
		b.FromTake.FillText = b.Text == nil && (b.FromTake.Prompt == nil || *b.FromTake.Prompt)
		b.FromTake.FillLoras = b.Loras == nil && (b.FromTake.Loras == nil || *b.FromTake.Loras)
	}
	return b.Check(b.Kind)
}

// LibraryItemUpdate is PUT /library/{id}: every field and the version the edit started from (checked against the
// stored item's kind by the service, CheckFor).
type LibraryItemUpdate struct {
	LibraryFields
	Version *int `json:"version"`
}

// Validate checks what does not depend on the kind; the service calls CheckFor with it.
func (b *LibraryItemUpdate) Validate() string {
	switch {
	case b.Text == nil:
		return "text is required"
	case b.Version == nil || *b.Version < 1:
		return "version must be a whole number from 1"
	}
	return ""
}

// CheckFor trims the body and returns the 422 detail for an item of kind, "" when valid.
func (b *LibraryItemUpdate) CheckFor(kind string) string { return b.Check(kind) }
