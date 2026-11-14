// Integration tests against a real Postgres: TEST_DATABASE_URL points at a server's maintenance database; each run
// creates and drops its own scratch database. Skipped when unset.
package tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/postgres"
	"github.com/helmstudio/orago/utility"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	natsmicro "github.com/nats-io/nats.go/micro"

	"github.com/helmstudio/auth_service/config"
	"github.com/helmstudio/auth_service/migrations"
	"github.com/helmstudio/auth_service/startup"
)

// env is one test's running service: a scratch database, a real HTTP server, and the events it published.
type env struct {
	t      *testing.T
	srv    *httptest.Server
	mod    *startup.Module
	db     *pgxpool.Pool
	mu     sync.Mutex
	events []map[string]any
}

// setup creates a scratch database, runs the migrations and serves the module over HTTP with no NATS server.
func setup(t *testing.T) *env {
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	name := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	cfg := config.Env{DatabaseURL: u.String(), Issuer: "helm-auth", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
		AdminEmail: "Admin@Example.com ", AdminPassword: "admin password 1"}
	db, err := postgres.Open(ctx, cfg.DatabaseURL, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	e := &env{t: t, db: db}
	e.mod = startup.NewModule(cfg, db, key, func(subj string, b []byte) error {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		e.mu.Lock()
		e.events = append(e.events, m)
		e.mu.Unlock()
		return nil
	}, nil)
	if err := startup.EnsureAdmin(ctx, cfg, e.mod.Auth); err != nil {
		t.Fatal(err)
	}
	if err := startup.EnsureAdmin(ctx, cfg, e.mod.Auth); err != nil { // idempotent
		t.Fatal(err)
	}
	// The HTTP part of the micro router only, so no NATS server is needed: TestNatsValidate calls the NATS endpoints.
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(e.mod.RootMiddlewares()...)
	for _, controller := range e.mod.Controllers() {
		router.LoadControllers(controller)
	}
	e.srv = httptest.NewServer(router.Engine())
	t.Cleanup(func() {
		e.srv.Close()
		db.Close()
		_, _ = conn.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		conn.Close(ctx)
	})
	return e
}

// do sends a request; out (if non-nil) receives the JSON body. Returns the status.
func (e *env) do(method, path, token string, body any, out any, hdr ...string) int {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// signup stands in for the missing sign-up: the admin creates the account (role user), then it signs in; out gets
// that session. Returns 201 like sign-up did, else the create's status.
func (e *env) signup(body map[string]string, out any) int {
	e.t.Helper()
	var admin session
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &admin); c != 200 {
		e.t.Fatalf("admin signin %d", c)
	}
	create := map[string]any{"email": body["email"], "password": body["password"], "name": body["name"], "roles": []string{"user"}}
	if c := e.do("POST", "/auth/admin/users", admin.AccessToken, create, nil); c != 201 {
		return c
	}
	e.do("POST", "/auth/signin", "", map[string]string{"email": body["email"], "password": body["password"]}, out)
	return 201
}

// session is the JSON body of signin and refresh.
type session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID    string   `json:"id"`
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	} `json:"user"`
}

// lastEvent returns the most recent published event, or nil.
func (e *env) lastEvent() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return nil
	}
	return e.events[len(e.events)-1]
}

