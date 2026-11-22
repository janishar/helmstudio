package dto

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/registry"
)

// Workflow is a template (source template) or one of the caller's own (source mine) with the schemas of its kinds.
type Workflow struct {
	ID          string                         `json:"id"`
	Title       string                         `json:"title"`
	Action      string                         `json:"action,omitempty"` // API 1.19, templates: the take action's label
	Family      string                         `json:"family"`
	Env         string                         `json:"env"`
	Media       string                         `json:"media"` // image | video
	Nodes       map[string]registry.Node       `json:"nodes"`
	NodeSchemas map[string]registry.NodeSchema `json:"node_schemas"`
	Source      string                         `json:"source"`
	Studio      *StudioLayout                  `json:"studio,omitempty"`
	// mine only: the id it was made from, or null. A nil interface is left out (templates); a nil *string is null.
	BasedOn   any        `json:"based_on,omitempty"`
	Version   *int       `json:"version,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Problems  *[]Problem `json:"problems,omitempty"` // only from get, create and replace
	// API 1.17, always sent: the keyframe rows New shot binds (null: neither option is offered); 1.37 or image params
	Keyframes *timeline.Keyframes `json:"keyframes"`
}

// StudioLayout is which params Create shows and where (contracts StudioLayout).
type StudioLayout struct {
	Composer  []string                  `json:"composer,omitempty"`
	Quick     []string                  `json:"quick,omitempty"`
	Inspector []StudioSection           `json:"inspector,omitempty"`
	Labels    map[string]string         `json:"labels,omitempty"`
	Presets   map[string]map[string]any `json:"presets,omitempty"`
	// API 1.25: every preset name once, in the order Create shows them (a map's order is not kept)
	PresetOrder []string `json:"preset_order,omitempty"`
	// API 1.37: the image params a New shot binds its frames into (Workflow.keyframes)
	Keyframes *timeline.FrameParams `json:"keyframes,omitempty"`
}

// FrameParams is the layout's studio.keyframes (API 1.37), nil without a layout or without it.
func (l *StudioLayout) FrameParams() *timeline.FrameParams {
	if l == nil {
		return nil
	}
	return l.Keyframes
}

// StudioSection is one inspector section.
type StudioSection struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
	Help  *string  `json:"help,omitempty"` // API 1.25: the section's (i) text
	// API 1.26: tiles that lead the section, each a pick among named sets of values (checked as presets)
	Choices []StudioChoice `json:"choices,omitempty"`
}

// StudioChoice is one choice tile: its options in order, each a set of values (LTX's Recipe).
type StudioChoice struct {
	Label   string  `json:"label"`
	Help    *string `json:"help,omitempty"`
	Options []struct {
		Name string         `json:"name"`
		Set  map[string]any `json:"set"`
	} `json:"options"`
}

// Sets are the layout's named sets of values that must be valid as presets: the presets, then every choice option
// ("<label>: <option>").
func (l *StudioLayout) Sets() map[string]map[string]any {
	out := maps.Clone(l.Presets)
	if out == nil {
		out = map[string]map[string]any{}
	}
	for _, section := range l.Inspector {
		for _, choice := range section.Choices {
			for _, option := range choice.Options {
				out[choice.Label+": "+option.Name] = option.Set
			}
		}
	}
	return out
}

// LookGroups are a look's groups, in the order the composer's default tiles show them.
var LookGroups = []string{"film", "camera", "colour", "lighting"}

// IsTile reports whether a studio.quick entry is a library tile (elements, look:<group>), not a path.
func IsTile(entry string) bool {
	group, isLook := strings.CutPrefix(entry, "look:")
	return entry == "elements" || isLook && slices.Contains(LookGroups, group)
}

// Placed is every path placed in composer, quick and the inspector, in that order.
func (l *StudioLayout) Placed() []string {
	if l == nil {
		return nil
	}
	out := append(append([]string{}, l.Composer...), l.Quick...)
	for _, section := range l.Inspector {
		out = append(out, section.Items...)
	}
	return out
}

// WorkflowDoc is a workflow as the editor saves or validates it.
type WorkflowDoc struct {
	Title  string                   `json:"title"`
	Family string                   `json:"family"`
	Env    string                   `json:"env"`
	Nodes  map[string]registry.Node `json:"nodes"`
	Studio *StudioLayout            `json:"studio,omitempty"`
}

// WorkflowCreate is POST /workflows: a whole document with optional based_on, or {based_on, title?} to copy.
type WorkflowCreate struct {
	WorkflowDoc
	BasedOn *string `json:"based_on"`
}

// IsCopy reports whether the body is the copy form: no document fields but the title.
func (b *WorkflowCreate) IsCopy() bool {
	return b.Family == "" && b.Env == "" && b.Nodes == nil && b.Studio == nil
}

// Validate checks either form's shape.
func (b *WorkflowCreate) Validate() string {
	if b.BasedOn != nil && (*b.BasedOn == "" || utf8.RuneCountInString(*b.BasedOn) > 64) {
		return "based_on must be 1-64 characters"
	}
	if b.IsCopy() {
		if b.BasedOn == nil {
			return "title, family, env and nodes are required (or based_on to copy)"
		}
		if b.Title != "" {
			return validTitle(b.Title)
		}
		return ""
	}
	return b.WorkflowDoc.Validate()
}

// WorkflowUpdate is PUT /workflows/{id}: the document and the version the edit started from.
type WorkflowUpdate struct {
	WorkflowDoc
	Version int `json:"version"`
}

// Validate checks the document and the version.
func (b *WorkflowUpdate) Validate() string {
	if b.Version < 1 {
		return "version is required (the version the edit started from)"
	}
	return b.WorkflowDoc.Validate()
}

// WorkflowPatch renames a workflow.
type WorkflowPatch struct {
	Title string `json:"title"`
}

// Validate wants a title of 1-80 characters after trimming.
func (b *WorkflowPatch) Validate() string { return validTitle(b.Title) }

// Problem is one broken graph rule; Node, Port and Param say where ("" = the whole workflow).
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Blocks  string `json:"blocks"` // save | run
	Node    string `json:"node,omitempty"`
	Port    string `json:"port,omitempty"`
	Param   string `json:"param,omitempty"`
}

// NodePalette is GET /nodes: the live kinds by family.
type NodePalette struct {
	Groups []registry.PaletteGroup `json:"groups"`
}

// WorkflowValidation is POST /workflows/validate's answer.
type WorkflowValidation struct {
	Problems []Problem `json:"problems"`
	CanSave  bool      `json:"can_save"`
	CanRun   bool      `json:"can_run"`
}

var (
	slug      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	family    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	envName   = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	kindName  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})?$`)
	reference = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}$`)
)

// validTitle wants 1-80 characters after trimming.
func validTitle(title string) string {
	if n := utf8.RuneCountInString(strings.TrimSpace(title)); n == 0 || n > 80 {
		return "title must be 1-80 characters"
	}
	return ""
}

// Validate checks the document's shape (contracts WorkflowDoc); the graph rules are problems, checked elsewhere.
// It gives every node non-nil params.
func (d *WorkflowDoc) Validate() string {
	if detail := validTitle(d.Title); detail != "" {
		return detail
	}
	switch {
	case !family.MatchString(d.Family):
		return "family must be a slug"
	case !envName.MatchString(d.Env):
		return "env must be 1-32 lower-case letters and digits"
	case len(d.Nodes) == 0:
		return "nodes must have at least one node"
	}
	for id, node := range d.Nodes {
		if !slug.MatchString(id) {
			return fmt.Sprintf("nodes.%s: a node id must match ^[a-z][a-z0-9_]{0,63}$", id)
		}
		if !kindName.MatchString(node.Kind) {
			return fmt.Sprintf("nodes.%s.kind: %q is not a node kind", id, node.Kind)
		}
		for name := range node.Params {
			if !slug.MatchString(name) {
				return fmt.Sprintf("nodes.%s.params: %q is not a param name", id, name)
			}
		}
		for key, ref := range node.In {
			if !slug.MatchString(key) || !reference.MatchString(ref) {
				return fmt.Sprintf("nodes.%s.in.%s: must name <node id>.<output>", id, key)
			}
		}
		if p := node.Position; p != nil && (p.X < -1e6 || p.X > 1e6 || p.Y < -1e6 || p.Y > 1e6) {
			return fmt.Sprintf("nodes.%s.position: x and y must be within ±1000000", id)
		}
		if node.Params == nil {
			node.Params = map[string]any{}
			d.Nodes[id] = node
		}
	}
	return d.Studio.validate()
}

// validate checks the layout's shape (sizes and path syntax).
func (l *StudioLayout) validate() string {
	if l == nil {
		return ""
	}
	paths := func(where string, list []string, most int) string {
		if len(list) > most {
			return fmt.Sprintf("studio.%s: at most %d items", where, most)
		}
		for _, path := range list {
			if !reference.MatchString(path) && !(where == "quick" && IsTile(path)) {
				return fmt.Sprintf("studio.%s: %q is not <node>.<param>", where, path)
			}
		}
		return ""
	}
	if detail := paths("composer", l.Composer, 8); detail != "" {
		return detail
	}
	if detail := paths("quick", l.Quick, 12); detail != "" {
		return detail
	}
	if len(l.Inspector) > 12 {
		return "studio.inspector: at most 12 sections"
	}
	for i, section := range l.Inspector {
		if n := utf8.RuneCountInString(section.Title); n == 0 || n > 40 || section.Items == nil {
			return fmt.Sprintf("studio.inspector[%d]: a title of 1-40 characters and items are required", i)
		}
		if detail := paths(fmt.Sprintf("inspector[%d].items", i), section.Items, 32); detail != "" {
			return detail
		}
		if section.Help != nil && (*section.Help == "" || utf8.RuneCountInString(*section.Help) > 300) {
			return fmt.Sprintf("studio.inspector[%d].help: 1-300 characters", i)
		}
		if len(section.Choices) > 4 {
			return fmt.Sprintf("studio.inspector[%d].choices: at most 4", i)
		}
		for j, choice := range section.Choices {
			where := fmt.Sprintf("studio.inspector[%d].choices[%d]", i, j)
			if n := utf8.RuneCountInString(choice.Label); n == 0 || n > 40 || len(choice.Options) < 2 || len(choice.Options) > 8 {
				return where + ": a label of 1-40 characters and 2-8 options"
			}
			if choice.Help != nil && (*choice.Help == "" || utf8.RuneCountInString(*choice.Help) > 300) {
				return where + ".help: 1-300 characters"
			}
			for _, option := range choice.Options {
				if n := utf8.RuneCountInString(option.Name); n == 0 || n > 40 || len(option.Set) == 0 || len(option.Set) > 32 {
					return where + ".options: a name of 1-40 characters and 1-32 values each"
				}
				for path := range option.Set {
					if !reference.MatchString(path) {
						return fmt.Sprintf("%s.options: %q is not <node>.<param>", where, path)
					}
				}
			}
		}
	}
	if len(l.Labels) > 64 {
		return "studio.labels: at most 64 labels"
	}
	for path, label := range l.Labels {
		if n := utf8.RuneCountInString(label); !reference.MatchString(path) || n == 0 || n > 60 {
			return fmt.Sprintf("studio.labels.%s: a <node>.<param> path and a label of 1-60 characters", path)
		}
	}
	if len(l.Presets) > 8 {
		return "studio.presets: at most 8 presets"
	}
	for name, values := range l.Presets {
		if n := utf8.RuneCountInString(name); n == 0 || n > 40 || len(values) == 0 || len(values) > 32 {
			return fmt.Sprintf("studio.presets.%s: a name of 1-40 characters and 1-32 values", name)
		}
		for path := range values {
			if !reference.MatchString(path) {
				return fmt.Sprintf("studio.presets.%s: %q is not <node>.<param>", name, path)
			}
		}
	}
	if l.PresetOrder != nil {
		seen := map[string]bool{}
		for _, name := range l.PresetOrder {
			if _, ok := l.Presets[name]; !ok || seen[name] {
				return "studio.preset_order: every preset once"
			}
			seen[name] = true
		}
		if len(seen) != len(l.Presets) {
			return "studio.preset_order: every preset once"
		}
	}
	return ""
}
