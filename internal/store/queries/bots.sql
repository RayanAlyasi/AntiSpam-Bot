-- Bot numbers and their connector lease (DESIGN.md §14, §15).

-- name: UpsertBot :exec
-- Records a paired device; an existing row keeps its status and lease.
INSERT INTO app.bots (jid, lid, display_name, status)
VALUES (@jid, sqlc.narg('lid'), @display_name, @status)
ON CONFLICT (jid) DO UPDATE SET
    lid = coalesce(EXCLUDED.lid, app.bots.lid),
    display_name = CASE WHEN EXCLUDED.display_name <> '' THEN EXCLUDED.display_name ELSE app.bots.display_name END;

-- name: SetBotStatus :execrows
UPDATE app.bots SET status = @status WHERE jid = @jid;

-- name: GetBot :one
SELECT id, jid, lid, display_name, status, lease_owner, lease_until FROM app.bots WHERE jid = @jid;

-- name: ListBots :many
SELECT id, jid, lid, display_name, status, lease_owner, lease_until FROM app.bots ORDER BY id;

-- name: AcquireBotLease :execrows
-- Takes the lease when it is free, expired or already ours. The database clock decides, so
-- processes on different hosts agree on expiry.
UPDATE app.bots
SET lease_owner = @owner, lease_until = now() + make_interval(secs => @ttl_seconds::float8)
WHERE jid = @jid
  AND (lease_owner IS NULL OR lease_owner = @owner OR lease_until IS NULL OR lease_until < now());

-- name: RenewBotLease :execrows
UPDATE app.bots
SET lease_until = now() + make_interval(secs => @ttl_seconds::float8)
WHERE jid = @jid AND lease_owner = @owner AND lease_until >= now();

-- name: ReleaseBotLease :exec
UPDATE app.bots SET lease_owner = NULL, lease_until = NULL WHERE jid = @jid AND lease_owner = @owner;
