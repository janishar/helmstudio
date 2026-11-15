package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/registry"
)

// Mentions (contracts/README.md "Mentions (normative)"): an @handle in a prompt names one of the owner's elements;
// studio expands it when it builds a run's graph (runGraphs, the one place), never a node.

// part is a piece of a scanned prompt: raw text (`@@` kept) or a mention of a handle.
type part struct {
	Text    string `json:"text,omitempty"`
	Mention string `json:"mention,omitempty"`
	Raw     string `json:"raw,omitempty"`
}

// element is a library item as the expansion reads it (deleted takes already left out); a batch keeps these as its copy.
type element struct {
	ID      string   `json:"id"`
	Handle  string   `json:"handle"`
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Text    string   `json:"text"`
	Images  []string `json:"images"`
}

// expansion is what the mentions changed: each changed path's resolved value and its value as set, and the elements.
type expansion struct {
	Inputs   map[string]any               `json:"-"`
	Typed    map[string]any               `json:"typed,omitempty"`
	Elements []gallerydto.MadeWithElement `json:"elements,omitempty"`
}

// isWord is an ASCII word character (so bytes, runes and UTF-16 units agree).
func isWord(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// scan splits a prompt into text and mention parts; the raw parts join to the text.
func scan(text string) []part {
	parts := []part{}
	var buf strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "@@") {
			buf.WriteString("@@")
			i += 2
			continue
		}
		if text[i] == '@' && (i == 0 || !isWord(text[i-1])) {
			j := i + 1
			for j < len(text) && (isWord(text[j]) || text[j] == '-') {
				j++
			}
			token := strings.TrimRight(text[i+1:j], "-_")
			if token != "" && ('a' <= token[0] && token[0] <= 'z' || 'A' <= token[0] && token[0] <= 'Z') {
				if buf.Len() > 0 {
					parts = append(parts, part{Text: buf.String()})
					buf.Reset()
				}
				parts = append(parts, part{Mention: strings.ToLower(token), Raw: "@" + token})
				i += 1 + len(token)
				continue
			}
		}
		buf.WriteByte(text[i])
		i++
	}
	if buf.Len() > 0 {
		parts = append(parts, part{Text: buf.String()})
	}
	return parts
}

// mentioned is the distinct handles of parts, first mention first.
func mentioned(parts []part) []string {
	var out []string
	for _, p := range parts {
		if p.Mention != "" && !slices.Contains(out, p.Mention) {
			out = append(out, p.Mention)
		}
	}
	return out
}

// valueOf is a node's param value, its schema default when the node has none.
func valueOf(node registry.Node, param registry.Param) any {
	if value, ok := node.Params[param.Name]; ok {
		return value
	}
	return param.Default
}

// prompt is one text param mentions are looked for in.
type prompt struct {
	Node  string
	Param registry.Param
	Value string
	Parts []part
}

// prompts are the unwired text params of the known, not bypassed nodes, node id then schema order.
func prompts(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []prompt {
	var out []prompt
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		schema, known := schemas[node.Kind]
		if !known || node.Bypass {
			continue
		}
		for _, param := range schema.Params {
			if _, wired := node.In[param.Name]; param.Type != "text" || wired {
				continue
			}
			value, _ := valueOf(node, param).(string)
			out = append(out, prompt{Node: id, Param: param, Value: value, Parts: scan(value)})
		}
	}
	return out
}

// mentionedHandles is every handle the graph's prompts mention, first mention first.
func mentionedHandles(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []string {
	var out []string
	for _, p := range prompts(nodes, schemas) {
		for _, handle := range mentioned(p.Parts) {
			if !slices.Contains(out, handle) {
				out = append(out, handle)
			}
		}
	}
	return out
}

// referenceRows is where element images go: the first unwired rows param (node id, then schema order) of a known, not
// bypassed node whose mention has {n} and whose item has one Image field; ok false = the family takes text only.
func referenceRows(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) (string, registry.Param, string, bool) {
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		schema, known := schemas[node.Kind]
		if !known || node.Bypass {
			continue
		}
		for _, param := range schema.Params {
			field, ok := imageField(param)
			if _, wired := node.In[param.Name]; ok && field != "" && !wired && strings.Contains(param.Mention, "{n}") {
				return id, param, field, true
			}
		}
	}
	return "", registry.Param{}, "", false
}

