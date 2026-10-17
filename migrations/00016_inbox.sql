-- +goose Up
-- The durable buffer of received messages (DESIGN.md §5.1, §14) and the text-free index of
-- processed group messages (§14, §17). Inbox rows are kept 72 h; message_index rows 7 d, or 30 d
-- for spam or actioned messages.
CREATE TABLE app.inbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    bot_id       bigint      NOT NULL REFERENCES app.bots (id),
    chat         text        NOT NULL,
    msg_id       text        NOT NULL,
    -- whatsmeow's edit attribute ('' for a normal message). With the sender and kind it makes
    -- redeliveries of one event duplicates, while a decryptable copy of an undecryptable one,
    -- or another member's message reusing the same (client-chosen) ID, is new.
    edit_seq     text        NOT NULL DEFAULT '',
    kind         text        NOT NULL CHECK (kind IN ('message', 'undecryptable')),
    sender       text        NOT NULL DEFAULT '',
    raw          bytea,
    info         jsonb       NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    -- A worker claims rows with FOR UPDATE SKIP LOCKED and stamps claimed_until; a crashed
    -- worker's claim expires and another worker resumes the row.
    claimed_until timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text,
    UNIQUE (chat, sender, msg_id, edit_seq, kind)
);

CREATE INDEX inbox_pending_idx ON app.inbox (id) WHERE processed_at IS NULL;
CREATE INDEX inbox_received_idx ON app.inbox (received_at);

-- Wake the inbox workers once per inserting statement (they then claim with SKIP LOCKED).
-- +goose StatementBegin
CREATE FUNCTION app.inbox_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('app_inbox', '');
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER inbox_notify AFTER INSERT ON app.inbox
    FOR EACH STATEMENT EXECUTE FUNCTION app.inbox_notify();

CREATE TABLE app.message_index (
    group_jid     text        NOT NULL,
    msg_id        text        NOT NULL,
    sender_person bigint      REFERENCES app.persons (id),
    ts            timestamptz NOT NULL,
    fingerprint   bytea,
    simhash       bigint,
    url_domains   text[]      NOT NULL DEFAULT '{}',
    media_sha256  bytea[]     NOT NULL DEFAULT '{}',
    verdict       text        NOT NULL DEFAULT 'clean' CHECK (verdict IN ('clean', 'spam', 'policy')),
    actioned      boolean     NOT NULL DEFAULT false,
    expires_at    timestamptz NOT NULL,
    PRIMARY KEY (group_jid, msg_id)
);

CREATE INDEX message_index_simhash_idx ON app.message_index (simhash);
CREATE INDEX message_index_sender_idx ON app.message_index (sender_person, ts);
CREATE INDEX message_index_expires_idx ON app.message_index (expires_at);

-- +goose Down
DROP TABLE IF EXISTS app.message_index;
DROP TRIGGER IF EXISTS inbox_notify ON app.inbox;
DROP FUNCTION IF EXISTS app.inbox_notify();
DROP TABLE IF EXISTS app.inbox;
