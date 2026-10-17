-- Persons, identities, merges and memberships (DESIGN.md §7.7, §8.1, §14).

-- name: LockIdentityWrites :exec
-- Serializes identity writes across processes for the rest of the transaction.
SELECT pg_advisory_xact_lock(hashtext('app.identity'));

-- name: FindOpenIdentity :one
SELECT person_id FROM app.identities
WHERE kind = @kind AND value = @value AND valid_to IS NULL;

-- name: GetPerson :one
SELECT id, lid, pn FROM app.persons WHERE id = @id AND merged_into IS NULL;

-- name: ListIdentities :many
SELECT kind, value, source, country_code, valid_to FROM app.identities
WHERE person_id = @person_id ORDER BY id;

-- name: CreatePerson :one
INSERT INTO app.persons (first_seen) VALUES (@first_seen) RETURNING id;

-- name: SetPersonIdentifiers :execrows
UPDATE app.persons
SET lid = sqlc.narg('lid'), pn = sqlc.narg('pn'), pn_country = sqlc.narg('pn_country')
WHERE id = @id AND merged_into IS NULL;

-- name: TouchOpenIdentity :one
UPDATE app.identities SET last_seen = @last_seen
WHERE kind = @kind AND value = @value AND valid_to IS NULL
RETURNING person_id;

-- name: InsertIdentity :exec
INSERT INTO app.identities (person_id, kind, value, country_code, number_prefix, source, first_seen, last_seen)
VALUES (@person_id, @kind, @value, sqlc.narg('country_code'), sqlc.narg('number_prefix'), @source, @seen, @seen);

-- name: CloseIdentity :exec
UPDATE app.identities SET valid_to = @valid_to
WHERE kind = @kind AND value = @value AND valid_to IS NULL;

-- name: RetirePerson :execrows
UPDATE app.persons SET merged_into = @into_person, lid = NULL, pn = NULL, pn_country = NULL
WHERE id = @from_person AND merged_into IS NULL;

-- name: InsertPersonMerge :exec
INSERT INTO app.person_merges (from_person, into_person, reason, at)
VALUES (@from_person, @into_person, @reason, @at);

-- name: GetMember :one
SELECT * FROM app.members WHERE group_jid = @group_jid AND person_id = @person_id;

-- name: ListMembersByPerson :many
SELECT * FROM app.members WHERE person_id = @person_id ORDER BY group_jid;

-- name: ListMembersByGroup :many
SELECT * FROM app.members WHERE group_jid = @group_jid ORDER BY person_id;

-- name: UpsertMember :exec
INSERT INTO app.members (group_jid, person_id, role, joined_at, first_seen, msg_count, clean_count,
                         allowlisted, pre_existing, established)
VALUES (@group_jid, @person_id, @role, sqlc.narg('joined_at'), @first_seen, @msg_count, @clean_count,
        @allowlisted, @pre_existing, @established)
ON CONFLICT (group_jid, person_id) DO UPDATE SET
    role = EXCLUDED.role, joined_at = EXCLUDED.joined_at, first_seen = EXCLUDED.first_seen,
    msg_count = EXCLUDED.msg_count, clean_count = EXCLUDED.clean_count,
    allowlisted = EXCLUDED.allowlisted, pre_existing = EXCLUDED.pre_existing,
    established = EXCLUDED.established;

-- name: DeleteMember :exec
DELETE FROM app.members WHERE group_jid = @group_jid AND person_id = @person_id;
