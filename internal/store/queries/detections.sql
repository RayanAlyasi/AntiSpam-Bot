-- name: InsertDetection :one
-- A message version already recorded in this mode gets no new row (ON CONFLICT DO NOTHING
-- returns none), and the caller reads the existing one.
INSERT INTO app.detections (group_jid, msg_id, edit_seq, person_id, score, tier_reached, signals, verdict,
    confidence_source, route, rule_version, action_plan, mode, network_ban_id, evidence_ref)
VALUES (@group_jid, @msg_id, @edit_seq, sqlc.narg('person_id'), @score, @tier_reached, @signals, @verdict,
    sqlc.narg('confidence_source'), @route, @rule_version, @action_plan, @mode,
    sqlc.narg('network_ban_id'), sqlc.narg('evidence_ref'))
ON CONFLICT (group_jid, msg_id, edit_seq, mode) DO NOTHING
RETURNING id, created_at;

-- name: GetDetectionByMessage :one
SELECT id, group_jid, msg_id, person_id, score, tier_reached, signals, verdict, confidence_source,
    route, rule_version, action_plan, mode, network_ban_id, evidence_ref, created_at, edit_seq
FROM app.detections
WHERE group_jid = @group_jid AND msg_id = @msg_id AND edit_seq = @edit_seq AND mode = @mode;

-- name: ListDetectionsByGroup :many
-- Newest first. before_id pages backwards; pass 0 for the first page.
SELECT id, group_jid, msg_id, person_id, score, tier_reached, signals, verdict, confidence_source,
    route, rule_version, action_plan, mode, network_ban_id, evidence_ref, created_at, edit_seq
FROM app.detections
WHERE group_jid = @group_jid
  AND (@before_id::bigint = 0 OR id < @before_id::bigint)
ORDER BY id DESC
LIMIT @row_limit;
