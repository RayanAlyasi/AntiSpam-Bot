---
paths:
  - "internal/wa/**"
---
# whatsmeow

- [WA-001] Look APIs up with `go doc go.mau.fi/whatsmeow[.<Symbol>]` (see `/whatsmeow-api`). The library changes often; don't rely on memory.
- [WA-002] Event handlers run on the receive loop: insert the raw event into the inbox, ack, and return. All analysis happens elsewhere (DESIGN.md §3.7, §5.1).
- [WA-003] `UnwrapRaw()` doesn't unwrap FutureProof wrappers (GroupMentioned, Spoiler, StatusMention, GroupStatus…). The recursive unwrapper handles them, with a depth cap (DESIGN.md §5.2).
- [WA-004] Delete others' messages with `SendMessage(chat, cli.BuildRevoke(chat, sender, id))`. `RevokeMessage` is deprecated.
- [WA-005] Key people by LID; add the phone number when it's learned. A phone number isn't always available (DESIGN.md §7.7).
- [WA-006] The bot can edit its own messages for only about 20 minutes; after that, post a follow-up reply instead.
- [WA-007] One process per bot number. A second connection causes `StreamReplaced`.
- [WA-008] Every write (send, revoke, kick, edit) goes through the action queue. Never call write APIs directly.