// expandMentions expands every mention of a graph whose params hold the merged, checked values against lib (the
// owner's elements, deleted takes left out); sizes are the user's row takes' [width, height] as studio knows them. It
// changes nothing; the errors are the 422 details.
func expandMentions(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, lib []element,
	sizes map[string][2]int) (expansion, []string) {
	byHandle := map[string]element{}
	for _, e := range lib {
		byHandle[e.Handle] = e
	}
	all := prompts(nodes, schemas)
	var errs, used []string
	for _, p := range all {
		for _, handle := range mentioned(p.Parts) {
			if _, ok := byHandle[handle]; !ok {
				errs = append(errs, fmt.Sprintf("%s.%s: unknown element @%s", p.Node, p.Param.Name, handle))
			} else if !slices.Contains(used, handle) {
				used = append(used, handle)
			}
		}
	}
	if len(errs) > 0 {
		return expansion{}, errs
	}
	out := expansion{Inputs: map[string]any{}, Typed: map[string]any{}, Elements: []gallerydto.MadeWithElement{}}
	tokens := map[string][]string{}
	if id, param, field, ok := referenceRows(nodes, schemas); ok && len(used) > 0 {
		limit := 100.0
		if param.Max != nil {
			limit = *param.Max
		}
		path := id + "." + param.Name
		before, _ := valueOf(nodes[id], param).([]any)
		if before == nil {
			before = []any{}
		}
		rows := slices.Clone(before)
		at := map[string]int{} // take id → its row number, the user's rows first
		for i, row := range rows {
			fields, _ := row.(map[string]any)
			if ref, ok := fields[field].(map[string]any); ok {
				if takeID, ok := ref["take_id"].(string); ok {
					if _, seen := at[takeID]; !seen {
						at[takeID] = i + 1
					}
				}
			}
		}
		var over []string
		full := -1
		for _, handle := range used {
			var fresh []string
			for _, takeID := range byHandle[handle].Images {
				if _, in := at[takeID]; !in {
					fresh = append(fresh, takeID)
				}
			}
			if full >= 0 || float64(len(rows)+len(fresh)) > limit {
				if full < 0 {
					full = len(rows)
				}
				if len(fresh) > 0 {
					over = append(over, fmt.Sprintf("@%s +%d", handle, len(fresh)))
				}
				continue
			}
			for _, takeID := range fresh {
				rows = append(rows, map[string]any{field: map[string]any{"take_id": takeID}})
				at[takeID] = len(rows)
			}
			for _, takeID := range byHandle[handle].Images {
				tokens[handle] = append(tokens[handle], strings.ReplaceAll(param.Mention, "{n}", strconv.Itoa(at[takeID])))
			}
		}
		if len(over) > 0 {
			return expansion{}, []string{fmt.Sprintf("%s: at most %s images; %d used, %s do not fit", path, format(limit),
				full, strings.Join(over, ", "))}
		}
		if len(rows) > len(before) {
			out.Inputs[path], out.Typed[path] = rows, before
			pinCanvases(nodes, schemas, before, field, sizes, out)
		}
	}
	for _, p := range all {
		var b strings.Builder
		for _, x := range p.Parts {
			if x.Mention == "" {
				b.WriteString(strings.ReplaceAll(x.Text, "@@", "@"))
				continue
			}
			b.WriteString(strings.Join(append([]string{byHandle[x.Mention].Name}, tokens[x.Mention]...), " "))
		}
		var notes []string
		for _, handle := range mentioned(p.Parts) {
			if e := byHandle[handle]; e.Text != "" {
				notes = append(notes, e.Name+": "+e.Text)
			}
		}
		if len(notes) > 0 {
			b.WriteString("\n\n" + strings.Join(notes, "\n"))
		}
		text, path := b.String(), p.Node+"."+p.Param.Name
		if n := utf8.RuneCountInString(text); p.Param.Max != nil && float64(n) > *p.Param.Max {
			errs = append(errs, fmt.Sprintf("%s: at most %s characters once elements are added (%d)", path, format(*p.Param.Max), n))
		} else if text != p.Value {
			out.Inputs[path], out.Typed[path] = text, p.Value
		}
	}
	if len(errs) > 0 {
		return expansion{}, errs
	}
	for _, handle := range used {
		e := byHandle[handle]
		out.Elements = append(out.Elements, gallerydto.MadeWithElement{ID: e.ID, Handle: e.Handle, Name: e.Name, Version: e.Version})
	}
	return out, nil
}

