package dto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	gallery "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/registry"
)

// Saved pipelines (contracts PipelineDoc and friends, API 1.7).

// PipelineInput is one column of an inputs node.
type PipelineInput struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"` // Text | Image | Int | Float | Seed
	Label *string `json:"label,omitempty"`
}

// PipelineNode is any node kind; only the fields of its kind are set. The inputs node's `inputs` (its columns) and a
// step's `inputs` (fixed values) share a JSON name, as an output's `from` (one source) and a write's, decide's or
// memory write's `from` (a list), so the node has its own (un)marshalling.
type PipelineNode struct {
	Kind     string
	Title    *string
	Position *registry.Position
	Columns  []PipelineInput // inputs: the batch's columns, in order
	// step
	Workflow string
	Seeds    *int
	Preset   *string
	Fixed    map[string]any    // the step's `inputs`
	Bind     map[string]string // workflow param ← source
	Looks    []string          // the looks every run of the step is made with
	// gate (judge: takes and keep too)
	Takes string
	Keep  *int
	// output
	From       string
	Collection string
	// judge (API 1.14)
	Criteria *PipelineCriteria
	Mode     *string // suggest | auto
	Model    *string // auto | an ask workflow id (judge, write, decide)
	Memory   *string // a memory read node
	// write, decide, memory write: their text sources
	Sources     []string
	To          string  // write: the $<Text input> it overwrites
	Instruction *string // write
	Question    string  // decide
	Routes      map[string]string
	// memory
	Op    string // read | write
	Key   *string
	Limit *int
}

// PipelineCriteria is a judge's checklist: every must line holds for a take to pass; nice lines order those that do.
type PipelineCriteria struct {
	Must []string `json:"must,omitempty"` // ponytail: an empty list is left out (a missing list means [])
	Nice []string `json:"nice,omitempty"`
}

// Lines are the must lines, then the nice ones.
func (c PipelineCriteria) Lines() []string { return append(slices.Clone(c.Must), c.Nice...) }

// ModelOr is the node's model, "auto" when absent.
func (n PipelineNode) ModelOr() string {
	if n.Model == nil {
		return "auto"
	}
	return *n.Model
}

// ModeOr is a judge's mode, suggest when absent.
func (n PipelineNode) ModeOr() string {
	if n.Mode == nil {
		return "suggest"
	}
	return *n.Mode
}

// LimitOr is a memory read's limit, 8 when absent.
func (n PipelineNode) LimitOr() int {
	if n.Limit == nil {
		return 8
	}
	return *n.Limit
}

// InstructionOr is a write's instruction with its default.
func (n PipelineNode) InstructionOr() string {
	if n.Instruction == nil {
		return "Fix what the reviewer found; keep everything else."
	}
	return *n.Instruction
}

// Asks reports whether the node kind asks a model (judge, write, decide).
func Asks(kind string) bool { return kind == "judge" || kind == "write" || kind == "decide" }

// SeedsOr1 is the step's seeds, 1 when absent.
func (n PipelineNode) SeedsOr1() int {
	if n.Seeds == nil {
		return 1
	}
	return *n.Seeds
}

// KeepOr1 is the gate's keep, 1 when absent.
func (n PipelineNode) KeepOr1() int {
	if n.Keep == nil {
		return 1
	}
	return *n.Keep
}

// nodeFields are the keys each kind allows (the schemas are closed).
var nodeFields = map[string][]string{
	"inputs": {"kind", "title", "position", "inputs"},
	"step":   {"kind", "title", "position", "workflow", "seeds", "preset", "inputs", "bind", "looks"},
	"gate":   {"kind", "title", "position", "takes", "keep"},
	"output": {"kind", "title", "position", "from", "collection"},
	"judge":  {"kind", "title", "position", "takes", "criteria", "keep", "mode", "model", "memory"},
	"write":  {"kind", "title", "position", "from", "to", "instruction", "model"},
	"decide": {"kind", "title", "position", "question", "from", "routes", "model"},
	"memory": {"kind", "title", "position", "op", "key", "limit", "from"},
}

