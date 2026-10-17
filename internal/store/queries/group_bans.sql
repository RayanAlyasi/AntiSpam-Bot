-- name: InsertGroupBan :one
-- Records an active ban. A person with an active ban in the group already gets no new row
-- (the partial unique index), and the caller reads the existing one.
INSERT INTO app.group_bans (group_jid, person_id, reason, decided_by, decider_person, detection_id,
    review_id, network_ban_id, rejoin_block, created_at)
VALUES (@group_jid, @person_id, @reason, @decided_by, sqlc.narg('decider_person'),
    sqlc.narg('detection_id'), sqlc.narg('review_id'), sqlc.narg('network_ban_id'), @rejoin_block, COALESCE(sqlc.narg('created_at'), now()))
ON CONFLICT (group_jid, person_id) WHERE active DO NOTHING
RETURNING id, group_jid, person_id, reason, decided_by, decider_person, detection_id, review_id,
    network_ban_id, rejoin_block, active, created_at, lifted_by, lifted_at;

-- name: GetActiveGroupBan :one
SELECT id, group_jid, person_id, reason, decided_by, decider_person, detection_id, review_id,
    network_ban_id, rejoin_block, active, created_at, lifted_by, lifted_at
FROM app.group_bans
WHERE group_jid = @group_jid AND person_id = @person_id AND active;

-- name: LiftGroupBan :execrows
UPDATE app.group_bans
SET active = false, lifted_by = @lifted_by, lifted_at = @lifted_at
WHERE group_jid = @group_jid AND person_id = @person_id AND active;

-- name: ListGroupBanTimesSince :many
-- Ban times in a group since a moment, oldest first, to seed the hourly ban cap after a restart.
SELECT created_at
FROM app.group_bans
WHERE group_jid = @group_jid AND created_at > @since
ORDER BY created_at;
