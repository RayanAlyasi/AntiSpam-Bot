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

## Second pass (coordinator's requirements)

- Nothing unshipped: help, onboarding, usage lines and docs/COMMANDS.md list only commands that
  work with the production wiring. `.undo`, `.allow @member`, `.domain`, `.word` and invite-link
  joins answer a short "isn't available in this version" naming the command. `.login`,
  `.review dm` and `.review dashboard` say plainly what this version does instead. No text says
  "yet", "later" or "for now" (checked by `TestCatalogFormatting`).
- `.status` without the stats port shows the cases waiting (from the review service) instead of
  "–" counts.
- Usage hints are one example per line with what it does, not `a|b|c`.
- New test: `internal/api/chat/unshipped_test.go`, with the production wiring.

## Follow-ups

- Group names go raw into `*{group}*` (protection on, `cmd.saved`, `cmd.using`, …): a name with
  `*` leaves a stray asterisk, and `Notify` doesn't fall back to the group ID when the name lookup
  fails. One helper that strips `*`, `_` and `~` from names would cover these and `.pending`.
- locales/README.md says aliases and keywords are checked at startup; nothing loads the
  `locales` package at runtime (the live ones are `chat.Aliases` and `replyKeywords`).
- docs/CONFIGURATION.md still describes `PUBLIC_URL` and `HTTP_ADDR` in terms of the website.
- The Arabic "not available" reply names the canonical English command (`.undo` for `.تراجع`).
- `cmd.autoban_ai_refused` mentions a track record, but production has no track-record source,
  so `ai` is always refused for now; reword when F-112's store is wired.

- Move the built-in catalogue into `locales/` (DESIGN §22.2). The test-change request was
  approved; card labels, setting keys and the group name are now bold, and `messages` in
  `locales/*.yaml` is empty.
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
