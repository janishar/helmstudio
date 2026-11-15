package lora

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Caps of a user's own LoRAs.
const (
	MaxBytes      = 2147483648  // one file
	MaxLoras      = 50          // per user, any state
	MaxTotalBytes = 21474836480 // per user, pending + ready
	UploadLink    = 15 * time.Minute
	PendingTTL    = time.Hour      // a LoRA still pending this long after its creation is deleted
	Grace         = 24 * time.Hour // a deleted LoRA's object stays this long
)

// ContentType is the only type a LoRA upload has.
const ContentType = "application/octet-stream"

var (
	errNotFound = network.NewNotFoundError("not found", nil)
	errShared   = network.NewConflictError("shared LoRAs are managed on their host", nil)
	errStarting = network.NewServiceUnavailableError("studio is starting; try again", nil)
	familyText  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	sharedID    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ownID       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// IsID reports whether id has a LoRA id's form: a shared LoRA's SHA-256 or an own LoRA's UUID (lower-case hex).
func IsID(id string) bool { return sharedID.MatchString(id) || ownID.MatchString(id) }

// Key is where an own LoRA's file lives.
func Key(ownerID, id string) string {
	return common.OwnerPrefix(ownerID) + "loras/" + id + ".safetensors"
}

// Lora is a LoRA in the library (openapi Lora).
type Lora struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"` // shared | mine
	Name      string    `json:"name"`
	Family    string    `json:"family"`
	Rank      *int      `json:"rank"`
	SizeBytes int64     `json:"size_bytes"`
	KeyStyle  *string   `json:"key_style"`
	Licence   *string   `json:"licence"`
	SHA256    *string   `json:"sha256"`
	Hosts     []string  `json:"hosts"`
	State     string    `json:"state"`
	Reason    *string   `json:"reason"`
	ThumbURL  *string   `json:"thumb_url"`
	CreatedAt time.Time `json:"created_at"`
}

// List is GET /loras.
type List struct {
	Loras []Lora `json:"loras"`
}

// Patch is PATCH /loras/{id}.
type Patch struct {
	Name string `json:"name"`
}

// UploadCreate is POST /loras.
type UploadCreate struct {
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
}

