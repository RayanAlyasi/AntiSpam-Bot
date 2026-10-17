-- +goose Up
-- Which message a confirmed-spam corpus entry came from (DESIGN.md §7.5), so a later "not spam"
-- on that message retracts exactly the entries it produced. corpus_entries keeps no message ID.
CREATE TABLE app.corpus_sources (
    group_jid       text        NOT NULL,
    msg_id          text        NOT NULL,
    corpus_entry_id bigint      NOT NULL REFERENCES app.corpus_entries (id) ON DELETE CASCADE,
    at              timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_jid, msg_id, corpus_entry_id)
);

CREATE INDEX corpus_sources_entry_idx ON app.corpus_sources (corpus_entry_id);

-- +goose Down
DROP TABLE IF EXISTS app.corpus_sources;
