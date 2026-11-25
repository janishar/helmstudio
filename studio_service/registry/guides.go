package registry

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// Guide is one entry of a registry guides value (API 1.71, contracts/README.md "Assistant: model guides"): a model
// family's assistant guide, its card and its recipes, as its pack ships them.
type Guide struct {
	Family  string   `json:"family"`
	Title   string   `json:"title"`
	Card    string   `json:"card"`
	Guide   string   `json:"guide"`
	Recipes []Recipe `json:"recipes"`
}

// Recipe is a guide's named settings: values by "<node>.<param>".
type Recipe struct {
	Name     string         `json:"name"`
	Title    string         `json:"title"`
	Workflow string         `json:"workflow"`
	Params   map[string]any `json:"params"`
	Note     *string        `json:"note,omitempty"`
}

// Guides returns, per family, the guide of the newest guides publish over the known hosts (a present hosts key, as
// Model), and whether a known host holds the family (a present variant; a model without variants counts as held).
func (r *Registry) Guides() (map[string]Guide, map[string]bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out, at, held := map[string]Guide{}, map[string]time.Time{}, map[string]bool{}
	for _, source := range slices.Sorted(maps.Keys(r.guides)) {
		if !r.present(source) {
			continue
		}
		for _, g := range r.guides[source] {
			if old, seen := at[g.Family]; !seen || r.guidesAt[source].After(old) {
				out[g.Family], at[g.Family] = g, r.guidesAt[source]
			}
		}
	}
	for source, models := range r.models {
		if !r.present(source) {
			continue
		}
		for _, m := range models {
			if len(m.Variants) == 0 || slices.ContainsFunc(m.Variants, func(v Variant) bool { return v.Presence == "present" }) {
				held[m.Family] = true
			}
		}
	}
	return out, held
}

// Cards are a turn's model cards: each held family's guide, sorted by family, so the prompt's start changes only
// when the models held change.
func (r *Registry) Cards() []Guide {
	guides, held := r.Guides()
	out := []Guide{}
	for family, g := range guides {
		if held[family] {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b Guide) int { return strings.Compare(a.Family, b.Family) })
	return out
}
