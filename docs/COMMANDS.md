# In-chat commands, reviews and the blacklist

Admins control the bot from WhatsApp. The bot never posts in a protected group: commands
sent there are acted on, the command message is deleted, and the reply arrives by DM. In a
review group the reply stays in the review group. Anyone who isn't a current admin of the group
is ignored. The full design is in [DESIGN.md](DESIGN.md) §9.5, §9.7, §9.8, §13.6 and §13.7.

## Where commands work

| Where | Which group the command acts on |
|---|---|
| The protected group | That group. |
| A DM with the bot | The group picked with `.use <name>` (remembered for an hour), or your only group. `.groups` lists them. |
| A review group | The group named in the review you reply to, the only group it serves, or the one picked with `.use`. |

The bot DMs only people who have messaged it first. The one exception: a reply to a command you
just sent in a protected group, at most once a day, until you write to the bot.

The prefix is `.`. Arabic aliases work too, and the reply is then in Arabic: `.حالة` (status),
`.حظر` (ban), `.طرد` (kick), `.سبام` (spam), `.ليس_سبام` (notspam), `.تراجع` (undo),
`.مراجعة` (review), `.معلق` (pending), `.القائمة_السوداء` (blacklist), `.استخدم` (use),
`.مجموعات` (groups), `.اعدادات` (settings).

## Help

`.help` (`.مساعدة`) sends a short menu grouped under Moderation, Reviews, Settings and Blacklist,
one command per line. `.help moderation`, `.help reviews`, `.help settings` and `.help blacklist`
(in Arabic `.مساعدة الإشراف`, `المراجعات`, `الإعدادات`, `القائمة_السوداء`) send a page with
examples for that part. v0.1 has no website, so `.login` says so, and no reply mentions a
website or a link.

Replies use WhatsApp formatting: `*bold*` headings and labels, line breaks and numbered steps.
All bot texts are in `internal/review/catalog.go` and `internal/review/catalog_cmd.go`, in
English and Arabic. Texts that link the website have a `_web` variant, sent only when the chat
handler is given a website URL (`chat.Deps.WebsiteURL`), which v0.1 doesn't do.

## Moderation

| Command | Effect |
|---|---|
| reply `.spam` | Deletes the message, bans the sender (even an established member) and remembers it as confirmed spam. |
| reply `.notspam` or `.ok` | Restores the member's trust and removes the strike. It allow-lists the message's fingerprint, records the false positive and retracts anything learned from the message. No text of the message is kept. |
| `.ban @member spam\|other` or reply `.ban spam\|other` | Local ban: kick plus rejoin block. Only `spam` feeds learning. Without a reason the ban still happens at once as `other`, and the bot asks; answering `spam` by DM within 10 minutes upgrades it. |
| `.kick @member [spam\|other]` | Removes the member without a rejoin block. |
| `.unban <number>` or reply | Lifts the ban. The bot never re-adds anyone. |
| `.allow @member` / `.unallow @member` | Member allow-list. |
| `.undo` | Reverses the bot's last reversible action in the group (unban, or trust restored). |

The bot never acts on group admins or on itself.

## Settings

`.set <key> <value>`, `.get <key>` and `.settings` work on any key from
[GROUP_SETTINGS.md](GROUP_SETTINGS.md), with the same validation as the website. The friendly
commands write the same keys:

| Command | Key |
|---|---|
| `.status` | shows preset, mode, review destination and today's counts |
| `.mode shadow\|enforce\|pause` | `mode` |
| `.preset relaxed\|standard\|strict\|ask` | `preset` (resets the auto-ban matrix) |
| `.silent on\|off`, `.newsignal on\|off`, `.ladder on\|off` | `silent`, `new_members.just_joined.enabled`, `ladder.enabled` |
| `.policy delete\|ask\|ban` | `policy.action` |
| `.block videos`, `.allow stickers`, `.blocknew links` | `content.<type>` |
| `.autoban` / `.autoban <type> auto\|ask\|delete\|allow [known\|known,ai]` | `enforcement.autoban_matrix.<type>.*`. `ai` is refused until AI evidence has a track record. |
| `.askadmins off\|delete\|before` | `enforcement.mode` |
| `.domain allow\|block example.com` | the group's domain lists |
| `.word block\|unblock <phrase>` | the group's blocked words. `.word block` first reports how many recent messages the phrase would have matched; send the same command again within 10 minutes to save it. |

