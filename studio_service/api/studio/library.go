package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// The library (contracts/README.md "Elements", "Looks"): the caller's elements and looks, one table for every library
// kind, owner-scoped by row-level security; an image is a take id, checked like a run's reference rows. Starter looks
// are not stored: they are the live hosts' registry looks, read-only, listed to everyone.

// MaxLibraryItems is how many library items one user may have.
const MaxLibraryItems = 200

// errReadOnly is the answer to a write of a starter look.
var errReadOnly = network.NewConflictError("starter looks are read-only; save your own copy", nil)

// libraryRow is a library_items row with its live images ({take_id, key, width, height}, deleted takes left out) and
// the take it was saved from while that take is live.
type libraryRow struct {
	ID, Kind, Group, Name string
	Handle                *string
	Text                  string
	Version               int
	CreatedAt, UpdatedAt  time.Time
	Images                json.RawMessage
	Loras                 json.RawMessage // [{lora_id, name, strength}]
	FromTakeID            *string
	FromTakeKey           *string
	Voice                 json.RawMessage // API 1.30, kind voice: its VoiceInfo
	ClipKey               *string         // API 1.31: the key of a voice's clip take, deleted or not
	VoiceID               *string         // API 1.32, an element: its voice while that is the owner's (elementVoiceCol)
}

// libraryCols are the columns libraryRow scans from library_items l, in order.
const libraryCols = `l.id::text, l.kind, l.item_group, l.name, l.handle, l.text, l.version, l.created_at, l.updated_at,
	coalesce((SELECT jsonb_agg(jsonb_build_object('take_id', t.id, 'key', t.key, 'thumb', ` + gallerymodel.ThumbKey + `, 'width', t.width, 'height', t.height)
		ORDER BY u.o) FROM unnest(l.images) WITH ORDINALITY AS u(i, o) JOIN takes t ON t.id = u.i AND t.owner_id = l.owner_id
		AND t.deleted_at IS NULL AND t.ready AND t.width IS NOT NULL AND t.height IS NOT NULL), '[]'), l.loras,
	(SELECT t.id::text FROM takes t WHERE t.id = l.from_take_id AND t.owner_id = l.owner_id AND t.deleted_at IS NULL AND t.ready),
	(SELECT ` + gallerymodel.ThumbKey + ` FROM takes t WHERE t.id = l.from_take_id AND t.owner_id = l.owner_id AND t.deleted_at IS NULL AND t.ready), l.voice,
	(SELECT t.key FROM takes t WHERE t.id = (l.voice->'clip'->>'take_id')::uuid AND t.owner_id = l.owner_id), ` + elementVoiceCol

// elementVoiceCol is an element's voice_id as read (API 1.32): a starter voice's id as stored (the caller checks it
// is published), an own voice's while it is the owner's voice, else null.
const elementVoiceCol = `(CASE WHEN l.voice_id LIKE '%/%' THEN l.voice_id ELSE (SELECT v.id::text FROM library_items v
	WHERE v.owner_id = l.owner_id AND v.kind = 'voice' AND v.id::text = l.voice_id) END)`

