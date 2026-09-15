package studioapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/media"
	"github.com/janishar/helmstudio/internal/store"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Default quotas when a manifest declares none (docs/decisions.md M4 Q20).
const (
	DefaultRecordsQuota = 100000
	DefaultKVBytesQuota = 8 << 20
)

// DefaultUploadLimit bounds one POST /assets body. The contract leaves the
// value open (first review #16); this is the judgement call until it is set.
const DefaultUploadLimit = 4 << 30

// Studios is what the service needs to know about studios beyond a request's
// principal: whether a handoff target exists, and a studio's manifest for its
// quotas and record indexes.
type Studios interface {
	Manifest(studioID string) (*manifest.Manifest, bool)
}

// Paths places the per-studio directories the API names.
type Paths interface {
	// Stage is where a launch's studio writes files for :adopt.
	Stage(p Principal) string
	// Data is the studio's persistent {data} directory.
	Data(studioID string) string
	// Logs is the logs root.
	Logs() string
}

// Config is everything a Service needs.
type Config struct {
	Store   *store.Store
	Media   *media.Engine
	Paths   Paths
	Studios Studios
	// Provider is "daemon" or "embedded", reported by /me.
	Provider string
	// Version is the daemon's or helm dev's build version, reported by /me.
	Version string
	// FreeBytes reports free space on the volume holding path, for the upload
	// check. Nil skips the check.
	FreeBytes func(path string) (uint64, error)
	// UploadLimit bounds one upload. Zero means DefaultUploadLimit.
	UploadLimit int64
	Now         func() time.Time
	Logf        func(string, ...any)
}

// Service implements every studio-api operation against helm.db and the media
// engine. It is the only place the contract is enforced.
type Service struct {
	cfg    Config
	st     *store.Store
	media  *media.Engine
	events *Broker

	// blobs is held by ingest from placing a blob to committing its row, and
	// by reclaim from committing its deletions to removing their files, so an
	// upload of the same bytes cannot land between reclaim's commit and its
	// removal and lose its blob (second review #3). The daemon is the only
	// process that opens helm.db, so a lock in the process is enough.
	blobs sync.Mutex
	// afterReclaimCommit is for tests: it runs between reclaim's commit and
	// its file removal.
	afterReclaimCommit func()
}

var _ Server = (*Service)(nil)

// NewService returns a service over cfg.
func NewService(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.UploadLimit == 0 {
		cfg.UploadLimit = DefaultUploadLimit
	}
	return &Service{cfg: cfg, st: cfg.Store, media: cfg.Media, events: NewBroker(1024)}
}

// Events is the service's event broker.
func (s *Service) Events() *Broker { return s.events }

func (s *Service) now() time.Time { return s.cfg.Now() }

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNullMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

func newETag() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) newID() string { return store.NewID(s.now()) }

// ---------------------------------------------------------------- quotas

func (s *Service) quotas(studioID string) (records, kvBytes int64) {
	records, kvBytes = DefaultRecordsQuota, DefaultKVBytesQuota
	if s.cfg.Studios == nil {
		return
	}
	if m, ok := s.cfg.Studios.Manifest(studioID); ok && m.Storage != nil {
		if m.Storage.Quota.Records > 0 {
			records = m.Storage.Quota.Records
		}
		if m.Storage.Quota.KVBytes > 0 {
			kvBytes = m.Storage.Quota.KVBytes
		}
	}
	return
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func kvBytesUsed(ctx context.Context, q querier, studioID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT sum(length(CAST(doc AS BLOB))) FROM kv WHERE studio_id = ?1), 0) +
		COALESCE((SELECT sum(length(CAST(state AS BLOB))) FROM sessions WHERE studio_id = ?1 AND deleted_at IS NULL), 0)`, studioID).Scan(&n)
	return n, err
}

func recordsUsed(ctx context.Context, q querier, studioID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE studio_id = ? AND deleted_at IS NULL`, studioID).Scan(&n)
	return n, err
}

// checkKVBytes refuses a write that would put the studio over kv_bytes, given
// the bytes the write removes and adds.
func (s *Service) checkKVBytes(ctx context.Context, tx *sql.Tx, studioID string, removed, added int64) error {
	if studioID == "" {
		return nil
	}
	_, limit := s.quotas(studioID)
	used, err := kvBytesUsed(ctx, tx, studioID)
	if err != nil {
		return err
	}
	if after := used - removed + added; added > removed && after > limit {
		return quotaExceeded("kv_bytes", limit, used)
	}
	return nil
}

// ---------------------------------------------------------------- /me

