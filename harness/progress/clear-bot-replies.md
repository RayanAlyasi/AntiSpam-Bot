# Clear, WhatsApp-formatted bot replies (v0.1.0)

Branch `feat/clear-bot-replies`, PR into `staging`.

## Done

- No website in v0.1: onboarding, "protection on", `.settings`, `.login`, `.review dashboard`,
  `.review dm` and `.reviewgroup off` never mention a website, a dashboard or an empty `{url}`.
  The `_web` variants are picked only when `chat.Deps.WebsiteURL` is set.
- What really happens with no review group: `Open` stores the item and posts nothing
  (`destination` returns false); it waits until the timeout (6 h, `keep_deleted_no_ban` by
  default; no ban). `.pending` in a DM (after `.use` with several groups) lists it, and
  `.review <code> ban|ok` decides it. Covered by `TestPendingInDMWithoutReviewGroup`.
- `.help` is a short menu with *Moderation*, *Reviews*, *Settings*, *Blacklist*; `.help <topic>`
  pages in English and Arabic, and a friendly reply for an unknown topic (`help.go`).
- `*bold*` headings and labels, numbered onboarding steps, kind labels in `.pending`.
- Unused duplicate messages removed from `locales/*.yaml`.
- New tests: `internal/api/chat/replies_test.go`, `internal/review/catalog_format_test.go`.

## Follow-ups

- Move the built-in catalogue into `locales/` (DESIGN §22.2) once the test-change request
  `harness/test-change-requests/replies-pinned-texts.md` is decided.
- Command aliases exist twice: `chat.Aliases` (live) and `locales/*.yaml` `commands` (unused),
  and they differ (e.g. `.معلق` is only in code, `.المعلقة` in both). Same for the review reply
  keywords in `internal/review/votes.go` vs `locales` `keywords` (`تمام` is only in the yaml).
- Production (`cmd/antispam/chat.go`) wires no `Joiner`, `Lists`, `Members`, `History` or
  `Stats`, so invite-link joining, `.undo`, `.allow @member`, `.domain`, `.word` reply "isn't
  available" and `.status` shows "–" counts. Help and onboarding no longer advertise them; add
  them back when they're wired.
- Pre-existing: `joinFromLink` calls `unavailable` with an empty command name, so the reply is
  " isn't available on this bot yet." (onboarding.go).
- `help_unknown` echoes the topic uncapped, and names in `.pending` lines aren't escaped for
  `*`, `_` or `~`; harmless but can break formatting.
- Pre-existing: `cmd.review_dm_later` was sent with nil vars, so `{prefix}` showed literally;
  fixed here because the text was rewritten anyway.