// itemDTO maps a row, signing its images' keys (each image's thumbnail is its take's, as in the gallery); names are the
// LoRA names now (a LoRA gone keeps the name it was saved with).
func (s *Service) itemDTO(row libraryRow, names map[string]string) dto.LibraryItem {
	out := dto.LibraryItem{ID: row.ID, Kind: row.Kind, Group: row.Group, Name: row.Name, Handle: row.Handle, Text: row.Text,
		Images: []dto.LibraryImage{}, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Source: "mine", Loras: []dto.LookLora{}, FromTakeID: row.FromTakeID}
	var images []struct {
		TakeID        string `json:"take_id"`
		Key           string `json:"key"`
		Thumb         string `json:"thumb"`
		Width, Height int
	}
	_ = json.Unmarshal(row.Images, &images)
	for _, image := range images {
		out.Images = append(out.Images, dto.LibraryImage{TakeID: image.TakeID, URL: s.signer.Sign(image.Key),
			ThumbURL: s.signer.Sign(cmp.Or(image.Thumb, image.Key)), Width: image.Width, Height: image.Height})
	}
	for _, l := range rowLoras(row) {
		out.Loras = append(out.Loras, dto.LookLora{Lora: &dto.LoraRef{LoraID: *l.LoraID}, Name: cmp.Or(names[*l.LoraID], l.Name),
			Strength: l.Strength})
	}
	switch {
	case len(out.Images) > 0:
		out.ThumbURL = &out.Images[0].ThumbURL
	case row.Kind == "voice": // API 1.31: a voice with a clip plays its take's file, also after the take was deleted
		out.ThumbURL = s.signer.SignPtr(row.ClipKey)
	case row.FromTakeKey != nil:
		out.ThumbURL = s.signer.SignPtr(row.FromTakeKey)
	}
	out.Needs = needs(row.Text, len(out.Images), len(out.Loras))
	if row.Kind == "voice" {
		_ = json.Unmarshal(row.Voice, &out.Voice)
	}
	if row.Kind == "element" { // API 1.32: null once its voice is deleted or no live host publishes it
		out.VoiceID = json.RawMessage("null")
		if id := row.VoiceID; id != nil && (utility.IsUUID(*id) || s.starterVoiceLive(*id)) {
			out.VoiceID, _ = json.Marshal(*id)
		}
	}
	return out
}

// starterVoiceLive reports whether a live host publishes the starter voice id.
func (s *Service) starterVoiceLive(id string) bool {
	_, ok := s.registry.Voices(time.Now())[id]
	return ok
}

// elementVoice is the voice_id an element saves (API 1.32): nil, or one of the owner's voices or a live starter
// voice, else 404 "voice_id: voice <id> not found"; a PUT sending back its stored id that is gone since saves nil.
func (s *Service) elementVoice(ctx context.Context, tx pgx.Tx, ownerID string, id *string, stored *string) (*string, error) {
	if id == nil {
		return nil, nil
	}
	found, err := readVoices(ctx, tx, s.registry, ownerID, []string{*id})
	if err != nil {
		return nil, err
	}
	if _, ok := found[*id]; ok {
		return id, nil
	}
	if stored != nil && *stored == *id {
		return nil, nil
	}
	return nil, network.NewNotFoundError("voice_id: voice "+*id+" not found", nil)
}

// rowLoras are a row's stored LoRAs.
func rowLoras(row libraryRow) []lookLora {
	var out []lookLora
	_ = json.Unmarshal(row.Loras, &out)
	for i := range out {
		out[i].Strength = dto.ClampStrength(out[i].Strength) // saved before API 1.23: -4..4
	}
	return slices.DeleteFunc(out, func(l lookLora) bool { return l.LoraID == nil })
}

// starterDTO maps a starter look.
func (s *Service) starterDTO(l registry.LiveLook) dto.LibraryItem {
	family := l.Family
	out := dto.LibraryItem{ID: l.ID, Kind: "look", Group: l.Group, Name: l.Name, Text: l.Text, Images: []dto.LibraryImage{},
		Version: l.Version, CreatedAt: l.PublishedAt, UpdatedAt: l.PublishedAt, Source: "starter", Family: &family,
		Loras: []dto.LookLora{}}
	for _, x := range l.Loras {
		item := dto.LookLora{Name: x.Name, Strength: dto.ClampStrength(x.Strength)}
		if x.LoraID != nil {
			item.Lora = &dto.LoraRef{LoraID: *x.LoraID}
		}
		out.Loras = append(out.Loras, item)
	}
	if l.Thumb != nil {
		out.ThumbURL = s.signer.SignPtr(&l.Thumb.Key)
	}
	out.Needs = needs(l.Text, 0, len(l.Loras))
	return out
}

// starterItem is a live starter look as the resolver reads it.
func starterItem(l registry.LiveLook) lookItem {
	out := lookItem{ID: l.ID, Source: "starter", Name: l.Name, Version: l.Version, Text: l.Text, Images: []string{},
		Loras: []lookLora{}}
	for _, x := range l.Loras {
		out.Loras = append(out.Loras, lookLora{LoraID: x.LoraID, Name: x.Name, Strength: dto.ClampStrength(x.Strength)})
	}
	return out
}

