// Package registry: what the runners publish, watched and cached. KV `registry` keys <host>.<env>.nodes|workflows|models|loras|looks
// (contracts/registry.schema.json) and KV `hosts` keys <host>.<env> (each runner's live state).
package registry

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Stale is how old a hosts key may be before its runner counts as offline.
const Stale = 30 * time.Second

// Param is one param of a node schema.
type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // int | float | bool | string | text | enum | seed | image | rows | frames
	Label   string   `json:"label,omitempty"`
	Default any      `json:"default"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Step    *float64 `json:"step,omitempty"`
	Enum    []string `json:"enum,omitempty"`
	Help    string   `json:"help,omitempty"`
	When    string   `json:"when,omitempty"` // a visibility expression, carried through, not evaluated
	// rows only: field → "Image" (a port type) or an item param (a Param without name), kept raw so it round-trips
	Item    map[string]json.RawMessage `json:"item,omitempty"`
	Mention string                     `json:"mention,omitempty"` // rows only: how the prompt names item n, e.g. <image{n}>
	FPS     string                     `json:"fps,omitempty"`     // frames only: the same node's frame-rate param
	// frames only: present = the value "auto" is allowed (the model predicts the length within min_s..max_s)
	Auto *Auto `json:"auto,omitempty"`
	// int only: the only allowed values; ValuesBy narrows them per value of an enum param of the same node
	Values   []int64   `json:"values,omitempty"`
	ValuesBy *ValuesBy `json:"values_by,omitempty"`
	Position string    `json:"position,omitempty"` // a float item field: "seconds" = a time on the clip (a keyframe's)
}

// Auto is a frames param's auto length bounds in seconds.
type Auto struct {
	MinS float64 `json:"min_s"`
	MaxS float64 `json:"max_s"`
}

// ValuesBy is an int param's allowed values per value of the node's enum param Param.
type ValuesBy struct {
	Param  string             `json:"param"`
	Values map[string][]int64 `json:"values"`
}

// IsImageField reports whether a rows item field holds an Image.
func IsImageField(raw json.RawMessage) bool { return string(raw) == `"Image"` }

// IsMediaField reports whether a rows item field holds an image or a video take (API 1.14, an ask node's media).
func IsMediaField(raw json.RawMessage) bool { return string(raw) == `"Media"` }

// IsLoraField reports whether a rows item field holds a Lora ({lora_id}).
func IsLoraField(raw json.RawMessage) bool { return string(raw) == `"Lora"` }

// LoraField is the Lora field of a rows param's item (the lora-stack's rows), "" for any other param.
func (p Param) LoraField() string {
	if p.Type != "rows" {
		return ""
	}
	for _, name := range slices.Sorted(maps.Keys(p.Item)) {
		if IsLoraField(p.Item[name]) {
			return name
		}
	}
	return ""
}

// ImageInput reports whether the param takes images, and the item field that holds one: "" for an image param, the
// only Image field for a rows param whose item has exactly one (ok false for any other param).
func (p Param) ImageInput() (field string, ok bool) {
	if p.Type == "image" {
		return "", true
	}
	if p.Type != "rows" {
		return "", false
	}
	for name, raw := range p.Item {
		if IsImageField(raw) {
			if ok {
				return "", false
			}
			field, ok = name, true
		}
	}
	return field, ok
}

// Port is a typed input or output of a node.
type Port struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"` // inputs only: may be left unwired
}

// NodeSchema is one node kind a runner can execute.
type NodeSchema struct {
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Family  *string `json:"family"`
	Help    string  `json:"help,omitempty"`
	Params  []Param `json:"params"`
	Inputs  []Port  `json:"inputs"`
	Outputs []Port  `json:"outputs"`
}

// Node is one node of a workflow graph. Bypass and Position belong to an edited workflow: a command never carries them.
type Node struct {
	Kind     string            `json:"kind"`
	Params   map[string]any    `json:"params"` // never nil: a command must carry every node's params
	In       map[string]string `json:"in,omitempty"`
	Bypass   bool              `json:"bypass,omitempty"`
	Position *Position         `json:"position,omitempty"`
}

