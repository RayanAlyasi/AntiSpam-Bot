-- name: LockMessage :exec
-- Serializes the fast lane on one message until the transaction ends, so two workers that got
-- the same redelivered event can't both act on it (DESIGN.md §4.1).
SELECT pg_advisory_xact_lock(hashtextextended(@message_key::text, 0));

-- name: DetectionExists :one
SELECT EXISTS (
    SELECT 1 FROM app.detections WHERE group_jid = @group_jid AND msg_id = @msg_id
) AS found;

-- name: ListDetectionsSince :many
-- Detections in one mode since a moment, oldest first, optionally for one group: the shadow-mode
-- review (`antispam replay -shadow`, DESIGN.md §8.4).
SELECT id, group_jid, msg_id, person_id, score, tier_reached, signals, verdict, confidence_source,
    route, rule_version, action_plan, mode, network_ban_id, evidence_ref, created_at
FROM app.detections
WHERE mode = @mode
  AND created_at >= @since
  AND (@group_jid::text = '' OR group_jid = @group_jid::text)
ORDER BY id
LIMIT @row_limit;

-- name: CountOpenActions :one
-- Actions of a bot that may still run before a moment: pending ones due by then, and running
-- ones. Tests use it to know when the queue has drained.
SELECT count(*) FROM app.actions
WHERE bot = @bot AND (status = 'running' OR (status = 'pending' AND not_before < @due_before));

-- name: PutCorpusMessage :exec
-- Records a corpus entry confirmed from a message. Every confirmation's entry is kept.
INSERT INTO app.corpus_messages (group_jid, msg_id, entry_id)
VALUES (@group_jid, @msg_id, @entry_id)
ON CONFLICT DO NOTHING;

-- name: GetCorpusMessage :one
-- The newest entry from a message that hasn't been retracted yet: an overturn retracts them one by
-- one until none is left.
SELECT m.entry_id
FROM app.corpus_messages m
JOIN app.corpus_entries e ON e.id = m.entry_id
WHERE m.group_jid = @group_jid AND m.msg_id = @msg_id AND e.status <> 'retracted'
ORDER BY m.entry_id DESC
LIMIT 1;

-- name: ListMatchableCorpusTexts :many
-- The near-duplicate index's input in one query: live active or promoted entries with their
-- stored (redacted) normalized text. Provisional entries are never hard evidence (§7.5).
SELECT id, scope, group_jid, status, text_norm
FROM app.corpus_entries
WHERE status IN ('active', 'promoted') AND expires_at > @now AND text_norm <> ''
ORDER BY id;

-- name: InsertCorpusAllow :exec
INSERT INTO app.corpus_allow (group_jid, simhash, minhash, tokens)
VALUES (@group_jid, @simhash, @minhash, @tokens);

-- name: ListCorpusAllow :many
SELECT group_jid, simhash, minhash, tokens FROM app.corpus_allow ORDER BY id;

-- name: ListInboxForReplay :many
-- Stored group messages for `antispam replay -inbox` (F-089), oldest first, optionally for one
-- chat. Read only.
SELECT i.id, b.jid AS bot_jid, i.chat, i.msg_id, i.kind, i.raw, i.info
FROM app.inbox i
JOIN app.bots b ON b.id = i.bot_id
WHERE i.received_at >= @since
  AND i.chat LIKE '%@g.us'
  AND (@chat::text = '' OR i.chat = @chat::text)
ORDER BY i.id
LIMIT @row_limit;
