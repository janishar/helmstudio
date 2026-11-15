package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// The library (contracts/README.md "Elements"): the caller's elements, one table for every library kind (looks in
// M3). Owner-scoped by row-level security; an image is a take id, checked like a run's reference rows.

// MaxLibraryItems is how many library items one user may have.
const MaxLibraryItems = 200

// libraryRow is a library_items row with its live images ({take_id, key, width, height}, deleted takes left out).
type libraryRow struct {
	ID, Kind, Group, Name string
	Handle                *string
	Text                  string
	Version               int
	CreatedAt, UpdatedAt  time.Time
	Images                json.RawMessage
}

// libraryCols are the columns libraryRow scans from library_items l, in order.
const libraryCols = `l.id::text, l.kind, l.item_group, l.name, l.handle, l.text, l.version, l.created_at, l.updated_at,
	coalesce((SELECT jsonb_agg(jsonb_build_object('take_id', t.id, 'key', t.key, 'width', t.width, 'height', t.height)
		ORDER BY u.o) FROM unnest(l.images) WITH ORDINALITY AS u(i, o) JOIN takes t ON t.id = u.i AND t.owner_id = l.owner_id
		AND t.deleted_at IS NULL AND t.ready AND t.width IS NOT NULL AND t.height IS NOT NULL), '[]')`

// itemDTO maps a row, signing its images' keys (the original is its own thumbnail, as in the gallery).
func (s *Service) itemDTO(row libraryRow) dto.LibraryItem {
	out := dto.LibraryItem{ID: row.ID, Kind: row.Kind, Group: row.Group, Name: row.Name, Handle: row.Handle, Text: row.Text,
		Images: []dto.LibraryImage{}, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	var images []struct {
		TakeID        string `json:"take_id"`
		Key           string `json:"key"`
		Width, Height int
	}
	_ = json.Unmarshal(row.Images, &images)
	for _, image := range images {
		link := s.signer.Sign(image.Key)
		out.Images = append(out.Images, dto.LibraryImage{TakeID: image.TakeID, URL: link, ThumbURL: link, Width: image.Width,
			Height: image.Height})
	}
	if len(out.Images) > 0 {
		out.ThumbURL = &out.Images[0].ThumbURL
	}
	return out
}

// LibraryQuery is GET /library's filters, checked.
type LibraryQuery struct {
	Kind, Group, Q string
	Limit          int
}

// ListLibrary returns the caller's items: with q, the exact handle first, then by handle; else newest updated first.
func (s *Service) ListLibrary(ctx context.Context, caller common.Caller, query LibraryQuery) (dto.LibraryItemList, error) {
	order := `l.updated_at DESC, l.id`
	if query.Q != "" {
		order = `l.handle = $3 DESC, l.handle COLLATE "C", l.id`
	}
	out := dto.LibraryItemList{Items: []dto.LibraryItem{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l
			WHERE ($1 = '' OR l.kind = $1) AND ($2 = '' OR l.item_group = $2)
			AND ($3 = '' OR starts_with(l.handle, $3) OR starts_with(lower(l.name), $3))
			ORDER BY `+order+` LIMIT $4`, query.Kind, query.Group, query.Q, query.Limit)
		for _, row := range rows {
			out.Items = append(out.Items, s.itemDTO(row))
		}
		return err
	})
	return out, err
}

// GetLibraryItem returns one of the caller's items.
func (s *Service) GetLibraryItem(ctx context.Context, caller common.Caller, id string) (dto.LibraryItem, error) {
	if !utility.IsUUID(id) {
		return dto.LibraryItem{}, errNotFound
	}
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l WHERE l.id = $1`, id)
		out = s.itemDTO(row)
		return err
	})
	return out, err
}