// Position is a node card's top-left in the Graph view.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PaletteNode is a node kind live hosts publish, with every env that publishes it.
type PaletteNode struct {
	NodeSchema
	Envs []string `json:"envs"`
}

// Family is the part of a kind before "/", "" for a core kind.
func Family(kind string) string {
	family, _, found := strings.Cut(kind, "/")
	if !found {
		return ""
	}
	return family
}

// Passthrough is the input each output passes through when the node is bypassed (the first input of the output's
// type, each once, in output order); nil when a kind of this schema cannot be bypassed.
func (s NodeSchema) Passthrough() []string {
	var through []string
	for _, output := range s.Outputs {
		i := slices.IndexFunc(s.Inputs, func(input Port) bool { return input.Type == output.Type })
		if i < 0 {
			return nil
		}
		if !slices.Contains(through, s.Inputs[i].Name) {
			through = append(through, s.Inputs[i].Name)
		}
	}
	return through
}

// Workflow is a runnable graph as a runner publishes it, with the schemas of its kinds from the same runner.
type Workflow struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Family      string                `json:"family"`
	Env         string                `json:"env"`
	Media       string                `json:"media,omitempty"` // image | video as published; "" = image
	Nodes       map[string]Node       `json:"nodes"`
	Studio      json.RawMessage       `json:"studio,omitempty"` // a StudioLayout as published; absent = the derived form
	NodeSchemas map[string]NodeSchema `json:"node_schemas"`
}

// Host is one hosts KV value.
type Host struct {
	Host      string     `json:"host"`
	Env       string     `json:"env"`
	State     string     `json:"state"` // idle | busy
	RunID     *string    `json:"run_id"`
	Attempt   *int       `json:"attempt,omitempty"` // of run_id while busy; absent = 1
	UpdatedAt time.Time  `json:"updated_at"`
	Memory    *Memory    `json:"memory,omitempty"`
	Resident  []Resident `json:"resident,omitempty"`
}

// Memory is a runner's model memory: what it may use and uses now.
type Memory struct {
	BudgetGB float64 `json:"budget_gb"`
	UsedGB   float64 `json:"used_gb"`
}

// Resident is one model component a runner holds loaded; it names no run or owner.
type Resident struct {
	Component string  `json:"component"`
	Model     string  `json:"model"`
	Variant   string  `json:"variant"`
	Precision string  `json:"precision"`
	GB        float64 `json:"gb"`
	Pinned    bool    `json:"pinned"`
	// when the runner last used it (absent from older runners)
	LastUsed *time.Time `json:"last_used,omitempty"`
	// a component with LoRAs baked in: those LoRAs in order (absent = the plain component)
	Loras []ResidentLora `json:"loras,omitempty"`
}

// ResidentLora is a LoRA baked into a resident component. The runner always fills every field; GET /hosts nulls the
// id, name and strength of another user's own LoRA.
type ResidentLora struct {
	LoraID   *string  `json:"lora_id"`
	Name     *string  `json:"name"`
	Strength *float64 `json:"strength"`
	Bytes    int64    `json:"bytes"`
}

// LoraBlock is a family's LoRA rules (registry models[].lora): what the normaliser needs to fit a file to it.
type LoraBlock struct {
	Component string   `json:"component"`
	Modules   []string `json:"modules"`
	Renames   []Rename `json:"renames"`
	Ignore    []string `json:"ignore"`
	// LoRAs already fused into a variant's weights (a stack row naming one on that variant is refused)
	Prefused []Prefused `json:"prefused,omitempty"`
	// Fake: only env fake publishes the family (set by LoraBlocks); family detection prefers the others
	Fake bool `json:"-"`
}

// Rename is one rewrite of a LoRA key's module part: a prefix, or with At "end" a suffix.
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
	At   string `json:"at,omitempty"` // start (default) | end
}

