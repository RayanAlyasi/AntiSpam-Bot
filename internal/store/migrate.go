package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/RayanAlyasi/AntiSpam-Bot/migrations"
)

// ErrSchemaTooNew means the database was migrated by a newer release than this binary. Running
// against a schema the binary doesn't know could corrupt data, so the bot refuses to start
// (DESIGN.md §19.3: migrations are forward-only).
var ErrSchemaTooNew = errors.New("database schema is newer than this binary")

// SchemaTooNewError reports the versions behind ErrSchemaTooNew.
type SchemaTooNewError struct {
	DBVersion     int64 // the goose version recorded in the database
	BinaryVersion int64 // the newest migration embedded in this binary
}

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("database schema is at migration %d but this binary only knows up to %d: "+
		"run a release that includes migration %d or newer; downgrades aren't supported, so going "+
		"back to this release means restoring a backup taken before the upgrade",
		e.DBVersion, e.BinaryVersion, e.DBVersion)
}

// Is makes errors.Is(err, ErrSchemaTooNew) true.
func (e *SchemaTooNewError) Is(target error) bool { return target == ErrSchemaTooNew }

// MigrateResult says what Migrate did.
type MigrateResult struct {
	From, To int64 // schema version before and after
	Applied  int   // migrations applied
}

// Migrate brings the schema up to the newest embedded migration. It first refuses a database
// that is ahead of the binary (a *SchemaTooNewError), then applies pending migrations under a
// Postgres advisory lock, so two processes starting at once don't race.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (MigrateResult, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }() // closes the database/sql wrapper only, not the pool

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return MigrateResult{}, fmt.Errorf("migration lock: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.SQL,
		goose.WithGoMigrations(migrations.Go()...),
		goose.WithDisableGlobalRegistry(true),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("load migrations: %w", err)
	}
	current, newest, err := p.GetVersions(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("read schema version: %w", err)
	}
	if current > newest {
		return MigrateResult{From: current, To: current}, &SchemaTooNewError{DBVersion: current, BinaryVersion: newest}
	}
	applied, err := p.Up(ctx)
	if err != nil {
		return MigrateResult{From: current}, fmt.Errorf("apply migrations: %w", err)
	}
	to, err := p.GetDBVersion(ctx)
	if err != nil {
		return MigrateResult{From: current}, fmt.Errorf("read schema version: %w", err)
	}
	return MigrateResult{From: current, To: to, Applied: len(applied)}, nil
}

// NewestMigration is the newest migration version embedded in the binary.
func NewestMigration() (int64, error) {
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		return 0, fmt.Errorf("list migrations: %w", err)
	}
	var newest int64
	for _, name := range names {
		v, err := goose.NumericComponent(name)
		if err != nil {
			return 0, fmt.Errorf("migration %s: %w", name, err)
		}
		newest = max(newest, v)
	}
	for _, m := range migrations.Go() {
		newest = max(newest, m.Version)
	}
	return newest, nil
}
