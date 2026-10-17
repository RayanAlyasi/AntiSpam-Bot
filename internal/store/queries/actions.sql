-- name: InsertAction :one
-- A duplicate dedupe key inserts nothing and returns no row.
INSERT INTO app.actions (bot, chat, kind, priority, payload, not_before, deadline, message_time,
                         dedupe_key, reason_detection_id, created_at, updated_at)
VALUES (@bot, @chat, @kind, @priority, @payload, @not_before, @deadline, sqlc.narg('message_time'),
        @dedupe_key, sqlc.narg('reason_detection_id'), @created_at, @created_at)
ON CONFLICT (dedupe_key) DO NOTHING
RETURNING id;

-- name: ActionIDByDedupeKey :one
SELECT id FROM app.actions WHERE dedupe_key = @dedupe_key;

-- name: GetAction :one
SELECT * FROM app.actions WHERE id = @id;

-- name: CancelAction :execrows
UPDATE app.actions SET status = 'cancelled', last_error = @reason, updated_at = @now
WHERE dedupe_key = @dedupe_key AND status = 'pending';

-- name: RecoverActions :execrows
UPDATE app.actions SET status = 'pending', updated_at = @now
WHERE bot = @bot AND status = 'running';

-- name: CandidateActions :many
-- The executor's window (actions.Store.Candidates): per priority and chat the n first in queue
-- order and the n newest messages, and per priority the n earliest deadlines.
SELECT * FROM app.actions
WHERE id IN (
    SELECT ranked.id FROM (
        SELECT p.id,
               row_number() OVER (PARTITION BY p.priority, p.chat ORDER BY p.not_before, p.id) AS by_queue,
               row_number() OVER (PARTITION BY p.priority, p.chat ORDER BY p.message_time DESC NULLS LAST, p.id) AS by_newest,
               row_number() OVER (PARTITION BY p.priority ORDER BY p.deadline, p.id) AS by_deadline
        FROM app.actions AS p
        WHERE p.bot = @bot AND p.status = 'pending'
    ) AS ranked
    WHERE ranked.by_queue <= @n::bigint OR ranked.by_newest <= @n::bigint OR ranked.by_deadline <= @n::bigint
)
ORDER BY priority, not_before, id;

-- name: PendingActionsInChat :many
SELECT * FROM app.actions
WHERE bot = @bot AND chat = @chat AND kind = @kind AND status = 'pending'
ORDER BY id;

-- name: ClaimAction :one
-- SKIP LOCKED: a row another transaction is changing (a cancel) is skipped, not waited for.
UPDATE app.actions AS a SET status = 'running', attempts = a.attempts + 1, updated_at = @now, claimed_by = @owner
WHERE a.id = (SELECT c.id FROM app.actions AS c WHERE c.id = @id AND c.status = 'pending' FOR UPDATE SKIP LOCKED)
RETURNING a.*;

-- name: FinishAction :execrows
UPDATE app.actions
SET status = @status, last_error = @last_error, result = @result, updated_at = @now,
    called = called OR @called::boolean
WHERE id = @id AND status = 'running' AND claimed_by = @owner;

-- name: RetryAction :execrows
-- Without attempted, the claim's attempt is given back: no call was tried for this action.
UPDATE app.actions
SET status = 'pending', not_before = @not_before, last_error = @last_error, result = @result, updated_at = @now,
    called = called OR @called::boolean,
    attempts = CASE WHEN @attempted::boolean THEN attempts ELSE greatest(attempts - 1, 0) END
WHERE id = @id AND status = 'running' AND claimed_by = @owner;

-- name: ExpireDueActions :many
UPDATE app.actions SET status = 'expired', last_error = 'deadline passed', updated_at = @now
WHERE bot = @bot AND status = 'pending' AND deadline <= @now
RETURNING *;

-- name: FailPendingActions :execrows
UPDATE app.actions SET status = 'failed', last_error = @reason, updated_at = @now
WHERE bot = @bot AND chat = @chat AND status = 'pending' AND kind = ANY(@kinds::text[]);

-- name: CountWritesSince :one
SELECT count(*) FROM app.actions WHERE bot = @bot AND called AND updated_at >= @since;

-- name: GetActionControl :one
SELECT state FROM app.action_controls WHERE scope = @scope;

-- name: LockActionControl :one
-- Creates the row if needed and locks it for a read-modify-write in the same transaction.
INSERT INTO app.action_controls (scope, state, updated_at) VALUES (@scope, '{}'::jsonb, @now)
ON CONFLICT (scope) DO UPDATE SET scope = EXCLUDED.scope
RETURNING state;

-- name: SetActionControl :exec
UPDATE app.action_controls SET state = @state, updated_at = @now WHERE scope = @scope;

-- name: GroupPosting :one
-- Whether the bot may post in a group (actions.Groups): a group with a settings document is
-- protected; a missing "silent" key counts as silent.
SELECT COALESCE((doc->>'silent')::boolean, true)::boolean AS silent
FROM app.group_settings WHERE group_jid = @group_jid;

-- name: IsReviewGroup :one
-- A group named as some protected group's review group isn't protected itself (DESIGN.md §9.7).
SELECT EXISTS (
    SELECT 1 FROM app.group_settings WHERE doc->'review'->>'review_group' = @chat::text
)::boolean AS review_group;