// Upload is POST /loras's answer.
type Upload struct {
	Lora        Lora      `json:"lora"`
	UploadURL   string    `json:"upload_url"`
	ContentType string    `json:"content_type"`
	Bytes       int64     `json:"bytes"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Query is GET /loras's filters.
type Query struct {
	Source, Family, Q string
}

// row is a loras row.
type row struct {
	ID, OwnerID, Name, Key string
	SizeBytes              int64
	State                  string
	Reason                 *string
	Family                 string
	Rank                   *int
	KeyStyle, Licence      *string
	SHA256                 *string
	CreatedAt              time.Time
}

const cols = `id::text, owner_id::text, name, key, size_bytes, state, reason, family, rank, key_style, licence, sha256, created_at`

// Service owns the loras table. signer signs upload and thumb links, store holds the files, registry the shared ones.
type Service struct {
	db       *pgxpool.Pool
	signer   *common.Signer
	store    storage.Store
	registry *registry.Registry
}

// NewService returns the service.
func NewService(db *pgxpool.Pool, signer *common.Signer, store storage.Store, reg *registry.Registry) *Service {
	return &Service{db: db, signer: signer, store: store, registry: reg}
}

// tx runs fn as the caller, a plain user: an admin sees only their own LoRAs here too.
func (s *Service) tx(ctx context.Context, caller common.Caller, fn func(tx pgx.Tx) error) error {
	return common.WithUser(ctx, s.db, common.Caller{ID: caller.ID}, fn)
}

// own reads the owner's undeleted LoRAs (all with ids nil), explicitly scoped: tx may be the caller's or System.
func own(ctx context.Context, tx pgx.Tx, ownerID string, ids []string) ([]row, error) {
	query := `SELECT ` + cols + ` FROM loras WHERE owner_id = $1 AND deleted_at IS NULL`
	args := []any{ownerID}
	if ids != nil {
		query += ` AND id = ANY($2::uuid[])`
		args = append(args, ids)
	}
	rows, _ := tx.Query(ctx, query+` ORDER BY created_at DESC, id`, args...)
	return pgx.CollectRows(rows, pgx.RowToStructByPos[row])
}

// ownDTO maps a row.
func ownDTO(r row) Lora {
	return Lora{ID: r.ID, Source: "mine", Name: r.Name, Family: r.Family, Rank: r.Rank, SizeBytes: r.SizeBytes,
		KeyStyle: r.KeyStyle, Licence: r.Licence, SHA256: r.SHA256, Hosts: []string{}, State: r.State, Reason: r.Reason,
		CreatedAt: r.CreatedAt.UTC()}
}

// sharedDTO maps a shared LoRA of the live hosts.
func sharedDTO(l registry.LiveLora) Lora {
	rank, style, sha := l.Rank, l.KeyStyle, l.ID
	return Lora{ID: l.ID, Source: "shared", Name: l.Name, Family: l.Family, Rank: &rank, SizeBytes: l.Bytes,
		KeyStyle: &style, Licence: l.Licence, SHA256: &sha, Hosts: l.Hosts, State: "ready", CreatedAt: l.ModifiedAt.UTC()}
}

// thumbs signs, per LoRA id, the caller's oldest take made with it (RLS: never another user's take).
// ponytail: scans the caller's takes that carry made_with.loras; an index on them if libraries grow large.
func (s *Service) thumbs(ctx context.Context, tx pgx.Tx, out []Lora) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]string, len(out))
	for i, l := range out {
		ids[i] = l.ID
	}
	rows, _ := tx.Query(ctx, `SELECT DISTINCT ON (l->>'id') l->>'id', t.key FROM takes t,
		jsonb_array_elements(CASE WHEN jsonb_typeof(t.made_with->'loras') = 'array' THEN t.made_with->'loras' ELSE '[]' END) l
		WHERE t.deleted_at IS NULL AND t.ready AND t.kind = 'image' AND l->>'id' = ANY($1)
		ORDER BY l->>'id', t.created_at, t.id`, ids)
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct{ ID, Key string }])
	for _, f := range found {
		i := slices.IndexFunc(out, func(l Lora) bool { return l.ID == f.ID })
		out[i].ThumbURL = s.signer.SignPtr(&f.Key)
	}
	return err
}

// matches reports whether a LoRA passes the list's filters.
func (q Query) matches(l Lora) bool {
	name := strings.ToLower(l.Name)
	for _, word := range strings.Fields(strings.ToLower(q.Q)) {
		if !strings.Contains(name, word) {
			return false
		}
	}
	return (q.Source == "" || q.Source == l.Source) && (q.Family == "" || q.Family == l.Family)
}

// List is the caller's own LoRAs (newest first, every state), then the live hosts' shared ones by name.
func (s *Service) List(ctx context.Context, caller common.Caller, q Query) (List, error) {
	out := List{Loras: []Lora{}}
	switch {
	case q.Source != "" && q.Source != "shared" && q.Source != "mine":
		return out, network.NewUnprocessableError("source must be shared or mine", nil)
	case q.Family != "" && !familyText.MatchString(q.Family):
		return out, network.NewUnprocessableError("family must be a family id", nil)
	case utf8.RuneCountInString(q.Q) > 80:
		return out, network.NewUnprocessableError("q must be at most 80 characters", nil)
	case !s.registry.Ready():
		return out, errStarting
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := own(ctx, tx, caller.ID, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if l := ownDTO(r); q.matches(l) {
				out.Loras = append(out.Loras, l)
			}
		}
		live := s.registry.Loras(time.Now(), "")
		var shared []Lora
		for _, l := range live {
			if dto := sharedDTO(l); q.matches(dto) {
				shared = append(shared, dto)
			}
		}
		slices.SortFunc(shared, func(a, b Lora) int {
			if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		out.Loras = append(out.Loras, shared...)
		return s.thumbs(ctx, tx, out.Loras)
	})
	return out, err
}

// Get is one shared LoRA a live host lists, or one of the caller's own in any state.
func (s *Service) Get(ctx context.Context, caller common.Caller, id string) (Lora, error) {
	if !s.registry.Ready() {
		return Lora{}, errStarting
	}
	var out Lora
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var err error
		if out, err = s.find(ctx, tx, caller, id); err != nil {
			return err
		}
		list := []Lora{out}
		err = s.thumbs(ctx, tx, list)
		out = list[0]
		return err
	})
	return out, err
}

// find is a LoRA the caller can see: shared on a live host, or the caller's own; errNotFound otherwise.
func (s *Service) find(ctx context.Context, tx pgx.Tx, caller common.Caller, id string) (Lora, error) {
	if sharedID.MatchString(id) {
		if live, ok := s.registry.Loras(time.Now(), "")[id]; ok {
			return sharedDTO(live), nil
		}
		return Lora{}, errNotFound
	}
	if !ownID.MatchString(id) {
		return Lora{}, errNotFound
	}
	rows, err := own(ctx, tx, caller.ID, []string{id})
	if err != nil || len(rows) == 0 {
		return Lora{}, cmpErr(err, errNotFound)
	}
	return ownDTO(rows[0]), nil
}

// cmpErr is err, or fallback when err is nil.
func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// uploadName is a file name's LoRA name: the base name without .safetensors (any case), control characters left out,
// trimmed, cut to 80 characters; "" when the name does not end in .safetensors.
func uploadName(filename string) (string, bool) {
	base := filename[strings.LastIndexAny(filename, `/\`)+1:]
	if !strings.HasSuffix(strings.ToLower(base), ".safetensors") {
		return "", false
	}
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, base[:len(base)-len(".safetensors")]))
	if name == "" {
		name = "lora"
	}
	return string([]rune(name)[:min(80, utf8.RuneCountInString(name))]), true
}

// CreateUpload stores a pending LoRA of the caller's and signs the PUT link of its key.
func (s *Service) CreateUpload(ctx context.Context, caller common.Caller, body *UploadCreate) (Upload, error) {
	name, ok := uploadName(body.Filename)
	switch {
	case !ok || utf8.RuneCountInString(body.Filename) > 255:
		return Upload{}, network.NewUnprocessableError("only .safetensors files can be uploaded", nil)
	case body.Bytes < 1:
		return Upload{}, network.NewUnprocessableError("bytes must be at least 1", nil)
	case body.Bytes > MaxBytes:
		return Upload{}, network.NewUnprocessableError("file is larger than 2 GB", nil)
	}
	id := utility.NewID()
	key := Key(caller.ID, id)
	var created row
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		// one create at a time per user, so concurrent creates can't all pass the caps
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('loras:' || $1::text))`, caller.ID); err != nil {
			return err
		}
		var count int
		var used int64
		// a deleted LoRA's bytes count until the sweep removes its object (Grace), so delete and re-upload can't grow
		// the store past the cap
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE deleted_at IS NULL),
			coalesce(sum(size_bytes) FILTER (WHERE state <> 'rejected'), 0)
			FROM loras WHERE owner_id = $1`, caller.ID).Scan(&count, &used); err != nil {
			return err
		}
		switch {
		case count >= MaxLoras:
			return network.NewConflictError(fmt.Sprintf("You have %d LoRAs; delete some first.", MaxLoras), nil)
		case used+body.Bytes > MaxTotalBytes:
			return network.NewConflictError("Your LoRAs would use more than 20 GB; delete some first.", nil)
		}
		rows, _ := tx.Query(ctx, `INSERT INTO loras (id, owner_id, name, key, size_bytes) VALUES ($1, $2, $3, $4, $5)
			RETURNING `+cols, id, caller.ID, name, key, body.Bytes)
		var err error
		created, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
		return err
	})
	if err != nil {
		return Upload{}, err
	}
	expiry := created.CreatedAt.Add(UploadLink)
	return Upload{Lora: ownDTO(created), UploadURL: s.signer.SignPut(key, body.Bytes, ContentType, expiry),
		ContentType: ContentType, Bytes: body.Bytes, ExpiresAt: expiry.UTC()}, nil
}

// Complete checks an uploaded LoRA (contracts/README.md "LoRA upload checks"), hashes it and detects its family:
// ready, or rejected with the reason (a 422). A LoRA that has ended is answered as it ended. 409 while the object is
// missing. The object is read once, outside any transaction (a 2 GB file takes seconds).
func (s *Service) Complete(ctx context.Context, caller common.Caller, id string) (Lora, error) {
	if !ownID.MatchString(id) {
		return Lora{}, errNotFound
	}
	if !s.registry.Ready() { // family detection needs the published lora blocks
		return Lora{}, errStarting
	}
	var r row
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT `+cols+` FROM loras WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
			AND (state <> 'pending' OR created_at > clock_timestamp() - make_interval(secs => $3))`, id, caller.ID,
			PendingTTL.Seconds())
		var err error
		if r, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row]); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	})
	if err != nil {
		return Lora{}, err
	}
	if r.State != "pending" {
		return ended(r)
	}
	found, detail, err := s.inspect(ctx, r)
	if err != nil {
		return Lora{}, err
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		var rows pgx.Rows
		if detail != "" {
			rows, _ = tx.Query(ctx, `UPDATE loras SET state = 'rejected', reason = $2 WHERE id = $1 AND state = 'pending'
				AND deleted_at IS NULL RETURNING `+cols, id, detail)
		} else {
			rows, _ = tx.Query(ctx, `UPDATE loras SET state = 'ready', family = $2, rank = $3, key_style = $4, licence = $5,
				sha256 = $6 WHERE id = $1 AND state = 'pending' AND deleted_at IS NULL RETURNING `+cols,
				id, found.Family, found.Rank, found.KeyStyle, found.Licence, found.SHA256)
		}
		updated, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
		if errors.Is(err, pgx.ErrNoRows) { // ended (or deleted) by a call that ran alongside
			rows, _ = tx.Query(ctx, `SELECT `+cols+` FROM loras WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`,
				id, caller.ID)
			if updated, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row]); errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
		}
		r = updated
		return err
	})
	if err != nil {
		return Lora{}, err
	}
	return ended(r)
}

