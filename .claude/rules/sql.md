---
paths:
  - "internal/store/**"
  - "migrations/**"
  - "**/*.sql"
  - "sqlc.yaml"
---
# SQL, migrations and Postgres

- [SQL-001] goose owns the schema. River's tables are added through goose migrations too, so two migration systems never run independently.
- [SQL-002] Use `-- +goose NO TRANSACTION` for `CREATE INDEX CONCURRENTLY`.
- [SQL-003] After changing queries or migrations, run `make gen`; `make check-gen` fails when sqlc output is stale. Never edit generated files.
- [SQL-004] pgx v5.11 reads `timestamptz` in `time.Local`, so containers and tests run with `TZ=UTC`.
- [SQL-005] Don't use `QueryExecModeSimpleProtocol` (pgx had an SQL-injection fix in that mode).
- [SQL-006] River v0.49: its transaction helpers reuse your transaction, so roll back on error. Enqueue with `InsertTx` in the same transaction as the state change.
