-- +goose Up
-- AI usage per group and UTC day, for budgets and cost (DESIGN.md §7.4, §14).
CREATE TABLE app.ai_usage (
    day           date   NOT NULL,
    group_jid     text   NOT NULL,
    calls         integer NOT NULL DEFAULT 0 CHECK (calls >= 0),
    input_tokens  bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cost_micros   bigint NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
    PRIMARY KEY (day, group_jid)
);

-- AI results: definitive verdicts only, no message text, kept 30 days (DESIGN.md §7.4, §11, §17).
CREATE TABLE app.ai_cache (
    key            text        PRIMARY KEY,
    model          text        NOT NULL,
    prompt_version text        NOT NULL,
    result         jsonb       NOT NULL,
    created_at     timestamptz NOT NULL
);

CREATE INDEX ai_cache_created_at_idx ON app.ai_cache (created_at);

-- +goose Down
DROP TABLE IF EXISTS app.ai_cache;
DROP TABLE IF EXISTS app.ai_usage;
