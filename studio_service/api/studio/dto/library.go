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

// LibraryItem is a library item: one of the caller's elements, looks or (API 1.30) voices, or a starter look or voice,
// its images signed.
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
	Voice      *VoiceInfo      `json:"voice,omitempty"`  // API 1.30, kind voice only
}

// VoiceInfo is a voice's own fields (API 1.30, contracts/README.md "Voices"): source preset (family, speaker,
// language, style from the pack) or designed (design, language, style from the caller); cloned from 1.31.
type VoiceInfo struct {
	Source   string `json:"source"`
	Family   string `json:"family,omitempty"`
	Speaker  string `json:"speaker,omitempty"`
	Design   string `json:"design,omitempty"`
	Language string `json:"language,omitempty"`
	Style    string `json:"style,omitempty"`
}

// VoiceDesign is a designed voice's fields as the caller writes them (POST and PUT of a voice); Check trims them.
type VoiceDesign struct {
	Design   *string `json:"design"`
	Language *string `json:"language"`
	Style    *string `json:"style"`
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
	Voice  *VoiceDesign      `json:"voice"` // API 1.30, a voice only
}

// LibraryGroups are an element's groups; VoiceGroups a voice's (= its voice.source).
var (
	LibraryGroups = []string{"character", "location", "prop"}
	VoiceGroups   = []string{"preset", "cloned", "designed"}
)

// AllGroups are every library group: an element's, a look's, a voice's.
func AllGroups() []string { return slices.Concat(LibraryGroups, LookGroups, VoiceGroups) }

// Language is a BCP 47 tag as the API takes it (en, en-US, zh, pt-BR).
var Language = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)

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
	look, elem := kind == "look", kind == "element"
	if kind == "voice" { // first: a voice's bad group is "not a voice group", whatever it is
		if detail := f.checkVoice(); detail != "" {
			return detail
		}
	} else if !slices.Contains(AllGroups(), f.Group) {
		return "group must be one of " + strings.Join(AllGroups(), ", ")
	} else if f.Voice != nil {
		return "voice: only a voice has voice fields"
	}
	switch n := utf8.RuneCountInString(f.Name); {
	case look && !slices.Contains(LookGroups, f.Group):
		return "group " + f.Group + " is not a look group"
	case elem && !slices.Contains(LibraryGroups, f.Group):
		return "group " + f.Group + " is not an element group"
	case n < 1 || n > 80:
		return "name must be 1-80 characters"
	case look && f.Handle != nil:
		return "a look has no handle"
	case elem && !Handle.MatchString(f.HandleOrEmpty()):
		return "handle must be a slug: a-z, 0-9, - and _, starting with a letter, ending with a letter or digit, at most 40 characters"
	case utf8.RuneCountInString(*f.Text) > 2000:
		return "text must be at most 2000 characters"
	case strings.ContainsFunc(f.Name, unicode.IsControl):
		return "name must not contain control characters"
	case strings.ContainsFunc(*f.Text, func(r rune) bool { return unicode.IsControl(r) && !strings.ContainsRune("\n\r\t", r) }):
		return "text must not contain control characters other than newlines and tabs"
	case elem && (len(f.Images) < 1 || len(f.Images) > MaxElementImages):
		return "images: an element has 1 to 4 images"
	case look && len(f.Images) > MaxElementImages:
		return "images: a look has at most 4 images"
	case elem && len(f.Loras) > 0:
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

// checkVoice checks a voice's fields in the contract's order (API 1.30): only a designed voice is made here, with no
// handle, text, images or LoRAs, and its design (1-2000 characters, no control characters but newlines and tabs),
// language (a BCP 47 tag) and style (at most 500 characters), trimmed.
func (f *LibraryFields) checkVoice() string {
	switch {
	case !slices.Contains(VoiceGroups, f.Group):
		return "group " + f.Group + " is not a voice group"
	case f.Group == "preset":
		return "preset voices come from packs"
	case f.Group == "cloned":
		return "cloned voices are not available yet"
	case f.Handle != nil:
		return "a voice has no handle"
	case *f.Text != "":
		return "a voice has no text; describe it in voice.design"
	case len(f.Images) > 0:
		return "images: a voice has no images"
	case len(f.Loras) > 0:
		return "loras: a voice has no LoRAs"
	case f.Voice == nil || f.Voice.Design == nil:
		return "voice: a designed voice needs voice.design"
	}
	v := f.Voice
	*v.Design = strings.TrimSpace(*v.Design)
	if v.Style != nil {
		*v.Style = strings.TrimSpace(*v.Style)
	}
	if v.Language != nil {
		*v.Language = strings.TrimSpace(*v.Language)
	}
	switch n := utf8.RuneCountInString(*v.Design); {
	case n < 1 || n > 2000:
		return "voice.design must be 1-2000 characters"
	case strings.ContainsFunc(*v.Design, func(r rune) bool { return unicode.IsControl(r) && !strings.ContainsRune("\n\r\t", r) }):
		return "voice.design must not contain control characters other than newlines and tabs"
	case v.Language != nil && !Language.MatchString(*v.Language):
		return "voice.language: " + *v.Language + " is not a BCP 47 language tag"
	case v.Style != nil && utf8.RuneCountInString(*v.Style) > 500:
		return "voice.style must be at most 500 characters"
	}
	return ""
}

// Designed is the stored VoiceInfo of a checked designed voice.
func (v *VoiceDesign) Designed() VoiceInfo {
	out := VoiceInfo{Source: "designed", Design: *v.Design}
	if v.Language != nil {
		out.Language = *v.Language
	}
	if v.Style != nil {
		out.Style = *v.Style
	}
	return out
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
	case b.Kind != "element" && b.Kind != "look" && b.Kind != "voice":
		return "kind must be element, look or voice"
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
