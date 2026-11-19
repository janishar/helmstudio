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

// checkMedia checks every media and span value of a resolved graph as the owner (contracts/README.md, Media inputs)
// and snaps them in place: the 422 detail of the first bad one in key order, "" when all are good. A media take must
// be the owner's completed, undeleted take with its object present (else "take <id> not found", whoever's it is), fit
// its param (media_fit), and a trim part lie inside it within max_s; a span needs the workflow's one video source.
func checkMedia(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, nodes map[string]registry.Node,
	schemas map[string]registry.NodeSchema) (string, error) {
	media, spans := mediaParams(nodes, schemas), paramsOf(nodes, schemas, "span")
	if len(media) == 0 && len(spans) == 0 {
		return "", nil
	}
	value := func(path string) map[string]any {
		id, name, _ := strings.Cut(path, ".")
		v, _ := nodes[id].Params[name].(map[string]any)
		return v
	}
	set := func(path string, v any) {
		id, name, _ := strings.Cut(path, ".")
		nodes[id].Params[name] = v
	}
	var ids []string
	for path := range media {
		if id, ok := value(path)["take_id"].(string); ok {
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
	for i, row := range found {
		if !there[i] {
			delete(rows, row.ID)
		} else if row.Etag == nil { // a run output recorded without its etag: hashed on first use (as images)
			etag, err := contentHash(ctx, store, row.Key)
			if errors.Is(err, storage.ErrNotFound) {
				delete(rows, row.ID)
				continue
			} else if err != nil {
				return "", network.NewServiceUnavailableError("could not read an input take; try again", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE takes SET etag = $2 WHERE id = $1 AND etag IS NULL`, row.ID, etag); err != nil {
				return "", err
			}
		}
	}
	var source *gallery.MediaTake // the one media param that accepts video: the spans' source
	sources := 0
	for _, path := range slices.Sorted(maps.Keys(media)) {
		p, v := media[path], value(path)
		if v == nil {
			continue // resolve said so
		}
		id, _ := v["take_id"].(string)
		row, ok := rows[id]
		if !ok {
			return path + ": take " + id + " not found", nil
		}
		take := gallery.Facts(row.ID, row.Kind, row.Width, row.Height, row.Video, row.Audio)
		if detail := gallery.MediaFit(p, take); detail != "" {
			return path + ": " + detail, nil
		}
		if slices.Contains(p.Accepts, "video") {
			sources++
			source = &take
		}
		if !p.Trim {
			continue
		}
		snap, d := snapper(take), take.DurationS
		in, out := 0.0, d
		if p.MaxS > 0 {
			out = min(d, p.MaxS)
		}
		if x, ok := v["in"].(float64); ok {
			in = x
		}
		if x, ok := v["out"].(float64); ok {
			out = x
		}
		in, out = snap(in), snap(out)
		if out > snap(d)+1e-9 {
			return fmt.Sprintf("%s: out %.1f is past the take's end %.1f", path, out, d), nil
		}
		if out > d { // rounded up past an off-grid end: the last grid point at or before it (the runner's bound)
			u := gridUnit(take)
			out = math.Floor(d*u+1e-9) / u
		}
		switch {
		case in >= out:
			return path + ": in must be before out", nil
		case p.MaxS > 0 && out-in > p.MaxS+1e-9:
			return fmt.Sprintf("%s: the part lasts %.1f s; this input takes at most %.1f s", path, out-in, p.MaxS), nil
		}
		set(path, map[string]any{"take_id": id, "in": in, "out": out})
	}
	for _, path := range slices.Sorted(maps.Keys(spans)) {
		v := value(path)
		if v == nil {
			continue
		}
		if sources != 1 {
			return path + ": this workflow has no single video source", nil
		}
		snap := snapper(*source)
		start, end := snap(v["start"].(float64)), snap(v["end"].(float64))
		switch {
		case start >= end:
			return path + ": start must be before end", nil
		case end > snap(source.DurationS)+1e-9:
			return fmt.Sprintf("%s: end %.1f is past the clip's end %.1f", path, end, source.DurationS), nil
		}
		set(path, map[string]any{"start": start, "end": end})
	}
	return "", nil
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
