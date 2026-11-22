package dto

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/helmstudio/studio_service/registry"
)

// The Models screen, model jobs and the hosts admin (API 1.16, contracts/README.md "Models and model jobs", "Hosts
// admin").

var (
	// HostName, EnvName, VariantID, ModelID, ComponentName are the contracts' name patterns.
	HostName      = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	VariantID     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	AddonID       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	ModelID       = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	ComponentName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	pinLoraID     = regexp.MustCompile(`^([0-9a-f]{64}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	precisions    = map[string]bool{"bf16": true, "fp16": true, "int8": true, "int4": true, "fp8": true, "fp32": true}
	afterRuns     = map[string]bool{"unload": true, "keep_warm": true, "keep_pinned": true}
)

// ModelList is GET /models.
type ModelList struct {
	Hosts    []ModelHost        `json:"hosts"`
	Licences []registry.Licence `json:"licences"`
	Models   []Model            `json:"models"`
}

// ModelHost is one host of the Models screen.
type ModelHost struct {
	Host       string   `json:"host"`
	Live       bool     `json:"live"`
	Envs       []string `json:"envs"`
	ModelsRoot *string  `json:"models_root"`
	FreeBytes  *int64   `json:"free_bytes"`
	TotalBytes *int64   `json:"total_bytes"`
	HubToken   *bool    `json:"hub_token,omitempty"` // admins only
}

// Model is one model and its variants over every host.
type Model struct {
	ID       string             `json:"id"`
	Family   string             `json:"family"`
	Title    string             `json:"title"`
	Media    []string           `json:"media"`
	Runtimes []registry.Runtime `json:"runtimes"`
	Variants []ModelVariant     `json:"variants"`
	Addons   []ModelAddon       `json:"addons"` // API 1.36
}

// ModelAddon is one optional add-on of a model with its per-host state (API 1.36).
type ModelAddon struct {
	Component     string             `json:"component"`
	Title         string             `json:"title"`
	Help          string             `json:"help,omitempty"`
	Dir           *string            `json:"dir"`
	Licence       *VariantLicence    `json:"licence"`
	Gated         bool               `json:"gated"`
	DownloadBytes *int64             `json:"download_bytes"`
	Hosts         []ModelVariantHost `json:"hosts"`
}

// ModelVariant is one variant with its components and per-host state.
type ModelVariant struct {
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Precisions    []string           `json:"precisions"`
	Devices       []string           `json:"devices,omitempty"`
	Dir           *string            `json:"dir"`
	Licence       *VariantLicence    `json:"licence"`
	Gated         bool               `json:"gated"`
	DownloadBytes *int64             `json:"download_bytes"`
	Components    []VariantComponent `json:"components"`
	Hosts         []ModelVariantHost `json:"hosts"`
}

// VariantLicence is a variant source's licence.
type VariantLicence struct {
	ID         string `json:"id"`
	MustAccept bool   `json:"must_accept"`
}

// VariantComponent is one component a variant loads.
type VariantComponent struct {
	Component   string   `json:"component"`
	FromVariant string   `json:"from_variant"`
	Precisions  []string `json:"precisions"`
	Stored      *string  `json:"stored,omitempty"`
	Bytes       *int64   `json:"bytes"`
}

// ModelVariantHost is a variant's state on one host.
type ModelVariantHost struct {
	Host     string      `json:"host"`
	State    string      `json:"state"`
	Bytes    *int64      `json:"bytes"`
	Mismatch []string    `json:"mismatch,omitempty"`
	Separate []string    `json:"separate,omitempty"`
	Job      *VariantJob `json:"job"`
}

// VariantJob is the download job of a downloading variant.
type VariantJob struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	DoneBytes  int64  `json:"done_bytes"`
	TotalBytes *int64 `json:"total_bytes"`
}

// ModelDownload is POST /admin/models/{model}/download.
type ModelDownload struct {
	Host          string  `json:"host"`
	Variant       *string `json:"variant"`
	Addon         *string `json:"addon"` // API 1.36: an optional add-on instead of a variant
	AcceptLicence bool    `json:"accept_licence"`
}

// Validate checks the names.
func (b *ModelDownload) Validate() string {
	switch {
	case !HostName.MatchString(b.Host):
		return "host must be a host name"
	case b.Variant != nil && !VariantID.MatchString(*b.Variant):
		return "variant must be a variant id"
	case b.Addon != nil && !AddonID.MatchString(*b.Addon):
		return "addon must be a component name"
	case b.Addon != nil && b.Variant != nil:
		return "give a variant or an add-on, not both"
	}
	return ""
}

// ModelJob is one model job.
type ModelJob struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Host        string          `json:"host"`
	Env         string          `json:"env"`
	Model       *string         `json:"model"`
	Variant     *string         `json:"variant"`
	Addon       *string         `json:"addon"` // API 1.36
	LoraID      *string         `json:"lora_id"`
	Title       string          `json:"title"`
	State       string          `json:"state"`
	Progress    json.RawMessage `json:"progress"`
	Failure     json.RawMessage `json:"failure"`
	Result      json.RawMessage `json:"result"`
	Keeps       []Keep          `json:"keeps"`
	Licence     *JobLicence     `json:"licence"`
	RequestedBy string          `json:"requested_by"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at"`
	EndedAt     *time.Time      `json:"ended_at"`
}

// Keep is a shared component a delete leaves on disk, and the variants that still load it.
type Keep struct {
	Component   string   `json:"component"`
	FromVariant string   `json:"from_variant"`
	For         []string `json:"for"`
}

// JobLicence is the licence a download's admin accepted and the SHA-256 of its text.
type JobLicence struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// ModelJobList is GET /admin/model-jobs.
type ModelJobList struct {
	Jobs []ModelJob `json:"jobs"`
}

// ------------------------------------------------------------------ hosts admin

// PinRef names one resident entry with its env (registry $defs/pin).
type PinRef struct {
	Env       string    `json:"env"`
	Component string    `json:"component"`
	Model     string    `json:"model"`
	Variant   string    `json:"variant"`
	Precision string    `json:"precision"`
	Loras     []PinLora `json:"loras,omitempty"`
}

// PinLora is one LoRA of a baked set.
type PinLora struct {
	LoraID   string  `json:"lora_id"`
	Strength float64 `json:"strength"`
}

// Key identifies the entry (env, component, model, variant, precision, baked set in order).
func (p PinRef) Key() string {
	key := p.Env + " " + p.Component + " " + p.Model + " " + p.Variant + " " + p.Precision
	for _, l := range p.Loras {
		key += fmt.Sprintf(" %s@%v", l.LoraID, l.Strength)
	}
	return key
}

// Validate checks the names (the 422 detail, "" when valid).
func (p PinRef) Validate() string {
	switch {
	case !HostName.MatchString(p.Env):
		return "env must be an env name"
	case !ComponentName.MatchString(p.Component):
		return "component must be a component name"
	case !ModelID.MatchString(p.Model):
		return "model must be a model id"
	case !VariantID.MatchString(p.Variant):
		return "variant must be a variant id"
	case !precisions[p.Precision]:
		return "precision must be bf16, fp16, int8, int4, fp8 or fp32"
	case len(p.Loras) > 32:
		return "loras: at most 32"
	}
	for _, l := range p.Loras {
		if !pinLoraID.MatchString(l.LoraID) {
			return "loras: lora_id must be a LoRA id"
		}
	}
	return ""
}

// HostSettingsPut is PUT /admin/hosts/{host}/settings.
type HostSettingsPut struct {
	AfterRun      string   `json:"after_run"`
	IdleUnloadMin *int     `json:"idle_unload_min"`
	ReserveGB     *float64 `json:"reserve_gb"`
}

// Validate checks the ranges.
func (b *HostSettingsPut) Validate() string {
	switch {
	case !afterRuns[b.AfterRun]:
		return "after_run must be unload, keep_warm or keep_pinned"
	case b.IdleUnloadMin == nil || *b.IdleUnloadMin < 0 || *b.IdleUnloadMin > 1440:
		return "idle_unload_min must be 0 to 1440"
	case b.ReserveGB == nil || *b.ReserveGB < 0 || *b.ReserveGB > 1024 || math.Mod(*b.ReserveGB, 0.5) != 0:
		return "reserve_gb must be 0 to 1024 in steps of 0.5"
	}
	return ""
}

// HostPins is PUT /admin/hosts/{host}/pins.
type HostPins struct {
	Pins []PinRef `json:"pins"`
}

// Validate checks every pin.
func (b *HostPins) Validate() string {
	if b.Pins == nil {
		return "pins is required"
	}
	if len(b.Pins) > 64 {
		return "pins: at most 64"
	}
	for i, p := range b.Pins {
		if detail := p.Validate(); detail != "" {
			return fmt.Sprintf("pins[%d]: %s", i, detail)
		}
	}
	return ""
}

// HostSettings is a host's stored settings.
type HostSettings struct {
	Host          string    `json:"host"`
	Version       int       `json:"version"`
	AfterRun      string    `json:"after_run"`
	IdleUnloadMin int       `json:"idle_unload_min"`
	ReserveGB     float64   `json:"reserve_gb"`
	Pins          []PinRef  `json:"pins"`
	Applied       bool      `json:"applied"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// HostUnload is POST /admin/hosts/{host}/unload: exactly one of the three.
type HostUnload struct {
	All       *bool    `json:"all"`
	Entries   []PinRef `json:"entries"`
	CachedEnv *string  `json:"cached_env"`
}

// Validate checks that exactly one is given.
func (b *HostUnload) Validate() string {
	given := 0
	if b.All != nil {
		given++
		if !*b.All {
			return "all must be true"
		}
	}
	if b.Entries != nil {
		given++
		if len(b.Entries) < 1 || len(b.Entries) > 64 {
			return "entries: 1 to 64"
		}
		for i, p := range b.Entries {
			if detail := p.Validate(); detail != "" {
				return fmt.Sprintf("entries[%d]: %s", i, detail)
			}
		}
	}
	if b.CachedEnv != nil {
		given++
		if !HostName.MatchString(*b.CachedEnv) {
			return "cached_env must be an env name"
		}
	}
	if given != 1 {
		return "give exactly one of all, entries, cached_env"
	}
	return ""
}

// HostUnloadAccepted is the unload's 202.
type HostUnloadAccepted struct {
	Host     string          `json:"host"`
	Requests []UnloadRequest `json:"requests"`
}

// UnloadRequest is one env asked (asked) or to be asked after its run (after_run).
type UnloadRequest struct {
	Env   string `json:"env"`
	State string `json:"state"`
}

// HostAdmin is what admins see of an env beyond the redacted view.
type HostAdmin struct {
	Settings      *HostSettings    `json:"settings"`
	Policy        *registry.Policy `json:"policy"`
	Run           *HostRun         `json:"run"`
	IdleSince     *time.Time       `json:"idle_since"`
	UnloadPending bool             `json:"unload_pending"`
	HubToken      bool             `json:"hub_token"`
}

// HostRun is the run that holds the host, with its latest progress.
type HostRun struct {
	RunID   string   `json:"run_id"`
	OwnerID string   `json:"owner_id"`
	Lane    string   `json:"lane"`
	Node    *string  `json:"node"`
	Step    *int     `json:"step"`
	Of      *int     `json:"of"`
	Phase   *string  `json:"phase"`
	EtaS    *float64 `json:"eta_s"`
}
