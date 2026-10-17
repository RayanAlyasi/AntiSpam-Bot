-- +goose Up
-- Fencing for the action executor: a claim records which store instance (process) made it, and
-- only that instance may settle it, so an executor that lost its lock mid-call can't overwrite
-- the new owner's state (DESIGN.md §10.1).
ALTER TABLE app.actions ADD COLUMN claimed_by text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app.actions DROP COLUMN IF EXISTS claimed_by;
