# Posting in a review group that was once protected (v0.1.0 fix)

Branch `feat/review-group-posting`, PR into `staging`. Live bug on staging: after the bot was
demoted in a group it had protected and an admin sent `.reviewgroup here` there, every post to
that group (the ✔ confirmation, review cards) was refused as silent, because its old settings
document was checked before the review-group rule.

## Done

- `PGGroups.Posting` checks `IsReviewGroup` before `GroupPosting`.
- `IsReviewGroup` ignores the group's own settings row (a group can't be its own review group)
  and doesn't apply while `app.groups.status` is `active`. `make gen` regenerated the query.
- Docs: docs/action-queue.md. Deviations: `harness/deviations/review-group-posting.md`.
- Tests: `internal/actions/pggroups_test.go` (check order, fake DBTX) and
  `internal/actions/pggroups_review_integration_test.go` (Postgres: demoted, active, left,
  self-named).

## Follow-ups

- Pre-existing, outside this change: `reviewGroupHere` (`internal/api/chat/reviews.go`) accepts
  the current chat as its own target, so a pending_admin group can be set as its own review
  group. The queue now refuses posts there, but the setting is still written. It should refuse
  with `cmd.reviewgroup_protected` when the target is the chat itself. Left alone because the
  ask-before session owns `internal/api/chat`.
