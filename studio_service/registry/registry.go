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
	"unicode/utf8"
)

// Stale is how old a hosts key may be before its runner counts as offline.
const Stale = 30 * time.Second

// Param is one param of a node schema.
type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // int | float | bool | string | text | enum | seed | image | rows | frames | media | span
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
	// int only: the only allowed values; ValuesBy narrows them (from API 1.39 an enum's too) per value of an enum param
	// of the same node or, from 1.39, of another node ("model.variant")
	Values   []int64   `json:"values,omitempty"`
	ValuesBy *ValuesBy `json:"values_by,omitempty"`
	Position string    `json:"position,omitempty"` // a float item field: "seconds" = a time on the clip (a keyframe's)
	// media only (API 1.19, contracts/README.md "Media inputs"): the take kinds it takes (video, audio), whether the
	// value may carry in/out, the px grid a video's sides keep, the longest it reads and the most video frames
	Accepts   []string `json:"accepts,omitempty"`
	Trim      bool     `json:"trim,omitempty"`
	Grid      int      `json:"grid,omitempty"`
	MaxS      float64  `json:"max_s,omitempty"`
	MaxFrames int      `json:"max_frames,omitempty"`
	// API 1.22 (contracts/README.md "Media inputs: rows of media"): media: the shortest it reads; rows with one media
	// field: the most seconds its items read together; rows: optional inputs of the node that, wired, keep it empty
	MinS      float64  `json:"min_s,omitempty"`
	MaxTotalS float64  `json:"max_total_s,omitempty"`
	NotWith   []string `json:"not_with,omitempty"`
	// API 1.37: a rows item's media field that may be left empty (MiniMax H3's soundtrack); not the item's media field
	Optional bool `json:"optional,omitempty"`
	// text only (API 1.32, contracts/README.md "Dialogue and Timeline audio"): a dialogue script, expanded into the
	// node's `lines` rows at create time
	Script bool `json:"script,omitempty"`
	// enum only (API 1.41, contracts/README.md "Optional add-ons": an add-on per value): value → the add-ons a run
	// needs while the param holds it; AddonsWith: an optional input or rows param → value → the add-ons that replace
	// them while it is wired or holds a row (MiniMax H3's lora: turbo, with a reference turbo_ref)
	Addons     map[string][]string            `json:"addons,omitempty"`
	AddonsWith map[string]map[string][]string `json:"addons_with,omitempty"`
	// API 1.47 (contracts/README.md, Param rules, option help and memory per run): refusals as data, checked by
	// resolve, and each value's line in the param's menu (served as is)
	Rules      []Rule            `json:"rules,omitempty"`
	OptionHelp map[string]string `json:"option_help,omitempty"`
}

// Rule is one of a param's refusals (API 1.47): while When holds the run is refused with Error.
type Rule struct {
	When  string `json:"when"`
	Error string `json:"error"`
}

// ValueAddons is the add-ons the param asks for at value (API 1.41): the lists of its AddonsWith names in wired,
// together, else its Addons list for the value.
func (p Param) ValueAddons(value string, wired func(string) bool) []string {
	var out []string
	hit := false
	for _, w := range slices.Sorted(maps.Keys(p.AddonsWith)) {
		if wired(w) {
			hit = true
			for _, c := range p.AddonsWith[w][value] {
				if !slices.Contains(out, c) {
					out = append(out, c)
				}
			}
		}
	}
	if hit {
		return out
	}
	return p.Addons[value]
}

// Auto is a frames param's auto length bounds in seconds.
type Auto struct {
	MinS float64 `json:"min_s"`
	MaxS float64 `json:"max_s"`
}

// ValuesBy is an int's (from API 1.39 an enum's) allowed values per value of enum param Param: the node's own, or
// (1.39) another node's in the `when` reference form "<node>.<param>". Values hold JSON numbers or strings.
type ValuesBy struct {
	Param  string           `json:"param"`
	Values map[string][]any `json:"values"`
	// Note (1.40, cross-node only) is the line under the named param's choice while this refuses one of its options
	Note string `json:"note,omitempty"`
}

