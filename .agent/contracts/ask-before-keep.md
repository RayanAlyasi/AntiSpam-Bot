# Contract ask-before-keep: ask_before_action keeps the message when nobody decides

**Goal:** In `enforcement.mode = ask_before_action`, the bot never deletes or bans unless an admin
says so. When a case times out, the default fallback is "leave": the message stays and nothing
happens. Maintainer decision for v0.1.0, 2026-10-18. Before this change the fallback deleted the
message without a ban.

## Behaviour

- **Defaults.** `policy.Defaults()` and every named preset have `Enforcement.AskBefore.OnTimeout ==
  policy.TimeoutLeave`. `ParseStored` of a document without `enforcement.ask_before` also gives
  leave. A stored `delete_no_ban` is read back unchanged (no rewrite when the document is read).
- **Values.** `enforcement.ask_before.on_timeout` accepts `leave`, `delete_no_ban` and `dismiss`.
  `auto_ban` and unknown values are refused by `Settings.SetText` and `Validate`.
- **Decide.** In ask_before_action the review request for spam and for content-rule violations
  (kind `ask_before`) carries OnTimeout `leave` by default, `delete_no_ban` only when the group
  chose it, and `dismiss` when chosen. Still-uncertain messages (kind `uncertain`) never get
  `delete_no_ban` (it becomes `leave`). `review.on_timeout`, even `auto_ban`, plays no part. The
  message itself is never deleted and the sender is never banned when the decision is made.
- **Review service.** `review.TimeoutLeave` ("leave") and `review.DecisionLeave` ("leave").
  `ConfigFrom` in ask_before_action maps delete_no_ban to TimeoutDeleteNoBan, dismiss to
  TimeoutDismiss, and anything else to TimeoutLeave. `EffectiveTimeout` defaults ask_before_action to
  (15 min, TimeoutLeave), and turns auto_ban into TimeoutLeave there. `CheckOnTimeout(ask_before_action,
  leave)` is nil. When a leave case expires there's no Delete, no Ban, and no strike, trust,
  allow-list or corpus change. The status is `timed_out`, the method `timeout` and the decision
  `leave`. A group destination still gets the timeout note: in EN its fallback text is
  "leave the message, no ban", and in AR it's an Arabic equivalent (never the raw key or the
  English text). Expiry happens once. An item whose stored fallback is auto_ban and whose group is
  now in ask_before_action times out as leave: no delete, no ban, decision `leave`.
- **`.review timeout <dur> <fallback>`.** In ask_before_action this writes
  `enforcement.ask_before.timeout` and `enforcement.ask_before.on_timeout` (keep→leave,
  delete→delete_no_ban, dismiss→dismiss) and leaves `review.timeout` and `review.on_timeout`
  alone. `ban` is refused with a reply and changes nothing. Other modes are unchanged (they write
  `review.*`). Nothing is ever posted in the protected group.
- **Wording (EN).** The `.status` "asking" line says "then delete" only for delete_no_ban. For leave
  and dismiss it says the message is left ("leave"). `OnPromoted` says "I delete the message" only
  for delete_no_ban, and "leave the message" for leave and dismiss. `cmd.help.reviews`,
  `cmd.help.settings` and `cmd.usage.askadmins` no longer say "I delete it" (EN) or "أحذفها بلا حظر" (AR).
- **Migration 00038.** A stored document whose `enforcement.ask_before.on_timeout` is delete_no_ban,
  and whose history never held another value, is switched to leave. This writes a new version and a
  history row with `updated_by = 'system'`, and leaves every other key untouched. Groups that ever
  chose another value, or whose value isn't delete_no_ban, are left alone.

## Public surface

`policy`: Defaults, PresetSettings, ParseStored, Settings.SetText/Validate, Decide,
TimeoutLeave/DeleteNoBan/Dismiss/AutoBan, ReviewAskBefore, ReviewUncertain. `review`: ConfigFrom,
EffectiveTimeout, CheckOnTimeout, TimeoutLeave, DecisionLeave, Service.Open/Expire, Texts.
`chat.Handler.Handle/OnPromoted`. `store.Migrate`, `migrations.SQL/Go` (goose `UpTo(37)` in the test).

## Out of scope

`review.on_timeout` defaults for auto and ask_delete_first. A website editor. Rewriting documents
when they're read. Deciding what a history row without `enforcement.ask_before` means.

## DESIGN.md refs

§8.3 (b), §9.5 Timeouts, §12.1, §13.7.

## Assumptions

- A dismiss timeout in ask_before_action restores trust and deletes nothing, as `review.on_timeout`
  dismiss does.
- `CheckOnTimeout` must accept leave in ask_before_action. Whether leave is valid in other modes
  isn't pinned.

## Acceptance tests

policy: `TestAskBeforeKeep_DefaultIsLeave` (Defaults and all presets) ·
`TestAskBeforeKeep_ParseStoredMissingAskBefore` (missing key gives leave; delete_no_ban is kept) ·
`TestAskBeforeKeep_OnTimeoutValues` (three accepted, auto_ban and unknown refused) ·
`TestAskBeforeKeep_DecideFallback` (spam/violation/uncertain × leave/delete_no_ban/dismiss).
policy, integration: `TestAskBeforeKeep_Migration00038` (switch only never-changed delete_no_ban; system version + history).
wpk: `TestAskBeforeKeep_ConfigFrom` · `TestAskBeforeKeep_EffectiveTimeout` ·
`TestAskBeforeKeep_ExpireFallbacks` (leave/delete_no_ban/dismiss effects, note text, once) ·
`TestAskBeforeKeep_TimeoutNoteArabic` · `TestAskBeforeKeep_AutoBanDowngradeLeaves` ·
`TestAskBeforeKeep_ReviewTimeoutCommand` · `TestAskBeforeKeep_Wording` · `TestAskBeforeKeep_HelpTexts`.
