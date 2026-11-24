// Package auth: sign-in, refresh rotation, sign-out, passwords, token validation, revocation events.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/auth_service/api/auth/dto"
	"github.com/helmstudio/auth_service/api/auth/model"
	"github.com/helmstudio/auth_service/config"
	"github.com/helmstudio/auth_service/utils"
)

// Errors the service returns, each with the status and detail a client sees.
var (
	ErrInvalidLogin  = network.NewUnauthorizedError("Wrong email or password.", nil)
	ErrAccountOff    = network.NewUnauthorizedError("This account is off. Ask your admin.", nil) // right password only
	ErrWrongPassword = network.NewUnauthorizedError("current password is incorrect", nil)
	ErrTooMany       = network.NewTooManyRequestsError("too many attempts, try again later", nil)
	ErrConflict      = network.NewConflictError("email already registered", nil)
	ErrUnauthorized  = model.ErrUnauthorized
	ErrNotFound      = network.NewNotFoundError("not found", nil)
)

// RevokedSubject is the NATS subject of the revocation event.
const RevokedSubject = "evt.auth.revoked"

// Service holds the user, session and password logic. Key signs access tokens and Kid names it in their header.
// Publish sends revocation events and may be nil.
type Service struct {
	DB         *pgxpool.Pool
	Key        *rsa.PrivateKey
	Kid        string
	Issuer     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Publish    func(subject string, data []byte) error
	EmailLimit *utils.Limiter // failed sign-ins per email and client IP, and wrong current passwords per session
	IPLimit    *utils.Limiter // failed sign-ins per client IP
}

// NewService returns the service. Failed sign-ins are limited to 5 per email from one IP and 50 per IP in 15 minutes;
// the 5th wrong current password of a session in 15 minutes revokes that session.
func NewService(db *pgxpool.Pool, env config.Env, key *rsa.PrivateKey, publish func(string, []byte) error) *Service {
	return &Service{
		DB: db, Key: key, Kid: utils.KeyID(&key.PublicKey), Issuer: env.Issuer,
		AccessTTL: env.AccessTTL, RefreshTTL: env.RefreshTTL, Publish: publish,
		EmailLimit: utils.NewLimiter(5, 15*time.Minute),
		IPLimit:    utils.NewLimiter(50, 15*time.Minute),
	}
}

// userQuery selects a user with its roles for scanUser; callers append the WHERE and GROUP BY.
const userQuery = `SELECT u.id, u.email, u.name, u.disabled_at IS NOT NULL, u.created_at,
	coalesce(array_agg(r.role ORDER BY r.role) FILTER (WHERE r.role IS NOT NULL), '{}')
	FROM users u LEFT JOIN roles r ON r.user_id = u.id`

// scanUser reads one userQuery row, turning no row into ErrNotFound.
func scanUser(row pgx.Row) (model.User, error) {
	var user model.User
	err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Disabled, &user.CreatedAt, &user.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return user, err
}

// GetUser returns one user, or ErrNotFound.
func (s *Service) GetUser(ctx context.Context, id string) (model.User, error) {
	return scanUser(s.DB.QueryRow(ctx, userQuery+` WHERE u.id = $1 GROUP BY u.id`, id))
}

// ListUsers returns every user, newest first.
func (s *Service) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.DB.Query(ctx, userQuery+` GROUP BY u.id ORDER BY u.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []model.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// CreateUser inserts a user, its credentials and roles in one transaction, with an already-validated, normalised
// email. A taken email is ErrConflict.
func (s *Service) CreateUser(ctx context.Context, email, name, password string, roles []string) (model.User, error) {
	hash, err := utils.HashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`, email, name).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.User{}, ErrConflict
	}
	if err != nil {
		return model.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credentials (user_id, password_hash) VALUES ($1, $2)`, id, hash); err != nil {
		return model.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO roles (user_id, role) SELECT $1, unnest($2::text[])`, id, roles); err != nil {
		return model.User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return model.User{}, err
	}
	return s.GetUser(ctx, id)
}

