-- +goose Up
-- Block- and allow-list rules and known fingerprints (DESIGN.md §12.3, §14). The bot keeps both in
-- memory and reloads them when a NOTIFY on rules_changed arrives (F-041).
CREATE TABLE app.rules (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope        text             NOT NULL CHECK (scope IN ('global', 'bot', 'group')),
    scope_id     text             NOT NULL DEFAULT '',
    pack         text             NOT NULL DEFAULT '',
    type         text             NOT NULL CHECK (type IN ('keyword', 'phrase', 'regex', 'domain', 'url_prefix',
                                                           'phone_number', 'channel', 'invite', 'sha256', 'phash',
                                                           'simhash')),
    pattern      text             NOT NULL CHECK (pattern <> '' AND length(pattern) <= 2048),
    norm_pattern text             NOT NULL CHECK (norm_pattern <> ''),
    action_class text             NOT NULL CHECK (action_class IN ('hard', 'weighted', 'allow')),
    weight       double precision NOT NULL DEFAULT 0 CHECK (weight BETWEEN -20 AND 20),
    enabled      boolean          NOT NULL DEFAULT true,
    created_by   bigint,
    hits         bigint           NOT NULL DEFAULT 0,
    overturns    bigint           NOT NULL DEFAULT 0,
    created_at   timestamptz      NOT NULL DEFAULT now(),
    CHECK ((scope = 'global') = (scope_id = '')),
    CHECK (scope = 'global' OR pack = '')
);

CREATE UNIQUE INDEX rules_pattern_idx ON app.rules (scope, scope_id, pack, type, norm_pattern, action_class);

CREATE TABLE app.fingerprints (
    kind       text        NOT NULL CHECK (kind IN ('sha256', 'phash', 'simhash', 'channel', 'invite', 'number', 'url')),
    value      text        NOT NULL CHECK (value <> ''),
    label      text        NOT NULL CHECK (label IN ('spam', 'allow')),
    source     text        NOT NULL CHECK (source IN ('admin', 'auto', 'feed')),
    scope      text        NOT NULL CHECK (scope IN ('global', 'bot', 'group')),
    scope_id   text        NOT NULL DEFAULT '',
    hits       bigint      NOT NULL DEFAULT 0,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, value, scope, scope_id),
    CHECK ((scope = 'global') = (scope_id = ''))
);

-- One notification per statement, and none for hit counters, so a busy group doesn't make every
-- node reload its rules.
-- +goose StatementBegin
CREATE FUNCTION app.notify_rules_changed() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    PERFORM pg_notify('rules_changed', TG_TABLE_NAME);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER rules_changed
    AFTER INSERT OR DELETE OR TRUNCATE ON app.rules
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();
CREATE TRIGGER rules_changed_update
    AFTER UPDATE OF scope, scope_id, pack, type, pattern, norm_pattern, action_class, weight, enabled ON app.rules
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();
CREATE TRIGGER fingerprints_changed
    AFTER INSERT OR DELETE OR TRUNCATE ON app.fingerprints
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();
CREATE TRIGGER fingerprints_changed_update
    AFTER UPDATE OF kind, value, label, source, scope, scope_id ON app.fingerprints
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();

-- +goose Down
DROP TABLE IF EXISTS app.fingerprints;
DROP TABLE IF EXISTS app.rules;
DROP FUNCTION IF EXISTS app.notify_rules_changed();
