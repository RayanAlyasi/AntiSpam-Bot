package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoDatabaseURL is returned by Open when DATABASE_URL is empty.
var ErrNoDatabaseURL = errors.New("DATABASE_URL is not set")

// Open connects a pgx pool to url and checks that the database answers. Every connection runs
// its session in UTC and scans timestamptz values as UTC, whatever the process time zone is
// (SQL-004), so times never depend on the host's TZ.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, ErrNoDatabaseURL
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// The parse error can quote the URL, password included, so it isn't wrapped.
		return nil, errors.New("parse DATABASE_URL: invalid connection string")
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

// TxBeginner starts transactions; *pgxpool.Pool, *pgx.Conn and pgx.Tx (a savepoint) satisfy it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn in a transaction. It commits when fn returns nil and rolls back when fn returns
// an error or panics (the panic is re-raised after the rollback).
func InTx(ctx context.Context, db TxBeginner, fn func(tx pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// A rollback after a failed fn is best effort; the fn error is the one that matters.
		// context.WithoutCancel lets the rollback reach the server after ctx is cancelled.
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && err != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}
