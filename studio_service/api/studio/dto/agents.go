package dto

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/studio_service/registry"
)

// API 1.15: model providers, model sources, custom nodes, the director's chat (contracts/README.md "Agents, fan-out,
// model sources and custom nodes").

var (
	providerModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	baseURL       = regexp.MustCompile(`^https?://[^\s/?#@]+(/[^\s?#]*)?$`)
	hostName      = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	keySecret     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// ProviderPut creates or replaces a provider (admin only).
type ProviderPut struct {
	Kind         string   `json:"kind"` // server | remote | search (API 1.44)
	API          *string  `json:"api"`  // API 1.44: absent = openai; search needs brave
	Title        *string  `json:"title"`
	BaseURL      string   `json:"base_url"`
	Models       []string `json:"models"`
	DefaultModel *string  `json:"default_model"`
	Enabled      *bool    `json:"enabled"`
	TimeoutS     *int     `json:"timeout_s"`
	Host         *string  `json:"host"`
	MemoryGB     *float64 `json:"memory_gb"`
	KeySecret    *string  `json:"key_secret"`
	MaxInFlight  *int     `json:"max_in_flight"`
	// API 1.51: from a preset (plan 25): what its OpenAI layer refuses, and the preset's id (display only)
	Compat *ProviderCompat `json:"compat"`
	Preset *string         `json:"preset"`
	// API 1.53 (plan 28): a thinking level per model, checked against the preset's rules by the service
	Thinking map[string]string `json:"thinking"`
}

// ThinkingLevels is every thinking level in its order (openapi ThinkingLevel).
var ThinkingLevels = []string{"off", "on", "low", "medium", "high"}

// ThinkingInfo is what one model offers (API 1.53): its levels, always thinks, the provider's default.
type ThinkingInfo struct {
	Levels  []string `json:"levels"`
	Always  bool     `json:"always,omitempty"`
	Default string   `json:"default,omitempty"`
}

// ProviderCompat is defs providerCompat (API 1.51): false temperature sends none; strict_schema sends strict json_schema
// without the json_object retry. Unknown fields are refused by the strict body.
type ProviderCompat struct {
	Temperature  *bool `json:"temperature,omitempty"`
	StrictSchema *bool `json:"strict_schema,omitempty"`
}

// Validate checks the shape (the host rule is checked by the service): a server needs host and memory_gb and may name
// a key (API 1.51); a remote one needs key_secret and https.
func (b *ProviderPut) Validate() string {
	switch {
	case b.Kind != "server" && b.Kind != "remote" && b.Kind != "search":
		return "kind must be server, remote or search"
	case b.Kind == "search" && (b.API == nil || *b.API != "brave"):
		return "a search provider needs api brave"
	case b.Kind != "search" && b.API != nil && *b.API != "openai":
		return "api must be openai for a server or remote provider"
	case b.Kind == "search" && !slices.Equal(b.Models, []string{"web"}):
		return "a search provider's models must be [web]"
	case b.Title != nil && !between(*b.Title, 1, 60):
		return "title must be 1-60 characters"
	case len(b.BaseURL) < 8 || len(b.BaseURL) > 300 || !baseURL.MatchString(b.BaseURL):
		return "base_url must be http(s)://host[:port][/path], without user, query or fragment"
	case len(b.Models) < 1 || len(b.Models) > 16 || len(slices.Compact(slices.Sorted(slices.Values(b.Models)))) < len(b.Models):
		return "models must have 1 to 16 names, none twice"
	case b.DefaultModel != nil && !slices.Contains(b.Models, *b.DefaultModel):
		return "default_model must be one of models"
	case b.TimeoutS != nil && (*b.TimeoutS < 5 || *b.TimeoutS > 600):
		return "timeout_s must be 5 to 600"
	case b.MaxInFlight != nil && (*b.MaxInFlight < 1 || *b.MaxInFlight > 8):
		return "max_in_flight must be 1 to 8"
	case b.Host != nil && !hostName.MatchString(*b.Host):
		return "host must be a host name (1-32 lower-case letters and digits)"
	case b.MemoryGB != nil && (*b.MemoryGB < 0.5 || *b.MemoryGB > 1024):
		return "memory_gb must be 0.5 to 1024"
	case b.KeySecret != nil && !keySecret.MatchString(*b.KeySecret):
		return "key_secret must be a secret file's name"
	case b.Kind == "server" && (b.Host == nil || b.MemoryGB == nil):
		return "a server needs host and memory_gb"
	case b.Kind == "server" && b.MaxInFlight != nil:
		return "a server has no max_in_flight"
	case b.Preset != nil && !ProviderName.MatchString(*b.Preset):
		return "preset must be a preset's id"
	case b.Kind != "server" && b.KeySecret == nil:
		return "a " + b.Kind + " provider needs key_secret (the name of its key's secret file)"
	case b.Kind != "server" && !strings.HasPrefix(b.BaseURL, "https://"):
		return "a " + b.Kind + " provider's base_url must be https"
	case b.Kind != "server" && (b.Host != nil || b.MemoryGB != nil):
		return "a " + b.Kind + " provider has no host or memory_gb"
	case len(b.Thinking) > 16:
		return "thinking has at most 16 models"
	}
	for m, level := range b.Thinking {
		if !slices.Contains(ThinkingLevels, level) {
			return "thinking: " + m + ": a level is off, on, low, medium or high"
		}
	}
	for _, m := range b.Models {
		if !providerModel.MatchString(m) {
			return "models: each like qwen2.5:7b"
		}
	}
	return ""
}

// Provider is one model provider as the admin sees it; the key itself is never here.
type Provider struct {
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	API          string          `json:"api"`
	Title        *string         `json:"title,omitempty"`
	BaseURL      string          `json:"base_url"`
	Models       []string        `json:"models"`
	DefaultModel string          `json:"default_model"`
	Enabled      bool            `json:"enabled"`
	TimeoutS     int             `json:"timeout_s"`
	Host         *string         `json:"host,omitempty"`
	MemoryGB     *float64        `json:"memory_gb,omitempty"`
	KeySecret    *string         `json:"key_secret,omitempty"`
	MaxInFlight  *int            `json:"max_in_flight,omitempty"`
	Compat       *ProviderCompat `json:"compat,omitempty"`
	Preset       *string         `json:"preset,omitempty"`
	// API 1.53: the levels set, and what each saved model a rule covers offers (read only)
	Thinking       map[string]string       `json:"thinking,omitempty"`
	ThinkingLevels map[string]ThinkingInfo `json:"thinking_levels,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

// ProviderList is GET /admin/providers.
type ProviderList struct {
	RemoteEnabled bool       `json:"remote_enabled"`
	Providers     []Provider `json:"providers"`
}

// ProviderPreset is one item of contracts/providers/presets.json (API 1.51).
type ProviderPreset struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Kind      string          `json:"kind"`
	BaseURL   string          `json:"base_url"`
	Models    []string        `json:"models"`
	KeySecret *string         `json:"key_secret,omitempty"`
	Compat    *ProviderCompat `json:"compat,omitempty"`
	Thinking  json.RawMessage `json:"thinking,omitempty"` // API 1.53: the rules as in the file, or "models"
	Docs      string          `json:"docs"`
	Note      string          `json:"note"`
}

// ProviderPresetList is GET /admin/provider-presets.
type ProviderPresetList struct {
	RemoteEnabled bool             `json:"remote_enabled"`
	Presets       []ProviderPreset `json:"presets"`
}

// ProviderFetch is a preset's Fetch body: only the key file's name (a base_url is an unknown field: 422).
type ProviderFetch struct {
	KeySecret *string `json:"key_secret"`
}

// Validate checks the key file's name.
func (b *ProviderFetch) Validate() string {
	if b.KeySecret != nil && !keySecret.MatchString(*b.KeySecret) {
		return "key_secret must be a secret file's name"
	}
	return ""
}

// KeySecretName reports whether s is a key file's name (no "/": never a path outside the keys folder).
func KeySecretName(s string) bool { return keySecret.MatchString(s) }

// ProviderKey is GET /admin/provider-keys/{key_secret}: whether the key file is there, never the key; API 1.52:
// whether the deployment lets the admin save keys here.
type ProviderKey struct {
	KeySecret string `json:"key_secret"`
	Path      string `json:"path"`
	Found     bool   `json:"found"`
	Writable  bool   `json:"writable"`
}

// ProviderKeySave is PUT /admin/provider-keys/{key_secret} (API 1.52, plan 27): a key to check and save, with one
// source for the check's URL (a base_url is an unknown field: 422). Key is never printed, logged or returned: no
// String method, no %v use; Validate's texts name the rule only.
type ProviderKeySave struct {
	Key      string  `json:"key"`
	Preset   *string `json:"preset"`
	Provider *string `json:"provider"`
	Force    bool    `json:"force"`
}

// KeyCharsText is the 422 for a key with a character outside 0x21-0x7E (a "…" from a cut copy, a curly quote, a
// non-breaking or zero-width space): our own fixed text, never the key or the character.
const KeyCharsText = "the key has a character keys never have (like … or a curly quote): copy it again from the provider"

// Validate trims the key and checks it (1-1024 printable ASCII characters without spaces) and the source.
func (b *ProviderKeySave) Validate() string {
	b.Key = strings.TrimSpace(b.Key)
	if b.Key == "" || len(b.Key) > 1024 {
		return "key must be 1-1024 characters"
	}
	for i := 0; i < len(b.Key); i++ {
		if b.Key[i] < 0x21 || b.Key[i] > 0x7e {
			return KeyCharsText
		}
	}
	if (b.Preset == nil) == (b.Provider == nil) {
		return "name either preset or provider"
	}
	return ""
}

// ProviderKeySaved is its answer: the check's verdict and the key file's status after it, never the key.
type ProviderKeySaved struct {
	KeySecret string  `json:"key_secret"`
	Path      string  `json:"path"`
	Found     bool    `json:"found"`
	Saved     bool    `json:"saved"`
	Check     string  `json:"check"` // ok | refused | unreachable | skipped
	Detail    *string `json:"detail,omitempty"`
}

// ProviderTest is POST /admin/providers/{name}/test.
type ProviderTest struct {
	OK      bool     `json:"ok"`
	Models  []string `json:"models"`
	Answer  *string  `json:"answer"`
	Error   *string  `json:"error"`
	Failure *string  `json:"failure"` // network | answer | refused | off
	Seconds float64  `json:"seconds"`
	// API 1.53: what each listed model a rule covers offers
	Thinking map[string]ThinkingInfo `json:"thinking,omitempty"`
}

// ModelSource is one provider as every user sees it: names and models only.
type ModelSource struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
	// API 1.53: the thinking level set per model (absent: none set)
	Thinking map[string]string `json:"thinking,omitempty"`
}

// ModelSourceList is GET /model-sources.
type ModelSourceList struct {
	RemoteEnabled bool          `json:"remote_enabled"`
	Sources       []ModelSource `json:"sources"`
}

// CustomNodeList is GET /custom-nodes.
type CustomNodeList struct {
	Nodes []registry.CustomNode `json:"nodes"`
}

// ------------------------------------------------------------------ the director's chat

// BatchDirector is a batch's director chat state.
type BatchDirector struct {
	Node     string `json:"node"`
	State    string `json:"state"` // idle | waiting | thinking | off
	Wakes    int    `json:"wakes"`
	MaxWakes int    `json:"max_wakes"`
}

// BatchRemote is a batch's remote calls so far against its caps.
type BatchRemote struct {
	Calls           int `json:"calls"`
	Tokens          int `json:"tokens"`
	NetworkFailures int `json:"network_failures"`
	MaxCalls        int `json:"max_calls"`
	MaxTokens       int `json:"max_tokens"`
}

// ChatMessage is one message of a batch's director chat.
type ChatMessage struct {
	ID        string            `json:"id"`
	Role      string            `json:"role"` // user | director | notice
	Text      string            `json:"text"`
	At        time.Time         `json:"at"`
	Wake      *int              `json:"wake"`
	ToolCalls []json.RawMessage `json:"tool_calls"`
	Rows      []int             `json:"rows"`
}

// ChatPage is GET /batches/{id}/chat.
type ChatPage struct {
	BatchID  string        `json:"batch_id"`
	Director BatchDirector `json:"director"`
	Messages []ChatMessage `json:"messages"`
}

// ChatPost is POST /batches/{id}/chat.
type ChatPost struct {
	Text string `json:"text"`
}

// Validate wants 1-2000 characters after trimming.
func (b *ChatPost) Validate() string {
	b.Text = strings.TrimSpace(b.Text)
	if n := utf8.RuneCountInString(b.Text); n < 1 || n > 2000 {
		return "text must be 1-2000 characters"
	}
	return ""
}
