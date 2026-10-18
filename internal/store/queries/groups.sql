-- Groups the bots are in (DESIGN.md §14).

-- name: GetGroup :one
SELECT g.jid, coalesce(b.jid, '')::text AS bot_jid, g.name, g.topic, g.size, g.addressing_mode,
       g.bot_is_admin, g.announce, g.locked, g.join_approval, g.joined_at, g.status, g.last_info_refresh,
       g.kind, g.community_jid, g.community_name
FROM app.groups g LEFT JOIN app.bots b ON b.id = g.bot_id
WHERE g.jid = @jid;

-- name: UpsertGroup :exec
INSERT INTO app.groups (jid, bot_id, name, topic, size, addressing_mode, bot_is_admin, announce, locked,
                        join_approval, joined_at, status, last_info_refresh, kind, community_jid, community_name)
VALUES (@jid, (SELECT id FROM app.bots WHERE app.bots.jid = @bot_jid), @name, @topic, @size, @addressing_mode,
        @bot_is_admin, @announce, @locked, @join_approval, sqlc.narg('joined_at'), @status,
        sqlc.narg('last_info_refresh'), @kind, @community_jid, @community_name)
ON CONFLICT (jid) DO UPDATE SET
    bot_id = coalesce(EXCLUDED.bot_id, app.groups.bot_id), name = EXCLUDED.name, topic = EXCLUDED.topic,
    size = EXCLUDED.size, addressing_mode = EXCLUDED.addressing_mode, bot_is_admin = EXCLUDED.bot_is_admin,
    announce = EXCLUDED.announce, locked = EXCLUDED.locked, join_approval = EXCLUDED.join_approval,
    joined_at = EXCLUDED.joined_at, status = EXCLUDED.status, last_info_refresh = EXCLUDED.last_info_refresh,
    kind = EXCLUDED.kind, community_jid = EXCLUDED.community_jid, community_name = EXCLUDED.community_name;

-- name: ListGroups :many
-- With an empty statuses array, every group.
SELECT g.jid, coalesce(b.jid, '')::text AS bot_jid, g.name, g.topic, g.size, g.addressing_mode,
       g.bot_is_admin, g.announce, g.locked, g.join_approval, g.joined_at, g.status, g.last_info_refresh,
       g.kind, g.community_jid, g.community_name
FROM app.groups g LEFT JOIN app.bots b ON b.id = g.bot_id
WHERE cardinality(@statuses::text[]) = 0 OR g.status = ANY(@statuses::text[])
ORDER BY g.jid;

-- name: ListStaleGroups :many
SELECT g.jid, coalesce(b.jid, '')::text AS bot_jid, g.name, g.topic, g.size, g.addressing_mode,
       g.bot_is_admin, g.announce, g.locked, g.join_approval, g.joined_at, g.status, g.last_info_refresh,
       g.kind, g.community_jid, g.community_name
FROM app.groups g LEFT JOIN app.bots b ON b.id = g.bot_id
WHERE g.status <> 'left' AND (g.last_info_refresh IS NULL OR g.last_info_refresh <= @refreshed_by)
ORDER BY g.last_info_refresh NULLS FIRST, g.jid;

-- name: ListCommunityGroups :many
-- A community's own list of its groups (DESIGN.md §12.4), including those the bot isn't in.
SELECT group_jid, name, announcement FROM app.community_groups
WHERE community_jid = @community_jid
ORDER BY group_jid;

-- name: ReplaceCommunityGroups :exec
-- Stores a community's full list: listed groups are added or updated, the rest are removed.
WITH gone AS (
    DELETE FROM app.community_groups
    WHERE community_jid = @community_jid AND NOT (group_jid = ANY(@group_jids::text[]))
)
INSERT INTO app.community_groups (community_jid, group_jid, name, announcement, refreshed_at)
SELECT @community_jid, unnest(@group_jids::text[]), unnest(@names::text[]), unnest(@announcements::boolean[]), now()
ON CONFLICT (community_jid, group_jid) DO UPDATE SET
    name = EXCLUDED.name, announcement = EXCLUDED.announcement, refreshed_at = EXCLUDED.refreshed_at;

-- name: LinkCommunityGroup :exec
INSERT INTO app.community_groups (community_jid, group_jid, name, announcement, refreshed_at)
VALUES (@community_jid, @group_jid, @name, @announcement, now())
ON CONFLICT (community_jid, group_jid) DO UPDATE SET
    name = CASE WHEN EXCLUDED.name = '' THEN app.community_groups.name ELSE EXCLUDED.name END,
    announcement = EXCLUDED.announcement, refreshed_at = EXCLUDED.refreshed_at;

-- name: UnlinkCommunityGroup :exec
DELETE FROM app.community_groups WHERE community_jid = @community_jid AND group_jid = @group_jid;
