package gallery

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
)

// Import (contracts/README.md "Import", API 1.16): helm import brings an old helmstudio gallery into one user's. Each
// old item is a takes row of that user (source import), pending until its files are uploaded and checked, idempotent
// on (owner_id, import_key); lineage is stored by key (import_edges) and becomes take_parents once both ends are
// ready. Admin only; every write runs as the owner, so row-level security keeps it in their gallery.

// ImportLink is how long an import's PUT links last, and how long a pending import waits for its complete.
const ImportLink = 24 * time.Hour

var errAdmin = network.NewForbiddenError("admin role required", nil)

// importWait is what a pending import keeps until it is complete: its original time and the declared sizes.
type importWait struct {
	CreatedAt time.Time        `json:"created_at"`
	Width     int              `json:"width"`
	Height    int              `json:"height"`
	Video     *dto.ImportVideo `json:"video,omitempty"`
}

// posterKey is a video import's poster key.
func posterKey(key string, poster *dto.ImportImage) string {
	return strings.TrimSuffix(key, ".mp4") + ".poster." + dto.UploadTypes[poster.ContentType]
}

// CreateImport answers POST /admin/imports/takes: a new key is a pending take (201) with signed PUT links, a pending
// key gets fresh links, a ready (or deleted) key changes nothing but the lineage edges not stored yet.
func (s *Service) CreateImport(ctx context.Context, caller common.Caller, body *dto.ImportTakeCreate) (dto.ImportTake, bool, error) {
	if !caller.IsAdmin() {
		return dto.ImportTake{}, false, errAdmin
	}
	owner := common.Caller{ID: body.OwnerID}
	out := dto.ImportTake{Key: body.Key}
	created := false
	var key string
	var wait importWait
	var contentType string
	var size int64
	err := s.tx(ctx, owner, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('import:' || $1 || ':' || $2))`, body.OwnerID, body.Key); err != nil {
			return err
		}
		type found struct {
			ID, Key, ContentType string
			Bytes                int64
			Ready, Deleted       bool
			Wait                 json.RawMessage
		}
		rows, _ := tx.Query(ctx, `SELECT id::text, key, content_type, bytes, ready, deleted_at IS NOT NULL, import_wait
			FROM takes WHERE owner_id = $1 AND import_key = $2`, body.OwnerID, body.Key)
		existing, err := pgx.CollectRows(rows, pgx.RowToStructByPos[found])
		if err != nil {
			return err
		}
		if len(existing) == 1 {
			e := existing[0]
			out.TakeID, key, contentType, size = e.ID, e.Key, e.ContentType, e.Bytes
			_ = json.Unmarshal(e.Wait, &wait)
			if e.Ready || e.Deleted {
				out.State = "ready"
			}
		} else {
			created = true
			out.TakeID = utility.NewID()
			ext := dto.UploadTypes[body.ContentType]
			if body.Kind == "video" {
				ext = "mp4"
			}
			key = common.OwnerPrefix(body.OwnerID) + "imports/" + out.TakeID + "." + ext
			contentType, size = body.ContentType, body.Bytes
			wait = importWait{CreatedAt: *body.CreatedAt, Width: body.Width, Height: body.Height, Video: body.Video}
			legacy, _ := json.Marshal(map[string]any{"settings": body.Legacy, "imported_at": time.Now().UTC()})
			if _, err := tx.Exec(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes,
				starred, ready, import_key, legacy, import_wait) VALUES ($1, $2, $3, 'import', $4, $5, $6, $7, $8, false, $9, $10, $11)`,
				out.TakeID, body.OwnerID, body.Kind, body.Title, key, body.ContentType, body.Bytes, *body.Starred, body.Key,
				legacy, wait); err != nil {
				return err
			}
		}
		for i, parent := range body.Parents {
			if parent == body.Key {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO import_edges (owner_id, key, parent_key, idx) VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`, body.OwnerID, body.Key, parent, i); err != nil {
				return err
			}
		}
		return lineage(ctx, tx, body.OwnerID, body.Key)
	})
	if err != nil {
		return dto.ImportTake{}, false, err
	}
	if out.State == "ready" {
		return out, false, nil
	}
	out.State = "pending"
	expiry := time.Now().Add(ImportLink).UTC().Truncate(time.Second)
	link := s.signer.SignPut(key, size, contentType, expiry)
	out.UploadURL, out.ExpiresAt = &link, &expiry
	if wait.Video != nil && wait.Video.Poster != nil {
		poster := s.signer.SignPut(posterKey(key, wait.Video.Poster), wait.Video.Poster.Bytes, wait.Video.Poster.ContentType, expiry)
		out.PosterUploadURL = &poster
	}
	return out, created, nil
}

// lineage adds the take_parents rows of key's import edges (as child or parent) whose both ends are ready.
func lineage(ctx context.Context, tx pgx.Tx, owner, key string) error {
	_, err := tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx)
		SELECT c.id, p.id, e.owner_id, e.idx FROM import_edges e
		JOIN takes c ON c.owner_id = e.owner_id AND c.import_key = e.key AND c.ready AND c.deleted_at IS NULL
		JOIN takes p ON p.owner_id = e.owner_id AND p.import_key = e.parent_key AND p.ready AND p.deleted_at IS NULL
		WHERE e.owner_id = $1 AND (e.key = $2 OR e.parent_key = $2) ON CONFLICT DO NOTHING`, owner, key)
	return err
}

