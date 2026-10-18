# F-069/F-074: markNotAdmin cleared BotIsAdmin but left the group `active`

Symptom: judge round 1 FAIL (major) on cmd/antispam/actions.go markNotAdmin. After a 403 the group stayed `active`, so `Tracker.GroupState` and inbox routing kept treating it as moderated. Detections kept enqueuing admin-only actions that each cost a failing write, until the next 6 h refresh.

Root cause: the new code set one field (`BotIsAdmin=false`). Other paths that clear admin (tracker.go statusFor, demotion handler) also set `Status=GroupPendingAdmin`, and "moderated" is derived from Status only. The unit test asserted only BotIsAdmin.

Evidence: .agent/logs/judge-F-064.md "WP-H wiring" round 1 and round 2 (probe marknotadmin_scratch_test.go: `status "active" after a not-admin error, want "pending_admin"`). Fixed in round 2 (left rows skipped; TestMarkNotAdminStopsModeration). Accepted nit: read-then-upsert without a transaction can overwrite a concurrent promotion until the next refresh.

Prevention: when a new path changes part of a state that other code already changes, copy the sibling path's full set of writes, and assert the derived behaviour (GroupState) in the test, not the raw field.
