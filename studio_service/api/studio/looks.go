package studio

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/helmstudio/studio_service/registry"
)

// Looks (contracts/README.md "Looks"): library items of kind look, chosen (RunRequest.looks), never typed. The one
// resolver (expandMentions) applies them with the mentions; these are the pieces it and the fit rule share.

// lookLora is one LoRA of a look as the resolver reads it: its id (null: a starter file no live host has), its name and
// the strength the look applies it at.
type lookLora struct {
	LoraID   *string `json:"lora_id"`
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
}

// lookItem is a look as the resolver reads it (deleted takes left out); a batch keeps these as its copy.
type lookItem struct {
	ID      string     `json:"id"`
	Source  string     `json:"source"` // mine | starter
	Name    string     `json:"name"`
	Version int        `json:"version"`
	Text    string     `json:"text"`
	Images  []string   `json:"images"`
	Loras   []lookLora `json:"loras"`
}

// needs is LibraryItem.needs: text (non-empty), images (at least one), lora (at least one), in that order.
func needs(text string, images, loras int) []string {
	out := []string{}
	if text != "" {
		out = append(out, "text")
	}
	if images > 0 {
		out = append(out, "images")
	}
	if loras > 0 {
		out = append(out, "lora")
	}
	return out
}

// lookStack is where look LoRAs go: among the known, not bypassed <family>/lora-stack nodes with an unwired rows param
// that has a Lora field, the first by node id from which no other such node is reachable downstream.
func lookStack(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) (string, registry.Param, bool) {
	cands := map[string]registry.Param{}
	for id, node := range nodes {
		schema, known := schemas[node.Kind]
		if !known || node.Bypass || !strings.HasSuffix(node.Kind, "/lora-stack") {
			continue
		}
		for _, param := range schema.Params {
			if _, wired := node.In[param.Name]; param.LoraField() != "" && !wired {
				cands[id] = param
				break
			}
		}
	}
	succ := map[string][]string{} // node → the nodes wired from it
	for id, node := range nodes {
		for _, ref := range node.In {
			src, _, _ := strings.Cut(ref, ".")
			succ[src] = append(succ[src], id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(cands)) {
		seen, todo, last := map[string]bool{}, slices.Clone(succ[id]), true
		for len(todo) > 0 && last {
			next := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			if seen[next] {
				continue
			}
			seen[next] = true
			if _, stack := cands[next]; stack {
				last = false
			}
			todo = append(todo, succ[next]...)
		}
		if last {
			return id, cands[id], true
		}
	}
	return "", registry.Param{}, false
}

// lookFit is why an item cannot be applied to a graph (README, Looks: fit), "" when it can. kind is element or look;
// loras is LoRA id → detected family of every LoRA the caller can use now in the workflow's env.
func lookFit(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, kind string, item lookItem,
	loras map[string]string) string {
	need := needs(item.Text, len(item.Images), len(item.Loras))
	if workflowMedia(nodes, schemas) == "audio" { // API 1.30: spoken text is never expanded (README, Voices)
		if kind == "element" {
			return "mentions are not read aloud; use a dialogue script"
		}
		return "looks do not apply to speech"
	}
	if (slices.Contains(need, "text") || kind == "element") && len(prompts(nodes, schemas)) == 0 {
		return "this workflow has no prompt"
	}
	if kind == "element" {
		return ""
	}
	if _, _, _, ok := referenceRows(nodes, schemas); slices.Contains(need, "images") && !ok {
		return "this workflow takes no reference images"
	}
	if !slices.Contains(need, "lora") {
		return ""
	}
	stack, _, ok := lookStack(nodes, schemas)
	if !ok {
		return "this workflow has no LoRA stack"
	}
	for _, l := range item.Loras {
		if l.LoraID == nil {
			return "LoRA " + l.Name + " is on no live host"
		}
		family, usable := loras[*l.LoraID]
		switch {
		case !usable:
			return "LoRA " + l.Name + " is not available"
		case family == "unknown":
			return "LoRA " + l.Name + " fits no known model"
		case family != registry.Family(nodes[stack].Kind):
			return "LoRA " + l.Name + " is for " + family
		}
	}
	return ""
}

// strengthRange is the min and max of a rows item's strength field (nil when the field or bound is absent).
func strengthRange(param registry.Param) (lo, hi *float64, has bool) {
	raw, ok := param.Item["strength"]
	if !ok {
		return nil, nil, false
	}
	var spec struct {
		Min *float64 `json:"min"`
		Max *float64 `json:"max"`
	}
	_ = json.Unmarshal(raw, &spec)
	return spec.Min, spec.Max, true
}

// lookLoraIDs are the distinct non-null LoRA ids of looks.
func lookLoraIDs(looks []lookItem) []string {
	var out []string
	for _, l := range looks {
		for _, x := range l.Loras {
			if x.LoraID != nil && !slices.Contains(out, *x.LoraID) {
				out = append(out, *x.LoraID)
			}
		}
	}
	return out
}
