// Package store owns Postgres access (DESIGN.md §14): the connection pool, transactions and
// repositories built on the sqlc-generated queries in store/db. goose migrations live in the
// top-level migrations directory.
package store
