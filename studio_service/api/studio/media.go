package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/registry"
)

// Media inputs (contracts/README.md "Media inputs", API 1.19): a run names a video or audio take by id in a media
// param ({take_id, in?, out?}) and a range on its one video source in a span param ({start, end}). Studio checks the
// take as the caller when it creates the run (media_fit), snaps and fills in/out and the span to the take's frames
// (audio: whole milliseconds), and resolves the take into defs media when it releases the run.

// paramsOf lists the graph's unwired params of type typ on known, not bypassed nodes: "<node>.<param>" → its schema.
func paramsOf(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, typ string) map[string]registry.Param {
	out := map[string]registry.Param{}
	for id, node := range nodes {
		if node.Bypass {
			continue
		}
		for _, p := range schemas[node.Kind].Params {
			if _, wired := node.In[p.Name]; p.Type == typ && !wired {
				out[id+"."+p.Name] = p
			}
		}
	}
	return out
}

// mediaParams lists the graph's media params.
func mediaParams(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) map[string]registry.Param {
	return paramsOf(nodes, schemas, "media")
}

// inputTake is a take as a media value reads it.
type inputTake struct {
	ID, Kind, Key, ContentType string
	Bytes                      int64
	Etag                       *string
	Width, Height              *int
	Video, Audio               json.RawMessage
}

// inputTakes reads the owner's ready, undeleted takes among ids (an explicit owner check: the caller's transaction or
// System).
func inputTakes(ctx context.Context, tx pgx.Tx, ownerID string, ids []string) (map[string]inputTake, error) {
	rows, err := collect[inputTake](ctx, tx, `SELECT id::text, kind, key, content_type, bytes, etag, width, height, video, audio
		FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND deleted_at IS NULL AND ready`, uuids(ids), ownerID)
	out := map[string]inputTake{}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, err
}

// gridUnit is a take's grid per second: a video's frames, audio whole milliseconds.
func gridUnit(t gallery.MediaTake) float64 {
	if t.Kind == "video" && t.FPS > 0 {
		return t.FPS
	}
	return 1000
}

// snapper rounds seconds to a take's grid (rnd: half away from zero).
func snapper(t gallery.MediaTake) func(float64) float64 {
	unit := gridUnit(t)
	return func(s float64) float64 { return math.Round(s*unit) / unit }
}

// mediaSlot is one media value of a graph: a media param's ("<node>.<param>") or, API 1.22, a rows item's media field
// ("<node>.<param>[<i>].<field>", items from 1; Rows names the rows param, Total its max_total_s).
type mediaSlot struct {
	Path, Rows string
	Param      registry.Param
	Total      float64
	Value      map[string]any
	set        func(map[string]any)
}

// mediaSlots lists a graph's media values (null ones left out) in input order: node id, then schema order, then item;
// unwired params of known, not bypassed nodes.
func mediaSlots(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []mediaSlot {
	var out []mediaSlot
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		if node.Bypass {
			continue
		}
		for _, p := range schemas[node.Kind].Params {
			if _, wired := node.In[p.Name]; wired {
				continue
			}
			path, params := id+"."+p.Name, node.Params
			if v, ok := params[p.Name].(map[string]any); ok && p.Type == "media" {
				out = append(out, mediaSlot{Path: path, Param: p, Value: v, set: func(x map[string]any) { params[p.Name] = x }})
			}
			field, spec, ok := p.MediaItem()
			items, _ := params[p.Name].([]any)
			for i, item := range items {
				fields, _ := item.(map[string]any)
				if v, isRef := fields[field].(map[string]any); ok && isRef {
					out = append(out, mediaSlot{Path: fmt.Sprintf("%s[%d].%s", path, i+1, field), Rows: path, Param: spec,
						Total: p.MaxTotalS, Value: v, set: func(x map[string]any) { fields[field] = x }})
				}
			}
		}
	}
	return out
}

