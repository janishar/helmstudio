// Package config reads the service's environment (see .env.example).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	oragoconfig "github.com/helmstudio/orago/config"
)

// Env is the service's validated configuration.
type Env struct {
	Port          string
	DatabaseURL   string
	NatsURL       string
	NatsUser      string
	NatsPassword  string
	PublicBaseURL string // base of signed /files/… links (what clients reach: Kong)
	FileURLSecret string
	FileURLTTL    time.Duration
	MaxBodyMB     int64 // cap on any request body (Kong also caps)
}

// Load resolves *_FILE secrets, then reads every variable with its default. It fails when DATABASE_URL is missing,
// FILE_URL_SECRET is under 16 characters, or a number or duration is invalid.
func Load() (Env, error) {
	if err := oragoconfig.LoadFileSecrets(); err != nil {
		return Env{}, err
	}
	e := Env{
		Port:          oragoconfig.Get("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		NatsURL:       oragoconfig.Get("NATS_URL", "nats://nats:4222"),
		NatsUser:      os.Getenv("NATS_USER"),
		NatsPassword:  os.Getenv("NATS_PASSWORD"),
		PublicBaseURL: oragoconfig.Get("PUBLIC_BASE_URL", "http://localhost:8300"),
		FileURLSecret: os.Getenv("FILE_URL_SECRET"),
	}
	var err error
	if e.FileURLTTL, err = time.ParseDuration(oragoconfig.Get("FILE_URL_TTL", "1h")); err != nil {
		return e, fmt.Errorf("FILE_URL_TTL: %w", err)
	}
	if e.MaxBodyMB, err = strconv.ParseInt(oragoconfig.Get("MAX_BODY_MB", "1"), 10, 64); err != nil {
		return e, fmt.Errorf("MAX_BODY_MB: %w", err)
	}
	if e.DatabaseURL == "" {
		return e, fmt.Errorf("DATABASE_URL is required")
	}
	if len(e.FileURLSecret) < 16 {
		return e, fmt.Errorf("FILE_URL_SECRET must be set to a random string of 16+ characters")
	}
	return e, nil
}