// Prefused is a LoRA already part of a variant's weights.
type Prefused struct {
	LoraID  string `json:"lora_id"`
	Variant string `json:"variant"`
}

// SharedLora is one entry of a registry loras value: a LoRA file a runner indexed on its host.
type SharedLora struct {
	ID         string    `json:"id"` // the file's SHA-256
	Name       string    `json:"name"`
	Family     string    `json:"family"` // or unknown
	Rank       int       `json:"rank"`
	Bytes      int64     `json:"bytes"`
	KeyStyle   string    `json:"key_style"`
	Licence    *string   `json:"licence"`
	ModifiedAt time.Time `json:"modified_at"`
}

// LiveLora is a shared LoRA as the live hosts of an env have it: the first host's entry (by key), the family the first
// not unknown over them (a non-fake env's before env fake's, as family detection), the oldest mtime, and every host
// that lists it (sorted).
type LiveLora struct {
	SharedLora
	Hosts      []string
	fakeFamily bool // Family came from an env fake host
}

// Model is one model of a registry models value: the models present on a host for an env.
type Model struct {
	ID       string     `json:"id"`
	Family   string     `json:"family"`
	Title    string     `json:"title"`
	Variants []Variant  `json:"variants"` // absent for a model without variants (echo)
	Lora     *LoraBlock `json:"lora,omitempty"`
}

// Variant is one variant of a model on a host.
type Variant struct {
	ID       string       `json:"id"`
	Presence string       `json:"presence"`         // present | missing
	Memory   []MemoryPlan `json:"memory,omitempty"` // per precision: what a run holds (the hand-over)
}

// MemoryPlan is what a run of a variant loaded at Precision holds: its planned peak and the components it loads, in
// the order of their last use.
type MemoryPlan struct {
	Precision  string          `json:"precision"`
	PeakGB     float64         `json:"peak_gb"`
	Components []PlanComponent `json:"components"`
	PlannedAt  *PlannedAt      `json:"planned_at,omitempty"` // the largest sizes PeakGB holds for; nil: every size
	Workflow   string          `json:"workflow,omitempty"`   // API 1.14: plans only runs of this workflow ("" = any)
}

// PlannedAt is the largest megapixels and frames a memory plan's peak was computed at.
type PlannedAt struct {
	MP     float64 `json:"mp"`
	Frames int     `json:"frames"`
	Images *int    `json:"images,omitempty"` // API 1.14, ask workflows: the most thumbnails it holds for
}

// PlanComponent is one component a run loads: the variant that owns its files and the precision it is loaded at.
type PlanComponent struct {
	Component    string  `json:"component"`
	Variant      string  `json:"variant"`
	Precision    string  `json:"precision"`
	GB           float64 `json:"gb"`
	UnloadsAfter bool    `json:"unloads_after,omitempty"`
}

// Has reports whether the variant's files are on the host: always for a model without variants.
func (m Model) Has(variant string) bool {
	return len(m.Variants) == 0 || slices.ContainsFunc(m.Variants, func(v Variant) bool { return v.ID == variant && v.Presence == "present" })
}

// Plan returns the memory plan of variant at precision for a run of workflow, if the runner publishes one: the item
// naming that workflow, else the one naming none.
func (m Model) Plan(variant, precision, workflow string) (MemoryPlan, bool) {
	for _, v := range m.Variants {
		if v.ID != variant {
			continue
		}
		for _, want := range []string{workflow, ""} {
			if i := slices.IndexFunc(v.Memory, func(p MemoryPlan) bool { return p.Precision == precision && p.Workflow == want }); i >= 0 {
				return v.Memory[i], true
			}
		}
	}
	return MemoryPlan{}, false
}

// IsText reports whether a graph answers text (API 1.14): an answer node with its text wired. Such a workflow makes
// no take; only a pipeline's judge, write and decide nodes run it.
func IsText(nodes map[string]Node) bool {
	for _, node := range nodes {
		if _, ok := node.In["text"]; ok && node.Kind == "answer" {
			return true
		}
	}
	return false
}

