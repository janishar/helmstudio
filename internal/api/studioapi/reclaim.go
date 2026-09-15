package studioapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Asset reclaim (Q12, 02 §8, first review #1, #3). Launcher-only, but it lives
// here with the rest of the asset rules so they are enforced in one place.

// ReclaimItem is one asset reclaim would delete.
type ReclaimItem struct {
	ID           string   `json:"id"`
	SHA256       string   `json:"sha256"`
	Bytes        int64    `json:"bytes"`
	BlobPath     string   `json:"blob_path"`
	LibraryPath  *string  `json:"library_path"`
	DeletedItems []string `json:"deleted_items"`
}

// ReclaimPreview is api/openapi.yaml AssetReclaimPreview.
type ReclaimPreview struct {
	Items        []ReclaimItem `json:"items"`
	TotalBytes   int64         `json:"total_bytes"`
	Unreferenced struct {
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	} `json:"unreferenced"`
	Confirm string `json:"confirm"`
}

// ReclaimResult is api/openapi.yaml AssetReclaimResult.
type ReclaimResult struct {
	Items            []ReclaimItem `json:"items"`
	TotalBytes       int64         `json:"total_bytes"`
	KeptLibraryPaths []string      `json:"kept_library_paths"`
}

// ErrPreviewChanged means the confirm did not match; nothing was deleted.
var ErrPreviewChanged = errors.New("the reclaimable set changed since that preview; nothing was deleted")

const reclaimable = `SELECT a.id, a.sha256, a.bytes, a.blob_path, a.library_path FROM assets a
	WHERE a.pinned = 0
	  AND a.first_referenced_at IS NOT NULL
	  AND NOT EXISTS (SELECT 1 FROM items i WHERE i.asset_id = a.id AND i.deleted_at IS NULL)
	  AND NOT EXISTS (SELECT 1 FROM item_inputs x JOIN items i ON i.id = x.item_id
	                  WHERE x.asset_id = a.id AND i.deleted_at IS NULL)
	  AND NOT EXISTS (SELECT 1 FROM timelines t, json_each(t.tracks) tr, json_each(tr.value, '$.clips') c
	                  WHERE json_extract(c.value, '$.asset_id') = a.id)
	ORDER BY a.id`

type rowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) preview(ctx context.Context, q rowsQuerier) (*ReclaimPreview, error) {
	rows, err := q.QueryContext(ctx, reclaimable)
	if err != nil {
		return nil, fmt.Errorf("finding reclaimable assets: %w", err)
	}
	p := &ReclaimPreview{Items: []ReclaimItem{}}
	for rows.Next() {
		var it ReclaimItem
		var lib sql.NullString
		if err := rows.Scan(&it.ID, &it.SHA256, &it.Bytes, &it.BlobPath, &lib); err != nil {
			rows.Close()
			return nil, err
		}
		if lib.Valid {
			it.LibraryPath = &lib.String
		}
		p.Items = append(p.Items, it)
		// A blob still linked from a studio's {data} frees nothing when it
		// goes (second review #7).
		if !s.media.LinkedOutside(it.BlobPath, lib.String) {
			p.TotalBytes += it.Bytes
		}
	}
	rows.Close()
	for i := range p.Items {
		it := &p.Items[i]
		dr, err := q.QueryContext(ctx, `SELECT i.id FROM items i WHERE i.deleted_at IS NOT NULL AND (i.asset_id = ?1
			OR EXISTS (SELECT 1 FROM item_inputs x WHERE x.item_id = i.id AND x.asset_id = ?1)) ORDER BY i.id`, it.ID)
		if err != nil {
			return nil, err
		}
		it.DeletedItems = []string{}
		for dr.Next() {
			var id string
			if err := dr.Scan(&id); err != nil {
				dr.Close()
				return nil, err
			}
			it.DeletedItems = append(it.DeletedItems, id)
		}
		dr.Close()
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(bytes), 0) FROM assets WHERE pinned = 0 AND first_referenced_at IS NULL`).
		Scan(&p.Unreferenced.Count, &p.Unreferenced.Bytes); err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, it := range p.Items {
		fmt.Fprintf(h, "%s:%d:%s;", it.ID, it.Bytes, strings.Join(it.DeletedItems, ","))
	}
	p.Confirm = hex.EncodeToString(h.Sum(nil))[:32]
	return p, nil
}

// ReclaimPreview is exactly what Reclaim would delete now.
func (s *Service) ReclaimPreview(ctx context.Context) (*ReclaimPreview, error) {
	return s.preview(ctx, s.st.Reader())
}

// Reclaim deletes exactly the set confirm was issued for. When the set has
// changed it deletes nothing and returns the current preview with
// ErrPreviewChanged.
func (s *Service) Reclaim(ctx context.Context, confirm string) (*ReclaimResult, *ReclaimPreview, error) {
	var doomed *ReclaimPreview
	s.blobs.Lock()
	defer s.blobs.Unlock()
	err := s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		p, err := s.preview(ctx, tx)
		if err != nil {
			return err
		}
		doomed = p
		if p.Confirm != confirm {
			return ErrPreviewChanged
		}
		for _, it := range p.Items {
			for _, item := range it.DeletedItems {
				if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE item_id = ?`, item); err != nil {
					return err
				}
				// Inputs, tags and inbox rows cascade.
				if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ? AND deleted_at IS NOT NULL`, item); err != nil {
					return fmt.Errorf("removing deleted item %s: %w", item, err)
				}
			}
		}
		for _, it := range p.Items {
			if _, err := tx.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, it.ID); err != nil {
				return fmt.Errorf("removing asset %s: %w", it.ID, err)
			}
		}
		return nil
	})
	if errors.Is(err, ErrPreviewChanged) {
		return nil, doomed, err
	}
	if err != nil {
		return nil, nil, err
	}
	if s.afterReclaimCommit != nil {
		s.afterReclaimCommit()
	}
	res := &ReclaimResult{Items: doomed.Items, TotalBytes: doomed.TotalBytes, KeptLibraryPaths: []string{}}
	for _, it := range doomed.Items {
		lib := ""
		if it.LibraryPath != nil {
			lib = *it.LibraryPath
		}
		removed, err := s.media.Remove(it.BlobPath, lib, it.SHA256)
		if err != nil {
			s.cfg.Logf("studioapi: reclaim removed asset %s's row but not all its files: %v", it.ID, err)
		}
		if removed.KeptLibraryPath != "" {
			res.KeptLibraryPaths = append(res.KeptLibraryPaths, removed.KeptLibraryPath)
		}
	}
	sort.Strings(res.KeptLibraryPaths)
	return res, nil, nil
}

// ServeReclaim serves GET and POST /assets:reclaim for the launcher.
func (s *Service) ServeReclaim(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, err := s.ReclaimPreview(r.Context())
		if err != nil {
			writeError(w, apiError(http.StatusInternalServerError, "internal", "%v", err))
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPost:
		var body map[string]any
		if err := readObject(w, r, &body, []string{"confirm"}, 1); err != nil {
			var e *helm.Error
			if errors.As(err, &e) {
				writeError(w, e)
			}
			return
		}
		confirm, _ := body["confirm"].(string)
		if confirm == "" {
			writeError(w, badRequest(`send {"confirm": "<the confirm value from GET /assets:reclaim>"}`))
			return
		}
		res, current, err := s.Reclaim(r.Context(), confirm)
		if errors.Is(err, ErrPreviewChanged) {
			e := conflict("preview_changed", "%v", err)
			e.Details = map[string]any{"preview": current}
			writeError(w, e)
			return
		}
		if err != nil {
			writeError(w, apiError(http.StatusInternalServerError, "internal", "%v", err))
			return
		}
		writeJSON(w, http.StatusOK, res)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, apiError(http.StatusMethodNotAllowed, "method_not_allowed", "use GET to preview or POST to reclaim"))
	}
}
