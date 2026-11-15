package dto

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"
)

// LibraryItem is one of the caller's library items (an element; looks later), its images signed.
type LibraryItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Group     string         `json:"group"`
	Name      string         `json:"name"`
	Handle    *string        `json:"handle"`
	Text      string         `json:"text"`
	Images    []LibraryImage `json:"images"`
	ThumbURL  *string        `json:"thumb_url"`
	Version   int            `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
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

// LibraryFields are what a create and a replace both set; Check trims them and says what is wrong.
type LibraryFields struct {
	Group  string            `json:"group"`
	Name   string            `json:"name"`
	Handle string            `json:"handle"`
	Text   *string           `json:"text"`
	Images []LibraryImageRef `json:"images"`
}

// LibraryGroups are an element's groups.
var LibraryGroups = []string{"character", "location", "prop"}

// Handle is an element's handle: what @ mentions.
var Handle = regexp.MustCompile(`^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$`)

// MaxElementImages is the most images an element has.
const MaxElementImages = 4

func (f *LibraryFields) check() string {
	f.Name = strings.TrimSpace(f.Name)
	if f.Text == nil {
		f.Text = new(string)
	}
	*f.Text = strings.TrimSpace(*f.Text)
	switch n := utf8.RuneCountInString(f.Name); {
	case f.Group != "character" && f.Group != "location" && f.Group != "prop":
		return "group must be one of " + strings.Join(LibraryGroups, ", ")
	case n < 1 || n > 80:
		return "name must be 1-80 characters"
	case !Handle.MatchString(f.Handle):
		return "handle must be a slug: a-z, 0-9, - and _, starting with a letter, ending with a letter or digit, at most 40 characters"
	case utf8.RuneCountInString(*f.Text) > 2000:
		return "text must be at most 2000 characters"
	case strings.ContainsFunc(f.Name, unicode.IsControl):
		return "name must not contain control characters"
	case strings.ContainsFunc(*f.Text, func(r rune) bool { return unicode.IsControl(r) && !strings.ContainsRune("\n\r\t", r) }):
		return "text must not contain control characters other than newlines and tabs"
	case len(f.Images) < 1 || len(f.Images) > MaxElementImages:
		return "images: an element has 1 to 4 images"
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
	return ""
}

// LibraryItemCreate is POST /library.
type LibraryItemCreate struct {
	Kind string `json:"kind"`
	LibraryFields
}

// Validate trims the body and returns the 422 detail, "" when valid.
func (b *LibraryItemCreate) Validate() string {
	if b.Kind != "element" {
		return "kind must be element"
	}
	return b.check()
}

// LibraryItemUpdate is PUT /library/{id}: every field and the version the edit started from.
type LibraryItemUpdate struct {
	LibraryFields
	Version *int `json:"version"`
}

// Validate trims the body and returns the 422 detail, "" when valid.
func (b *LibraryItemUpdate) Validate() string {
	switch {
	case b.Text == nil:
		return "text is required"
	case b.Version == nil || *b.Version < 1:
		return "version must be a whole number from 1"
	}
	return b.check()
}
