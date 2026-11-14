package gallery

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg" // registers jpeg with image.DecodeConfig
	_ "image/png"  // registers png
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp" // registers webp

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
)

// MaxSide is the largest width or height an upload may have.
const MaxSide = 16384

// headBytes is how much of an upload is read to check its header. The contract names 64 KB; a JPEG's frame header can
// sit behind up to ~64 KB of EXIF plus ICC segments, so a little more is read.
const headBytes = 1 << 20

// formats maps an upload's content type to the name image.DecodeConfig reports.
var formats = map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/webp": "webp"}

// CreateUpload stores a pending take and signs the PUT link for its key u/<caller>/uploads/<take_id>.<ext>.
func (s *Service) CreateUpload(ctx context.Context, caller common.Caller, body *dto.UploadCreate) (dto.Upload, error) {
	id := utility.NewID()
	key := common.OwnerPrefix(caller.ID) + "uploads/" + id + "." + dto.UploadTypes[body.ContentType]
	var expiry time.Time
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready)
			VALUES ($1, $2, 'image', 'upload', $3, $4, $5, $6, false) RETURNING created_at`,
			id, caller.ID, body.Title(), key, body.ContentType, body.Bytes).Scan(&expiry)
		expiry = expiry.Add(UploadLink)
		return err
	})
	if err != nil {
		return dto.Upload{}, err
	}
	return dto.Upload{TakeID: id, UploadURL: s.signer.SignPut(key, body.Bytes, body.ContentType, expiry),
		ContentType: body.ContentType, Bytes: body.Bytes, ExpiresAt: expiry.UTC()}, nil
}

// CompleteUpload checks the uploaded object (size, a real image of the declared type, at most MaxSide a side) and shows
// the take. 409 while the object is not there; a failed check discards the upload (404 from then on) and is a 422.
// A take already completed is answered as it is.
func (s *Service) CompleteUpload(ctx context.Context, caller common.Caller, id string) (dto.Take, error) {
	if !utility.IsUUID(id) {
		return dto.Take{}, errNotFound
	}
	var row model.Take
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND t.source = 'upload'
			AND t.deleted_at IS NULL AND (t.ready OR t.created_at > clock_timestamp() - make_interval(secs => $2))`,
			id, UploadTTL.Seconds())
		return err
	})
	if err != nil || row.Ready {
		return s.takeDTO(row), err
	}
	width, height, detail, err := s.inspect(ctx, row)
	if err != nil {
		return dto.Take{}, err
	}
	if detail != "" { // discarded: the sweep removes the object
		err = s.tx(ctx, caller, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE takes SET deleted_at = clock_timestamp() WHERE id = $1 AND NOT ready`, id)
			return err
		})
		if err == nil {
			err = network.NewUnprocessableError(detail, nil)
		}
		return dto.Take{}, err
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `UPDATE takes t SET ready = true, width = $2, height = $3, created_at = clock_timestamp()
			WHERE t.id = $1 AND NOT t.ready AND t.deleted_at IS NULL RETURNING `+model.TakeCols, id, width, height)
		if errors.Is(err, errNotFound) { // completed (or deleted) by a call that ran alongside
			row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		}
		return err
	})
	return s.takeDTO(row), err
}

// inspect reads the object's size and header: the image's size, or the 422 detail of the failed check. A missing
// object is a 409.
func (s *Service) inspect(ctx context.Context, row model.Take) (int, int, string, error) {
	info, err := s.store.Stat(ctx, row.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, 0, "", network.NewConflictError("upload not received", nil)
	} else if err != nil {
		return 0, 0, "", network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	if info.Size != row.Bytes {
		return 0, 0, "size differs from the declared bytes", nil
	}
	body, err := s.store.Get(ctx, row.Key)
	if err != nil {
		return 0, 0, "", network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	defer body.Close()
	head, err := io.ReadAll(io.LimitReader(body, headBytes))
	if err != nil {
		return 0, 0, "", network.NewServiceUnavailableError("could not read the upload; try again", err)
	}
	width, height, detail := checkImage(head, row.ContentType)
	return width, height, detail, nil
}

// checkImage decodes an image header: its size, or why it is not a valid image of contentType at most MaxSide a side.
func checkImage(head []byte, contentType string) (int, int, string) {
	config, format, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil || format != formats[contentType] {
		return 0, 0, "not a valid " + formats[contentType] + " image"
	}
	if config.Width < 1 || config.Height < 1 {
		return 0, 0, "not a valid " + formats[contentType] + " image"
	}
	if config.Width > MaxSide || config.Height > MaxSide {
		return 0, 0, "image is larger than 16384 px"
	}
	return config.Width, config.Height, ""
}
