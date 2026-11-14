// Package registry: the agents' KV `registry` (catalog_library, catalog_prices, catalog_checks), watched and cached.
package registry

import (
	"encoding/json"
	"sync/atomic"
)

// Family is a group of products (jewellery, furniture, garments), in run order.
type Family struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Category is one product (Ring, Sofa …) in run order, with its family, SKU code prefix and ORA defaults; a product
// that does not use a model (UsesModel false) has no default model; DefaultMenModel is its men's wear SKUs' (nil: the
// default model); Wearer is whose wear all its SKUs are ("men" for men's rings; "" = per SKU). MaxAttempts and DefaultResolution override
// catalog_prices.max_attempts and defaults.resolution for this product (nil: the global one).
type Category struct {
	ID                string        `json:"id"`
	Family            string        `json:"family"`
	Name              string        `json:"name"`
	CodePrefix        string        `json:"code_prefix"`
	UsesModel         bool          `json:"uses_model"`
	Subcategories     []Subcategory `json:"subcategories"`
	DefaultPoses      []string      `json:"default_poses"`
	DefaultModel      *string       `json:"default_model"`
	DefaultMenModel   *string       `json:"default_men_model,omitempty"`
	Wearer            string        `json:"wearer,omitempty"`
	DefaultBackground string        `json:"default_background"`
	MaxAttempts       *int          `json:"max_attempts,omitempty"`
	DefaultResolution *string       `json:"default_resolution,omitempty"`
	PoseNoun          PoseNoun      `json:"pose_noun"`
}

// PoseNoun is what the UI and chat call a pose of a product (jewellery pose, furniture angle, garments look).
type PoseNoun struct {
	Singular string `json:"singular"`
	Plural   string `json:"plural"`
}

// Subcategory is a finer group inside a category; About says what it is in one line and Styling how the brief styles
// or wears it (the web's info tip), both passed through to GET /library.
type Subcategory struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	About   string  `json:"about,omitempty"`
	Styling *string `json:"styling,omitempty"`
}

// Frame zooms into part of a shared example picture.
type Frame struct {
	Zoom   float64 `json:"zoom"`
	Origin string  `json:"origin"`
}

// Pose is one pose of a category's library; ExampleURL is a static web path (nil: no example yet).
type Pose struct {
	ID         string  `json:"id"`
	Category   string  `json:"category"`
	Name       string  `json:"name"`
	ExampleURL *string `json:"example_url"`
	Frame      *Frame  `json:"frame,omitempty"`
}

// Model is a built-in model with category tags such as no-necklace; FullBodyURL is its full-body view (nil: none);
// Wearer is whose wear it shows ("women", "men"; "" = any).
type Model struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ImageURL     *string  `json:"image_url"`
	FullBodyURL  *string  `json:"full_body_url,omitempty"`
	CategoryTags []string `json:"category_tags"`
	Note         string   `json:"note,omitempty"`
	Wearer       string   `json:"wearer,omitempty"`
}

// Background is a built-in background with its swatch and preview.
type Background struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Swatch   *string  `json:"swatch"`
	ImageURL *string  `json:"image_url"`
	Families []string `json:"families,omitempty"` // the families it suits; empty = every family
}

