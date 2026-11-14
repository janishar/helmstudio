package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/helmstudio/studio_service/api/catalog/dto"
	"github.com/helmstudio/studio_service/api/catalog/model"
	"github.com/helmstudio/studio_service/registry"
)

// group is one logical batch of a category's Not-run SKUs.
type group struct {
	key, label string
	count      int
}

// categoryWork is what is left in one category, for the Next step rules.
type categoryWork struct {
	id, name   string
	skus       []model.Sku // every SKU of the category, in order
	notRun     []model.Sku
	generating []string // codes of Queued or Running SKUs
	groups     []group  // of notRun, in order of their first SKU
}

// NextStep works out the Next step card (spec "Next step rules"). It goes through the categories in order and stops at
// the first with work left (something generating, or SKUs not run): generating → no buttons; nothing run → sample this
// category, then the next category not started (else run all of this one); sample run with several groups left → the
// next batch, then run all; one group left → run all, then the next category's sample. With no work left anywhere:
// images waiting → no buttons, pointing to the review; all approved → Download. Waiting images lead the line.
func NextStep(st *state, lib *registry.Library, prices *registry.Prices) dto.NextStep {
	works := categoryWorks(st, lib)
	waiting := codesWith(st, "To review")
	lead := ""
	if len(waiting) > 0 {
		verb := "is"
		if len(waiting) > 1 {
			verb = "are"
		}
		lead = listCodes(waiting) + " " + verb + " ready on the right — approve, reject or write feedback there. "
	}
	if blocked := gatedCodes(st); len(blocked) > 0 {
		lead += listCodes(blocked) + ": main image hasn't passed review — fix or regenerate it on the right, then run the other shots again. "
	}
	if st.session.Stage != "ready" || len(st.skus) == 0 {
		return dto.NextStep{Heading: "Next step", State: "next", Line: "Start processing first.", Options: []dto.NextOption{}}
	}
	for i, work := range works {
		if len(work.generating) == 0 && len(work.notRun) == 0 {
			continue
		}
		card := dto.NextStep{Heading: "Next step · " + work.name, State: "next", Options: []dto.NextOption{}}
		noun := singular(work.name)
		nextSample := func() *dto.NextOption {
			for _, later := range works[i+1:] {
				if len(later.notRun) == len(later.skus) {
					return runOption(st, lib, prices, "Run sample · "+later.name, "sample", later.id, "", later.notRun[:1])
				}
			}
			return nil
		}
		runAll := runOption(st, lib, prices, "Run all "+work.name, "all", work.id, "", work.notRun)
		switch {
		case len(work.generating) > 0:
			card.Heading, card.State, card.Line = "In progress", "in_progress", "Generating "+listCodes(work.generating)+"…"
			return withLead(card, lead)
		case len(work.notRun) == len(work.skus):
			card.Line = "Next, one " + noun + " sample to check the look before the rest."
			card.Options = append(card.Options, *runOption(st, lib, prices, "Run sample · "+work.name, "sample", work.id, "", work.notRun[:1]))
			if next := nextSample(); next != nil {
				card.Options = append(card.Options, *next)
			} else {
				card.Options = append(card.Options, *runAll)
			}
		case len(work.groups) > 1:
			var parts []string
			for _, g := range work.groups {
				parts = append(parts, fmt.Sprintf("%s (%d)", g.label, g.count))
			}
			card.Line = fmt.Sprintf("%d %s left, in groups: %s.", len(work.notRun), countNoun(work.name, len(work.notRun)), strings.Join(parts, ", "))
			first := work.groups[0]
			batch := filter(work.notRun, func(sku model.Sku) bool { return groupKey(sku) == first.key })
			card.Options = append(card.Options, *runOption(st, lib, prices, "Run next batch · "+first.label, "batch", work.id, first.key, batch), *runAll)
		default:
			card.Line = fmt.Sprintf("%d %s left to run.", len(work.notRun), countNoun(work.name, len(work.notRun)))
			card.Options = append(card.Options, *runAll)
			if next := nextSample(); next != nil {
				card.Options = append(card.Options, *next)
			}
		}
		return withLead(card, lead)
	}
	if slices.ContainsFunc(st.skus, func(sku model.Sku) bool { return !resolved(sku) }) {
		return withLead(dto.NextStep{Heading: "Next step", State: "next", Line: "Start processing the new batch to run it.",
			Options: []dto.NextOption{}}, lead)
	}
	if len(codesWith(st, "Approved")) == len(st.skus) {
		return dto.NextStep{Heading: "Next step · Download", State: "done", Line: "All approved. Your catalog is ready.",
			Options: []dto.NextOption{{Label: "Download approved images", Action: dto.NextAction{Type: "download"}}}}
	}
	return withLead(dto.NextStep{Heading: "Review", State: "review",
		Line: "Everything's generated. Approve, reject or write feedback on the images on the right.", Options: []dto.NextOption{}}, lead)
}

// gatedError starts the errors agents give a shot they skipped because its SKU's main image had not passed review
// (agents' run.GATED, run.UNREVIEWED).
const gatedError = "Main image "

