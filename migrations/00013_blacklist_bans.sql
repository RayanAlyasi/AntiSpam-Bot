-- +goose Up
-- Local bans the phone-number blacklist placed (DESIGN.md §9.8), by ban ID. Removing a number
-- lifts the active ban only when it is this one, never a ban the group's admins placed.
CREATE TABLE app.blacklist_bans (
    group_jid text        NOT NULL,
    person_id bigint      NOT NULL,
    ban_id    bigint      NOT NULL, -- the group_bans row the blacklist created
    at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_jid, person_id)
);

-- +goose Down
DROP TABLE IF EXISTS app.blacklist_bans;
