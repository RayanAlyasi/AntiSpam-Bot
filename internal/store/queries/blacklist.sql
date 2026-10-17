-- name: AddBlacklistEntry :one
-- Adding a number that is already active in the same scope returns the existing entry unchanged
-- (inserted = false), so `.blacklist add` is idempotent.
WITH ins AS (
    INSERT INTO app.number_blacklist (scope, group_jid, pn_e164, pn_hash, reason, added_by_person, added_at)
    VALUES (@scope, sqlc.narg('group_jid'), @pn_e164, @pn_hash, @reason, sqlc.narg('added_by_person'), @now)
    ON CONFLICT (scope, group_jid, pn_e164) WHERE removed_at IS NULL DO NOTHING
    RETURNING id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at
)
SELECT id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at,
       true AS inserted
FROM ins
UNION ALL
SELECT id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at,
       false AS inserted
FROM app.number_blacklist
WHERE scope = @scope AND group_jid IS NOT DISTINCT FROM sqlc.narg('group_jid') AND pn_e164 = @pn_e164
  AND removed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM ins)
LIMIT 1;

-- name: RemoveBlacklistEntry :one
UPDATE app.number_blacklist
SET removed_at = @now::timestamptz, removed_by_person = sqlc.narg('removed_by_person')
WHERE scope = @scope AND group_jid IS NOT DISTINCT FROM sqlc.narg('group_jid') AND pn_e164 = @pn_e164
  AND removed_at IS NULL
RETURNING id;

-- name: ListGroupBlacklist :many
SELECT id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at
FROM app.number_blacklist
WHERE scope = 'group' AND group_jid = @group_jid::text AND removed_at IS NULL
ORDER BY added_at DESC, id DESC;

-- name: ListInstanceBlacklist :many
SELECT id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at
FROM app.number_blacklist
WHERE scope = 'instance' AND removed_at IS NULL
ORDER BY added_at DESC, id DESC;

-- name: FindBlacklistEntries :many
-- Active entries for a number that apply to a group: the group's own and instance-wide ones.
SELECT id, scope, group_jid, pn_e164, pn_hash, person_id, on_whatsapp, reason, added_by_person, added_at
FROM app.number_blacklist
WHERE pn_e164 = @pn_e164 AND removed_at IS NULL
  AND (scope = 'instance' OR group_jid = @group_jid::text)
ORDER BY scope, id;

-- name: ListActiveBlacklistHashes :many
SELECT id, scope, group_jid, pn_hash
FROM app.number_blacklist
WHERE removed_at IS NULL
ORDER BY id;

-- name: SetBlacklistIdentity :exec
-- Links an entry to the person once the number resolves (§9.8 step 1).
UPDATE app.number_blacklist SET person_id = sqlc.narg('person_id'), on_whatsapp = sqlc.narg('on_whatsapp')
WHERE id = @id;
