package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/registry"
)

// LoRA values (contracts/README.md "LoRAs"): a run names LoRAs by id in a lora-stack node's rows; studio checks them as
// the owner when it makes the run (runGraphs), keeps them resolved on the run (runs.loras) and turns them into the
// command's lora values when it releases it.

// loraAt is one Lora value of a graph: where it is ("lora.rows[2].lora", items from 1), the node, its row and item.
type loraAt struct {
	Path, Node, ID string
	Row            int
	Item           map[string]any
}

// eachLora lists the Lora values of nodes in input order (node id, param name, item), by their kinds' schemas.
func eachLora(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []loraAt {
	var out []loraAt
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		for _, param := range schemas[node.Kind].Params {
			field := param.LoraField()
			items, _ := node.Params[param.Name].([]any)
			if field == "" {
				continue
			}
			for i, item := range items {
				fields, _ := item.(map[string]any)
				ref, _ := fields[field].(map[string]any)
				if loraID, ok := ref["lora_id"].(string); ok {
					out = append(out, loraAt{Path: fmt.Sprintf("%s.%s[%d].%s", id, param.Name, i+1, field), Node: id,
						ID: loraID, Row: i + 1, Item: fields})
				}
			}
		}
	}
	return out
}

// loraIDs are the distinct ids of values.
func loraIDs(values []loraAt) []string {
	var ids []string
	for _, v := range values {
		if !slices.Contains(ids, v.ID) {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// checkLoras resolves a graph's Lora values against what the owner can use (usable): each must be usable, of its
// node's family and not already in an earlier row of the same node. It returns them as the run keeps them, or the 422
// detail of the first bad one.
func checkLoras(values []loraAt, kinds map[string]string, usable map[string]lora.Info) ([]lora.Used, string) {
	var out []lora.Used
	for i, v := range values {
		info, ok := usable[v.ID]
		family := registry.Family(kinds[v.Node])
		switch {
		case !ok:
			return nil, v.Path + ": LoRA " + v.ID + " not found"
		case info.Family == "unknown":
			return nil, v.Path + ": LoRA " + info.Name + " does not fit " + family + " (its model is unknown)"
		case info.Family != family:
			return nil, v.Path + ": LoRA " + info.Name + " does not fit " + family + " (it is for " + info.Family + ")"
		}
		if j := slices.IndexFunc(values[:i], func(e loraAt) bool { return e.Node == v.Node && e.ID == v.ID }); j >= 0 {
			return nil, fmt.Sprintf("%s: LoRA %s is already in row %d", v.Path, info.Name, values[j].Row)
		}
		strength, _ := v.Item["strength"].(float64)
		mode, _ := v.Item["mode"].(string)
		out = append(out, lora.Used{Node: v.Node, ID: v.ID, Source: info.Source, Name: info.Name, Strength: strength,
			Mode: mode, SHA256: info.SHA256, Bytes: info.Bytes})
	}
	return out, ""
}

// runLoras checks the Lora values of every graph without a detail yet (as owner, in env), filling Loras or the detail.
func runLoras(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID, env string, graphs []built,
	schemas map[string]registry.NodeSchema, details []string) error {
	values := make([][]loraAt, len(graphs))
	var all []loraAt
	for i := range graphs {
		if details[i] == "" {
			values[i] = eachLora(graphs[i].Nodes, schemas)
			all = append(all, values[i]...)
		}
	}
	if len(all) == 0 {
		return nil
	}
	usable, err := lora.Usable(ctx, tx, reg, ownerID, env, loraIDs(all))
	if err != nil {
		return err
	}
	for i := range graphs {
		if details[i] == "" && len(values[i]) > 0 {
			graphs[i].Loras, details[i] = checkLoras(values[i], nodeKinds(graphs[i].Nodes), usable)
		}
	}
	return nil
}

// nodeKinds maps each node id to its kind.
func nodeKinds(nodes map[string]registry.Node) map[string]string {
	out := map[string]string{}
	for id, node := range nodes {
		out[id] = node.Kind
	}
	return out
}

// lorasJSON is runs.loras: null without LoRAs.
func lorasJSON(used []lora.Used) []byte {
	if len(used) == 0 {
		return nil
	}
	data, _ := json.Marshal(used)
	return data
}

// runUsed reads runs.loras.
func runUsed(raw []byte) []lora.Used {
	var out []lora.Used
	_ = json.Unmarshal(raw, &out)
	return out
}

// resolveLoras puts each {lora_id} of a released run's graph in its command form (defs lora): shared {lora_id, source,
// name, sha256, bytes}, own the same with source own and its key under the run owner's prefix. The values come from
// what the run resolved when it was made; a value it did not resolve is left as it is (the runner fails it).
func resolveLoras(nodes map[string]registry.Node, used []lora.Used, ownerID string) {
	if len(used) == 0 {
		return
	}
	for id, node := range nodes {
		for _, value := range node.Params {
			items, _ := value.([]any)
			for _, item := range items {
				fields, _ := item.(map[string]any)
				for field, v := range fields {
					ref, _ := v.(map[string]any)
					loraID, ok := ref["lora_id"].(string)
					if !ok || len(ref) != 1 {
						continue
					}
					at := slices.IndexFunc(used, func(u lora.Used) bool { return u.Node == id && u.ID == loraID })
					if at < 0 {
						continue
					}
					u := used[at]
					resolved := map[string]any{"lora_id": u.ID, "source": "shared", "name": u.Name, "sha256": u.SHA256,
						"bytes": u.Bytes}
					if u.Source == "mine" {
						resolved["source"], resolved["key"] = "own", lora.Key(ownerID, u.ID)
					}
					fields[field] = resolved
				}
			}
		}
	}
}

// baked is a run's bake rows as (id, strength) in graph order (a stack after the stacks upstream of it, then row
// order), one LoRA twice once with the summed strength: what a resident component must have baked in, as the runner
// keys it.
func baked(used []lora.Used, graph map[string]registry.Node) []registry.ResidentLora {
	depth := map[string]int{} // a node's longest path from a source: upstream stacks come first
	var walk func(id string) int
	walk = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		depth[id] = 0 // a cycle (never in a validated graph) ends here
		d := 0
		for _, ref := range graph[id].In {
			src, _, _ := strings.Cut(ref, ".")
			if _, ok := graph[src]; ok {
				d = max(d, walk(src)+1)
			}
		}
		depth[id] = d
		return d
	}
	rows := slices.Clone(used) // node id order, then row order
	slices.SortStableFunc(rows, func(a, b lora.Used) int { return walk(a.Node) - walk(b.Node) })
	var out []registry.ResidentLora
	for _, u := range rows {
		if u.Mode != "bake" {
			continue
		}
		if at := slices.IndexFunc(out, func(r registry.ResidentLora) bool { return *r.LoraID == u.ID }); at >= 0 {
			*out[at].Strength += u.Strength
			continue
		}
		id, strength := u.ID, u.Strength
		out = append(out, registry.ResidentLora{LoraID: &id, Strength: &strength})
	}
	return out
}

// sameBaked reports whether a resident entry's baked LoRAs are want, in order (by id and strength).
func sameBaked(have, want []registry.ResidentLora) bool {
	return slices.EqualFunc(have, want, func(a, b registry.ResidentLora) bool {
		return a.LoraID != nil && b.LoraID != nil && *a.LoraID == *b.LoraID && a.Strength != nil && b.Strength != nil &&
			*a.Strength == *b.Strength
	})
}