// TestSessionFlow walks a user through signup, signin, refresh, password change and signout.
func TestSessionFlow(t *testing.T) {
	e := setup(t)
	var s session
	if c := e.signup(map[string]string{"email": " Ann@Example.com", "password": "a long passphrase", "name": "Ann"}, &s); c != 201 {
		t.Fatalf("signup %d", c)
	}
	if s.User.Email != "ann@example.com" || len(s.User.Roles) != 1 || s.AccessToken == "" || s.RefreshToken == "" {
		t.Fatalf("signup body %+v", s)
	}
	if c := e.signup(map[string]string{"email": "ANN@example.com", "password": "a long passphrase", "name": "Ann"}, nil); c != 409 {
		t.Fatalf("duplicate %d", c)
	}
	for _, bad := range []map[string]string{
		{"email": "nope", "password": "a long passphrase", "name": "x"},
		{"email": "b@example.com", "password": "short", "name": "x"},
		{"email": "b@example.com", "password": "password123", "name": "x"},
	} {
		if c := e.signup(bad, nil); c != 422 {
			t.Fatalf("invalid signup %v: %d", bad, c)
		}
	}

	// sign in: generic errors for wrong password and unknown email
	var d1, d2 map[string]string
	e.do("POST", "/auth/signin", "", map[string]string{"email": "ann@example.com", "password": "wrong password!"}, &d1)
	e.do("POST", "/auth/signin", "", map[string]string{"email": "who@example.com", "password": "wrong password!"}, &d2)
	if d1["detail"] != "invalid email or password" || d1["detail"] != d2["detail"] {
		t.Fatalf("generic error %v %v", d1, d2)
	}
	var s1 session
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "ANN@example.com", "password": "a long passphrase"}, &s1); c != 200 {
		t.Fatalf("signin %d", c)
	}
	var me map[string]any
	if c := e.do("GET", "/auth/me", s1.AccessToken, nil, &me); c != 200 || me["email"] != "ann@example.com" {
		t.Fatalf("me %d %v", c, me)
	}
	if c := e.do("GET", "/auth/me", "", nil, nil); c != 401 {
		t.Fatalf("me without token %d", c)
	}
	if c := e.do("GET", "/auth/admin/users", s1.AccessToken, nil, nil); c != 403 {
		t.Fatalf("non-admin %d", c)
	}

	// refresh rotation; reuse of the old token revokes the family
	var s2 session
	if c := e.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s1.RefreshToken}, &s2); c != 200 || s2.RefreshToken == s1.RefreshToken {
		t.Fatalf("refresh %d", c)
	}
	if c := e.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s1.RefreshToken}, nil); c != 401 {
		t.Fatalf("reuse %d", c)
	}
	if ev := e.lastEvent(); ev == nil || ev["type"] != "evt.auth.revoked" || ev["data"].(map[string]any)["reason"] != "refresh_reuse" {
		t.Fatalf("reuse event %v", ev)
	}
	if c := e.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s2.RefreshToken}, nil); c != 401 {
		t.Fatalf("family must be revoked %d", c)
	}
	if c := e.do("GET", "/auth/me", s2.AccessToken, nil, nil); c != 401 {
		t.Fatalf("access token of a revoked family %d", c)
	}

	// sign out
	var s3 session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "ann@example.com", "password": "a long passphrase"}, &s3)
	if c := e.do("DELETE", "/auth/signout", s3.AccessToken, nil, nil); c != 204 {
		t.Fatalf("signout %d", c)
	}
	ev := e.lastEvent()
	data := ev["data"].(map[string]any)
	if data["reason"] != "signout" || data["scope"] != "family" || data["family_id"] == "" || data["jti"] == "" ||
		ev["owner_id"] != s3.User.ID || ev["job_id"] != nil {
		t.Fatalf("signout event %v", ev)
	}
	if c := e.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s3.RefreshToken}, nil); c != 401 {
		t.Fatalf("refresh after signout %d", c)
	}

	// password change signs out everywhere
	var s4 session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "ann@example.com", "password": "a long passphrase"}, &s4)
	if c := e.do("PUT", "/auth/password", s4.AccessToken, map[string]string{"current_password": "nope nope nope", "new_password": "another passphrase"}, nil); c != 401 {
		t.Fatalf("wrong current %d", c)
	}
	if c := e.do("PUT", "/auth/password", s4.AccessToken, map[string]string{"current_password": "a long passphrase", "new_password": "another passphrase"}, nil); c != 204 {
		t.Fatalf("change %d", c)
	}
	if c := e.do("GET", "/auth/me", s4.AccessToken, nil, nil); c != 401 {
		t.Fatalf("token after password change %d", c)
	}
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "ann@example.com", "password": "another passphrase"}, nil); c != 200 {
		t.Fatalf("signin with new password %d", c)
	}
}

