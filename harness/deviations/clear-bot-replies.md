# Deviations: clear, WhatsApp-formatted bot replies (v0.1.0)

1. **One catalogue, kept in `internal/review/catalog*.go`, not moved to `locales/`.** DESIGN §22.2
   puts all user-facing text in `locales/<lang>.yaml` with variants. The bot sends only the
   built-in catalogue today; the yaml messages were unused copies (`onboarding.welcome` vs
   `onboard.start`, `help.short` vs `cmd.help`, the review card lines, …). The more conservative
   option for a release-eve change was to remove the unused copies rather than rewire every reply
   to rotating variants, which would also make reply texts nondeterministic under the committed
   tests. Two yaml messages stay because `locales` `TestRender` pins them; see
   `harness/test-change-requests/replies-pinned-texts.md`. Moving the catalogue into `locales/` is
   left as a follow-up.
2. **`PUBLIC_URL` is still not passed to the chat handler.** Without a frontend, a `PUBLIC_URL` set
   for the API would make onboarding and `.login` link a page that doesn't exist. The `_web`
   variants exist and are picked when `chat.Deps.WebsiteURL` is set; wiring it waits for the
   website.
3. **"Reply to decide" from `.pending` is not offered.** The `.pending` list isn't a review message,
   so replying to it decides nothing. The texts say `.review <code> ban` or `ok` instead, which is
   what works in a DM and in a review group.
4. **`.review dashboard` without a website still sets `review.destination = dashboard`** (it stops
   posting to a review group, which the contract requires), and the reply says there's no website
   and that cases wait for `.pending`.
