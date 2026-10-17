-- name: InsertReview :one
INSERT INTO app.reviews (
    short_code, group_jid, group_name, kind, detection_id, subject, message_sender, message_id,
    evidence, evidence_raw, thumbnail, thumbnail_mime, proposed, mode, status, deadline, on_timeout, created_at
) VALUES (
    @short_code, @group_jid, @group_name, @kind, sqlc.narg('detection_id'), @subject,
    sqlc.narg('message_sender'), sqlc.narg('message_id'), @evidence, sqlc.narg('evidence_raw'), sqlc.narg('thumbnail'),
    sqlc.narg('thumbnail_mime'), @proposed, @mode, @status, @deadline, @on_timeout, @created_at
)
RETURNING id;

-- name: GetReview :one
SELECT * FROM app.reviews WHERE id = @id;

-- name: GetReviewByCode :one
SELECT * FROM app.reviews WHERE short_code = @short_code;

-- name: DecideReview :one
-- First decision wins: only a pending review changes (DESIGN.md §9.5); the row lock makes a
-- concurrent loser re-check status and get no row. The unredacted text is returned once, for
-- the corpus, and cleared in the same statement. A not-spam decision also clears the redacted
-- text and thumbnail, so no text of an overturned message remains (§7.5).
WITH prev AS (
    SELECT p.id, p.evidence_raw FROM app.reviews p WHERE p.id = @id AND p.status = 'pending' FOR UPDATE
)
UPDATE app.reviews r
SET status          = @status,
    decision        = @decision,
    decided_by      = sqlc.narg('decided_by'),
    decided_by_name = sqlc.narg('decided_by_name'),
    method          = @method,
    decided_at      = @decided_at,
    evidence_raw    = NULL,
    evidence        = CASE WHEN @decision::text = 'not_spam' THEN r.evidence - 'text' ELSE r.evidence END,
    thumbnail       = CASE WHEN @decision::text = 'not_spam' THEN NULL ELSE r.thumbnail END,
    thumbnail_mime  = CASE WHEN @decision::text = 'not_spam' THEN NULL ELSE r.thumbnail_mime END
FROM prev
WHERE r.id = prev.id
RETURNING r.id, prev.evidence_raw AS raw_text;

-- name: ClearReviewEvidenceText :execrows
-- An overturn removes the message's text wherever it is kept, reviews included (§7.5).
UPDATE app.reviews
SET evidence = evidence - 'text', evidence_raw = NULL, thumbnail = NULL, thumbnail_mime = NULL
WHERE group_jid = @group_jid AND message_id = @msg_id;

-- name: ClearStaleReviewRawText :execrows
-- The unredacted text waits for the decision at most 72 h, like the raw inbox (§17); a longer
-- timeout or a stalled expire job never keeps it longer.
UPDATE app.reviews SET evidence_raw = NULL
WHERE created_at <= @cutoff AND evidence_raw IS NOT NULL;

-- name: ExpireReviewEvidence :execrows
-- Review evidence follows the 30-day evidence retention (§14, §17.1).
UPDATE app.reviews
SET evidence = evidence - 'text', evidence_raw = NULL, thumbnail = NULL, thumbnail_mime = NULL
WHERE created_at <= @cutoff AND (evidence ? 'text' OR evidence_raw IS NOT NULL OR thumbnail IS NOT NULL);

-- name: InsertReviewVote :exec
INSERT INTO app.review_votes (review_id, voter, method, raw, decision, valid, invalid_reason, at)
VALUES (@review_id, @voter, @method, @raw, @decision, @valid, @invalid_reason, @at);

-- name: ListReviewVotes :many
SELECT * FROM app.review_votes WHERE review_id = @review_id ORDER BY id;

-- name: InsertReviewMessage :exec
INSERT INTO app.review_messages (chat_jid, msg_id, review_id, sent_at)
VALUES (@chat_jid, @msg_id, @review_id, @sent_at)
ON CONFLICT (chat_jid, msg_id) DO NOTHING;

-- name: ListReviewMessages :many
SELECT * FROM app.review_messages WHERE review_id = @review_id ORDER BY sent_at, msg_id;

-- name: GetReviewByMessage :one
SELECT r.* FROM app.reviews r
JOIN app.review_messages m ON m.review_id = r.id
WHERE m.chat_jid = @chat_jid AND m.msg_id = @msg_id;

-- name: MarkReviewOutcomeEdited :execrows
UPDATE app.review_messages SET outcome_edited = true WHERE chat_jid = @chat_jid AND msg_id = @msg_id;

-- name: ListPendingReviews :many
-- Oldest first; an empty group lists every group.
SELECT * FROM app.reviews
WHERE status = 'pending' AND (@group_jid::text = '' OR group_jid = @group_jid::text)
ORDER BY created_at, id;

-- name: ListDueReviews :many
SELECT * FROM app.reviews
WHERE status = 'pending' AND deadline <= @now
ORDER BY deadline, id;

-- name: MarkDMInbound :exec
INSERT INTO app.dm_threads (person, last_inbound_at) VALUES (@person, @at)
ON CONFLICT (person) DO UPDATE
SET last_inbound_at = GREATEST(app.dm_threads.last_inbound_at, EXCLUDED.last_inbound_at);

-- name: MarkDMException :exec
INSERT INTO app.dm_threads (person, last_exception_at) VALUES (@person, @at)
ON CONFLICT (person) DO UPDATE
SET last_exception_at = GREATEST(app.dm_threads.last_exception_at, EXCLUDED.last_exception_at);

-- name: GetDMThread :one
SELECT * FROM app.dm_threads WHERE person = @person;

-- name: UpsertEvidenceSnapshot :exec
INSERT INTO app.evidence_snapshots (group_jid, msg_id, sender, text, thumbnail, thumbnail_mime, detection_id, reason, at, expires_at)
VALUES (@group_jid, @msg_id, @sender, @text, sqlc.narg('thumbnail'), sqlc.narg('thumbnail_mime'), sqlc.narg('detection_id'), @reason, @at, sqlc.narg('expires_at'))
ON CONFLICT (group_jid, msg_id) DO UPDATE
SET sender = EXCLUDED.sender, text = EXCLUDED.text, thumbnail = EXCLUDED.thumbnail,
    thumbnail_mime = EXCLUDED.thumbnail_mime, detection_id = EXCLUDED.detection_id,
    reason = EXCLUDED.reason, at = EXCLUDED.at, expires_at = EXCLUDED.expires_at;

-- name: GetEvidenceSnapshot :one
SELECT * FROM app.evidence_snapshots WHERE group_jid = @group_jid AND msg_id = @msg_id;

-- name: ClearEvidenceText :execrows
UPDATE app.evidence_snapshots SET text = '', thumbnail = NULL, thumbnail_mime = NULL
WHERE group_jid = @group_jid AND msg_id = @msg_id;

-- name: PurgeEvidence :execrows
DELETE FROM app.evidence_snapshots WHERE expires_at IS NOT NULL AND expires_at <= @now;

-- name: MarkBlacklistBan :exec
INSERT INTO app.blacklist_bans (group_jid, person_id, ban_id) VALUES (@group_jid, @person_id, @ban_id)
ON CONFLICT (group_jid, person_id) DO UPDATE SET ban_id = EXCLUDED.ban_id, at = now();

-- name: GetBlacklistBan :one
SELECT ban_id FROM app.blacklist_bans WHERE group_jid = @group_jid AND person_id = @person_id;

-- name: ForgetBlacklistBan :exec
DELETE FROM app.blacklist_bans WHERE group_jid = @group_jid AND person_id = @person_id;
