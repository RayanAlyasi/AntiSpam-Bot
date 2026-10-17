# Contract WP-K: reviews, in-chat commands, learning overturns, blacklist

**Goal:** Admins run the bot entirely from WhatsApp DMs and review groups: review items with
first-wins votes and timeouts, moderation and settings commands, overturns that remove what was
learned, and a phone-number blacklist that is enforced silently. Nothing is ever posted in a
protected group, and every write goes through the paced action queue.

**Features:** F-079 to F-087, F-114 to F-116, F-118 to F-122, F-124 to F-126. Hidden tests cover the
core ones only: F-083, F-118, F-119, F-120, F-122, F-124, F-125, F-126.

## Behaviour

- **Everywhere (F-080).** Every WhatsApp write is a `review.Action` given to `Actions.Enqueue`.
  There is never a `review.Send` whose `Chat` is a protected group. Commands and votes from anyone
  who isn't a current admin of the target protected group are ignored: no actions, no reply, and
  no deletion of their message. A non-admin's review vote is stored as `valid=false, not_admin`. A
  command an admin sends in a protected group is acted on, the command message gets a `Delete`,
  and the reply is a `Send` to the admin's own JID. A command sent in a review group is answered in
  the review group. Changes are written to the audit log. The bot never acts on admins or itself.
- **F-079 DMs.** Each inbound DM opens or refreshes a thread. Notices need an open thread. A reply
  to a command may go to someone without an open thread at most once per 24 h (`Gate`).
- **F-081/F-082 settings.** `.set/.get/.settings` and the friendly commands (`.status .mode
  .preset .silent .newsignal .policy .block/.allow/.blocknew .autoban .askadmins`) write the same
  dotted keys through `chat.Settings`. `.autoban … auto ai` is refused until `TrackRecord.AIReady`.
- **F-083 moderation.** `.ban` works on a reply or an @mention (`Message.Mentions`).
  `.ban spam` enqueues `Ban{Reason: spam}`, deletes the replied message and confirms it as spam
  through the Learner. `.ban other` enqueues `Ban{Reason: other}` and learns nothing. With no reason
  or an unknown one, `Ban{Reason: other}` is enqueued at once and the admin gets a DM asking
  "Reason? reply spam or other". If the admin DMs `spam` within 10 minutes, another
  `Ban{Reason: spam}` is enqueued for the same target. A later answer changes nothing.
  `.kick` enqueues a `Kick` and never a `Ban`. `.unban <number>` enqueues `Unban`.
  `.allow/.unallow @m` update `chat.Members`. `.undo` reverses `History.LastReversible`
  (ban or kick: `Unban`; strike: trust restored) and marks it undone. When the target is an admin,
  nothing is done and the command's sender gets a DM explaining why.
- **F-084.** `.domain allow|block` and `.word block|unblock` edit `chat.Lists`. `.word block` first
  replies with `Recent.CountMatching`.
- **F-085 onboarding.** START gets the three setup steps. `OnPromoted` sends a DM only into an
  open thread; otherwise the message waits until the admin writes. `OnNotAdmin` sends at most one
  reminder per day, in an existing thread only. An invite link from a DM is joined, then the bot
  leaves if the sender isn't an admin of that group.
- **F-086/F-087/F-115 review items.** `Open` assigns a unique short code (`NewCode`, no
  look-alike characters), a deadline and a fallback. Destination `dashboard` (the default) posts
  nothing to WhatsApp. Destination `group` sends one `Send{Chat: ReviewGroup, ReviewID}`, and the
  queue later calls `RecordPosted`. `Render` masks third-party numbers and emails, masks the
  subject's number except its last two digits, and truncates text to about 300 characters.
  `.reviewgroup here` is refused in a protected group.
- **F-114 evidence.** `Learning.Snapshot` stores redacted text and the thumbnail for 30 days.
  `.spam`, a review ✅ and `.ban spam` become corpus entries through `ConfirmSpam`.
- **F-116 votes.** ✅ and 👍 mean ban. ❌ and 👎 mean not spam. Reply keywords: `ban`, `kick`,
  `طرد`, `حظر` mean ban; `ok`, `not spam`, `ليس سبام` mean not spam; `delete only`; `clear`;
  `mute`/`كتم` only with the ladder on. Removing a reaction stores a withdrawn vote, and does
  nothing once the item is decided. Other emoji are not votes. `.review K7Q2 ban` and `.pending`
  also work.
- **F-118 overturns.** `Learning.Overturn` works whether or not the message was confirmed. It
  allow-lists the fingerprint (no text), retracts any corpus entry from that message (and only
  that one), deletes the evidence text and thumbnail, removes the strike, restores trust and
  records a false positive. Afterwards no text of the message remains anywhere. Repeating it is
  harmless.
- **F-119 .spam/.notspam.** Replying `.spam` enqueues `Delete{Priority: P1}` for the quoted
  message and `Ban{Reason: spam}` for its sender (established members included), and confirms the
  spam. Replying `.notspam` or `.ok` calls `Overturn` and never bans.