Every change is written to the audit log.

## Reviews

When the bot isn't allowed or isn't sure enough to ban, it opens a review with a short code such as
`#K7Q2`.

- **Where they go.** By default the bot posts reviews nowhere: they wait until an admin sends
  `.pending` (in a DM, after `.use <group>` if you admin several groups, or in the review group)
  and decides with `.review <code> ban|ok|delete only`. The website that will also show them
  isn't in v0.1. Send `.reviewgroup here` in a separate admins' group (never the protected group
  itself) to receive each review there as it comes in. Add the group's name when you admin
  several groups. The bot warns when that group contains people who aren't admins of the
  protected group, because they'd see the evidence. `.reviewgroup off` stops posting there.
  `.review group|dashboard` switches between posting to the review group and waiting for
  `.pending`; without a website the bot says so. Reviews by DM come in a later release.
- **Deciding.** React ✅ or 👍 to ban, ❌ or 👎 for not spam. Or reply `ban`, `kick`, `طرد` or
  `حظر` (all mean ban), `ok`, `not spam` or `ليس سبام`, `delete only`, or `clear` for a hacked
  account. `.review K7Q2 ban` and `.pending` work in a DM or the review group. Only current
  admins of the protected group count. The first valid decision wins, and later voters get a 👌
  and an "already handled" note. The review message is edited to show the outcome within
  20 minutes; after that a follow-up reply is posted.
- **Timeouts.** With no decision within `review.timeout` (6 h; 15 min in ask-before-action mode)
  the fallback applies: `keep_deleted_no_ban`, `delete_no_ban`, `auto_ban` or `dismiss`. Set it
  with `.review timeout 6h keep|delete|ban|dismiss`. A timeout never bans in ask-admins modes,
  nor an established member, a possibly hacked account or an admin. The workers role checks
  timeouts every minute.
- **Evidence.** Every deletion keeps a redacted snapshot (text and thumbnail) for 30 days; the
  workers role purges older ones hourly. A "not spam" decision deletes the snapshot's text at once.

## Phone-number blacklist

| Command | Effect |
|---|---|
| `.blacklist add <number> [<number> …] [reason]` | Lists the numbers in any common format (`+966 55 123 4567`, `0551234567`, `00966…`, Arabic digits, `wa.me/…`). Invalid numbers are reported back. |
| `.blacklist remove <number> …` | Removes them and lifts their rejoin block. |
| `.blacklist list` | Shows this group's entries, masked except the last two digits. |
| `.blacklist check <number>` | Says whether the number is listed here or instance-wide, and whether it's a member. |

The operator can add instance-wide entries with `--instance`. Listed members who are in the group
when the number is added are banned silently through the paced queue, and a listed number in a
message (text, contact card, `wa.me` link) counts as known spam. The bot never acts on group
admins; it tells whoever added the number instead. Nothing happens in groups in shadow or paused
mode. In ask-admins groups, instance-wide entries become review items, while the group's own
entries apply directly.

The list keeps working after the add: their messages are deleted and they're banned, a listed
person who joins later is removed, and their join requests are rejected in groups that approve
new members (checked every 5 minutes). The list wins over an admin re-adding a listed person: to
let them back, `.blacklist remove` the number. Removing joiners and rejecting join requests need
the connector and workers roles in the same process, which is the default; with them split, use
`.ban` on someone who came back.

## Getting started by DM

Send the bot `start` for the three setup steps. When you make the bot an admin, it tells you by
DM, but only once you've messaged it; until then it waits. You can also send it a group invite
link: it joins only if you're an admin of that group, and at most five groups a day.
