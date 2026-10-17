-- +goose Up
-- Local bans: kick from this group and block rejoining it (DESIGN.md §9.1, §9.6, §14).
CREATE TABLE app.group_bans (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_jid      text        NOT NULL CHECK (group_jid <> ''),
    person_id      bigint      NOT NULL, -- app.persons(id); foreign key added once the persons table exists (wp-f)
    reason         text        NOT NULL CHECK (reason IN ('spam', 'other', 'policy')),
    decided_by     text        NOT NULL CHECK (decided_by IN ('auto', 'admin', 'network')),
    decider_person bigint,
    detection_id   bigint      REFERENCES app.detections (id) ON DELETE SET NULL, -- detections expire (180 d), bans stay
    review_id      bigint,
    network_ban_id bigint,
    rejoin_block   boolean     NOT NULL,
    active         boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    lifted_by      text,
    lifted_at      timestamptz,
    CHECK (active = (lifted_at IS NULL))
);

-- At most one active ban per person per group, so a repeated decision never doubles it.
CREATE UNIQUE INDEX group_bans_active_idx ON app.group_bans (group_jid, person_id) WHERE active;
CREATE INDEX group_bans_group_created_idx ON app.group_bans (group_jid, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS app.group_bans;
