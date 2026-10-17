-- +goose Up
-- whatsmeow's sqlstore owns the whatsmeow schema; app data lives in app (DESIGN.md §14).
CREATE SCHEMA IF NOT EXISTS app;
CREATE SCHEMA IF NOT EXISTS whatsmeow;

-- +goose Down
DROP SCHEMA IF EXISTS whatsmeow CASCADE;
DROP SCHEMA IF EXISTS app CASCADE;