// TestSigninLimit blocks an email after 5 failures, even with the right password.
func TestSigninLimit(t *testing.T) {
	e := setup(t)
	e.signup(map[string]string{"email": "bob@example.com", "password": "a long passphrase", "name": "Bob"}, nil)
	for i := 0; i < 5; i++ {
		e.do("POST", "/auth/signin", "", map[string]string{"email": "bob@example.com", "password": "wrong password!"}, nil)
	}
	var body map[string]string
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "bob@example.com", "password": "a long passphrase"}, &body); c != 429 {
		t.Fatalf("per-email limit %d", c)
	}
	if body["detail"] != "too many attempts, try again later" {
		t.Fatalf("detail %q", body["detail"])
	}
}

// TestSigninLimitPerIP keys the limit on the peer: this router trusts no proxy, so a spoofed X-Forwarded-For is ignored.
func TestSigninLimitPerIP(t *testing.T) {
	e := setup(t)
	for i := range 50 {
		e.do("POST", "/auth/signin", "", map[string]string{"email": fmt.Sprintf("nobody%d@example.com", i), "password": "wrong password!"}, nil)
	}
	attempt := map[string]string{"email": "someone.else@example.com", "password": "wrong password!"}
	if c := e.do("POST", "/auth/signin", "", attempt, nil, "X-Forwarded-For", "203.0.113.9"); c != 429 {
		t.Fatalf("per-IP limit %d, want 429 despite the spoofed header", c)
	}
}

