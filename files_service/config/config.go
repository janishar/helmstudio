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
	FileURLSecret string        // shared with catalog_service, which signs the links
	WorkRetention time.Duration // the janitor deletes work/ objects older than this (RETENTION_WORK_DAYS)

	StorageDriver string // s3 | memory
	S3Bucket      string
	S3Region      string
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3PathStyle   bool
}

// Load resolves *_FILE secrets, then reads every variable with its default. It fails when FILE_URL_SECRET is under 16
// characters or RETENTION_WORK_DAYS is not a whole number of days.
func Load() (Env, error) {
	if err := oragoconfig.LoadFileSecrets(); err != nil {
		return Env{}, err
	}
	e := Env{
		Port:          oragoconfig.Get("PORT", "8080"),
		FileURLSecret: os.Getenv("FILE_URL_SECRET"),
		StorageDriver: oragoconfig.Get("STORAGE_DRIVER", "s3"),
		S3Bucket:      oragoconfig.Get("S3_BUCKET", "ora"),
		S3Region:      oragoconfig.Get("S3_REGION", "us-east-1"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3AccessKey:   os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretKey:   os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3PathStyle:   oragoconfig.Get("S3_FORCE_PATH_STYLE", "true") == "true",
	}
	days, err := strconv.Atoi(oragoconfig.Get("RETENTION_WORK_DAYS", "30"))
	if err != nil || days < 1 {
		return e, fmt.Errorf("RETENTION_WORK_DAYS: a whole number of days, 1 or more")
	}
	e.WorkRetention = time.Duration(days) * 24 * time.Hour
	if len(e.FileURLSecret) < 16 {
		return e, fmt.Errorf("FILE_URL_SECRET must be set to a random string of 16+ characters")
	}
	return e, nil
}
