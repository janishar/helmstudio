package studioapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/janishar/helmstudio/internal/media"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Assets (R34-R37, 07 §4, Q9-Q14, first review #3, #4, #9, #16).

const assetCols = `id, sha256, kind, mime, bytes, width, height, duration_s, fps, blob_path, library_path, origin_studio, pinned, state, created_at`

type assetRow struct {
	helm.Asset
	blobPath string
	library  sql.NullString
	origin   sql.NullString
}

func scanAsset(sc interface{ Scan(...any) error }) (*assetRow, error) {
	var a assetRow
	var width, height sql.NullInt64
	var dur, fps sql.NullFloat64
	var pinned int64
	var created sql.NullInt64
	var kind string
	if err := sc.Scan(&a.ID, &a.SHA256, &kind, &a.Mime, &a.Bytes, &width, &height, &dur, &fps, &a.blobPath, &a.library, &a.origin, &pinned, &a.State, &created); err != nil {
		return nil, err
	}
	a.Kind = helm.AssetKind(kind)
	if width.Valid {
		a.Width = &width.Int64
	}
	if height.Valid {
		a.Height = &height.Int64
	}
	if dur.Valid {
		a.DurationS = &dur.Float64
	}
	if fps.Valid {
		a.FPS = &fps.Float64
	}
	a.Pinned = pinned == 1
	a.CreatedAt = fromMS(created.Int64)
	return &a, nil
}

// view is the asset as a caller may see it: another studio's origin and
// library path are never shown (first review #9).
func (a *assetRow) view(p Principal) helm.Asset {
	out := a.Asset
	out.URL = Base + "/assets/" + a.ID
	out.Thumb = Base + "/assets/" + a.ID + "/thumb?w=320"
	if a.origin.Valid && a.origin.String == p.StudioID {
		out.OriginStudio = &a.origin.String
		if a.library.Valid {
			out.LibraryPath = &a.library.String
		}
	}
	return out
}

// readableAsset is the Q9 read rule, in one query.
const readableAsset = `(?2 = 1
	OR EXISTS (SELECT 1 FROM studio_assets s WHERE s.asset_id = a.id AND s.studio_id = ?3)
	OR EXISTS (SELECT 1 FROM items i WHERE i.asset_id = a.id AND i.studio_id = ?3)
	OR EXISTS (SELECT 1 FROM item_inputs x JOIN items i ON i.id = x.item_id WHERE x.asset_id = a.id AND i.studio_id = ?3)
	OR EXISTS (SELECT 1 FROM inbox n JOIN items i ON i.id = n.item_id WHERE n.to_studio = ?3
		AND (i.asset_id = a.id OR EXISTS (SELECT 1 FROM item_inputs x WHERE x.item_id = i.id AND x.asset_id = a.id))))`

func readAll(p Principal) int {
	if p.has(string(helm.CapabilityGalleryReadAll)) {
		return 1
	}
	return 0
}

func (s *Service) readAsset(ctx context.Context, q querier, p Principal, id string) (*assetRow, error) {
	a, err := scanAsset(q.QueryRowContext(ctx, `SELECT `+assetCols+` FROM assets a WHERE a.id = ?1 AND `+readableAsset, id, readAll(p), p.StudioID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no asset %s", id)
	}
	return a, err
}

func mimeFor(name string, head []byte) string {
	if t := mime.TypeByExtension(extOf(name)); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	if len(head) > 0 {
		return strings.SplitN(http.DetectContentType(head), ";", 2)[0]
	}
	return "application/octet-stream"
}

func sniff(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, 512)
	n, _ := io.ReadFull(f, b)
	return b[:n]
}

type hints struct {
	width, height *int64
	duration, fps *float64
	pinned        bool
}

