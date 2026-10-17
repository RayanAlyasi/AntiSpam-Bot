-- +goose Up
-- Settings changes, commands, manual and automated actions (DESIGN.md §8.6, §13.3, §14).
CREATE TABLE app.audit_log (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor     text        NOT NULL CHECK (actor <> ''),
    group_jid text,
    action    text        NOT NULL CHECK (action <> ''),
    diff      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_group_id_idx ON app.audit_log (group_jid, id DESC);

-- +goose Down
DROP TABLE IF EXISTS app.audit_log;