// rowItem is an own look row as the resolver reads it (images of deleted takes left out).
func rowItem(row libraryRow) lookItem {
	out := lookItem{ID: row.ID, Source: "mine", Name: row.Name, Version: row.Version, Text: row.Text, Images: []string{},
		Loras: rowLoras(row)}
	var images []struct {
		TakeID string `json:"take_id"`
	}
	_ = json.Unmarshal(row.Images, &images)
	for _, image := range images {
		out.Images = append(out.Images, image.TakeID)
	}
	if out.Loras == nil {
		out.Loras = []lookLora{}
	}
	return out
}

// LibraryQuery is GET /library's filters, checked.
type LibraryQuery struct {
	Kind, Group, Q, Source, Family, Workflow string
	Limit                                    int
}

// ListLibrary returns the caller's items and the live starter looks, filtered: with q, the exact handle first, then by
// handle (null last), then name; else own items newest updated first, then starter looks by name, then id. With a
// workflow every item says whether it fits it.
func (s *Service) ListLibrary(ctx context.Context, caller common.Caller, query LibraryQuery) (dto.LibraryItemList, error) {
	out := dto.LibraryItemList{Items: []dto.LibraryItem{}}
	if query.Workflow != "" {
		if err := s.ready(); err != nil {
			return out, err
		}
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var rows []libraryRow
		if query.Source != "starter" {
			var err error
			rows, err = collect[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l
				WHERE ($1 = '' OR l.kind = $1) AND ($2 = '' OR l.item_group = $2)
				AND ($3 = '' OR starts_with(l.handle, $3) OR starts_with(lower(l.name), $3))
				ORDER BY l.updated_at DESC, l.id`, query.Kind, query.Group, query.Q)
			if err != nil {
				return err
			}
		}
		type starter struct {
			item dto.LibraryItem
			look lookItem
		}
		var starters []starter
		keep := func(group, family, name string) bool {
			return (query.Group == "" || group == query.Group) && (query.Family == "" || family == query.Family) &&
				(query.Q == "" || strings.HasPrefix(strings.ToLower(name), query.Q))
		}
		if query.Source != "mine" && (query.Kind == "" || query.Kind == "look") {
			for _, l := range s.registry.Looks(time.Now()) {
				if keep(l.Group, l.Family, l.Name) {
					starters = append(starters, starter{s.starterDTO(l), starterItem(l)})
				}
			}
		}
		if query.Source != "mine" && (query.Kind == "" || query.Kind == "voice") { // API 1.30
			for _, v := range s.registry.Voices(time.Now()) {
				if keep("preset", v.Family, v.Name) {
					starters = append(starters, starter{item: s.starterVoiceDTO(v)})
				}
			}
		}
		slices.SortFunc(starters, func(a, b starter) int {
			return cmp.Or(strings.Compare(a.item.Name, b.item.Name), strings.Compare(a.item.ID, b.item.ID))
		})
		var ids []string
		for _, row := range rows {
			for _, l := range rowLoras(row) {
				ids = append(ids, *l.LoraID)
			}
		}
		names, err := loraNames(ctx, tx, s.registry, caller.ID, ids)
		if err != nil {
			return err
		}
		items := make([]lookItem, 0, len(rows)+len(starters))
		for _, row := range rows {
			out.Items = append(out.Items, s.itemDTO(row, names))
			items = append(items, rowItem(row))
		}
		for _, l := range starters {
			out.Items = append(out.Items, l.item)
			items = append(items, l.look)
		}
		if query.Workflow != "" {
			if err := s.annotateFit(ctx, tx, caller, query.Workflow, out.Items, items); err != nil {
				return err
			}
		}
		return nil
	})
	if query.Q != "" {
		slices.SortStableFunc(out.Items, func(a, b dto.LibraryItem) int {
			handle := func(i dto.LibraryItem) string { return cmp.Or(i.HandleOr(), "\U0010FFFF") }
			return cmp.Or(cmp.Compare(btoi(a.HandleOr() != query.Q), btoi(b.HandleOr() != query.Q)),
				strings.Compare(handle(a), handle(b)), strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
		})
	}
	if len(out.Items) > query.Limit {
		out.Items = out.Items[:query.Limit]
	}
	return out, err
}

// btoi is 1 for true.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// annotateFit sets fits and reason on every item against the workflow's graph and the caller's LoRAs now in its env;
// items are the same items as the resolver reads them.
func (s *Service) annotateFit(ctx context.Context, tx pgx.Tx, caller common.Caller, workflowID string, out []dto.LibraryItem,
	items []lookItem) error {
	nodes, schemas, env, err := s.fitGraph(ctx, tx, workflowID)
	if err != nil {
		return err
	}
	families, err := loraFamilies(ctx, tx, s.registry, caller.ID, env, lookLoraIDs(items))
	if err != nil {
		return err
	}
	for i := range out {
		var reason string
		if out[i].Kind == "voice" {
			reason = voiceFit(nodes, schemas, voiceOf(out[i]))
		} else {
			reason = lookFit(nodes, schemas, out[i].Kind, items[i], families)
		}
		fits := reason == ""
		out[i].Fits, out[i].Reason = &fits, json.RawMessage(`null`)
		if !fits {
			out[i].Reason, _ = json.Marshal(reason)
		}
	}
	return nil
}

// fitGraph is the graph, kinds and env of a workflow the caller can run (a template, or one of theirs as stored); 422
// "unknown workflow <id>" otherwise.
func (s *Service) fitGraph(ctx context.Context, tx pgx.Tx, id string) (map[string]registry.Node,
	map[string]registry.NodeSchema, string, error) {
	unknown := network.NewUnprocessableError("unknown workflow "+id, nil)
	if !utility.IsUUID(id) {
		workflow, ok := s.registry.Workflow(id)
		if !ok {
			return nil, nil, "", unknown
		}
		return workflow.Nodes, workflow.NodeSchemas, workflow.Env, nil
	}
	row, err := readWorkflow(ctx, tx, id, false)
	if errors.Is(err, errNotFound) {
		return nil, nil, "", unknown
	} else if err != nil {
		return nil, nil, "", err
	}
	doc := rowDoc(row)
	return doc.Nodes, s.schemasOf(doc.Nodes), doc.Env, nil
}

// loraNames is the name now of each of ids the owner can use (any live host).
func loraNames(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	usable, err := lora.Usable(ctx, tx, reg, ownerID, "", ids)
	for id, info := range usable {
		out[id] = info.Name
	}
	return out, err
}

// loraFamilies is the detected family of each of ids the owner can use now in env (the fit rule's loras).
func loraFamilies(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID, env string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	usable, err := lora.Usable(ctx, tx, reg, ownerID, env, ids)
	for id, info := range usable {
		out[id] = info.Family
	}
	return out, err
}

// GetLibraryItem returns one of the caller's items.
func (s *Service) GetLibraryItem(ctx context.Context, caller common.Caller, id string) (dto.LibraryItem, error) {
	if !utility.IsUUID(id) {
		return dto.LibraryItem{}, errNotFound
	}
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var err error
		out, err = s.readItem(ctx, tx, caller.ID, id)
		return err
	})
	return out, err
}

// readItem reads one of the caller's items as its DTO.
func (s *Service) readItem(ctx context.Context, tx pgx.Tx, ownerID, id string) (dto.LibraryItem, error) {
	row, err := one[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l WHERE l.id = $1`, id)
	if err != nil {
		return dto.LibraryItem{}, err
	}
	var ids []string
	for _, l := range rowLoras(row) {
		ids = append(ids, *l.LoraID)
	}
	names, err := loraNames(ctx, tx, s.registry, ownerID, ids)
	return s.itemDTO(row, names), err
}

// GetStarterLook returns a starter look or (API 1.30) starter voice a live host publishes (id <family>/<id>).
func (s *Service) GetStarterLook(ctx context.Context, id string) (dto.LibraryItem, error) {
	if err := s.ready(); err != nil {
		return dto.LibraryItem{}, err
	}
	if l, ok := s.registry.Looks(time.Now())[id]; ok {
		return s.starterDTO(l), nil
	}
	if v, ok := s.registry.Voices(time.Now())[id]; ok {
		return s.starterVoiceDTO(v), nil
	}
	return dto.LibraryItem{}, errNotFound
}

// WriteStarterLook answers a PUT or DELETE of a starter look or voice: 409 when a live host publishes it, else 404.
func (s *Service) WriteStarterLook(ctx context.Context, id string) error {
	item, err := s.GetStarterLook(ctx, id)
	switch {
	case err != nil:
		return err
	case item.Kind == "voice":
		return errVoiceReadOnly
	}
	return errReadOnly
}

// CreateLibraryItem makes an element of the caller's gallery images (the gallery's "Make element" too) or a look
// ("Save as look" with from_take).
func (s *Service) CreateLibraryItem(ctx context.Context, caller common.Caller, body *dto.LibraryItemCreate) (dto.LibraryItem, error) {
	if body.Kind == "look" {
		if err := s.ready(); err != nil { // the shared LoRAs a look names are the live hosts'
			return dto.LibraryItem{}, err
		}
	}
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var fromTake *string
		var voice *dto.VoiceInfo
		switch {
		case body.Kind == "voice": // API 1.31: Save as voice, clips; from_take_id is the clip's take
			info, err := s.checkVoice(ctx, tx, caller.ID, &body.LibraryFields, body.FromTake, "")
			if err != nil {
				return err
			}
			voice, fromTake = &info, clipTake(&info)
		case body.FromTake != nil:
			if err := s.fillFromTake(ctx, tx, caller.ID, body); err != nil {
				return err
			}
			fromTake = &body.FromTake.TakeID
		}
		if err := s.checkItem(ctx, tx, caller.ID, "", body.Kind, body.LibraryFields); err != nil {
			return err
		}
		voiceID, err := s.elementVoice(ctx, tx, caller.ID, body.VoiceID, nil)
		if err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM library_items`).Scan(&count); err != nil {
			return err
		} else if count >= MaxLibraryItems {
			return network.NewConflictError(fmt.Sprintf("You have %d library items; delete some first.", MaxLibraryItems), nil)
		}
		loras, err := s.storedLoras(ctx, tx, caller.ID, body.Loras)
		if err != nil {
			return err
		}
		id := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO library_items (id, owner_id, kind, item_group, name, handle, text, images,
			loras, from_take_id, voice, voice_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid[], $9, $10, $11, $12)`, id, caller.ID,
			body.Kind, body.Group, body.Name, body.Handle, *body.Text, takeIDs(body.Images), loras, fromTake, voiceJSON(voice), voiceID); err != nil {
			return err
		}
		out, err = s.readItem(ctx, tx, caller.ID, id)
		return err
	})
	return out, err
}

