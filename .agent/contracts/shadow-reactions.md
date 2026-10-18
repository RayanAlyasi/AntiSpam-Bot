# shadow-reactions: shadow mode reacts to the messages it caught

**Goal:** in shadow mode, when a group turns on `shadow.reactions`, the bot shows its judgement live
by reacting to each message it caught (❌ would act, ❓ would ask), while still deleting, banning
and posting nothing else.

**Behaviour**
- Shadow mode with `shadow.reactions` on: a caught message gets exactly one react action
  (`actions.KindReact`, priority `actions.P4`, `Payload{Target: {Sender, ID}, Emoji, Purpose:
  actions.PurposeShadow}`, `DedupeKey: actions.ShadowReactKey(group, msgID)`,
  `ReasonDetectionID` = the detection, `Bot`/`Chat` = the bot and the group), and nothing else is
  enqueued (no revoke, kick or review).
- Emoji by the decision's route: ❌ (U+274C) for `auto_ban`, `delete_only`, `policy`; ❓ (U+2753)
  for `ask`. No reaction for `allow`, `none` or a clean message.
- No reaction in enforce or paused mode, nor in shadow mode with `shadow.reactions` off.
- One reaction per message: an edit (same message ID, new content) never adds a second react
  action, even if the edit is judged differently; a redelivery adds nothing.
- Executor: in a protected `silent` group every post is refused, except a `PurposeShadow` react
  while the group's `Posting.ShadowReactions` is true. The check is made when the action runs: if
  the group left shadow mode or turned the setting off meanwhile, the react is `StatusRefused` and
  never sent. An ordinary react (no purpose) or a send stays refused even with `ShadowReactions`.
- Shadow reacts are paced by the 1-post-per-group-per-minute cap (DESIGN §10.3) and default to a
  deadline of enqueue time + `actions.DefaultShadowReactWindow` (10 min); past it they are
  `StatusExpired`, never sent late.
- Setting `shadow.reactions` (bool), default **false**; `.set shadow.reactions true|false` works
  through `policy.Settings.SetText`; a stored document without the key reads as false.

**Public surface:** `policy.Settings.Shadow` (`policy.Shadow{Reactions bool}`, json
`shadow.reactions`), `policy.Defaults`, `policy.ParseStored`, `policy.Keys`;
`actions.PurposeShadow`, `actions.Posting.ShadowReactions`, `actions.ShadowReactKey`,
`actions.DefaultShadowReactWindow`, `actions.DefaultDeadline`; existing `pipeline.New`/`Process`
with `pipeline.MemStore`, and `actions.NewExecutor` with the fake `WAClient`.

**Out of scope:** removing reactions later, reacting to review decisions, website, migrations,
how `Posting.ShadowReactions` is computed from storage (the executor only reads it).

**DESIGN.md refs:** §8.4, §9.7, §10.2, §10.3, §10.4, §12.1.

**Assumptions**
- Default false (conservative; a committed fastlane test asserts shadow mode makes no WhatsApp
  calls). Flipping it needs a test-change request.
- `allow`/`none` routes (e.g. spam from an admin) are "not caught": no reaction.
- On an edit the first reaction stands; the bot doesn't change or remove it.

**Acceptance tests**
- `acceptance/pipeline` `TestShadowReactionFollowsRoute`: emoji per route, the action's fields, and
  no reaction for clean/allow, enforce, paused, or reactions off.
- `acceptance/pipeline` `TestShadowReactionOncePerMessageAcrossEdits`: edit with a different
  judgement and a redelivery add no second reaction.
- `acceptance/actions` `TestShadowReactIsTheOnlyPostInASilentGroup`: shadow react sent; ordinary
  react, send, and shadow react without `ShadowReactions` refused.
- `acceptance/actions` `TestShadowReactRefusedOnceShadowReactionsTurnOff`: the check runs at
  execution time.
- `acceptance/actions` `TestShadowReactsPacedAndExpireAfterTenMinutes`: 1/min per group, 10 min
  default deadline, expired ones never sent.
- `acceptance/policy` `TestShadowReactionsSetting`: default off, `.set` true/false, stored doc
  without the key is off, bad value rejected.
