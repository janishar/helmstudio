package gallery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
)

// Plugins: takes (API 1.70; contracts/README.md "Plugins: takes"): a `takes` plugin, with its call's pass (the
// caller is the pass's owner, Caller.Plugin its plugin), reads one of the owner's takes and makes a new one from
// them. Everything runs as the owner under row-level security, so another user's take is a 404 as everywhere. The new
// take is an upload's (image, audio) or an import's (video) objects and checks, source tool.

// PluginReadLink is how long a plugin's read link lasts.
const PluginReadLink = 10 * time.Minute

// GetPluginTake answers GET /plugin/takes/{take_id}.
func (s *Service) GetPluginTake(ctx context.Context, caller common.Caller, id string) (dto.PluginTake, error) {
	if caller.Plugin == "" || !utility.IsUUID(id) {
		return dto.PluginTake{}, errNotFound
	}
	var row model.Take
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		return err
	})
	if err != nil {
		return dto.PluginTake{}, err
	}
	out := dto.PluginTake{ID: row.ID, Kind: row.Kind, Title: row.Title, ContentType: row.ContentType, Bytes: row.Bytes,
		Width: row.Width, Height: row.Height, URL: s.signer.SignUntil(row.Key, time.Now().Add(PluginReadLink))}
	var media struct {
		DurationS *float64 `json:"duration_s"`
		FPS       *float64 `json:"fps"`
		HasAudio  *bool    `json:"has_audio"`
	}
	if row.Kind == "video" {
		_ = json.Unmarshal(row.Video, &media)
	} else if row.Kind == "audio" {
		_ = json.Unmarshal(row.Audio, &media)
	}
	out.DurationS, out.FPS, out.HasAudio = media.DurationS, media.FPS, media.HasAudio
	return out, nil
}

// CreatePluginTake answers POST /plugin/takes: a pending take of the owner (source tool) and its PUT links.
func (s *Service) CreatePluginTake(ctx context.Context, caller common.Caller, body *dto.PluginTakeCreate) (dto.PluginTakeUpload, error) {
	if caller.Plugin == "" {
		return dto.PluginTakeUpload{}, errNotFound
	}
	id := utility.NewID()
	key := common.OwnerPrefix(caller.ID) + "uploads/" + id + "." + dto.UploadTypes[body.ContentType]
	if body.Kind == "video" { // an import's key shape: files_service takes a video only there
		key = common.OwnerPrefix(caller.ID) + "imports/" + id + ".mp4"
	}
	now := time.Now().UTC()
	wait := importWait{CreatedAt: now, Width: body.Width, Height: body.Height, Video: body.Video}
	tool, _ := json.Marshal(map[string]any{"plugin": caller.Plugin, "tool": body.Tool, "input": body.Input})
	var audio any
	if body.Kind == "audio" {
		audio = dto.AudioInfo{DurationS: body.DurationS}
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM takes t WHERE t.id = ANY($1::uuid[]) AND `+visible,
			body.Parents).Scan(&n); err != nil {
			return err
		}
		if n != len(body.Parents) {
			return errNotFound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, bytes, ready,
			import_wait, tool, audio) VALUES ($1, $2, $3, 'tool', $4, $5, $6, $7, false, $8, $9, $10)`,
			id, caller.ID, body.Kind, body.Title, key, body.ContentType, body.Bytes, wait, tool, audio); err != nil {
			return err
		}
		for i, parent := range body.Parents {
			if _, err := tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx) VALUES ($1, $2, $3, $4)`,
				id, parent, caller.ID, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return dto.PluginTakeUpload{}, err
	}
	expiry := now.Add(UploadLink).Truncate(time.Second)
	out := dto.PluginTakeUpload{TakeID: id, UploadURL: s.signer.SignPut(key, body.Bytes, body.ContentType, expiry), ExpiresAt: expiry}
	if v := body.Video; v != nil {
		poster := s.signer.SignPut(posterKey(key, v.Poster), v.Poster.Bytes, v.Poster.ContentType, expiry)
		out.PosterUploadURL = &poster
	}
	return out, nil
}

// CompletePluginTake answers POST /plugin/takes/{take_id}/complete: the objects checked as an import's (audio as an
// upload's), the take shown; a failed check discards it (422, 404 from then on); a missing object is a 409.
func (s *Service) CompletePluginTake(ctx context.Context, caller common.Caller, id string) (dto.Take, error) {
	if caller.Plugin == "" || !utility.IsUUID(id) {
		return dto.Take{}, errNotFound
	}
	var row model.Take
	var wait importWait
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var raw json.RawMessage
		var err error
		row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND t.source = 'tool'
			AND t.deleted_at IS NULL AND (t.ready OR t.created_at > clock_timestamp() - make_interval(secs => $2))`,
			id, UploadTTL.Seconds())
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT import_wait FROM takes WHERE id = $1`, id).Scan(&raw)
			_ = json.Unmarshal(raw, &wait)
		}
		return err
	})
	if err != nil || row.Ready {
		return s.takeDTO(row), err
	}
	var width, height int
	var etag, detail string
	if row.Kind == "audio" {
		var head []byte
		if head, etag, detail, err = s.readHead(ctx, row.Key, row.Bytes, true); err == nil && detail == "" {
			detail = checkAudio(head, row.ContentType)
		}
	} else {
		width, height, etag, detail, err = s.inspectImport(ctx, row, wait)
	}
	if err != nil {
		return dto.Take{}, err
	}
	if detail != "" { // discarded: the sweep removes the objects
		err = s.tx(ctx, caller, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE takes SET deleted_at = clock_timestamp() WHERE id = $1 AND NOT ready`, id)
			return err
		})
		if err == nil {
			err = network.NewUnprocessableError(detail, nil)
		}
		return dto.Take{}, err
	}
	var video any
	if v := wait.Video; row.Kind == "video" && v != nil {
		video = dto.Video{DurationS: v.DurationS, FPS: v.FPS, Frames: v.Frames, HasAudio: v.HasAudio,
			Poster: dto.Poster{Key: posterKey(row.Key, v.Poster), Width: v.Poster.Width, Height: v.Poster.Height, Bytes: v.Poster.Bytes}}
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = take(ctx, tx, `UPDATE takes t SET ready = true, width = nullif($2, 0), height = nullif($3, 0),
			etag = nullif($4, ''), video = $5, import_wait = NULL, created_at = clock_timestamp()
			WHERE t.id = $1 AND NOT t.ready AND t.deleted_at IS NULL RETURNING `+model.TakeCols, id, width, height, etag, video)
		if errors.Is(err, errNotFound) { // completed (or deleted) by a call that ran alongside
			row, err = take(ctx, tx, `SELECT `+model.TakeCols+` FROM takes t WHERE t.id = $1 AND `+visible, id)
		}
		return err
	})
	return s.takeDTO(row), err
}
