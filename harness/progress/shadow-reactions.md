# Shadow-mode reactions (v0.1.0)

Branch `feat/shadow-reactions`, PR into `staging`. Coordinator request: let admins see shadow
mode's judgement live in the group, while it still deletes, bans and sends nothing.

## Done

- Setting `shadow.reactions` (bool, default off, `.set shadow.reactions true|false`):
  `policy.Shadow` in `internal/policy/settings.go`. Help lines in EN and AR in
  `internal/review/catalog_cmd.go` (`cmd.help.settings`).
- Pipeline (`internal/pipeline/pipeline.go`): in shadow mode with the setting on, the detection's
  transaction also enqueues one `react` at P4 with `Purpose: shadow`. The emoji is ❌ for routes
  auto_ban, delete_only and policy, and ❓ for ask; allow and none get nothing. The dedupe key is
  `actions.ShadowReactKey(group, msg)`, so an edit never reacts again.
- Queue (`internal/actions`): `PurposeShadow`, `DefaultShadowReactWindow` (10 min, the default
  deadline), `Posting.ShadowReactions`, and `Posting.rule`, which both the picker and execute use.
  A shadow react passes `silent` only while `ShadowReactions` is true, is paced by the post cap,
  and is refused otherwise.
- `GroupPosting` query and `PGGroups` (production), and the fast lane's own `posting`: shadow
  reactions are allowed when mode is shadow and `shadow.reactions` is true. No migration.
- Docs: DESIGN §8.4, §9.7, §12.1; docs/GROUP_SETTINGS.md; docs/COMMANDS.md.
- Tests: `internal/pipeline/shadowreact_test.go`, `internal/actions/shadowreact_test.go`,
  `internal/actions/shadowreact_integration_test.go`, plus the architect's hidden tests for the
  pipeline, actions and policy areas.

## Deferred

- Default on: test-change request `harness/test-change-requests/shadow-reactions-default-on.md`.

## Follow-ups

- `cmd.usage.toggle` and `.silent`-style on/off shortcuts don't cover `shadow.reactions`. Admins
  must type `true`/`false` with `.set`, because `SetText` reads `on` as a string.
- Switching a group from shadow to enforce leaves its ❌/❓ reactions on old messages. Nothing
  removes them.
- Pre-existing, not touched here: the linear-growth timing tests `TestExtractBounded`
  (`internal/normalize/urls`) and `TestTextBounded` (`internal/normalize/redact`) failed under a
  machine load average of ~35 on 10 cores, with other sessions' gates running. Both pass when run
  alone, and pass on origin/staging. They compare wall-clock ratios, so a busy host breaks them.