// pinCanvases keeps the output size the element rows would change (Mentions, step 2, Canvas): each canvas on auto
// becomes fixed at the size auto gives without them: its own without user rows, else the auto size of the last
// user row's take (clamped to the params' range; an unknown size pins the mode only).
func pinCanvases(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, before []any, field string,
	sizes map[string][2]int, out expansion) {
	var size [2]int
	known := false
	if len(before) > 0 {
		fields, _ := before[len(before)-1].(map[string]any)
		ref, _ := fields[field].(map[string]any)
		takeID, _ := ref["take_id"].(string)
		size, known = sizes[takeID]
	}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		schema, ok := schemas[node.Kind]
		if !ok || node.Bypass || node.Kind != "canvas" {
			continue
		}
		params := map[string]registry.Param{}
		for _, p := range schema.Params {
			params[p.Name] = p
		}
		if valueOf(node, params["mode"]) != "auto" {
			continue
		}
		out.Inputs[id+".mode"], out.Typed[id+".mode"] = "fixed", "auto"
		if !known {
			continue
		}
		width, height := autoSize(size[0], size[1])
		for name, v := range map[string]float64{"width": float64(width), "height": float64(height)} {
			p := params[name]
			if p.Min != nil {
				v = max(v, *p.Min)
			}
			if p.Max != nil {
				v = min(v, *p.Max)
			}
			if was := valueOf(node, p); was != v {
				out.Inputs[id+"."+name], out.Typed[id+"."+name] = v, was
			}
		}
	}
}

// autoSize is canvas auto with references (the runner's images.auto_size): about 1 MP at width / height, sides
// rounded half to even to 32.
func autoSize(width, height int) (int, int) {
	ratio := float64(width) / float64(height)
	w := math.Sqrt(1024 * 1024 * ratio)
	return int(math.RoundToEven(w/32)) * 32, int(math.RoundToEven(w/ratio/32)) * 32
}

// built is one run's graph and what its mentions did.
type built struct {
	Nodes     map[string]registry.Node
	Expansion expansion
}