// Pool is what values_by narrows: an int's values, else an enum's.
func (p Param) Pool() []any {
	if p.Type == "enum" {
		out := make([]any, len(p.Enum))
		for i, v := range p.Enum {
			out[i] = v
		}
		return out
	}
	out := make([]any, len(p.Values))
	for i, v := range p.Values {
		out[i] = float64(v)
	}
	return out
}

// IsImageField reports whether a rows item field holds an Image.
func IsImageField(raw json.RawMessage) bool { return string(raw) == `"Image"` }

// IsMediaField reports whether a rows item field holds an image or a video take (API 1.14, an ask node's media).
func IsMediaField(raw json.RawMessage) bool { return string(raw) == `"Media"` }

// IsTakeField reports whether a rows item field holds a take: an Image, a Media, or (API 1.22) a media item param.
func IsTakeField(raw json.RawMessage) bool {
	var item Param
	return IsImageField(raw) || IsMediaField(raw) || (json.Unmarshal(raw, &item) == nil && item.Type == "media")
}

// MediaRowsField is a rows param's one required media item field (API 1.22: video or audio references; API 1.37
// optional ones aside) and its item param, ok false for any other param.
func MediaRowsField(p Param) (string, Param, bool) {
	if p.Type != "rows" {
		return "", Param{}, false
	}
	var name string
	var field Param
	n := 0
	for k, raw := range p.Item {
		var item Param
		if json.Unmarshal(raw, &item) == nil && item.Type == "media" && !item.Optional {
			name, field, n = k, item, n+1
		}
	}
	return name, field, n == 1
}

// IsVoiceField reports whether a rows item field holds a saved voice (API 1.32: a dialogue's lines).
func IsVoiceField(raw json.RawMessage) bool {
	var item Param
	return json.Unmarshal(raw, &item) == nil && item.Type == "voice"
}

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

// MediaItem is a rows param's one media item field (API 1.22: video or audio references) and its schema; ok false for
// any other param.
func (p Param) MediaItem() (field string, spec Param, ok bool) {
	if p.Type != "rows" {
		return "", Param{}, false
	}
	n := 0
	for _, name := range slices.Sorted(maps.Keys(p.Item)) {
		var item Param
		if json.Unmarshal(p.Item[name], &item) == nil && item.Type == "media" && !item.Optional {
			field, spec, n = name, item, n+1
		}
	}
	return field, spec, n == 1
}

// Excluded reports whether a rows param must stay empty on node: an input its not_with names is wired (API 1.22); the
// second result is that input.
func (p Param) Excluded(node Node) (string, bool) {
	for _, input := range p.NotWith {
		if _, wired := node.In[input]; wired {
			return input, true
		}
	}
	return "", false
}

// Port is a typed input or output of a node.
type Port struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"` // inputs only: may be left unwired
}

// NodeSchema is one node kind a runner can execute.
type NodeSchema struct {
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Family *string `json:"family"`
	// API 1.24: the model component it loads, in words (the Graph inspector's "uses the <component>")
	Component string `json:"component,omitempty"`
	Help      string `json:"help,omitempty"`
	// API 1.36: the optional add-ons the node loads (its family's Model.Addons) and a one-engine node's phases
	Addons []string `json:"addons,omitempty"`
	Phases []Phase  `json:"phases,omitempty"`
	// API 1.47: a native stage's peak_gb formula (studio plans its runs by it) and its references' sound limits
	PeakGB  string  `json:"peak_gb,omitempty"`
	Sounds  *Sounds `json:"sounds,omitempty"`
	Params  []Param `json:"params"`
	Inputs  []Port  `json:"inputs"`
	Outputs []Port  `json:"outputs"`
}

// Sounds are a node's limits on the sound its references carry together (API 1.47, MiniMax H3: 15 s, 3 inputs).
type Sounds struct {
	MaxS      float64 `json:"max_s"`
	MaxInputs int     `json:"max_inputs"`
}

// Node is one node of a workflow graph. Bypass and Position belong to an edited workflow: a command never carries them.
type Node struct {
	Kind     string              `json:"kind"`
	Params   map[string]any      `json:"params"` // never nil: a command must carry every node's params
	In       map[string]string   `json:"in,omitempty"`
	Bypass   bool                `json:"bypass,omitempty"`
	Position *Position           `json:"position,omitempty"`
	Only     map[string][]string `json:"only,omitempty"` // API 1.19: enum param → the values this workflow allows
	// API 1.22: a core image node that may stay empty: studio drops it and its wires before a run (never sent)
	Optional bool `json:"optional,omitempty"`
}

