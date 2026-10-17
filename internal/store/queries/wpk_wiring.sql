-- name: InsertCorpusSource :exec
INSERT INTO app.corpus_sources (group_jid, msg_id, corpus_entry_id) VALUES (@group_jid, @msg_id, @corpus_entry_id)
ON CONFLICT DO NOTHING;

-- name: LiveCorpusEntryForMessage :one
-- The newest entry from a message that hasn't been retracted (an expired one still holds text).
SELECT e.id FROM app.corpus_sources s
JOIN app.corpus_entries e ON e.id = s.corpus_entry_id
WHERE s.group_jid = @group_jid AND s.msg_id = @msg_id
  AND e.status <> 'retracted' 
ORDER BY e.id DESC
LIMIT 1;

-- name: ListBlacklistByNumber :many
-- Every active entry for a number, in any scope and group.
SELECT id, scope, group_jid, pn_e164, pn_hash, reason, person_id, on_whatsapp, added_by_person, added_at
FROM app.number_blacklist
WHERE pn_e164 = @pn_e164 AND removed_at IS NULL
ORDER BY added_at, id;

-- name: ListBlacklistByPerson :many
SELECT id, scope, group_jid, pn_e164, pn_hash, reason, person_id, on_whatsapp, added_by_person, added_at
FROM app.number_blacklist
WHERE person_id = @person_id AND removed_at IS NULL
ORDER BY added_at, id;

-- name: FindGroupRule :one
SELECT id FROM app.rules
WHERE scope = 'group' AND scope_id = @scope_id AND pack = '' AND type = @type
  AND norm_pattern = @norm_pattern AND action_class = @action_class;
