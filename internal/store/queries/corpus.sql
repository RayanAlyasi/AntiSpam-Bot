-- name: InsertCorpusEntry :one
INSERT INTO app.corpus_entries (kind, scope, group_jid, status, source, import_batch_id, source_review_id,
                                source_action_id, decided_by_person, text_raw_redacted, text_norm, simhash,
                                minhash, keywords, urls, domains, number_hashes, channel_jids, invite_codes,
                                media_sha256, phashes, profile, biz, confidence, created_at, expires_at)
VALUES (@kind, @scope, sqlc.narg('group_jid'), @status, @source, sqlc.narg('import_batch_id'),
        sqlc.narg('source_review_id'), sqlc.narg('source_action_id'), sqlc.narg('decided_by_person'),
        @text_raw_redacted, @text_norm, sqlc.narg('simhash'), sqlc.narg('minhash'), @keywords, @urls, @domains,
        @number_hashes, @channel_jids, @invite_codes, @media_sha256, @phashes, sqlc.narg('profile'),
        sqlc.narg('biz'), @confidence, @created_at, @expires_at)
RETURNING id;

-- name: GetCorpusEntry :one
SELECT id, kind, label, scope, group_jid, status, source, import_batch_id, source_review_id, source_action_id,
       decided_by_person, text_raw_redacted, text_norm, simhash, minhash, keywords, urls, domains, number_hashes,
       channel_jids, invite_codes, media_sha256, phashes, profile, biz, confidence, hits, last_hit_at,
       created_at, expires_at
FROM app.corpus_entries
WHERE id = @id;

-- name: ListLiveCorpusFingerprints :many
-- What the near-duplicate indexes load: live entries' fingerprints, no text.
SELECT id, scope, group_jid, status, simhash, minhash
FROM app.corpus_entries
WHERE status IN ('provisional', 'active', 'promoted') AND expires_at > @now
  AND (simhash IS NOT NULL OR minhash IS NOT NULL)
ORDER BY id;

-- name: SetCorpusStatus :execrows
UPDATE app.corpus_entries SET status = @status WHERE id = @id;

-- name: RecordCorpusHit :exec
UPDATE app.corpus_entries SET hits = hits + 1, last_hit_at = @now WHERE id = @id;

-- name: ConfirmPattern :execrows
-- One confirmation per pattern per group; a second confirmation in the same group changes nothing.
INSERT INTO app.corpus_confirmations (pattern_key, group_jid, admin_person_id, corpus_entry_id, at)
VALUES (@pattern_key, @group_jid, sqlc.narg('admin_person_id'), @corpus_entry_id, @at)
ON CONFLICT (pattern_key, group_jid) DO NOTHING;

-- name: ListPatternConfirmations :many
SELECT pattern_key, group_jid, admin_person_id, corpus_entry_id, at
FROM app.corpus_confirmations
WHERE pattern_key = @pattern_key AND at >= @since
ORDER BY at, group_jid;

-- name: LinkLearnedRule :exec
INSERT INTO app.learned_rules (rule_id, corpus_entry_id, group_jid, created_at)
VALUES (@rule_id, @corpus_entry_id, @group_jid, @created_at)
ON CONFLICT DO NOTHING;

-- name: ListLearnedRules :many
SELECT rule_id FROM app.learned_rules WHERE corpus_entry_id = @corpus_entry_id ORDER BY rule_id;

-- name: RetractCorpusEntry :execrows
-- A "not spam" overturn (§7.5): the entry's text goes and the rules learned only from it are
-- deleted, which notifies every node to drop them from memory. Confirmations stay for the
-- review record.
WITH gone AS (
    DELETE FROM app.rules r
    USING app.learned_rules l
    WHERE l.rule_id = r.id AND l.corpus_entry_id = @id
      -- A rule another live entry also taught stays.
      AND NOT EXISTS (SELECT 1
                      FROM app.learned_rules o
                               JOIN app.corpus_entries c ON c.id = o.corpus_entry_id
                      WHERE o.rule_id = r.id AND o.corpus_entry_id <> @id
                        AND c.status IN ('provisional', 'active', 'promoted'))
)
UPDATE app.corpus_entries
SET status = 'retracted', text_raw_redacted = '', text_norm = '', keywords = '{}'
WHERE app.corpus_entries.id = @id;