// CreateLibraryItem makes an element of the caller's gallery images (the gallery's "Make element" too).
func (s *Service) CreateLibraryItem(ctx context.Context, caller common.Caller, body *dto.LibraryItemCreate) (dto.LibraryItem, error) {
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if err := s.checkItem(ctx, tx, caller.ID, "", body.LibraryFields); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM library_items`).Scan(&count); err != nil {
			return err
		} else if count >= MaxLibraryItems {
			return network.NewConflictError(fmt.Sprintf("You have %d library items; delete some first.", MaxLibraryItems), nil)
		}
		id := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO library_items (id, owner_id, kind, item_group, name, handle, text, images)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid[])`, id, caller.ID, body.Kind, body.Group, body.Name, body.Handle,
			*body.Text, takeIDs(body.Images)); err != nil {
			return err
		}
		row, err := one[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l WHERE l.id = $1`, id)
		out = s.itemDTO(row)
		return err
	})
	return out, err
}

// ReplaceLibraryItem saves every field of one of the caller's items when body.Version is the stored one (version + 1).
func (s *Service) ReplaceLibraryItem(ctx context.Context, caller common.Caller, id string, body *dto.LibraryItemUpdate) (dto.LibraryItem, error) {
	if !utility.IsUUID(id) {
		return dto.LibraryItem{}, errNotFound
	}
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var version int
		if err := tx.QueryRow(ctx, `SELECT version FROM library_items WHERE id = $1 FOR UPDATE`, id).Scan(&version); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if version != *body.Version {
			return network.NewConflictError(fmt.Sprintf("element changed since you opened it (now version %d)", version), nil)
		}
		if err := s.checkItem(ctx, tx, caller.ID, id, body.LibraryFields); err != nil {
			return err
		}
		row, err := one[libraryRow](ctx, tx, `UPDATE library_items l SET item_group = $2, name = $3, handle = $4, text = $5,
			images = $6::uuid[], version = version + 1, updated_at = clock_timestamp() WHERE l.id = $1 RETURNING `+libraryCols,
			id, body.Group, body.Name, body.Handle, *body.Text, takeIDs(body.Images))
		out = s.itemDTO(row)
		return err
	})
	return out, err
}

// DeleteLibraryItem deletes one of the caller's items; never refused (past takes keep what they resolved).
func (s *Service) DeleteLibraryItem(ctx context.Context, caller common.Caller, id string) error {
	if !utility.IsUUID(id) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM library_items WHERE id = $1`, id)
		if err == nil && tag.RowsAffected() == 0 {
			err = errNotFound
		}
		return err
	})
}

// checkItem checks an item's images and handle in tx (the caller's), holding a per-user lock until commit so two
// writes can't both take one handle or pass the cap: each image a take of the caller's like a reference row (404
// "images[2]: take <id> not found", 422 "… is not an image" / "… has no size"), the handle no other item's (409).
func (s *Service) checkItem(ctx context.Context, tx pgx.Tx, ownerID, id string, fields dto.LibraryFields) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('library:' || $1::text))`, ownerID); err != nil {
		return err
	}
	refs := make([]imageAt, len(fields.Images))
	for i, image := range fields.Images {
		refs[i] = imageAt{Path: fmt.Sprintf("images[%d]", i+1), TakeID: image.TakeID}
	}
	problems, err := vetTakes(ctx, tx, s.store, ownerID, refs)
	if err != nil {
		return err
	}
	if detail := firstBad(refs, problems); detail != "" {
		if strings.HasSuffix(detail, " not found") {
			return network.NewNotFoundError(detail, nil)
		}
		return network.NewUnprocessableError(detail, nil)
	}
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM library_items WHERE handle = $1 AND id::text <> $2)`,
		fields.Handle, id).Scan(&used); err != nil {
		return err
	} else if used {
		return network.NewConflictError("handle @"+fields.Handle+" is already used", nil)
	}
	return nil
}

// takeIDs are the refs' take ids.
func takeIDs(refs []dto.LibraryImageRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.TakeID
	}
	return out
}
