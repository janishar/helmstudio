package gallery

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // registers jpeg with image.DecodeConfig
	_ "image/png"  // registers png
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp" // registers webp

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
)

// MaxSide is the largest width or height an upload may have.
const MaxSide = 16384

// MaxPixels is the most pixels an upload may have: the runner decodes no more (runner_service core/images.py
// MAX_PIXELS); a small file can hold a huge flat image.
const MaxPixels = 50_000_000

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
	kind, audio := "image", (*dto.AudioInfo)(nil)
	if body.Audio() { // API 1.19: its length as declared; the runner reads the file itself
		kind, audio = "audio", &dto.AudioInfo{DurationS: *body.DurationS}
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready, audio)
			VALUES ($1, $2, $3, 'upload', $4, $5, $6, $7, false, $8) RETURNING created_at`,
			id, caller.ID, kind, body.Title(), key, body.ContentType, body.Bytes, audio).Scan(&expiry)
		expiry = expiry.Add(UploadLink)
		return err
	})
	if err != nil {
		return dto.Upload{}, err
	}
	return dto.Upload{TakeID: id, UploadURL: s.signer.SignPut(key, body.Bytes, body.ContentType, expiry),
		ContentType: body.ContentType, Bytes: body.Bytes, ExpiresAt: expiry.UTC()}, nil
}

// CompleteUpload checks the uploaded object (size, a real image of the declared type, at most MaxSide a side and
// MaxPixels) and shows the take, upright: a JPEG's EXIF orientation 5-8 swaps the stored width and height. 409 while the object is not there; a failed check discards the upload (404 from then on) and is a 422.
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
	width, height, etag, detail, err := s.inspect(ctx, row)
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
		row, err = take(ctx, tx, `UPDATE takes t SET ready = true, width = nullif($2, 0), height = nullif($3, 0), etag = $4,
			created_at = clock_timestamp()
			WHERE t.id = $1 AND NOT t.ready AND t.deleted_at IS NULL RETURNING `+model.TakeCols, id, width, height, etag)
		if errors.Is(err, errNotFound) { // completed (or deleted) by a call that ran alongside
			row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		}
		return err
	})
	return s.takeDTO(row), err
}

// inspect reads the object's size and header: the image's size and the object's content hash (md5 hex, the etag of
// cmd.run.execute's image values), or the 422 detail of the failed check. A missing object is a 409.
func (s *Service) inspect(ctx context.Context, row model.Take) (width, height int, etag, detail string, err error) {
	info, err := s.store.Stat(ctx, row.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, 0, "", "", network.NewConflictError("upload not received", nil)
	} else if err != nil {
		return 0, 0, "", "", network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	if info.Size != row.Bytes {
		return 0, 0, "", "size differs from the declared bytes", nil
	}
	body, err := s.store.Get(ctx, row.Key)
	if err != nil {
		return 0, 0, "", "", network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	defer body.Close()
	hash := md5.New()
	head, err := io.ReadAll(io.LimitReader(io.TeeReader(body, hash), headBytes))
	if err == nil {
		_, err = io.Copy(hash, body) // the rest, for the hash (at most 50 MB)
	}
	if err != nil {
		return 0, 0, "", "", network.NewServiceUnavailableError("could not read the upload; try again", err)
	}
	if row.Kind == "audio" {
		detail = checkAudio(head, row.ContentType)
	} else {
		width, height, detail = checkImage(head, row.ContentType)
	}
	return width, height, hex.EncodeToString(hash.Sum(nil)), detail, nil
}

// checkAudio checks an audio upload's header (API 1.19): RIFF….WAVE for a wav; an ID3v2 tag or an MPEG audio frame
// sync for an mp3. Its length is the client's (the runner reads the file itself).
func checkAudio(head []byte, contentType string) string {
	if contentType == "audio/wav" {
		if len(head) < 12 || string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
			return "not a valid wav file"
		}
		return ""
	}
	if len(head) < 3 || (string(head[:3]) != "ID3" && (head[0] != 0xFF || head[1]&0xE0 != 0xE0)) {
		return "not a valid mp3 file"
	}
	return ""
}

// checkImage decodes an image header: its upright size, or why it is not a valid image of contentType at most MaxSide
// a side and MaxPixels.
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
	if config.Width*config.Height > MaxPixels {
		return 0, 0, "image is larger than 50 MP"
	}
	if format == "jpeg" && jpegOrientation(head) >= 5 { // shown (and decoded by the runner) turned a quarter
		return config.Height, config.Width, ""
	}
	return config.Width, config.Height, ""
}

// jpegOrientation is the EXIF Orientation (1-8) of a JPEG header, 1 without one: the first APP1 Exif segment's IFD0
// tag 0x0112, as Pillow's exif_transpose reads it.
func jpegOrientation(head []byte) int {
	for i := 2; i+4 <= len(head) && head[i] == 0xFF; {
		marker, size := head[i+1], int(binary.BigEndian.Uint16(head[i+2:]))
		if marker == 0xDA || size < 2 { // start of scan: no more header segments
			break
		}
		segment := head[i+4 : min(i+2+size, len(head))]
		if marker == 0xE1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return exifOrientation(segment[6:])
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation reads the Orientation tag of a TIFF structure's first IFD, 1 when absent or out of range.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int64(order.Uint32(tiff[4:]))
	if ifd+2 > int64(len(tiff)) {
		return 1
	}
	for e, n := int(ifd)+2, int(order.Uint16(tiff[ifd:])); n > 0 && e+12 <= len(tiff); e, n = e+12, n-1 {
		if order.Uint16(tiff[e:]) == 0x0112 {
			if v := int(order.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
