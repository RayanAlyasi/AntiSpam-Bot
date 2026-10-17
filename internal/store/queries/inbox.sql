-- The inbox of received messages and the text-free message index (DESIGN.md §5.1, §14, §17).

-- name: InsertInbox :execrows
-- A duplicate (same chat, sender, message ID, edit attribute and kind) inserts nothing and returns 0.
INSERT INTO app.inbox (bot_id, chat, msg_id, edit_seq, kind, sender, raw, info, received_at)
VALUES ((SELECT id FROM app.bots WHERE jid = @bot_jid), @chat, @msg_id, @edit_seq, @kind, @sender,
        @raw, @info, @received_at)
ON CONFLICT (chat, sender, msg_id, edit_seq, kind) DO NOTHING;

-- name: ClaimInbox :many
-- Claims up to row_limit unprocessed rows that no live worker holds, oldest first.
UPDATE app.inbox i
SET claimed_until = now() + make_interval(secs => @claim_seconds::float8), attempts = i.attempts + 1
WHERE i.id IN (
    SELECT p.id FROM app.inbox p
    WHERE p.processed_at IS NULL AND (p.claimed_until IS NULL OR p.claimed_until < now())
    ORDER BY p.id
    LIMIT @row_limit
    FOR UPDATE SKIP LOCKED
)
RETURNING i.id, (SELECT jid FROM app.bots WHERE app.bots.id = i.bot_id)::text AS bot_jid, i.chat, i.msg_id,
          i.edit_seq, i.kind, i.sender, i.raw, i.info, i.received_at, i.attempts;

-- name: MarkInboxProcessed :exec
UPDATE app.inbox SET processed_at = @processed_at, claimed_until = NULL, last_error = sqlc.narg('last_error')
WHERE id = @id;

-- name: UnclaimInbox :exec
-- Hands a row back at once without counting the attempt (the worker is shutting down).
UPDATE app.inbox SET claimed_until = NULL, attempts = greatest(attempts - 1, 0) WHERE id = @id;

-- name: ReleaseInboxClaim :exec
-- Gives a failed row back for a later retry, after retry_seconds.
UPDATE app.inbox
SET claimed_until = now() + make_interval(secs => @retry_seconds::float8), last_error = @last_error
WHERE id = @id;

-- name: DeleteInboxBefore :execrows
DELETE FROM app.inbox WHERE received_at < @before;

-- name: CountPendingInbox :one
SELECT count(*) FROM app.inbox WHERE processed_at IS NULL;

-- name: UpsertMessageIndex :exec
INSERT INTO app.message_index (group_jid, msg_id, sender_person, ts, fingerprint, simhash, url_domains,
                               media_sha256, verdict, actioned, expires_at)
VALUES (@group_jid, @msg_id, sqlc.narg('sender_person'), @ts, @fingerprint, sqlc.narg('simhash'),
        @url_domains, @media_sha256, @verdict, @actioned, @expires_at)
ON CONFLICT (group_jid, msg_id) DO UPDATE SET
    -- A partial write (the inbox's base row, a retry) never erases what a fuller one recorded.
    sender_person = coalesce(EXCLUDED.sender_person, app.message_index.sender_person),
    fingerprint = coalesce(EXCLUDED.fingerprint, app.message_index.fingerprint),
    simhash = coalesce(EXCLUDED.simhash, app.message_index.simhash),
    url_domains = CASE WHEN cardinality(EXCLUDED.url_domains) > 0 THEN EXCLUDED.url_domains ELSE app.message_index.url_domains END,
    media_sha256 = CASE WHEN cardinality(EXCLUDED.media_sha256) > 0 THEN EXCLUDED.media_sha256 ELSE app.message_index.media_sha256 END,
    -- An edit or a late detection can raise a row's verdict (clean < policy < spam), never lower it.
    verdict = CASE WHEN array_position(ARRAY['clean', 'policy', 'spam'], EXCLUDED.verdict)
                        > array_position(ARRAY['clean', 'policy', 'spam'], app.message_index.verdict)
                   THEN EXCLUDED.verdict ELSE app.message_index.verdict END,
    actioned = app.message_index.actioned OR EXCLUDED.actioned,
    expires_at = greatest(app.message_index.expires_at, EXCLUDED.expires_at);

-- name: GetMessageIndex :one
SELECT * FROM app.message_index WHERE group_jid = @group_jid AND msg_id = @msg_id;

-- name: DeleteExpiredMessageIndex :execrows
DELETE FROM app.message_index WHERE expires_at <= @now;
