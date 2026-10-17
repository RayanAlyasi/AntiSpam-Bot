-- +goose Up
-- Bot numbers and the groups they are in (DESIGN.md §14, §15). whatsmeow's own device store is in
-- the whatsmeow schema; bots records each paired number and its connector lease.
CREATE TABLE app.bots (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    jid          text        NOT NULL UNIQUE CHECK (jid <> ''),
    lid          text,
    display_name text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'paused', 'banned', 'unpaired')),
    -- A process connects a bot only while lease_owner is its ID and lease_until is in the future.
    lease_owner  text,
    lease_until  timestamptz,
    rate_profile jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.groups (
    jid               text        PRIMARY KEY CHECK (jid LIKE '%@g.us'),
    bot_id            bigint      REFERENCES app.bots (id),
    name              text        NOT NULL DEFAULT '',
    topic             text        NOT NULL DEFAULT '',
    size              integer     NOT NULL DEFAULT 0,
    addressing_mode   text        NOT NULL DEFAULT '',
    bot_is_admin      boolean     NOT NULL DEFAULT false,
    announce          boolean     NOT NULL DEFAULT false,
    locked            boolean     NOT NULL DEFAULT false,
    join_approval     boolean     NOT NULL DEFAULT false,
    joined_at         timestamptz,
    status            text        NOT NULL DEFAULT 'pending_admin'
                      CHECK (status IN ('pending_admin', 'active', 'left')),
    last_info_refresh timestamptz
);

CREATE INDEX groups_refresh_idx ON app.groups (last_info_refresh) WHERE status <> 'left';

-- +goose Down
DROP TABLE IF EXISTS app.groups;
DROP TABLE IF EXISTS app.bots;