// Resolution is an output resolution (1K, 2K, 4K).
type Resolution struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Ratio is an aspect ratio with what it is for.
type Ratio struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Use   string `json:"use"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// Defaults are the output size used when the brand skips resolution & ratio.
type Defaults struct {
	Resolution string `json:"resolution"`
	Ratio      string `json:"ratio"`
}

// Library is the catalog_library value.
type Library struct {
	Families    []Family     `json:"families"`
	Categories  []Category   `json:"categories"`
	Poses       []Pose       `json:"poses"`
	Models      []Model      `json:"models"`
	Backgrounds []Background `json:"backgrounds"`
	Resolutions []Resolution `json:"resolutions"`
	Ratios      []Ratio      `json:"ratios"`
	Defaults    Defaults     `json:"defaults"`
}

// Prices is the catalog_prices value: credits per image by resolution id (quotes only) and the renders per image at
// most (first try + regenerations after failed reviews), for the worst-case quote.
type Prices struct {
	CreditsPerImage map[string]float64 `json:"credits_per_image"`
	MaxAttempts     int                `json:"max_attempts"`
}

// Snapshot is the current registry; a nil part means agents_service has not published it yet.
type Snapshot struct {
	Library *Library
	Prices  *Prices
}

// Ready: the library and prices are known (routes that need them answer 503 until then).
func (s Snapshot) Ready() bool { return s.Library != nil && s.Prices != nil }

// Category finds a category by id; nil when unknown.
func (l *Library) Category(id string) *Category {
	for i := range l.Categories {
		if l.Categories[i].ID == id {
			return &l.Categories[i]
		}
	}
	return nil
}

// DefaultModelFor is the built-in model a SKU of the category with this wearer ("men", "women", "" unknown) defaults
// to: the men's default for men's wear when the category has one, else its default; "" when it has none.
func (c *Category) DefaultModelFor(wearer string) string {
	switch {
	case wearer == "men" && c.DefaultMenModel != nil && *c.DefaultMenModel != "":
		return *c.DefaultMenModel
	case c.DefaultModel != nil:
		return *c.DefaultModel
	}
	return ""
}

// SuitsWearer reports whether a model fits a SKU's wearer: true unless both are known and differ (an upload has no
// wearer, nor has a SKU whose wear is unknown).
func (l *Library) SuitsWearer(modelID, wearer string) bool {
	m := l.Model(modelID)
	return m == nil || m.Wearer == "" || wearer == "" || m.Wearer == wearer
}

// Family finds a family by id; nil when unknown.
func (l *Library) Family(id string) *Family {
	for i := range l.Families {
		if l.Families[i].ID == id {
			return &l.Families[i]
		}
	}
	return nil
}

// Pose finds a pose by id; nil when unknown.
func (l *Library) Pose(id string) *Pose {
	for i := range l.Poses {
		if l.Poses[i].ID == id {
			return &l.Poses[i]
		}
	}
	return nil
}

// Model finds a built-in model by id; nil when unknown.
func (l *Library) Model(id string) *Model {
	for i := range l.Models {
		if l.Models[i].ID == id {
			return &l.Models[i]
		}
	}
	return nil
}

// Background finds a built-in background by id; nil when unknown.
func (l *Library) Background(id string) *Background {
	for i := range l.Backgrounds {
		if l.Backgrounds[i].ID == id {
			return &l.Backgrounds[i]
		}
	}
	return nil
}

// current is the latest snapshot, replaced whole so readers never lock.
var current atomic.Pointer[Snapshot]

// Current returns the latest snapshot; empty before the first KV value arrives.
func Current() Snapshot {
	if snapshot := current.Load(); snapshot != nil {
		return *snapshot
	}
	return Snapshot{}
}

// Set applies one KV value (key catalog_library | catalog_prices); other keys (catalog_checks: agents read it) are ignored.
func Set(key string, value []byte) error {
	snapshot := Current()
	var err error
	switch key {
	case "catalog_library":
		var parsed Library
		if err = json.Unmarshal(value, &parsed); err == nil {
			for i := range parsed.Categories { // a library published before pose nouns says "pose"
				if parsed.Categories[i].PoseNoun.Singular == "" || parsed.Categories[i].PoseNoun.Plural == "" {
					parsed.Categories[i].PoseNoun = PoseNoun{Singular: "pose", Plural: "poses"}
				}
			}
			snapshot.Library = &parsed
		}
	case "catalog_prices":
		var parsed Prices
		if err = json.Unmarshal(value, &parsed); err == nil {
			snapshot.Prices = &parsed
		}
	default:
		return nil
	}
	if err == nil {
		current.Store(&snapshot)
	}
	return err
}