func (s *Service) MeGet(ctx context.Context) (*helm.Me, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	recLimit, kvLimit := s.quotas(p.StudioID)
	recUsed, err := recordsUsed(ctx, s.st.Reader(), p.StudioID)
	if err != nil {
		return nil, err
	}
	kvUsed, err := kvBytesUsed(ctx, s.st.Reader(), p.StudioID)
	if err != nil {
		return nil, err
	}
	caps := make([]helm.Capability, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		caps = append(caps, helm.Capability(c))
	}
	return &helm.Me{
		StudioID:     p.StudioID,
		Capabilities: caps,
		Quota: helm.MeQuota{
			Records: helm.QuotaUse{Limit: recLimit, Used: recUsed},
			KVBytes: helm.QuotaUse{Limit: kvLimit, Used: kvUsed},
		},
		Paths:         helm.MePaths{Stage: s.cfg.Paths.Stage(p), Data: s.cfg.Paths.Data(p.StudioID)},
		Provider:      s.cfg.Provider,
		APIVersion:    helm.APIVersion,
		DaemonVersion: s.cfg.Version,
	}, nil
}

// ---------------------------------------------------------------- cursors

// cursor is an opaque keyset position plus a digest of the filters it was
// issued for, so a cursor replayed against other filters is refused.
type cursor struct {
	Filters string `json:"f"`
	Values  []any  `json:"v"`
}

