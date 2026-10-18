-- +goose Up
-- Which corpus entries admins' confirmations (a review ✅, `.spam`) created from which message, so
-- overturning that message retracts every one of them (DESIGN.md §7.5 "Overturns remove what was
-- learned"): a message confirmed twice has two entries. No text: the entries hold the redacted
-- text and the fingerprints.
CREATE TABLE app.corpus_messages (
    group_jid  text        NOT NULL CHECK (group_jid <> ''),
    msg_id     text        NOT NULL CHECK (msg_id <> ''),
    entry_id   bigint      NOT NULL REFERENCES app.corpus_entries (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_jid, msg_id, entry_id)
);

-- Fingerprint allow-list of overturned messages (DESIGN.md §7.5 "Overturns"): only the
-- near-duplicate fingerprint is kept, never the text, so the same false positive isn't repeated.
CREATE TABLE app.corpus_allow (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_jid  text        NOT NULL CHECK (group_jid <> ''),
    simhash    bigint      NOT NULL,
    minhash    bytea       NOT NULL,
    tokens     integer     NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX corpus_allow_group_idx ON app.corpus_allow (group_jid);

-- +goose Down
DROP TABLE IF EXISTS app.corpus_allow;
DROP TABLE IF EXISTS app.corpus_messages;