// Live reports whether the runner refreshed its key within Stale of now.
func (h Host) Live(now time.Time) bool { return now.Sub(h.UpdatedAt) <= Stale }

// Registry holds the latest value of every registry and hosts key. The zero value is empty and ready to use.
type Registry struct {
	mu        sync.RWMutex
	nodes     map[string][]NodeSchema  // "<host>.<env>" → its node schemas
	nodesAt   map[string]time.Time     // "<host>.<env>" → when its nodes were published
	workflows map[string][]Workflow    // "<host>.<env>" → its workflows (without node schemas)
	models    map[string][]Model       // "<host>.<env>" → the models present there
	loras     map[string][]SharedLora  // "<host>.<env>" → the shared LoRAs on that host
	looks     map[string][]StarterLook // "<host>.<env>" → the starter looks of its packs
	looksAt   map[string]time.Time     // "<host>.<env>" → when its looks were published
	hosts     map[string]Host          // "<host>.<env>" → its state
	awaiting  map[string]bool          // watched buckets whose current values have not all arrived yet
	custom    []CustomNode             // pipelines.engine.custom: the custom nodes pipelines' packs register (API 1.15)
}

// CustomNode is one custom node pipelines_service's packs register (registry.schema.json custom, API 1.15).
type CustomNode struct {
	Name    string       `json:"name"`
	Title   string       `json:"title"`
	Help    string       `json:"help"`
	Input   CustomFields `json:"input"`
	Output  CustomFields `json:"output"`
	Network bool         `json:"network"`
	Version string       `json:"version"`
}

// CustomFields is a custom node's input or output model as a JSON schema: its top-level fields.
type CustomFields struct {
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// Custom returns the registered custom nodes (pipelines.engine.custom); nil before pipelines published them.
func (r *Registry) Custom() []CustomNode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.custom
}

// Await marks buckets as not loaded yet: Ready is false until Loaded was called for each.
func (r *Registry) Await(buckets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, bucket := range buckets {
		set(&r.awaiting, bucket, true)
	}
}

// Loaded says the watch of bucket has handed over its current values.
func (r *Registry) Loaded(bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.awaiting, bucket)
}

// Ready reports whether every awaited bucket is loaded: until then an empty or partial view must not be read as
// "no live host". The zero value is ready.
func (r *Registry) Ready() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.awaiting) == 0
}