// Phase is one phase of a node that runs one engine end to end (API 1.36): its progress names it by ID.
type Phase struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	Media       string                `json:"media,omitempty"`  // image | video as published; "" = image
	Action      string                `json:"action,omitempty"` // API 1.19: the take action's label ("Use in…"); "" = the title
	Nodes       map[string]Node       `json:"nodes"`
	Studio      json.RawMessage       `json:"studio,omitempty"` // a StudioLayout as published; absent = the derived form
	NodeSchemas map[string]NodeSchema `json:"node_schemas"`
	Order       int                   `json:"-"` // its place in its runner's workflows value (the pack's file order)
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
	// API 1.16: the models root's volume, the machine, whether a Hub token file exists, the residency settings in effect
	ModelsDisk *ModelsDisk `json:"models_disk,omitempty"`
	Machine    *Machine    `json:"machine,omitempty"`
	HubToken   bool        `json:"hub_token,omitempty"`
	Policy     *Policy     `json:"policy,omitempty"`
	// API 1.17: this env runs media jobs (exports and frames) with these H.264 encoders, preferred first
	Media *Media `json:"media,omitempty"`
	// API 1.18: the device it runs on, and on cuda the host-RAM limit (RAM − RUNNER_RAM_RESERVE_GB, RSS used)
	Device  *Device `json:"device,omitempty"`
	HostRAM *Memory `json:"host_ram,omitempty"`
}

// Device is the one device a runner uses (API 1.18): cuda, mps or cpu.
type Device struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	MemoryTotalGB float64  `json:"memory_total_gb"`
	MemoryUsedGB  *float64 `json:"memory_used_gb,omitempty"`
	Capability    string   `json:"capability,omitempty"`
	Driver        string   `json:"driver,omitempty"`
	Runtime       string   `json:"runtime,omitempty"`
}

// OnDevice reports whether a resident entry or a planned component is in device memory: device cpu (CPU offload on a
// cuda host) is in host RAM and never counted in the memory budget (API 1.18).
func OnDevice(device string) bool { return device != "cpu" }

// Media is what a runner's media worker has: ffmpeg's version and its H.264 encoders (API 1.17); Thumbs: it runs
// thumbs jobs (API 1.48; an older runner does not).
type Media struct {
	FFmpeg   string   `json:"ffmpeg"`
	Encoders []string `json:"encoders"`
	Thumbs   bool     `json:"thumbs,omitempty"`
}

// ModelsDisk is the volume holding a runner's models root (API 1.16).
type ModelsDisk struct {
	Root       string `json:"root"`
	FreeBytes  int64  `json:"free_bytes"`
	TotalBytes int64  `json:"total_bytes"`
	Writable   *bool  `json:"writable,omitempty"` // API 1.18: false when bind-mounted read-only; absent = true
}

// Machine is what a runner runs on (API 1.16).
type Machine struct {
	OS        string  `json:"os"`
	Accel     string  `json:"accel"`
	Packaging string  `json:"packaging"`
	RAMGB     float64 `json:"ram_gb"`
}

// Policy is the residency settings a runner applies now (API 1.16): host_settings, else its env defaults.
type Policy struct {
	AfterRun        string  `json:"after_run"`
	IdleUnloadMin   int     `json:"idle_unload_min"`
	ReserveGB       float64 `json:"reserve_gb"`
	SettingsVersion *int    `json:"settings_version"`
}

// Memory is a runner's model memory: what it may use and uses now.
type Memory struct {
	BudgetGB float64 `json:"budget_gb"`
	UsedGB   float64 `json:"used_gb"`
}

