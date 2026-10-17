// Package jobs runs background and periodic work on River (DESIGN.md §4.1), backed by the same
// Postgres as the app data. River's tables come from a goose migration (SQL-001), in the schema
// migrations.RiverSchema. Jobs are enqueued with InsertTx in the transaction that makes the change
// needing them, so a rolled-back change never leaves a job behind (SQL-006).
//
// WhatsApp writes never go through River: they use the paced action queue (DESIGN.md §10).
package jobs
