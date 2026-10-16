---
name: add-migration
description: Adds a database schema change correctly (goose migration, sqlc queries, regenerated code). Use whenever a feature needs a new table, column, index or query.
---
1. Create `migrations/<NNNNN>_<slug>.sql` with `-- +goose Up` and `-- +goose Down` sections, using the next free number. For `CREATE INDEX CONCURRENTLY`, add `-- +goose NO TRANSACTION`.
2. Put queries in `internal/store/queries/<area>.sql` with sqlc annotations (`-- name: GetX :one`).
3. Run `make gen`, then `make check-gen`. Never edit generated files.
4. Cover the new queries with an integration test (build tag `integration`, testcontainers Postgres).
5. A migration that's already committed is never edited. Add a new one instead.