// TestAdminAndAPIKeys covers the admin routes and the life of a client API key.
func TestAdminAndAPIKeys(t *testing.T) {
	e := setup(t)
	var adm session
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &adm); c != 200 {
		t.Fatalf("admin signin %d", c)
	}
	var u session
	e.signup(map[string]string{"email": "cy@example.com", "password": "a long passphrase", "name": "Cy"}, &u)

	var list struct{ Users []map[string]any }
	if c := e.do("GET", "/auth/admin/users", adm.AccessToken, nil, &list); c != 200 || len(list.Users) != 2 {
		t.Fatalf("list %d %v", c, list)
	}
	if c := e.do("PUT", "/auth/admin/users/"+u.User.ID+"/roles", adm.AccessToken, map[string]any{"roles": []string{"user", "dev"}}, nil); c != 200 {
		t.Fatalf("roles %d", c)
	}
	if c := e.do("PUT", "/auth/admin/users/"+u.User.ID+"/roles", adm.AccessToken, map[string]any{"roles": []string{"user", "admin"}}, nil); c != 422 {
		t.Fatalf("admin role given %d", c)
	}
	if c := e.do("PUT", "/auth/admin/users/"+u.User.ID+"/roles", adm.AccessToken, map[string]any{"roles": []string{"root"}}, nil); c != 422 {
		t.Fatalf("bad role %d", c)
	}
	if c := e.do("PUT", "/auth/admin/users/not-a-uuid/disabled", adm.AccessToken, map[string]any{"disabled": true}, nil); c != 404 {
		t.Fatalf("malformed id %d", c)
	}
	if c := e.do("PUT", "/auth/admin/users/00000000-0000-0000-0000-000000000000/disabled", adm.AccessToken, map[string]any{"disabled": true}, nil); c != 404 {
		t.Fatalf("unknown id %d", c)
	}
	var cy session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "cy@example.com", "password": "a long passphrase"}, &cy)
	if len(cy.User.Roles) != 2 {
		t.Fatalf("new roles in session %v", cy.User.Roles)
	}
	if c := e.do("PUT", "/auth/admin/users/"+u.User.ID+"/disabled", adm.AccessToken, map[string]any{"disabled": true}, nil); c != 200 {
		t.Fatalf("disable %d", c)
	}
	if ev := e.lastEvent(); ev["data"].(map[string]any)["scope"] != "user" {
		t.Fatalf("disable event %v", ev)
	}
	if c := e.do("GET", "/auth/me", cy.AccessToken, nil, nil); c != 401 {
		t.Fatalf("disabled user's token %d", c)
	}
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "cy@example.com", "password": "a long passphrase"}, nil); c != 401 {
		t.Fatalf("disabled signin %d", c)
	}
	e.do("PUT", "/auth/admin/users/"+u.User.ID+"/disabled", adm.AccessToken, map[string]any{"disabled": false}, nil)
	if c := e.do("PUT", "/auth/admin/users/"+u.User.ID+"/password", adm.AccessToken, map[string]any{"password": "admin set this one"}, nil); c != 204 {
		t.Fatalf("admin set password %d", c)
	}
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "cy@example.com", "password": "admin set this one"}, nil); c != 200 {
		t.Fatalf("signin after admin reset %d", c)
	}

	// API keys
	var k struct{ ID, Key string }
	var created map[string]any
	if c := e.do("POST", "/auth/admin/apikeys", adm.AccessToken, map[string]any{"client": "web", "scopes": []string{"v1"}}, &created); c != 201 {
		t.Fatalf("create key %d", c)
	}
	k.ID, k.Key = created["id"].(string), created["key"].(string)
	if c := e.do("POST", "/auth/admin/apikeys", adm.AccessToken, map[string]any{"client": "Bad Name"}, nil); c != 422 {
		t.Fatalf("bad client %d", c)
	}
	var v map[string]any
	if c := e.do("GET", "/verify/apikey", "", nil, &v, "x-api-key", k.Key); c != 200 || v["client"] != "web" || v["client_id"] == "" {
		t.Fatalf("verify %d %v", c, v)
	}
	if c := e.do("GET", "/verify/apikey", "", nil, nil, "x-api-key", "helm_nope"); c != 401 {
		t.Fatalf("bad key %d", c)
	}
	var keys struct {
		APIKeys []map[string]any `json:"api_keys"`
	}
	if c := e.do("GET", "/auth/admin/apikeys", adm.AccessToken, nil, &keys); c != 200 || len(keys.APIKeys) != 1 || keys.APIKeys[0]["key"] != nil {
		t.Fatalf("list keys %d %v", c, keys)
	}
	if c := e.do("DELETE", "/auth/admin/apikeys/"+k.ID, adm.AccessToken, nil, nil); c != 204 {
		t.Fatalf("revoke %d", c)
	}
	if c := e.do("DELETE", "/auth/admin/apikeys/"+k.ID, adm.AccessToken, nil, nil); c != 404 {
		t.Fatalf("revoke twice %d", c)
	}
	if c := e.do("GET", "/verify/apikey", "", nil, nil, "x-api-key", k.Key); c != 401 {
		t.Fatalf("revoked key %d", c)
	}

	// jwks + health
	var jwks struct{ Keys []map[string]string }
	if c := e.do("GET", "/auth/.well-known/jwks.json", "", nil, &jwks); c != 200 || len(jwks.Keys) != 1 || jwks.Keys[0]["alg"] != "RS256" {
		t.Fatalf("jwks %d %v", c, jwks)
	}
	if c := e.do("GET", "/health", "", nil, nil); c != 200 {
		t.Fatalf("health %d", c)
	}
}

// TestErrorDetails pins the error strings and headers the HTTP edge guarantees (orago/network), which other
// tests see only as statuses.
func TestErrorDetails(t *testing.T) {
	e := setup(t)
	var user session
	e.signup(map[string]string{"email": "di@example.com", "password": "a long passphrase", "name": "Di"}, &user)
	for _, c := range []struct {
		method, path, token string
		status              int
		detail              string
	}{
		{"GET", "/auth/me", "", 401, "missing bearer token"},
		{"GET", "/auth/me", "garbage", 401, "invalid or expired token"},
		{"GET", "/auth/admin/users", user.AccessToken, 403, "admin role required"},
		{"GET", "/auth/admin/apikeys", user.AccessToken, 403, "admin role required"},
		{"GET", "/auth/nope", "", 404, "Not Found"},
		{"GET", "/auth/me/", user.AccessToken, 404, "Not Found"},                           // trailing slash: no redirect
		{"POST", "/auth/me", user.AccessToken, 404, "Not Found"},                           // wrong method: no 405
		{"PUT", "/auth/admin/users/x/roles", user.AccessToken, 403, "admin role required"}, // role check before the path id
		{"POST", "/auth/signin", "", 422, "invalid JSON body"},
		{"GET", "/verify/apikey", "", 401, "invalid api key"},
	} {
		var body map[string]string
		if status := e.do(c.method, c.path, c.token, nil, &body); status != c.status || body["detail"] != c.detail {
			t.Errorf("%s %s: %d %v, want %d %q", c.method, c.path, status, body, c.status, c.detail)
		}
	}

	resp, err := http.Get(e.srv.URL + "/auth/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "public, max-age=300" || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("jwks headers %v", resp.Header)
	}
	if c := e.do("GET", "/auth/health", "", nil, nil); c != 200 {
		t.Fatalf("/auth/health %d", c)
	}
}

