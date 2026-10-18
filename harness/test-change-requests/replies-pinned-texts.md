# Test-change request: committed tests that pin reply texts

**Status:** not blocking. The `feat/clear-bot-replies` PR (clear, WhatsApp-formatted replies for
v0.1.0) keeps every pinned substring below, so all committed tests pass unchanged. Approving
this request would let a follow-up finish the formatting and the single catalogue.

## Tests and the texts they pin

| Test | Pins (exact substring) | Old text | New text it would allow |
|---|---|---|---|
| `internal/review` `TestRender`, `service_test.go:151` | `"Score: 0.97"`, `"Source: rules"` | `Score: 0.97 · Source: rules · Message: deleted ✔` | `*Score:* 0.97 · *Source:* rules · *Message:* deleted ✔` (bold labels like the other card lines) |
| `internal/api/chat` `TestSettingsErrorsAndReads`, `handler_test.go:171` | `"preset: standard"` | `preset: standard` (one summary line per key) | `*preset:* standard` |
| `internal/api/chat` `TestOnboarding`, `handler_test.go:345` | `"Protection is on in Riyadh Cars Market"` | `✅ Protection is on in Riyadh Cars Market. I stay silent there.` | `✅ Protection is on in *Riyadh Cars Market*. I stay silent there.` |
| `locales` `TestRender`, `locales_test.go:73` | keys `onboarding.protection_on` (variant 0 must contain `"{group}"` in quotes) and `command.done` (`Done.`) in `locales/*.yaml` | the yaml held a second, unused copy of every onboarding and help text | no message kept in both places |

Suggested test changes:

1. `service_test.go:151`: check `"0.97"` and `"rules"` (the values), not the label punctuation.
2. `handler_test.go:171`: check `"standard"` together with `"4 deleted"`.
3. `handler_test.go:345`: check `"Protection is on"` and `"Riyadh Cars Market"` separately.
4. `TestRender`: render from an inline fixture passed to `Parse` (as `TestParseRejectsIncompleteTranslations`
   already does) instead of two embedded keys. Then `locales/*.yaml` no longer needs to carry
   message keys that the bot never sends, and the planned move of the built-in catalogue into
   `locales/` can choose its own keys.

## Why

- DESIGN.md §22.2: "All user-facing text … lives in `locales/<lang>.yaml`." Today the bot sends
  only `internal/review/catalog*.go`; the yaml messages were unused duplicates. The PR removes
  them except the two `TestRender` pins, which conflict with the live onboarding text: `TestRender`
  wants `"{group}"` quoted in `onboarding.protection_on`, `TestOnboarding` wants the group
  unquoted, so one text can't serve both.
- The PR's task: `*bold*` labels in multi-line replies (status, settings, review cards). The three
  substrings above stop the labels or the group name from being bold.

**Confidence:** high that the tests check content, not formatting; the change keeps their intent.

## What I tried

Kept each pinned substring and bolded everything around it (headings, `*Who:*`, `*What:*`,
`*Why:*`, `*Decide:*`, `*Today:*`, numbered steps), and rewrote the two pinned yaml messages to
the new wording without a website. Every committed test passes; nothing was edited, skipped or
worked around.
