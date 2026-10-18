-- +goose Up
-- WhatsApp communities (DESIGN.md §12.4, §14). A group row records the community it belongs to;
-- a community's own row (kind 'community') and its announcement group (kind 'announcement') are
-- tracked like any group but hold no chat members can spam in. community_groups is the
-- community's own list of its groups, including the ones the bot isn't in, so admins can be told
-- where to add it.
ALTER TABLE app.groups
    ADD COLUMN kind           text NOT NULL DEFAULT 'group'
                              CHECK (kind IN ('group', 'community', 'announcement')),
    ADD COLUMN community_jid  text NOT NULL DEFAULT '' CHECK (community_jid = '' OR community_jid LIKE '%@g.us'),
    ADD COLUMN community_name text NOT NULL DEFAULT '';

CREATE INDEX groups_community_idx ON app.groups (community_jid) WHERE community_jid <> '';

CREATE TABLE app.community_groups (
    community_jid text        NOT NULL CHECK (community_jid LIKE '%@g.us'),
    group_jid     text        NOT NULL CHECK (group_jid LIKE '%@g.us'),
    name          text        NOT NULL DEFAULT '',
    announcement  boolean     NOT NULL DEFAULT false,
    refreshed_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (community_jid, group_jid)
);

-- +goose Down
DROP TABLE IF EXISTS app.community_groups;
DROP INDEX IF EXISTS app.groups_community_idx;
ALTER TABLE app.groups DROP COLUMN IF EXISTS community_name, DROP COLUMN IF EXISTS community_jid, DROP COLUMN IF EXISTS kind;
