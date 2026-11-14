// Package config reads the service's environment (see .env.example).
package config

import (
	"fmt"
	"os"
	"time"

	oragoconfig "github.com/helmstudio/orago/config"
	"github.com/helmstudio/orago/network"
)

// Env is the service's settings, read once at startup by Load.
type Env struct {
	Port           string
	DatabaseURL    string
	NatsURL        string
	NatsUser       string
	NatsPassword   string
	KeysDir        string
	PrivateKeyPEM  string   // JWT_PRIVATE_KEY(_FILE): the signing key itself; KeysDir/private.pem when empty
	WebAPIKey      string   // WEB_API_KEY(_FILE): added on start as a key of client "web" unless it exists
	HelmAPIKey     string   // HELM_API_KEY(_FILE): the same for client "helm" (the CLI)
	TrustedProxies []string // TRUSTED_PROXIES: hops allowed to set X-Forwarded-For (Kong, the web BFF), IPs or CIDRs
	Issuer         string
	AccessTTL      time.Duration
	RefreshTTL     time.Duration
	AdminEmail     string
	AdminPassword  string
}

// Load resolves the X_FILE secrets, applies defaults and parses the settings. It fails on an unreadable X_FILE, a bad
// TTL or TRUSTED_PROXIES entry, or when DATABASE_URL is missing.
func Load() (Env, error) {
	if err := oragoconfig.LoadFileSecrets(); err != nil {
		return Env{}, err
	}
	env := Env{
		Port:           oragoconfig.Get("PORT", "8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		NatsURL:        oragoconfig.Get("NATS_URL", "nats://nats:4222"),
		NatsUser:       os.Getenv("NATS_USER"),
		NatsPassword:   os.Getenv("NATS_PASSWORD"),
		KeysDir:        oragoconfig.Get("KEYS_DIR", "keys"),
		PrivateKeyPEM:  os.Getenv("JWT_PRIVATE_KEY"),
		WebAPIKey:      os.Getenv("WEB_API_KEY"),
		HelmAPIKey:     os.Getenv("HELM_API_KEY"),
		TrustedProxies: oragoconfig.List("TRUSTED_PROXIES"),
		Issuer:         oragoconfig.Get("JWT_ISSUER", "helm-auth"),
		AdminEmail:     os.Getenv("ADMIN_EMAIL"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
	}
	var err error
	if env.AccessTTL, err = time.ParseDuration(oragoconfig.Get("ACCESS_TTL", "15m")); err != nil {
		return env, fmt.Errorf("ACCESS_TTL: %w", err)
	}
	if env.RefreshTTL, err = time.ParseDuration(oragoconfig.Get("REFRESH_TTL", "720h")); err != nil {
		return env, fmt.Errorf("REFRESH_TTL: %w", err)
	}
	// The router is what parses these. Trying it here stops every command, the CLI ones too, before the database.
	if _, err = network.NewRouter(env.TrustedProxies); err != nil {
		return env, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	if env.DatabaseURL == "" {
		return env, fmt.Errorf("DATABASE_URL is required")
	}
	return env, nil
}