// fillFromTake is "Save as look": the take must be the caller's, live and made by a run; its typed prompt fills text
// and its usable LoRAs (first per id, at most 4, mode dropped) fill loras, each only when the body left it out.
func (s *Service) fillFromTake(ctx context.Context, tx pgx.Tx, ownerID string, body *dto.LibraryItemCreate) error {
	from := body.FromTake
	var made *gallerydto.MadeWith
	err := tx.QueryRow(ctx, `SELECT made_with FROM takes WHERE id = $1 AND deleted_at IS NULL AND ready`, from.TakeID).Scan(&made)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return network.NewNotFoundError("from_take: take "+from.TakeID+" not found", nil)
	case err != nil:
		return err
	case made == nil:
		return network.NewUnprocessableError("from_take: take "+from.TakeID+" was not made by a run", nil)
	}
	if from.FillText {
		text := ""
		if made.Prompt != nil {
			text = strings.TrimSpace(strings.ReplaceAll(*made.Prompt, "@@", "@")) // as the prompt reached the runner
		}
		if utf8.RuneCountInString(text) > 2000 {
			text = strings.TrimSpace(string([]rune(text)[:2000]))
		}
		body.Text = &text
	}
	if from.FillLoras {
		var ids []string
		for _, l := range made.Loras {
			ids = append(ids, l.ID)
		}
		usable, err := lora.Usable(ctx, tx, s.registry, ownerID, "", ids)
		if err != nil {
			return err
		}
		body.Loras = []dto.LookLoraRef{}
		var seen []string
		for _, l := range made.Loras {
			if _, ok := usable[l.ID]; ok && !slices.Contains(seen, l.ID) && len(seen) < dto.MaxLookLoras {
				seen = append(seen, l.ID)
				strength := dto.ClampStrength(l.Strength) // a take made before API 1.23 may hold -4..4
				body.Loras = append(body.Loras, dto.LookLoraRef{Lora: &dto.LoraRef{LoraID: l.ID}, Strength: &strength})
			}
		}
	}
	if detail := body.Check("look"); detail != "" { // the copied text keeps the text rules
		return network.NewUnprocessableError(detail, nil)
	}
	return nil
}