// ended answers a LoRA that has left pending: ready, or the 422 of its reason.
func ended(r row) (Lora, error) {
	if r.State == "rejected" && r.Reason != nil {
		return Lora{}, network.NewUnprocessableError(*r.Reason, nil)
	}
	return ownDTO(r), nil
}

// inspect reads the uploaded object once: its size, header and the upload checks, its SHA-256 (the rest streamed
// through the hash, never held), its family. detail is the failed check; a missing object is a 409.
func (s *Service) inspect(ctx context.Context, r row) (row, string, error) {
	unreachable := func(err error) error {
		return network.NewServiceUnavailableError("could not reach the object store; try again", err)
	}
	info, err := s.store.Stat(ctx, r.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return r, "", network.NewConflictError("upload not received", nil)
	} else if err != nil {
		return r, "", unreachable(err)
	}
	if info.Size != r.SizeBytes {
		return r, "size differs from the declared bytes", nil
	}
	body, err := s.store.Get(ctx, r.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return r, "", network.NewConflictError("upload not received", nil)
	} else if err != nil {
		return r, "", unreachable(err)
	}
	defer body.Close()
	hash := sha256.New()
	stream := io.TeeReader(body, hash)
	header, detail, err := ReadHeader(stream, info.Size)
	if err != nil {
		return r, "", unreachable(err)
	}
	if detail != "" {
		return r, detail, nil
	}
	blocks := s.registry.LoraBlocks()
	style, rank, detail := Check(header, Ignores(blocks))
	if detail != "" {
		return r, detail, nil
	}
	if _, err := io.Copy(hash, body); err != nil {
		return r, "", unreachable(err)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	r.Family, r.Rank, r.KeyStyle, r.Licence, r.SHA256 = Detect(r.Name, header, blocks), &rank, &style, Licence(header.Metadata), &sum
	return r, "", nil
}

// Rename sets an own LoRA's name; 409 for a shared one.
func (s *Service) Rename(ctx context.Context, caller common.Caller, id string, body *Patch) (Lora, error) {
	var out Lora
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		found, err := s.find(ctx, tx, caller, id)
		switch {
		case err != nil:
			return err
		case found.Source == "shared":
			return errShared
		}
		name := strings.TrimSpace(body.Name)
		switch {
		case name == "" || utf8.RuneCountInString(name) > 80:
			return network.NewUnprocessableError("name must be 1-80 characters", nil)
		case strings.IndexFunc(name, unicode.IsControl) >= 0:
			return network.NewUnprocessableError("name must not contain control characters", nil)
		}
		rows, _ := tx.Query(ctx, `UPDATE loras SET name = $2 WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL
			RETURNING `+cols, id, name, caller.ID)
		r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		list := []Lora{ownDTO(r)}
		err = s.thumbs(ctx, tx, list)
		out = list[0]
		return err
	})
	return out, err
}

