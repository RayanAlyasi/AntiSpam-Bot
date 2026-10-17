-- +goose Up
-- Confirmed-spam corpus (DESIGN.md §7.5, §14). Spam only: the bot keeps no normal-message corpus.
-- Text is redacted before it's stored; advertised numbers are keyed hashes.
CREATE TABLE app.corpus_entries (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind              text             NOT NULL CHECK (kind IN ('message', 'profile')),
    label             text             NOT NULL DEFAULT 'spam' CHECK (label = 'spam'),
    scope             text             NOT NULL CHECK (scope IN ('group', 'global')),
    group_jid         text,
    status            text             NOT NULL CHECK (status IN ('provisional', 'active', 'retracted', 'promoted', 'expired')),
    source            text             NOT NULL CHECK (source IN ('review', 'command', 'auto', 'import')),
    import_batch_id   bigint,
    source_review_id  bigint,
    source_action_id  bigint,
    decided_by_person bigint,
    text_raw_redacted text             NOT NULL DEFAULT '',
    text_norm         text             NOT NULL DEFAULT '',
    simhash           bigint,
    minhash           bytea CHECK (minhash IS NULL OR length(minhash) = 512),
    keywords          text[]           NOT NULL DEFAULT '{}',
    urls              text[]           NOT NULL DEFAULT '{}',
    domains           text[]           NOT NULL DEFAULT '{}',
    number_hashes     bytea[]          NOT NULL DEFAULT '{}',
    channel_jids      text[]           NOT NULL DEFAULT '{}',
    invite_codes      text[]           NOT NULL DEFAULT '{}',
    media_sha256      bytea[]          NOT NULL DEFAULT '{}',
    phashes           bigint[]         NOT NULL DEFAULT '{}',
    profile           jsonb,
    biz               jsonb,
    confidence        double precision NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 1),
    hits              integer          NOT NULL DEFAULT 0,
    last_hit_at       timestamptz,
    created_at        timestamptz      NOT NULL DEFAULT now(),
    expires_at        timestamptz      NOT NULL,
    CHECK (scope = 'global' OR group_jid IS NOT NULL)
);

CREATE INDEX corpus_entries_group_idx ON app.corpus_entries (group_jid, created_at DESC);
CREATE INDEX corpus_entries_simhash_idx ON app.corpus_entries (simhash) WHERE simhash IS NOT NULL;
CREATE INDEX corpus_entries_expires_idx ON app.corpus_entries (expires_at);
CREATE INDEX corpus_entries_domains_idx ON app.corpus_entries USING gin (domains);
CREATE INDEX corpus_entries_number_hashes_idx ON app.corpus_entries USING gin (number_hashes);
CREATE INDEX corpus_entries_channel_jids_idx ON app.corpus_entries USING gin (channel_jids);

-- One row per pattern per group: the ≥ 3 unrelated groups promotion rule counts these (§7.5).
CREATE TABLE app.corpus_confirmations (
    pattern_key     text        NOT NULL CHECK (pattern_key <> ''),
    group_jid       text        NOT NULL CHECK (group_jid <> ''),
    admin_person_id bigint,
    corpus_entry_id bigint      NOT NULL REFERENCES app.corpus_entries (id) ON DELETE CASCADE,
    at              timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pattern_key, group_jid)
);

CREATE INDEX corpus_confirmations_entry_idx ON app.corpus_confirmations (corpus_entry_id);

-- Rules created from a corpus entry, so retracting the entry removes them (§7.5).
CREATE TABLE app.learned_rules (
    rule_id         bigint      NOT NULL REFERENCES app.rules (id) ON DELETE CASCADE,
    corpus_entry_id bigint      NOT NULL REFERENCES app.corpus_entries (id) ON DELETE CASCADE,
    group_jid       text        NOT NULL CHECK (group_jid <> ''),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (rule_id, corpus_entry_id)
);

CREATE INDEX learned_rules_entry_idx ON app.learned_rules (corpus_entry_id);

-- +goose Down
DROP TABLE IF EXISTS app.learned_rules;
DROP TABLE IF EXISTS app.corpus_confirmations;
DROP TABLE IF EXISTS app.corpus_entries;