// storedLoras is library_items.loras for refs (checked): [{lora_id, name, strength}], the name as it is now.
func (s *Service) storedLoras(ctx context.Context, tx pgx.Tx, ownerID string, refs []dto.LookLoraRef) ([]byte, error) {
	var ids []string
	for _, ref := range refs {
		ids = append(ids, ref.Lora.LoraID)
	}
	names, err := loraNames(ctx, tx, s.registry, ownerID, ids)
	out := []lookLora{}
	for _, ref := range refs {
		id := ref.Lora.LoraID
		out = append(out, lookLora{LoraID: &id, Name: names[id], Strength: *ref.Strength})
	}
	data, _ := json.Marshal(out)
	return data, err
}

// ReplaceLibraryItem saves every field of one of the caller's items when body.Version is the stored one (version + 1).
func (s *Service) ReplaceLibraryItem(ctx context.Context, caller common.Caller, id string, body *dto.LibraryItemUpdate) (dto.LibraryItem, error) {
	if !utility.IsUUID(id) {
		return dto.LibraryItem{}, errNotFound
	}
	var out dto.LibraryItem
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var version int
		var kind, kept string
		var storedVoice *string
		if err := tx.QueryRow(ctx, `SELECT version, kind, coalesce(voice->'clip'->>'take_id', ''), voice_id FROM library_items
			WHERE id = $1 FOR UPDATE`, id).Scan(&version, &kind, &kept, &storedVoice); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if detail := body.CheckFor(kind); detail != "" {
			return network.NewUnprocessableError(detail, nil)
		}
		if version != *body.Version {
			return network.NewConflictError(fmt.Sprintf("%s changed since you opened it (now version %d)", kind, version), nil)
		}
		if kind == "look" {
			if err := s.ready(); err != nil {
				return err
			}
		}
		var voice *dto.VoiceInfo
		if kind == "voice" { // API 1.31: its clip may change (from_take_id follows it) or stay, its take deleted or not
			info, err := s.checkVoice(ctx, tx, caller.ID, &body.LibraryFields, nil, kept)
			if err != nil {
				return err
			}
			voice = &info
		}
		if err := s.checkItem(ctx, tx, caller.ID, id, kind, body.LibraryFields); err != nil {
			return err
		}
		voiceID, err := s.elementVoice(ctx, tx, caller.ID, body.VoiceID, storedVoice)
		if err != nil {
			return err
		}
		loras, err := s.storedLoras(ctx, tx, caller.ID, body.Loras)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE library_items l SET item_group = $2, name = $3, handle = $4, text = $5,
			images = $6::uuid[], loras = $7, voice = $8, version = version + 1, updated_at = clock_timestamp(),
			from_take_id = CASE WHEN l.kind = 'voice' THEN $9::uuid ELSE l.from_take_id END, voice_id = $10 WHERE l.id = $1`,
			id, body.Group, body.Name, body.Handle, *body.Text, takeIDs(body.Images), loras, voiceJSON(voice), clipTake(voice),
			voiceID); err != nil {
			return err
		}
		out, err = s.readItem(ctx, tx, caller.ID, id)
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

// checkItem checks an item's images, handle and LoRAs in tx (the caller's), holding a per-user lock until commit so two
// writes can't both take one handle or pass the cap: each image a take of the caller's like a reference row (404
// "images[2]: take <id> not found", 422 "… is not an image" / "… has no size"), the handle no other item's (409); a
// look needs text, an image or a LoRA, each LoRA one the caller can use now (404) and listed once (422).
func (s *Service) checkItem(ctx context.Context, tx pgx.Tx, ownerID, id, kind string, fields dto.LibraryFields) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('library:' || $1::text))`, ownerID); err != nil {
		return err
	}
	if kind == "look" && *fields.Text == "" && len(fields.Images) == 0 && len(fields.Loras) == 0 {
		return network.NewUnprocessableError("a look needs text, an image or a LoRA", nil)
	}
	refs := make([]imageAt, len(fields.Images))
	for i, image := range fields.Images {
		refs[i] = imageAt{Path: fmt.Sprintf("images[%d]", i+1), TakeID: image.TakeID}
	}
	problems, err := vetTakes(ctx, tx, s.store, ownerID, refs, false)
	if err != nil {
		return err
	}
	if detail := firstBad(refs, problems); detail != "" {
		if strings.HasSuffix(detail, " not found") {
			return network.NewNotFoundError(detail, nil)
		}
		return network.NewUnprocessableError(detail, nil)
	}
	if len(fields.Loras) > 0 {
		var ids []string
		for _, l := range fields.Loras {
			ids = append(ids, l.Lora.LoraID)
		}
		usable, err := lora.Usable(ctx, tx, s.registry, ownerID, "", ids)
		if err != nil {
			return err
		}
		for i, l := range fields.Loras {
			info, ok := usable[l.Lora.LoraID]
			if !ok {
				return network.NewNotFoundError(fmt.Sprintf("loras[%d].lora: LoRA %s not found", i+1, l.Lora.LoraID), nil)
			}
			if slices.ContainsFunc(fields.Loras[:i], func(e dto.LookLoraRef) bool { return e.Lora.LoraID == l.Lora.LoraID }) {
				return network.NewUnprocessableError(fmt.Sprintf("loras[%d]: LoRA %s is listed twice", i+1, info.Name), nil)
			}
		}
	}
	if fields.Handle == nil {
		return nil
	}
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM library_items WHERE handle = $1 AND id::text <> $2)`,
		*fields.Handle, id).Scan(&used); err != nil {
		return err
	} else if used {
		return network.NewConflictError("handle @"+*fields.Handle+" is already used", nil)
	}
	return nil
}

// voiceJSON is library_items.voice for a checked voice, null for any other item.
func voiceJSON(v *dto.VoiceInfo) []byte {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

// clipTake is a voice's from_take_id: its clip's take, nil without one (or for any other item).
func clipTake(v *dto.VoiceInfo) *string {
	if v == nil || v.Clip == nil {
		return nil
	}
	return &v.Clip.TakeID
}

// takeIDs are the refs' take ids.
func takeIDs(refs []dto.LibraryImageRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.TakeID
	}
	return out
}

// readLooks reads the looks of ids the owner can use: from the library now (own looks by an explicit owner check, so
// tx may be System, and the live starter looks), or from a batch's copy; images of takes that are no longer the
// owner's ready, undeleted takes are left out either way. Any other id (unknown, an element, another user's, deleted,
// a starter look no live host publishes) is absent.
func readLooks(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID string, ids []string,
	copied *[]lookItem) ([]lookItem, error) {
	out := []lookItem{}
	if len(ids) == 0 {
		return out, nil
	}
	if copied != nil {
		var takes []string
		for _, l := range *copied {
			takes = append(takes, l.Images...)
		}
		rows, _ := tx.Query(ctx, `SELECT id::text FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2
			AND deleted_at IS NULL AND ready`, takes, ownerID)
		live, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, l := range *copied {
			if slices.Contains(ids, l.ID) {
				l.Images = slices.DeleteFunc(slices.Clone(l.Images), func(id string) bool { return !slices.Contains(live, id) })
				out = append(out, l)
			}
		}
		return out, nil
	}
	var own []string
	starters := map[string]registry.LiveLook{}
	for _, id := range ids {
		switch {
		case utility.IsUUID(id):
			own = append(own, strings.ToLower(id))
		case strings.Contains(id, "/") && len(starters) == 0:
			starters = reg.Looks(time.Now())
		}
	}
	if len(own) > 0 {
		rows, err := collect[libraryRow](ctx, tx, `SELECT `+libraryCols+` FROM library_items l WHERE l.owner_id = $1
			AND l.kind = 'look' AND l.id = ANY($2::uuid[]) ORDER BY l.id`, ownerID, own)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, rowItem(row))
		}
	}
	for _, id := range ids {
		if l, ok := starters[id]; ok && !slices.ContainsFunc(out, func(x lookItem) bool { return x.ID == id }) {
			out = append(out, starterItem(l))
		}
	}
	return out, nil
}