// gatedCodes returns, in order, the codes of the SKUs with a live shot skipped by the main-image gate whose main image
// is still not fixed: no other live image of the SKU passed review or was approved after the newest skipped shot.
// ponytail: catalog does not know the anchor pose, so approving any other shot after the gate also clears the lead;
// put the anchor poses in the registry if that misleads.
func gatedCodes(st *state) []string {
	gatedAt, fixedAt := map[string]time.Time{}, map[string]time.Time{}
	for _, image := range liveImages(st.images) {
		var review struct {
			Passed bool `json:"passed"`
		}
		_ = json.Unmarshal(image.Review, &review)
		switch {
		case image.Status == "failed" && image.Error != nil && strings.HasPrefix(*image.Error, gatedError):
			if at, seen := gatedAt[image.SkuID]; !seen || image.UpdatedAt.After(at) {
				gatedAt[image.SkuID] = image.UpdatedAt
			}
		case image.Status == "approved" || image.Status == "to_review" && review.Passed:
			if image.UpdatedAt.After(fixedAt[image.SkuID]) {
				fixedAt[image.SkuID] = image.UpdatedAt
			}
		}
	}
	var codes []string
	for _, sku := range st.skus {
		if at, ok := gatedAt[sku.ID]; ok && !fixedAt[sku.ID].After(at) {
			codes = append(codes, sku.Code)
		}
	}
	return codes
}

// withLead puts the waiting-images sentence in front of the card's line.
func withLead(card dto.NextStep, lead string) dto.NextStep {
	card.Line = lead + card.Line
	return card
}

// categoryWorks splits the started SKUs by category in run order (categories the library doesn't know come last); a
// batch not started yet (after a Refresh) waits for Start.
func categoryWorks(st *state, lib *registry.Library) []*categoryWork {
	var works []*categoryWork
	byID := map[string]*categoryWork{}
	for _, sku := range filter(st.skus, resolved) { // already in category order
		work := byID[sku.Category]
		if work == nil {
			name := sku.Category
			if category := lib.Category(sku.Category); category != nil {
				name = category.Name
			}
			work = &categoryWork{id: sku.Category, name: name}
			byID[sku.Category] = work
			works = append(works, work)
		}
		work.skus = append(work.skus, sku)
		switch st.status[sku.ID] {
		case "Not run":
			work.notRun = append(work.notRun, sku)
			key := groupKey(sku)
			if i := slices.IndexFunc(work.groups, func(g group) bool { return g.key == key }); i >= 0 {
				work.groups[i].count++
			} else {
				label := key
				if sku.GroupLabel != nil && *sku.GroupLabel != "" {
					label = *sku.GroupLabel
				}
				work.groups = append(work.groups, group{key: key, label: label, count: 1})
			}
		case "Queued", "Running":
			work.generating = append(work.generating, sku.Code)
		}
	}
	return works
}

// groupKey is a SKU's batch group; "" when processing gave none.
func groupKey(sku model.Sku) string {
	if sku.GroupKey == nil {
		return ""
	}
	return *sku.GroupKey
}

// runOption is a run button with its credit quote: the SKUs' brief poses × credits per image at their resolution.
func runOption(st *state, lib *registry.Library, prices *registry.Prices, label, scope, category, key string, skus []model.Sku) *dto.NextOption {
	credits, worst := quote(skus, lib, prices)
	return &dto.NextOption{Label: label, Action: dto.NextAction{Type: "run", Scope: scope, Category: category, GroupKey: key},
		Credits: &credits, WorstCredits: &worst}
}

// quote prices rendering every brief pose of skus (a SKU without poses yet counts its category's default poses), and
// the worst case: every image rendered its product's max attempts.
func quote(skus []model.Sku, lib *registry.Library, prices *registry.Prices) (credits, worst float64) {
	for _, sku := range skus {
		poses := len(sku.Brief.Poses)
		if poses == 0 {
			if category := lib.Category(sku.Category); category != nil {
				poses = len(category.DefaultPoses)
			}
		}
		one, most := imageCredits(lib, prices, sku)
		credits, worst = credits+one*float64(poses), worst+most*float64(poses)
	}
	return credits, worst
}

// resolutionOf is the resolution a SKU renders at: its brief's, else its product's default, else the library's.
func resolutionOf(lib *registry.Library, sku model.Sku) string {
	if sku.Brief.Resolution != nil {
		return *sku.Brief.Resolution
	}
	if category := lib.Category(sku.Category); category != nil && category.DefaultResolution != nil {
		return *category.DefaultResolution
	}
	return lib.Defaults.Resolution
}

// imageCredits is one image of a SKU: its credits at the SKU's resolution, and that × its product's max attempts
// (the category's, else catalog_prices').
func imageCredits(lib *registry.Library, prices *registry.Prices, sku model.Sku) (credits, worst float64) {
	credits, _ = creditsPerImage(prices, resolutionOf(lib, sku))
	attempts := prices.MaxAttempts
	if category := lib.Category(sku.Category); category != nil && category.MaxAttempts != nil {
		attempts = *category.MaxAttempts
	}
	return credits, credits * float64(max(1, attempts))
}

// codesWith returns the codes of the SKUs with status, in order.
func codesWith(st *state, status string) []string {
	var codes []string
	for _, sku := range st.skus {
		if st.status[sku.ID] == status {
			codes = append(codes, sku.Code)
		}
	}
	return codes
}

// listCodes names up to three codes: "R-001", "R-001, R-002 and 4 more".
func listCodes(codes []string) string {
	if len(codes) <= 3 {
		return strings.Join(codes, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(codes[:3], ", "), len(codes)-3)
}

// singular turns a category name into the noun of one item: Rings → ring, TV units → TV unit.
func singular(name string) string {
	return strings.TrimSuffix(lower(name), "s")
}

// countNoun names n items of a category: 1 ring, 3 rings (category names are plural already).
func countNoun(name string, n int) string {
	if n == 1 {
		return singular(name)
	}
	return lower(name)
}

// lower lower-cases a name's words except acronyms such as TV.
func lower(name string) string {
	words := strings.Fields(name)
	for i, word := range words {
		if word != strings.ToUpper(word) || len(word) == 1 {
			words[i] = strings.ToLower(word)
		}
	}
	return strings.Join(words, " ")
}

// filter returns the items keep accepts.
func filter[T any](items []T, keep func(T) bool) []T {
	var out []T
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

// plural adds an s for counts other than one.
func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