// checkMedia checks every media and span value of a resolved graph as the owner (contracts/README.md, Media inputs)
// and snaps them in place: the 422 detail of the first bad one, "" when all are good. A media take must be the owner's
// completed, undeleted take with its object present (else "take <id> not found", whoever's it is); mediaRules does the
// rest.
func checkMedia(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, nodes map[string]registry.Node,
	schemas map[string]registry.NodeSchema) (string, error) {
	slots := mediaSlots(nodes, schemas)
	if len(slots) == 0 && len(paramsOf(nodes, schemas, "span")) == 0 {
		return "", nil
	}
	var ids []string
	for _, slot := range slots {
		if id, ok := slot.Value["take_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	rows, err := inputTakes(ctx, tx, ownerID, ids)
	if err != nil {
		return "", err
	}
	found := slices.Collect(maps.Values(rows))
	there, err := present(ctx, store, len(found), func(i int) (string, int64) { return found[i].Key, found[i].Bytes })
	if err != nil {
		return "", err
	}
	takes := map[string]gallery.MediaTake{}
	for i, row := range found {
		if !there[i] {
			continue
		} else if row.Etag == nil { // a run output recorded without its etag: hashed on first use (as images)
			etag, err := contentHash(ctx, store, row.Key)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			} else if err != nil {
				return "", network.NewServiceUnavailableError("could not read an input take; try again", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE takes SET etag = $2 WHERE id = $1 AND etag IS NULL`, row.ID, etag); err != nil {
				return "", err
			}
		}
		takes[row.ID] = gallery.Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)
	}
	return mediaRules(nodes, schemas, takes), nil
}

// mediaRules checks every media and span value of a resolved graph against takes (the owner's usable takes by id) and
// snaps them in place: the 422 detail of the first bad one, "" when all are good. Each value fits its param
// (media_fit) and a trim part lies inside the take within min_s..max_s (in 0, out min(d, max_s) when left out); a rows
// param's items together keep its max_total_s (API 1.22); a span needs the workflow's one video source (a media
// param, never a rows item).
func mediaRules(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, takes map[string]gallery.MediaTake) string {
	slots := mediaSlots(nodes, schemas)
	var source *gallery.MediaTake // the one media param that accepts video: the spans' source
	sources, total := 0, 0.0
	for i, slot := range slots {
		id, _ := slot.Value["take_id"].(string)
		take, ok := takes[id]
		if !ok {
			return slot.Path + ": take " + id + " not found"
		}
		if detail := gallery.MediaFit(slot.Param, take); detail != "" {
			return slot.Path + ": " + detail
		}
		if slot.Rows == "" && slices.Contains(slot.Param.Accepts, "video") {
			sources++
			source = &take
		}
		value, detail := snapMedia(slot.Param, take, slot.Value)
		if detail != "" {
			return slot.Path + ": " + detail
		}
		slot.set(value)
		if slot.Rows == "" {
			continue
		}
		if out, trimmed := value["out"].(float64); trimmed {
			total += out - value["in"].(float64)
		} else {
			total += take.DurationS
		}
		if i+1 < len(slots) && slots[i+1].Rows == slot.Rows {
			continue // the rows param's last item adds up its items
		}
		if slot.Total > 0 && total > slot.Total+1e-9 {
			return fmt.Sprintf("%s: the takes last %.1f s in all; at most %.1f s", slot.Rows, total, slot.Total)
		}
		total = 0
	}
	for _, path := range slices.Sorted(maps.Keys(paramsOf(nodes, schemas, "span"))) {
		id, name, _ := strings.Cut(path, ".")
		v, _ := nodes[id].Params[name].(map[string]any)
		if v == nil {
			continue
		}
		if sources != 1 {
			return path + ": this workflow has no single video source"
		}
		snap := snapper(*source)
		start, end := snap(v["start"].(float64)), snap(v["end"].(float64))
		switch {
		case start >= end:
			return path + ": start must be before end"
		case end > snap(source.DurationS)+1e-9:
			return fmt.Sprintf("%s: end %.1f is past the clip's end %.1f", path, end, source.DurationS)
		}
		nodes[id].Params[name] = map[string]any{"start": start, "end": end}
	}
	return ""
}

// snapMedia is a trim value with in / out snapped to the take's grid and filled (in 0, out min(d, max_s)), or why the
// part does not do; a value without trim as it is.
func snapMedia(p registry.Param, take gallery.MediaTake, v map[string]any) (map[string]any, string) {
	if !p.Trim {
		return v, ""
	}
	snap, d := snapper(take), take.DurationS
	in, out := 0.0, d
	if p.MaxS > 0 {
		out = min(d, p.MaxS)
	}
	if x, ok := number(v["in"]); ok {
		in = x
	}
	if x, ok := number(v["out"]); ok {
		out = x
	}
	in, out = snap(in), snap(out)
	if out > snap(d)+1e-9 {
		return nil, fmt.Sprintf("out %.1f is past the take's end %.1f", out, d)
	}
	if out > d { // rounded up past an off-grid end: the last grid point at or before it (the runner's bound)
		u := gridUnit(take)
		out = math.Floor(d*u+1e-9) / u
	}
	switch {
	case in >= out:
		return nil, "in must be before out"
	case p.MaxS > 0 && out-in > p.MaxS+1e-9:
		return nil, fmt.Sprintf("the part lasts %.1f s; this input takes at most %.1f s", out-in, p.MaxS)
	case p.MinS > 0 && out-in < p.MinS-1e-9:
		return nil, fmt.Sprintf("the part lasts %.1f s; this input takes at least %.1f s", out-in, p.MinS)
	}
	return map[string]any{"take_id": v["take_id"], "in": in, "out": out}, ""
}

// mediaDescriptor is a media value resolved for cmd.run.execute (defs media): the take's object and the facts studio
// checked, with the snapped in/out a trim param stored.
func mediaDescriptor(row inputTake, value map[string]any) map[string]any {
	t := gallery.Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)
	out := map[string]any{"take_id": row.ID, "kind": row.Kind, "key": row.Key, "content_type": row.ContentType,
		"bytes": row.Bytes, "etag": deref(row.Etag), "duration_s": t.DurationS, "has_audio": t.HasAudio}
	if row.Kind == "video" {
		out["width"], out["height"], out["fps"], out["frames"] = t.Width, t.Height, t.FPS, t.Frames
	}
	for _, k := range []string{"in", "out"} {
		if x, ok := value[k]; ok {
			out[k] = x
		}
	}
	return out
}

// mediaAt is a resolved graph's largest media facts for its memory plan (runs.media_at; contracts/README.md, Media
// inputs: memory): a video source's megapixels and frames (a <family>/extend node adds its 8N frames), an audio part's
// frames at its node's fps (24 without one); nil without media values.
func mediaAt(ctx context.Context, tx pgx.Tx, ownerID string, nodes map[string]registry.Node,
	schemas map[string]registry.NodeSchema) ([]byte, error) {
	media := mediaParams(nodes, schemas)
	var ids []string
	for path := range media {
		id, name, _ := strings.Cut(path, ".")
		if v, ok := nodes[id].Params[name].(map[string]any); ok {
			ids = append(ids, fmt.Sprint(v["take_id"]))
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := inputTakes(ctx, tx, ownerID, ids)
	if err != nil {
		return nil, err
	}
	var at registry.PlannedAt
	for path, p := range media {
		id, name, _ := strings.Cut(path, ".")
		v, _ := nodes[id].Params[name].(map[string]any)
		row, ok := rows[fmt.Sprint(v["take_id"])]
		if !ok {
			continue
		}
		t := gallery.Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)
		if t.Kind == "video" && slices.Contains(p.Accepts, "video") {
			frames := t.Frames
			for _, n := range nodes {
				if seconds, ok := number(n.Params["seconds"]); ok && strings.HasSuffix(n.Kind, "/extend") {
					frames += 8 * max(1, int(math.Ceil(seconds*t.FPS/8-1e-9)))
				}
			}
			at.MP, at.Frames = max(at.MP, float64(t.Width*t.Height)/1e6), max(at.Frames, frames)
			continue
		}
		fps, ok := number(nodes[id].Params["fps"])
		if !ok {
			fps = 24
		}
		in, _ := number(v["in"])
		out, ok := number(v["out"])
		if !ok {
			out = t.DurationS
		}
		at.Frames = max(at.Frames, int(math.Floor((out-in)*fps+1e-6)))
	}
	return json.Marshal(at)
}