func filterDigest(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func encodeCursor(filters string, values ...any) *string {
	b, _ := json.Marshal(cursor{Filters: filters, Values: values})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(raw *string, filters string, n int) ([]any, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(*raw)
	if err != nil {
		return nil, badCursor()
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var c cursor
	if dec.Decode(&c) != nil || c.Filters != filters || len(c.Values) != n {
		return nil, badCursor()
	}
	for i, v := range c.Values {
		c.Values[i] = normalizeNumbers(v)
	}
	return c.Values, nil
}

func pageLimit(limit *int64) int {
	if limit == nil {
		return 50
	}
	return int(*limit)
}

func cursorInt(v any) (int64, bool) {
	n, ok := v.(int64)
	return n, ok
}

func cursorString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// ---------------------------------------------------------------- merge patch

// mergePatch applies an RFC 7396 merge patch to target.
func mergePatch(target map[string]any, patch map[string]any) map[string]any {
	if target == nil {
		target = map[string]any{}
	}
	for k, v := range patch {
		switch pv := v.(type) {
		case nil:
			delete(target, k)
		case map[string]any:
			cur, _ := target[k].(map[string]any)
			target[k] = mergePatch(cur, pv)
		default:
			target[k] = v
		}
	}
	return target
}

func decodeDoc(raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("a stored document is not a JSON object: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return normalizeNumbers(m).(map[string]any), nil
}

func encodeDoc(m map[string]any) (string, error) {
	if m == nil {
		m = map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", badRequest("the document cannot be stored as JSON: %v", err)
	}
	return string(b), nil
}

// ---------------------------------------------------------------- kv

const sharedNS = "shared"

// kvOwner is the studio_id a namespace's rows belong to: ” for the shared
// namespace, which needs kv.shared (Q21).
func kvOwner(p Principal, ns string) (string, error) {
	if ns != sharedNS {
		return p.StudioID, nil
	}
	if !p.has(string(helm.CapabilityKVShared)) {
		return "", capabilityRequired(string(helm.CapabilityKVShared))
	}
	return "", nil
}

func (s *Service) KVList(ctx context.Context, ns string, params helm.KVListParams) (*helm.KVKeyPage, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := kvOwner(p, ns)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if params.Prefix != nil {
		prefix = *params.Prefix
	}
	digest := filterDigest("kv", owner, ns, prefix)
	after, err := decodeCursor(params.Cursor, digest, 1)
	if err != nil {
		return nil, err
	}
	q := `SELECT key, length(CAST(doc AS BLOB)), updated_at FROM kv WHERE studio_id = ? AND ns = ? AND substr(key, 1, length(?)) = ?`
	args := []any{owner, ns, prefix, prefix}
	if after != nil {
		k, ok := cursorString(after[0])
		if !ok {
			return nil, badCursor()
		}
		q += ` AND key > ?`
		args = append(args, k)
	}
	limit := pageLimit(params.Limit)
	q += ` ORDER BY key LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &helm.KVKeyPage{Items: []helm.KVKey{}}
	for rows.Next() {
		var k helm.KVKey
		var updated int64
		if err := rows.Scan(&k.Key, &k.Bytes, &updated); err != nil {
			return nil, err
		}
		k.UpdatedAt = fromMS(updated)
		page.Items = append(page.Items, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = encodeCursor(digest, page.Items[limit-1].Key)
	}
	return page, nil
}

type kvRow struct {
	doc     string
	etag    string
	updated int64
}

func readKV(ctx context.Context, q querier, owner, ns, key string) (*kvRow, error) {
	var r kvRow
	err := q.QueryRowContext(ctx, `SELECT doc, etag, updated_at FROM kv WHERE studio_id = ? AND ns = ? AND key = ?`, owner, ns, key).Scan(&r.doc, &r.etag, &r.updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

func kvDoc(ns, key string, r *kvRow) (*helm.KVDoc, error) {
	doc, err := decodeDoc(r.doc)
	if err != nil {
		return nil, err
	}
	return &helm.KVDoc{NS: ns, Key: key, Doc: doc, ETag: r.etag, UpdatedAt: fromMS(r.updated)}, nil
}

func (s *Service) KVGet(ctx context.Context, ns, key string) (*helm.KVDoc, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := kvOwner(p, ns)
	if err != nil {
		return nil, err
	}
	r, err := readKV(ctx, s.st.Reader(), owner, ns, key)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, notFound("no document %s/%s", ns, key)
	}
	return kvDoc(ns, key, r)
}

// kvWrite reads the current document, applies change and stores the result,
// all in one transaction, checking If-Match and the kv_bytes quota.
func (s *Service) kvWrite(ctx context.Context, ns, key string, ifMatch *string, mustExist bool, change func(cur map[string]any) (map[string]any, error)) (*helm.KVDoc, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := kvOwner(p, ns)
	if err != nil {
		return nil, err
	}
	var out *helm.KVDoc
	err = s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := readKV(ctx, tx, owner, ns, key)
		if err != nil {
			return err
		}
		if ifMatch != nil && (cur == nil || unquoteETag(*ifMatch) != cur.etag) {
			return etagMismatch(ns + "/" + key)
		}
		if cur == nil && mustExist {
			return notFound("no document %s/%s", ns, key)
		}
		var existing map[string]any
		var removed int64
		if cur != nil {
			if existing, err = decodeDoc(cur.doc); err != nil {
				return err
			}
			removed = int64(len(cur.doc))
		}
		next, err := change(existing)
		if err != nil {
			return err
		}
		doc, err := encodeDoc(next)
		if err != nil {
			return err
		}
		if err := s.checkKVBytes(ctx, tx, p.StudioID, removed, int64(len(doc))); err != nil {
			return err
		}
		row := &kvRow{doc: doc, etag: newETag(), updated: ms(s.now())}
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv (studio_id, ns, key, doc, etag, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (studio_id, ns, key) DO UPDATE SET doc = excluded.doc, etag = excluded.etag, updated_at = excluded.updated_at`,
			owner, ns, key, row.doc, row.etag, row.updated); err != nil {
			return err
		}
		out, err = kvDoc(ns, key, row)
		return err
	})
	return out, err
}

func (s *Service) KVPut(ctx context.Context, ns, key string, body map[string]any, params helm.KVPutParams) (*helm.KVDoc, error) {
	return s.kvWrite(ctx, ns, key, params.IfMatch, false, func(map[string]any) (map[string]any, error) { return body, nil })
}

func (s *Service) KVPatch(ctx context.Context, ns, key string, body map[string]any, params helm.KVPatchParams) (*helm.KVDoc, error) {
	return s.kvWrite(ctx, ns, key, params.IfMatch, true, func(cur map[string]any) (map[string]any, error) {
		return mergePatch(cur, body), nil
	})
}

func (s *Service) KVDelete(ctx context.Context, ns, key string, params helm.KVDeleteParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	owner, err := kvOwner(p, ns)
	if err != nil {
		return err
	}
	return s.st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := readKV(ctx, tx, owner, ns, key)
		if err != nil {
			return err
		}
		if cur == nil {
			return notFound("no document %s/%s", ns, key)
		}
		if params.IfMatch != nil && unquoteETag(*params.IfMatch) != cur.etag {
			return etagMismatch(ns + "/" + key)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE studio_id = ? AND ns = ? AND key = ?`, owner, ns, key)
		return err
	})
}

// ---------------------------------------------------------------- helpers

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sqlIdent(parts ...string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('_')
		}
		for _, r := range p {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
	}
	return b.String()
}

func extOf(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 12 || strings.ContainsAny(ext, " /\\") {
		return ""
	}
	return ext
}
