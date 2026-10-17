-- name: InsertAuditLog :one
INSERT INTO app.audit_log (actor, group_jid, action, diff)
VALUES (@actor, sqlc.narg('group_jid'), @action, @diff)
RETURNING id, at;

-- name: ListAuditLogByGroup :many
-- Newest first. before_id pages backwards; pass 0 for the first page.
SELECT id, actor, group_jid, action, diff, at
FROM app.audit_log
WHERE group_jid = @group_jid::text
  AND (@before_id::bigint = 0 OR id < @before_id::bigint)
ORDER BY id DESC
LIMIT @row_limit;

-- name: ListAuditLogInstance :many
-- Instance-wide entries (no group), newest first; paged like ListAuditLogByGroup.
SELECT id, actor, group_jid, action, diff, at
FROM app.audit_log
WHERE group_jid IS NULL
  AND (@before_id::bigint = 0 OR id < @before_id::bigint)
ORDER BY id DESC
LIMIT @row_limit;
