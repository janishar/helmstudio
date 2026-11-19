package studio

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/registry"
)

// Image inputs (contracts/README.md "Image inputs"): a run names takes by id; studio checks them when it creates the
// run and resolves them into object descriptors when it releases it.

// checkImages returns the 422 detail of the first Image value of nodes whose take is not the owner's completed,
// undeleted image with its object in the store ("encode.refs[2].image: take <id> not found"), "" when all are good.
// A text (ask) workflow's takes may also be videos (its Media rows; API 1.14).
// From API 1.19 it also checks and snaps the graph's media and span values (checkMedia; schemas: the workflow's).
func checkImages(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, nodes map[string]registry.Node,
	schemas map[string]registry.NodeSchema) (string, error) {
	media := mediaParams(nodes, schemas)
	refs := slices.DeleteFunc(images(nodes), func(at imageAt) bool { _, ok := media[at.Path]; return ok })
	problems, err := vetTakes(ctx, tx, store, ownerID, refs, registry.IsText(nodes))
	if detail := firstBad(refs, problems); detail != "" || err != nil {
		return detail, err
	}
	return checkMedia(ctx, tx, store, ownerID, nodes, schemas)
}

// firstBad is the detail of the first ref whose take has a problem.
func firstBad(refs []imageAt, problems map[string]string) string {
	for _, ref := range refs {
		if problem := problems[ref.TakeID]; problem != "" {
			return ref.Path + ": take " + ref.TakeID + " " + problem
		}
	}
	return ""
}

// vetTakes checks each referenced take once, with an explicit owner check (the caller's transaction or System): the
// problem per take id ("not found": unknown, another owner's, deleted, pending or its object missing or of another
// size; "is not an image"; "has no size": release needs width and height), none for a good one. The objects are
// HEADed statHeads at a time (a batch can name hundreds of takes). A take without a content hash gets one now (the
// lazy backfill of run outputs recorded before evt.run.output carried an etag: uploads get theirs at complete).
// ponytail: the backfill reads whole objects one at a time inside the transaction; only old takes need it.
// video allows video takes too (a text workflow's Media rows). ponytail: per run, not per field; an ask workflow's only
// takes are its Media rows.
func vetTakes(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, refs []imageAt, video bool) (map[string]string, error) {
	problems := map[string]string{}
	var ids []string
	for _, ref := range refs {
		if !slices.Contains(ids, ref.TakeID) {
			ids = append(ids, ref.TakeID)
			problems[ref.TakeID] = "not found"
		}
	}
	if len(ids) == 0 {
		return problems, nil
	}
	type found struct {
		ID, Kind, Key string
		Bytes         int64
		Etag          *string
		Width, Height *int
	}
	takes, err := collect[found](ctx, tx, `SELECT id::text, kind, key, bytes, etag, width, height FROM takes
		WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND deleted_at IS NULL AND ready`, ids, ownerID)
	if err != nil {
		return nil, err
	}
	var usable []found
	for _, take := range takes {
		if take.Kind != "image" && !(video && take.Kind == "video") {
			problems[take.ID] = "is not an image"
		} else if take.Width == nil || take.Height == nil {
			problems[take.ID] = "has no size"
		} else {
			usable = append(usable, take)
		}
	}
	there, err := present(ctx, store, len(usable), func(i int) (string, int64) { return usable[i].Key, usable[i].Bytes })
	if err != nil {
		return nil, err
	}
	for i, take := range usable {
		if !there[i] {
			continue // not found
		}
		if take.Etag == nil {
			etag, err := contentHash(ctx, store, take.Key)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			} else if err != nil {
				return nil, network.NewServiceUnavailableError("could not read a reference image; try again", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE takes SET etag = $2 WHERE id = $1 AND etag IS NULL`, take.ID, etag); err != nil {
				return nil, err
			}
		}
		delete(problems, take.ID)
	}
	return problems, nil
}

// statHeads is how many objects vetTakes HEADs at once.
const statHeads = 16

// present HEADs n objects (key and expected size of each), statHeads at a time: whether each is there with that size.
func present(ctx context.Context, store storage.Store, n int, object func(int) (string, int64)) ([]bool, error) {
	there, errs := make([]bool, n), make([]error, n)
	var wg sync.WaitGroup
	slots := make(chan struct{}, statHeads)
	for i := range n {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			key, size := object(i)
			info, err := store.Stat(ctx, key)
			if err == nil {
				there[i] = info.Size == size
			} else if !errors.Is(err, storage.ErrNotFound) {
				errs[i] = err
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	return there, nil
}

// contentHash is the md5 hex of an object: the S3 ETag of a single-part PUT.
func contentHash(ctx context.Context, store storage.Store, key string) (string, error) {
	body, err := store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer body.Close()
	hash := md5.New()
	if _, err := io.Copy(hash, body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// resolveImages returns run's graph with every {take_id} replaced by the take's object descriptor (defs image; from
// API 1.19 a video or audio take of a media param: defs media), read as System with an explicit owner check. A media
// value is told by its take: create let a video or audio take only into a media param (or a text workflow's Media
// rows, which stay images). gone names the first take that is no longer usable (deleted since the run was created):
// the run must fail rather than send a dead key.
func resolveImages(ctx context.Context, tx pgx.Tx, ownerID string, graph []byte) (map[string]registry.Node, string, error) {
	var nodes map[string]registry.Node
	if err := json.Unmarshal(graph, &nodes); err != nil {
		return nil, "", fmt.Errorf("graph: %w", err)
	}
	refs := images(nodes)
	if len(refs) == 0 {
		return nodes, "", nil
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.TakeID
	}
	rows, err := inputTakes(ctx, tx, ownerID, ids)
	if err != nil {
		return nil, "", err
	}
	text := registry.IsText(nodes)
	gone := ""
	eachImage(nodes, func(at imageAt) any {
		row, ok := rows[at.TakeID]
		media := ok && row.Kind != "image" && !text && !strings.Contains(at.Path, "[")
		image := ok && (row.Kind == "image" || (text && row.Kind == "video")) && row.Width != nil && row.Height != nil
		switch {
		case ok && row.Etag != nil && media:
			return mediaDescriptor(row, at.Value)
		case ok && row.Etag != nil && image:
			return map[string]any{"take_id": row.ID, "key": row.Key, "content_type": row.ContentType, "width": *row.Width,
				"height": *row.Height, "bytes": row.Bytes, "etag": *row.Etag}
		}
		if gone == "" {
			gone = at.TakeID
		}
		return nil
	})
	return nodes, gone, nil
}
