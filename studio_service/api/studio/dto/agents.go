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
	Kind         string   `json:"kind"` // server | remote
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
}

// Validate checks the shape (the host rule is checked by the service): a server needs host and memory_gb and has no
// key; a remote one needs key_secret and https.
func (b *ProviderPut) Validate() string {
	switch {
	case b.Kind != "server" && b.Kind != "remote":
		return "kind must be server or remote"
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
	case b.Kind == "server" && (b.KeySecret != nil || b.MaxInFlight != nil):
		return "a server has no key_secret or max_in_flight"
	case b.Kind == "remote" && b.KeySecret == nil:
		return "a remote provider needs key_secret (the name of its key's secret file)"
	case b.Kind == "remote" && !strings.HasPrefix(b.BaseURL, "https://"):
		return "a remote provider's base_url must be https"
	case b.Kind == "remote" && (b.Host != nil || b.MemoryGB != nil):
		return "a remote provider has no host or memory_gb"
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
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	API          string    `json:"api"`
	Title        *string   `json:"title,omitempty"`
	BaseURL      string    `json:"base_url"`
	Models       []string  `json:"models"`
	DefaultModel string    `json:"default_model"`
	Enabled      bool      `json:"enabled"`
	TimeoutS     int       `json:"timeout_s"`
	Host         *string   `json:"host,omitempty"`
	MemoryGB     *float64  `json:"memory_gb,omitempty"`
	KeySecret    *string   `json:"key_secret,omitempty"`
	MaxInFlight  *int      `json:"max_in_flight,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ProviderList is GET /admin/providers.
type ProviderList struct {
	RemoteEnabled bool       `json:"remote_enabled"`
	Providers     []Provider `json:"providers"`
}

// ProviderTest is POST /admin/providers/{name}/test.
type ProviderTest struct {
	OK      bool     `json:"ok"`
	Models  []string `json:"models"`
	Answer  *string  `json:"answer"`
	Error   *string  `json:"error"`
	Failure *string  `json:"failure"` // network | answer | refused | off
	Seconds float64  `json:"seconds"`
}

// ModelSource is one provider as every user sees it: names and models only.
type ModelSource struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
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