// TestBusinessErrorDetails pins the exact words, because the web app shows them to people.
func TestBusinessErrorDetails(t *testing.T) {
	e := setup(t)
	credentials := map[string]string{"email": "erin@example.com", "password": "a long passphrase", "name": "Erin"}
	var user, admin session
	if c := e.signup(credentials, &user); c != 201 {
		t.Fatalf("signup %d", c)
	}
	e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &admin)

	const unknownID = "00000000-0000-4000-8000-000000000000"
	const tooShort = "password must be at least 10 characters"
	userPath := "/auth/admin/users/" + user.User.ID
	for _, c := range []struct {
		name, method, path, token string
		body                      any
		status                    int
		detail                    string
	}{
		{"duplicate account", "POST", "/auth/admin/users", admin.AccessToken,
			map[string]any{"email": "erin@example.com", "password": "a long passphrase", "roles": []string{"user"}}, 409, "email already registered"},
		{"admin is not grantable", "POST", "/auth/admin/users", admin.AccessToken,
			map[string]any{"email": "eve@example.com", "password": "a long passphrase", "roles": []string{"admin"}}, 422,
			"admin is only the ADMIN_EMAIL account (auth's .env)"},
		{"admin account is the .env's", "PUT", "/auth/admin/users/" + admin.User.ID + "/disabled", admin.AccessToken,
			map[string]bool{"disabled": true}, 409, "the admin account is set by ADMIN_EMAIL / ADMIN_PASSWORD in auth's .env"},
		{"admin account roles", "PUT", "/auth/admin/users/" + admin.User.ID + "/roles", admin.AccessToken,
			map[string][]string{"roles": {"user"}}, 409, "the admin account is set by ADMIN_EMAIL / ADMIN_PASSWORD in auth's .env"},
		{"admin account password", "PUT", "/auth/admin/users/" + admin.User.ID + "/password", admin.AccessToken,
			map[string]string{"password": "a long passphrase"}, 409, "the admin account is set by ADMIN_EMAIL / ADMIN_PASSWORD in auth's .env"},
		{"wrong current password", "PUT", "/auth/password", user.AccessToken,
			map[string]string{"current_password": "not my password", "new_password": "another long passphrase"},
			401, "current password is incorrect"},
		{"weak new password", "PUT", "/auth/password", user.AccessToken,
			map[string]string{"current_password": "a long passphrase", "new_password": "short"}, 422, tooShort},
		{"weak admin password", "PUT", userPath + "/password", admin.AccessToken, map[string]string{"password": "short"}, 422, tooShort},
		{"disabled missing", "PUT", userPath + "/disabled", admin.AccessToken, map[string]string{}, 422, "disabled is required"},
		{"unknown user", "PUT", "/auth/admin/users/" + unknownID + "/disabled", admin.AccessToken, map[string]bool{"disabled": true}, 404, "not found"},
		{"malformed id, disabled", "PUT", "/auth/admin/users/not-a-uuid/disabled", admin.AccessToken, map[string]bool{"disabled": true}, 404, "not found"},
		{"malformed id, roles", "PUT", "/auth/admin/users/not-a-uuid/roles", admin.AccessToken, map[string][]string{"roles": {"user"}}, 404, "not found"},
		{"malformed id, password", "PUT", "/auth/admin/users/not-a-uuid/password", admin.AccessToken, map[string]string{"password": "a long passphrase"}, 404, "not found"},
		{"malformed key id", "DELETE", "/auth/admin/apikeys/not-a-uuid", admin.AccessToken, nil, 404, "not found"},
		{"unknown key id", "DELETE", "/auth/admin/apikeys/" + unknownID, admin.AccessToken, nil, 404, "not found"},
	} {
		var body map[string]string
		if status := e.do(c.method, c.path, c.token, c.body, &body); status != c.status || body["detail"] != c.detail {
			t.Errorf("%s: %d %v, want %d %q", c.name, status, body, c.status, c.detail)
		}
	}
}

