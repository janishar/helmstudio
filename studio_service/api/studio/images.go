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
func checkImages(ctx context.Context, tx pgx.Tx, store storage.Store, ownerID string, nodes map[string]registry.Node) (string, error) {
	refs := images(nodes)
	problems, err := vetTakes(ctx, tx, store, ownerID, refs, registry.IsText(nodes))
	return firstBad(refs, problems), err
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

// resolveImages returns run's graph with every {take_id} replaced by the take's object descriptor (defs image), read
// as System with an explicit owner check. gone names the first take that is no longer usable (deleted since the run
// was created): the run must fail rather than send a dead key.
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
	type descriptor struct {
		TakeID      string `json:"take_id"`
		Key         string `json:"key"`
		ContentType string `json:"content_type"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Bytes       int64  `json:"bytes"`
		Etag        string `json:"etag"`
	}
	rows, err := collect[descriptor](ctx, tx, `SELECT id::text, key, content_type, width, height, bytes, etag FROM takes
		WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND deleted_at IS NULL AND ready AND (kind = 'image' OR $3 AND kind = 'video')
		AND width IS NOT NULL AND height IS NOT NULL AND etag IS NOT NULL`, ids, ownerID, registry.IsText(nodes))
	if err != nil {
		return nil, "", err
	}
	byID := map[string]descriptor{}
	for _, row := range rows {
		byID[row.TakeID] = row
	}
	gone := ""
	eachImage(nodes, func(at imageAt) any {
		row, ok := byID[at.TakeID]
		if !ok && gone == "" {
			gone = at.TakeID
		}
		return row
	})
	return nodes, gone, nil
}