// SetRegistry applies one KV `registry` value (nil: the key was deleted); keys of other kinds are ignored.
// A value speaks only for the runner its key names: a workflow of another env is refused, and so is a workflow id a
// runner of another env already publishes.
func (r *Registry) SetRegistry(key string, value []byte) error {
	source, kind, ok := cut(key)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch kind {
	case "nodes":
		if value == nil {
			delete(r.nodes, source)
			delete(r.nodesAt, source)
			return nil
		}
		var parsed struct {
			Nodes       []NodeSchema `json:"nodes"`
			PublishedAt time.Time    `json:"published_at"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.nodes, source, parsed.Nodes)
		set(&r.nodesAt, source, parsed.PublishedAt)
	case "workflows":
		if value == nil {
			delete(r.workflows, source)
			return nil
		}
		var parsed struct {
			Workflows []Workflow `json:"workflows"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		env := source[strings.IndexByte(source, '.')+1:]
		kept := []Workflow{}
		for _, workflow := range parsed.Workflows {
			if workflow.Env != env {
				return fmt.Errorf("workflow %s: env %q is not the key's %q", workflow.ID, workflow.Env, env)
			}
			if owner := r.ownerOf(workflow.ID, env); owner != "" {
				log.Printf("registry %s: workflow %s refused: %s publishes it", key, workflow.ID, owner)
				continue
			}
			for id, node := range workflow.Nodes {
				if node.Params == nil {
					node.Params = map[string]any{}
					workflow.Nodes[id] = node
				}
			}
			kept = append(kept, workflow)
		}
		set(&r.workflows, source, kept)
	case "models":
		if value == nil {
			delete(r.models, source)
			return nil
		}
		var parsed struct {
			Models []Model `json:"models"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.models, source, parsed.Models)
	case "looks":
		if value == nil {
			delete(r.looks, source)
			delete(r.looksAt, source)
			return nil
		}
		var parsed struct {
			Looks       []StarterLook `json:"looks"`
			PublishedAt time.Time     `json:"published_at"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		kept := []StarterLook{}
		for _, look := range parsed.Looks {
			if why := badLook(&look); why != "" {
				log.Printf("registry %s: look %s dropped: %s", key, look.ID, why)
				continue
			}
			kept = append(kept, look)
		}
		set(&r.looks, source, kept)
		set(&r.looksAt, source, parsed.PublishedAt)
	case "custom":
		if source != "pipelines.engine" {
			return nil
		}
		r.custom = nil
		if value == nil {
			return nil
		}
		var parsed struct {
			Nodes []CustomNode `json:"nodes"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		r.custom = parsed.Nodes
	case "loras":
		if value == nil {
			delete(r.loras, source)
			return nil
		}
		var parsed struct {
			Loras []SharedLora `json:"loras"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.loras, source, parsed.Loras)
	}
	return nil
}

// Loras returns the shared LoRAs of the live hosts of env ("" = every env), by id.
func (r *Registry) Loras(now time.Time, env string) map[string]LiveLora {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]LiveLora{}
	for _, source := range slices.Sorted(maps.Keys(r.loras)) {
		host, ok := r.hosts[source]
		if !ok || !host.Live(now) || (env != "" && host.Env != env) {
			continue
		}
		for _, entry := range r.loras[source] {
			live, seen := out[entry.ID]
			if !seen {
				live.SharedLora, live.fakeFamily = entry, host.Env == "fake"
			}
			if entry.Family != "unknown" && (live.Family == "unknown" || live.fakeFamily && host.Env != "fake") {
				live.Family, live.fakeFamily = entry.Family, host.Env == "fake"
			}
			if entry.ModifiedAt.Before(live.ModifiedAt) {
				live.ModifiedAt = entry.ModifiedAt
			}
			if !slices.Contains(live.Hosts, host.Host) {
				live.Hosts = append(live.Hosts, host.Host)
				slices.Sort(live.Hosts)
			}
			out[entry.ID] = live
		}
	}
	return out
}

// StarterLook is one entry of a registry looks value: a look a pack ships (contracts/README.md, Looks: starter looks).
type StarterLook struct {
	ID      string        `json:"id"` // <family>/<pack look id>
	Family  string        `json:"family"`
	Group   string        `json:"group"`
	Name    string        `json:"name"`
	Text    string        `json:"text"`
	Version int           `json:"version"`
	Loras   []StarterLora `json:"loras"`
	Thumb   *LookThumb    `json:"thumb"`
}

// StarterLora is a starter look's LoRA: its file, name, strength and shared id (nil: this host has no such file).
type StarterLora struct {
	File     string  `json:"file"`
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
	LoraID   *string `json:"lora_id"`
}

// LookThumb is a starter look's pack image in the object store (packs/<family>/looks/…), only ever a thumbnail.
type LookThumb struct {
	Key string `json:"key"`
}

var (
	lookID    = regexp.MustCompile(`^([a-z0-9][a-z0-9_-]{0,63})/[a-z0-9][a-z0-9-]{0,63}$`)
	lookThumb = regexp.MustCompile(`^packs/([a-z0-9][a-z0-9_-]{0,63})/looks/[a-z0-9][a-z0-9-]{0,63}-[0-9a-f]{16}\.(png|jpg|webp)$`)
)

// badLook says why a published starter look is not kept ("" = kept): its id not <family>/<slug>, or a control
// character in its name or (other than \n \r \t) its text. A thumb key outside packs/<family>/looks/ is dropped
// (never signed), the look kept.
func badLook(l *StarterLook) string {
	if m := lookID.FindStringSubmatch(l.ID); m == nil || m[1] != l.Family {
		return "id is not <family>/<slug>"
	}
	if strings.ContainsFunc(l.Name, unicode.IsControl) ||
		strings.ContainsFunc(l.Text, func(r rune) bool { return unicode.IsControl(r) && !strings.ContainsRune("\n\r\t", r) }) {
		return "control characters in its name or text"
	}
	if l.Thumb != nil {
		if m := lookThumb.FindStringSubmatch(l.Thumb.Key); m == nil || m[1] != l.Family {
			l.Thumb = nil
		}
	}
	return ""
}

// LiveLook is a starter look as the live hosts publish it, from hosts that serve its family: a non-fake env's entry
// before env fake's, then the highest version (a tie: host, then env), each LoRA's id the first non-null over the hosts publishing that version and file, and
// the oldest published_at of the hosts publishing the id.
type LiveLook struct {
	StarterLook
	PublishedAt time.Time
}

// Looks returns the starter looks of the live hosts, by id.
func (r *Registry) Looks(now time.Time) map[string]LiveLook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sources []string
	for source := range r.looks {
		if host, ok := r.hosts[source]; ok && host.Live(now) {
			sources = append(sources, source)
		}
	}
	slices.SortFunc(sources, func(a, b string) int { // non-fake env first, then host, then env
		ha, ea, _ := strings.Cut(a, ".")
		hb, eb, _ := strings.Cut(b, ".")
		return cmp.Or(cmp.Compare(btoi(ea == "fake"), btoi(eb == "fake")), strings.Compare(ha, hb), strings.Compare(ea, eb))
	})
	kept := map[string][]StarterLook{} // the looks of a family the source serves
	for _, source := range sources {
		kept[source] = slices.DeleteFunc(slices.Clone(r.looks[source]), func(l StarterLook) bool {
			return !slices.ContainsFunc(r.models[source], func(m Model) bool { return m.Family == l.Family })
		})
	}
	out := map[string]LiveLook{}
	real := map[string]bool{} // ids a non-fake env publishes: a fake env's entry never replaces theirs
	for _, source := range sources {
		_, env, _ := strings.Cut(source, ".")
		fake := env == "fake"
		for _, entry := range kept[source] {
			live, seen := out[entry.ID]
			if !fake {
				real[entry.ID] = true
			}
			if !seen || entry.Version > live.Version && (!fake || !real[entry.ID]) {
				at := live.PublishedAt
				live.StarterLook = entry
				live.Loras = slices.Clone(entry.Loras)
				if !seen || r.looksAt[source].Before(at) {
					at = r.looksAt[source]
				}
				live.PublishedAt = at
			} else if r.looksAt[source].Before(live.PublishedAt) {
				live.PublishedAt = r.looksAt[source]
			}
			out[entry.ID] = live
		}
	}
	for _, source := range sources { // a LoRA this host lacks: another host's id for the same version and file
		for _, entry := range kept[source] {
			live := out[entry.ID]
			if entry.Version != live.Version {
				continue
			}
			for i, l := range live.Loras {
				if l.LoraID != nil {
					continue
				}
				if at := slices.IndexFunc(entry.Loras, func(e StarterLora) bool { return e.File == l.File && e.LoraID != nil }); at >= 0 {
					live.Loras[i].LoraID = entry.Loras[at].LoraID
				}
			}
		}
	}
	return out
}

