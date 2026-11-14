// Package config reads the service's environment (see .env.example).
package config

import (
	"fmt"
	"os"

	helmgoconfig "github.com/helmstudio/helmgo/config"
)

// Env is the service's validated configuration.
type Env struct {
	Port          string
	FileURLSecret string // shared with studio_service, which signs the links

	StorageDriver string // s3 | memory
	S3Bucket      string
	S3Region      string
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3PathStyle   bool
}

// Load resolves *_FILE secrets, then reads every variable with its default. It fails when FILE_URL_SECRET is under 16
// characters.
func Load() (Env, error) {
	if err := helmgoconfig.LoadFileSecrets(); err != nil {
		return Env{}, err
	}
	e := Env{
		Port:          helmgoconfig.Get("PORT", "8080"),
		FileURLSecret: os.Getenv("FILE_URL_SECRET"),
		StorageDriver: helmgoconfig.Get("STORAGE_DRIVER", "s3"),
		S3Bucket:      helmgoconfig.Get("S3_BUCKET", "helmstudio"),
		S3Region:      helmgoconfig.Get("S3_REGION", "us-east-1"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3AccessKey:   os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretKey:   os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3PathStyle:   helmgoconfig.Get("S3_FORCE_PATH_STYLE", "true") == "true",
	}
	if len(e.FileURLSecret) < 16 {
		return e, fmt.Errorf("FILE_URL_SECRET must be set to a random string of 16+ characters")
	}
	return e, nil
}