// runGraphs is the one place a run's graph is made (Create, a batch's rows at Submit, studio.runs.create): each
// inputs resolved with the workflow (resolve), then its mentions expanded against the owner's elements, read once for
// all rows: now (copied nil, as the caller) or a batch's copy (images of takes deleted since left out). It returns per
// row the graph or the 422 detail (errors joined by "; "), and the elements read (a batch keeps them as its copy).
func (s *Service) runGraphs(ctx context.Context, tx pgx.Tx, ownerID string, workflow registry.Workflow,
	rows []map[string]any, copied *[]element) ([]built, []string, []element, error) {
	out, details := make([]built, len(rows)), make([]string, len(rows))
	var handles []string
	for i, inputs := range rows {
		nodes, detail := resolve(workflow, inputs)
		out[i].Nodes, details[i] = nodes, detail
		if detail == "" {
			for _, handle := range mentionedHandles(nodes, workflow.NodeSchemas) {
				if !slices.Contains(handles, handle) {
					handles = append(handles, handle)
				}
			}
		}
	}
	lib, err := readElements(ctx, tx, ownerID, handles, copied)
	if err != nil {
		return nil, nil, nil, err
	}
	var takeIDs []string
	for i := range out {
		if details[i] == "" && len(handles) > 0 {
			for _, ref := range images(out[i].Nodes) {
				takeIDs = append(takeIDs, ref.TakeID)
			}
		}
	}
	sizes, err := takeSizes(ctx, tx, ownerID, takeIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range out {
		if details[i] != "" {
			continue
		}
		exp, errs := expandMentions(out[i].Nodes, workflow.NodeSchemas, lib, sizes)
		if len(errs) > 0 {
			details[i] = strings.Join(errs, "; ")
			continue
		}
		var bad []string
		for _, path := range slices.Sorted(maps.Keys(exp.Inputs)) {
			value := exp.Inputs[path]
			id, name, _ := strings.Cut(path, ".")
			if param, _ := paramOf(workflow.NodeSchemas[out[i].Nodes[id].Kind], name); param.Type == "rows" {
				var detail string // the element rows take their fields' defaults, or say why they cannot
				if value, detail = checkRows(param, value); detail != "" {
					bad = append(bad, path+": "+detail)
					continue
				}
			}
			if out[i].Nodes[id].Params == nil {
				node := out[i].Nodes[id]
				node.Params = map[string]any{}
				out[i].Nodes[id] = node
			}
			out[i].Nodes[id].Params[name] = value
		}
		if len(bad) > 0 {
			details[i] = strings.Join(bad, "; ")
			continue
		}
		out[i].Expansion = exp
	}
	return out, details, lib, nil
}

// readElements reads the owner's elements of handles: from the library now (as the caller's transaction or with an
// explicit owner check), or from a batch's copy; images of takes that are no longer the owner's ready, undeleted takes
// are left out either way.
func readElements(ctx context.Context, tx pgx.Tx, ownerID string, handles []string, copied *[]element) ([]element, error) {
	if len(handles) == 0 {
		return []element{}, nil
	}
	if copied == nil {
		return collect[element](ctx, tx, `SELECT l.id::text, l.handle, l.name, l.version, l.text,
			ARRAY(SELECT i::text FROM unnest(l.images) WITH ORDINALITY AS u(i, o) JOIN takes t ON t.id = u.i
				AND t.owner_id = l.owner_id AND t.deleted_at IS NULL AND t.ready ORDER BY u.o)
			FROM library_items l WHERE l.owner_id = $1 AND l.handle = ANY($2::text[]) ORDER BY l.handle`, ownerID, handles)
	}
	var ids []string
	for _, e := range *copied {
		ids = append(ids, e.Images...)
	}
	rows, _ := tx.Query(ctx, `SELECT id::text FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2
		AND deleted_at IS NULL AND ready`, ids, ownerID)
	live, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []element{}
	for _, e := range *copied {
		if slices.Contains(handles, e.Handle) {
			e.Images = slices.DeleteFunc(slices.Clone(e.Images), func(id string) bool { return !slices.Contains(live, id) })
			out = append(out, e)
		}
	}
	return out, nil
}

// takeSizes is [width, height] of each of ids that is the owner's ready, undeleted take with a size (explicit owner
// check: a batch's runs are made as System).
func takeSizes(ctx context.Context, tx pgx.Tx, ownerID string, ids []string) (map[string][2]int, error) {
	out := map[string][2]int{}
	if len(ids) == 0 {
		return out, nil
	}
	type sized struct {
		ID            string
		Width, Height int
	}
	rows, err := collect[sized](ctx, tx, `SELECT id::text, width, height FROM takes WHERE id = ANY($1::uuid[])
		AND owner_id = $2 AND deleted_at IS NULL AND ready AND width IS NOT NULL AND height IS NOT NULL`, ids, ownerID)
	for _, r := range rows {
		out[r.ID] = [2]int{r.Width, r.Height}
	}
	return out, err
}

// mentionsJSON is runs.mentions: what the expansion did, null when it changed nothing.
func mentionsJSON(exp expansion) []byte {
	if len(exp.Elements) == 0 && len(exp.Typed) == 0 {
		return nil
	}
	data, _ := json.Marshal(exp)
	return data
}

// typedView lays a run's typed values (runs.mentions) over its stored graph: the graph as the user set it, which
// lineage reads (element images are never parents).
func typedView(graph, mentions []byte) []byte {
	var exp expansion
	if len(mentions) == 0 || json.Unmarshal(mentions, &exp) != nil || len(exp.Typed) == 0 {
		return graph
	}
	var nodes map[string]registry.Node
	if json.Unmarshal(graph, &nodes) != nil {
		return graph
	}
	for path, value := range exp.Typed {
		id, name, _ := strings.Cut(path, ".")
		if node, ok := nodes[id]; ok && node.Params != nil {
			node.Params[name] = value
		}
	}
	out, _ := json.Marshal(nodes)
	return out
}

// joinRows is a batch's 422 detail of the rows that do not resolve: each "row <n>: …", at most 10, then the rest
// counted.
func joinRows(failed []string) string {
	if len(failed) <= 10 {
		return strings.Join(failed, "; ")
	}
	return strings.Join(failed[:10], "; ") + fmt.Sprintf("; and %d more rows", len(failed)-10)
}
