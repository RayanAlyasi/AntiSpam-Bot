-- name: InsertRule :one
INSERT INTO app.rules (scope, scope_id, pack, type, pattern, norm_pattern, action_class, weight, enabled, created_by)
VALUES (@scope, @scope_id, @pack, @type, @pattern, @norm_pattern, @action_class, @weight, @enabled,
        sqlc.narg('created_by'))
RETURNING id, created_at;

-- name: SetRuleEnabled :execrows
UPDATE app.rules SET enabled = @enabled WHERE id = @id;

-- name: DeleteRule :execrows
DELETE FROM app.rules WHERE id = @id;

-- name: ListEnabledRules :many
SELECT id, scope, scope_id, pack, type, pattern, norm_pattern, action_class, weight, enabled
FROM app.rules
WHERE enabled
ORDER BY id;

-- name: AddRuleHits :exec
-- Hit counters don't fire the change trigger, so they don't cause reloads.
UPDATE app.rules SET hits = hits + @n::bigint WHERE id = @id;

-- name: UpsertFingerprint :exec
-- Re-labelling a value (an overturn turns spam into allow) keeps one row per value and scope.
INSERT INTO app.fingerprints (kind, value, label, source, scope, scope_id, first_seen, last_seen)
VALUES (@kind, @value, @label, @source, @scope, @scope_id, @now, @now)
ON CONFLICT (kind, value, scope, scope_id) DO UPDATE
    SET label = excluded.label, source = excluded.source, last_seen = excluded.last_seen;

-- name: DeleteFingerprint :execrows
DELETE FROM app.fingerprints
WHERE kind = @kind AND value = @value AND scope = @scope AND scope_id = @scope_id;

-- name: ListFingerprints :many
SELECT kind, value, label, source, scope, scope_id
FROM app.fingerprints
ORDER BY kind, value, scope, scope_id;

-- name: TouchFingerprint :exec
UPDATE app.fingerprints SET hits = hits + 1, last_seen = @now
WHERE kind = @kind AND value = @value AND scope = @scope AND scope_id = @scope_id;
