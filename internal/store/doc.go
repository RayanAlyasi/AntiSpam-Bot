// Package store owns Postgres access (DESIGN.md §14): the connection pool (Open), transactions
// (InTx), schema migrations (Migrate) and repositories built on the sqlc-generated queries in
// store/db. The goose migrations live in the top-level migrations package and are embedded in
// the binary; River's job tables are one of them (store/jobs runs the client).
package store