// TestNewKeyIsNeverCached matters because a new key is shown only once, in this response.
func TestNewKeyIsNeverCached(t *testing.T) {
	e := setup(t)
	var admin session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &admin)
	request, err := http.NewRequest("POST", e.srv.URL+"/auth/admin/apikeys", bytes.NewBufferString(`{"client":"cache-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+admin.AccessToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 201 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, Cache-Control %q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
}

// TestHealthAndPanicWiring checks the module's own wiring: NATS down reads as unhealthy, and the error catcher is
// loaded first, so a panic behind it is still a JSON 500.
func TestHealthAndPanicWiring(t *testing.T) {
	e := setup(t)
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	module := startup.NewModule(config.Env{Issuer: "helm-auth"}, e.db, e.mod.Auth.Key, nil, func() bool { return false })
	router, err := network.NewRouter(nil)
	if err != nil {
		t.Fatal(err)
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	for _, controller := range module.Controllers() {
		router.LoadControllers(controller)
	}
	router.Engine().GET("/boom", func(*gin.Context) { panic("boom") })

	for path, want := range map[string]struct {
		status int
		detail string
	}{
		"/health": {503, "nats unavailable"},
		"/boom":   {500, "internal error"},
	} {
		recorder := httptest.NewRecorder()
		router.Engine().ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		var body map[string]string
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		if recorder.Code != want.status || body["detail"] != want.detail {
			t.Errorf("%s: %d %v, want %d %q", path, recorder.Code, body, want.status, want.detail)
		}
	}
}

// A failed refresh must not spend the token: the client's retry would look like theft and revoke the whole family.
func TestRefreshFailureKeepsTheToken(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var session struct {
		RefreshToken string `json:"refresh_token"`
	}
	if c := e.signup(map[string]string{"email": "retry@example.com", "password": "a good password 1", "name": "Retry"}, &session); c != 201 {
		t.Fatalf("signup %d", c)
	}
	body := map[string]string{"refresh_token": session.RefreshToken}

	// Only the new token's row fails this: spending the old token passes it, so the failure comes after the spend.
	if _, err := e.db.Exec(ctx, `ALTER TABLE refresh_tokens ADD CONSTRAINT refuse_new CHECK (used_at IS NOT NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if c := e.do("POST", "/auth/refresh", "", body, nil); c != 500 {
		t.Fatalf("refresh while the insert fails: %d, want 500", c)
	}
	if _, err := e.db.Exec(ctx, `ALTER TABLE refresh_tokens DROP CONSTRAINT refuse_new`); err != nil {
		t.Fatal(err)
	}

	if c := e.do("POST", "/auth/refresh", "", body, nil); c != 200 {
		t.Fatalf("retry %d, want 200: the failed attempt spent the token", c)
	}
}

// Verify runs on Kong's cache misses, so it should not write a new row version on each one.
func TestVerifyWritesAtMostOncePerMinute(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	const key = "verify-test-key-of-valid-length"
	if err := e.mod.APIKeys.Ensure(ctx, "web", key); err != nil {
		t.Fatal(err)
	}
	verify := func() (version string, lastUsed *time.Time) {
		if _, err := e.mod.APIKeys.Verify(ctx, key); err != nil {
			t.Fatal(err)
		}
		err := e.db.QueryRow(ctx, `SELECT xmin::text, last_used_at FROM api_keys WHERE hash = $1`, utility.SHA256(key)).Scan(&version, &lastUsed)
		if err != nil {
			t.Fatal(err)
		}
		return version, lastUsed
	}

	first, stamped := verify()
	if stamped == nil {
		t.Fatal("the first Verify didn't stamp last_used_at")
	}
	if second, _ := verify(); second != first {
		t.Errorf("the second Verify wrote the row: version %s, then %s", first, second)
	}

	if _, err := e.db.Exec(ctx, `UPDATE api_keys SET last_used_at = now() - interval '61 seconds' WHERE hash = $1`, utility.SHA256(key)); err != nil {
		t.Fatal(err)
	}
	if _, refreshed := verify(); refreshed == nil || time.Since(*refreshed) > time.Minute {
		t.Errorf("a stamp over a minute old wasn't refreshed: %v", refreshed)
	}
}

// TestDisabledSigninTakesAsLongAsAWrongPassword stops a timing probe from telling a disabled account apart. Password
// hashing takes milliseconds and skipping it takes almost none, so the gap can't be noise.
func TestDisabledSigninTakesAsLongAsAWrongPassword(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, email := range []string{"active@example.com", "disabled@example.com"} {
		e.signup(map[string]string{"email": email, "password": "a long passphrase", "name": "Timing"}, nil)
	}
	if _, err := e.db.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE email = 'disabled@example.com'`); err != nil {
		t.Fatal(err)
	}
	fastest := func(email, password string) time.Duration {
		best := time.Hour
		for range 3 {
			start := time.Now()
			if c := e.do("POST", "/auth/signin", "", map[string]string{"email": email, "password": password}, nil); c != 401 {
				t.Fatalf("%s: status %d, want 401", email, c)
			}
			best = min(best, time.Since(start))
		}
		return best
	}

	wrongPassword := fastest("active@example.com", "not the passphrase")
	disabled := fastest("disabled@example.com", "a long passphrase")
	if disabled < wrongPassword/2 {
		t.Errorf("a disabled account answered in %v, a wrong password in %v", disabled, wrongPassword)
	}
}

// TestNatsValidate calls the NATS endpoints the auth controller mounts, with the bodies other services send.
func TestNatsValidate(t *testing.T) {
	e := setup(t)
	var s session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &s)
	group := &recordingGroup{endpoints: map[string]natsmicro.Handler{}}
	for _, controller := range e.mod.Controllers() {
		if err := controller.MountNats(group); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(endpoint, body string) natsReply {
		t.Helper()
		handler := group.endpoints[endpoint]
		if handler == nil {
			t.Fatalf("no endpoint %s in %v", endpoint, group.endpoints)
		}
		request := &recordingRequest{subject: "auth." + endpoint, data: []byte(body)}
		handler.Handle(request)
		var reply natsReply
		if err := json.Unmarshal(request.reply, &reply); err != nil {
			t.Fatalf("%s: reply %s: %v", endpoint, request.reply, err)
		}
		return reply
	}

	r := ask("validate.token", `{"token":"`+s.AccessToken+`"}`)
	if r.Error != "" || r.UserID != s.User.ID || r.Email != "admin@example.com" || r.JTI == "" || r.ExpiresAt.IsZero() {
		t.Fatalf("validate %+v", r)
	}
	if r = ask("validate.role", `{"token":"`+s.AccessToken+`","role":"admin"}`); r.Allowed == nil || !*r.Allowed {
		t.Fatalf("role %+v", r)
	}
	if r = ask("validate.role", `{"token":"`+s.AccessToken+`","role":"auditor"}`); r.Allowed == nil || *r.Allowed {
		t.Fatalf("a role the token lacks %+v", r)
	}
	if r = ask("validate.role", `{"token":"`+s.AccessToken+`"}`); r.Error != `bad request: want {"token": ..., "role": ...}` {
		t.Fatalf("no role %+v", r)
	}
	if r = ask("validate.token", `{"token":"garbage"}`); r.Error != "invalid or expired token" {
		t.Fatalf("garbage %+v", r)
	}
	if r = ask("validate.token", `{}`); r.Error != `bad request: want {"token": ...}` {
		t.Fatalf("no token %+v", r)
	}
	if r = ask("validate.token", `nope`); r.Error != "bad request: invalid JSON" {
		t.Fatalf("bad request %+v", r)
	}
	// a token signed by another key is rejected
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	saved := e.mod.Auth.Key
	e.mod.Auth.Key = other
	var forged session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &forged)
	e.mod.Auth.Key = saved
	if r = ask("validate.token", `{"token":"`+forged.AccessToken+`"}`); r.Error != "invalid or expired token" {
		t.Fatalf("foreign signature accepted %+v", r)
	}
}

// natsReply reads a validate reply, an error reply included.
type natsReply struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	JTI       string    `json:"jti"`
	ExpiresAt time.Time `json:"expires_at"`
	Allowed   *bool     `json:"allowed"`
	Error     string    `json:"error"`
}

// recordingGroup collects the endpoints a controller mounts, keyed by their subject below the service name.
type recordingGroup struct {
	prefix    string
	endpoints map[string]natsmicro.Handler
}

func (g *recordingGroup) AddGroup(name string, _ ...natsmicro.GroupOpt) natsmicro.Group {
	return &recordingGroup{prefix: g.prefix + name + ".", endpoints: g.endpoints}
}

func (g *recordingGroup) AddEndpoint(name string, handler natsmicro.Handler, _ ...natsmicro.EndpointOpt) error {
	g.endpoints[g.prefix+name] = handler
	return nil
}

// recordingRequest is one request to an endpoint; Respond keeps the reply.
type recordingRequest struct {
	micro.NatsRequest
	subject     string
	data, reply []byte
}

func (r *recordingRequest) Data() []byte    { return r.data }
func (r *recordingRequest) Subject() string { return r.subject }

func (r *recordingRequest) Respond(body []byte, _ ...natsmicro.RespondOpt) error {
	r.reply = body
	return nil
}

// TestAccounts: there is no sign-up, the admin makes accounts (dev is grantable, the name defaults from the email),
// and EnsureAdmin keeps ADMIN_EMAIL the one admin with the .env's password.
func TestAccounts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if c := e.do("POST", "/auth/signup", "", map[string]string{"email": "pub@example.com", "password": "a long passphrase", "name": "Pub"}, nil); c != 404 {
		t.Fatalf("there should be no sign-up: %d", c)
	}
	var admin session
	e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, &admin)
	var made struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	body := map[string]any{"email": " Dana@Example.com", "password": "a long passphrase", "roles": []string{"user", "dev"}}
	if c := e.do("POST", "/auth/admin/users", admin.AccessToken, body, &made); c != 201 || made.Name != "dana" || strings.Join(made.Roles, ",") != "dev,user" {
		t.Fatalf("create %d %+v", c, made)
	}
	var dana session
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "dana@example.com", "password": "a long passphrase"}, &dana); c != 200 {
		t.Fatalf("created user signin %d", c)
	}
	if c := e.do("POST", "/auth/admin/users", dana.AccessToken, body, nil); c != 403 {
		t.Fatalf("non-admin create %d", c)
	}

	// another admin (set behind the API's back) loses the role; a new ADMIN_PASSWORD replaces the old one
	if _, err := e.db.Exec(ctx, `INSERT INTO roles (user_id, role) VALUES ($1, 'admin')`, made.ID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Env{AdminEmail: "admin@example.com", AdminPassword: "a new admin pass 2"}
	if err := startup.EnsureAdmin(ctx, cfg, e.mod.Auth); err != nil {
		t.Fatal(err)
	}
	var roles []string
	if err := e.db.QueryRow(ctx, `SELECT array_agg(role ORDER BY role) FROM roles WHERE user_id = $1`, made.ID).Scan(&roles); err != nil || strings.Join(roles, ",") != "dev,user" {
		t.Fatalf("demoted roles %v %v", roles, err)
	}
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "admin password 1"}, nil); c != 401 {
		t.Fatalf("old admin password %d", c)
	}
	if c := e.do("POST", "/auth/signin", "", map[string]string{"email": "admin@example.com", "password": "a new admin pass 2"}, nil); c != 200 {
		t.Fatalf("new admin password %d", c)
	}
}
