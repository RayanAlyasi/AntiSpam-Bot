package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

const (
	// riverGooseVersion is the goose version that installs River's tables.
	riverGooseVersion = 2
	// RiverSchemaVersion is the River migration this goose version brings the schema to. It is
	// pinned: a River upgrade that adds a migration needs a new goose migration, so the schema
	// never changes under a deployer just because a dependency moved.
	RiverSchemaVersion = 8
	// RiverSchema is where River's tables live; the job client uses the same schema.
	RiverSchema = "app"
)

// riverMigration runs River's own migrations from goose (SQL-001: one migration system owns the
// order). It runs outside goose's transaction because some River migrations can't share one
// transaction; River applies each in its own and records it in river_migration, so a start that
// dies half-way resumes where it stopped.
func riverMigration() *goose.Migration {
	run := func(direction rivermigrate.Direction, target int) func(context.Context, *sql.DB) error {
		return func(ctx context.Context, db *sql.DB) error {
			m, err := rivermigrate.New(riverdatabasesql.New(db), &rivermigrate.Config{Schema: RiverSchema})
			if err != nil {
				return fmt.Errorf("river migrator: %w", err)
			}
			if _, err := m.Migrate(ctx, direction, &rivermigrate.MigrateOpts{TargetVersion: target}); err != nil {
				return fmt.Errorf("river migrate %s to %d: %w", direction, target, err)
			}
			return nil
		}
	}
	return goose.NewGoMigration(riverGooseVersion,
		&goose.GoFunc{RunDB: run(rivermigrate.DirectionUp, RiverSchemaVersion)},
		&goose.GoFunc{RunDB: run(rivermigrate.DirectionDown, -1)},
	)
}
