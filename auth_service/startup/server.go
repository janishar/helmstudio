// Package startup: keys, NATS, HTTP server and the CLI commands.
package startup

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/postgres"

	"github.com/helmstudio/auth_service/api/apikey"
	apikeydto "github.com/helmstudio/auth_service/api/apikey/dto"
	"github.com/helmstudio/auth_service/api/auth"
	"github.com/helmstudio/auth_service/api/auth/dto"
	"github.com/helmstudio/auth_service/config"
	"github.com/helmstudio/auth_service/migrations"
	"github.com/helmstudio/auth_service/utils"
)

// EnsureAdmin makes ADMIN_EMAIL the one admin, on every start: the account is created when missing, else enabled,
// given the user and admin roles and ADMIN_PASSWORD (a changed password signs it out everywhere). Every other
// account loses the admin role and its sessions, and without ADMIN_EMAIL / ADMIN_PASSWORD no account is admin.
func EnsureAdmin(ctx context.Context, env config.Env, service *auth.Service) error {
	var id string
	if env.AdminEmail != "" && env.AdminPassword != "" {
		body := dto.SignupRequest{Email: env.AdminEmail, Password: env.AdminPassword, Name: "Admin"}
		if detail := body.Validate(); detail != "" {
			return fmt.Errorf("ADMIN_EMAIL/ADMIN_PASSWORD: %s", detail)
		}
		user, err := service.CreateUser(ctx, body.Email, body.Name, body.Password, []string{"user", "admin"})
		switch {
		case err == nil:
			log.Printf("created admin %s", body.Email)
			id = user.ID
		case errors.Is(err, auth.ErrConflict):
			if id, err = syncAdmin(ctx, service, body.Email, body.Password); err != nil {
				return err
			}
		default:
			return err
		}
	}
	rows, err := service.DB.Query(ctx, `DELETE FROM roles WHERE role = 'admin' AND user_id::text <> $1 RETURNING user_id::text`, id)
	if err != nil {
		return err
	}
	demoted, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, user := range demoted {
		log.Printf("removed the admin role from user %s (admin is ADMIN_EMAIL only)", user)
		if err := service.RevokeUser(ctx, user, "admin"); err != nil {
			return err
		}
	}
	return nil
}

// syncAdmin brings the existing ADMIN_EMAIL account in line with the .env and returns its id: enabled, user and
// admin roles, and ADMIN_PASSWORD when the stored one differs.
func syncAdmin(ctx context.Context, service *auth.Service, email, password string) (string, error) {
	var id, hash string
	err := service.DB.QueryRow(ctx, `SELECT u.id::text, c.password_hash FROM users u JOIN credentials c ON c.user_id = u.id
		WHERE u.email = $1`, email).Scan(&id, &hash)
	if err != nil {
		return "", err
	}
	if _, err := service.DB.Exec(ctx, `UPDATE users SET disabled_at = NULL WHERE id = $1`, id); err != nil {
		return "", err
	}
	if _, err := service.DB.Exec(ctx, `INSERT INTO roles (user_id, role) VALUES ($1, 'user'), ($1, 'admin') ON CONFLICT DO NOTHING`, id); err != nil {
		return "", err
	}
	if !utils.VerifyPassword(password, hash) {
		log.Printf("admin %s: password set from ADMIN_PASSWORD", email)
		return id, service.SetPassword(ctx, id, password, "admin")
	}
	return id, nil
}

// loadKey reads the JWT signing key from JWT_PRIVATE_KEY, or from KeysDir.
func loadKey(env config.Env) (*rsa.PrivateKey, error) {
	if env.PrivateKeyPEM != "" {
		return utils.ParseKey([]byte(env.PrivateKeyPEM), "JWT_PRIVATE_KEY")
	}
	return utils.LoadKey(env.KeysDir)
}

// Run starts the service and blocks until SIGINT or SIGTERM. It opens the database, loads the signing key, connects
// NATS, ensures the admin and web API key, mounts the validators, then serves HTTP. On a signal it waits for requests
// in flight, then for NATS handlers, and closes the pool last because they use it.
func Run() error {
	env, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, env.DatabaseURL, migrations.FS)
	if err != nil {
		return err
	}
	defer db.Close()

	key, err := loadKey(env)
	if err != nil {
		return fmt.Errorf("keys: %w", err)
	}

	client, err := micro.NewNatsClient(micro.Config{
		URL: env.NatsURL, User: env.NatsUser, Password: env.NatsPassword, Name: "auth_service",
	})
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer client.Disconnect()

	module := NewModule(env, db, key, client.Conn().Publish, client.Conn().IsConnected)
	if err := EnsureAdmin(ctx, env, module.Auth); err != nil {
		return err
	}
	if env.WebAPIKey != "" {
		if err := module.APIKeys.Ensure(ctx, "web", env.WebAPIKey); err != nil {
			return fmt.Errorf("WEB_API_KEY: %w", err)
		}
	}

	router, err := micro.NewRouter(env.TrustedProxies, client, micro.ServiceConfig{
		Name: "auth", Version: "1.0.0", QueueGroup: "auth",
	})
	if err != nil {
		return err
	}
	router.LoadRootMiddlewares(module.RootMiddlewares()...)
	if err := router.LoadControllers(module.Controllers()...); err != nil {
		return err
	}

	log.Printf("auth_service listening on :%s", env.Port)
	return router.Start(ctx, ":"+env.Port)
}

// CreateAPIKey issues a key from the command line (bootstrap: the first key cannot come through Kong).
func CreateAPIKey(client string, scopes []string) error {
	env, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, env.DatabaseURL, migrations.FS)
	if err != nil {
		return err
	}
	defer db.Close()
	body := apikeydto.Create{Client: client, Scopes: scopes}
	if detail := body.Validate(); detail != "" {
		return errors.New(detail)
	}
	created, err := apikey.NewService(db).Create(ctx, &body)
	if err != nil {
		return err
	}
	fmt.Println(created.Key) // stdout only: shown once
	return nil
}

// CreateAdmin applies ADMIN_EMAIL / ADMIN_PASSWORD once and exits (.tools/create-admin). The bare Service is enough:
// creating a user needs no signing key, and this must not generate one.
func CreateAdmin() error {
	env, err := config.Load()
	if err != nil {
		return err
	}
	if env.AdminEmail == "" || env.AdminPassword == "" {
		return errors.New("ADMIN_EMAIL and ADMIN_PASSWORD are required")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, env.DatabaseURL, migrations.FS)
	if err != nil {
		return err
	}
	defer db.Close()
	return EnsureAdmin(ctx, env, &auth.Service{DB: db})
}