// UnmarshalJSON decodes the fields of the node's kind; another kind's field, or an unknown one, is an error.
func (n *PipelineNode) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw["kind"], &n.Kind); err != nil || nodeFields[n.Kind] == nil {
		return fmt.Errorf("kind must be inputs, step, gate, output, judge, write, decide or memory")
	}
	for key := range raw {
		if !slices.Contains(nodeFields[n.Kind], key) {
			return fmt.Errorf("unknown field %s of a %s node", key, n.Kind)
		}
	}
	targets := map[string]any{"title": &n.Title, "position": &n.Position, "workflow": &n.Workflow, "seeds": &n.Seeds,
		"preset": &n.Preset, "bind": &n.Bind, "looks": &n.Looks, "takes": &n.Takes, "keep": &n.Keep, "from": &n.From, "collection": &n.Collection,
		"criteria": &n.Criteria, "mode": &n.Mode, "model": &n.Model, "memory": &n.Memory, "to": &n.To, "instruction": &n.Instruction,
		"question": &n.Question, "routes": &n.Routes, "op": &n.Op, "key": &n.Key, "limit": &n.Limit}
	if n.Kind != "output" {
		targets["from"] = &n.Sources
	}
	if n.Kind == "inputs" {
		targets["inputs"] = &n.Columns
	} else {
		targets["inputs"] = &n.Fixed
	}
	for key, value := range raw {
		if key == "kind" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(targets[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// MarshalJSON writes the fields of the node's kind that are set.
func (n PipelineNode) MarshalJSON() ([]byte, error) {
	out := map[string]any{"kind": n.Kind}
	set := func(key string, value any, present bool) {
		if present {
			out[key] = value
		}
	}
	set("title", n.Title, n.Title != nil)
	set("position", n.Position, n.Position != nil)
	switch n.Kind {
	case "inputs":
		out["inputs"] = n.Columns
	case "step":
		out["workflow"] = n.Workflow
		set("seeds", n.Seeds, n.Seeds != nil)
		set("preset", n.Preset, n.Preset != nil)
		set("inputs", n.Fixed, n.Fixed != nil)
		set("bind", n.Bind, n.Bind != nil)
		set("looks", n.Looks, n.Looks != nil)
	case "gate":
		out["takes"] = n.Takes
		set("keep", n.Keep, n.Keep != nil)
	case "output":
		out["from"], out["collection"] = n.From, n.Collection
	case "judge":
		out["takes"], out["criteria"] = n.Takes, n.Criteria
		set("keep", n.Keep, n.Keep != nil)
		set("mode", n.Mode, n.Mode != nil)
		set("model", n.Model, n.Model != nil)
		set("memory", n.Memory, n.Memory != nil)
	case "write":
		out["from"], out["to"] = n.Sources, n.To
		set("instruction", n.Instruction, n.Instruction != nil)
		set("model", n.Model, n.Model != nil)
	case "decide":
		out["question"], out["from"], out["routes"] = n.Question, n.Sources, n.Routes
		set("model", n.Model, n.Model != nil)
	case "memory":
		out["op"] = n.Op
		set("key", n.Key, n.Key != nil)
		set("limit", n.Limit, n.Limit != nil)
		set("from", n.Sources, n.Sources != nil)
	}
	return json.Marshal(out)
}

// PipelineBranch is a gate's {pass, fail, max, then}.
type PipelineBranch struct {
	Pass string `json:"pass"`
	Fail string `json:"fail"`
	Max  int    `json:"max"`
	Then string `json:"then"`
}

// PipelineRoutes is a decide's {routes: {label: node}, max, then: label} (API 1.14).
type PipelineRoutes struct {
	Routes map[string]string `json:"routes"`
	Max    int               `json:"max"`
	Then   string            `json:"then"`
}

// PipelineEdge is [from, to], [gate or judge, {pass, fail, max, then}] or [decide, {routes, max, then}].
type PipelineEdge struct {
	From   string
	To     string          // a plain edge
	Branch *PipelineBranch // a branch
	Routes *PipelineRoutes // a decide's routes
}

// Targets are the nodes the edge leads to: to, or pass, fail, then, or every route's node (by label).
func (e PipelineEdge) Targets() []string {
	switch {
	case e.Branch != nil:
		return []string{e.Branch.Pass, e.Branch.Fail, e.Branch.Then}
	case e.Routes != nil:
		var out []string
		for _, label := range slices.Sorted(maps.Keys(e.Routes.Routes)) {
			out = append(out, e.Routes.Routes[label])
		}
		return out
	}
	return []string{e.To}
}

// UnmarshalJSON decodes the two-item array.
func (e *PipelineEdge) UnmarshalJSON(data []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil || len(pair) != 2 {
		return fmt.Errorf("an edge is [from, to] or [gate, {pass, fail, max, then}]")
	}
	if err := json.Unmarshal(pair[0], &e.From); err != nil {
		return fmt.Errorf("an edge's from must be a node id")
	}
	if json.Unmarshal(pair[1], &e.To) == nil {
		return nil
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(pair[1], &probe) == nil && probe["routes"] != nil {
		decoder := json.NewDecoder(bytes.NewReader(pair[1]))
		decoder.DisallowUnknownFields()
		var routes struct {
			Routes map[string]string
			Max    *int
			Then   *string
		}
		if err := decoder.Decode(&routes); err != nil || routes.Max == nil || routes.Then == nil {
			return fmt.Errorf("a decide's edge is {routes, max, then}")
		}
		e.Routes = &PipelineRoutes{Routes: routes.Routes, Max: *routes.Max, Then: *routes.Then}
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(pair[1]))
	decoder.DisallowUnknownFields()
	var branch struct {
		Pass, Fail, Then *string
		Max              *int
	}
	if err := decoder.Decode(&branch); err != nil || branch.Pass == nil || branch.Fail == nil || branch.Then == nil ||
		branch.Max == nil {
		return fmt.Errorf("a branch is {pass, fail, max, then}")
	}
	e.Branch = &PipelineBranch{Pass: *branch.Pass, Fail: *branch.Fail, Max: *branch.Max, Then: *branch.Then}
	return nil
}

// MarshalJSON writes the two-item array.
func (e PipelineEdge) MarshalJSON() ([]byte, error) {
	if e.Branch != nil {
		return json.Marshal([]any{e.From, e.Branch})
	}
	if e.Routes != nil {
		return json.Marshal([]any{e.From, e.Routes})
	}
	return json.Marshal([]any{e.From, e.To})
}

// PipelineDoc is a pipeline as the editor saves or validates it.
type PipelineDoc struct {
	Title string                  `json:"title"`
	Nodes map[string]PipelineNode `json:"nodes"`
	Edges []PipelineEdge          `json:"edges"`
}

// Pipeline is one of the caller's pipelines.
type Pipeline struct {
	PipelineDoc
	ID             string            `json:"id"`
	BasedOn        *string           `json:"based_on"`
	Version        int               `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	WorkflowTitles map[string]string `json:"workflow_titles"`
	LastBatch      *Batch            `json:"last_batch"`
	Problems       *[]Problem        `json:"problems,omitempty"` // only from get, create, replace and save-as
	Bounds         *PipelineBounds   `json:"bounds,omitempty"`   // with problems, when nothing blocks saving
}

// PipelineBounds is a pipeline's worst case per row: generation runs and LLM calls (API 1.14).
type PipelineBounds struct {
	Runs     int `json:"runs"`
	LLMCalls int `json:"llm_calls"`
}

// PipelineValidation is POST /pipelines/validate's answer.
type PipelineValidation struct {
	Problems []Problem         `json:"problems"`
	CanSave  bool              `json:"can_save"`
	CanRun   bool              `json:"can_run"`
	Bounds   *PipelineBounds   `json:"bounds,omitempty"` // when nothing blocks saving
	Models   map[string]string `json:"models"`
}

// PipelineList is the caller's pipelines, newest first.
type PipelineList struct {
	Pipelines []Pipeline `json:"pipelines"`
}

// PipelineCreate is POST /pipelines: a whole document with optional based_on, or {based_on, title?} to copy.
type PipelineCreate struct {
	PipelineDoc
	BasedOn *string `json:"based_on"`
}

// IsCopy reports whether the body is the copy form: no document fields but the title.
func (b *PipelineCreate) IsCopy() bool { return b.Nodes == nil && b.Edges == nil }

// Validate checks either form's shape.
func (b *PipelineCreate) Validate() string {
	if b.BasedOn != nil && !uuidPattern.MatchString(*b.BasedOn) {
		return "based_on must be a uuid"
	}
	if b.IsCopy() {
		if b.BasedOn == nil {
			return "title, nodes and edges are required (or based_on to copy)"
		}
		if b.Title != "" {
			return validTitle(b.Title)
		}
		return ""
	}
	return b.PipelineDoc.Validate()
}

// PipelineUpdate is PUT /pipelines/{id}: the document and the version the edit started from.
type PipelineUpdate struct {
	PipelineDoc
	Version int `json:"version"`
}

// Validate checks the document and the version.
func (b *PipelineUpdate) Validate() string {
	if b.Version < 1 {
		return "version is required (the version the edit started from)"
	}
	return b.PipelineDoc.Validate()
}

// PipelinePatch renames a pipeline.
type PipelinePatch struct {
	Title string `json:"title"`
}

// Validate wants a title of 1-80 characters after trimming.
func (b *PipelinePatch) Validate() string { return validTitle(b.Title) }

// TakePipeline is POST /takes/{id}/pipeline: an optional title.
type TakePipeline struct {
	Title *string `json:"title"`
}

// Validate wants 1-80 characters when given.
func (b *TakePipeline) Validate() string {
	if b.Title != nil {
		return validTitle(*b.Title)
	}
	return ""
}

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	workflowRef = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	sourceRef   = regexp.MustCompile(`^(\$[a-z][a-z0-9_]{0,63}|[a-z][a-z0-9_]{0,63}\.(takes|picked|reasons|notes))$`)
	inputRef    = regexp.MustCompile(`^\$[a-z][a-z0-9_]{0,63}$`)
	routeLabel  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	memoryKey   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	fromRef     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.(takes|picked)$`)
	inputTypes  = []string{"Text", "Image", "Int", "Float", "Seed"}
	maxEdges    = 64
)

// between reports whether text has lo..hi characters.
func between(text string, lo, hi int) bool {
	n := utf8.RuneCountInString(text)
	return n >= lo && n <= hi
}

// Validate checks the document's shape (contracts PipelineDoc); the pipeline rules are problems, checked elsewhere.
func (d *PipelineDoc) Validate() string {
	if detail := validTitle(d.Title); detail != "" {
		return detail
	}
	if len(d.Nodes) == 0 {
		return "nodes must have at least one node"
	}
	if d.Edges == nil {
		return "edges is required"
	}
	if len(d.Edges) > maxEdges {
		return "edges: at most 64"
	}
	for _, id := range slices.Sorted(maps.Keys(d.Nodes)) {
		if detail := validNode(id, d.Nodes[id]); detail != "" {
			return detail
		}
	}
	for i, edge := range d.Edges {
		ids := append([]string{edge.From}, edge.Targets()...)
		for _, id := range ids {
			if !slug.MatchString(id) {
				return fmt.Sprintf("edges[%d]: %q is not a node id", i, id)
			}
		}
		if edge.Branch != nil && (edge.Branch.Max < 1 || edge.Branch.Max > 10) {
			return fmt.Sprintf("edges[%d].max must be 1 to 10", i)
		}
		if r := edge.Routes; r != nil {
			if r.Max < 1 || r.Max > 10 || len(r.Routes) < 2 || len(r.Routes) > 6 || !routeLabel.MatchString(r.Then) {
				return fmt.Sprintf("edges[%d]: routes needs 2 to 6 labels, max 1 to 10 and then a label", i)
			}
			for label := range r.Routes {
				if !routeLabel.MatchString(label) {
					return fmt.Sprintf("edges[%d]: %q is not a route label", i, label)
				}
			}
		}
	}
	return ""
}

// validNode checks one node's fields.
func validNode(id string, n PipelineNode) string {
	at := "nodes." + id
	switch {
	case !slug.MatchString(id):
		return fmt.Sprintf("%s: a node id must match ^[a-z][a-z0-9_]{0,63}$", at)
	case n.Title != nil && !between(*n.Title, 1, 60):
		return at + ".title must be 1-60 characters"
	case n.Position != nil && (n.Position.X < -1e6 || n.Position.X > 1e6 || n.Position.Y < -1e6 || n.Position.Y > 1e6):
		return at + ".position: x and y must be within ±1000000"
	}
	switch n.Kind {
	case "inputs":
		if len(n.Columns) < 1 || len(n.Columns) > 16 {
			return at + ".inputs must have 1 to 16 inputs"
		}
		for _, input := range n.Columns {
			if !slug.MatchString(input.Name) || !slices.Contains(inputTypes, input.Type) ||
				(input.Label != nil && !between(*input.Label, 1, 60)) {
				return at + ".inputs: each needs a slug name, a type of Text, Image, Int, Float or Seed, and a label of 1-60 characters if any"
			}
		}
	case "step":
		switch {
		case !workflowRef.MatchString(n.Workflow):
			return at + ".workflow must be a template slug or a workflow id"
		case n.Seeds != nil && (*n.Seeds < 1 || *n.Seeds > 8):
			return at + ".seeds must be 1 to 8"
		case n.Preset != nil && !between(*n.Preset, 1, 40):
			return at + ".preset must be 1-40 characters"
		case len(n.Bind) > 16:
			return at + ".bind: at most 16"
		case len(n.Looks) > 8:
			return at + ".looks: at most 8"
		}
		for i, id := range n.Looks {
			if !LookID.MatchString(id) {
				return fmt.Sprintf("%s.looks[%d]: not a look id", at, i+1)
			}
		}
		for key := range n.Fixed {
			if !reference.MatchString(key) {
				return fmt.Sprintf("%s.inputs: %q is not <node>.<param>", at, key)
			}
		}
		for key, source := range n.Bind {
			if !reference.MatchString(key) || !sourceRef.MatchString(source) {
				return fmt.Sprintf("%s.bind.%s: a <node>.<param> key and a source ($input, step.takes or gate.picked)", at, key)
			}
		}
	case "gate":
		switch {
		case !slug.MatchString(n.Takes):
			return at + ".takes must be a step id"
		case n.Keep != nil && (*n.Keep < 1 || *n.Keep > 8):
			return at + ".keep must be 1 to 8"
		}
	case "output":
		switch {
		case !fromRef.MatchString(n.From):
			return at + ".from must be <step>.takes or <gate>.picked"
		case !between(strings.TrimSpace(n.Collection), 1, 80):
			return at + ".collection must be 1-80 characters"
		}
	case "judge":
		switch {
		case !slug.MatchString(n.Takes):
			return at + ".takes must be a step id"
		case n.Criteria == nil || len(n.Criteria.Must) > 12 || len(n.Criteria.Nice) > 12:
			return at + ".criteria is {must, nice}, at most 12 lines each"
		case n.Keep != nil && (*n.Keep < 1 || *n.Keep > 8):
			return at + ".keep must be 1 to 8"
		case n.Mode != nil && *n.Mode != "suggest" && *n.Mode != "auto":
			return at + ".mode must be suggest or auto"
		case n.Memory != nil && !slug.MatchString(*n.Memory):
			return at + ".memory must be a memory node id"
		}
		for _, line := range n.Criteria.Lines() {
			if !between(line, 1, 200) {
				return at + ".criteria: each line 1-200 characters"
			}
		}
	case "write":
		switch {
		case !inputRef.MatchString(n.To):
			return at + ".to must be a $<input>"
		case n.Instruction != nil && !between(*n.Instruction, 1, 1000):
			return at + ".instruction must be 1-1000 characters"
		}
	case "decide":
		if !between(n.Question, 1, 1000) || len(n.Routes) < 2 || len(n.Routes) > 6 {
			return at + ": a question of 1-1000 characters and 2 to 6 routes"
		}
		for label, meaning := range n.Routes {
			if !routeLabel.MatchString(label) || !between(meaning, 1, 200) {
				return at + ".routes: each a label like retry and a meaning of 1-200 characters"
			}
		}
	case "memory":
		switch {
		case n.Op != "read" && n.Op != "write":
			return at + ".op must be read or write"
		case n.Key != nil && !memoryKey.MatchString(*n.Key):
			return at + ".key must match ^[a-z][a-z0-9_-]{0,63}$"
		case n.Op == "read" && (n.Sources != nil || (n.Limit != nil && (*n.Limit < 1 || *n.Limit > 16))):
			return at + ": a read has a key and a limit of 1 to 16, no from"
		case n.Op == "write" && (n.Key == nil || n.Limit != nil || n.Sources == nil):
			return at + ": a write has a key and from, no limit"
		}
	}
	if n.Model != nil && !workflowRef.MatchString(*n.Model) {
		return at + ".model must be auto or an ask workflow id"
	}
	if n.Sources != nil || n.Kind == "write" || n.Kind == "decide" {
		if len(n.Sources) < 1 || len(n.Sources) > 4 {
			return at + ".from must have 1 to 4 sources"
		}
		for _, src := range n.Sources {
			if !sourceRef.MatchString(src) {
				return at + ".from: each a source ($input, <judge>.reasons or <memory>.notes)"
			}
		}
	}
	return ""
}

// ------------------------------------------------------------------ batches of a pipeline

// BatchRowParked is where a waiting row is parked.
type BatchRowParked struct {
	Gate       string   `json:"gate"`
	Visit      int      `json:"visit"`
	Keep       int      `json:"keep"`
	Candidates []string `json:"candidates"`
	// a judge (API 1.14)
	Kind       string   `json:"kind,omitempty"` // gate | judge; "" = gate
	Suggested  []string `json:"suggested,omitempty"`
	DecisionID string   `json:"decision_id,omitempty"`
	OnReject   string   `json:"on_reject,omitempty"`
}

// BatchCell is one node of a pipeline row in the run table, its visits stacked.
type BatchCell struct {
	Node     string             `json:"node"`
	Kind     string             `json:"kind"`
	Attempts []BatchCellAttempt `json:"attempts"`
	Loop     json.RawMessage    `json:"loop,omitempty"` // a judge with a branch, a decide: {turns, max, then, gave_up}
}

// BatchCellAttempt is one visit of a cell.
type BatchCellAttempt struct {
	Visit      int      `json:"visit"`
	Status     string   `json:"status"`
	RunIDs     []string `json:"run_ids"`
	TakeIDs    []string `json:"take_ids"`
	Candidates []string `json:"candidates,omitempty"`
	Seeds      []int64  `json:"seeds"`
	Seconds    *float64 `json:"seconds"`
	Error      *string  `json:"error"`
	ThumbURL   *string  `json:"thumb_url"`
	// judge, write, decide (API 1.14)
	DecisionID string          `json:"decision_id,omitempty"`
	Judged     json.RawMessage `json:"judged,omitempty"`
	Route      json.RawMessage `json:"route,omitempty"`
}

// BatchPick is POST …/rows/{n}/pick: take_ids, or reject.
type BatchPick struct {
	Gate    string   `json:"gate"`
	TakeIDs []string `json:"take_ids"`
	Reject  *bool    `json:"reject"`
	Note    *string  `json:"note"` // a judge pick only: kept as a judge example
}

// Validate wants a gate and exactly one of take_ids (1-8 uuids) or reject: true.
func (b *BatchPick) Validate() string {
	switch {
	case !slug.MatchString(b.Gate):
		return "gate must be a node id"
	case (b.TakeIDs == nil) == (b.Reject == nil) || (b.Reject != nil && !*b.Reject):
		return "give take_ids or reject: true"
	case b.TakeIDs != nil && (len(b.TakeIDs) < 1 || len(b.TakeIDs) > 8):
		return "take_ids must have 1 to 8 takes"
	case b.Note != nil && !between(*b.Note, 1, 2000):
		return "note must be 1-2000 characters"
	}
	for _, id := range b.TakeIDs {
		if !uuidPattern.MatchString(id) {
			return "take_ids must be uuids"
		}
	}
	return ""
}

// BatchRowRetry is the optional body of POST …/rows/{n}/retry.
type BatchRowRetry struct {
	FromStep *int `json:"from_step"`
}

// Validate wants 1..2000 when given.
func (b *BatchRowRetry) Validate() string {
	if b.FromStep != nil && (*b.FromStep < 1 || *b.FromStep > 2000) {
		return "from_step must be 1 to 2000"
	}
	return ""
}

// ReviewCandidate is one take a gate offers.
type ReviewCandidate struct {
	TakeID   string             `json:"take_id"`
	Kind     string             `json:"kind"`
	URL      string             `json:"url"`
	ThumbURL string             `json:"thumb_url"`
	Video    *gallery.VideoInfo `json:"video,omitempty"` // kind video only
	Width    *int               `json:"width"`
	Height   *int               `json:"height"`
	Seed     *int64             `json:"seed"`
	Judge    *ReviewJudge       `json:"judge,omitempty"` // a row parked on a judge: this take's verdict
}

// BatchReviewItem is one parked row.
type BatchReviewItem struct {
	Row         int               `json:"row"`
	Attempt     int               `json:"attempt"`
	Inputs      map[string]any    `json:"inputs"`
	Gate        string            `json:"gate"`
	GateTitle   *string           `json:"gate_title"`
	Visit       int               `json:"visit"`
	Keep        int               `json:"keep"`
	OnReject    string            `json:"on_reject"` // retry | then | fail
	RejectsLeft int               `json:"rejects_left"`
	Candidates  []ReviewCandidate `json:"candidates"`
	// a row parked on a judge (API 1.14)
	Kind       string   `json:"kind,omitempty"`
	Suggested  []string `json:"suggested,omitempty"`
	Verdict    string   `json:"verdict,omitempty"`
	Model      string   `json:"model,omitempty"`
	DecisionID string   `json:"decision_id,omitempty"`
}

// BatchReview is every parked row of a batch.
type BatchReview struct {
	BatchID string            `json:"batch_id"`
	Items   []BatchReviewItem `json:"items"`
}

// ExecutionEdge is the edge a step left by.
type ExecutionEdge struct {
	To  string `json:"to"`
	Via string `json:"via"` // next | pass | fail | then
}

// ExecutionStep is one graph step of a row attempt.
type ExecutionStep struct {
	Step     int            `json:"step"`
	Node     string         `json:"node"`
	Part     string         `json:"part"`
	Visit    int            `json:"visit"`
	What     string         `json:"what"`
	Changed  []string       `json:"changed"`
	Status   string         `json:"status"`
	Edge     *ExecutionEdge `json:"edge"`
	Loops    map[string]int `json:"loops"`
	RunIDs   []string       `json:"run_ids"`
	TakeIDs  []string       `json:"take_ids"`
	Error    *string        `json:"error"`
	At       time.Time      `json:"at"`
	HasState bool           `json:"has_state"`
	Decision *Decision      `json:"decision,omitempty"` // API 1.14: the step details of a judge, write or decide
	Memory   *StepMemory    `json:"memory,omitempty"`
}

// ExecutionNode is a node the row passed.
type ExecutionNode struct {
	Visits int    `json:"visits"`
	Status string `json:"status"`
}

// ExecutionEdgeCount is an edge taken and how often.
type ExecutionEdgeCount struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Via   string `json:"via"`
	Count int    `json:"count"`
}

// RetriedFrom is the attempt and step a retried attempt re-ran from.
type RetriedFrom struct {
	Attempt int `json:"attempt"`
	Step    int `json:"step"`
}

// RowExecution is the execution view of one row attempt.
type RowExecution struct {
	BatchID     string                   `json:"batch_id"`
	Row         int                      `json:"row"`
	Attempt     int                      `json:"attempt"`
	Attempts    int                      `json:"attempts"`
	Status      string                   `json:"status"`
	Current     *string                  `json:"current"`
	Pipeline    PipelineDoc              `json:"pipeline"`
	Steps       []ExecutionStep          `json:"steps"`
	Nodes       map[string]ExecutionNode `json:"nodes"`
	Edges       []ExecutionEdgeCount     `json:"edges"`
	Loops       map[string]int           `json:"loops"`
	RetriedFrom *RetriedFrom             `json:"retried_from"`
}

// RowState is a row's state at one step's checkpoint.
type RowState struct {
	Row     int            `json:"row"`
	Attempt int            `json:"attempt"`
	Step    int            `json:"step"`
	Node    string         `json:"node"`
	State   map[string]any `json:"state"`
}

// ------------------------------------------------------------------ agent nodes (API 1.14)

// JudgeCheck is one criterion of a judge's checklist with its answer for one take.
type JudgeCheck struct {
	Criterion string `json:"criterion"`
	Level     string `json:"level"` // must | nice
	Pass      bool   `json:"pass"`
}

// ReviewJudge is a judge's verdict on one candidate of the review grid.
type ReviewJudge struct {
	Passes bool         `json:"passes"`
	Rank   int          `json:"rank"`
	Checks []JudgeCheck `json:"checks"`
	Reason string       `json:"reason"`
}

// DecisionSeen is one thumbnail a model saw, signed.
type DecisionSeen struct {
	TakeID   string `json:"take_id"`
	Frame    *int   `json:"frame"`
	Sha256   string `json:"sha256"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	ThumbURL string `json:"thumb_url"`
}

// DecisionJudgeTake is a judge's answer for one candidate.
type DecisionJudgeTake struct {
	TakeID   string       `json:"take_id"`
	Checks   []JudgeCheck `json:"checks"`
	Reason   string       `json:"reason"`
	Passes   bool         `json:"passes"`
	Rank     int          `json:"rank"`
	ThumbURL *string      `json:"thumb_url"`
}

// DecisionJudge is a judge decision's result.
type DecisionJudge struct {
	Criteria    PipelineCriteria    `json:"criteria"`
	Keep        int                 `json:"keep"`
	Mode        string              `json:"mode"`
	Takes       []DecisionJudgeTake `json:"takes"`
	Picked      []string            `json:"picked"`
	Verdict     string              `json:"verdict"`
	FirstFailed *string             `json:"first_failed"`
	Agreed      *bool               `json:"agreed"`
	UserPick    *[]string           `json:"user_pick"`
}

// Decision is one judge, write or decide visit as pipelines recorded it, thumbnails signed (plan 16.7).
type Decision struct {
	ID            string            `json:"id"`
	BatchID       string            `json:"batch_id"`
	Row           int               `json:"row"`
	Node          string            `json:"node"`
	Kind          string            `json:"kind"`
	Visit         int               `json:"visit"`
	Attempt       int               `json:"attempt"`
	Replayed      bool              `json:"replayed"`
	ReplayedFrom  *string           `json:"replayed_from"`
	RunIDs        []string          `json:"run_ids"`
	Workflow      string            `json:"workflow"`
	WorkflowTitle string            `json:"workflow_title"`
	Model         json.RawMessage   `json:"model"`
	Template      string            `json:"template"`
	Prompt        string            `json:"prompt"`
	Seen          []DecisionSeen    `json:"seen"`
	Answers       []json.RawMessage `json:"answers"`
	TokensIn      int               `json:"tokens_in"`
	TokensOut     int               `json:"tokens_out"`
	Seconds       float64           `json:"seconds"`
	MemoryRead    []string          `json:"memory_read"`
	Error         *string           `json:"error"`
	At            time.Time         `json:"at"`
	Judge         *DecisionJudge    `json:"judge,omitempty"`
	Write         json.RawMessage   `json:"write,omitempty"`
	Decide        json.RawMessage   `json:"decide,omitempty"`
}

// DecisionList is a row's or a take's decisions.
type DecisionList struct {
	Decisions []Decision `json:"decisions"`
}

// StepMemory is what a step read from the batch's memory snapshot and wrote for the next batches.
type StepMemory struct {
	Read  []string `json:"read"`
	Wrote []string `json:"wrote"`
}

// BatchAgain is POST /batches/{id}/again.
type BatchAgain struct {
	Decisions string  `json:"decisions"` // same | again
	Title     *string `json:"title"`
}

// Validate wants same or again, and a title of 1-200 characters when given.
func (b *BatchAgain) Validate() string {
	switch {
	case b.Decisions != "same" && b.Decisions != "again":
		return "decisions must be same or again"
	case b.Title != nil && !between(strings.TrimSpace(*b.Title), 1, 200):
		return "title must be 1-200 characters"
	}
	return ""
}

// MemoryCreate is POST /memory: a note for a pipeline's memory.
type MemoryCreate struct {
	PipelineID string  `json:"pipeline_id"`
	Text       string  `json:"text"`
	Key        *string `json:"key"`
}

// Validate wants a pipeline id, a text of 1-2000 characters after trimming and a key like briefs when given.
func (b *MemoryCreate) Validate() string {
	switch {
	case !uuidPattern.MatchString(b.PipelineID):
		return "pipeline_id must be a uuid"
	case !between(strings.TrimSpace(b.Text), 1, 2000):
		return "text must be 1-2000 characters"
	case b.Key != nil && !memoryKey.MatchString(*b.Key):
		return "key must match ^[a-z][a-z0-9_-]{0,63}$"
	}
	return ""
}

// MemorySource is where a memory item came from.
type MemorySource struct {
	Kind    string  `json:"kind"` // pick | node | user
	BatchID *string `json:"batch_id"`
	Row     *int    `json:"row"`
	Node    *string `json:"node"`
	Visit   *int    `json:"visit"`
}

// MemoryItem is one item of a pipeline's memory, its takes as signed refs.
type MemoryItem struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	PipelineID string            `json:"pipeline_id"`
	Key        *string           `json:"key"`
	Text       string            `json:"text"`
	Kept       []gallery.TakeRef `json:"kept"`
	Rejected   []gallery.TakeRef `json:"rejected"`
	Agreed     *bool             `json:"agreed"`
	Source     MemorySource      `json:"source"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// MemoryList is a pipeline's memory, newest first, with counts per kind.
type MemoryList struct {
	PipelineID string         `json:"pipeline_id"`
	Items      []MemoryItem   `json:"items"`
	Truncated  bool           `json:"truncated,omitempty"` // older items left out (pipelines' reply size cap)
	Counts     map[string]int `json:"counts"`
}