// Resident is one model component a runner holds loaded; it names no run or owner.
type Resident struct {
	Component string `json:"component"`
	Model     string `json:"model"`
	Variant   string `json:"variant"`
	Precision string `json:"precision"`
	// API 1.39: the load option compute of the entry (absent = float32)
	Compute string  `json:"compute,omitempty"`
	GB      float64 `json:"gb"`
	Pinned  bool    `json:"pinned"`
	// when the runner last used it (absent from older runners)
	LastUsed *time.Time `json:"last_used,omitempty"`
	// a component with LoRAs baked in: those LoRAs in order (absent = the plain component)
	Loras []ResidentLora `json:"loras,omitempty"`
	// API 1.16, with Pinned: in_use (the running run uses it) or pin (a pin of the host)
	PinnedWhy string `json:"pinned_why,omitempty"`
	// API 1.18, cuda hosts: cpu = held in host RAM (not counted in memory); absent = on the device
	Device string `json:"device,omitempty"`
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
	Media    []string   `json:"media,omitempty"`    // API 1.16
	Runtimes []Runtime  `json:"runtimes,omitempty"` // API 1.16: every runtime of the pack
	Addons   []Addon    `json:"addons,omitempty"`   // API 1.36: its optional add-ons on the host
	// API 1.39: the components the model node's compute applies to (absent: it applies to none here)
	Compute *ModelCompute `json:"compute,omitempty"`
}

// ModelCompute names the components a model node's compute (a load option) keys apart.
type ModelCompute struct {
	Components []string `json:"components"`
}

// Runtime is one runtime of a model's pack and its env.
type Runtime struct {
	Runtime string   `json:"runtime"`
	Env     string   `json:"env"`
	Devices []string `json:"devices,omitempty"` // API 1.18; absent = every device of the runtime
}

// Variant is one variant of a model on a host.
type Variant struct {
	ID       string       `json:"id"`
	Presence string       `json:"presence"`         // present | partial | missing | mismatch
	Memory   []MemoryPlan `json:"memory,omitempty"` // per precision: what a run holds (the hand-over)
	// API 1.16 (the Models screen and model jobs)
	Title      string      `json:"title,omitempty"`
	Bytes      *int64      `json:"bytes,omitempty"`
	Precisions []string    `json:"precisions,omitempty"`
	Dir        string      `json:"dir,omitempty"`
	Source     *Source     `json:"source,omitempty"`
	Components []Component `json:"components,omitempty"`
	Mismatch   []string    `json:"mismatch,omitempty"`
	Devices    []string    `json:"devices,omitempty"` // API 1.34: the only devices it runs on
	// M9: the variant whose weights are this one's distilled model (ltx dev-int8: distilled-fp8), used by <family>/distill
	DistillFrom string `json:"distill_from,omitempty"`
	Help        string `json:"help,omitempty"` // API 1.39: its line in the model chip's menu
}

