// Package migrations holds the goose migrations (DESIGN.md §14, §19.3). They're embedded in the
// binary and run forward-only on start. SQL migrations are the numbered .sql files; River's
// tables come from a Go migration (river.go), so goose owns every schema change.
package migrations

import (
	"embed"

	"github.com/pressly/goose/v3"
)

// SQL is the embedded set of SQL migrations.
//
//go:embed *.sql
var SQL embed.FS

// Go returns the migrations written in Go, to pass to goose.WithGoMigrations.
func Go() []*goose.Migration {
	return []*goose.Migration{riverMigration()}
}
