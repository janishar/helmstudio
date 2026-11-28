package registry

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"
)

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
	// frames only: present = the value "auto" is allowed (the model predicts the length within min_s..max_s); from
	// API 1.79 an int's or float's `auto: true` (Auto.Zero): the value 0 means Auto, allowed below min
	Auto *Auto `json:"auto,omitempty"`
	// text only (API 1.79): the lyrics tag chips over the field, served as is
	Tags []string `json:"tags,omitempty"`
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

// Auto is a frames param's auto length bounds in seconds, or (API 1.79, JSON `true`) an int's or float's Auto: 0.
type Auto struct {
	MinS float64 `json:"min_s"`
	MaxS float64 `json:"max_s"`
	Zero bool    `json:"-"`
}

// UnmarshalJSON reads `true` (an int's or float's Auto) or the frames bounds.
func (a *Auto) UnmarshalJSON(b []byte) error {
	if string(b) == "true" {
		*a = Auto{Zero: true}
		return nil
	}
	type bounds Auto
	*a = Auto{}
	return json.Unmarshal(b, (*bounds)(a))
}

// MarshalJSON writes it back as it came.
func (a Auto) MarshalJSON() ([]byte, error) {
	if a.Zero {
		return []byte("true"), nil
	}
	type bounds Auto
	return json.Marshal(bounds(a))
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
	PeakGB string  `json:"peak_gb,omitempty"`
	Sounds *Sounds `json:"sounds,omitempty"`
	// API 1.79 (contracts/README.md, Music and sound): a node with an Audio output: its sound class (Workflow.sound)
	Sound   string  `json:"sound,omitempty"`
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
	// API 1.81: what Magic's standard request fills in it (registry.schema.json standard); nil = it serves no kind
	Standard *Standard `json:"standard,omitempty"`
}

// Standard is a workflow's standard block (API 1.81, contracts/README.md "Magic: the standard request").
type Standard struct {
	Kind    string            `json:"kind"`   // image | video | voice | music | sound
	Model   string            `json:"model"`  // the model's name in Magic's picker
	Fields  map[string]string `json:"fields"` // shared field → "<node>.<param>"
	Set     map[string]any    `json:"set,omitempty"`
	Seconds *float64          `json:"seconds,omitempty"` // the declared GPU seconds per unit
	Default bool              `json:"default,omitempty"` // API 1.85: the kind's default model (first in the picker)
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
	// API 1.78: every model folder in priority order (absent from older runners)
	ModelsDisks []ModelFolder `json:"models_disks,omitempty"`
	Machine     *Machine      `json:"machine,omitempty"`
	HubToken    bool          `json:"hub_token,omitempty"`
	Policy      *Policy       `json:"policy,omitempty"`
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

// ModelFolder is one model folder of a host (API 1.78, contracts/README.md "Model folders").
type ModelFolder struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Root       string `json:"root"`
	Connected  bool   `json:"connected"`
	FreeBytes  *int64 `json:"free_bytes,omitempty"`
	TotalBytes *int64 `json:"total_bytes,omitempty"`
	Writable   bool   `json:"writable"`
	Downloads  bool   `json:"downloads"`
}

// Copy is a variant's or add-on's files in one model folder (API 1.78).
type Copy struct {
	Folder     string `json:"folder"`
	State      string `json:"state"` // present | partial | mismatch | unplugged
	Bytes      *int64 `json:"bytes,omitempty"`
	FreedBytes *int64 `json:"freed_bytes,omitempty"`
}

// Machine is what a runner runs on (API 1.16).
type Machine struct {
	OS        string  `json:"os"`
	Accel     string  `json:"accel"`
	Packaging string  `json:"packaging"`
	RAMGB     float64 `json:"ram_gb"`
	// API 1.75: the macOS GPU limit; null when unset, on other hosts and from older runners
	GPULimitGB *float64 `json:"gpu_limit_gb"`
}

// Policy is the residency settings a runner applies now (API 1.16): host_settings, else its env defaults.
type Policy struct {
	AfterRun        string  `json:"after_run"`
	IdleUnloadMin   int     `json:"idle_unload_min"`
	ReserveGB       float64 `json:"reserve_gb"`
	ReserveMode     string  `json:"reserve_mode,omitempty"` // API 1.75; absent (older runners) = custom
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
	Sound    string     `json:"sound,omitempty"`    // API 1.79: an audio model's class (music, sound, voice)
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
	Presence string       `json:"presence"`         // present | partial | missing | mismatch | unplugged (1.78)
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
	// API 1.78: the folder a run loads it from, and every folder with files of it
	Folder string `json:"folder,omitempty"`
	Copies []Copy `json:"copies,omitempty"`
}

// Addon is one optional add-on of a model on a host (API 1.36): a component a few nodes load and no variant needs.
type Addon struct {
	Component string   `json:"component"`
	Title     string   `json:"title"`
	Help      string   `json:"help,omitempty"`
	Dir       string   `json:"dir"`
	Presence  string   `json:"presence"` // present | partial | missing | mismatch | unplugged (1.78)
	Bytes     *int64   `json:"bytes,omitempty"`
	Source    *Source  `json:"source,omitempty"`
	Mismatch  []string `json:"mismatch,omitempty"`
	Folder    string   `json:"folder,omitempty"` // API 1.78, as a variant's
	Copies    []Copy   `json:"copies,omitempty"`
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
