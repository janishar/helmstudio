// Package migrations: the goose SQL files, embedded (each file names what it adds).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
