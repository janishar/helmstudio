// Package config reads process environment variables, including the file-backed
// secrets that compose mounts through X_FILE variables.
package config

import (
	"fmt"
	"os"
	"strings"
)

// LoadFileSecrets sets X to the trimmed contents of the file named by X_FILE, for
// every X_FILE that has a path. The file wins over any X from .env, because compose
// secrets are the production source.
func LoadFileSecrets() error {
	for _, entry := range os.Environ() {
		key, path, _ := strings.Cut(entry, "=")
		name, ok := strings.CutSuffix(key, "_FILE")
		if !ok || path == "" {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if err := os.Setenv(name, strings.TrimSpace(string(contents))); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// Get returns the variable, or fallback when it is unset or empty.
func Get(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// List splits a comma-separated variable, trimming entries and dropping empty ones.
// It returns nil when no entries remain.
func List(key string) []string {
	var entries []string
	for _, entry := range strings.Split(os.Getenv(key), ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}
