-- +goose Up
-- People across groups and their identifiers (DESIGN.md §7.7, §14). The LID is primary, the
-- phone number is added when a free source reveals it. Merging two person records re-points
-- every app column that refers to a person: each foreign key to app.persons(id), and each bigint
-- column named person_id, *_person or *_person_id.
CREATE TABLE app.persons (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lid               text UNIQUE,
    pn                text,
    pn_country        text,
    push_name         text        NOT NULL DEFAULT '',
    verified_name     text        NOT NULL DEFAULT '',
    profile_pic_id    text,
    profile_pic_phash bigint,
    about_text_hash   bytea,
    business          jsonb,
    global_reputation double precision NOT NULL DEFAULT 0,
    first_seen        timestamptz NOT NULL DEFAULT now(),
    last_profile_scan timestamptz,
    -- Set when this record was merged into another (a PN-only record into its LID person).
    merged_into       bigint REFERENCES app.persons (id)
);

CREATE TABLE app.identities (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    person_id     bigint      NOT NULL REFERENCES app.persons (id),
    kind          text        NOT NULL CHECK (kind IN ('lid', 'pn', 'other')),
    value         text        NOT NULL CHECK (value <> ''),
    country_code  text,
    number_prefix text,
    source        text        NOT NULL
                  CHECK (source IN ('sender_alt', 'participant', 'event', 'lid_store', 'dm', 'self_vcard', 'usync')),
    first_seen    timestamptz NOT NULL,
    last_seen     timestamptz NOT NULL,
    -- Closed links (a recycled number) are kept, never overwritten.
    valid_to      timestamptz
);

CREATE UNIQUE INDEX identities_open_idx ON app.identities (kind, value) WHERE valid_to IS NULL;
CREATE INDEX identities_kind_value_idx ON app.identities (kind, value);
CREATE INDEX identities_person_idx ON app.identities (person_id);

CREATE TABLE app.person_merges (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_person bigint      NOT NULL REFERENCES app.persons (id),
    into_person bigint      NOT NULL REFERENCES app.persons (id),
    reason      text        NOT NULL,
    at          timestamptz NOT NULL
);

CREATE TABLE app.members (
    group_jid         text        NOT NULL,
    person_id         bigint      NOT NULL REFERENCES app.persons (id),
    role              text        NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin', 'superadmin')),
    joined_at         timestamptz,
    first_seen        timestamptz NOT NULL,
    msg_count         integer     NOT NULL DEFAULT 0,
    clean_count       integer     NOT NULL DEFAULT 0,
    trust             double precision NOT NULL DEFAULT 0,
    strikes           integer     NOT NULL DEFAULT 0,
    last_strike_at    timestamptz,
    muted_until       timestamptz,
    allowlisted       boolean     NOT NULL DEFAULT false,
    pre_existing      boolean     NOT NULL DEFAULT false,
    established       boolean     NOT NULL DEFAULT false,
    restricted        boolean     NOT NULL DEFAULT false,
    restricted_since  timestamptz,
    restricted_until  timestamptz,
    restricted_reason text CHECK (restricted_reason IN ('compromised', 'manual')),
    PRIMARY KEY (group_jid, person_id)
);

CREATE INDEX members_person_idx ON app.members (person_id);

-- +goose Down
DROP TABLE IF EXISTS app.members;
DROP TABLE IF EXISTS app.person_merges;
DROP TABLE IF EXISTS app.identities;
DROP TABLE IF EXISTS app.persons;