// HasLora reports whether the runner at source ("<host>.<env>") lists the shared LoRA id.
func (r *Registry) HasLora(source, id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.ContainsFunc(r.loras[source], func(l SharedLora) bool { return l.ID == id })
}

// LoraBlocks returns every family's lora block any runner publishes (live or not), by family: the first source by key;
// Fake when every source publishing it is env fake.
func (r *Registry) LoraBlocks() map[string]LoraBlock {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]LoraBlock{}
	for _, source := range slices.Sorted(maps.Keys(r.models)) {
		_, env, _ := strings.Cut(source, ".")
		for _, model := range r.models[source] {
			if model.Lora == nil {
				continue
			}
			block, seen := out[model.Family]
			if !seen {
				block = *model.Lora
				block.Fake = true
			}
			block.Fake = block.Fake && env == "fake"
			out[model.Family] = block
		}
	}
	return out
}

// ownerOf returns the source of another env that publishes workflow id, or "". Callers hold r.mu.
func (r *Registry) ownerOf(id, env string) string {
	for _, source := range slices.Sorted(maps.Keys(r.workflows)) {
		for _, workflow := range r.workflows[source] {
			if workflow.ID == id && workflow.Env != env {
				return source
			}
		}
	}
	return ""
}

// SetHost applies one KV `hosts` value (nil: the key was deleted, the host is offline). The value's host and env
// must be the key's <host>.<env>.
func (r *Registry) SetHost(key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if value == nil {
		delete(r.hosts, key)
		return nil
	}
	var host Host
	if err := json.Unmarshal(value, &host); err != nil {
		return err
	}
	if host.Host+"."+host.Env != key || strings.Count(key, ".") != 1 {
		return fmt.Errorf("hosts value %s.%s is not its key's", host.Host, host.Env)
	}
	set(&r.hosts, key, host)
	return nil
}