// Addon is one optional add-on of a model on a host (API 1.36): a component a few nodes load and no variant needs.
type Addon struct {
	Component string   `json:"component"`
	Title     string   `json:"title"`
	Help      string   `json:"help,omitempty"`
	Dir       string   `json:"dir"`
	Presence  string   `json:"presence"` // present | partial | missing | mismatch
	Bytes     *int64   `json:"bytes,omitempty"`
	Source    *Source  `json:"source,omitempty"`
	Mismatch  []string `json:"mismatch,omitempty"`
	// API 1.40: the variants whose runs need it; absent = every variant
	Variants []string `json:"variants,omitempty"`
	// API 1.40: the task whose runs need it (true: a run with references, false: one without); absent = both
	Refs *bool `json:"refs,omitempty"`
	// API 1.40: the name in `<short> not downloaded` (absent = Title) and the Models card's mono line
	Short string `json:"short,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// Needs reports whether a run on variant, with references or not, needs the add-on (API 1.40: its variants, absent =
// every one; its refs, absent = both).
func (a Addon) Needs(variant string, refs bool) bool {
	return (len(a.Variants) == 0 || slices.Contains(a.Variants, variant)) && (a.Refs == nil || *a.Refs == refs)
}

// Name is what the run check and the UI call it: its short name, else its title.
func (a Addon) Name() string {
	if a.Short != "" {
		return a.Short
	}
	return a.Title
}

// Addon returns the model's add-on named component, if the host lists it.
func (m Model) Addon(component string) (Addon, bool) {
	i := slices.IndexFunc(m.Addons, func(a Addon) bool { return a.Component == component })
	if i < 0 {
		return Addon{}, false
	}
	return m.Addons[i], true
}

// Source is where a download fetches a variant's own files; nil: not downloadable.
type Source struct {
	Repo       string `json:"repo"`
	Revision   string `json:"revision"`
	Licence    string `json:"licence"`
	MustAccept bool   `json:"must_accept"`
	Gated      bool   `json:"gated"`
	Bytes      *int64 `json:"bytes,omitempty"`
}

// Component is one weight component a variant loads: from its own folder or its owner variant's.
type Component struct {
	Component   string   `json:"component"`
	FromVariant string   `json:"from_variant"`
	Precisions  []string `json:"precisions"`
	Bytes       *int64   `json:"bytes,omitempty"`
	Separate    bool     `json:"separate,omitempty"`
	Stored      string   `json:"stored,omitempty"` // API 1.34: the dtype most of its bytes have on disk
}

// Licence is one licence text a models key publishes (API 1.16).
type Licence struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	URL   *string `json:"url"`
	Text  string  `json:"text"`
}

// Published is what one runner's registry keys say about weights: its models, their licences and its shared LoRAs.
type Published struct {
	Models   []Model
	Licences []Licence
	Loras    []SharedLora
}

// MemoryPlan is what a run of a variant loaded at Precision holds: its planned peak and the components it loads, in
// the order of their last use.
type MemoryPlan struct {
	Precision  string          `json:"precision"`
	PeakGB     float64         `json:"peak_gb"`
	Components []PlanComponent `json:"components"`
	PlannedAt  *PlannedAt      `json:"planned_at,omitempty"`  // the largest sizes PeakGB holds for; nil: every size
	Workflow   string          `json:"workflow,omitempty"`    // API 1.14: plans only runs of this workflow ("" = any)
	RAMPeakGB  *float64        `json:"ram_peak_gb,omitempty"` // API 1.18, cuda: the most host RAM the run holds
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
	Device       string  `json:"device,omitempty"` // API 1.18: cpu = kept in host RAM
}

// Has reports whether the variant's files are on the host: always for a model without variants.
func (m Model) Has(variant string) bool {
	return len(m.Variants) == 0 || slices.ContainsFunc(m.Variants, func(v Variant) bool { return v.ID == variant && v.Presence == "present" })
}

// HasPrecision reports whether the variant lists precision (API 1.18 eligibility): always for a model without
// variants, or a model node without a precision param.
func (m Model) HasPrecision(variant, precision string) bool {
	return len(m.Variants) == 0 || precision == "" || slices.ContainsFunc(m.Variants, func(v Variant) bool {
		return v.ID == variant && slices.Contains(v.Precisions, precision)
	})
}

// RunsAt is the precision variant loads at when the model node asks for precision: a stored-quantized variant (API
// 1.33: one precision, the community base-fp8's [fp8]) loads at its own whatever the node asks, as the runner does.
func (m Model) RunsAt(variant, precision string) string {
	if i := slices.IndexFunc(m.Variants, func(v Variant) bool { return v.ID == variant }); i >= 0 && len(m.Variants[i].Precisions) == 1 {
		return m.Variants[i].Precisions[0]
	}
	return precision
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
	seenMu    sync.Mutex                      // guards seenAt (Guides reads under mu.RLock)
	seenAt    map[string]time.Time            // "<host>.<env>" → when it was last present, for guideGrace
	nodes     map[string][]NodeSchema         // "<host>.<env>" → its node schemas
	nodesAt   map[string]time.Time            // "<host>.<env>" → when its nodes were published
	workflows map[string][]Workflow           // "<host>.<env>" → its workflows (without node schemas)
	models    map[string][]Model              // "<host>.<env>" → the models present there
	licences  map[string][]Licence            // "<host>.<env>" → the licences its models key publishes (API 1.16)
	loras     map[string][]SharedLora         // "<host>.<env>" → the shared LoRAs on that host
	looks     map[string][]StarterLook        // "<host>.<env>" → the starter looks of its packs
	looksAt   map[string]time.Time            // "<host>.<env>" → when its looks were published
	voices    map[string][]StarterVoice       // "<host>.<env>" → the starter voices of its packs (API 1.30)
	voicesAt  map[string]time.Time            // "<host>.<env>" → when its voices were published
	guides    map[string][]Guide              // "<host>.<env>" → its packs' assistant guides (API 1.71)
	guidesAt  map[string]time.Time            // "<host>.<env>" → when its guides were published
	hosts     map[string]Host                 // "<host>.<env>" → its state
	awaiting  map[string]bool                 // watched buckets whose current values have not all arrived yet
	custom    []CustomNode                    // pipelines.engine.custom: the custom nodes pipelines' packs register (API 1.15)
	plugins   map[string]map[string]PluginRun // "<host>.<env>" → the plugins its runner runs, by name (API 1.67)
}

// PluginRun is one plugin a host's runner runs (registry.schema.json plugins, API 1.67).
type PluginRun struct {
	State    string    `json:"state"` // starting | running | failed | stopped
	Detail   *string   `json:"detail,omitempty"`
	Restarts int       `json:"restarts"`
	Since    time.Time `json:"since"`
	Log      string    `json:"log"`
}

// PluginRuns is what the runners of host say of their plugins, by plugin name (the newest since wins a name two
// runners report); live: a runner of host is up (a fresh hosts key).
func (r *Registry) PluginRuns(host string, now time.Time) (out map[string]PluginRun, live bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out = map[string]PluginRun{}
	for source, h := range r.hosts {
		live = live || strings.HasPrefix(source, host+".") && h.Live(now)
	}
	for source, runs := range r.plugins {
		if !strings.HasPrefix(source, host+".") {
			continue
		}
		for name, run := range runs {
			if old, ok := out[name]; !ok || run.Since.After(old.Since) {
				out[name] = run
			}
		}
	}
	return out, live
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
		set(&r.nodesAt, source, received(parsed.PublishedAt))
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
			if owner := r.ownerOf(workflow.ID, env, source); owner != "" {
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
			delete(r.licences, source)
			return nil
		}
		var parsed struct {
			Models   []Model   `json:"models"`
			Licences []Licence `json:"licences"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.models, source, parsed.Models)
		set(&r.licences, source, parsed.Licences)
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
		set(&r.looksAt, source, received(parsed.PublishedAt))
	case "voices":
		if value == nil {
			delete(r.voices, source)
			delete(r.voicesAt, source)
			return nil
		}
		var parsed struct {
			Voices      []StarterVoice `json:"voices"`
			PublishedAt time.Time      `json:"published_at"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		kept := []StarterVoice{}
		for _, voice := range parsed.Voices {
			if why := badVoice(&voice); why != "" {
				log.Printf("registry %s: voice %s dropped: %s", key, voice.ID, why)
				continue
			}
			kept = append(kept, voice)
		}
		set(&r.voices, source, kept)
		set(&r.voicesAt, source, received(parsed.PublishedAt))
	case "guides": // API 1.71
		if value == nil {
			delete(r.guides, source)
			delete(r.guidesAt, source)
			return nil
		}
		var parsed struct {
			Guides      []Guide   `json:"guides"`
			PublishedAt time.Time `json:"published_at"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		// the schema's limits, enforced here too: a bad or old runner never puts a huge card into every prompt
		kept := []Guide{}
		for _, g := range parsed.Guides {
			if len(kept) == 32 {
				break
			}
			if g.Family == "" || utf8.RuneCountInString(g.Card) > 800 || utf8.RuneCountInString(g.Title) > 80 ||
				utf8.RuneCountInString(g.Guide) > 24576 {
				continue
			}
			kept = append(kept, g)
		}
		set(&r.guides, source, kept)
		set(&r.guidesAt, source, received(parsed.PublishedAt))
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
	case "plugins": // API 1.67
		if value == nil {
			delete(r.plugins, source)
			return nil
		}
		var parsed struct {
			Plugins map[string]PluginRun `json:"plugins"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.plugins, source, parsed.Plugins)
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
	slices.SortFunc(sources, byRank)   // own hosts first, a fake env last
	kept := map[string][]StarterLook{} // the looks of a family the source serves
	for _, source := range sources {
		kept[source] = slices.DeleteFunc(slices.Clone(r.looks[source]), func(l StarterLook) bool {
			return !slices.ContainsFunc(r.models[source], func(m Model) bool { return m.Family == l.Family })
		})
	}
	out := map[string]LiveLook{}
	level := map[string]int{} // id → the best rank publishing it: a worse-ranked entry never replaces it
	for _, source := range sources {
		for _, entry := range kept[source] {
			live, seen := out[entry.ID]
			if !seen {
				level[entry.ID] = rank(source)
			}
			if !seen || entry.Version > live.Version && rank(source) == level[entry.ID] {
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

// PrefusedIn is the title of a published model whose lora block lists the shared LoRA id as pre-fused (the first by
// source), or "": its file is part of that model's weights.
func (r *Registry) PrefusedIn(id string) string {
	published := r.Published()
	for _, source := range slices.Sorted(maps.Keys(published)) {
		for _, m := range published[source].Models {
			if m.Lora != nil && slices.ContainsFunc(m.Lora.Prefused, func(p Prefused) bool { return p.LoraID == id }) {
				return m.Title
			}
		}
	}
	return ""
}

// Published returns what every runner publishes about weights, by "<host>.<env>" (live or not, its host present).
func (r *Registry) Published() map[string]Published {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]Published{}
	for source, models := range r.models {
		if r.present(source) {
			out[source] = Published{Models: models, Licences: r.licences[source], Loras: r.loras[source]}
		}
	}
	for source, loras := range r.loras {
		if _, seen := out[source]; !seen && r.present(source) {
			out[source] = Published{Loras: loras}
		}
	}
	return out
}

// HasLora reports whether the runner at source ("<host>.<env>") lists the shared LoRA id.
func (r *Registry) HasLora(source, id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.present(source) && slices.ContainsFunc(r.loras[source], func(l SharedLora) bool { return l.ID == id })
}

// LoraBlocks returns every family's lora block any runner publishes (live or not), by family: the first source by key;
// Fake when every source publishing it is env fake.
func (r *Registry) LoraBlocks() map[string]LoraBlock {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]LoraBlock{}
	for _, source := range slices.Sorted(maps.Keys(r.models)) {
		if !r.present(source) {
			continue
		}
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

// ownerOf returns the source of another env, ranked at or above from, that publishes workflow id, or "": a lower-ranked
// source (a remote host) never takes an id from a higher-ranked one. Callers hold r.mu.
func (r *Registry) ownerOf(id, env, from string) string {
	for _, source := range slices.SortedFunc(maps.Keys(r.workflows), byRank) {
		for _, workflow := range r.workflows[source] {
			if workflow.ID == id && workflow.Env != env && rank(source) <= rank(from) {
				return source
			}
		}
	}
	return ""
}

// localHosts are the stack's own hosts: their NATS users are written in deploy/nats/nats.conf, outside the gpu-hosts
// block .tools/make-secrets generates (which refuses these names). contracts/README.md, Several hosts: precedence.
var localHosts = []string{"mac", "fake"}

// rank orders sources ("<host>.<env>") by trust, lowest first: the stack's own hosts before remote ones, a fake env
// after every other. A source never replaces a definition (workflow template, look, node schema) of a lower rank.
func rank(source string) int {
	host, env, _ := strings.Cut(source, ".")
	return 2*btoi(env == "fake") + btoi(!slices.Contains(localHosts, host))
}

// byRank sorts sources by rank, then key.
func byRank(a, b string) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a, b)) }

// received is a value's published_at, no later than now: a runner's clock never ranks its publish above another's.
func received(at time.Time) time.Time {
	if now := time.Now(); at.After(now) {
		return now
	}
	return at
}

// SetHost applies one KV `hosts` value (nil: the key was deleted, the host is offline). The value's host and env
// must be the key's <host>.<env>.
func (r *Registry) SetHost(key string, value []byte) error {
	return r.SetHostAt(key, value, time.Time{})
}

// SetHostAt is SetHost with the KV entry's server timestamp: when not zero it is the value's updated_at, so a key's
// age is measured on the stack's clock, never the runner's (contracts/README.md, Several hosts: freshness).
func (r *Registry) SetHostAt(key string, value []byte, at time.Time) error {
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
	if !at.IsZero() {
		host.UpdatedAt = at
	}
	set(&r.hosts, key, host)
	return nil
}

// Workflows returns every workflow whose kinds all have a schema from the same runner, by id; the same id from several
// runners is listed once (the first runner by rank, then key, wins).
func (r *Registry) Workflows() []Workflow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Workflow{}
	seen := map[string]bool{}
	for _, source := range slices.SortedFunc(maps.Keys(r.workflows), byRank) {
		if !r.present(source) {
			continue
		}
		schemas := map[string]NodeSchema{}
		for _, schema := range r.nodes[source] {
			schemas[schema.Kind] = schema
		}
		for i, workflow := range r.workflows[source] {
			if seen[workflow.ID] {
				continue
			}
			if complete := withSchemas(workflow, schemas); complete != nil {
				seen[workflow.ID] = true
				complete.Order = i
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
	return r.present(source) && slices.ContainsFunc(r.workflows[source], func(w Workflow) bool { return w.ID == id })
}

// PublishesKinds reports whether the runner at source ("<host>.<env>") publishes every one of kinds.
func (r *Registry) PublishesKinds(source string, kinds []string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.present(source) {
		return false
	}
	for _, kind := range kinds {
		if !slices.ContainsFunc(r.nodes[source], func(s NodeSchema) bool { return s.Kind == kind }) {
			return false
		}
	}
	return true
}

// Addons returns the optional add-ons (API 1.36) the runner at source says its kind loads.
func (r *Registry) Addons(source, kind string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if i := slices.IndexFunc(r.nodes[source], func(s NodeSchema) bool { return s.Kind == kind }); i >= 0 {
		return r.nodes[source][i].Addons
	}
	return nil
}

// NodeAddons returns what node needs on the runner at source: its kind's add-ons, then (API 1.41) those its enum
// params' values ask for on that runner's schema, in schema order (a param the node leaves out: its default; wired:
// an input in In, or a rows param holding a row).
func (r *Registry) NodeAddons(source string, node Node) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i := slices.IndexFunc(r.nodes[source], func(s NodeSchema) bool { return s.Kind == node.Kind })
	if i < 0 {
		return nil
	}
	s := r.nodes[source][i]
	out := slices.Clone(s.Addons)
	wired := func(name string) bool {
		if _, ok := node.In[name]; ok {
			return true
		}
		rows, ok := node.Params[name].([]any)
		return ok && len(rows) > 0
	}
	for _, p := range s.Params {
		if len(p.Addons) == 0 {
			continue
		}
		v, ok := node.Params[p.Name].(string)
		if !ok {
			v, _ = p.Default.(string)
		}
		for _, c := range p.ValueAddons(v, wired) {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// Schemas returns every kind any runner publishes, live or not (its host present): the schema of the newest publish.
func (r *Registry) Schemas() map[string]NodeSchema {
	if r == nil { // no registry (a projector without one)
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]NodeSchema{}
	for kind, found := range r.newest(r.present) {
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
	rank   int
	envs   []string
}

// newest collects every kind the sources keep publishes: the schema of the newest publish among the best-ranked sources
// publishing it (ties: the first source by key) and every env, sorted. Callers hold r.mu.
func (r *Registry) newest(keep func(source string) bool) map[string]found {
	out := map[string]found{}
	for _, source := range slices.SortedFunc(maps.Keys(r.nodes), byRank) {
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
			if !seen || rank(source) == old.rank && at.After(old.at) {
				old = found{schema: schema, index: i, at: at, rank: rank(source)}
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
	if !r.present(source) {
		return Model{}, false
	}
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

// present reports whether the host of source ("<host>.<env>") has a present hosts key: from API 1.18 a host's registry
// keys (they have no TTL) are read only while it has one, so they lapse with it (contracts/README.md, Several hosts:
// host loss). Callers hold r.mu.
func (r *Registry) present(source string) bool {
	host, _, _ := strings.Cut(source, ".")
	for key := range r.hosts {
		if h, _, _ := strings.Cut(key, "."); h == host {
			return true
		}
	}
	return false
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
