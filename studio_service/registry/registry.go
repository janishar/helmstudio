// Package registry: what the runners publish, watched and cached. KV `registry` keys <host>.<env>.nodes|workflows|models
// (contracts/registry.schema.json) and KV `hosts` keys <host>.<env> (each runner's live state).
package registry

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// Stale is how old a hosts key may be before its runner counts as offline.
const Stale = 30 * time.Second

// Param is one param of a node schema.
type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // int | float | bool | string | text | enum | seed | image | rows
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
}

// IsImageField reports whether a rows item field holds an Image.
func IsImageField(raw json.RawMessage) bool { return string(raw) == `"Image"` }

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
	Name string `json:"name"`
	Type string `json:"type"`
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

// Node is one node of a workflow graph.
type Node struct {
	Kind   string            `json:"kind"`
	Params map[string]any    `json:"params"` // never nil: a command must carry every node's params
	In     map[string]string `json:"in,omitempty"`
}

// Workflow is a runnable graph as a runner publishes it, with the schemas of its kinds from the same runner.
type Workflow struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Family      string                `json:"family"`
	Env         string                `json:"env"`
	Nodes       map[string]Node       `json:"nodes"`
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
}

// Model is one model of a registry models value: the models present on a host for an env.
type Model struct {
	ID       string    `json:"id"`
	Family   string    `json:"family"`
	Variants []Variant `json:"variants"` // absent for a model without variants (echo)
}

// Variant is one variant of a model on a host.
type Variant struct {
	ID       string `json:"id"`
	Presence string `json:"presence"` // present | missing
}

// Has reports whether the variant's files are on the host: always for a model without variants.
func (m Model) Has(variant string) bool {
	return len(m.Variants) == 0 || slices.Contains(m.Variants, Variant{ID: variant, Presence: "present"})
}

// Live reports whether the runner refreshed its key within Stale of now.
func (h Host) Live(now time.Time) bool { return now.Sub(h.UpdatedAt) <= Stale }

// Registry holds the latest value of every registry and hosts key. The zero value is empty and ready to use.
type Registry struct {
	mu        sync.RWMutex
	nodes     map[string][]NodeSchema // "<host>.<env>" → its node schemas
	workflows map[string][]Workflow   // "<host>.<env>" → its workflows (without node schemas)
	models    map[string][]Model      // "<host>.<env>" → the models present there
	hosts     map[string]Host         // "<host>.<env>" → its state
	awaiting  map[string]bool         // watched buckets whose current values have not all arrived yet
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
			return nil
		}
		var parsed struct {
			Nodes []NodeSchema `json:"nodes"`
		}
		if err := json.Unmarshal(value, &parsed); err != nil {
			return err
		}
		set(&r.nodes, source, parsed.Nodes)
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
	}
	return nil
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
