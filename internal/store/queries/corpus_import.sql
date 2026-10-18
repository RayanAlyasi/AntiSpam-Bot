-- name: InsertImportBatch :one
INSERT INTO app.import_batches (file_name, file_sha256, format, scope, group_jid, started_by, started_at)
VALUES (@file_name, @file_sha256, @format, @scope, sqlc.narg('group_jid'), @started_by, @started_at)
RETURNING id;

-- name: FinishImportBatch :exec
UPDATE app.import_batches SET finished_at = @finished_at, stats = @stats WHERE id = @id;

-- name: GetImportBatch :one
SELECT id, file_name, file_sha256, format, scope, group_jid, started_by, started_at, finished_at, removed_at, stats
FROM app.import_batches
WHERE id = @id;

-- name: ListCorpusForDedupe :many
-- The live entries an import dedupes against: those in the same scope (the group's own entries
-- for a group import, the global ones for an instance import). Only what matching needs: the
-- normalized text and media hashes.
SELECT id, text_norm, media_sha256, phashes
FROM app.corpus_entries
WHERE status IN ('provisional', 'active', 'promoted') AND expires_at > @now
  AND ((sqlc.narg('group_jid')::text IS NULL AND scope = 'global')
    OR (scope = 'group' AND group_jid = sqlc.narg('group_jid')::text))
ORDER BY id;

-- name: ListEvalForDedupe :many
-- Every hold-out record, so an import never stores a held-out record as a corpus entry.
SELECT text_norm, media_sha256, phashes FROM app.eval_corpus ORDER BY id;

-- name: InsertEvalCorpus :exec
INSERT INTO app.eval_corpus (source, import_batch_id, cluster_key, text_norm, media_sha256, phashes, category, kind,
                             language, added_at)
VALUES ('import_holdout', @import_batch_id, @cluster_key, @text_norm, @media_sha256, @phashes, @category, @kind,
        @language, @added_at);

-- name: InsertImportAllow :exec
INSERT INTO app.corpus_allow (group_jid, simhash, minhash, tokens, import_batch_id)
VALUES (@group_jid, @simhash, @minhash, @tokens, @import_batch_id);

-- name: DeleteImportRules :execrows
-- Undoing an import: the rules learned only from the batch's entries go (which notifies every
-- node to drop them); a rule another live entry also taught stays.
DELETE FROM app.rules r
USING app.learned_rules l
         JOIN app.corpus_entries c ON c.id = l.corpus_entry_id
WHERE l.rule_id = r.id AND c.import_batch_id = @import_batch_id
  AND NOT EXISTS (SELECT 1
                  FROM app.learned_rules o
                           JOIN app.corpus_entries oc ON oc.id = o.corpus_entry_id
                  WHERE o.rule_id = r.id AND oc.import_batch_id IS DISTINCT FROM @import_batch_id
                    AND oc.status IN ('provisional', 'active', 'promoted'));

-- name: DeleteImportEntries :execrows
DELETE FROM app.corpus_entries WHERE import_batch_id = @import_batch_id;

-- name: DeleteImportEval :execrows
DELETE FROM app.eval_corpus WHERE import_batch_id = @import_batch_id;

-- name: DeleteImportAllow :execrows
DELETE FROM app.corpus_allow WHERE import_batch_id = @import_batch_id;

-- name: MarkImportRemoved :execrows
UPDATE app.import_batches SET removed_at = @removed_at WHERE id = @id AND removed_at IS NULL;
