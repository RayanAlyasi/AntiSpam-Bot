package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

type txKey struct{}

// ContextWithTx returns ctx carrying tx, so writes made through Conn (or TxFrom) while handling
// one message join that message's transaction.
func ContextWithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFrom returns the transaction ctx carries, if any.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok && tx != nil
}

// Conn is a db.DBTX that runs in the context's transaction when there is one (ContextWithTx)
// and on Pool otherwise. Long-lived services such as the review store use it, so the review
// items a detection opens commit or roll back with that detection.
type Conn struct{ Pool db.DBTX }

func (c Conn) on(ctx context.Context) db.DBTX {
	if tx, ok := TxFrom(ctx); ok {
		return tx
	}
	return c.Pool
}

// Exec implements db.DBTX.
func (c Conn) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return c.on(ctx).Exec(ctx, sql, args...)
}

// Query implements db.DBTX.
func (c Conn) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return c.on(ctx).Query(ctx, sql, args...)
}

// QueryRow implements db.DBTX.
func (c Conn) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return c.on(ctx).QueryRow(ctx, sql, args...)
}