// Signin checks the password and starts a new session. An unknown email and a wrong password give the same
// ErrInvalidLogin; a disabled user gets ErrAccountOff only with the right password (API 1.58), so it tells nothing of
// which emails exist. Every refusal counts against both limits. The email's count is per IP, so failures from elsewhere
// can't lock the owner out. Success clears only the email's count.
func (s *Service) Signin(ctx context.Context, body *dto.SigninRequest, ip string) (dto.Session, error) {
	email := dto.NormalizeEmail(body.Email)
	emailKey := email + "\x00" + ip
	if s.EmailLimit.Blocked(emailKey) || s.IPLimit.Blocked(ip) {
		return dto.Session{}, ErrTooMany
	}
	var id, hash string
	var disabled bool
	err := s.DB.QueryRow(ctx, `SELECT u.id, c.password_hash, u.disabled_at IS NOT NULL
		FROM users u JOIN credentials c ON c.user_id = u.id WHERE u.email = $1`, email).Scan(&id, &hash, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		utils.DummyVerify(body.Password)
	} else if err != nil {
		return dto.Session{}, err
	}
	// The password check runs for a disabled account too, so it takes as long to refuse as a wrong password.
	if right := err == nil && utils.VerifyPassword(body.Password, hash); !right || disabled {
		s.EmailLimit.Fail(emailKey)
		s.IPLimit.Fail(ip)
		if right {
			return dto.Session{}, ErrAccountOff
		}
		return dto.Session{}, ErrInvalidLogin
	}
	s.EmailLimit.Reset(emailKey)
	user, err := s.GetUser(ctx, id)
	if err != nil {
		return dto.Session{}, err
	}
	return s.issue(ctx, s.DB, user, "")
}

// rowQuerier is what issue writes through: the pool, or a transaction it has to join.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// issue creates a refresh token (in family, or a new family) and a signed access token.
func (s *Service) issue(ctx context.Context, db rowQuerier, user model.User, family string) (dto.Session, error) {
	refresh := utility.RandomToken(32)
	err := db.QueryRow(ctx, `INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, coalesce(nullif($2, '')::uuid, gen_random_uuid()), $3, $4) RETURNING family_id`,
		user.ID, family, utility.SHA256(refresh), time.Now().Add(s.RefreshTTL)).Scan(&family)
	if err != nil {
		return dto.Session{}, err
	}
	now := time.Now()
	claims := model.Claims{Email: user.Email, Roles: user.Roles, FamilyID: family, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: s.Issuer, Subject: user.ID, ID: utility.RandomHex(16),
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(s.AccessTTL)),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.Kid
	access, err := token.SignedString(s.Key)
	if err != nil {
		return dto.Session{}, err
	}
	return dto.Session{AccessToken: access, RefreshToken: refresh, User: user}, nil
}

// Refresh rotates a refresh token; presenting an already-used one that is neither expired nor revoked revokes its
// whole family. A token is spent only when its replacement commits, so a failure here leaves it usable for a retry.
func (s *Service) Refresh(ctx context.Context, token string) (dto.Session, error) {
	var id, userID, family string
	var used, revoked, expired bool
	err := s.DB.QueryRow(ctx, `SELECT id, user_id, family_id, used_at IS NOT NULL, revoked_at IS NOT NULL, expires_at < now()
		FROM refresh_tokens WHERE token_hash = $1`, utility.SHA256(token)).Scan(&id, &userID, &family, &used, &revoked, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return dto.Session{}, ErrUnauthorized
	}
	if err != nil {
		return dto.Session{}, err
	}
	if revoked || expired {
		return dto.Session{}, ErrUnauthorized
	}
	if used {
		return dto.Session{}, s.rejectReuse(ctx, userID, family)
	}
	user, err := s.GetUser(ctx, userID)
	if errors.Is(err, ErrNotFound) || (err == nil && user.Disabled) {
		return dto.Session{}, ErrUnauthorized
	}
	if err != nil {
		return dto.Session{}, err
	}
	session, claimed, err := s.rotate(ctx, id, user, family)
	if err != nil {
		return dto.Session{}, err
	}
	if !claimed { // a concurrent second use counts as reuse
		return dto.Session{}, s.rejectReuse(ctx, userID, family)
	}
	return session, nil
}

// rotate spends a refresh token and issues its replacement in one transaction. claimed is false when another request
// spent the token first.
func (s *Service) rotate(ctx context.Context, tokenID string, user model.User, family string) (session dto.Session, claimed bool, err error) {
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, tokenID)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		if !claimed {
			return nil
		}
		session, err = s.issue(ctx, tx, user, family)
		return err
	})
	return session, claimed, err
}

// rejectReuse revokes the family of a token presented twice, which may mean it was stolen, and refuses the request.
func (s *Service) rejectReuse(ctx context.Context, userID, family string) error {
	if err := s.revokeFamily(ctx, userID, family, "", "refresh_reuse"); err != nil {
		return err
	}
	return ErrUnauthorized
}

