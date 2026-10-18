-- +goose Up
-- One message → corpus entry table: corpus_sources (00033) is the one the corpus uses, so
-- corpus_messages (00034) goes. corpus_allow stays.
-- Rows written while both existed move over first, so a later "not spam" still retracts them.
INSERT INTO app.corpus_sources (group_jid, msg_id, corpus_entry_id, at)
SELECT group_jid, msg_id, entry_id, created_at FROM app.corpus_messages
ON CONFLICT DO NOTHING;
DROP TABLE IF EXISTS app.corpus_messages;

-- +goose Down
CREATE TABLE app.corpus_messages (
    group_jid  text        NOT NULL CHECK (group_jid <> ''),
    msg_id     text        NOT NULL CHECK (msg_id <> ''),
    entry_id   bigint      NOT NULL REFERENCES app.corpus_entries (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_jid, msg_id, entry_id)
);
