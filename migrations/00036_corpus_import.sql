-- +goose Up
-- Importing a labeled dataset (DESIGN.md §7.8, §14). Each `antispam corpus import` run is one
-- batch; its corpus entries, hold-out rows and allow-list fingerprints carry the batch ID, so
-- `antispam corpus remove --import <id>` can undo it. Ham records are never stored as text.
CREATE TABLE app.import_batches (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file_name   text        NOT NULL DEFAULT '' CHECK (length(file_name) <= 500),
    -- Empty when the caller didn't read a file (records passed in directly).
    file_sha256 text        NOT NULL DEFAULT '' CHECK (file_sha256 = '' OR file_sha256 ~ '^[0-9a-f]{64}$'),
    format      text        NOT NULL DEFAULT '' CHECK (format IN ('', 'jsonl', 'csv')),
    scope       text        NOT NULL CHECK (scope IN ('instance', 'group')),
    group_jid   text,
    started_by  text        NOT NULL DEFAULT '',
    started_at  timestamptz NOT NULL,
    finished_at timestamptz,
    removed_at  timestamptz,
    stats       jsonb       NOT NULL DEFAULT '{}',
    CHECK ((scope = 'group') = (group_jid IS NOT NULL))
);

ALTER TABLE app.corpus_entries
    ADD CONSTRAINT corpus_entries_import_batch_fk FOREIGN KEY (import_batch_id) REFERENCES app.import_batches (id);
CREATE INDEX corpus_entries_import_batch_idx ON app.corpus_entries (import_batch_id) WHERE import_batch_id IS NOT NULL;

-- The evaluation corpus (§7.8, §18): spam-only, split off by cluster so near-duplicates of a
-- test record never reach the corpus. Only the redacted, normalized text and media hashes are
-- kept, like corpus entries.
CREATE TABLE app.eval_corpus (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source          text        NOT NULL CHECK (source IN ('import_holdout', 'confirmed_live')),
    import_batch_id bigint REFERENCES app.import_batches (id),
    cluster_key     text        NOT NULL CHECK (cluster_key <> ''),
    text_norm       text        NOT NULL DEFAULT '',
    -- A record without text is known by its media, so a later import can't store it again.
    media_sha256    bytea[]     NOT NULL DEFAULT '{}',
    phashes         bigint[]    NOT NULL DEFAULT '{}',
    label           text        NOT NULL DEFAULT 'spam' CHECK (label = 'spam'),
    category        text        NOT NULL DEFAULT '',
    kind            text        NOT NULL DEFAULT '',
    language        text        NOT NULL DEFAULT '',
    added_at        timestamptz NOT NULL DEFAULT now(),
    CHECK ((source = 'import_holdout') = (import_batch_id IS NOT NULL))
);
CREATE INDEX eval_corpus_import_batch_idx ON app.eval_corpus (import_batch_id) WHERE import_batch_id IS NOT NULL;

-- Allow-list fingerprints an import adds from its ham records (`--allow-ham`).
ALTER TABLE app.corpus_allow
    ADD COLUMN import_batch_id bigint REFERENCES app.import_batches (id);
CREATE INDEX corpus_allow_import_batch_idx ON app.corpus_allow (import_batch_id) WHERE import_batch_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS app.corpus_allow_import_batch_idx;
ALTER TABLE app.corpus_allow DROP COLUMN IF EXISTS import_batch_id;
DROP TABLE IF EXISTS app.eval_corpus;
DROP INDEX IF EXISTS app.corpus_entries_import_batch_idx;
ALTER TABLE app.corpus_entries DROP CONSTRAINT IF EXISTS corpus_entries_import_batch_fk;
DROP TABLE IF EXISTS app.import_batches;
