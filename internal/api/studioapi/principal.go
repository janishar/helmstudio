package studioapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/store"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Principal is who a request acts for: one studio and the capabilities its
// manifest declares (docs/design/07-platform-services.md §3).
type Principal struct {
	StudioID     string
	Capabilities []string
	// GroupRunID is the launch the token was minted for. It names the stage
	// directory; empty for the in-process embedded provider.
	GroupRunID string
}

func (p Principal) has(capability string) bool { return slices.Contains(p.Capabilities, capability) }

type principalKey struct{}

// WithPrincipal marks ctx as acting for p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalOf(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// mustPrincipal is for Service methods, which the router only reaches after
// authentication.
func mustPrincipal(ctx context.Context) (Principal, error) {
	p, ok := principalOf(ctx)
	if !ok {
		return Principal{}, unauthenticated("no studio token")
	}
	return p, nil
}

// allow applies an operation's x-helm-capability. "token" and "" need only a
// principal.
func allow(ctx context.Context, capability string) error {
	p, ok := principalOf(ctx)
	if !ok {
		return unauthenticated("no studio token")
	}
	if capability == "" || capability == "token" || p.has(capability) {
		return nil
	}
	return capabilityRequired(capability)
}

// TokenPrefix starts every studio token (07 §3: hs_live_…).
const TokenPrefix = "hs_live_"

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// Tokens mints, resolves and revokes studio tokens (docs/decisions.md M4 Q6).
// Only a token's sha256 is stored; the token itself exists in the studio's
// environment and nowhere else.
type Tokens struct {
	Store *store.Store
	Now   func() time.Time
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Mint issues a token for one launch of a studio. A studio with no
// capabilities gets none (07 §3): Mint returns "" for it.
func (t *Tokens) Mint(ctx context.Context, studioID, groupRunID string, capabilities []string) (string, error) {
	if len(capabilities) == 0 {
		return "", nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("minting a token for %s: %w", studioID, err)
	}
	tok := TokenPrefix + strings.ToLower(tokenEncoding.EncodeToString(raw[:]))
	caps, _ := json.Marshal(capabilities)
	var group any
	if groupRunID != "" {
		group = groupRunID
	}
	err := t.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO studio_tokens (token_sha256, studio_id, group_run_id, capabilities, created_at) VALUES (?, ?, ?, ?, ?)`,
			hashToken(tok), studioID, group, string(caps), t.now().UnixMilli())
		return err
	})
	if err != nil {
		return "", fmt.Errorf("recording the token for %s: %w", studioID, err)
	}
	return tok, nil
}

// RevokeGroup revokes every token minted for a launch. It is called when the
// group stops.
func (t *Tokens) RevokeGroup(ctx context.Context, groupRunID string) error {
	return t.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE studio_tokens SET revoked_at = ? WHERE group_run_id = ? AND revoked_at IS NULL`, t.now().UnixMilli(), groupRunID)
		return err
	})
}

// RevokeAllExcept revokes every live token whose group is not in live. The
// daemon calls it after re-adoption: a group that died with the last daemon
// never had its tokens revoked.
func (t *Tokens) RevokeAllExcept(ctx context.Context, live []string) (int64, error) {
	var n int64
	err := t.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		q := `UPDATE studio_tokens SET revoked_at = ? WHERE revoked_at IS NULL AND group_run_id IS NOT NULL`
		args := []any{t.now().UnixMilli()}
		if len(live) > 0 {
			q += ` AND group_run_id NOT IN (?` + strings.Repeat(",?", len(live)-1) + `)`
			for _, g := range live {
				args = append(args, g)
			}
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err == nil {
			n, _ = res.RowsAffected()
		}
		return err
	})
	return n, err
}

// Resolve returns the principal a live token acts for.
func (t *Tokens) Resolve(ctx context.Context, tok string) (Principal, error) {
	if !strings.HasPrefix(tok, TokenPrefix) {
		return Principal{}, unauthenticated("that is not a helmstudio studio token")
	}
	var p Principal
	var group sql.NullString
	var caps string
	var revoked sql.NullInt64
	err := t.Store.Reader().QueryRowContext(ctx, `SELECT studio_id, group_run_id, capabilities, revoked_at FROM studio_tokens WHERE token_sha256 = ?`,
		hashToken(tok)).Scan(&p.StudioID, &group, &caps, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, unauthenticated("unknown token")
	}
	if err != nil {
		return Principal{}, fmt.Errorf("resolving a token: %w", err)
	}
	if revoked.Valid {
		return Principal{}, unauthenticated("this token was revoked when its studio stopped; the studio's next launch gets a new one")
	}
	p.GroupRunID = group.String
	if err := json.Unmarshal([]byte(caps), &p.Capabilities); err != nil {
		return Principal{}, fmt.Errorf("reading a token's capabilities: %w", err)
	}
	return p, nil
}

// Authenticate is the daemon's Authenticator: a Bearer studio token.
func (t *Tokens) Authenticate(r *http.Request) (context.Context, error) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return nil, unauthenticated("send the studio's HELM_TOKEN as Authorization: Bearer <token>")
	}
	p, err := t.Resolve(r.Context(), strings.TrimSpace(tok))
	if err != nil {
		return nil, err
	}
	return WithPrincipal(r.Context(), p), nil
}

// Launcher is the principal the daemon answers its own page's gallery under
// (03 §10, docs/decisions.md 2026-09-19). It is not a studio, and its studio
// id is the empty string, which no studio id can be: `scope=self` finds
// nothing, an item can never be recorded as the launcher's, and what it may
// read comes from gallery.read_all alone — which is the whole of what the
// screens are, and what the decision entry says they cost. A sequence it
// creates is owned by nobody, which is what the launcher owning it is.
func Launcher() Principal {
	return Principal{Capabilities: []string{
		string(helm.CapabilityGallery),
		string(helm.CapabilityGalleryReadAll),
		string(helm.CapabilityAssets),
		string(helm.CapabilityTimeline),
	}}
}

// Fixed is the embedded provider's Authenticator: every request acts for p.
func Fixed(p Principal) Authenticator {
	return func(r *http.Request) (context.Context, error) {
		return WithPrincipal(r.Context(), p), nil
	}
}

var _ = helm.APIVersion