- **F-120 decisions.** Only a current admin of the item's group can decide. The first valid vote
  wins atomically (`Store.Decide`), even under concurrent votes. Every vote is stored. A ban
  decision enqueues `Ban{Reason: spam, Via: "review", ReviewID}`, adds a `Delete` if the message
  is still visible, and confirms the spam. A not-spam decision calls `Overturn`. Late votes are
  stored as `late`. Each late voter gets at most one 👌 `React` on their own message and one
  "already handled by ~<name>" `Send` per review.
- **F-121.** The outcome is an `Edit` of the review message within 20 minutes, and a quoting
  follow-up `Send` after that.
- **F-122 timeouts.** The default is 6 h with `keep_deleted_no_ban`. In `ask_before_action` it is
  15 min with `delete_no_ban`. `auto_ban` falls back to the mode's default for ask modes,
  established members, possibly hacked accounts and admins. `CheckOnTimeout` returns
  `ErrAutoBanInAskMode` or `ErrUnknownFallback`. `Expire` handles each overdue item once
  (`timed_out`, method `timeout`) and posts the "No decision" note only to a group destination, as
  a `Send` or an `Edit`. `.review group|dashboard` sets `review.destination`. `.review timeout
  <dur> keep|delete|ban|dismiss` sets `review.timeout` and `review.on_timeout`. `ban` is refused,
  with an explaining reply and no change, when `enforcement.mode` is an ask mode.
- **F-124 blacklist management.** `Add` normalizes numbers to E.164, reports invalid inputs and
  numbers already listed, and records the reason and who added the entry. `Remove` reports removed,
  not found and invalid numbers. Instance scope (`--instance`) is for the operator only
  (`ErrNotOperator`). `Check` reports the group entry, the instance entry (only when the group uses
  instance entries) and whether the person is a member. Each add and remove writes an audit entry
  (`blacklist.add`, `blacklist.remove`). `.blacklist list` shows `review.MaskNumber` forms only.
- **F-125 resolution.** `UserInfo` lookups go in batches of at most `Batch` (default 50), with one
  `Budget.Wait` before each call and no call if the budget refuses. A found LID is linked with
  `Linker.Link(phone, lid)` and recorded on the entry (`ByPerson`). A number missing from the
  answer keeps its entry with `OnWhatsApp=false`.
- **F-126 enforcement.** A listed non-admin member gets `Ban{Via: "blacklist"}`, with no `Send`
  anywhere. A listed admin is never acted on, and the adder gets a `Notifier` notice. `OnJoin`
  bans. `OnJoinRequests` enqueues one `RejectJoin` with every listed requester. `OnMessage`
  enqueues `Delete{P1}`, plus a `Ban` while the sender is still a member. A person matches by LID
  or by phone. In ask-admins groups an instance entry opens a review item
  (`KindNetworkIncoming`) instead of a ban, while the group's own entries apply directly. A group
  with `UseInstance=false` ignores instance entries.

## Public surface (already stubbed; tests use exactly these)

`internal/review`: types, ports, `Service` (`NewService`, `Open`, `Vote`, `HandleReaction`,
`HandleReply`, `RecordPosted`, `Expire`, `Pending`, `ByCode`), `EffectiveTimeout`,
`CheckOnTimeout`, `Learning` (`NewLearning`, `Snapshot`, `ConfirmSpam`, `Overturn`),
`MemoryStore`, `MemoryEvidence`, `MaskNumber`. `internal/review/reviewtest` fakes.
`internal/review/blacklist`: `Service` (`Add`, `Remove`, `ForGroup`, `Check`, `OnJoin`,
`OnJoinRequests`, `OnMessage`), `Resolver`, `MemoryList`, `SimpleNormalizer`, `Entry.PhoneJID`,
plus the `blacklisttest` fakes. `internal/api/chat`: `New`, `Handle`, `Message`, `Quoted`,
`Reversible`, `NewMemoryThreads`, plus the `chattest` fakes. Also `clock.Fake` and
`wa/fake.Client`.

## Out of scope

DM reviewers, `.reviewers` and the fallback chain (F-183, v0.3); the network ban flow (§9.6); the
Postgres stores and the action-queue adapter; quiet hours and digests; the website;
libphonenumber normalization (F-123).

## DESIGN.md refs

§7.5 (overturns, learning events), §8.3 (ask-admins modes), §8.6, §9.5, §9.7, §9.8, §10.2, §13.6,
§13.7, §14 (reviews, review_messages, review_votes, number_blacklist).

## Assumptions (conservative readings)

1. "Silent" means no `Send` into a protected group, ever. Command replies go by DM, or into the
   review group when the command was sent there.
2. A non-admin's commands get no reply, and their command message is not deleted.
3. A missing or unknown `.ban` reason bans at once as `other`. A `spam` answer within 10 minutes
   adds a second `Ban{Reason: spam}`. The ban is never delayed while waiting for the answer.
4. A review decision is final as soon as it is made. Withdrawing a vote after the decision does
   nothing. `kick` and `طرد` count as ban votes.
