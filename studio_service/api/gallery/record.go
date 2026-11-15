package gallery

import (
	"context"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery/dto"
)

// Output is a run output the projector stores as a take.
type Output struct {
	OwnerID, SessionID, RunID string
	Index                     int
	Key, Kind                 string
	Width, Height             *int
	Bytes                     int64
	Etag                      *string // md5 hex of the object, when the runner sent it (evt.run.output etag)
	MadeWith                  dto.MadeWith
	CreatedAt                 *time.Time // when the output was made; nil: now
	BatchID                   *string    // a batch run's: the take's source is pipeline
	Parents                   []string   // the takes of the run's Image values, each once, in input order
	Video                     *dto.Video // kind video: its metadata and poster
}

// contentTypes maps a run output key's extension to its content type.
var contentTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".wav": "audio/wav", ".mp3": "audio/mpeg"}

// Record stores the take of a run output (source pipeline for a batch run's, else create) in the projector's transaction (as System) and returns its id; the same
// (run, index) again returns the take already stored.
func Record(ctx context.Context, tx pgx.Tx, output Output) (string, error) {
	contentType := contentTypes[strings.ToLower(path.Ext(output.Key))]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO takes (id, owner_id, kind, source, title, key, content_type, width, height, bytes,
		session_id, run_id, output_idx, made_with, ready, created_at, batch_id, etag, video)
		VALUES ($1, $2, $3, CASE WHEN $15::uuid IS NULL THEN 'create' ELSE 'pipeline' END, $4, $5, $6, $7, $8, $9, $10, $11,
		$12, $13, true, coalesce($14, clock_timestamp()), $15, $16, $17)
		ON CONFLICT (run_id, output_idx) DO UPDATE SET run_id = EXCLUDED.run_id RETURNING id::text`,
		utility.NewID(), output.OwnerID, output.Kind, Title(output.MadeWith), output.Key, contentType, output.Width,
		output.Height, output.Bytes, output.SessionID, output.RunID, output.Index, output.MadeWith, output.CreatedAt,
		output.BatchID, output.Etag, output.Video).Scan(&id)
	if err != nil || len(output.Parents) == 0 {
		return id, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO take_parents (take_id, parent_id, owner_id, idx)
		SELECT $1, parent, $2, idx - 1 FROM unnest($3::uuid[]) WITH ORDINALITY AS u(parent, idx)
		ON CONFLICT DO NOTHING`, id, output.OwnerID, output.Parents)
	return id, err
}

// Title is a run output's first title: the prompt's first line (at most 80 characters), else the workflow's title.
func Title(madeWith dto.MadeWith) string {
	line := ""
	if madeWith.Prompt != nil {
		line, _, _ = strings.Cut(strings.TrimSpace(*madeWith.Prompt), "\n")
		line = strings.TrimSpace(line)
	}
	if utf8.RuneCountInString(line) > 80 {
		line = strings.TrimSpace(string([]rune(line)[:80]))
	}
	if line == "" {
		return madeWith.WorkflowTitle
	}
	return line
}
