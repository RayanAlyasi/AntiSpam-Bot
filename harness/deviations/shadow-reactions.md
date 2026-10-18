# Deviations: shadow-mode reactions (v0.1.0)

1. **`shadow.reactions` is off by default.** The ask was on by default. The committed test
   `TestLaneShadowGroupRecordsAndNeverActs` asserts that shadow mode makes no WhatsApp calls, so
   by the coordinator's rule the default stays off, and
   `harness/test-change-requests/shadow-reactions-default-on.md` asks a human to flip it. A
   stored document without the key counts as off, both in `ParseStored` and in the
   `GroupPosting` query.
2. **Shadow reactions get through `silent`, and nothing else does.** `silent: true` is the
   default and refuses every post (DESIGN §9.7), so without an exception the feature would never
   show anything. Only a react with `Purpose: shadow` gets through, and only while the group is
   in shadow mode with the setting on. That is checked again when the action runs, so a reaction
   left in the queue after the group switches mode or turns the setting off is refused. Other
   posts stay refused.
3. **Reactions use the 1-post-per-group-per-minute cap and a 10-minute deadline.** This is the
   conservative reading of "lowest priority, dropped when stale". In a burst, only the first ~10
   messages in a group are reacted to, and the rest expire.
4. **The emoji goes by route only.** As asked, `ask` → ❓ even when the bot would delete first
   and only ask about the ban (`ask_delete_first`, established members). `policy` → ❌ even under
   `ask_before_action`, where a violation is asked about first.