// Workflows returns every workflow whose kinds all have a schema from the same runner, by id; the same id from several
// runners is listed once (the first runner by key wins).
func (r *Registry) Workflows() []Workflow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Workflow{}
	seen := map[string]bool{}
	for _, source := range slices.Sorted(maps.Keys(r.workflows)) {
		schemas := map[string]NodeSchema{}
		for _, schema := range r.nodes[source] {
			schemas[schema.Kind] = schema
		}
		for _, workflow := range r.workflows[source] {
			if seen[workflow.ID] {
				continue
			}
			if complete := withSchemas(workflow, schemas); complete != nil {
				seen[workflow.ID] = true
				out = append(out, *complete)
			}
		}
	}
	slices.SortFunc(out, func(a, b Workflow) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Workflow returns the workflow with id, or false.
func (r *Registry) Workflow(id string) (Workflow, bool) {
	for _, workflow := range r.Workflows() {
		if workflow.ID == id {
			return workflow, true
		}
	}
	return Workflow{}, false
}

// Hosts returns every hosts value, by key.
func (r *Registry) Hosts() []Host {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Host{}
	for _, key := range slices.Sorted(maps.Keys(r.hosts)) {
		out = append(out, r.hosts[key])
	}
	return out
}

// Publishes reports whether the runner at source ("<host>.<env>") publishes the workflow id.
func (r *Registry) Publishes(source, id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.ContainsFunc(r.workflows[source], func(w Workflow) bool { return w.ID == id })
}

// PublishesKinds reports whether the runner at source ("<host>.<env>") publishes every one of kinds.
func (r *Registry) PublishesKinds(source string, kinds []string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, kind := range kinds {
		if !slices.ContainsFunc(r.nodes[source], func(s NodeSchema) bool { return s.Kind == kind }) {
			return false
		}
	}
	return true
}

// Schemas returns every kind any runner publishes, live or not: the schema of the newest publish.
func (r *Registry) Schemas() map[string]NodeSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]NodeSchema{}
	for kind, found := range r.newest(func(string) bool { return true }) {
		out[kind] = found.schema
	}
	return out
}

// Palette returns every kind a live host publishes now (its hosts key fresh), with the envs that publish it, sorted,
// and the schema of the newest publish.
func (r *Registry) Palette(now time.Time) map[string]PaletteNode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]PaletteNode{}
	for kind, found := range r.newest(func(source string) bool { host, ok := r.hosts[source]; return ok && host.Live(now) }) {
		out[kind] = PaletteNode{NodeSchema: found.schema, Envs: found.envs}
	}
	return out
}

// PaletteGroup is one family's kinds in the palette (Family "" for the core kinds).
type PaletteGroup struct {
	Family *string       `json:"family"`
	Title  string        `json:"title"`
	Nodes  []PaletteNode `json:"nodes"`
}