// ingest records a staged blob: a new asset, or the existing one for the same
// bytes. It grants the caller read access either way.
func (s *Service) ingest(ctx context.Context, p Principal, st *media.Staged, kind helm.AssetKind, name, contentType string, h hints) (*helm.Asset, bool, error) {
	var out *assetRow
	created := false
	var placed, libRel string
	s.blobs.Lock()
	defer s.blobs.Unlock()
	err := s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := ms(s.now())
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM assets WHERE sha256 = ?`, st.SHA256).Scan(&existing)
		switch {
		case err == nil:
			s.media.Discard(st)
			if h.pinned {
				if _, err := tx.ExecContext(ctx, `UPDATE assets SET pinned = 1 WHERE id = ?`, existing); err != nil {
					return err
				}
			}
		case errors.Is(err, sql.ErrNoRows):
			ext := extOf(name)
			mimeType := contentType
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = mimeFor(name, sniff(st.Path))
			}
			if placed, err = s.media.Place(st, ext); err != nil {
				return err
			}
			width, height := h.width, h.height
			if kind == helm.AssetKindImage {
				if w, hh, err := media.ProbeImage(s.media.BlobPath(placed)); err == nil {
					wi, hi := int64(w), int64(hh)
					width, height = &wi, &hi
				}
			}
			existing = s.newID()
			var warning string
			libRel, warning, err = s.media.LibraryLink(placed, p.StudioID, s.now(), libraryName(name, existing, ext))
			if err != nil {
				return err
			}
			var lib any
			if libRel != "" {
				lib = libRel
			}
			pinned := 0
			if h.pinned {
				pinned = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO assets (id, sha256, kind, mime, bytes, width, height, duration_s, fps, blob_path, library_path, origin_studio, pinned, state, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?)`,
				existing, st.SHA256, string(kind), mimeType, st.Bytes, width, height, h.duration, h.fps, placed, lib, p.StudioID, pinned, now); err != nil {
				return err
			}
			created = true
			defer func() {
				if out != nil && warning != "" {
					out.Warnings = append(out.Warnings, warning)
				}
			}()
		default:
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO studio_assets (studio_id, asset_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, p.StudioID, existing, now); err != nil {
			return err
		}
		out, err = s.readAsset(ctx, tx, p, existing)
		return err
	})
	if err != nil {
		s.media.Discard(st)
		if created || placed != "" {
			// The row did not commit: take back the files this call made.
			_, _ = s.media.Remove(placed, libRel, "")
		}
		return nil, false, err
	}
	v := out.view(p)
	return &v, created, nil
}

func libraryName(name, id, ext string) string {
	base := filepath.Base(name)
	if name == "" || base == "." || base == string(filepath.Separator) {
		return id + ext
	}
	return base
}

func (s *Service) AssetsUpload(ctx context.Context, body io.Reader, contentType string, contentLength int64, params helm.AssetsUploadParams) (*helm.Asset, bool, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, false, err
	}
	name := ""
	if params.Filename != nil {
		name = *params.Filename
	}
	if s.cfg.FreeBytes != nil {
		if free, err := s.cfg.FreeBytes(s.media.Assets); err == nil {
			// Headroom as for weight downloads: the larger of 2 GiB and 5%. The
			// body's size is its Content-Length when the client sent one
			// (second review #8); a chunked upload is checked for headroom only.
			need := uint64(0)
			if contentLength > 0 {
				if contentLength > s.cfg.UploadLimit {
					e := apiError(http.StatusRequestEntityTooLarge, "too_large", "the upload is %d bytes, over the limit of %d", contentLength, s.cfg.UploadLimit)
					e.Details = map[string]any{"limit": s.cfg.UploadLimit}
					return nil, false, e
				}
				need = uint64(contentLength)
			}
			headroom := max(uint64(2<<30), need/20)
			if free < need+headroom {
				e := apiError(http.StatusInsufficientStorage, "disk_space", "the asset store's volume has %d bytes free, and this upload needs %d plus %d of headroom", free, need, headroom)
				e.Details = map[string]any{"needed": need + headroom, "free": free}
				return nil, false, e
			}
		}
	}
	st, err := s.media.Write(body, s.cfg.UploadLimit)
	if errors.Is(err, media.ErrTooLarge) {
		e := apiError(http.StatusRequestEntityTooLarge, "too_large", "the upload is over %d bytes", s.cfg.UploadLimit)
		e.Details = map[string]any{"limit": s.cfg.UploadLimit}
		return nil, false, e
	}
	if err != nil {
		return nil, false, err
	}
	pinned := params.Pinned != nil && *params.Pinned
	return s.ingest(ctx, p, st, params.Kind, name, strings.SplitN(contentType, ";", 2)[0],
		hints{width: params.Width, height: params.Height, duration: params.DurationS, fps: params.FPS, pinned: pinned})
}

