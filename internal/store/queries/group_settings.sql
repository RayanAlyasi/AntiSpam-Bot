-- name: GetGroupSettings :one
SELECT group_jid, version, doc, updated_by, updated_at
FROM app.group_settings
WHERE group_jid = @group_jid;

-- name: InsertGroupSettings :one
-- The first version of a group's document. Fails on a concurrent first save (primary key).
INSERT INTO app.group_settings (group_jid, version, doc, updated_by)
VALUES (@group_jid, 1, @doc, @updated_by)
RETURNING group_jid, version, doc, updated_by, updated_at;

-- name: UpdateGroupSettings :one
-- Optimistic concurrency: only replaces the document if it is still at prev_version.
UPDATE app.group_settings
SET version = version + 1, doc = @doc, updated_by = @updated_by, updated_at = now()
WHERE group_jid = @group_jid AND version = @prev_version
RETURNING group_jid, version, doc, updated_by, updated_at;

-- name: InsertGroupSettingsHistory :exec
INSERT INTO app.group_settings_history (group_jid, version, doc, updated_by, updated_at)
VALUES (@group_jid, @version, @doc, @updated_by, @updated_at);

-- name: ListGroupSettingsHistory :many
SELECT group_jid, version, doc, updated_by, updated_at
FROM app.group_settings_history
WHERE group_jid = @group_jid
ORDER BY version DESC
LIMIT @row_limit;

-- name: NotifyGroupSettings :exec
SELECT pg_notify('group_settings', @group_jid::text);
