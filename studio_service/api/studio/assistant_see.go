package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // registers png with image.Decode
	"io"
	"log"

	"github.com/jackc/pgx/v5"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers webp (thumbnails)

	"github.com/helmstudio/helmgo/utility"
	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/common"
)

// SeeRequest is req.studio.see's request (API 1.65): one take's pictures for the assistant's own model.
type SeeRequest struct {
	OwnerID  string `json:"owner_id"`
	ThreadID string `json:"thread_id"`
	TakeID   string `json:"take_id"`
}

// SeeReply is req.studio.see's reply: ok (take_id, kind, title, images) or error and code.
type SeeReply struct {
	TakeID string    `json:"take_id,omitempty"`
	Kind   string    `json:"kind,omitempty"`
	Title  *string   `json:"title,omitempty"`
	Images *[][]byte `json:"images,omitempty"` // base64 in JSON; a pointer so an ok reply keeps "images": [] and an error has none
	// API 1.104: a video's or audio's facts (defs takeFacts) and, when the picture is its frame grid, its frames' seconds
	Facts json.RawMessage `json:"facts,omitempty"`
	At    []float64       `json:"at,omitempty"`
	Error string          `json:"error,omitempty"`
	Code  string          `json:"code,omitempty"`
}

// seeSide is the long side the model gets (alpha.2 item 8: "about 1024 px"); seeMax caps one picture's bytes.
const (
	seeSide = 1024
	seeMax  = 500 << 10
)

// See answers studio.see: the thread's owner's take as JPEG pictures (an image's thumbnail, a video's poster or, from
// API 1.104, its frame grid; none for audio) with a video's or audio's facts, never stored. Another owner's thread or take, or a missing one, is not_found.
func (s *Service) See(ctx context.Context, req *SeeRequest) SeeReply {
	if !utility.IsUUID(req.OwnerID) || !utility.IsUUID(req.ThreadID) || !utility.IsUUID(req.TakeID) {
		return SeeReply{Error: "owner_id, thread_id and take_id are ids", Code: "invalid"}
	}
	var kind, title, key string
	var facts struct {
		Facts json.RawMessage `json:"facts"`
		Key   *string         `json:"key"`
		At    []float64       `json:"at"`
	}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT t.kind, t.title, `+gallerymodel.ThumbKey+`, coalesce(t.facts, '{}') FROM takes t
			JOIN assistant_threads a ON a.id = $2 AND a.owner_id = $3
			WHERE t.id = $1 AND t.owner_id = $3 AND t.deleted_at IS NULL AND t.ready`, req.TakeID, req.ThreadID,
			req.OwnerID).Scan(&kind, &title, &key, &facts)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return SeeReply{Error: "not found", Code: "not_found"}
	case err != nil:
		log.Printf("studio.see %s: %v", req.TakeID, err)
		return SeeReply{Error: "internal error", Code: "internal"}
	}
	pics := [][]byte{}
	out := SeeReply{TakeID: req.TakeID, Kind: kind, Title: &title, Images: &pics, Facts: facts.Facts}
	if kind == "audio" {
		return out
	}
	if facts.Key != nil { // API 1.104: a video's frame grid, not its poster
		if pic, err := s.seeJPEG(ctx, *facts.Key); err == nil {
			out.At = facts.At
			*out.Images = append(*out.Images, pic)
			return out
		}
	}
	pic, err := s.seeJPEG(ctx, key)
	if err != nil {
		log.Printf("studio.see %s: %v", req.TakeID, err)
		return out // the model still gets the take's title, with no pictures
	}
	*out.Images = append(*out.Images, pic)
	return out
}

// seeJPEG reads an object and answers it as a JPEG, its long side at most seeSide.
func (s *Service) seeJPEG(ctx context.Context, key string) ([]byte, error) {
	body, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	src, _, err := image.Decode(io.LimitReader(body, 64<<20))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if long := max(w, h); long > seeSide {
		w, h = max(1, w*seeSide/long), max(1, h*seeSide/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src) // a transparent pixel reads as white
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	for _, q := range []int{85, 60} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() <= seeMax {
			return buf.Bytes(), nil
		}
	}
	return nil, errors.New("too large as JPEG")
}