// Delete deletes an own LoRA at once (never refused); the sweep removes its object after Grace. 409 for a shared one.
func (s *Service) Delete(ctx context.Context, caller common.Caller, id string) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		found, err := s.find(ctx, tx, caller, id)
		switch {
		case err != nil:
			return err
		case found.Source == "shared":
			return errShared
		}
		_, err = tx.Exec(ctx, `UPDATE loras SET deleted_at = clock_timestamp() WHERE id = $1 AND owner_id = $2
			AND deleted_at IS NULL`, id, caller.ID)
		return err
	})
}

// ------------------------------------------------------------------ what runs use

// Info is a LoRA a run may use, as studio resolves it.
type Info struct {
	ID, Source, Name, Family, SHA256 string // Source shared | mine
	Bytes                            int64
}

// Usable returns, of ids, the LoRAs owner can use now in env: own ones ready and not deleted (an explicit owner check,
// so tx may be System), shared ones a live host of env lists. Any other id is absent.
func Usable(ctx context.Context, tx pgx.Tx, reg *registry.Registry, ownerID, env string, ids []string) (map[string]Info, error) {
	out := map[string]Info{}
	var owned []string
	live := map[string]registry.LiveLora{}
	for _, id := range ids {
		switch {
		case ownID.MatchString(id):
			owned = append(owned, id)
		case sharedID.MatchString(id) && len(live) == 0:
			live = reg.Loras(time.Now(), env)
		}
	}
	for _, id := range ids {
		if l, ok := live[id]; ok {
			out[id] = Info{ID: id, Source: "shared", Name: l.Name, Family: l.Family, SHA256: l.ID, Bytes: l.Bytes}
		}
	}
	if len(owned) == 0 {
		return out, nil
	}
	rows, err := own(ctx, tx, ownerID, owned)
	for _, r := range rows {
		if r.State == "ready" && r.SHA256 != nil {
			out[r.ID] = Info{ID: r.ID, Source: "mine", Name: r.Name, Family: r.Family, SHA256: *r.SHA256, Bytes: r.SizeBytes}
		}
	}
	return out, err
}

// Ready returns which of the owner's ids are ready and not deleted (explicit owner check; tx may be System).
func Ready(ctx context.Context, tx pgx.Tx, ownerID string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := own(ctx, tx, ownerID, ids)
	for _, r := range rows {
		out[r.ID] = r.State == "ready"
	}
	return out, err
}

// Owned returns which of ids are the caller's own LoRAs, deleted or not (RLS as the caller, a plain user): GET /hosts
// names these; any other own id is another user's and shows only as a size.
func Owned(ctx context.Context, tx pgx.Tx, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, _ := tx.Query(ctx, `SELECT id::text FROM loras WHERE id = ANY($1::uuid[])`, ids)
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, id := range found {
		out[id] = true
	}
	return out, err
}

// Used is one LoRA row of a lora-stack node as a run resolved it when it was made (runs.loras): the take's
// made_with.loras entry plus the file size the command carries.
type Used struct {
	Node     string  `json:"node"`
	ID       string  `json:"id"`
	Source   string  `json:"source"` // shared | mine
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
	Mode     string  `json:"mode"` // adapter | bake
	SHA256   string  `json:"sha256"`
	Bytes    int64   `json:"bytes"`
}