// PaletteGroups is the palette for GET /nodes: only kinds env publishes (all when ""), the core kinds first (title
// Core), then each family by title (its live models entry's title, else the family id); in a group, kinds in the
// order their newest publisher lists them.
func (r *Registry) PaletteGroups(now time.Time, env string) []PaletteGroup {
	r.mu.RLock()
	defer r.mu.RUnlock()
	live := func(source string) bool { host, ok := r.hosts[source]; return ok && host.Live(now) }
	byFamily := map[string][]found{}
	for _, found := range r.newest(live) {
		if env == "" || slices.Contains(found.envs, env) {
			family := Family(found.schema.Kind)
			byFamily[family] = append(byFamily[family], found)
		}
	}
	titles := map[string]string{}
	for source, models := range r.models {
		for _, model := range models {
			if live(source) && model.Title != "" {
				titles[model.Family] = model.Title
			}
		}
	}
	out := []PaletteGroup{}
	for family, kinds := range byFamily {
		slices.SortFunc(kinds, func(a, b found) int {
			return cmp.Or(cmp.Compare(a.index, b.index), strings.Compare(a.schema.Kind, b.schema.Kind))
		})
		group := PaletteGroup{Title: cmp.Or(titles[family], family)}
		if family == "" {
			group.Title = "Core"
		} else {
			group.Family = &family
		}
		for _, kind := range kinds {
			group.Nodes = append(group.Nodes, PaletteNode{NodeSchema: kind.schema, Envs: kind.envs})
		}
		out = append(out, group)
	}
	slices.SortFunc(out, func(a, b PaletteGroup) int {
		return cmp.Or(cmp.Compare(btoi(a.Family != nil), btoi(b.Family != nil)), strings.Compare(a.Title, b.Title))
	})
	return out
}

// found is one kind as newest finds it: the newest schema, its index in that publisher's list, every env.
type found struct {
	schema NodeSchema
	index  int
	at     time.Time
	envs   []string
}

// newest collects every kind the sources keep publishes: the schema of the newest publish (ties: the first source
// by key) and every env, sorted. Callers hold r.mu.
func (r *Registry) newest(keep func(source string) bool) map[string]found {
	out := map[string]found{}
	for _, source := range slices.Sorted(maps.Keys(r.nodes)) {
		if !keep(source) {
			continue
		}
		env := source[strings.IndexByte(source, '.')+1:]
		at := r.nodesAt[source]
		for i, schema := range r.nodes[source] {
			old, seen := out[schema.Kind]
			envs := old.envs
			if !slices.Contains(envs, env) {
				envs = append(slices.Clone(envs), env)
				slices.Sort(envs)
			}
			if !seen || at.After(old.at) {
				old = found{schema: schema, index: i, at: at}
			}
			old.envs = envs
			out[schema.Kind] = old
		}
	}
	return out
}

// btoi is 1 for true.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Model returns the model of family that the runner at source ("<host>.<env>") has, or false.
func (r *Registry) Model(source, family string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, model := range r.models[source] {
		if model.Family == family {
			return model, true
		}
	}
	return Model{}, false
}

// withSchemas returns the workflow with the schemas of its kinds, or nil when one is missing.
func withSchemas(workflow Workflow, schemas map[string]NodeSchema) *Workflow {
	workflow.NodeSchemas = map[string]NodeSchema{}
	for _, node := range workflow.Nodes {
		schema, ok := schemas[node.Kind]
		if !ok {
			return nil
		}
		workflow.NodeSchemas[node.Kind] = schema
	}
	return &workflow
}

// cut splits a registry key "<host>.<env>.<kind>" into "<host>.<env>" and kind.
func cut(key string) (string, string, bool) {
	i := strings.LastIndexByte(key, '.')
	if i <= 0 || strings.Count(key, ".") != 2 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// set puts value under key in *m, making the map on first use.
func set[V any](m *map[string]V, key string, value V) {
	if *m == nil {
		*m = map[string]V{}
	}
	(*m)[key] = value
}