// Signout revokes the session (refresh-token family) the access token belongs to.
func (s *Service) Signout(ctx context.Context, claims *model.Claims) error {
	return s.revokeFamily(ctx, claims.Subject, claims.FamilyID, claims.ID, "signout")
}

// ChangePassword replaces the caller's password after checking the current one, which is ErrWrongPassword if wrong.
// Wrong ones count per session: the one that uses up EmailLimit revokes the session, so a stolen access token can't
// guess on.
func (s *Service) ChangePassword(ctx context.Context, claims *model.Claims, current, next string) error {
	var hash string
	if err := s.DB.QueryRow(ctx, `SELECT password_hash FROM credentials WHERE user_id = $1`, claims.Subject).Scan(&hash); err != nil {
		return err
	}
	if !utils.VerifyPassword(current, hash) {
		key := "session\x00" + claims.FamilyID
		if s.EmailLimit.Fail(key); s.EmailLimit.Blocked(key) {
			s.EmailLimit.Reset(key)
			if err := s.revokeFamily(ctx, claims.Subject, claims.FamilyID, "", "password_guess"); err != nil {
				return err
			}
		}
		return ErrWrongPassword
	}
	return s.SetPassword(ctx, claims.Subject, next, "password_change")
}

// SetPassword replaces the password and signs the user out everywhere. reason goes into the revocation event.
func (s *Service) SetPassword(ctx context.Context, userID, password, reason string) error {
	hash, err := utils.HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `UPDATE credentials SET password_hash = $2, updated_at = now() WHERE user_id = $1`, userID, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.RevokeUser(ctx, userID, reason)
}

// RevokeUser ends every session of the user. Validate then refuses the user's access tokens too.
func (s *Service) RevokeUser(ctx context.Context, userID, reason string) error {
	if _, err := s.DB.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	s.publishRevoked(userID, map[string]any{"scope": "user"}, reason)
	return nil
}

// revokeFamily ends one session. jti is optional and names the access token in the event.
func (s *Service) revokeFamily(ctx context.Context, userID, family, jti, reason string) error {
	if _, err := s.DB.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, family); err != nil {
		return err
	}
	data := map[string]any{"scope": "family", "family_id": family}
	if jti != "" {
		data["jti"] = jti
	}
	s.publishRevoked(userID, data, reason)
	return nil
}

// publishRevoked sends evt.auth.revoked (contracts/messages/evt.auth.revoked.schema.json). Best effort: the
// revocation is already in the database, which Validate checks.
func (s *Service) publishRevoked(userID string, data map[string]any, reason string) {
	if s.Publish == nil {
		return
	}
	now := time.Now().UTC()
	data["user_id"] = userID
	data["reason"] = reason
	data["expires_at"] = now.Add(s.AccessTTL).Format(time.RFC3339)
	event, _ := json.Marshal(map[string]any{
		"type": RevokedSubject, "version": 1, "id": utility.NewID(), "job_id": nil, "owner_id": userID,
		"occurred_at": now.Format(time.RFC3339Nano),
		"trace":       map[string]string{"traceparent": utility.Traceparent()},
		"data":        data,
	})
	if err := s.Publish(RevokedSubject, event); err != nil {
		log.Printf("publish %s: %v", RevokedSubject, err)
	}
}

// Validate checks signature, expiry and issuer, then that the user is enabled and the session not revoked.
func (s *Service) Validate(ctx context.Context, token string) (*model.Claims, error) {
	claims := &model.Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return &s.Key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(s.Issuer), jwt.WithExpirationRequired())
	if err != nil || claims.Subject == "" || claims.FamilyID == "" {
		return nil, ErrUnauthorized
	}
	var ok bool
	err = s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND disabled_at IS NULL)
		AND NOT EXISTS (SELECT 1 FROM refresh_tokens WHERE family_id = $2 AND revoked_at IS NOT NULL)`,
		claims.Subject, claims.FamilyID).Scan(&ok)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

// JWKS returns the public signing key as a JSON Web Key Set.
func (s *Service) JWKS() map[string]any {
	publicKey := s.Key.PublicKey
	encode := base64.RawURLEncoding.EncodeToString
	return map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": s.Kid,
		"n": encode(publicKey.N.Bytes()), "e": encode(big.NewInt(int64(publicKey.E)).Bytes()),
	}}}
}
