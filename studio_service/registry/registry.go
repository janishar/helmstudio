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

// WorkflowSound is a graph's sound class (API 1.79, contracts/README.md "Music and sound"; check.py workflow_sound):
// the `sound` of the first node, in output node id order, whose Audio output an output node takes; "" without one.
func WorkflowSound(nodes map[string]Node, schemas map[string]NodeSchema) string {
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		ref, ok := nodes[id].In["media"]
		if nodes[id].Kind != "output" || !ok {
			continue
		}
		srcID, out, _ := strings.Cut(ref, ".")
		schema := schemas[nodes[srcID].Kind]
		for _, port := range schema.Outputs {
			if port.Name == out && port.Type == "Audio" && schema.Sound != "" {
				return schema.Sound
			}
		}
	}
	return ""
}
