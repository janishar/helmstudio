// Package config reads the service's environment (see .env.example).
package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	helmgoconfig "github.com/helmstudio/helmgo/config"
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

	// the object store, read to check an upload and to remove deleted takes' objects (files_service's settings)
	StorageDriver string // s3 | memory
	S3Bucket      string
	S3Region      string
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3PathStyle   bool

	// RemoteModels is the deployment's switch for remote model providers (API 1.15): off unless 1, true, on or yes (SwitchOn)
	RemoteModels bool

	// PluginDirs are the read-only folders plugins/*/plugin.json are read from (API 1.61; STUDIO_PLUGINS_DIRS, `:`-separated)
	PluginDirs []string
}

// Load resolves *_FILE secrets, then reads every variable with its default. It fails when DATABASE_URL is missing,
// FILE_URL_SECRET is under 16 characters, or a number or duration is invalid.
// SwitchOn reads an on/off switch as pipelines_service does (config.REMOTE_MODELS): 1, true, on or yes, any case.
func SwitchOn(v string) bool {
	return slices.Contains([]string{"1", "true", "on", "yes"}, strings.ToLower(v))
}

func Load() (Env, error) {
	if err := helmgoconfig.LoadFileSecrets(); err != nil {
		return Env{}, err
	}
	e := Env{
		Port:          helmgoconfig.Get("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		NatsURL:       helmgoconfig.Get("NATS_URL", "nats://nats:4222"),
		NatsUser:      os.Getenv("NATS_USER"),
		NatsPassword:  os.Getenv("NATS_PASSWORD"),
		PublicBaseURL: helmgoconfig.Get("PUBLIC_BASE_URL", "http://localhost:8300"),
		FileURLSecret: os.Getenv("FILE_URL_SECRET"),
		StorageDriver: helmgoconfig.Get("STORAGE_DRIVER", "s3"),
		S3Bucket:      helmgoconfig.Get("S3_BUCKET", "helmstudio"),
		S3Region:      helmgoconfig.Get("S3_REGION", "us-east-1"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3AccessKey:   os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretKey:   os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3PathStyle:   helmgoconfig.Get("S3_FORCE_PATH_STYLE", "true") == "true",
		RemoteModels:  SwitchOn(os.Getenv("STUDIO_REMOTE_MODELS")),
		PluginDirs:    strings.Split(helmgoconfig.Get("STUDIO_PLUGINS_DIRS", "/plugins"), ":"),
	}
	var err error
	if e.FileURLTTL, err = time.ParseDuration(helmgoconfig.Get("FILE_URL_TTL", "1h")); err != nil {
		return e, fmt.Errorf("FILE_URL_TTL: %w", err)
	}
	if e.MaxBodyMB, err = strconv.ParseInt(helmgoconfig.Get("MAX_BODY_MB", "1"), 10, 64); err != nil {
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