5. The late-voter 👌 goes on the voter's own reply. Reaction votes have no message to react to.
6. `.review timeout … ban` in an ask mode changes neither the timeout nor the fallback.
7. For the `dismiss` timeout fallback, the hidden tests check only that there is no ban and no
   delete. How trust is restored is left to the builder.
8. A listed admin's message is neither deleted nor banned. `OnMessage` may still return `true`.
9. `.unban <number>` targets the person's LID when it is known, and otherwise the phone JID. Tests
   accept either.

## Acceptance tests (`acceptance/wpk`)

- `TestF083_BanRequiresReason`: spam/other/none/unknown → Ban reason, learning only for spam,
  command deleted, ask DM.
- `TestF083_BanReasonUpgradedWithinWindow`: a `spam` answer at 2 min upgrades the ban; one at
  11 min doesn't.
- `TestF083_KickIsNeverABan`: `.kick` → exactly one Kick, no Ban.
- `TestF083_UnbanAllowUnallow`: `.unban <number>` → Unban; `.allow/.unallow` toggle the
  allow-list.
- `TestF083_UndoReversesLastAction`: ban or kick undone once → Unban; marked undone.
- `TestF083_NeverActsOnAdmins`: `.ban` or `.kick` of an admin → no action; the sender gets a DM.
- `TestF083_NonAdminCommandsIgnored`: zero actions or state changes from non-admin commands.
- `TestF118_OverturnRemovesWhatWasLearned`: confirmed and unconfirmed cases → retracted,
  allow-listed, standing restored, no text left.
- `TestF118_OverturnLeavesOtherEntriesAlone`: only the overturned entry is retracted.
- `TestF118_OverturnIsIdempotent`: a repeated overturn succeeds and leaves no text.
- `TestF119_SpamReplyDeletesBansAndLearns`: P1 Delete, Ban spam and corpus entry.
- `TestF119_NotSpamOverturnsConfirmation`: `.notspam` and `.ok` after `.spam` → retract,
  allow-list, trust restored, no text left.
- `TestF119_NotSpamOverturnsBotDeletion`: an overturn of a bot deletion clears the evidence text;
  no ban.
- `TestF119_NonAdminSpamReplyIgnored`: no effect from non-admins.
- `TestF120_OnlyCurrentAdminsDecide`: votes by non-admins and by review-group-only admins are
  stored as `not_admin` and have no effect.
- `TestF120_FirstVoteWinsUnderConcurrency`: 16 concurrent votes → one decision, executed once,
  all votes stored.
- `TestF120_DecisionExecutesThroughActions`: ban → Ban via review (+Delete if visible) and
  learning; not spam → overturn; vote by code.
- `TestF120_ReactionAndReplyKeywords`: emoji and EN/AR keywords map to decisions; unknown emoji
  and non-review targets are ignored.
- `TestF120_LateVotersGetOneAck`: one 👌, one note naming the decider, nothing more; late votes
  stored.
- `TestF120_WithdrawAfterDecisionDoesNothing`: removing a reaction after the decision changes
  nothing.
- `TestF122_EffectiveTimeout`: defaults per mode and the auto_ban guards.
- `TestF122_CheckOnTimeout`: auto_ban refused in ask modes; unknown fallback rejected.
- `TestF122_ExpireAppliesFallbackOnce`: fallback actions, deadline, method timeout, note in the
  review group, runs once.
- `TestF122_DashboardPostsNothingInWhatsApp`: no Send or Edit with the dashboard destination.
- `TestF122_ReviewCommands`: destination and timeout keys set; ban refused in ask modes; non-admins
  ignored.
- `TestF124_AddNormalizesAndReportsInvalid`: E.164, invalid and already-listed numbers reported,
  audit entry.
- `TestF124_RemoveAndAudit`: removed, not found and invalid numbers reported; audit entry.
- `TestF124_InstanceScopeIsOperatorOnly`: ErrNotOperator; use_instance=false hides instance
  entries.
- `TestF124_CheckReportsMembership`: Check reports membership and E.164.
- `TestF124_BlacklistCommands`: add with reason and invalid input, masked list, `--instance` for
  the operator only, non-admins ignored.
- `TestF125_ResolvesInBatchesWithinReadBudget`: batch sizes, call count and one budget wait per
  call.
- `TestF125_LinksIdentitiesAndKeepsUnknownNumbers`: Link, ByPerson, `OnWhatsApp=false` kept,
  PhoneJID.
- `TestF125_NoLookupWithoutBudget`: a refused budget means no UserInfo call.
- `TestF126_AddBansListedMemberSilently`: Ban via blacklist; no Send.
- `TestF126_NeverActsOnAdmins`: no action on an admin; the adder is notified.
- `TestF126_JoinsRequestsAndMessages`: join → Ban, one RejectJoin, P1 Delete (+Ban for a member),
  matched by LID or by phone.
- `TestF126_ScopeAndAskMode`: group vs instance scope, ask group → review item, own entries
  direct, opt-out respected.
