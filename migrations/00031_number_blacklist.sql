-- +goose Up
-- The manual phone-number blacklist (DESIGN.md §9.8, §14). pn_hash is the keyed HMAC-SHA256 of
-- pn_e164 that content matching uses; removing an entry sets removed_at (the undo, kept for audit).
CREATE TABLE app.number_blacklist (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope             text        NOT NULL CHECK (scope IN ('group', 'instance')),
    group_jid         text,
    pn_e164           text        NOT NULL CHECK (pn_e164 ~ '^\+[1-9][0-9]{6,14}$'),
    pn_hash           text        NOT NULL CHECK (pn_hash ~ '^[0-9a-f]{64}$'),
    person_id         bigint,
    on_whatsapp       boolean,
    reason            text        NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    added_by_person   bigint,
    added_at          timestamptz NOT NULL DEFAULT now(),
    removed_by_person bigint,
    removed_at        timestamptz,
    CHECK ((scope = 'group') = (group_jid IS NOT NULL))
);

CREATE UNIQUE INDEX number_blacklist_active_idx ON app.number_blacklist (scope, group_jid, pn_e164) NULLS NOT DISTINCT
    WHERE removed_at IS NULL;
CREATE INDEX number_blacklist_group_idx ON app.number_blacklist (group_jid, added_at DESC) WHERE removed_at IS NULL;

CREATE TRIGGER number_blacklist_changed
    AFTER INSERT OR DELETE OR TRUNCATE ON app.number_blacklist
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();
CREATE TRIGGER number_blacklist_changed_update
    AFTER UPDATE OF scope, group_jid, pn_hash, removed_at ON app.number_blacklist
    FOR EACH STATEMENT EXECUTE FUNCTION app.notify_rules_changed();

-- +goose Down
DROP TABLE IF EXISTS app.number_blacklist;
