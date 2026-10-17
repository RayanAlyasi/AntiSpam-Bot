-- +goose Up
-- One versioned settings document per group, plus every earlier version (DESIGN.md §12.1, §14).
-- The document is validated by internal/policy before it is written.
CREATE TABLE app.group_settings (
    group_jid  text        PRIMARY KEY CHECK (group_jid <> ''),
    version    integer     NOT NULL CHECK (version > 0),
    doc        jsonb       NOT NULL,
    updated_by text        NOT NULL CHECK (updated_by <> ''),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.group_settings_history (
    group_jid  text        NOT NULL,
    version    integer     NOT NULL CHECK (version > 0),
    doc        jsonb       NOT NULL,
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (group_jid, version)
);

-- +goose Down
DROP TABLE IF EXISTS app.group_settings_history;
DROP TABLE IF EXISTS app.group_settings;