func (s *Service) AssetsAdopt(ctx context.Context, body *helm.AdoptRequest) (*helm.Asset, bool, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, false, err
	}
	src, fromStage, err := s.resolveAdoptable(p, body.Path)
	if err != nil {
		return nil, false, err
	}
	st, err := s.media.Link(src)
	if errors.Is(err, media.ErrCrossDevice) {
		return nil, false, unprocessable("cross_device", "%s is on a different volume from the asset store at %s; adoption never copies, so write the file to HELM_STAGE_DIR", src, s.media.Assets)
	}
	if err != nil {
		return nil, false, err
	}
	pinned := body.Pinned != nil && *body.Pinned
	asset, created, err := s.ingest(ctx, p, st, body.Kind, src, "",
		hints{width: body.Width, height: body.Height, duration: body.DurationS, fps: body.FPS, pinned: pinned})
	if err != nil {
		return nil, false, err
	}
	if fromStage {
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.cfg.Logf("studioapi: adopted %s but could not remove the stage file: %v", src, err)
		}
	}
	return asset, created, nil
}

// resolveAdoptable finds path inside the caller's stage or data directory.
func (s *Service) resolveAdoptable(p Principal, path string) (string, bool, error) {
	var lastErr error
	for i, root := range []string{s.cfg.Paths.Stage(p), s.cfg.Paths.Data(p.StudioID)} {
		if root == "" {
			continue
		}
		got, err := media.Resolve(root, path)
		if err == nil {
			return got, i == 0, nil
		}
		if !errors.Is(err, media.ErrOutside) {
			lastErr = err
			break
		}
	}
	switch {
	case lastErr == nil:
		return "", false, unprocessable("outside_roots", "%s is not inside this studio's stage directory (%s) or data directory (%s), or it passes through a symlink", path, s.cfg.Paths.Stage(p), s.cfg.Paths.Data(p.StudioID))
	case errors.Is(lastErr, fs.ErrNotExist):
		return "", false, notFound("no file at %s", path)
	default:
		return "", false, unprocessable("not_a_file", "%v", lastErr)
	}
}

func (s *Service) AssetsRead(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, params helm.AssetsReadParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	a, err := s.readAsset(ctx, s.st.Reader(), p, id)
	if err != nil {
		return err
	}
	f, err := os.Open(s.media.BlobPath(a.blobPath))
	if errors.Is(err, fs.ErrNotExist) {
		return apiError(http.StatusGone, "asset_missing", "asset %s is recorded but its file is gone from %s", id, a.blobPath)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if params.Range != nil {
		if unsatisfiable(*params.Range, fi.Size()) {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(fi.Size(), 10))
			return apiError(http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable", "%s is not a range within %d bytes", *params.Range, fi.Size())
		}
	}
	w.Header().Set("Content-Type", a.Mime)
	setETag(w, a.SHA256)
	http.ServeContent(w, r, "", fi.ModTime(), f)
	return nil
}

// unsatisfiable reports a single byte range that starts past the end. Other
// shapes are left to http.ServeContent, which ignores what it cannot serve.
func unsatisfiable(spec string, size int64) bool {
	v, ok := strings.CutPrefix(strings.TrimSpace(spec), "bytes=")
	if !ok || strings.Contains(v, ",") {
		return false
	}
	start, _, _ := strings.Cut(v, "-")
	if start == "" {
		return false
	}
	n, err := strconv.ParseInt(start, 10, 64)
	return err == nil && n >= size
}

func (s *Service) AssetsThumb(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, params helm.AssetsThumbParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	a, err := s.readAsset(ctx, s.st.Reader(), p, id)
	if err != nil {
		return err
	}
	if a.Kind != helm.AssetKindImage {
		return unsupported("thumbnails for %s assets need ffmpeg, which helmstudio does not ship yet", a.Kind)
	}
	width := 320
	if params.W != nil {
		width = int(*params.W)
	}
	abs, rel, made, err := s.media.Thumb(a.blobPath, a.SHA256, width)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return apiError(http.StatusGone, "asset_missing", "asset %s is recorded but its file is gone", id)
	case errors.Is(err, media.ErrNotImage):
		return unsupported("asset %s is not an image the standard library can decode", id)
	case err != nil:
		return err
	}
	if made {
		fi, _ := os.Stat(abs)
		size := int64(0)
		if fi != nil {
			size = fi.Size()
		}
		_ = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO derived (asset_id, variant, path, bytes) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
				a.ID, fmt.Sprintf("thumb-%d", width), rel, size)
			return err
		})
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, abs)
	return nil
}