// CompleteImport answers POST /admin/imports/takes/{take_id}/complete: the uploads checked (size, header, pixel size
// as declared; a video's MP4 ftyp box and its poster), the take shown at its original time. A failed check deletes
// the pending take (its key freed for a later run) and is a 422; a missing object is a 409.
func (s *Service) CompleteImport(ctx context.Context, caller common.Caller, id string) (dto.Take, error) {
	if !caller.IsAdmin() {
		return dto.Take{}, errAdmin
	}
	if !utility.IsUUID(id) {
		return dto.Take{}, errNotFound
	}
	var owner string
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT owner_id::text FROM takes WHERE id = $1 AND source = 'import' AND deleted_at IS NULL`,
			id).Scan(&owner)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dto.Take{}, errNotFound
	} else if err != nil {
		return dto.Take{}, err
	}
	as := common.Caller{ID: owner}
	var row model.Take
	var wait importWait
	err = s.tx(ctx, as, func(tx pgx.Tx) error {
		var raw json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT import_wait FROM takes WHERE id = $1`, id).Scan(&raw); err != nil {
			return err
		}
		_ = json.Unmarshal(raw, &wait)
		var err error
		row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND t.deleted_at IS NULL`, id)
		return err
	})
	if err != nil || row.Ready {
		return s.takeDTO(row), err
	}
	width, height, etag, detail, err := s.inspectImport(ctx, row, wait)
	if err != nil {
		return dto.Take{}, err
	}
	if detail != "" { // deleted with its objects (the sweep), the key freed so a later run creates it again
		err = s.tx(ctx, as, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE takes SET deleted_at = clock_timestamp(), import_key = NULL WHERE id = $1 AND NOT ready`, id)
			return err
		})
		if err == nil {
			err = network.NewUnprocessableError(detail, nil)
		}
		return dto.Take{}, err
	}
	var video any
	if v := wait.Video; v != nil {
		video = dto.Video{DurationS: v.DurationS, FPS: v.FPS, Frames: v.Frames, HasAudio: v.HasAudio,
			Poster: dto.Poster{Key: posterKey(row.Key, v.Poster), Width: v.Poster.Width, Height: v.Poster.Height, Bytes: v.Poster.Bytes}}
	}
	err = s.tx(ctx, as, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `UPDATE takes t SET ready = true, width = $2, height = $3, etag = nullif($4, ''),
			created_at = $5, video = $6, import_wait = NULL WHERE t.id = $1 AND NOT t.ready AND t.deleted_at IS NULL
			RETURNING `+model.TakeCols, id, width, height, etag, wait.CreatedAt, video)
		if errors.Is(err, errNotFound) { // completed by a call that ran alongside
			row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
			return err
		}
		if err == nil {
			var key string
			if err = tx.QueryRow(ctx, `SELECT import_key FROM takes WHERE id = $1`, id).Scan(&key); err == nil {
				err = lineage(ctx, tx, owner, key)
			}
		}
		if err == nil { // its parents as stored now
			row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1`, id)
		}
		return err
	})
	return s.takeDTO(row), err
}

// inspectImport checks an import's objects: the pixel size and, for an image, its md5 (the etag), or the 422 detail.
func (s *Service) inspectImport(ctx context.Context, row model.Take, wait importWait) (int, int, string, string, error) {
	head, etag, detail, err := s.readHead(ctx, row.Key, row.Bytes, row.Kind == "image")
	if err != nil || detail != "" {
		return 0, 0, "", detail, err
	}
	if row.Kind == "video" {
		if len(head) < 8 || string(head[4:8]) != "ftyp" {
			return 0, 0, "", "not an MP4 file", nil
		}
		poster := wait.Video.Poster
		posterHead, _, detail, err := s.readHead(ctx, posterKey(row.Key, poster), poster.Bytes, false)
		if err != nil || detail != "" {
			return 0, 0, "", detail, err
		}
		if detail := checkImport(posterHead, poster.ContentType, poster.Width, poster.Height); detail != "" {
			return 0, 0, "", detail, nil
		}
		return wait.Width, wait.Height, "", "", nil
	}
	if detail := checkImport(head, row.ContentType, wait.Width, wait.Height); detail != "" {
		return 0, 0, "", detail, nil
	}
	return wait.Width, wait.Height, etag, "", nil
}

// checkImport checks an image header against the declared type and size.
func checkImport(head []byte, contentType string, width, height int) string {
	w, h, detail := checkImage(head, contentType)
	switch {
	case strings.HasPrefix(detail, "not a valid"):
		return "not a png, jpeg or webp image"
	case detail != "":
		return detail
	case w != width || h != height:
		return "width and height differ from the file"
	}
	return ""
}

// readHead stats key (missing: 409; another size: the 422 detail) and reads its first headBytes; hash: the md5 of
// the whole object too.
func (s *Service) readHead(ctx context.Context, key string, size int64, hash bool) ([]byte, string, string, error) {
	unreachable := func(err error) error {
		return network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	info, err := s.store.Stat(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, "", "", network.NewConflictError("upload not received", nil)
	} else if err != nil {
		return nil, "", "", unreachable(err)
	}
	if info.Size != size {
		return nil, "", "size differs from the declared bytes", nil
	}
	body, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, "", "", unreachable(err)
	}
	defer body.Close()
	sum := md5.New()
	var reader io.Reader = body
	if hash {
		reader = io.TeeReader(body, sum)
	}
	head, err := io.ReadAll(io.LimitReader(reader, headBytes))
	if err == nil && hash {
		_, err = io.Copy(sum, body)
	}
	if err != nil {
		return nil, "", "", unreachable(err)
	}
	etag := ""
	if hash {
		etag = hex.EncodeToString(sum.Sum(nil))
	}
	return bytes.Clone(head), etag, "", nil
}
